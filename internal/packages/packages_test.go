package packages

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pin/tftp/v3"

	"github.com/farshadfighter/taktaplus-lic/pkg/keys"
	"github.com/farshadfighter/taktaplus-lic/pkg/licformat"
	"github.com/farshadfighter/taktaplus-lic/pkg/manifest"
	"github.com/farshadfighter/taktaplus-lic/pkg/repoauth"

	"github.com/farshadfighter/taktaplus/internal/license"
	"github.com/farshadfighter/taktaplus/internal/license/licensetest"
	"github.com/farshadfighter/taktaplus/internal/secretbox"
	"github.com/farshadfighter/taktaplus/internal/testdb"
)

type repoFixture struct {
	files map[string]string // name -> content
	body  []byte
	sig   string
	pub   ed25519.PublicKey
}

func shaOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// newRepoFixture signs a manifest for the given files with a fresh key.
func newRepoFixture(t *testing.T, files map[string]string) *repoFixture {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	m := manifest.Manifest{Format: manifest.FormatVersion, PublishedAt: time.Now().UTC().Truncate(time.Second)}
	for name, content := range files {
		typ, ver, plat := manifest.TypeIPSDB, "28.950", licformat.PlatformFortiGate
		var up []string
		if strings.HasSuffix(name, ".out") {
			typ, ver, up = manifest.TypeFirmware, "7.4.4", []string{"7.2"}
		}
		m.Packages = append(m.Packages, manifest.Package{File: name, SHA256: shaOf(content), Size: int64(len(content)), Platform: plat,
			Type: typ, Version: ver, Models: []string{"*"}, ReleasedAt: m.PublishedAt, UpgradeFrom: up})
	}
	body, _ := json.Marshal(m)
	sig, err := manifest.Sign(body, "pkg-test", priv)
	if err != nil {
		t.Fatal(err)
	}
	return &repoFixture{files: files, body: body, sig: sig, pub: pub}
}

func newService(t *testing.T, ring keys.Keyring, lic *license.Manager) *Service {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secretbox.New(key)
	return New(testdb.New(t), box, t.TempDir(), ring, lic)
}

func TestGuess(t *testing.T) {
	cases := map[string][3]string{
		"FGT_100F-v7-build2662-FORTINET.out": {"fortigate", "firmware", "7.0.2662"},
		"ips-db-28.00950.pkg":                {"fortigate", "ips_db", "28.00950"},
		"vsigupdate-OS7.4.0_92.01234.ETDB":   {"fortigate", "av_db", "92.01234"},
		"isdb-7.03944.pkg":                   {"fortigate", "isdb", "7.03944"},
		"FWB-waf-signature-0.00371.pkg":      {"fortiweb", "waf_signature", "0.00371"},
	}
	for name, want := range cases {
		g := Guess(name)
		if g.Platform != want[0] || g.Type != want[1] || g.Version != want[2] {
			t.Errorf("Guess(%s) = %s %s %s", name, g.Platform, g.Type, g.Version)
		}
	}
}

