// Package system backs up TaktaPlus itself and stages signed upgrade
// bundles for the updater, with the shared sysadmin implementation.
package system

import (
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/farshadfighter/taktaplus-lic/pkg/keys"
	"github.com/farshadfighter/taktaplus-lic/pkg/release"
	"github.com/farshadfighter/taktaplus-lic/pkg/sysadmin"

	"github.com/farshadfighter/taktaplus/internal/buildinfo"
	"github.com/farshadfighter/taktaplus/internal/settings"
)

//go:embed all:keys
var embeddedKeys embed.FS

// TrustedKeys returns the release signing keys built into the binary.
func TrustedKeys() (keys.Keyring, error) {
	ring := keys.Keyring{}
	entries, err := fs.ReadDir(embeddedKeys, "keys")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pub") {
			continue
		}
		data, err := fs.ReadFile(embeddedKeys, "keys/"+e.Name())
		if err != nil {
			return nil, err
		}
		part, err := keys.ParseKeyring(string(data))
		if err != nil {
			return nil, fmt.Errorf("system: %s: %w", e.Name(), err)
		}
		for kid, pub := range part {
			if err := ring.Add(kid, pub); err != nil {
				return nil, err
			}
		}
	}
	return ring, nil
}

// Data directory entries used by backups and upgrades.
const (
	BackupDir  = sysadmin.BackupDir
	UpgradeDir = sysadmin.UpgradeDir
)

// Excluded are the data directory entries a backup leaves out.
var Excluded = sysadmin.Excluded

// Types and errors of the shared implementation.
type (
	Service    = sysadmin.Service
	Settings   = sysadmin.Settings
	InputError = sysadmin.InputError
	Run        = sysadmin.Run
	Upgrade    = sysadmin.Upgrade
)

// Errors of the shared implementation.
var (
	ErrBusy     = sysadmin.ErrBusy
	ErrNotFound = sysadmin.ErrNotFound
)

// Options configure the service.
type Options struct {
	DataDir     string
	DatabaseURL string
	MasterKey   string // TAKTAPLUS_MASTER_KEY, stored inside backups
	PgDump      string
}

// New returns the service for TaktaPlus.
func New(db *pgxpool.Pool, st *settings.Store, ring keys.Keyring, opt Options, log *slog.Logger) *Service {
	return sysadmin.New(db, st, ring, sysadmin.Options{
		Product: release.ProductApp,
		Build:   sysadmin.Build{Version: buildinfo.Version, Commit: buildinfo.Commit, Date: buildinfo.Date},
		DataDir: opt.DataDir, DatabaseURL: opt.DatabaseURL, MasterKey: opt.MasterKey, PgDump: opt.PgDump,
	}, log)
}
