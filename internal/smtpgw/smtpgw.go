// Package smtpgw is the email-to-SMS gateway for FortiGate local users with
// SMS two-factor: FortiGate mails the code to <mobile>@<gateway domain>,
// and TaktaPlus sends it with sms.ir. Other mail from the devices is
// relayed to the organization's mail server.
package smtpgw

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"sync"
	"time"

	gosmtp "github.com/emersion/go-smtp"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/farshadfighter/taktaplus/internal/device"
	"github.com/farshadfighter/taktaplus/internal/inventory"
	"github.com/farshadfighter/taktaplus/internal/otp"
	"github.com/farshadfighter/taktaplus/internal/settings"
	"github.com/farshadfighter/taktaplus/internal/sms"
)

// Config is the gateway setting ("smtpgw").
type Config struct {
	Domain        string `json:"domain"` // e.g. sms.taktaplus.local
	RelayHost     string `json:"relay_host"`
	RelayPort     int    `json:"relay_port"`
	RelaySecurity string `json:"relay_security"` // starttls, tls, none
	RelayUser     string `json:"relay_user"`
	RelayPassword string `json:"relay_password"`
}

// DefaultConfig is used until one is saved.
var DefaultConfig = Config{Domain: "sms.taktaplus.local", RelayPort: 25, RelaySecurity: "starttls"}

// Redacted hides the relay password.
func (c Config) Redacted() Config {
	if c.RelayPassword != "" {
		c.RelayPassword = "********"
	}
	return c
}

// Gateway runs the SMTP listener and the local user sync.
type Gateway struct {
	db       *pgxpool.Pool
	settings *settings.Store
	sms      *sms.Service
	otp      *otp.Service
	inv      *inventory.Service
	log      *slog.Logger
	srv      *gosmtp.Server
	// relay is replaceable in tests.
	relay func(cfg Config, from string, to []string, msg []byte) error

	mu      sync.Mutex
	devices map[string]int64 // device IP -> id
	loaded  time.Time
}

// New returns a gateway.
func New(db *pgxpool.Pool, st *settings.Store, s *sms.Service, o *otp.Service, inv *inventory.Service, log *slog.Logger) *Gateway {
	g := &Gateway{db: db, settings: st, sms: s, otp: o, inv: inv, log: log}
	g.relay = relaySMTP
	return g
}

// Config returns the stored settings.
func (g *Gateway) Config(ctx context.Context) (Config, error) {
	c := DefaultConfig
	err := g.settings.Get(ctx, "smtpgw", &c)
	return c, err
}

// SaveConfig stores the settings; a redacted password keeps the old one.
func (g *Gateway) SaveConfig(ctx context.Context, c Config) error {
	old, err := g.Config(ctx)
	if err != nil {
		return err
	}
	if c.RelayPassword == "" || c.RelayPassword == "********" {
		c.RelayPassword = old.RelayPassword
	}
	c.Domain = strings.ToLower(strings.TrimSpace(c.Domain))
	if c.Domain == "" || strings.ContainsAny(c.Domain, " @/") {
		return InputError("the gateway domain is required, e.g. sms.taktaplus.local")
	}
	if c.RelayPort == 0 {
		c.RelayPort = 25
	}
	return g.settings.Set(ctx, "smtpgw", c)
}

// InputError is a validation failure.
type InputError string

func (e InputError) Error() string { return string(e) }

// deviceFor returns the registered device sending from addr.
func (g *Gateway) deviceFor(ctx context.Context, addr net.Addr) (int64, bool) {
	host := addr.String()
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if time.Since(g.loaded) > time.Minute {
		list, err := g.inv.List(ctx, inventory.Filter{})
		if err != nil {
			g.log.Error("smtp gateway: load devices", "err", err)
			return 0, false
		}
		g.devices = map[string]int64{}
		for _, d := range list {
			ips := []string{d.Host}
			if net.ParseIP(d.Host) == nil {
				ips, _ = net.LookupHost(d.Host)
			}
			for _, ip := range ips {
				if p := net.ParseIP(ip); p != nil {
					g.devices[p.String()] = d.ID
				}
			}
		}
		// RADIUS source addresses identify devices too.
		rows, err := g.db.Query(ctx, `SELECT device_id, source_ip FROM radius_clients WHERE source_ip <> ''`)
		if err == nil {
			for rows.Next() {
				var id int64
				var ip string
				if rows.Scan(&id, &ip) == nil {
					if p := net.ParseIP(ip); p != nil {
						g.devices[p.String()] = id
					}
				}
			}
			rows.Close()
		}
		g.loaded = time.Now()
	}
	if p := net.ParseIP(host); p != nil {
		id, ok := g.devices[p.String()]
		return id, ok
	}
	return 0, false
}

