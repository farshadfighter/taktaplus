package alerts

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Channel kinds.
const (
	ChannelEmail   = "email"
	ChannelWebhook = "webhook"
	ChannelSyslog  = "syslog"
)

// ChannelConfig holds the settings of every kind; only the fields of the
// channel's kind are used. It is stored sealed.
type ChannelConfig struct {
	// email
	SMTPHost     string   `json:"smtp_host,omitempty"`
	SMTPPort     int      `json:"smtp_port,omitempty"`
	SMTPSecurity string   `json:"smtp_security,omitempty"` // starttls, tls, none
	SMTPUser     string   `json:"smtp_user,omitempty"`
	SMTPPassword string   `json:"smtp_password,omitempty"`
	From         string   `json:"from,omitempty"`
	To           []string `json:"to,omitempty"`
	// webhook
	URL    string `json:"url,omitempty"`
	Secret string `json:"secret,omitempty"` // HMAC-SHA256 key for X-TaktaPlus-Signature
	// syslog
	Address  string `json:"address,omitempty"`  // host:port
	Protocol string `json:"protocol,omitempty"` // udp, tcp
}

// redacted returns a copy without secrets, for the API.
func (c ChannelConfig) redacted() ChannelConfig {
	if c.SMTPPassword != "" {
		c.SMTPPassword = "********"
	}
	if c.Secret != "" {
		c.Secret = "********"
	}
	return c
}

func (c *ChannelConfig) validate(kind string) error {
	switch kind {
	case ChannelEmail:
		if c.SMTPHost == "" {
			return invalidf("SMTP server is required")
		}
		if c.SMTPPort == 0 {
			c.SMTPPort = 587
		}
		if c.SMTPSecurity == "" {
			c.SMTPSecurity = "starttls"
		}
		if c.SMTPSecurity != "starttls" && c.SMTPSecurity != "tls" && c.SMTPSecurity != "none" {
			return invalidf("SMTP security must be starttls, tls or none")
		}
		if _, err := mail.ParseAddress(c.From); err != nil {
			return invalidf("sender address is not valid")
		}
		if len(c.To) == 0 {
			return invalidf("at least one recipient is required")
		}
		for _, to := range c.To {
			if _, err := mail.ParseAddress(to); err != nil {
				return invalidf("recipient %q is not valid", to)
			}
		}
	case ChannelWebhook:
		u, err := url.Parse(c.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return invalidf("webhook URL must be an http(s) URL")
		}
	case ChannelSyslog:
		if _, _, err := net.SplitHostPort(c.Address); err != nil {
			return invalidf("syslog address must be host:port")
		}
		if c.Protocol == "" {
			c.Protocol = "udp"
		}
		if c.Protocol != "udp" && c.Protocol != "tcp" {
			return invalidf("syslog protocol must be udp or tcp")
		}
	default:
		return invalidf("unknown channel kind")
	}
	return nil
}

// Message is one notification.
type Message struct {
	Event    string    `json:"event"` // opened, resolved, test
	Severity string    `json:"severity"`
	Rule     string    `json:"rule"`
	Device   string    `json:"device"`
	Serial   string    `json:"serial"`
	Text     string    `json:"message"`
	At       time.Time `json:"at"`
}

func (m Message) subject() string {
	prefix := strings.ToUpper(m.Severity)
	if m.Event == "resolved" {
		prefix = "RESOLVED"
	}
	return fmt.Sprintf("[TaktaPlus] %s: %s — %s", prefix, m.Rule, m.Device)
}

func (m Message) body() string {
	return fmt.Sprintf("%s\n\nDevice: %s (%s)\nRule: %s\nSeverity: %s\nTime: %s\n",
		m.Text, m.Device, m.Serial, m.Rule, m.Severity, m.At.Format(time.RFC3339))
}

// sender delivers a message through one channel kind. Tests replace it.
type sender func(ctx context.Context, kind string, c ChannelConfig, m Message) error

func send(ctx context.Context, kind string, c ChannelConfig, m Message) error {
	switch kind {
	case ChannelEmail:
		return sendEmail(ctx, c, m)
	case ChannelWebhook:
		return sendWebhook(ctx, c, m)
	case ChannelSyslog:
		return sendSyslog(ctx, c, m)
	}
	return fmt.Errorf("unknown channel kind %q", kind)
}

func sendEmail(ctx context.Context, c ChannelConfig, m Message) error {
	addr := net.JoinHostPort(c.SMTPHost, strconv.Itoa(c.SMTPPort))
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	var err error
	if c.SMTPSecurity == "tls" {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: &tls.Config{ServerName: c.SMTPHost, MinVersion: tls.VersionTLS12}}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	client, err := smtp.NewClient(conn, c.SMTPHost)
	if err != nil {
		conn.Close()
		return err
	}
	defer client.Close()
	if c.SMTPSecurity == "starttls" {
		if err := client.StartTLS(&tls.Config{ServerName: c.SMTPHost, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if c.SMTPUser != "" {
		if err := client.Auth(smtp.PlainAuth("", c.SMTPUser, c.SMTPPassword, c.SMTPHost)); err != nil {
			return err
		}
	}
	from, _ := mail.ParseAddress(c.From)
	if err := client.Mail(from.Address); err != nil {
		return err
	}
	var to []string
	for _, t := range c.To {
		a, _ := mail.ParseAddress(t)
		if err := client.Rcpt(a.Address); err != nil {
			return err
		}
		to = append(to, a.String())
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n",
		from.String(), strings.Join(to, ", "), mime.QEncoding.Encode("utf-8", m.subject()), m.At.Format(time.RFC1123Z))
	msg.WriteString(strings.ReplaceAll(m.body(), "\n", "\r\n"))
	if _, err := w.Write(msg.Bytes()); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}

var webhookClient = &http.Client{Timeout: 15 * time.Second}

func sendWebhook(ctx context.Context, c ChannelConfig, m Message) error {
	body, _ := json.Marshal(m)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "TaktaPlus")
	if c.Secret != "" {
		mac := hmac.New(sha256.New, []byte(c.Secret))
		mac.Write(body)
		req.Header.Set("X-TaktaPlus-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	resp, err := webhookClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("webhook answered %s", resp.Status)
	}
	return nil
}

// sendSyslog writes an RFC 5424 message (facility local0).
func sendSyslog(ctx context.Context, c ChannelConfig, m Message) error {
	sev := map[string]int{"critical": 2, "warning": 4, "info": 6}[m.Severity]
	if m.Event == "resolved" {
		sev = 5
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "-"
	}
	text := strings.ReplaceAll(m.subject()+": "+m.Text, "\n", " ")
	line := fmt.Sprintf("<%d>1 %s %s taktaplus - alert - %s", 16*8+sev, m.At.UTC().Format(time.RFC3339), host, text)
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, c.Protocol, c.Address)
	if err != nil {
		return err
	}
	defer conn.Close()
	if c.Protocol == "tcp" {
		line = strconv.Itoa(len(line)) + " " + line // octet counting (RFC 6587)
	}
	_, err = conn.Write([]byte(line))
	return err
}
