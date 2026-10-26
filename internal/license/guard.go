// Package license enforces TaktaPlus licenses inside the application: it
// holds the installation identity, the trusted license-authority keys and
// the currently installed license, and answers entitlement questions.
package license

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"time"

	"github.com/farshadfighter/taktaplus-lic/pkg/keys"
	"github.com/farshadfighter/taktaplus-lic/pkg/licformat"

	"github.com/farshadfighter/taktaplus/internal/buildinfo"
)

//go:embed all:keys
var embeddedKeys embed.FS

// TrustedKeys returns the license-authority public keys built into the
// binary (internal/license/keys/*.pub).
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
			return nil, fmt.Errorf("license: %s: %w", e.Name(), err)
		}
		for kid, pub := range part {
			if err := ring.Add(kid, pub); err != nil {
				return nil, err
			}
		}
	}
	return ring, nil
}

// ErrNoLicense is returned when no license has been installed yet.
var ErrNoLicense = errors.New("license: no license installed")

// Guard holds the active license. It is safe for concurrent use.
type Guard struct {
	ring     keys.Keyring
	identity Identity
	now      func() time.Time

	mu    sync.RWMutex
	lic   *licformat.License
	floor time.Time // latest time ever observed; the clock never counts as earlier
}

// NewGuard returns a Guard without a license; every device is unlicensed
// until Install succeeds.
func NewGuard(ring keys.Keyring, identity Identity) *Guard {
	return &Guard{ring: ring, identity: identity, now: time.Now}
}

// Identity returns the installation identity.
func (g *Guard) Identity() Identity { return g.identity }

// SetClockFloor records the latest time seen on this installation (for
// example from the database). Entitlements are evaluated at the later of the
// system clock and this floor, so turning the clock back gains nothing.
func (g *Guard) SetClockFloor(t time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if t.After(g.floor) {
		g.floor = t
	}
}

// Now is the time entitlements are evaluated at.
func (g *Guard) Now() time.Time {
	g.mu.RLock()
	floor := g.floor
	g.mu.RUnlock()
	if now := g.now(); now.After(floor) {
		return now
	}
	return floor
}

// ClockRolledBack reports whether the system clock is more than an hour
// behind the latest time seen.
func (g *Guard) ClockRolledBack() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.now().Before(g.floor.Add(-time.Hour))
}

// Clear removes the active license (for example after revocation).
func (g *Guard) Clear() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.lic = nil
}

// Install verifies a .tplic token and makes it the active license. It is
// rejected if it was issued for another installation or has fully expired.
func (g *Guard) Install(token string) (*licformat.License, error) {
	lic, err := licformat.ParseLicense(token, g.ring)
	if err != nil {
		return nil, err
	}
	if err := lic.BoundTo(g.identity.Public()); err != nil {
		return nil, err
	}
	if !g.Now().Before(lic.NotAfter) {
		return nil, errors.New("license: license has expired")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.lic != nil && lic.IssuedAt.Before(g.lic.IssuedAt) {
		return nil, errors.New("license: a newer license is already installed")
	}
	g.lic = lic
	return lic, nil
}

// License returns the active license, or ErrNoLicense.
func (g *Guard) License() (*licformat.License, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.lic == nil {
		return nil, ErrNoLicense
	}
	return g.lic, nil
}

// DeviceStatus returns the entitlement of a device serial right now.
func (g *Guard) DeviceStatus(serial string) licformat.Status {
	lic, err := g.License()
	if err != nil {
		return licformat.StatusUnlicensed
	}
	return lic.DeviceStatus(serial, g.Now())
}

// OTPCapacity returns the number of two-factor users currently entitled.
func (g *Guard) OTPCapacity() int {
	lic, err := g.License()
	if err != nil {
		return 0
	}
	return lic.OTPCapacity(g.Now())
}

// ActivationRequest builds a signed .tpreq token for offline or online
// activation with the given license key.
func (g *Guard) ActivationRequest(licenseKey string, fp licformat.Fingerprint, usage *licformat.Usage) (string, error) {
	return g.request(licformat.RequestActivate, strings.TrimSpace(licenseKey), "", fp, usage)
}

// RefreshRequest builds a signed request for the current license, used for
// heartbeats and offline renewals.
func (g *Guard) RefreshRequest(fp licformat.Fingerprint, usage *licformat.Usage) (string, error) {
	lic, err := g.License()
	if err != nil {
		return "", err
	}
	return g.request(licformat.RequestRefresh, "", lic.LicenseID, fp, usage)
}

func (g *Guard) request(typ licformat.RequestType, key, licenseID string, fp licformat.Fingerprint, usage *licformat.Usage) (string, error) {
	pub := g.identity.Public()
	req := &licformat.Request{
		Format:             licformat.FormatVersion,
		Type:               typ,
		LicenseKey:         key,
		LicenseID:          licenseID,
		InstallationID:     licformat.InstallationID(pub),
		InstallationPubKey: pub,
		Fingerprint:        fp,
		ProductVersion:     buildinfo.Version,
		CreatedAt:          g.now().UTC().Truncate(time.Second), // the real clock: the server checks it
		Nonce:              licformat.NewNonce(),
		Usage:              usage,
	}
	return req.Sign(g.identity.Private)
}