// NewSession implements the SMTP backend: only registered devices may
// connect.
func (g *Gateway) NewSession(c *gosmtp.Conn) (gosmtp.Session, error) {
	id, ok := g.deviceFor(context.Background(), c.Conn().RemoteAddr())
	if !ok {
		g.log.Warn("smtp gateway: connection refused", "from", c.Conn().RemoteAddr().String())
		return nil, &gosmtp.SMTPError{Code: 554, Message: "not a registered device"}
	}
	cfg, err := g.Config(context.Background())
	if err != nil {
		return nil, err
	}
	return &session{g: g, deviceID: id, cfg: cfg}, nil
}

type session struct {
	g        *Gateway
	deviceID int64
	cfg      Config
	from     string
	mobiles  []string
	relayTo  []string
}

func (s *session) Reset()        { s.from, s.mobiles, s.relayTo = "", nil, nil }
func (s *session) Logout() error { return nil }

func (s *session) Mail(from string, _ *gosmtp.MailOptions) error {
	s.from = from
	return nil
}

func (s *session) Rcpt(to string, _ *gosmtp.RcptOptions) error {
	local, domain, ok := strings.Cut(strings.ToLower(strings.TrimSpace(to)), "@")
	if ok && domain == s.cfg.Domain {
		m, err := sms.NormalizeMobile(local)
		if err != nil {
			return &gosmtp.SMTPError{Code: 550, Message: "recipient is not a mobile number"}
		}
		s.mobiles = append(s.mobiles, m)
		return nil
	}
	if s.cfg.RelayHost == "" {
		return &gosmtp.SMTPError{Code: 550, Message: "relaying is not configured"}
	}
	s.relayTo = append(s.relayTo, to)
	return nil
}

func (s *session) Data(r io.Reader) error {
	raw, err := io.ReadAll(io.LimitReader(r, 10<<20))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if len(s.relayTo) > 0 {
		if err := s.g.relay(s.cfg, s.from, s.relayTo, raw); err != nil {
			s.g.log.Warn("smtp gateway: relay failed", "err", err)
			return &gosmtp.SMTPError{Code: 451, Message: "relay failed"}
		}
	}
	if len(s.mobiles) == 0 {
		return nil
	}
	code := sms.ParseCode(messageText(raw))
	if code == "" {
		s.g.log.Warn("smtp gateway: no code in message", "device", s.deviceID)
		return &gosmtp.SMTPError{Code: 550, Message: "no code found in the message"}
	}
	var failed []string
	for _, m := range s.mobiles {
		if err := s.g.deliver(ctx, s.deviceID, m, code); err != nil {
			failed = append(failed, m+": "+err.Error())
		}
	}
	if len(failed) > 0 {
		return &gosmtp.SMTPError{Code: 550, Message: strings.Join(failed, "; ")}
	}
	return nil
}

// ErrUnknownMobile means the mobile belongs to no counted two-factor user.
var ErrUnknownMobile = errors.New("mobile is not registered for SMS two-factor")

// deliver sends a code to a mobile that belongs to a synced FortiGate user
// or a TaktaPlus two-factor user, within the license and rate limits.
func (g *Gateway) deliver(ctx context.Context, deviceID int64, mobile, code string) error {
	var user string
	err := g.db.QueryRow(ctx, `SELECT username FROM fortigate_sms_users WHERE mobile = $1
		UNION ALL SELECT username FROM otp_users WHERE mobile = $1 AND active LIMIT 1`, mobile).Scan(&user)
	dev := &deviceID
	if err != nil {
		g.sms.LogBlocked(ctx, mobile, "gateway", "", dev, ErrUnknownMobile.Error())
		return ErrUnknownMobile
	}
	used, err := g.otp.Usage(ctx)
	if err != nil {
		return err
	}
	if used > g.otp.Capacity() {
		g.sms.LogBlocked(ctx, mobile, "gateway", user, dev, "license capacity exceeded")
		return otp.ErrCapacity
	}
	p, err := g.otp.Policy(ctx)
	if err != nil {
		return err
	}
	if n, err := g.sms.SentSince(ctx, mobile, time.Now().Add(-10*time.Minute)); err != nil {
		return err
	} else if n >= p.MaxSends {
		g.sms.LogBlocked(ctx, mobile, "gateway", user, dev, "rate limit")
		return otp.ErrRateLimited
	}
	return g.sms.Send(ctx, mobile, code, "gateway", user, dev)
}

// messageText returns the text of a mail: the first text/plain part, or
// the body with HTML tags removed.
func messageText(raw []byte) string {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return string(raw)
	}
	subject := msg.Header.Get("Subject")
	mt, params, _ := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if strings.HasPrefix(mt, "multipart/") {
		mr := multipart.NewReader(msg.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			if strings.HasPrefix(part.Header.Get("Content-Type"), "text/plain") {
				b, _ := io.ReadAll(io.LimitReader(part, 1<<20))
				return subject + "\n" + string(b)
			}
		}
		return subject
	}
	b, _ := io.ReadAll(io.LimitReader(msg.Body, 1<<20))
	return subject + "\n" + string(b)
}

