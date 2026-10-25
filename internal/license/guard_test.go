package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/farshadfighter/taktaplus-lic/pkg/keys"
	"github.com/farshadfighter/taktaplus-lic/pkg/licformat"
)

func newIdentity(t *testing.T) Identity {
	t.Helper()
	id, err := LoadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// issue plays the license server: it signs a license for the installation
// that produced the request.
func issue(t *testing.T, priv ed25519.PrivateKey, reqToken string, issuedAt time.Time, expires licformat.Date) string {
	t.Helper()
	req, err := licformat.ParseRequest(reqToken)
	if err != nil {
		t.Fatal(err)
	}
	lic := &licformat.License{
		Format:             licformat.FormatVersion,
		KeyID:              "lic-test",
		LicenseID:          "LIC-1",
		Customer:           licformat.Customer{ID: "C-1", Name: "Test"},
		InstallationID:     req.InstallationID,
		InstallationPubKey: req.InstallationPubKey,
		IssuedAt:           issuedAt,
		NotAfter:           expires.EndOfDay().AddDate(0, 0, 14),
		GraceDays:          14,
		Devices:            []licformat.DeviceItem{{Serial: "FGT60FTK0001", Platform: licformat.PlatformFortiGate, Expires: expires}},
		OTP:                []licformat.OTPItem{{Users: 20, Expires: expires}},
	}
	token, err := lic.Sign(priv)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestActivationFlow(t *testing.T) {
	authPub, authPriv, _ := ed25519.GenerateKey(rand.Reader)
	g := NewGuard(keys.Keyring{"lic-test": authPub}, newIdentity(t))
	g.now = func() time.Time { return time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC) }

	if got := g.DeviceStatus("FGT60FTK0001"); got != licformat.StatusUnlicensed {
		t.Fatalf("before install: %s", got)
	}
	req, err := g.ActivationRequest("TPK-TEST", CollectFingerprint("/"), nil)
	if err != nil {
		t.Fatal(err)
	}
	token := issue(t, authPriv, req, g.now(), licformat.NewDate(2027, 10, 31))
	if _, err := g.Install(token); err != nil {
		t.Fatal(err)
	}
	if got := g.DeviceStatus("fgt60ftk0001"); got != licformat.StatusActive {
		t.Fatalf("after install: %s", got)
	}
	if got := g.OTPCapacity(); got != 20 {
		t.Fatalf("otp capacity: %d", got)
	}

	// An older license must not replace the active one.
	older := issue(t, authPriv, req, g.now().Add(-time.Hour), licformat.NewDate(2027, 10, 31))
	if _, err := g.Install(older); err == nil {
		t.Fatal("older license must be rejected")
	}
}

func TestInstallRejectsForeignAndExpired(t *testing.T) {
	authPub, authPriv, _ := ed25519.GenerateKey(rand.Reader)
	ring := keys.Keyring{"lic-test": authPub}
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

	other := NewGuard(ring, newIdentity(t))
	other.now = func() time.Time { return now }
	req, _ := other.ActivationRequest("TPK-TEST", licformat.Fingerprint{}, nil)
	foreign := issue(t, authPriv, req, now, licformat.NewDate(2027, 1, 1))

	g := NewGuard(ring, newIdentity(t))
	g.now = func() time.Time { return now }
	if _, err := g.Install(foreign); err == nil {
		t.Fatal("license for another installation must be rejected")
	}

	ownReq, _ := g.ActivationRequest("TPK-TEST", licformat.Fingerprint{}, nil)
	expired := issue(t, authPriv, ownReq, now.AddDate(-2, 0, 0), licformat.NewDate(2025, 1, 1))
	if _, err := g.Install(expired); err == nil {
		t.Fatal("expired license must be rejected")
	}

	untrusted := NewGuard(keys.Keyring{}, g.Identity())
	if _, err := untrusted.Install(issue(t, authPriv, ownReq, now, licformat.NewDate(2027, 1, 1))); err == nil {
		t.Fatal("license from an unknown key must be rejected")
	}
}

func TestIdentityPersists(t *testing.T) {
	dir := t.TempDir()
	a, err := LoadOrCreateIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreateIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID() != b.ID() {
		t.Fatal("identity must be stable across loads")
	}
	info, err := os.Stat(filepath.Join(dir, identityFile))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("identity file mode %o, want 600", perm)
	}
}

func TestCollectFingerprint(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc/machine-id"), []byte("ABC123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fp := CollectFingerprint(root)
	if fp.MachineID == "" || fp.MachineID == "abc123" || fp.ProductUUID != "" {
		t.Fatalf("unexpected fingerprint %+v", fp)
	}
	if again := CollectFingerprint(root); again.MachineID != fp.MachineID {
		t.Fatal("fingerprint must be deterministic")
	}
}

func TestTrustedKeysLoads(t *testing.T) {
	if _, err := TrustedKeys(); err != nil {
		t.Fatal(err)
	}
}
