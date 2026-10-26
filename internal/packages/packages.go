// Package packages keeps the local package repository: signature packages
// and firmware images synced from sources or uploaded, addressed by SHA-256.
package packages

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/farshadfighter/taktaplus-lic/pkg/keys"
	"github.com/farshadfighter/taktaplus-lic/pkg/licformat"
	"github.com/farshadfighter/taktaplus-lic/pkg/manifest"

	"github.com/farshadfighter/taktaplus/internal/license"
	"github.com/farshadfighter/taktaplus/internal/secretbox"
)

//go:embed all:keys
var embeddedKeys embed.FS

// TrustedKeys returns the manifest signing keys built into the binary.
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
			return nil, fmt.Errorf("packages: %s: %w", e.Name(), err)
		}
		for kid, pub := range part {
			if err := ring.Add(kid, pub); err != nil {
				return nil, err
			}
		}
	}
	return ring, nil
}

// MaxFileSize bounds a stored file.
const MaxFileSize = 4 << 30

var (
	// ErrNotFound means the package or source does not exist.
	ErrNotFound = errors.New("not found")
)

// InputError is a validation failure to show to the user.
type InputError struct{ msg string }

func (e InputError) Error() string { return e.msg }

func invalidf(format string, a ...any) error { return InputError{fmt.Sprintf(format, a...)} }

// Package is a file in the local repository.
type Package struct {
	ID          int64     `json:"id"`
	SHA256      string    `json:"sha256"`
	File        string    `json:"file"`
	Size        int64     `json:"size"`
	Platform    string    `json:"platform"`
	Type        string    `json:"type"`
	Version     string    `json:"version"`
	Models      []string  `json:"models"`
	FwMin       string    `json:"fw_min"`
	FwMax       string    `json:"fw_max"`
	UpgradeFrom []string  `json:"upgrade_from"`
	Notes       string    `json:"notes"`
	ReleasedAt  time.Time `json:"released_at"`
	SourceID    *int64    `json:"source_id"`
	Origin      string    `json:"origin"`
	Verified    bool      `json:"verified"`
	Withdrawn   bool      `json:"withdrawn"`
	CreatedAt   time.Time `json:"created_at"`
}

// Manifest converts p for compatibility checks.
func (p *Package) Manifest() manifest.Package {
	return manifest.Package{
		File: p.File, SHA256: p.SHA256, Size: p.Size, Platform: licformat.Platform(p.Platform), Type: manifest.PackageType(p.Type),
		Version: p.Version, Models: p.Models, Firmware: manifest.FirmwareRange{Min: p.FwMin, Max: p.FwMax},
		ReleasedAt: p.ReleasedAt, UpgradeFrom: p.UpgradeFrom, Notes: p.Notes,
	}
}

// Compatible reports whether p can be installed on a device. Model names
// are compared without the FG/FGT and FWB/FV prefixes differences.
func (p *Package) Compatible(platform, model, firmware string) bool {
	if p.Platform != platform {
		return false
	}
	ok := false
	for _, m := range p.Models {
		if m == "*" || normModel(m) == normModel(model) {
			ok = true
			break
		}
	}
	if !ok {
		return false
	}
	if firmware != "" && p.FwMin != "" && manifest.CompareVersions(firmware, p.FwMin) < 0 {
		return false
	}
	if firmware != "" && p.FwMax != "" && manifest.CompareVersions(firmware, p.FwMax) > 0 {
		return false
	}
	return true
}

var modelPrefix = regexp.MustCompile(`^(FORTIGATE|FORTIWEB|FGT|FG|FWB|FV|FWEB)[-_ ]?`)

// normModel maps FGT_100F, FG100F and FortiGate-100F to 100F.
func normModel(m string) string {
	return modelPrefix.ReplaceAllString(strings.ToUpper(strings.TrimSpace(m)), "")
}

// Service manages the repository and its sources.
type Service struct {
	db    *pgxpool.Pool
	box   *secretbox.Box
	dir   string
	ring  keys.Keyring
	lic   *license.Manager
	now   func() time.Time
	fetch fetcherFactory
}

// New returns the service. Files are kept in dir; ring holds the trusted
// manifest keys; lic provides the installation identity and license for
// the central repository.
func New(db *pgxpool.Pool, box *secretbox.Box, dir string, ring keys.Keyring, lic *license.Manager) *Service {
	return &Service{db: db, box: box, dir: dir, ring: ring, lic: lic, now: time.Now, fetch: defaultFetcher}
}

