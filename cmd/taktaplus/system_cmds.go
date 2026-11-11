package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/farshadfighter/taktaplus-lic/pkg/release"
	"github.com/farshadfighter/taktaplus-lic/pkg/selfbackup"
	"github.com/farshadfighter/taktaplus-lic/pkg/sysadmin"

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
	h, mk, err := sysadmin.RestoreFile(ctx, *file, pass, release.ProductApp, buildinfo.Version,
		selfbackup.Options{DatabaseURL: cfg.DatabaseURL, PgRestore: cfg.PgRestore, DataDir: cfg.DataDir})
	if err != nil {
		return err
	}
	if len(mk) > 0 && *keyOut != "" {
		if err := os.WriteFile(*keyOut, mk, 0o600); err != nil {
			return err
		}
	}
	fmt.Printf("restored the backup of %s (version %s, %s)\n", h.Hostname, h.Version, h.CreatedAt.Format("2006-01-02 15:04 MST"))
	if string(mk) != os.Getenv("TAKTAPLUS_MASTER_KEY") {
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
	ring, err := system.TrustedKeys()
	if err != nil {
		return err
	}
	m, err := sysadmin.VerifyBundleDir(os.Args[2], ring)
	if err != nil {
		return err
	}
	fmt.Printf("valid: %s %s, %d files\n", m.Product, m.Version, len(m.Files))
	return nil
}

// runUpgradeSnapshot dumps the database before an upgrade so a failed
// upgrade can return to it.
func runUpgradeSnapshot(ctx context.Context, cfg config.Config, _ *slog.Logger) error {
	p, err := sysadmin.Snapshot(ctx, cfg.PgDump, cfg.DatabaseURL, cfg.DataDir)
	if err != nil {
		return err
	}
	fmt.Println(p)
	return nil
}

// runUpgradeRollback restores the database dumped by upgrade-snapshot.
func runUpgradeRollback(ctx context.Context, cfg config.Config, _ *slog.Logger) error {
	p, err := sysadmin.Rollback(ctx, cfg.PgRestore, cfg.DatabaseURL, cfg.DataDir)
	if err != nil {
		return err
	}
	fmt.Println("database restored from", p)
	return nil
}
