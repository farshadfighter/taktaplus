package license

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/farshadfighter/taktaplus-lic/pkg/licformat"
)

// UsageFunc reports current consumption (device serials, two-factor users).
type UsageFunc func(ctx context.Context) (*licformat.Usage, error)

// Manager persists the license and talks to the license server.
type Manager struct {
	guard     *Guard
	db        *pgxpool.Pool
	log       *slog.Logger
	serverURL string // base URL of the license server for online activation; may be empty
	hostRoot  string
	usage     UsageFunc
	client    *http.Client
}

// NewManager returns a manager. serverURL is the license server base URL
// (e.g. https://license.example.com); usage may be nil.
func NewManager(g *Guard, db *pgxpool.Pool, log *slog.Logger, serverURL, hostRoot string, usage UsageFunc) *Manager {
	if usage == nil {
		usage = func(context.Context) (*licformat.Usage, error) { return &licformat.Usage{}, nil }
	}
	return &Manager{
		guard: g, db: db, log: log, serverURL: strings.TrimRight(serverURL, "/"), hostRoot: hostRoot, usage: usage,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// Guard returns the entitlement checker.
func (m *Manager) Guard() *Guard { return m.guard }

// Load installs the stored license, if any, and restores the clock floor.
func (m *Manager) Load(ctx context.Context) error {
	var token *string
	var maxSeen *time.Time
	if err := m.db.QueryRow(ctx, `SELECT token, max_seen_time FROM license_state WHERE id = 1`).Scan(&token, &maxSeen); err != nil {
		return err
	}
	if maxSeen != nil {
		m.guard.SetClockFloor(*maxSeen)
	}
	if token == nil {
		return nil
	}
	if _, err := m.guard.Install(*token); err != nil {
		m.log.Warn("stored license is not usable", "err", err)
	}
	return m.TouchClock(ctx)
}

// TouchClock records the current time as seen, so a later clock rollback is
// detected and ignored.
func (m *Manager) TouchClock(ctx context.Context) error {
	now := time.Now()
	m.guard.SetClockFloor(now)
	_, err := m.db.Exec(ctx, `UPDATE license_state SET max_seen_time = GREATEST(coalesce(max_seen_time, $1), $1) WHERE id = 1`, now)
	return err
}

// Install verifies and stores a license token.
func (m *Manager) Install(ctx context.Context, token string) (*licformat.License, error) {
	token = strings.TrimSpace(token)
	lic, err := m.guard.Install(token)
	if err != nil {
		return nil, err
	}
	_, err = m.db.Exec(ctx, `UPDATE license_state SET token = $1, installed_at = now(), last_error = '' WHERE id = 1`, token)
	return lic, err
}

func (m *Manager) fingerprint() licformat.Fingerprint { return CollectFingerprint(m.hostRoot) }

// OfflineRequest returns a .tpreq for activating with key on another machine.
func (m *Manager) OfflineRequest(ctx context.Context, key string) (string, error) {
	if strings.TrimSpace(key) == "" {
		// Without a key this is a renewal request for the installed license.
		u, err := m.usage(ctx)
		if err != nil {
			return "", err
		}
		return m.guard.RefreshRequest(m.fingerprint(), u)
	}
	u, err := m.usage(ctx)
	if err != nil {
		return "", err
	}
	return m.guard.ActivationRequest(key, m.fingerprint(), u)
}

// ServerError is a refusal reported by the license server.
type ServerError struct {
	Status  int
	Message string
}

func (e ServerError) Error() string { return e.Message }

// ErrNoServer means online operations are not configured.
var ErrNoServer = errors.New("license server address is not configured; use offline activation")

func (m *Manager) post(ctx context.Context, url string, reqToken string, out any) (int, error) {
	body, _ := json.Marshal(map[string]string{"request": reqToken})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("cannot reach the license server: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode >= 400 && resp.StatusCode != http.StatusGone {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return resp.StatusCode, ServerError{Status: resp.StatusCode, Message: e.Error}
	}
	return resp.StatusCode, json.Unmarshal(data, out)
}

// ActivateOnline activates key against the license server.
func (m *Manager) ActivateOnline(ctx context.Context, key string) (*licformat.License, error) {
	if m.serverURL == "" {
		return nil, ErrNoServer
	}
	reqToken, err := m.OfflineRequest(ctx, key)
	if err != nil {
		return nil, err
	}
	var res struct {
		License string `json:"license"`
	}
	if _, err := m.post(ctx, m.serverURL+"/api/v1/activate", reqToken, &res); err != nil {
		return nil, err
	}
	return m.Install(ctx, res.License)
}

// Heartbeat checks in with the license server named in the license.
func (m *Manager) Heartbeat(ctx context.Context) (string, error) {
	lic, err := m.guard.License()
	if err != nil {
		return "", err
	}
	if lic.Online == nil {
		return "", errors.New("license is offline-only")
	}
	u, err := m.usage(ctx)
	if err != nil {
		return "", err
	}
	reqToken, err := m.guard.RefreshRequest(m.fingerprint(), u)
	if err != nil {
		return "", err
	}
	var res struct {
		Status  string `json:"status"`
		License string `json:"license"`
	}
	_, err = m.post(ctx, lic.Online.HeartbeatURL, reqToken, &res)
	status := res.Status
	switch {
	case err != nil:
		status = "error"
	case res.Status == "updated":
		if _, err = m.Install(ctx, res.License); err != nil {
			status = "error"
		}
	case res.Status == "revoked":
		m.guard.Clear()
		_, err = m.db.Exec(ctx, `UPDATE license_state SET token = NULL WHERE id = 1`)
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if _, dbErr := m.db.Exec(ctx, `UPDATE license_state SET last_heartbeat_at = now(), last_heartbeat_status = $1, last_error = $2 WHERE id = 1`,
		status, msg); dbErr != nil && err == nil {
		err = dbErr
	}
	return status, err
}

// Run performs heartbeats on the license's interval and records the clock
// hourly, until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	clock := time.NewTicker(time.Hour)
	defer clock.Stop()
	next := time.Now().Add(time.Minute)
	beat := time.NewTimer(time.Until(next))
	defer beat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-clock.C:
			if err := m.TouchClock(ctx); err != nil {
				m.log.Warn("record clock", "err", err)
			}
		case <-beat.C:
			interval := 24 * time.Hour
			if lic, err := m.guard.License(); err == nil && lic.Online != nil {
				if lic.Online.IntervalHours > 0 {
					interval = time.Duration(lic.Online.IntervalHours) * time.Hour
				}
				status, err := m.Heartbeat(ctx)
				m.log.Info("license heartbeat", "status", status, "err", err)
				if err != nil {
					interval = time.Hour // retry sooner after a failure
				}
			}
			beat.Reset(interval)
		}
	}
}