// Path returns where a stored file lives.
func (s *Service) Path(sha string) string { return filepath.Join(s.dir, sha[:2], sha) }

// Open returns a stored file.
func (s *Service) Open(sha string) (*os.File, error) {
	if len(sha) != 64 {
		return nil, ErrNotFound
	}
	f, err := os.Open(s.Path(sha))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return f, err
}

// OpenFile opens a stored file with its size, for the file server.
func (s *Service) OpenFile(sha string) (io.ReadSeekCloser, int64, error) {
	f, err := s.Open(sha)
	if err != nil {
		return nil, 0, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, fi.Size(), nil
}

// store copies r into the repository and returns its hash and size. When
// wantSHA is set the content must match it.
func (s *Service) store(r io.Reader, wantSHA string) (string, int64, error) {
	if err := os.MkdirAll(filepath.Join(s.dir, "tmp"), 0o700); err != nil {
		return "", 0, err
	}
	tmp, err := os.CreateTemp(filepath.Join(s.dir, "tmp"), "in-*")
	if err != nil {
		return "", 0, err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, MaxFileSize+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", 0, err
	}
	if n > MaxFileSize {
		return "", 0, invalidf("file is larger than %d GB", MaxFileSize>>30)
	}
	if n == 0 {
		return "", 0, invalidf("file is empty")
	}
	sha := hex.EncodeToString(h.Sum(nil))
	if wantSHA != "" && sha != wantSHA {
		return "", 0, fmt.Errorf("checksum mismatch: expected %s, got %s", wantSHA, sha)
	}
	dst := s.Path(sha)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return "", 0, err
	}
	return sha, n, os.Rename(tmp.Name(), dst)
}

func (s *Service) has(ctx context.Context, sha string) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM packages WHERE sha256 = $1)`, sha).Scan(&ok)
	if ok {
		if _, statErr := os.Stat(s.Path(sha)); statErr != nil {
			return false, nil // row without file: fetch again
		}
	}
	return ok, err
}

// upsert records a stored file. A verified description (from a signed
// manifest) replaces whatever is stored; an unverified one is only written
// for new files, so corrections made in the panel survive syncs.
func (s *Service) upsert(ctx context.Context, p *Package) error {
	if p.Models == nil {
		p.Models = []string{"*"}
	}
	if p.UpgradeFrom == nil {
		p.UpgradeFrom = []string{}
	}
	onConflict := `UPDATE SET withdrawn = false`
	if p.Verified {
		onConflict = `UPDATE SET file = EXCLUDED.file, platform = EXCLUDED.platform, type = EXCLUDED.type, version = EXCLUDED.version,
		  models = EXCLUDED.models, fw_min = EXCLUDED.fw_min, fw_max = EXCLUDED.fw_max, upgrade_from = EXCLUDED.upgrade_from,
		  notes = EXCLUDED.notes, released_at = EXCLUDED.released_at, source_id = EXCLUDED.source_id, origin = EXCLUDED.origin,
		  verified = true, withdrawn = false`
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO packages (sha256, file, size, platform, type, version, models, fw_min, fw_max, upgrade_from, notes, released_at, source_id, origin, verified)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		ON CONFLICT (sha256) DO `+onConflict,
		p.SHA256, p.File, p.Size, p.Platform, p.Type, p.Version, p.Models, p.FwMin, p.FwMax, p.UpgradeFrom, p.Notes, p.ReleasedAt,
		p.SourceID, p.Origin, p.Verified)
	return err
}

const pkgCols = `id, sha256, file, size, platform, type, version, models, fw_min, fw_max, upgrade_from, notes, released_at, source_id, origin, verified, withdrawn, created_at`

// Filter narrows List.
type Filter struct {
	Platform string
	Type     string
	All      bool // include withdrawn
}

// List returns packages, newest release first.
func (s *Service) List(ctx context.Context, f Filter) ([]*Package, error) {
	rows, err := s.db.Query(ctx, `SELECT `+pkgCols+` FROM packages
		WHERE ($1 = '' OR platform = $1) AND ($2 = '' OR type = $2) AND ($3 OR NOT withdrawn)
		ORDER BY released_at DESC, id DESC`, f.Platform, f.Type, f.All)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToAddrOfStructByPos[Package])
}