// relaySMTP forwards a message to the organization's mail server.
func relaySMTP(cfg Config, from string, to []string, msg []byte) error {
	addr := net.JoinHostPort(cfg.RelayHost, strconv.Itoa(cfg.RelayPort))
	var conn net.Conn
	var err error
	d := &net.Dialer{Timeout: 15 * time.Second}
	if cfg.RelaySecurity == "tls" {
		conn, err = tls.DialWithDialer(d, "tcp", addr, &tls.Config{ServerName: cfg.RelayHost, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = d.Dial("tcp", addr)
	}
	if err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	c, err := smtp.NewClient(conn, cfg.RelayHost)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if cfg.RelaySecurity == "starttls" {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: cfg.RelayHost, MinVersion: tls.VersionTLS12}); err != nil {
				return err
			}
		}
	}
	if cfg.RelayUser != "" {
		if err := c.Auth(smtp.PlainAuth("", cfg.RelayUser, cfg.RelayPassword, cfg.RelayHost)); err != nil {
			return err
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, t := range to {
		if err := c.Rcpt(t); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// Serve listens on addr until Close.
func (g *Gateway) Serve(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return g.ServeListener(ln)
}

// ServeListener serves on ln.
func (g *Gateway) ServeListener(ln net.Listener) error {
	g.srv = gosmtp.NewServer(g)
	g.srv.Domain = "taktaplus"
	g.srv.MaxMessageBytes = 10 << 20
	g.srv.MaxRecipients = 20
	g.srv.ReadTimeout = 60 * time.Second
	g.srv.WriteTimeout = 60 * time.Second
	return g.srv.Serve(ln)
}

// Close stops the listener.
func (g *Gateway) Close() error {
	if g.srv == nil {
		return nil
	}
	return g.srv.Close()
}

// SyncUsers records the local users with SMS two-factor on every FortiGate
// (for license counting and to accept their mobiles).
func (g *Gateway) SyncUsers(ctx context.Context) error {
	list, err := g.inv.List(ctx, inventory.Filter{})
	if err != nil {
		return err
	}
	var errs []error
	for _, d := range list {
		if d.Kind != device.KindFortiGate || !d.Operational() {
			continue
		}
		lister, ok := device.AsUserLister(g.inv.Driver(d))
		if !ok {
			continue
		}
		uctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		users, err := lister.ListLocalUsers(uctx)
		cancel()
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", d.Name, err))
			continue
		}
		tx, err := g.db.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM fortigate_sms_users WHERE device_id = $1`, d.ID); err != nil {
			tx.Rollback(ctx)
			return err
		}
		for _, u := range users {
			if u.TwoFactor != "sms" || !u.Enabled {
				continue
			}
			m, _ := sms.NormalizeMobile(u.Phone)
			if _, err := tx.Exec(ctx, `INSERT INTO fortigate_sms_users (device_id, username, mobile) VALUES ($1, $2, $3)
				ON CONFLICT DO NOTHING`, d.ID, u.Name, m); err != nil {
				tx.Rollback(ctx)
				return err
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return errors.Join(errs...)
}

// SMSUser is a FortiGate local user with SMS two-factor.
type SMSUser struct {
	DeviceID   int64     `json:"device_id"`
	DeviceName string    `json:"device_name"`
	Username   string    `json:"username"`
	Mobile     string    `json:"mobile"`
	SeenAt     time.Time `json:"seen_at"`
}

// SMSUsers lists synced FortiGate users.
func (g *Gateway) SMSUsers(ctx context.Context) ([]SMSUser, error) {
	rows, err := g.db.Query(ctx, `SELECT f.device_id, d.name, f.username, f.mobile, f.seen_at FROM fortigate_sms_users f
		JOIN devices d ON d.id = f.device_id ORDER BY lower(f.username), d.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SMSUser{}
	for rows.Next() {
		var u SMSUser
		if err := rows.Scan(&u.DeviceID, &u.DeviceName, &u.Username, &u.Mobile, &u.SeenAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Run syncs users hourly until ctx ends.
func (g *Gateway) Run(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if err := g.SyncUsers(ctx); err != nil && ctx.Err() == nil {
			g.log.Warn("sync FortiGate SMS users", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// FortiGateConfig returns the CLI that sends a FortiGate's SMS mail through
// this gateway.
func (g *Gateway) FortiGateConfig(ctx context.Context, serverIP string, port int) (string, error) {
	cfg, err := g.Config(ctx)
	if err != nil {
		return "", err
	}
	if port == 0 {
		port = 25
	}
	return fmt.Sprintf(`config system email-server
    set server "%s"
    set port %d
    set security none
end
config system sms-server
    edit "TaktaPlus"
        set mail-server "%s"
    next
end
# For each local user:
# config user local
#     edit "<user>"
#         set two-factor sms
#         set sms-server custom
#         set sms-custom-server "TaktaPlus"
#         set sms-phone "09xxxxxxxxx"
#     next
# end`, serverIP, port, cfg.Domain), nil
}
