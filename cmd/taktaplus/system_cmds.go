package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/farshadfighter/taktaplus-lic/pkg/manifest"
	"github.com/farshadfighter/taktaplus-lic/pkg/release"
	"github.com/farshadfighter/taktaplus-lic/pkg/selfbackup"

	"github.com/farshadfighter/taktaplus/internal/buildinfo"
	"github.com/farshadfighter/taktaplus/internal/config"
	"github.com/farshadfighter/taktaplus/internal/settings"
	"github.com/farshadfighter/taktaplus/internal/store"
	"github.com/farshadfighter/taktaplus/internal/system"
)

func systemService(ctx context.Context, cfg config.Config, log *slog.Logger) (*system.Service, func(), error) {
	pool, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, nil, err
	}
	if err := store.Migrate(ctx, pool, log); err != nil {
		pool.Close()
		return nil, nil, err
	}
	ring, err := system.TrustedKeys()
	if err != nil {
		pool.Close()
		return nil, nil, err
	}
	svc := system.New(pool, settings.New(pool, cfg.MasterKey), ring, system.Options{
		DataDir: cfg.DataDir, DatabaseURL: cfg.DatabaseURL, MasterKey: os.Getenv("TAKTAPLUS_MASTER_KEY"), PgDump: cfg.PgDump,
	}, log)
	return svc, pool.Close, nil
}

// runSystemBackup takes an encrypted backup of TaktaPlus with the stored key.
func runSystemBackup(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	fs := flag.NewFlagSet("system-backup", flag.ContinueOnError)
	trigger := fs.String("trigger", "manual", "manual or pre_upgrade")
	if err := fs.Parse(os.Args[2:]); err != nil {
		return err
	}
	svc, done, err := systemService(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer done()
	r, err := svc.Backup(ctx, *trigger, "cli")
	if err != nil {
		return err
	}
	fmt.Printf("backup: %s (%d bytes)\n", filepath.Join(cfg.DataDir, system.BackupDir, r.File), r.Size)
	if r.SFTPStatus == "failed" {
		fmt.Fprintln(os.Stderr, "warning:", r.Error)
	}
	return nil
}

// runRestoreBackup restores the database and data directory from a backup.
// The passphrase comes from $TAKTAPLUS_BACKUP_PASSPHRASE or stdin. The
// backed-up master key is written to -master-key-out so the operator (or
// restore.sh) can put it into the environment.
func runRestoreBackup(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	fs := flag.NewFlagSet("restore-backup", flag.ContinueOnError)
	file := fs.String("file", "", "backup file (.tpbak, required)")
	keyOut := fs.String("master-key-out", "", "write the backed-up master key to this file")
	if err := fs.Parse(os.Args[2:]); err != nil {
		return err
	}
	if *file == "" {
		return errors.New("-file is required")
	}
	pass := os.Getenv("TAKTAPLUS_BACKUP_PASSPHRASE")
	if pass == "" {
		fmt.Fprint(os.Stderr, "backup passphrase: ")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return err
		}
		pass = strings.TrimRight(line, "\r\n")
	}
	f, err := os.Open(*file)
	if err != nil {
		return err
	}
	defer f.Close()
	h, r, err := selfbackup.Decrypt(f, pass)
	if err != nil {
		return err
	}
	if h.Product != release.ProductApp {
		return fmt.Errorf("this is a backup of %s, not TaktaPlus", h.Product)
	}
	if buildinfo.Version != "dev" && manifest.CompareVersions(h.Version, buildinfo.Version) > 0 {
		return fmt.Errorf("the backup was made by version %s; install that version or newer before restoring", h.Version)
	}
	log.Info("restoring", "version", h.Version, "created_at", h.CreatedAt, "host", h.Hostname)
	extra, err := selfbackup.Restore(ctx, r, selfbackup.Options{DatabaseURL: cfg.DatabaseURL, PgRestore: cfg.PgRestore,
		DataDir: cfg.DataDir, Exclude: system.Excluded})
	if err != nil {
		return err
	}
	if mk := extra["master.key"]; len(mk) > 0 && *keyOut != "" {
		if err := os.WriteFile(*keyOut, mk, 0o600); err != nil {
			return err
		}
	}
	fmt.Printf("restored the backup of %s (version %s, %s)\n", h.Hostname, h.Version, h.CreatedAt.Format("2006-01-02 15:04 MST"))
	if string(extra["master.key"]) != os.Getenv("TAKTAPLUS_MASTER_KEY") {
		fmt.Println("the backup was made with another master key: set TAKTAPLUS_MASTER_KEY to the restored one before starting TaktaPlus")
	}
	return nil
}