// Get returns one package.
func (s *Service) Get(ctx context.Context, id int64) (*Package, error) {
	rows, err := s.db.Query(ctx, `SELECT `+pkgCols+` FROM packages WHERE id = $1`, id)
	if err != nil {
		return nil, err
	}
	p, err := pgx.CollectExactlyOneRow(rows, pgx.RowToAddrOfStructByPos[Package])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// Meta is the editable description of an unverified package.
type Meta struct {
	Platform    string    `json:"platform"`
	Type        string    `json:"type"`
	Version     string    `json:"version"`
	Models      []string  `json:"models"`
	FwMin       string    `json:"fw_min"`
	FwMax       string    `json:"fw_max"`
	UpgradeFrom []string  `json:"upgrade_from"`
	Notes       string    `json:"notes"`
	ReleasedAt  time.Time `json:"released_at"`
}

func clean(list []string) []string {
	out := []string{}
	for _, v := range list {
		if v = strings.TrimSpace(v); v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

func (m *Meta) validate(file string) error {
	m.Models, m.UpgradeFrom = clean(m.Models), clean(m.UpgradeFrom)
	if len(m.Models) == 0 {
		m.Models = []string{"*"}
	}
	if m.ReleasedAt.IsZero() {
		m.ReleasedAt = time.Now()
	}
	if m.Type != "firmware" {
		m.UpgradeFrom = []string{}
	}
	mf := manifest.Manifest{Format: manifest.FormatVersion, PublishedAt: time.Now(), Packages: []manifest.Package{{
		File: "x", SHA256: strings.Repeat("0", 64), Size: 1, Platform: licformat.Platform(m.Platform), Type: manifest.PackageType(m.Type),
		Version: m.Version, Models: m.Models, Firmware: manifest.FirmwareRange{Min: m.FwMin, Max: m.FwMax}, UpgradeFrom: m.UpgradeFrom,
	}}}
	if err := mf.Validate(); err != nil {
		return invalidf("%s", strings.ReplaceAll(strings.TrimPrefix(err.Error(), "manifest: invalid: "), "packages[0]: ", ""))
	}
	return nil
}

// Update changes the description of a package that no signed manifest
// describes (customer sources and single uploads).
func (s *Service) Update(ctx context.Context, id int64, m Meta) error {
	p, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if p.Verified {
		return invalidf("this package is described by a signed manifest and cannot be edited")
	}
	if err := m.validate(p.File); err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `UPDATE packages SET platform = $2, type = $3, version = $4, models = $5, fw_min = $6, fw_max = $7,
		upgrade_from = $8, notes = $9, released_at = $10 WHERE id = $1`,
		id, m.Platform, m.Type, m.Version, m.Models, m.FwMin, m.FwMax, m.UpgradeFrom, m.Notes, m.ReleasedAt)
	return err
}

// Delete removes a package and its file.
func (s *Service) Delete(ctx context.Context, id int64) error {
	p, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM packages WHERE id = $1`, id); err != nil {
		return err
	}
	if err := os.Remove(s.Path(p.SHA256)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Upload stores a single file. Its description is guessed from the name
// and m, when given, overrides the guess.
func (s *Service) Upload(ctx context.Context, r io.Reader, file string, m *Meta) (*Package, error) {
	file = filepath.Base(strings.TrimSpace(file))
	if file == "" || file == "." {
		return nil, invalidf("file name is required")
	}
	guess := Guess(file)
	if m == nil {
		m = &guess
	}
	if err := m.validate(file); err != nil {
		return nil, err
	}
	sha, size, err := s.store(r, "")
	if err != nil {
		return nil, err
	}
	p := &Package{SHA256: sha, File: file, Size: size, Platform: m.Platform, Type: m.Type, Version: m.Version, Models: m.Models,
		FwMin: m.FwMin, FwMax: m.FwMax, UpgradeFrom: m.UpgradeFrom, Notes: m.Notes, ReleasedAt: m.ReleasedAt, Origin: "upload"}
	if err := s.upsert(ctx, p); err != nil {
		return nil, err
	}
	var id int64
	if err := s.db.QueryRow(ctx, `SELECT id FROM packages WHERE sha256 = $1`, sha).Scan(&id); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// Latest returns the newest non-withdrawn package of each platform/type that
// is compatible with a device, for "install the latest" deployments.
func (s *Service) Latest(ctx context.Context, platform, typ, model, firmware string) (*Package, error) {
	list, err := s.List(ctx, Filter{Platform: platform, Type: typ})
	if err != nil {
		return nil, err
	}
	var best *Package
	for _, p := range list {
		if !p.Compatible(platform, model, firmware) {
			continue
		}
		if best == nil || manifest.CompareVersions(p.Version, best.Version) > 0 {
			best = p
		}
	}
	if best == nil {
		return nil, ErrNotFound
	}
	return best, nil
}
