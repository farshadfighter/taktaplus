// Package sms sends one-time codes by SMS (sms.ir) and logs every message.
package sms

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/farshadfighter/taktaplus/internal/settings"
)

// Provider sends a verification code with a pre-approved template.
type Provider interface {
	SendCode(ctx context.Context, mobile, code string) (id string, err error)
	Credit(ctx context.Context) (float64, error)
}

// Config is the sms.ir setting ("sms").
type Config struct {
	APIKey     string  `json:"api_key"`
	TemplateID int     `json:"template_id"` // quick-send (verify) template
	Param      string  `json:"param"`       // template parameter that receives the code, e.g. CODE
	BaseURL    string  `json:"base_url,omitempty"`
	LowCredit  float64 `json:"low_credit"` // warn below this balance
}

// Redacted hides the API key.
func (c Config) Redacted() Config {
	if c.APIKey != "" {
		c.APIKey = "********"
	}
	return c
}

// Configured reports whether sending is possible.
func (c Config) Configured() bool { return c.APIKey != "" && c.TemplateID > 0 && c.Param != "" }

// SMSIR implements Provider with the sms.ir REST API v1.
type SMSIR struct {
	cfg    Config
	client *http.Client
}

// NewSMSIR returns the provider.
func NewSMSIR(cfg Config) *SMSIR {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.sms.ir"
	}
	return &SMSIR{cfg: cfg, client: &http.Client{Timeout: 20 * time.Second}}
}

type smsirResponse struct {
	Status  int             `json:"status"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (p *SMSIR) call(ctx context.Context, method, path string, body any) (*smsirResponse, error) {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(p.cfg.BaseURL, "/")+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", p.cfg.APIKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sms.ir: %w", err)
	}
	defer resp.Body.Close()
	var out smsirResponse
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("sms.ir: HTTP %d", resp.StatusCode)
	}
	if resp.StatusCode/100 != 2 || out.Status != 1 {
		if out.Message == "" {
			out.Message = resp.Status
		}
		return nil, fmt.Errorf("sms.ir: %s", out.Message)
	}
	return &out, nil
}

// SendCode uses the quick-send (verify) API with the configured template.
func (p *SMSIR) SendCode(ctx context.Context, mobile, code string) (string, error) {
	res, err := p.call(ctx, http.MethodPost, "/v1/send/verify", map[string]any{
		"mobile": mobile, "templateId": p.cfg.TemplateID,
		"parameters": []map[string]string{{"name": p.cfg.Param, "value": code}},
	})
	if err != nil {
		return "", err
	}
	var d struct {
		MessageID json.Number `json:"messageId"`
	}
	_ = json.Unmarshal(res.Data, &d)
	return d.MessageID.String(), nil
}

// Credit returns the account balance.
func (p *SMSIR) Credit(ctx context.Context) (float64, error) {
	res, err := p.call(ctx, http.MethodGet, "/v1/credit", nil)
	if err != nil {
		return 0, err
	}
	var v json.Number
	if err := json.Unmarshal(res.Data, &v); err != nil {
		return 0, err
	}
	return v.Float64()
}

var nonDigit = regexp.MustCompile(`\D`)

// NormalizeMobile turns +98 912…, 98912…, 0912… and 912… into 0912xxxxxxx.
func NormalizeMobile(s string) (string, error) {
	d := nonDigit.ReplaceAllString(s, "")
	switch {
	case strings.HasPrefix(d, "0098"):
		d = "0" + d[4:]
	case strings.HasPrefix(d, "98") && len(d) == 12:
		d = "0" + d[2:]
	case strings.HasPrefix(d, "9") && len(d) == 10:
		d = "0" + d
	}
	if len(d) != 11 || !strings.HasPrefix(d, "09") {
		return "", fmt.Errorf("%q is not an Iranian mobile number", s)
	}
	return d, nil
}

// Message is a logged SMS.
type Message struct {
	ID         int64     `json:"id"`
	At         time.Time `json:"at"`
	Mobile     string    `json:"mobile"`
	Purpose    string    `json:"purpose"`
	Username   string    `json:"username"`
	DeviceID   *int64    `json:"device_id"`
	Status     string    `json:"status"`
	ProviderID string    `json:"provider_id"`
	Error      string    `json:"error"`
}

// ErrNotConfigured means sms.ir is not set up.
var ErrNotConfigured = errors.New("SMS sending is not configured (sms.ir API key and template)")

// Service sends and logs messages.
type Service struct {
	db       *pgxpool.Pool
	settings *settings.Store
	// newProvider is replaceable in tests.
	newProvider func(Config) Provider
}

// New returns the service.
func New(db *pgxpool.Pool, st *settings.Store) *Service {
	return &Service{db: db, settings: st, newProvider: func(c Config) Provider { return NewSMSIR(c) }}
}

// SetProviderFactory replaces the provider (tests).
func (s *Service) SetProviderFactory(f func(Config) Provider) { s.newProvider = f }

// Config returns the stored configuration.
func (s *Service) Config(ctx context.Context) (Config, error) {
	c := Config{Param: "CODE", LowCredit: 50000}
	err := s.settings.Get(ctx, "sms", &c)
	return c, err
}

// SaveConfig stores the configuration; a redacted key keeps the old one.
func (s *Service) SaveConfig(ctx context.Context, c Config) error {
	old, err := s.Config(ctx)
	if err != nil {
		return err
	}
	if c.APIKey == "" || c.APIKey == "********" {
		c.APIKey = old.APIKey
	}
	c.Param = strings.TrimSpace(c.Param)
	if c.TemplateID < 0 {
		return errors.New("template id must be positive")
	}
	return s.settings.Set(ctx, "sms", c)
}

// Send delivers a code and logs the attempt.
func (s *Service) Send(ctx context.Context, mobile, code, purpose, username string, deviceID *int64) error {
	cfg, err := s.Config(ctx)
	if err != nil {
		return err
	}
	m, nerr := NormalizeMobile(mobile)
	if nerr != nil {
		s.log(ctx, mobile, purpose, username, deviceID, "failed", "", nerr.Error())
		return nerr
	}
	if !cfg.Configured() {
		s.log(ctx, m, purpose, username, deviceID, "failed", "", ErrNotConfigured.Error())
		return ErrNotConfigured
	}
	id, err := s.newProvider(cfg).SendCode(ctx, m, code)
	if err != nil {
		s.log(ctx, m, purpose, username, deviceID, "failed", "", err.Error())
		return err
	}
	s.log(ctx, m, purpose, username, deviceID, "sent", id, "")
	return nil
}

// LogBlocked records a message that policy refused to send.
func (s *Service) LogBlocked(ctx context.Context, mobile, purpose, username string, deviceID *int64, reason string) {
	s.log(ctx, mobile, purpose, username, deviceID, "blocked", "", reason)
}

func (s *Service) log(ctx context.Context, mobile, purpose, username string, deviceID *int64, status, pid, msg string) {
	_, _ = s.db.Exec(context.WithoutCancel(ctx), `INSERT INTO sms_messages (mobile, purpose, username, device_id, status, provider_id, error)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, mobile, purpose, username, deviceID, status, pid, truncate(msg, 500))
}

