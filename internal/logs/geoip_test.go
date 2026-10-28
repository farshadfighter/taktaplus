package logs

import (
	"bytes"
	"context"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"
)

func testMMDB(t *testing.T, country string) []byte {
	t.Helper()
	w, err := mmdbwriter.New(mmdbwriter.Options{DatabaseType: "DBIP-Country-Lite", IPVersion: 6})
	if err != nil {
		t.Fatal(err)
	}
	_, nw, _ := net.ParseCIDR("8.8.0.0/16")
	rec := mmdbtype.Map{"country": mmdbtype.Map{"iso_code": mmdbtype.String("US"), "names": mmdbtype.Map{"en": mmdbtype.String(country)}}}
	if err := w.Insert(nw, rec); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := w.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestGeoIP(t *testing.T) {
	dir := t.TempDir()
	g := NewGeoIP(dir)
	if g.Info().Loaded || g.Country("8.8.8.8") != "" {
		t.Fatal("database loaded from an empty directory")
	}
	if err := g.Install(strings.NewReader("not a database"), "upload", ""); err == nil {
		t.Fatal("junk accepted")
	}
	if err := g.Install(bytes.NewReader(testMMDB(t, "United States")), "upload", ""); err != nil {
		t.Fatal(err)
	}
	if g.Country("8.8.8.8") != "United States" || g.Country("10.0.0.1") != "" || g.Country("9.9.9.9") != "" {
		t.Fatal("lookups wrong")
	}
	row := map[string]any{"srcip": "10.0.0.1", "dstip": "8.8.4.4", "dstcountry": "Reserved"}
	g.fill(row)
	if row["dstcountry"] != "United States" || row["srccountry"] != nil {
		t.Fatalf("fill = %v", row)
	}
	// The repository's database replaces the upload once, by checksum.
	opened := 0
	latest := func(context.Context) (*RepoPackage, error) {
		return &RepoPackage{SHA256: "abc", Version: "2026.10", Open: func() (io.ReadCloser, error) {
			opened++
			return io.NopCloser(bytes.NewReader(testMMDB(t, "USA"))), nil
		}}, nil
	}
	for range 2 {
		if err := g.SyncFromRepo(context.Background(), latest); err != nil {
			t.Fatal(err)
		}
	}
	if opened != 1 || g.Country("8.8.8.8") != "USA" || g.Info().Source != "repository 2026.10" {
		t.Fatalf("repo sync: opened %d, info %+v", opened, g.Info())
	}
	// A restart loads the stored database and remembers its checksum.
	g2 := NewGeoIP(dir)
	if err := g2.SyncFromRepo(context.Background(), latest); err != nil || opened != 1 || g2.Country("8.8.8.8") != "USA" {
		t.Fatalf("reload: opened %d", opened)
	}
}
