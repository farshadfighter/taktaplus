// Package inventory stores managed devices, their credentials and their
// latest discovered state, and builds drivers for them.
package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/farshadfighter/taktaplus-lic/pkg/licformat"

	"github.com/farshadfighter/taktaplus/internal/device"
	"github.com/farshadfighter/taktaplus/internal/license"
	"github.com/farshadfighter/taktaplus/internal/secretbox"
)

const credContext = "device-credentials"

// Credentials are the secrets used to reach a device. They are stored sealed
// and never returned by the API.
type Credentials struct {
	APIToken    string `json:"api_token,omitempty"`
	APIUser     string `json:"api_user,omitempty"`
	APIPass     string `json:"api_pass,omitempty"`
	SSHUser     string `json:"ssh_user,omitempty"`
	SSHPassword string `json:"ssh_password,omitempty"`
	SSHKey      string `json:"ssh_key,omitempty"` // PEM
}

// InputError is a validation failure to show to the user.
type InputError struct{ msg string }

func (e InputError) Error() string { return e.msg }

func invalidf(format string, a ...any) error { return InputError{fmt.Sprintf(format, a...)} }

// ErrNotFound means the device or group does not exist.
var ErrNotFound = errors.New("not found")

// Peer is an HA member reported by a device, with its own entitlement.
type Peer struct {
	Serial  string           `json:"serial"`
	License licformat.Status `json:"license"`
}

// Device is a managed appliance as shown in the panel.
type Device struct {
	ID          int64        `json:"id"`
	Name        string       `json:"name"`
	Kind        device.Kind  `json:"kind"`
	Host        string       `json:"host"`
	HTTPSPort   int          `json:"https_port"`
	SSHPort     int          `json:"ssh_port"`
	Order       device.Order `json:"transport_order"`
	GroupID     *int64       `json:"group_id"`
	GroupName   string       `json:"group_name"`
	Tags        []string     `json:"tags"`
	TLSPin      string       `json:"tls_pin"`
	SSHHostKey  string       `json:"ssh_host_key"`
	HasAPI      bool         `json:"has_api"`
	HasSSH      bool         `json:"has_ssh"`
	Serial      string       `json:"serial"`
	Model       string       `json:"model"`
	Firmware    string       `json:"firmware"`
	Build       string       `json:"build"`
	Hostname    string       `json:"hostname"`
	HAMode      string       `json:"ha_mode"`
	HAPeers     []Peer       `json:"ha_peers"`
	VDOMs       []string     `json:"vdoms"`
	FortiMgr    bool         `json:"fortimanager"`
	Status      string       `json:"status"`
	LastError   string       `json:"last_error"`
	LastSeenAt  *time.Time   `json:"last_seen_at"`
	LastProbeAt *time.Time   `json:"last_probe_at"`
	CreatedAt   time.Time    `json:"created_at"`
	// License is this serial's entitlement; only active and grace devices
	// are polled, backed up or changed.
	License licformat.Status `json:"license"`

	creds Credentials
}

// Operational reports whether the device's license allows operations.
func (d *Device) Operational() bool { return d.License.Operational() }

// Input creates or updates a device. On update, nil Credentials keeps the
// stored ones.
type Input struct {
	Name        string       `json:"name"`
	Kind        device.Kind  `json:"kind"`
	Host        string       `json:"host"`
	HTTPSPort   int          `json:"https_port"`
	SSHPort     int          `json:"ssh_port"`
	Order       device.Order `json:"transport_order"`
	GroupID     *int64       `json:"group_id"`
	Tags        []string     `json:"tags"`
	Credentials *Credentials `json:"credentials"`
	TLSPin      string       `json:"tls_pin"`
	SSHHostKey  string       `json:"ssh_host_key"`
}