// State is the stored online state of the license.
type State struct {
	InstalledAt         *time.Time `json:"installed_at"`
	LastHeartbeatAt     *time.Time `json:"last_heartbeat_at"`
	LastHeartbeatStatus string     `json:"last_heartbeat_status"`
	LastError           string     `json:"last_error"`
}

// State returns the stored online state.
func (m *Manager) State(ctx context.Context) (State, error) {
	var s State
	err := m.db.QueryRow(ctx, `SELECT installed_at, last_heartbeat_at, last_heartbeat_status, last_error FROM license_state WHERE id = 1`).
		Scan(&s.InstalledAt, &s.LastHeartbeatAt, &s.LastHeartbeatStatus, &s.LastError)
	return s, err
}

// ServerURL returns the license server base URL: the configured one, or the
// one the installed license names for heartbeats.
func (m *Manager) ServerURL() string {
	if m.serverURL != "" {
		return m.serverURL
	}
	if lic, err := m.guard.License(); err == nil && lic.Online != nil {
		return strings.TrimSuffix(strings.TrimRight(lic.Online.HeartbeatURL, "/"), "/api/v1/heartbeat")
	}
	return ""
}

// OnlineConfigured reports whether online activation is possible.
func (m *Manager) OnlineConfigured() bool { return m.serverURL != "" }