// runStageUpgrade verifies a bundle (from -file or stdin) and extracts it
// for the updater; it prints the staged directory.
func runStageUpgrade(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	fs := flag.NewFlagSet("stage-upgrade", flag.ContinueOnError)
	file := fs.String("file", "", "bundle (default stdin)")
	noQueue := fs.Bool("no-queue", false, "only verify and extract; do not queue for the updater")
	if err := fs.Parse(os.Args[2:]); err != nil {
		return err
	}
	var in io.Reader = os.Stdin
	if *file != "" {
		f, err := os.Open(*file)
		if err != nil {
			return err
		}
		defer f.Close()
		in = f
	}
	svc, done, err := systemService(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer done()
	u, dir, err := svc.Stage(ctx, bufio.NewReaderSize(in, 1<<20), "cli", !*noQueue)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "verified %s %s (installed %s)\n", release.ProductApp, u.Version, buildinfo.Version)
	rel, err := filepath.Rel(cfg.DataDir, dir)
	if err != nil {
		return err
	}
	// "<id> <version> <directory relative to the data directory>" for upgrade.sh
	fmt.Printf("%d %s %s\n", u.ID, u.Version, filepath.ToSlash(rel))
	return nil
}

// runVerifyBundleDir checks an extracted bundle directory against its signed
// manifest (install.sh).
func runVerifyBundleDir(_ context.Context, _ config.Config, _ *slog.Logger) error {
	if len(os.Args) != 3 {
		return errors.New("usage: taktaplus verify-bundle-dir DIR")
	}
	dir := os.Args[2]
	ring, err := system.TrustedKeys()
	if err != nil {
		return err
	}
	if len(ring) == 0 {
		return errors.New("this build has no release signing key")
	}
	mj, err := os.ReadFile(filepath.Join(dir, release.ManifestName))
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(filepath.Join(dir, release.SignatureName))
	if err != nil {
		return err
	}
	m, err := release.Verify(mj, string(sig), ring)
	if err != nil {
		return err
	}
	for _, f := range m.Files {
		fh, err := os.Open(filepath.Join(dir, filepath.FromSlash(f.Path)))
		if err != nil {
			return err
		}
		h := sha256.New()
		n, err := io.Copy(h, fh)
		fh.Close()
		if err != nil {
			return err
		}
		if n != f.Size || hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
			return fmt.Errorf("%s is damaged (size or hash mismatch)", f.Path)
		}
	}
	fmt.Printf("valid: %s %s, %d files\n", m.Product, m.Version, len(m.Files))
	return nil
}

func rollbackDump(cfg config.Config) string {
	return filepath.Join(cfg.DataDir, system.UpgradeDir, "rollback.dump")
}

// runUpgradeSnapshot dumps the database before an upgrade so a failed
// upgrade can return to it.
func runUpgradeSnapshot(ctx context.Context, cfg config.Config, _ *slog.Logger) error {
	p := rollbackDump(cfg)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(p+".tmp", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, cfg.PgDump, "--format=custom", "--no-owner", "--no-privileges", "--dbname="+cfg.DatabaseURL)
	cmd.Stdout, cmd.Stderr = f, &stderr
	err = cmd.Run()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(p + ".tmp")
		return fmt.Errorf("pg_dump: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	if err := os.Rename(p+".tmp", p); err != nil {
		return err
	}
	fmt.Println(p)
	return nil
}

// runUpgradeRollback restores the database dumped by upgrade-snapshot.
func runUpgradeRollback(ctx context.Context, cfg config.Config, _ *slog.Logger) error {
	p := rollbackDump(cfg)
	if _, err := os.Stat(p); err != nil {
		return fmt.Errorf("no rollback snapshot: %w", err)
	}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, cfg.PgRestore, "--clean", "--if-exists", "--no-owner", "--no-privileges", "--single-transaction", "--dbname="+cfg.DatabaseURL, p)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pg_restore: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	fmt.Println("database restored from", p)
	return nil
}
