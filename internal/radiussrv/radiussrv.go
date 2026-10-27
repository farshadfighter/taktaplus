// Package radiussrv is the RADIUS server for SMS two-factor sign-in:
// FortiGate sends the user's password (PAP), TaktaPlus checks it and
// answers with an Access-Challenge after texting a code, and accepts the
// second request that carries the code.
package radiussrv

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"layeh.com/radius"
	"layeh.com/radius/rfc2865"

	"github.com/farshadfighter/taktaplus/internal/otp"
	"github.com/farshadfighter/taktaplus/internal/secretbox"
	"github.com/farshadfighter/taktaplus/internal/settings"
)

// messageAuthenticatorType is RFC 3579's Message-Authenticator attribute.
const messageAuthenticatorType radius.Type = 80

// Config is the RADIUS setting ("radius").
type Config struct {
	// RequireMessageAuthenticator drops requests without a valid
	// Message-Authenticator (protects against spoofed requests and
	// BlastRADIUS); FortiOS sends it since the 2024 fixes.
	RequireMessageAuthenticator bool   `json:"require_message_authenticator"`
	Message                     string `json:"message"` // shown when asking for the code
}

// DefaultConfig is used until one is saved.
var DefaultConfig = Config{RequireMessageAuthenticator: true, Message: "Enter the code sent to your mobile"}

// messageAuthenticator computes HMAC-MD5 over p with the attribute zeroed
// and the given authenticator in the header.
func messageAuthenticator(p *radius.Packet) ([]byte, error) {
	q := *p
	q.Attributes = append(radius.Attributes(nil), p.Attributes...)
	q.Attributes.Set(messageAuthenticatorType, make(radius.Attribute, 16))
	b, err := q.MarshalBinary()
	if err != nil {
		return nil, err
	}
	mac := hmac.New(md5.New, p.Secret)
	mac.Write(b)
	return mac.Sum(nil), nil
}

// verifyMessageAuthenticator reports whether p carries the attribute and
// whether it is valid.
func verifyMessageAuthenticator(p *radius.Packet) (present, valid bool) {
	got, ok := p.Attributes.Lookup(messageAuthenticatorType)
	if !ok {
		return false, false
	}
	want, err := messageAuthenticator(p)
	return true, err == nil && hmac.Equal(got, want)
}

// signResponse adds a Message-Authenticator to a response (computed with
// the request authenticator, before the response authenticator).
func signResponse(resp *radius.Packet) {
	resp.Attributes.Set(messageAuthenticatorType, make(radius.Attribute, 16))
	if ma, err := messageAuthenticator(resp); err == nil {
		resp.Attributes.Set(messageAuthenticatorType, ma)
	}
}

// FortinetVendorID is Fortinet's IANA enterprise number.
const FortinetVendorID = 12356

const secretContext = "radius-secret"

// fortinetGroupName builds the Fortinet-Group-Name (vendor type 1) VSA.
func fortinetGroupName(group string) (radius.Attribute, error) {
	v := make(radius.Attribute, 0, 2+len(group))
	v = append(v, 1, byte(2+len(group)))
	v = append(v, group...)
	return radius.NewVendorSpecific(FortinetVendorID, v)
}

// Client is a device allowed to send RADIUS requests.
type Client struct {
	DeviceID   int64  `json:"device_id"`
	DeviceName string `json:"device_name"`
	Host       string `json:"host"`
	SourceIP   string `json:"source_ip"`
	Enabled    bool   `json:"enabled"`
	HasSecret  bool   `json:"has_secret"`
	secret     string
}

// Server serves RADIUS.
type Server struct {
	db       *pgxpool.Pool
	box      *secretbox.Box
	otp      *otp.Service
	settings *settings.Store
	log      *slog.Logger
	srv      *radius.PacketServer

	mu      sync.Mutex
	clients map[string]Client // by source address
	loaded  time.Time
}

// New returns a server.
func New(db *pgxpool.Pool, box *secretbox.Box, st *settings.Store, o *otp.Service, log *slog.Logger) *Server {
	return &Server{db: db, box: box, settings: st, otp: o, log: log}
}

// Config returns the stored RADIUS settings.
func (s *Server) Config(ctx context.Context) (Config, error) {
	c := DefaultConfig
	err := s.settings.Get(ctx, "radius", &c)
	return c, err
}

