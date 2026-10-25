package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/farshadfighter/taktaplus-lic/pkg/licformat"
)

const identityFile = "installation.key"

// Identity is this installation's key pair. The license server binds licenses
// to its public key.
type Identity struct {
	Private ed25519.PrivateKey
}

// Public returns the installation public key.
func (id Identity) Public() ed25519.PublicKey { return id.Private.Public().(ed25519.PublicKey) }

// ID returns the human-readable installation ID.
func (id Identity) ID() string { return licformat.InstallationID(id.Public()) }

// LoadOrCreateIdentity reads the installation key from dataDir, creating it
// on first start. Losing this file means the installation must be activated
// again, so it belongs in the data volume and in backups.
func LoadOrCreateIdentity(dataDir string) (Identity, error) {
	path := filepath.Join(dataDir, identityFile)
	data, err := os.ReadFile(path)
	if err == nil {
		seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
		if err != nil || len(seed) != ed25519.SeedSize {
			return Identity{}, fmt.Errorf("license: %s is corrupted", path)
		}
		return Identity{Private: ed25519.NewKeyFromSeed(seed)}, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return Identity{}, err
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Identity{}, err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return Identity{}, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return Identity{}, err
	}
	if _, err := f.WriteString(base64.StdEncoding.EncodeToString(priv.Seed()) + "\n"); err != nil {
		f.Close()
		return Identity{}, err
	}
	if err := f.Close(); err != nil {
		return Identity{}, err
	}
	return Identity{Private: priv}, nil
}

// CollectFingerprint reads hardware identifiers. hostRoot is "/" on a bare
// host; in a container the host's /etc/machine-id and /sys/class/dmi/id are
// mounted read-only under it (e.g. "/host").
func CollectFingerprint(hostRoot string) licformat.Fingerprint {
	read := func(rel string) string {
		data, err := os.ReadFile(filepath.Join(hostRoot, rel))
		if err != nil {
			return ""
		}
		return strings.ToLower(strings.TrimSpace(string(data)))
	}
	return licformat.Fingerprint{
		MachineID:   hashComponent("machine-id", read("etc/machine-id")),
		ProductUUID: hashComponent("product-uuid", read("sys/class/dmi/id/product_uuid")),
		MAC:         hashComponent("mac", primaryMAC()),
	}
}

func hashComponent(kind, value string) string {
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("taktaplus:" + kind + ":" + value))
	return hex.EncodeToString(sum[:])
}

// primaryMAC returns the lowest hardware address among physical-looking,
// non-loopback interfaces, so the choice is stable across reboots.
func primaryMAC() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	var macs []string
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagLoopback != 0 || len(ifc.HardwareAddr) != 6 {
			continue
		}
		if ifc.HardwareAddr[0]&0x02 != 0 { // locally administered: veth, bridges, VPNs
			continue
		}
		macs = append(macs, ifc.HardwareAddr.String())
	}
	if len(macs) == 0 {
		return ""
	}
	sort.Strings(macs)
	return macs[0]
}
