// Package alerts evaluates alert rules against device state and sends
// notifications.
package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/farshadfighter/taktaplus-lic/pkg/licformat"

	"github.com/farshadfighter/taktaplus/internal/inventory"
	"github.com/farshadfighter/taktaplus/internal/license"
	"github.com/farshadfighter/taktaplus/internal/secretbox"
)

const channelContext = "notification-channel"

// Rule kinds.
const (
	KindOffline        = "offline"
	KindCPU            = "cpu"
	KindMem            = "mem"
	KindDisk           = "disk"
	KindContractExpiry = "contract_expiry"
	KindLicenseExpiry  = "license_expiry"
)

var kinds = []string{KindOffline, KindCPU, KindMem, KindDisk, KindContractExpiry, KindLicenseExpiry}

// InputError is a validation failure to show to the user.
type InputError struct{ msg string }

func (e InputError) Error() string { return e.msg }

func invalidf(format string, a ...any) error { return InputError{fmt.Sprintf(format, a...)} }

// ErrNotFound means the rule, channel or alert does not exist.
var ErrNotFound = errors.New("not found")

// Rule is an alert rule.
type Rule struct {
	ID              int64   `json:"id"`
	Name            string  `json:"name"`
	Kind            string  `json:"kind"`
	Threshold       float64 `json:"threshold"`
	DurationMinutes int     `json:"duration_minutes"`
	Severity        string  `json:"severity"`
	GroupID         *int64  `json:"group_id"`
	ChannelIDs      []int64 `json:"channel_ids"`
	RepeatMinutes   int     `json:"repeat_minutes"`
	Enabled         bool    `json:"enabled"`
}

func (r *Rule) validate() error {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" || len(r.Name) > 100 {
		return invalidf("rule name is required (at most 100 characters)")
	}
	if !slices.Contains(kinds, r.Kind) {
		return invalidf("unknown rule kind")
	}
	if r.Severity == "" {
		r.Severity = "warning"
	}
	if r.Severity != "info" && r.Severity != "warning" && r.Severity != "critical" {
		return invalidf("severity must be info, warning or critical")
	}
	switch r.Kind {
	case KindCPU, KindMem, KindDisk:
		if r.Threshold <= 0 || r.Threshold > 100 {
			return invalidf("threshold must be a percentage between 1 and 100")
		}
	case KindContractExpiry, KindLicenseExpiry:
		if r.Threshold < 0 || r.Threshold > 365 {
			return invalidf("threshold must be between 0 and 365 days")
		}
	}
	if r.DurationMinutes < 0 || r.DurationMinutes > 1440 || r.RepeatMinutes < 0 || r.RepeatMinutes > 10080 {
		return invalidf("duration or repeat interval is out of range")
	}
	if r.ChannelIDs == nil {
		r.ChannelIDs = []int64{}
	}
	return nil
}

// Channel is a notification destination.
type Channel struct {
	ID      int64         `json:"id"`
	Name    string        `json:"name"`
	Kind    string        `json:"kind"`
	Config  ChannelConfig `json:"config"`
	Enabled bool          `json:"enabled"`
}

// Alert is an open or resolved alert.
type Alert struct {
	ID         int64      `json:"id"`
	RuleID     int64      `json:"rule_id"`
	RuleName   string     `json:"rule_name"`
	Kind       string     `json:"kind"`
	DeviceID   int64      `json:"device_id"`
	DeviceName string     `json:"device_name"`
	Subject    string     `json:"subject"`
	Severity   string     `json:"severity"`
	Message    string     `json:"message"`
	State      string     `json:"state"`
	OpenedAt   time.Time  `json:"opened_at"`
	ResolvedAt *time.Time `json:"resolved_at"`
	AckedBy    string     `json:"acked_by"`
	AckedAt    *time.Time `json:"acked_at"`
}