func truncate(v string, n int) string {
	if len(v) > n {
		return v[:n]
	}
	return v
}

// SentSince counts messages sent to mobile since t (for rate limits).
func (s *Service) SentSince(ctx context.Context, mobile string, t time.Time) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `SELECT count(*) FROM sms_messages WHERE mobile = $1 AND status = 'sent' AND at >= $2`, mobile, t).Scan(&n)
	return n, err
}

// Messages returns recent messages, newest first.
func (s *Service) Messages(ctx context.Context, limit int) ([]Message, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.Query(ctx, `SELECT id, at, mobile, purpose, username, device_id, status, provider_id, error
		FROM sms_messages ORDER BY at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Message])
}

// Stats returns sent and failed counts for the last day and month.
func (s *Service) Stats(ctx context.Context) (map[string]int, error) {
	var sentDay, failedDay, sentMonth int
	err := s.db.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE status = 'sent' AND at > now() - interval '1 day'),
		count(*) FILTER (WHERE status <> 'sent' AND at > now() - interval '1 day'),
		count(*) FILTER (WHERE status = 'sent' AND at > now() - interval '30 days')
		FROM sms_messages`).Scan(&sentDay, &failedDay, &sentMonth)
	return map[string]int{"sent_day": sentDay, "failed_day": failedDay, "sent_month": sentMonth}, err
}

// Credit queries the provider balance.
func (s *Service) Credit(ctx context.Context) (float64, error) {
	cfg, err := s.Config(ctx)
	if err != nil {
		return 0, err
	}
	if cfg.APIKey == "" {
		return 0, ErrNotConfigured
	}
	return s.newProvider(cfg).Credit(ctx)
}

// ParseCode extracts the one-time code from a message text.
func ParseCode(text string) string {
	m := codeRe.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return m[1]
}

var codeRe = regexp.MustCompile(`(?:^|\D)(\d{4,8})(?:\D|$)`)