func (in *Input) normalize(requireCreds bool) error {
	in.Name = strings.TrimSpace(in.Name)
	in.Host = strings.TrimSpace(in.Host)
	in.TLSPin = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(in.TLSPin), ":", ""))
	in.SSHHostKey = strings.TrimSpace(in.SSHHostKey)
	if in.Name == "" || len(in.Name) > 100 {
		return invalidf("name is required (at most 100 characters)")
	}
	if in.Kind != device.KindFortiGate && in.Kind != device.KindFortiWeb {
		return invalidf("kind must be fortigate or fortiweb")
	}
	if in.Host == "" || strings.ContainsAny(in.Host, " /:@") && net.ParseIP(in.Host) == nil {
		return invalidf("host must be an IP address or host name")
	}
	if in.HTTPSPort == 0 {
		in.HTTPSPort = 443
	}
	if in.SSHPort == 0 {
		in.SSHPort = 22
	}
	if in.HTTPSPort < 1 || in.HTTPSPort > 65535 || in.SSHPort < 1 || in.SSHPort > 65535 {
		return invalidf("ports must be between 1 and 65535")
	}
	if in.Order == "" {
		in.Order = device.APIFirst
	}
	if !slices.Contains([]device.Order{device.APIFirst, device.SSHFirst, device.APIOnly, device.SSHOnly}, in.Order) {
		return invalidf("invalid transport order")
	}
	tags := []string{}
	for _, t := range in.Tags {
		if t = strings.TrimSpace(t); t != "" && !slices.Contains(tags, t) {
			tags = append(tags, t)
		}
	}
	in.Tags = tags
	if in.Credentials == nil {
		if requireCreds {
			return invalidf("credentials are required")
		}
		return nil
	}
	c := in.Credentials
	hasAPI := c.APIToken != "" || (in.Kind == device.KindFortiWeb && c.APIUser != "")
	hasSSH := c.SSHUser != "" && (c.SSHPassword != "" || c.SSHKey != "")
	switch {
	case !hasAPI && !hasSSH:
		return invalidf("enter an API token or SSH credentials")
	case in.Order == device.APIOnly && !hasAPI:
		return invalidf("API-only devices need API credentials")
	case in.Order == device.SSHOnly && !hasSSH:
		return invalidf("SSH-only devices need SSH credentials")
	}
	return nil
}

// Service manages the inventory.
type Service struct {
	db    *pgxpool.Pool
	box   *secretbox.Box
	guard *license.Guard
	// newDriver is replaceable in tests.
	newDriver func(device.Config) device.Driver
}

// New returns the inventory service.
func New(db *pgxpool.Pool, box *secretbox.Box, guard *license.Guard) *Service {
	return &Service{db: db, box: box, guard: guard, newDriver: device.New}
}

// SetDriverFactory replaces how drivers are built (for tests).
func (s *Service) SetDriverFactory(f func(device.Config) device.Driver) { s.newDriver = f }

func (s *Service) config(d *Device) device.Config {
	return device.Config{
		Kind: d.Kind, Host: d.Host, HTTPSPort: d.HTTPSPort, SSHPort: d.SSHPort, Order: d.Order,
		APIToken: d.creds.APIToken, APIUser: d.creds.APIUser, APIPass: d.creds.APIPass, PinnedSHA256: d.TLSPin,
		SSHUser: d.creds.SSHUser, SSHPassword: d.creds.SSHPassword, SSHKey: []byte(d.creds.SSHKey), SSHHostKey: d.SSHHostKey,
	}
}

// DiscoverResult is what the add-device dialog shows before saving.
type DiscoverResult struct {
	Info       device.Info      `json:"info"`
	TLSPin     string           `json:"tls_pin"`
	SSHHostKey string           `json:"ssh_host_key"`
	License    licformat.Status `json:"license"`
	Peers      []Peer           `json:"ha_peers"`
}

// Discover connects to a device before it is saved: it fetches the TLS
// certificate and SSH host key for pinning (unless given) and probes it.
// With existingID, secrets left empty are taken from that saved device.
func (s *Service) Discover(ctx context.Context, existingID int64, in Input) (*DiscoverResult, error) {
	if existingID != 0 {
		cur, err := s.Get(ctx, existingID)
		if err != nil {
			return nil, err
		}
		merged := mergeCredentials(cur.creds, in.Credentials)
		in.Credentials = &merged
	}
	if err := in.normalize(true); err != nil {
		return nil, err
	}
	res := &DiscoverResult{TLSPin: in.TLSPin, SSHHostKey: in.SSHHostKey}
	c := in.Credentials
	if res.TLSPin == "" && in.Order != device.SSHOnly && (c.APIToken != "" || c.APIUser != "") {
		pin, err := device.FetchCertificate(ctx, in.Host, in.HTTPSPort)
		if err != nil {
			return nil, fmt.Errorf("HTTPS connection to %s failed: %w", in.Host, err)
		}
		res.TLSPin = pin
	}
	if res.SSHHostKey == "" && in.Order != device.APIOnly && c.SSHUser != "" {
		if hk, err := device.FetchSSHHostKey(ctx, in.Host, in.SSHPort); err == nil {
			res.SSHHostKey = hk
		} else if in.Order == device.SSHOnly {
			return nil, fmt.Errorf("SSH connection to %s failed: %w", in.Host, err)
		}
	}
	d := &Device{Kind: in.Kind, Host: in.Host, HTTPSPort: in.HTTPSPort, SSHPort: in.SSHPort, Order: in.Order,
		TLSPin: res.TLSPin, SSHHostKey: res.SSHHostKey, creds: *c}
	info, err := s.newDriver(s.config(d)).Probe(ctx)
	if err != nil {
		return nil, err
	}
	res.Info = info
	res.License = s.serialStatus(info.Serial)
	res.Peers = s.peers(info.HAPeers)
	return res, nil
}