func TestCentralSync(t *testing.T) {
	ctx := context.Background()
	fx := newRepoFixture(t, map[string]string{"fgt-ips-28.950.pkg": "IPS DB", "FGT_100F-v7.4.4.out": "FIRMWARE"})

	guard := licensetest.Guard(t, map[string]licformat.Date{"FG1": licformat.NewDate(time.Now().Year()+1, 1, 1)})
	lic, _ := guard.License()
	instPub := guard.Identity().Public()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := repoauth.Parse(r.Header.Get("Authorization"))
		if err != nil || c.LicenseID != lic.LicenseID || c.Verify(instPub, r.Method, r.URL.Path, time.Now()) != nil {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"not entitled"}`)
			return
		}
		switch {
		case r.URL.Path == "/api/v1/repo/manifest":
			_, _ = w.Write(fx.body)
		case r.URL.Path == "/api/v1/repo/manifest.sig":
			_, _ = io.WriteString(w, fx.sig)
		case strings.HasPrefix(r.URL.Path, "/api/v1/repo/files/"):
			sha := strings.TrimPrefix(r.URL.Path, "/api/v1/repo/files/")
			for _, content := range fx.files {
				if shaOf(content) == sha {
					_, _ = io.WriteString(w, content)
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	db := testdb.New(t)
	mgr := license.NewManager(guard, db, slog.New(slog.NewTextHandler(io.Discard, nil)), srv.URL, t.TempDir(), nil)
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secretbox.New(key)
	s := New(db, box, t.TempDir(), keys.Keyring{"pkg-test": fx.pub}, mgr)

	id, err := s.SaveSource(ctx, Source{Name: "Central", Kind: KindCentral, Enabled: true, Schedule: "0 * * * *"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.Sync(ctx, id, "test")
	if err != nil || run.Status != "ok" || run.Added != 2 {
		t.Fatalf("sync = %+v %v", run, err)
	}
	list, _ := s.List(ctx, Filter{})
	if len(list) != 2 || !list[0].Verified {
		t.Fatalf("list = %+v", list)
	}
	fw, err := s.Latest(ctx, "fortigate", "firmware", "FGT100F", "7.2.8")
	if err != nil || fw.Version != "7.4.4" || !fw.Manifest().UpgradesFrom("7.2.8") {
		t.Fatalf("latest firmware = %+v %v", fw, err)
	}
	f, err := s.Open(fw.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(f)
	f.Close()
	if string(data) != "FIRMWARE" {
		t.Fatal("content mismatch")
	}
	// Second sync downloads nothing; verified packages cannot be edited.
	run, _ = s.Sync(ctx, id, "test")
	if run.Added != 0 || run.Seen != 2 {
		t.Fatalf("resync = %+v", run)
	}
	var ie InputError
	if err := s.Update(ctx, fw.ID, Meta{Platform: "fortigate", Type: "firmware", Version: "7.4.5"}); !errors.As(err, &ie) {
		t.Fatalf("edit verified = %v", err)
	}

	// A wrong trusted key fails the sync.
	s.ring = keys.Keyring{"pkg-test": ed25519.PublicKey(make([]byte, 32))}
	run, _ = s.Sync(ctx, id, "test")
	if run.Status != "failed" || !strings.Contains(run.Error, "signature") {
		t.Fatalf("bad key sync = %+v", run)
	}
	srcs, _ := s.ListSources(ctx)
	if srcs[0].LastStatus != "failed" || srcs[0].NextSyncAt == nil {
		t.Fatalf("source = %+v", srcs[0])
	}
}

func TestHTTPMirrorAndBundle(t *testing.T) {
	ctx := context.Background()
	fx := newRepoFixture(t, map[string]string{"a.pkg": "AAA", "b.pkg": "BBB"})
	mux := http.NewServeMux()
	mux.HandleFunc("/mirror/manifest.json", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(fx.body) })
	mux.HandleFunc("/mirror/manifest.sig", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, fx.sig) })
	mux.HandleFunc("/mirror/files/{name}", func(w http.ResponseWriter, r *http.Request) {
		if c, ok := fx.files[r.PathValue("name")]; ok {
			_, _ = io.WriteString(w, c)
			return
		}
		w.WriteHeader(404)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	s := newService(t, keys.Keyring{"pkg-test": fx.pub}, nil)
	id, err := s.SaveSource(ctx, Source{Name: "Mirror", Kind: KindHTTP, Enabled: true, Config: SourceConfig{URL: srv.URL + "/mirror/"}})
	if err != nil {
		t.Fatal(err)
	}
	if run, err := s.Sync(ctx, id, "t"); err != nil || run.Status != "ok" || run.Added != 2 {
		t.Fatalf("mirror sync = %+v %v", run, err)
	}

	// Bundle with only one of the files, signed with a second trusted key.
	fx2 := newRepoFixture(t, map[string]string{"c.pkg": "CCC", "d.pkg": "DDD"})
	s.ring["pkg-2"] = fx2.pub
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name, content string) { w, _ := zw.Create(name); _, _ = io.WriteString(w, content) }
	add("manifest.json", string(fx2.body))
	add("manifest.sig", strings.Replace(fx2.sig, ".pkg-test.", ".pkg-2.", 1))
	add("files/c.pkg", "CCC")
	_ = zw.Close()
	run, err := s.ImportBundle(ctx, bytes.NewReader(buf.Bytes()), int64(buf.Len()), "t")
	if err != nil || run.Added != 1 || run.Status != "ok" {
		t.Fatalf("bundle = %+v %v", run, err)
	}
	if list, _ := s.List(ctx, Filter{}); len(list) != 3 {
		t.Fatalf("after bundle = %d", len(list))
	}
	var ie InputError
	if _, err := s.ImportBundle(ctx, bytes.NewReader([]byte("nope")), 4, "t"); !errors.As(err, &ie) {
		t.Fatalf("bad zip = %v", err)
	}
}

func TestTFTPSourceAndUpload(t *testing.T) {
	ctx := context.Background()
	content := map[string]string{"ips-db-28.00950.pkg": "IPS FROM TFTP"}
	srv := tftp.NewServer(func(name string, rf io.ReaderFrom) error {
		c, ok := content[name]
		if !ok {
			return errors.New("not found")
		}
		_, err := rf.ReadFrom(strings.NewReader(c))
		return err
	}, nil)
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(conn.(*net.UDPConn)) }()
	defer srv.Shutdown()
	port := conn.LocalAddr().(*net.UDPAddr).Port

	s := newService(t, keys.Keyring{}, nil)
	if _, err := s.SaveSource(ctx, Source{Name: "t", Kind: KindTFTP, Config: SourceConfig{Host: "127.0.0.1"}}); err == nil {
		t.Fatal("TFTP without files accepted")
	}
	id, err := s.SaveSource(ctx, Source{Name: "Customer TFTP", Kind: KindTFTP, Enabled: true, Config: SourceConfig{Host: "127.0.0.1", Port: port, Files: []string{"ips-db-28.00950.pkg"}}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.Sync(ctx, id, "t")
	if err != nil || run.Status != "ok" || run.Added != 1 {
		t.Fatalf("tftp sync = %+v %v", run, err)
	}
	list, _ := s.List(ctx, Filter{})
	if len(list) != 1 || list[0].Verified || list[0].Type != "ips_db" || list[0].Version != "28.00950" {
		t.Fatalf("tftp package = %+v", list[0])
	}
	// Admins correct unverified descriptions; a later sync keeps them.
	if err := s.Update(ctx, list[0].ID, Meta{Platform: "fortigate", Type: "ips_db", Version: "28.951", Models: []string{"FG100F"}}); err != nil {
		t.Fatal(err)
	}
	if run, _ := s.Sync(ctx, id, "t"); run.Added != 0 {
		t.Fatalf("resync = %+v", run)
	}
	p, _ := s.Get(ctx, list[0].ID)
	if p.Version != "28.951" || !p.Compatible("fortigate", "FortiGate-100F", "7.4.4") || p.Compatible("fortigate", "FG60F", "") {
		t.Fatalf("after edit = %+v", p)
	}

	up, err := s.Upload(ctx, strings.NewReader("FW"), "FGT_60F-v7-build2662-FORTINET.out", nil)
	if err != nil || up.Type != "firmware" || up.Origin != "upload" {
		t.Fatalf("upload = %+v %v", up, err)
	}
	if err := s.Delete(ctx, up.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(up.SHA256); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted file = %v", err)
	}
}