// Service stores rules, channels and alerts and evaluates rules.
type Service struct {
	db    *pgxpool.Pool
	box   *secretbox.Box
	inv   *inventory.Service
	guard *license.Guard
	log   *slog.Logger
	send  sender
	// mailer sends report emails; tests replace it.
	mailer func(ctx context.Context, c ChannelConfig, to []string, subject, body string, files []Attachment, at time.Time) error
	now    func() time.Time
}

// New returns the alert service.
func New(db *pgxpool.Pool, box *secretbox.Box, inv *inventory.Service, guard *license.Guard, log *slog.Logger) *Service {
	return &Service{db: db, box: box, inv: inv, guard: guard, log: log, send: send, mailer: sendMail, now: time.Now}
}

// --- rules ---

// ListRules returns all rules.
func (s *Service) ListRules(ctx context.Context) ([]Rule, error) {
	rows, err := s.db.Query(ctx, `SELECT id, name, kind, threshold, duration_minutes, severity, group_id, channel_ids, repeat_minutes, enabled
		FROM alert_rules ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Rule])
}

// SaveRule creates (ID 0) or updates a rule.
func (s *Service) SaveRule(ctx context.Context, r Rule) (int64, error) {
	if err := r.validate(); err != nil {
		return 0, err
	}
	if r.ID == 0 {
		err := s.db.QueryRow(ctx, `INSERT INTO alert_rules (name, kind, threshold, duration_minutes, severity, group_id, channel_ids, repeat_minutes, enabled)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
			r.Name, r.Kind, r.Threshold, r.DurationMinutes, r.Severity, r.GroupID, r.ChannelIDs, r.RepeatMinutes, r.Enabled).Scan(&r.ID)
		return r.ID, err
	}
	tag, err := s.db.Exec(ctx, `UPDATE alert_rules SET name = $2, kind = $3, threshold = $4, duration_minutes = $5, severity = $6,
		group_id = $7, channel_ids = $8, repeat_minutes = $9, enabled = $10 WHERE id = $1`,
		r.ID, r.Name, r.Kind, r.Threshold, r.DurationMinutes, r.Severity, r.GroupID, r.ChannelIDs, r.RepeatMinutes, r.Enabled)
	if err == nil && tag.RowsAffected() == 0 {
		return 0, ErrNotFound
	}
	return r.ID, err
}