func (s *Service) serialStatus(serial string) licformat.Status {
	if serial == "" {
		return licformat.StatusUnlicensed
	}
	return s.guard.DeviceStatus(serial)
}

func (s *Service) peers(serials []string) []Peer {
	out := []Peer{}
	for _, p := range serials {
		out = append(out, Peer{Serial: p, License: s.serialStatus(p)})
	}
	return out
}

func (s *Service) seal(c Credentials) ([]byte, error) {
	data, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	return s.box.Seal(data, credContext), nil
}

func mapDBError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case pgErr.Code == "23505" && pgErr.ConstraintName == "devices_name_key":
			return invalidf("a device with this name already exists")
		case pgErr.Code == "23505" && pgErr.ConstraintName == "devices_serial_key":
			return invalidf("a device with this serial number is already registered")
		case pgErr.Code == "23505" && pgErr.ConstraintName == "device_groups_name_key":
			return invalidf("a group with this name already exists")
		case pgErr.Code == "23503":
			return invalidf("the selected group does not exist")
		}
	}
	return err
}

// Create saves a device. Callers then Probe it.
func (s *Service) Create(ctx context.Context, in Input) (int64, error) {
	if err := in.normalize(true); err != nil {
		return 0, err
	}
	sealed, err := s.seal(*in.Credentials)
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.db.QueryRow(ctx, `
		INSERT INTO devices (name, kind, host, https_port, ssh_port, transport_order, group_id, tags, credentials, tls_pin, ssh_host_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) RETURNING id`,
		in.Name, in.Kind, in.Host, in.HTTPSPort, in.SSHPort, in.Order, in.GroupID, in.Tags, sealed, in.TLSPin, in.SSHHostKey).Scan(&id)
	return id, mapDBError(err)
}