// SaveConfig stores the RADIUS settings.
func (s *Server) SaveConfig(ctx context.Context, c Config) error {
	if strings.TrimSpace(c.Message) == "" {
		c.Message = DefaultConfig.Message
	}
	return s.settings.Set(ctx, "radius", c)
}

// ListClients returns devices with their RADIUS settings (devices without
// a row are listed as not enabled).
func (s *Server) ListClients(ctx context.Context) ([]Client, error) {
	rows, err := s.db.Query(ctx, `SELECT d.id, d.name, d.host, coalesce(c.source_ip, ''), coalesce(c.enabled, false), c.secret
		FROM devices d LEFT JOIN radius_clients c ON c.device_id = d.id WHERE d.kind = 'fortigate' ORDER BY lower(d.name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Client{}
	for rows.Next() {
		var c Client
		var sealed []byte
		if err := rows.Scan(&c.DeviceID, &c.DeviceName, &c.Host, &c.SourceIP, &c.Enabled, &sealed); err != nil {
			return nil, err
		}
		if sealed != nil {
			plain, err := s.box.Open(sealed, secretContext)
			if err != nil {
				return nil, err
			}
			c.secret, c.HasSecret = string(plain), true
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func newSecret() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return strings.TrimRight(base64.RawURLEncoding.EncodeToString(b), "=")
}

// SetClient enables or disables a device as RADIUS client, generating a
// shared secret the first time. sourceIP overrides the address requests
// come from.
func (s *Server) SetClient(ctx context.Context, deviceID int64, enabled bool, sourceIP string) error {
	sourceIP = strings.TrimSpace(sourceIP)
	if sourceIP != "" && net.ParseIP(sourceIP) == nil {
		return otpInvalid("source address must be an IP address")
	}
	_, err := s.db.Exec(ctx, `INSERT INTO radius_clients (device_id, secret, source_ip, enabled) VALUES ($1, $2, $3, $4)
		ON CONFLICT (device_id) DO UPDATE SET source_ip = EXCLUDED.source_ip, enabled = EXCLUDED.enabled`,
		deviceID, s.box.Seal([]byte(newSecret()), secretContext), sourceIP, enabled)
	s.invalidate()
	return err
}

// RotateSecret replaces a device's shared secret.
func (s *Server) RotateSecret(ctx context.Context, deviceID int64) error {
	_, err := s.db.Exec(ctx, `UPDATE radius_clients SET secret = $2 WHERE device_id = $1`, deviceID, s.box.Seal([]byte(newSecret()), secretContext))
	s.invalidate()
	return err
}

type invalidError string

func (e invalidError) Error() string { return string(e) }

func otpInvalid(msg string) error { return invalidError(msg) }

// IsInput reports whether err is a validation error.
func IsInput(err error) bool {
	var ie invalidError
	return errors.As(err, &ie)
}

// FortiGateConfig returns the CLI that points a FortiGate at this server.
func (s *Server) FortiGateConfig(ctx context.Context, deviceID int64, serverIP, group string) (string, error) {
	list, err := s.ListClients(ctx)
	if err != nil {
		return "", err
	}
	for _, c := range list {
		if c.DeviceID != deviceID {
			continue
		}
		if !c.HasSecret {
			return "", otpInvalid("enable the device as a RADIUS client first")
		}
		if group == "" {
			group = "TaktaPlus-2FA"
		}
		return fmt.Sprintf(`config system global
    set remoteauthtimeout 90
end
config user radius
    edit "TaktaPlus"
        set server "%s"
        set secret %s
        set auth-type pap
    next
end
config user group
    edit "%s"
        set member "TaktaPlus"
    next
end`, serverIP, c.secret, group), nil
	}
	return "", otpInvalid("unknown device")
}

func (s *Server) invalidate() {
	s.mu.Lock()
	s.loaded = time.Time{}
	s.mu.Unlock()
}

// lookup finds the enabled client for a source address (cached for a minute).
func (s *Server) lookup(ctx context.Context, addr net.Addr) (Client, bool) {
	host := addr.String()
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.loaded) > time.Minute {
		list, err := s.ListClients(ctx)
		if err != nil {
			s.log.Error("load radius clients", "err", err)
			return Client{}, false
		}
		s.clients = map[string]Client{}
		for _, c := range list {
			if !c.Enabled || !c.HasSecret {
				continue
			}
			ips := []string{c.SourceIP}
			if net.ParseIP(c.Host) != nil {
				ips = append(ips, c.Host)
			} else if resolved, err := net.LookupHost(c.Host); err == nil {
				ips = append(ips, resolved...)
			}
			for _, ip := range ips {
				if p := net.ParseIP(ip); p != nil {
					s.clients[p.String()] = c
				}
			}
		}
		s.loaded = time.Now()
	}
	if p := net.ParseIP(host); p != nil {
		c, ok := s.clients[p.String()]
		return c, ok
	}
	return Client{}, false
}

// RADIUSSecret implements radius.SecretSource; unknown sources get no
// secret, so their packets are dropped.
func (s *Server) RADIUSSecret(ctx context.Context, remote net.Addr) ([]byte, error) {
	c, ok := s.lookup(ctx, remote)
	if !ok {
		return nil, nil
	}
	return []byte(c.secret), nil
}

// ServeRADIUS handles Access-Request packets.
func (s *Server) ServeRADIUS(w radius.ResponseWriter, r *radius.Request) {
	if r.Code != radius.CodeAccessRequest {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	cfg, err := s.Config(ctx)
	if err != nil {
		s.log.Error("radius settings", "err", err)
		return
	}
	c, _ := s.lookup(ctx, r.RemoteAddr)
	dev := &c.DeviceID
	if present, valid := verifyMessageAuthenticator(r.Packet); (present && !valid) || (!present && cfg.RequireMessageAuthenticator) {
		// Wrong secret or a spoofed packet: drop it without counting a
		// failure against the user.
		s.log.Warn("radius request dropped: missing or invalid Message-Authenticator", "from", r.RemoteAddr.String(), "device", c.DeviceName)
		return
	}
	ip := r.RemoteAddr.String()
	username := rfc2865.UserName_GetString(r.Packet)
	password := rfc2865.UserPassword_GetString(r.Packet)
	state := rfc2865.State_GetString(r.Packet)

	reject := func(why string) {
		s.otp.LogEvent(ctx, username, dev, ip, "reject", why)
		resp := r.Response(radius.CodeAccessReject)
		signResponse(resp)
		_ = w.Write(resp)
	}
	challenge := func(state, msg string) {
		resp := r.Response(radius.CodeAccessChallenge)
		_ = rfc2865.State_SetString(resp, state)
		_ = rfc2865.ReplyMessage_SetString(resp, msg)
		signResponse(resp)
		_ = w.Write(resp)
	}

	if state != "" {
		u, why, err := s.otp.VerifyChallenge(ctx, state, password)
		switch {
		case errors.Is(err, otp.ErrWrongCode):
			s.otp.LogEvent(ctx, username, dev, ip, "wrong_code", "")
			challenge(state, "Wrong code. "+cfg.Message)
			return
		case err != nil:
			if why == "" {
				why = err.Error()
			}
			reject(why)
			return
		case !strings.EqualFold(u.Username, username):
			reject("code belongs to another user")
			return
		}
		resp := r.Response(radius.CodeAccessAccept)
		if u.FortinetGroup != "" {
			if a, err := fortinetGroupName(u.FortinetGroup); err == nil {
				resp.Add(rfc2865.VendorSpecific_Type, a)
			}
		}
		s.otp.LogEvent(ctx, u.Username, dev, ip, "accept", u.FortinetGroup)
		signResponse(resp)
		_ = w.Write(resp)
		return
	}

	u, why, err := s.otp.CheckPassword(ctx, username, password)
	if err != nil {
		if why == "" {
			why = err.Error()
		}
		reject(why)
		return
	}
	st, err := s.otp.StartChallenge(ctx, u, dev)
	if err != nil {
		reject("sending the code failed: " + err.Error())
		return
	}
	s.otp.LogEvent(ctx, u.Username, dev, ip, "challenge", "code sent to "+mask(u.Mobile))
	challenge(st, cfg.Message)
}

func mask(m string) string {
	if len(m) < 7 {
		return m
	}
	return m[:4] + "***" + m[len(m)-4:]
}

// Serve listens on addr until Shutdown.
func (s *Server) Serve(addr string) error {
	s.srv = &radius.PacketServer{Addr: addr, Network: "udp", Handler: s, SecretSource: s}
	return s.srv.ListenAndServe()
}

// ServeConn serves on an existing connection (tests).
func (s *Server) ServeConn(conn net.PacketConn) error {
	s.srv = &radius.PacketServer{Handler: s, SecretSource: s}
	return s.srv.Serve(conn)
}

// Shutdown stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.srv == nil {
		return nil
	}
	return s.srv.Shutdown(ctx)
}
