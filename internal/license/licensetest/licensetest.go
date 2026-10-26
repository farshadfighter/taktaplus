// Package licensetest builds licensed guards for tests of other packages.
package licensetest

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/farshadfighter/taktaplus-lic/pkg/keys"
	"github.com/farshadfighter/taktaplus-lic/pkg/licformat"

	"github.com/farshadfighter/taktaplus/internal/license"
)

// Guard returns a guard with a license covering the given serials, each
// expiring on the paired date (grace period 14 days).
func Guard(t testing.TB, devices map[string]licformat.Date) *license.Guard {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	id, err := license.LoadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	g := license.NewGuard(keys.Keyring{"lic-test": pub}, id)
	reqToken, err := g.ActivationRequest("TPK-TEST", licformat.Fingerprint{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	req, err := licformat.ParseRequest(reqToken)
	if err != nil {
		t.Fatal(err)
	}
	latest := licformat.NewDate(time.Now().Year()+1, 1, 1)
	lic := &licformat.License{
		Format: licformat.FormatVersion, KeyID: "lic-test", LicenseID: "LIC-TEST",
		Customer:       licformat.Customer{ID: "C-1", Name: "Test"},
		InstallationID: req.InstallationID, InstallationPubKey: req.InstallationPubKey,
		IssuedAt: time.Now().UTC().Truncate(time.Second), GraceDays: 14,
	}
	for serial, exp := range devices {
		lic.Devices = append(lic.Devices, licformat.DeviceItem{Serial: serial, Platform: platform(serial), Expires: exp})
		if exp.EndOfDay().After(latest.EndOfDay()) {
			latest = exp
		}
	}
	lic.NotAfter = latest.EndOfDay().AddDate(0, 0, 14)
	token, err := lic.Sign(priv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Install(token); err != nil {
		t.Fatal(err)
	}
	return g
}

func platform(serial string) licformat.Platform {
	if strings.HasPrefix(serial, "FV") || strings.HasPrefix(serial, "FWB") {
		return licformat.PlatformFortiWeb
	}
	return licformat.PlatformFortiGate
}