// Update changes a device. Changing the host clears nothing: pins stay
// until the caller sends new ones.
func (s *Service) Update(ctx context.Context, id int64, in Input) error {
	if err := in.normalize(false); err != nil {
		return err
	}
	cur, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	sealed, err := s.seal(mergeCredentials(cur.creds, in.Credentials))
	if err != nil {
		return err
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE devices SET name = $2, kind = $3, host = $4, https_port = $5, ssh_port = $6, transport_order = $7,
		       group_id = $8, tags = $9, credentials = $10, tls_pin = $11, ssh_host_key = $12
		WHERE id = $1`,
		id, in.Name, in.Kind, in.Host, in.HTTPSPort, in.SSHPort, in.Order, in.GroupID, in.Tags, sealed, in.TLSPin, in.SSHHostKey)
	if err != nil {
		return mapDBError(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// mergeCredentials applies an update: nil keeps the stored credentials, and
// empty secret fields keep the stored secrets, so forms need not resend
// them. User names are taken as sent.
func mergeCredentials(old Credentials, upd *Credentials) Credentials {
	if upd == nil {
		return old
	}
	n := *upd
	keep := func(v *string, prev string) {
		if *v == "" {
			*v = prev
		}
	}
	keep(&n.APIToken, old.APIToken)
	keep(&n.APIPass, old.APIPass)
	keep(&n.SSHPassword, old.SSHPassword)
	keep(&n.SSHKey, old.SSHKey)
	return n
}

// Delete removes a device and its status, metrics and alerts.
func (s *Service) Delete(ctx context.Context, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM devices WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

const deviceCols = `d.id, d.name, d.kind, d.host, d.https_port, d.ssh_port, d.transport_order, d.group_id, coalesce(g.name, ''),
	d.tags, d.credentials, d.tls_pin, d.ssh_host_key, d.serial, d.model, d.firmware, d.build, d.hostname, d.ha_mode,
	d.ha_peers, d.vdoms, d.fortimanager, d.status, d.last_error, d.last_seen_at, d.last_probe_at, d.created_at`

func (s *Service) scan(row pgx.Row) (*Device, error) {
	var d Device
	var sealed []byte
	var peers []string
	err := row.Scan(&d.ID, &d.Name, &d.Kind, &d.Host, &d.HTTPSPort, &d.SSHPort, &d.Order, &d.GroupID, &d.GroupName,
		&d.Tags, &sealed, &d.TLSPin, &d.SSHHostKey, &d.Serial, &d.Model, &d.Firmware, &d.Build, &d.Hostname, &d.HAMode,
		&peers, &d.VDOMs, &d.FortiMgr, &d.Status, &d.LastError, &d.LastSeenAt, &d.LastProbeAt, &d.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	plain, err := s.box.Open(sealed, credContext)
	if err != nil {
		return nil, fmt.Errorf("device %d: credentials: %w", d.ID, err)
	}
	if err := json.Unmarshal(plain, &d.creds); err != nil {
		return nil, err
	}
	d.HasAPI = d.creds.APIToken != "" || d.creds.APIUser != ""
	d.HasSSH = d.creds.SSHUser != ""
	d.License = s.serialStatus(d.Serial)
	d.HAPeers = s.peers(peers)
	return &d, nil
}

// Get returns one device.
func (s *Service) Get(ctx context.Context, id int64) (*Device, error) {
	return s.scan(s.db.QueryRow(ctx, `SELECT `+deviceCols+` FROM devices d LEFT JOIN device_groups g ON g.id = d.group_id WHERE d.id = $1`, id))
}

// Filter narrows List.
type Filter struct {
	GroupID *int64
	Query   string // name, host, serial or model substring
}

// List returns devices ordered by name.
func (s *Service) List(ctx context.Context, f Filter) ([]*Device, error) {
	rows, err := s.db.Query(ctx, `SELECT `+deviceCols+` FROM devices d LEFT JOIN device_groups g ON g.id = d.group_id
		WHERE ($1::bigint IS NULL OR d.group_id = $1)
		  AND ($2 = '' OR d.name ILIKE '%'||$2||'%' OR d.host ILIKE '%'||$2||'%' OR d.serial ILIKE '%'||$2||'%' OR d.model ILIKE '%'||$2||'%')
		ORDER BY lower(d.name)`, f.GroupID, strings.TrimSpace(f.Query))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Device{}
	for rows.Next() {
		d, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Driver returns a driver for the device.
func (s *Service) Driver(d *Device) device.Driver { return s.newDriver(s.config(d)) }

// Probe connects to the device and stores what it reports. Probing is
// allowed for unlicensed devices: it is how their serial is learned.
func (s *Service) Probe(ctx context.Context, id int64) (*Device, error) {
	d, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	info, perr := s.Driver(d).Probe(ctx)
	if perr != nil {
		_ = s.MarkUnreachable(ctx, id, perr)
		return nil, perr
	}
	_, err = s.db.Exec(ctx, `
		UPDATE devices SET serial = $2, model = $3, firmware = $4, build = $5, hostname = $6, ha_mode = $7,
		       ha_peers = $8, vdoms = $9, fortimanager = $10, status = 'online', last_error = '',
		       last_seen_at = now(), last_probe_at = now(), offline_since = NULL
		WHERE id = $1`,
		id, info.Serial, info.Model, info.Firmware, info.Build, info.Hostname, info.HAMode,
		nonNil(info.HAPeers), nonNil(info.VDOMs), info.FortiManager)
	if err != nil {
		return nil, mapDBError(err)
	}
	return s.Get(ctx, id)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// MarkUnreachable records a failed contact.
func (s *Service) MarkUnreachable(ctx context.Context, id int64, cause error) error {
	status := "offline"
	var cm device.CertMismatchError
	var hm device.HostKeyMismatchError
	var he device.HTTPError
	if errors.As(cause, &cm) || errors.As(cause, &hm) || errors.As(cause, &he) {
		status = "error" // reachable, but refused or not trusted
	}
	msg := cause.Error()
	if len(msg) > 500 {
		msg = msg[:500]
	}
	_, err := s.db.Exec(ctx, `UPDATE devices SET status = $2, last_error = $3,
		offline_since = CASE WHEN $2 = 'offline' THEN coalesce(offline_since, now()) ELSE NULL END WHERE id = $1`, id, status, msg)
	return err
}

// MarkSeen records a successful contact.
func (s *Service) MarkSeen(ctx context.Context, id int64) error {
	_, err := s.db.Exec(ctx, `UPDATE devices SET status = 'online', last_error = '', last_seen_at = now(), offline_since = NULL WHERE id = $1`, id)
	return err
}

// Usage reports the managed serials (HA peers included) for license
// requests.
func (s *Service) Usage(ctx context.Context) (*licformat.Usage, error) {
	rows, err := s.db.Query(ctx, `SELECT serial FROM devices WHERE serial <> '' UNION SELECT unnest(ha_peers) FROM devices ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	serials, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	return &licformat.Usage{Devices: serials}, nil
}
