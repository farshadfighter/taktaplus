package system

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/farshadfighter/taktaplus-lic/pkg/keys"
	"github.com/farshadfighter/taktaplus-lic/pkg/release"
	"github.com/farshadfighter/taktaplus-lic/pkg/selfbackup"

	"github.com/farshadfighter/taktaplus/internal/secretbox"
	"github.com/farshadfighter/taktaplus/internal/settings"
	"github.com/farshadfighter/taktaplus/internal/testdb"
)

func newService(t *testing.T, ring keys.Keyring) (*Service, string) {
	t.Helper()
	db := testdb.New(t)
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secretbox.New(key)
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "backups"), 0o700)
	_ = os.WriteFile(filepath.Join(dir, "installation.key"), []byte("ID"), 0o600)
	svc := New(db, settings.New(db, box), ring, Options{DataDir: dir, DatabaseURL: os.Getenv("TAKTAPLUS_TEST_DATABASE_URL"), MasterKey: "MK"},
		slog.New(slog.DiscardHandler))
	return svc, dir
}

func TestBackup(t *testing.T) {
	if _, err := exec.LookPath("pg_dump"); err != nil {
		t.Skip("pg_dump not installed")
	}
	svc, dir := newService(t, nil)
	ctx := context.Background()
	if err := svc.SaveSettings(ctx, Settings{Enabled: true, Cron: "0 3 * * *", Keep: 2}); err == nil {
		t.Fatal("enabled scheduled backups without a passphrase")
	}
	if _, err := svc.Start(ctx, "manual", "admin"); err == nil {
		t.Fatal("backup without a passphrase")
	}
	if err := svc.SetPassphrase(ctx, "short"); err == nil {
		t.Fatal("short passphrase accepted")
	}
	if err := svc.SetPassphrase(ctx, "a long backup passphrase"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveSettings(ctx, Settings{Enabled: true, Cron: "bad", Keep: 2}); err == nil {
		t.Fatal("bad cron accepted")
	}
	if err := svc.SaveSettings(ctx, Settings{Enabled: true, Cron: "0 3 * * *", Keep: 2,
		SFTPEnabled: true, SFTP: selfbackup.SFTP{Host: "sftp.example", User: "u", Dir: "/b", Password: "pw"}}); err != nil {
		t.Fatal(err)
	}
	v, _ := svc.Settings(ctx)
	if !v.KeySet || !v.HasPassword || v.SFTP.Password != "" {
		t.Fatalf("view %+v", v)
	}
	// SFTP off for the run itself; an empty password keeps the stored one.
	if err := svc.SaveSettings(ctx, Settings{Enabled: true, Cron: "0 3 * * *", Keep: 2, SFTP: selfbackup.SFTP{Host: "sftp.example", User: "u", Dir: "/b"}}); err != nil {
		t.Fatal(err)
	}
	if st, _ := svc.load(ctx); st.SFTP.Password != "pw" {
		t.Fatal("stored SFTP password was dropped")
	}
	r, err := svc.Backup(ctx, "manual", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "ok" || !r.Available || r.Size == 0 {
		t.Fatalf("run %+v", r)
	}
	f, err := svc.File(r.File)
	if err != nil {
		t.Fatal(err)
	}
	h, tr, err := selfbackup.Decrypt(f, "a long backup passphrase")
	if err != nil || h.Product != release.ProductApp {
		t.Fatal(h, err)
	}
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(tr)
	f.Close()
	if !bytes.Contains(buf.Bytes(), []byte("data/installation.key")) || !bytes.Contains(buf.Bytes(), []byte("extra/master.key")) {
		t.Fatal("backup misses data files or the master key")
	}
	if _, err := svc.File("../installation.key"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("path traversal = %v", err)
	}
	_ = dir
}

func TestStage(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	ring := keys.Keyring{}
	_ = ring.Add("rel-2026-01", pub)
	svc, dir := newService(t, ring)
	ctx := context.Background()
	src := t.TempDir()
	_ = os.WriteFile(filepath.Join(src, "docker-compose.yml"), []byte("services: {}\n"), 0o644)
	var b bytes.Buffer
	if _, err := release.Build(&b, src, release.Manifest{Product: release.ProductApp, Version: "1.1.0", Notes: "notes"}, "rel-2026-01", priv); err != nil {
		t.Fatal(err)
	}
	u, staged, err := svc.Stage(ctx, bytes.NewReader(b.Bytes()), "admin", true)
	if err != nil {
		t.Fatal(err)
	}
	if u.Version != "1.1.0" || u.Status != "queued" {
		t.Fatalf("upgrade %+v", u)
	}
	if _, err := os.Stat(filepath.Join(staged, "docker-compose.yml")); err != nil {
		t.Fatal(err)
	}
	req, _ := os.ReadFile(filepath.Join(dir, UpgradeDir, "request.json"))
	if !strings.Contains(string(req), `"dir":"upgrade/staged-`) {
		t.Fatalf("request %s", req)
	}
	// A second one waits for the first.
	if _, _, err := svc.Stage(ctx, bytes.NewReader(b.Bytes()), "admin", true); err == nil {
		t.Fatal("queued two upgrades")
	}
	// Status from the updater.
	_ = os.WriteFile(filepath.Join(dir, UpgradeDir, "status-"+itoa(u.ID)+".json"), []byte(`{"id":`+itoa(u.ID)+`,"state":"running","step":"load","message":"loading images"}`), 0o600)
	_ = os.WriteFile(filepath.Join(dir, UpgradeDir, "upgrade-"+itoa(u.ID)+".log"), []byte("docker load\n"), 0o600)
	list, err := svc.Upgrades(ctx)
	if err != nil || len(list) != 1 || list[0].Status != "running" || list[0].Step != "load" || list[0].Log != "docker load\n" {
		t.Fatalf("upgrades %+v %v", list, err)
	}
	// Cancel only while queued.
	if err := svc.Cancel(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.Cancel(ctx, u.ID); err == nil {
		t.Fatal("cancelled twice")
	}
	// Another signer is refused.
	_, priv2, _ := ed25519.GenerateKey(rand.Reader)
	b.Reset()
	_, _ = release.Build(&b, src, release.Manifest{Product: release.ProductApp, Version: "1.1.0"}, "rel-2026-01", priv2)
	if _, _, err := svc.Stage(ctx, bytes.NewReader(b.Bytes()), "admin", true); err == nil {
		t.Fatal("accepted a bundle from another key")
	}
	// No keys built in: refused.
	svc.ring = nil
	if _, _, err := svc.Stage(ctx, bytes.NewReader(b.Bytes()), "admin", true); err == nil {
		t.Fatal("accepted a bundle without trusted keys")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
