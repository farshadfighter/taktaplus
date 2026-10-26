// Package device talks to FortiGate and FortiWeb appliances over their REST
// API and SSH. Feature modules only use the Driver interface.
package device

import (
	"context"
	"errors"
	"time"
)

// Kind is a device family.
type Kind string

const (
	KindFortiGate Kind = "fortigate"
	KindFortiWeb  Kind = "fortiweb"
)

// Info is what Probe discovers about a device.
type Info struct {
	Serial       string   `json:"serial"`
	Model        string   `json:"model"`
	Firmware     string   `json:"firmware"` // e.g. 7.4.4
	Build        string   `json:"build"`
	Hostname     string   `json:"hostname"`
	HAMode       string   `json:"ha_mode"` // standalone, a-p, a-a
	HAPeers      []string `json:"ha_peers"`
	VDOMs        []string `json:"vdoms"`
	FortiManager bool     `json:"fortimanager"` // managed by a FortiManager
}

// Interface is one network interface's state.
type Interface struct {
	Name    string `json:"name"`
	Alias   string `json:"alias"`
	IP      string `json:"ip"`
	Up      bool   `json:"up"`
	Speed   int    `json:"speed"` // Mbps
	RxBytes int64  `json:"rx_bytes"`
	TxBytes int64  `json:"tx_bytes"`
}

// Health is a point-in-time resource snapshot.
type Health struct {
	CPU        float64     `json:"cpu"`  // percent
	Mem        float64     `json:"mem"`  // percent
	Disk       float64     `json:"disk"` // percent, -1 if unknown
	Sessions   int64       `json:"sessions"`
	Uptime     int64       `json:"uptime"` // seconds, 0 if unknown
	Interfaces []Interface `json:"interfaces"`
}

// Signature is an installed security database or engine version.
type Signature struct {
	Name    string `json:"name"`    // e.g. ips_db, av_engine
	Version string `json:"version"` // as reported, e.g. 28.00948
}

// Contract is a FortiGuard subscription on the device.
type Contract struct {
	Name    string     `json:"name"`
	Status  string     `json:"status"` // licensed, expired, ...
	Expires *time.Time `json:"expires"`
}

// Driver is implemented per device family.
type Driver interface {
	Probe(ctx context.Context) (Info, error)
	Health(ctx context.Context) (Health, error)
	Signatures(ctx context.Context) ([]Signature, []Contract, error)
	// BackupConfig returns the full configuration file.
	BackupConfig(ctx context.Context) ([]byte, error)
	// RestoreConfig uploads a configuration; the device reboots afterwards.
	RestoreConfig(ctx context.Context, cfg []byte) error
}

// ErrUnsupported means the operation is not available over the configured
// transports.
var ErrUnsupported = errors.New("operation not supported by this device or transport")

// Order selects which transport a device uses first.
type Order string

const (
	APIFirst Order = "api_first"
	SSHFirst Order = "ssh_first"
	APIOnly  Order = "api_only"
	SSHOnly  Order = "ssh_only"
)

// Config is everything needed to reach one device.
type Config struct {
	Kind      Kind
	Host      string
	HTTPSPort int
	SSHPort   int
	Order     Order

	APIToken string // FortiGate: REST API token
	APIUser  string // FortiWeb: admin user for the API token
	APIPass  string

	// TLS: PinnedSHA256 is the hex SHA-256 of the device certificate (DER).
	// Empty means verify against system CAs.
	PinnedSHA256 string

	SSHUser     string
	SSHPassword string
	SSHKey      []byte // PEM private key
	SSHHostKey  string // pinned host key (authorized_keys format); empty = accept and report
}

// New returns the driver for a device, combining its transports in the
// configured order.
func New(c Config) Driver {
	var api, ssh Driver
	if (c.APIToken != "" || c.APIUser != "") && c.Order != SSHOnly {
		switch c.Kind {
		case KindFortiGate:
			api = newFortiGateAPI(c)
		case KindFortiWeb:
			api = newFortiWebAPI(c)
		}
	}
	if c.SSHUser != "" && c.Order != APIOnly {
		ssh = newSSHDriver(c)
	}
	switch {
	case api != nil && ssh != nil && c.Order == SSHFirst:
		return fallback{ssh, api}
	case api != nil && ssh != nil:
		return fallback{api, ssh}
	case api != nil:
		return api
	case ssh != nil:
		return ssh
	}
	return noTransport{}
}

// fallback tries the primary transport and uses the secondary when the
// primary fails or does not support the operation.
type fallback struct{ primary, secondary Driver }

func try[T any](ctx context.Context, a, b func(context.Context) (T, error)) (T, error) {
	v, err := a(ctx)
	if err == nil {
		return v, nil
	}
	v2, err2 := b(ctx)
	if err2 == nil {
		return v2, nil
	}
	if errors.Is(err2, ErrUnsupported) {
		return v, err
	}
	return v2, errors.Join(err, err2)
}

func (f fallback) Probe(ctx context.Context) (Info, error) {
	return try(ctx, f.primary.Probe, f.secondary.Probe)
}
func (f fallback) Health(ctx context.Context) (Health, error) {
	return try(ctx, f.primary.Health, f.secondary.Health)
}
func (f fallback) BackupConfig(ctx context.Context) ([]byte, error) {
	return try(ctx, f.primary.BackupConfig, f.secondary.BackupConfig)
}
func (f fallback) Signatures(ctx context.Context) ([]Signature, []Contract, error) {
	s, c, err := f.primary.Signatures(ctx)
	if err == nil {
		return s, c, nil
	}
	s2, c2, err2 := f.secondary.Signatures(ctx)
	if err2 == nil {
		return s2, c2, nil
	}
	return nil, nil, errors.Join(err, err2)
}
func (f fallback) RestoreConfig(ctx context.Context, cfg []byte) error {
	err := f.primary.RestoreConfig(ctx, cfg)
	if err == nil {
		return nil
	}
	if err2 := f.secondary.RestoreConfig(ctx, cfg); err2 == nil {
		return nil
	} else if !errors.Is(err2, ErrUnsupported) {
		return errors.Join(err, err2)
	}
	return err
}

type noTransport struct{}

var errNoTransport = errors.New("no API token or SSH credentials configured")

func (noTransport) Probe(context.Context) (Info, error)          { return Info{}, errNoTransport }
func (noTransport) Health(context.Context) (Health, error)       { return Health{}, errNoTransport }
func (noTransport) BackupConfig(context.Context) ([]byte, error) { return nil, errNoTransport }
func (noTransport) RestoreConfig(context.Context, []byte) error  { return errNoTransport }
func (noTransport) Signatures(context.Context) ([]Signature, []Contract, error) {
	return nil, nil, errNoTransport
}
