package logs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/oschwald/maxminddb-golang"
)

// GeoPackageType is the repository package type that carries the offline
// country database (an MMDB file such as DB-IP Lite Country).
const GeoPackageType = "geoip_mmdb"

// GeoInfo describes the installed country database.
type GeoInfo struct {
	Loaded    bool       `json:"loaded"`
	Type      string     `json:"type"`
	BuiltAt   *time.Time `json:"built_at"`
	Source    string     `json:"source"` // upload or the repository package version
	UpdatedAt *time.Time `json:"updated_at"`
}

// GeoIP looks up countries for addresses the device left without one.
type GeoIP struct {
	dir string

	mu     sync.RWMutex
	db     *maxminddb.Reader
	info   GeoInfo
	repoSH string
}

// NewGeoIP loads DIR/country.mmdb if it exists.
func NewGeoIP(dir string) *GeoIP {
	g := &GeoIP{dir: dir}
	_ = g.load()
	return g
}

func (g *GeoIP) path() string { return filepath.Join(g.dir, "country.mmdb") }

func (g *GeoIP) load() error {
	db, err := maxminddb.Open(g.path())
	if err != nil {
		return err
	}
	src, _ := os.ReadFile(g.path() + ".source")
	st, _ := os.Stat(g.path())
	built := time.Unix(int64(db.Metadata.BuildEpoch), 0).UTC()
	info := GeoInfo{Loaded: true, Type: db.Metadata.DatabaseType, BuiltAt: &built, Source: string(src)}
	if st != nil {
		mt := st.ModTime().UTC()
		info.UpdatedAt = &mt
	}
	sha, _ := os.ReadFile(g.path() + ".sha")
	g.mu.Lock()
	old := g.db
	g.db, g.info, g.repoSH = db, info, string(sha)
	g.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	return nil
}

// Info describes the installed database.
func (g *GeoIP) Info() GeoInfo {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.info
}

// Install validates an MMDB file and makes it the active database.
func (g *GeoIP) Install(r io.Reader, source, sha string) error {
	if err := os.MkdirAll(g.dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(g.dir, "upload-*.mmdb")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, io.LimitReader(r, 512<<20)); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	db, err := maxminddb.Open(tmp.Name())
	if err != nil {
		return errors.New("the file is not a valid MMDB database")
	}
	var probe countryRecord
	err = db.Lookup(netip.MustParseAddr("8.8.8.8").AsSlice(), &probe)
	db.Close()
	if err != nil {
		return fmt.Errorf("the database cannot be read as a country database: %w", err)
	}
	if err := os.Rename(tmp.Name(), g.path()); err != nil {
		return err
	}
	_ = os.WriteFile(g.path()+".source", []byte(source), 0o640)
	_ = os.WriteFile(g.path()+".sha", []byte(sha), 0o640)
	return g.load()
}

// RepoPackage is the newest country database in the package repository.
type RepoPackage struct {
	SHA256  string
	Version string
	Open    func() (io.ReadCloser, error)
}

// SyncFromRepo installs the repository's database when it differs from
// the installed one.
func (g *GeoIP) SyncFromRepo(ctx context.Context, latest func(ctx context.Context) (*RepoPackage, error)) error {
	p, err := latest(ctx)
	if err != nil || p == nil {
		return err
	}
	g.mu.RLock()
	same := g.repoSH == p.SHA256
	g.mu.RUnlock()
	if same {
		return nil
	}
	rc, err := p.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	return g.Install(rc, "repository "+p.Version, p.SHA256)
}

type countryRecord struct {
	Country struct {
		ISOCode string            `maxminddb:"iso_code"`
		Names   map[string]string `maxminddb:"names"`
	} `maxminddb:"country"`
}

// Country returns the English country name for a public address, or "".
func (g *GeoIP) Country(ip string) string {
	if g == nil {
		return ""
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsMulticast() || addr.IsUnspecified() {
		return ""
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.db == nil {
		return ""
	}
	var rec countryRecord
	if err := g.db.Lookup(addr.Unmap().AsSlice(), &rec); err != nil {
		return ""
	}
	if n := rec.Country.Names["en"]; n != "" {
		return n
	}
	return rec.Country.ISOCode
}

// fill sets missing country columns from the database. Tables without a
// country column ignore the extra key on insert.
func (g *GeoIP) fill(row map[string]any) {
	for _, pair := range [][2]string{{"srcip", "srccountry"}, {"dstip", "dstcountry"}} {
		ip, _ := row[pair[0]].(string)
		if c, _ := row[pair[1]].(string); ip == "" || (c != "" && c != "Reserved") {
			continue
		}
		if c := g.Country(ip); c != "" {
			row[pair[1]] = c
		}
	}
}