// DeleteRule removes a rule and its alerts.
func (s *Service) DeleteRule(ctx context.Context, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM alert_rules WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// --- channels ---

func (s *Service) loadChannels(ctx context.Context, where string, args ...any) ([]Channel, error) {
	rows, err := s.db.Query(ctx, `SELECT id, name, kind, config, enabled FROM notification_channels `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Channel{}
	for rows.Next() {
		var c Channel
		var sealed []byte
		if err := rows.Scan(&c.ID, &c.Name, &c.Kind, &sealed, &c.Enabled); err != nil {
			return nil, err
		}
		plain, err := s.box.Open(sealed, channelContext)
		if err != nil {
			return nil, fmt.Errorf("channel %d: %w", c.ID, err)
		}
		if err := json.Unmarshal(plain, &c.Config); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListChannels returns channels with secrets redacted.
func (s *Service) ListChannels(ctx context.Context) ([]Channel, error) {
	list, err := s.loadChannels(ctx, "")
	for i := range list {
		list[i].Config = list[i].Config.redacted()
	}
	return list, err
}

// SaveChannel creates (ID 0) or updates a channel. A redacted or empty
// secret keeps the stored one.
func (s *Service) SaveChannel(ctx context.Context, c Channel) (int64, error) {
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		return 0, invalidf("channel name is required")
	}
	if c.ID != 0 {
		old, err := s.loadChannels(ctx, "WHERE id = $1", c.ID)
		if err != nil {
			return 0, err
		}
		if len(old) == 0 {
			return 0, ErrNotFound
		}
		if c.Config.SMTPPassword == "" || c.Config.SMTPPassword == "********" {
			c.Config.SMTPPassword = old[0].Config.SMTPPassword
		}
		if c.Config.Secret == "" || c.Config.Secret == "********" {
			c.Config.Secret = old[0].Config.Secret
		}
	}
	if err := c.Config.validate(c.Kind); err != nil {
		return 0, err
	}
	data, _ := json.Marshal(c.Config)
	sealed := s.box.Seal(data, channelContext)
	if c.ID == 0 {
		err := s.db.QueryRow(ctx, `INSERT INTO notification_channels (name, kind, config, enabled) VALUES ($1, $2, $3, $4) RETURNING id`,
			c.Name, c.Kind, sealed, c.Enabled).Scan(&c.ID)
		return c.ID, err
	}
	_, err := s.db.Exec(ctx, `UPDATE notification_channels SET name = $2, kind = $3, config = $4, enabled = $5 WHERE id = $1`,
		c.ID, c.Name, c.Kind, sealed, c.Enabled)
	return c.ID, err
}

// DeleteChannel removes a channel and unlinks it from rules.
func (s *Service) DeleteChannel(ctx context.Context, id int64) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `DELETE FROM notification_channels WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `UPDATE alert_rules SET channel_ids = array_remove(channel_ids, $1)`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// TestChannel sends a test message.
func (s *Service) TestChannel(ctx context.Context, id int64) error {
	list, err := s.loadChannels(ctx, "WHERE id = $1", id)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		return ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return s.send(ctx, list[0].Kind, list[0].Config, Message{
		Event: "test", Severity: "info", Rule: "Test", Device: "TaktaPlus", Text: "This is a test notification from TaktaPlus.", At: s.now(),
	})
}

// EmailChannels lists the enabled email channels (for report delivery).
func (s *Service) EmailChannels(ctx context.Context) ([]Channel, error) {
	list, err := s.loadChannels(ctx, "WHERE kind = $1 AND enabled", ChannelEmail)
	for i := range list {
		list[i].Config = list[i].Config.redacted()
	}
	return list, err
}

// SendMail sends an email with attachments through an email channel, to
// the given recipients or, when none are given, the channel's own.
func (s *Service) SendMail(ctx context.Context, channelID int64, to []string, subject, body string, files []Attachment) error {
	list, err := s.loadChannels(ctx, "WHERE id = $1 AND kind = $2", channelID, ChannelEmail)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		return ErrNotFound
	}
	c := list[0].Config
	if len(to) == 0 {
		to = c.To
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	return s.mailer(ctx, c, to, subject, body, files, s.now())
}

// SetMailer replaces email delivery (for tests of other packages).
func (s *Service) SetMailer(f func(ctx context.Context, c ChannelConfig, to []string, subject, body string, files []Attachment, at time.Time) error) {
	s.mailer = f
}

// --- alerts ---

// List returns alerts, newest first. state is "open", "resolved" or "".
func (s *Service) List(ctx context.Context, state string, deviceID int64, limit int) ([]Alert, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.Query(ctx, `
		SELECT a.id, a.rule_id, r.name, r.kind, a.device_id, d.name, a.subject, a.severity, a.message, a.state,
		       a.opened_at, a.resolved_at, a.acked_by, a.acked_at
		FROM alerts a JOIN alert_rules r ON r.id = a.rule_id JOIN devices d ON d.id = a.device_id
		WHERE ($1 = '' OR a.state = $1) AND ($2 = 0 OR a.device_id = $2)
		ORDER BY a.state = 'open' DESC, a.opened_at DESC LIMIT $3`, state, deviceID, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Alert])
}

// Ack marks an open alert as acknowledged; it stops repeat notifications.
func (s *Service) Ack(ctx context.Context, id int64, by string) error {
	tag, err := s.db.Exec(ctx, `UPDATE alerts SET acked_by = $2, acked_at = now() WHERE id = $1 AND state = 'open'`, id, by)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// OpenCounts returns open alerts per severity.
func (s *Service) OpenCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.Query(ctx, `SELECT severity, count(*) FROM alerts WHERE state = 'open' GROUP BY severity`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{"info": 0, "warning": 0, "critical": 0}
	for rows.Next() {
		var sev string
		var n int
		if err := rows.Scan(&sev, &n); err != nil {
			return nil, err
		}
		out[sev] = n
	}
	return out, rows.Err()
}

// --- evaluation ---

// condition is one firing instance of a rule on a device.
type condition struct {
	subject string
	message string
}

func (s *Service) conditions(r Rule, d *inventory.Device, st *inventory.Status, lic *licformat.License, now time.Time) []condition {
	fresh := st != nil && now.Sub(st.At) < 10*time.Minute
	pct := func(name string, v float64) []condition {
		if fresh && v >= r.Threshold {
			return []condition{{message: fmt.Sprintf("%s usage is %.0f%% (threshold %.0f%%)", name, v, r.Threshold)}}
		}
		return nil
	}
	switch r.Kind {
	case KindOffline:
		if d.Status == "offline" || d.Status == "error" {
			msg := "device is unreachable"
			if d.Status == "error" {
				msg = "device refused the connection"
			}
			if d.LastError != "" {
				msg += ": " + d.LastError
			}
			return []condition{{message: msg}}
		}
	case KindCPU:
		if st != nil {
			return pct("CPU", st.CPU)
		}
	case KindMem:
		if st != nil {
			return pct("Memory", st.Mem)
		}
	case KindDisk:
		if st != nil && st.Disk >= 0 {
			return pct("Disk", st.Disk)
		}
	case KindContractExpiry:
		if st == nil {
			return nil
		}
		var out []condition
		limit := now.Add(time.Duration(r.Threshold*24) * time.Hour)
		for _, c := range st.Contracts {
			if c.Expires == nil || c.Expires.After(limit) || c.Expires.Before(now.AddDate(0, 0, -30)) {
				continue
			}
			out = append(out, condition{subject: c.Name, message: expiryText("FortiGuard contract "+c.Name, *c.Expires, now)})
		}
		return out
	case KindLicenseExpiry:
		if lic == nil || d.Serial == "" {
			return nil
		}
		for _, item := range lic.Devices {
			if item.Serial != d.Serial {
				continue
			}
			end := item.Expires.EndOfDay()
			if end.Before(now.Add(time.Duration(r.Threshold*24) * time.Hour)) {
				msg := expiryText("TaktaPlus subscription", end, now)
				if st := lic.DeviceStatus(d.Serial, now); st == licformat.StatusGrace {
					msg += "; in grace period until " + end.AddDate(0, 0, lic.GraceDays).Format("2006-01-02")
				} else if st == licformat.StatusExpired {
					msg += "; the device is now read-only"
				}
				return []condition{{message: msg}}
			}
		}
	}
	return nil
}

func expiryText(what string, at, now time.Time) string {
	days := int(math.Ceil(at.Sub(now).Hours() / 24))
	if days < 0 {
		return fmt.Sprintf("%s expired %d days ago (%s)", what, -days, at.Format("2006-01-02"))
	}
	return fmt.Sprintf("%s expires in %d days (%s)", what, days, at.Format("2006-01-02"))
}

// Evaluate checks every enabled rule against every device once, opening,
// resolving and notifying alerts.
func (s *Service) Evaluate(ctx context.Context) error {
	now := s.now()
	rules, err := s.ListRules(ctx)
	if err != nil {
		return err
	}
	devices, err := s.inv.List(ctx, inventory.Filter{})
	if err != nil {
		return err
	}
	statuses, err := s.inv.Statuses(ctx)
	if err != nil {
		return err
	}
	lic, _ := s.guard.License()
	var notes []pending
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		for _, d := range devices {
			// Expired and unlicensed devices are not polled; only the
			// license rule still applies to them.
			if r.Kind != KindLicenseExpiry && !d.Operational() {
				continue
			}
			var conds []condition
			if r.GroupID == nil || (d.GroupID != nil && *d.GroupID == *r.GroupID) {
				conds = s.conditions(r, d, statuses[d.ID], lic, now)
			}
			n, err := s.apply(ctx, r, d, conds, now)
			if err != nil {
				return err
			}
			notes = append(notes, n...)
		}
	}
	s.notify(ctx, notes)
	return nil
}

type pending struct {
	alertID int64
	rule    Rule
	msg     Message
}

// apply reconciles stored alerts of (rule, device) with the current
// conditions and returns the notifications to send.
func (s *Service) apply(ctx context.Context, r Rule, d *inventory.Device, conds []condition, now time.Time) ([]pending, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var notes []pending
	msg := func(event, text string) Message {
		return Message{Event: event, Severity: r.Severity, Rule: r.Name, Device: d.Name, Serial: d.Serial, Text: text, At: now}
	}
	subjects := []string{}
	for _, c := range conds {
		subjects = append(subjects, c.subject)
		if r.DurationMinutes > 0 {
			var since time.Time
			if err := tx.QueryRow(ctx, `INSERT INTO alert_pending (rule_id, device_id, subject, since) VALUES ($1, $2, $3, $4)
				ON CONFLICT (rule_id, device_id, subject) DO UPDATE SET since = alert_pending.since RETURNING since`,
				r.ID, d.ID, c.subject, now).Scan(&since); err != nil {
				return nil, err
			}
			if now.Sub(since) < time.Duration(r.DurationMinutes)*time.Minute {
				continue
			}
		}
		var id int64
		var lastNotified *time.Time
		var acked bool
		err := tx.QueryRow(ctx, `SELECT id, last_notified_at, acked_at IS NOT NULL FROM alerts
			WHERE rule_id = $1 AND device_id = $2 AND subject = $3 AND state = 'open'`, r.ID, d.ID, c.subject).Scan(&id, &lastNotified, &acked)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			if err := tx.QueryRow(ctx, `INSERT INTO alerts (rule_id, device_id, subject, severity, message, opened_at)
				VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`, r.ID, d.ID, c.subject, r.Severity, c.message, now).Scan(&id); err != nil {
				return nil, err
			}
			notes = append(notes, pending{id, r, msg("opened", c.message)})
		case err != nil:
			return nil, err
		default:
			if _, err := tx.Exec(ctx, `UPDATE alerts SET message = $2 WHERE id = $1`, id, c.message); err != nil {
				return nil, err
			}
			if r.RepeatMinutes > 0 && !acked && (lastNotified == nil || now.Sub(*lastNotified) >= time.Duration(r.RepeatMinutes)*time.Minute) {
				notes = append(notes, pending{id, r, msg("opened", c.message)})
			}
		}
	}
	// Conditions that no longer hold.
	if _, err := tx.Exec(ctx, `DELETE FROM alert_pending WHERE rule_id = $1 AND device_id = $2 AND NOT (subject = ANY($3))`,
		r.ID, d.ID, subjects); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `UPDATE alerts SET state = 'resolved', resolved_at = $4
		WHERE rule_id = $1 AND device_id = $2 AND state = 'open' AND NOT (subject = ANY($3)) RETURNING id, message`,
		r.ID, d.ID, subjects, now)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var text string
		if err := rows.Scan(&id, &text); err != nil {
			rows.Close()
			return nil, err
		}
		notes = append(notes, pending{0, r, msg("resolved", "Resolved: "+text)})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return notes, tx.Commit(ctx)
}

func (s *Service) notify(ctx context.Context, notes []pending) {
	if len(notes) == 0 {
		return
	}
	channels, err := s.loadChannels(ctx, "WHERE enabled")
	if err != nil {
		s.log.Error("load notification channels", "err", err)
		return
	}
	for _, n := range notes {
		for _, c := range channels {
			if !slices.Contains(n.rule.ChannelIDs, c.ID) {
				continue
			}
			sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			if err := s.send(sctx, c.Kind, c.Config, n.msg); err != nil {
				s.log.Warn("notification failed", "channel", c.Name, "err", err)
			}
			cancel()
		}
		if n.alertID != 0 {
			if _, err := s.db.Exec(ctx, `UPDATE alerts SET last_notified_at = $2 WHERE id = $1`, n.alertID, n.msg.At); err != nil {
				s.log.Warn("record notification", "err", err)
			}
		}
	}
}
