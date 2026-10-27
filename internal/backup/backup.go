// Package backup takes, stores, compares and restores device configuration
// backups.
package backup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/robfig/cron/v3"

	"github.com/farshadfighter/taktaplus/internal/device"
	"github.com/farshadfighter/taktaplus/internal/inventory"
	"github.com/farshadfighter/taktaplus/internal/secretbox"
)

// Reasons a backup was taken.
const (
	ReasonScheduled  = "scheduled"
	ReasonManual     = "manual"
	ReasonPreRestore = "pre_restore"
	ReasonOnChange   = "on_change"
	ReasonPreChange  = "pre_change"
)

// InputError is a validation failure to show to the user.
type InputError struct{ msg string }

func (e InputError) Error() string { return e.msg }

func invalidf(format string, a ...any) error { return InputError{fmt.Sprintf(format, a...)} }

var (
	// ErrNotFound means the backup, policy or job does not exist.
	ErrNotFound = errors.New("not found")
	// ErrNotLicensed means the device's license does not allow operations.
	ErrNotLicensed = errors.New("this device's license is not active; only existing backups can be viewed and downloaded")
	// ErrRestoreRunning means a restore is already in progress on the device.
	ErrRestoreRunning = errors.New("a restore is already in progress on this device")
)

// Backup is one stored (or failed) backup.
type Backup struct {
	ID         int64     `json:"id"`
	DeviceID   int64     `json:"device_id"`
	DeviceName string    `json:"device_name"`
	At         time.Time `json:"at"`
	Reason     string    `json:"reason"`
	Actor      string    `json:"actor"`
	Status     string    `json:"status"`
	Error      string    `json:"error"`
	SHA256     string    `json:"sha256"`
	Size       int64     `json:"size"`
	Firmware   string    `json:"firmware"`
	Locked     bool      `json:"locked"`
	Note       string    `json:"note"`
	path       string
}

// Options tune the service. Zero values take the defaults.
type Options struct {
	Timeout     time.Duration // device backup or restore call, default 2m
	RebootWait  time.Duration // wait before checking a restored device, default 60s
	RebootPoll  time.Duration // between checks, default 20s
	RebootLimit time.Duration // give up after, default 15m
	Concurrency int           // scheduled backups in parallel, default 4
	FailedKeep  time.Duration // failed-backup rows, default 30 days
}

// Service manages backups.
type Service struct {
	db  *pgxpool.Pool
	box *secretbox.Box
	inv *inventory.Service
	dir string
	log *slog.Logger
	opt Options
	now func() time.Time

	wg sync.WaitGroup // background restore watchers
}

// New returns the service; dir holds the encrypted files.
func New(db *pgxpool.Pool, box *secretbox.Box, inv *inventory.Service, dir string, log *slog.Logger, opt Options) *Service {
	def := func(v *time.Duration, d time.Duration) {
		if *v == 0 {
			*v = d
		}
	}
	def(&opt.Timeout, 2*time.Minute)
	def(&opt.RebootWait, time.Minute)
	def(&opt.RebootPoll, 20*time.Second)
	def(&opt.RebootLimit, 15*time.Minute)
	def(&opt.FailedKeep, 30*24*time.Hour)
	if opt.Concurrency <= 0 {
		opt.Concurrency = 4
	}
	return &Service{db: db, box: box, inv: inv, dir: dir, log: log, opt: opt, now: time.Now}
}

// Wait blocks until background restore watchers finish (for tests and
// shutdown).
func (s *Service) Wait() { s.wg.Wait() }

func fileContext(deviceID int64) string { return "backup:" + strconv.FormatInt(deviceID, 10) }

const backupCols = `b.id, b.device_id, d.name, b.at, b.reason, b.actor, b.status, b.error, b.sha256, b.size, b.firmware, b.locked, b.note, b.path`

func scanBackup(row pgx.Row) (*Backup, error) {
	var b Backup
	err := row.Scan(&b.ID, &b.DeviceID, &b.DeviceName, &b.At, &b.Reason, &b.Actor, &b.Status, &b.Error, &b.SHA256, &b.Size,
		&b.Firmware, &b.Locked, &b.Note, &b.path)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &b, err
}

// Get returns one backup.
func (s *Service) Get(ctx context.Context, id int64) (*Backup, error) {
	return scanBackup(s.db.QueryRow(ctx, `SELECT `+backupCols+` FROM backups b JOIN devices d ON d.id = b.device_id WHERE b.id = $1`, id))
}

// List returns backups, newest first; deviceID 0 lists all devices.
func (s *Service) List(ctx context.Context, deviceID int64, limit int) ([]*Backup, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.Query(ctx, `SELECT `+backupCols+` FROM backups b JOIN devices d ON d.id = b.device_id
		WHERE ($1 = 0 OR b.device_id = $1) ORDER BY b.at DESC, b.id DESC LIMIT $2`, deviceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Backup{}
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Run takes a backup of a device now. When the configuration equals the
// latest stored one, no new backup is stored and that one is returned with
// unchanged = true (pre-restore backups are always stored).
func (s *Service) Run(ctx context.Context, deviceID int64, reason, actor string) (b *Backup, unchanged bool, err error) {
	d, err := s.inv.Get(ctx, deviceID)
	if err != nil {
		if errors.Is(err, inventory.ErrNotFound) {
			return nil, false, ErrNotFound
		}
		return nil, false, err
	}
	if !d.Operational() {
		return nil, false, ErrNotLicensed
	}
	cctx, cancel := context.WithTimeout(ctx, s.opt.Timeout)
	cfg, err := s.inv.Driver(d).BackupConfig(cctx)
	cancel()
	if err != nil {
		msg := err.Error()
		if errors.Is(err, device.ErrUnsupported) {
			msg = "backup is not available over the configured transports (add SSH credentials)"
		}
		_, dbErr := s.db.Exec(ctx, `INSERT INTO backups (device_id, at, reason, actor, status, error, firmware) VALUES ($1, $2, $3, $4, 'failed', $5, $6)`,
			deviceID, s.now(), reason, actor, truncate(msg, 500), d.Firmware)
		if dbErr != nil {
			s.log.Error("record failed backup", "err", dbErr)
		}
		return nil, false, fmt.Errorf("backup of %s failed: %s", d.Name, msg)
	}
	fp := Fingerprint(cfg)
	if reason != ReasonPreRestore {
		last, err := scanBackup(s.db.QueryRow(ctx, `SELECT `+backupCols+` FROM backups b JOIN devices d ON d.id = b.device_id
			WHERE b.device_id = $1 AND b.status = 'ok' ORDER BY b.at DESC, b.id DESC LIMIT 1`, deviceID))
		if err == nil && last.SHA256 == fp {
			return last, true, nil
		} else if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, false, err
		}
	}

	rel, err := s.writeFile(deviceID, cfg)
	if err != nil {
		return nil, false, err
	}
	var id int64
	err = s.db.QueryRow(ctx, `INSERT INTO backups (device_id, at, reason, actor, status, sha256, size, firmware, path)
		VALUES ($1, $2, $3, $4, 'ok', $5, $6, $7, $8) RETURNING id`,
		deviceID, s.now(), reason, actor, fp, len(cfg), d.Firmware, rel).Scan(&id)
	if err != nil {
		_ = os.Remove(filepath.Join(s.dir, rel))
		return nil, false, err
	}
	if err := s.applyRetention(ctx, deviceID); err != nil {
		s.log.Error("backup retention", "device", d.Name, "err", err)
	}
	b, err = s.Get(ctx, id)
	return b, false, err
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func (s *Service) writeFile(deviceID int64, cfg []byte) (string, error) {
	rnd := make([]byte, 4)
	_, _ = rand.Read(rnd)
	rel := filepath.Join(strconv.FormatInt(deviceID, 10), s.now().UTC().Format("20060102T150405Z")+"-"+hex.EncodeToString(rnd)+".conf.enc")
	full := filepath.Join(s.dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		return "", err
	}
	tmp := full + ".tmp"
	if err := os.WriteFile(tmp, s.box.Seal(cfg, fileContext(deviceID)), 0o600); err != nil {
		return "", err
	}
	return rel, os.Rename(tmp, full)
}

// Content returns a backup's decrypted configuration.
func (s *Service) Content(ctx context.Context, id int64) ([]byte, *Backup, error) {
	b, err := s.Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if b.Status != "ok" {
		return nil, nil, invalidf("this backup failed and has no content")
	}
	sealed, err := os.ReadFile(filepath.Join(s.dir, b.path))
	if err != nil {
		return nil, nil, fmt.Errorf("backup file: %w", err)
	}
	cfg, err := s.box.Open(sealed, fileContext(b.DeviceID))
	if err != nil {
		return nil, nil, fmt.Errorf("backup file: %w", err)
	}
	return cfg, b, nil
}

// SetLock locks (exempts from retention and deletion) or unlocks a backup
// and sets its note.
func (s *Service) SetLock(ctx context.Context, id int64, locked bool, note string) error {
	tag, err := s.db.Exec(ctx, `UPDATE backups SET locked = $2, note = $3 WHERE id = $1`, id, locked, truncate(strings.TrimSpace(note), 500))
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// Delete removes an unlocked backup.
func (s *Service) Delete(ctx context.Context, id int64) error {
	b, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if b.Locked {
		return invalidf("unlock the backup before deleting it")
	}
	return s.remove(ctx, []*Backup{b})
}

func (s *Service) remove(ctx context.Context, list []*Backup) error {
	for _, b := range list {
		if _, err := s.db.Exec(ctx, `DELETE FROM backups WHERE id = $1`, b.ID); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23503" {
				continue // still referenced by a running restore
			}
			return err
		}
		if b.path != "" {
			if err := os.Remove(filepath.Join(s.dir, b.path)); err != nil && !errors.Is(err, os.ErrNotExist) {
				s.log.Warn("remove backup file", "path", b.path, "err", err)
			}
		}
	}
	return nil
}

// DiffBackups compares two stored backups (older first).
func (s *Service) DiffBackups(ctx context.Context, fromID, toID int64) ([]Change, error) {
	from, fb, err := s.Content(ctx, fromID)
	if err != nil {
		return nil, err
	}
	to, tb, err := s.Content(ctx, toID)
	if err != nil {
		return nil, err
	}
	if fb.DeviceID != tb.DeviceID {
		return nil, invalidf("both backups must belong to the same device")
	}
	return nonNil(Diff(string(from), string(to))), nil
}

// DiffCurrent compares a backup with the device's running configuration,
// which is fetched but not stored: the restore preview.
func (s *Service) DiffCurrent(ctx context.Context, id int64) ([]Change, error) {
	cfg, b, err := s.Content(ctx, id)
	if err != nil {
		return nil, err
	}
	d, err := s.inv.Get(ctx, b.DeviceID)
	if err != nil {
		return nil, err
	}
	if !d.Operational() {
		return nil, ErrNotLicensed
	}
	cctx, cancel := context.WithTimeout(ctx, s.opt.Timeout)
	defer cancel()
	live, err := s.inv.Driver(d).BackupConfig(cctx)
	if err != nil {
		return nil, fmt.Errorf("reading the running configuration failed: %w", err)
	}
	// From the running configuration to the backup: what a restore changes.
	return nonNil(Diff(string(live), string(cfg))), nil
}

func nonNil(c []Change) []Change {
	if c == nil {
		return []Change{}
	}
	return c
}

// --- policies and retention ---

// Policy schedules backups for all devices, a group or one device.
type Policy struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	GroupID   *int64     `json:"group_id"`
	DeviceID  *int64     `json:"device_id"`
	Schedule  string     `json:"schedule"`
	KeepCount int        `json:"keep_count"`
	KeepDays  int        `json:"keep_days"`
	Enabled   bool       `json:"enabled"`
	LastRunAt *time.Time `json:"last_run_at"`
	NextRunAt *time.Time `json:"next_run_at"`
	createdAt time.Time
}

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

func (p *Policy) next(now time.Time) *time.Time {
	sched, err := cronParser.Parse(p.Schedule)
	if err != nil {
		return nil
	}
	from := p.createdAt
	if p.LastRunAt != nil {
		from = *p.LastRunAt
	}
	n := sched.Next(from)
	// A missed run (server down) happens once, not once per missed slot.
	if n.Before(now.Add(-24 * time.Hour)) {
		n = sched.Next(now.Add(-time.Minute))
	}
	return &n
}

// ListPolicies returns all policies with their next run time.
func (s *Service) ListPolicies(ctx context.Context) ([]*Policy, error) {
	rows, err := s.db.Query(ctx, `SELECT id, name, group_id, device_id, schedule, keep_count, keep_days, enabled, last_run_at, created_at
		FROM backup_policies ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Policy{}
	now := s.now()
	for rows.Next() {
		var p Policy
		if err := rows.Scan(&p.ID, &p.Name, &p.GroupID, &p.DeviceID, &p.Schedule, &p.KeepCount, &p.KeepDays, &p.Enabled, &p.LastRunAt, &p.createdAt); err != nil {
			return nil, err
		}
		if p.Enabled {
			p.NextRunAt = p.next(now)
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

// SavePolicy creates (ID 0) or updates a policy.
func (s *Service) SavePolicy(ctx context.Context, p Policy) (int64, error) {
	p.Name = strings.TrimSpace(p.Name)
	p.Schedule = strings.TrimSpace(p.Schedule)
	if p.Name == "" || len(p.Name) > 100 {
		return 0, invalidf("policy name is required (at most 100 characters)")
	}
	if _, err := cronParser.Parse(p.Schedule); err != nil {
		return 0, invalidf("schedule is not a valid cron expression (minute hour day month weekday)")
	}
	if p.GroupID != nil && p.DeviceID != nil {
		return 0, invalidf("choose a group or a device, not both")
	}
	if p.KeepCount < 0 || p.KeepDays < 0 || p.KeepCount > 10000 || p.KeepDays > 3650 {
		return 0, invalidf("retention is out of range")
	}
	var err error
	if p.ID == 0 {
		err = s.db.QueryRow(ctx, `INSERT INTO backup_policies (name, group_id, device_id, schedule, keep_count, keep_days, enabled)
			VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`, p.Name, p.GroupID, p.DeviceID, p.Schedule, p.KeepCount, p.KeepDays, p.Enabled).Scan(&p.ID)
	} else {
		var tag pgconn.CommandTag
		tag, err = s.db.Exec(ctx, `UPDATE backup_policies SET name = $2, group_id = $3, device_id = $4, schedule = $5, keep_count = $6,
			keep_days = $7, enabled = $8 WHERE id = $1`, p.ID, p.Name, p.GroupID, p.DeviceID, p.Schedule, p.KeepCount, p.KeepDays, p.Enabled)
		if err == nil && tag.RowsAffected() == 0 {
			return 0, ErrNotFound
		}
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return 0, invalidf("the selected group or device does not exist")
	}
	return p.ID, err
}

// DeletePolicy removes a policy; existing backups stay.
func (s *Service) DeletePolicy(ctx context.Context, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM backup_policies WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (p *Policy) covers(d *inventory.Device) bool {
	switch {
	case p.DeviceID != nil:
		return *p.DeviceID == d.ID
	case p.GroupID != nil:
		return d.GroupID != nil && *d.GroupID == *p.GroupID
	}
	return true
}

func (p *Policy) specificity() int {
	switch {
	case p.DeviceID != nil:
		return 2
	case p.GroupID != nil:
		return 1
	}
	return 0
}

// applyRetention deletes a device's unlocked backups beyond the most
// specific enabled policy's limits. The newest backup is always kept.
func (s *Service) applyRetention(ctx context.Context, deviceID int64) error {
	d, err := s.inv.Get(ctx, deviceID)
	if err != nil {
		return err
	}
	policies, err := s.ListPolicies(ctx)
	if err != nil {
		return err
	}
	var rule *Policy
	for _, p := range policies {
		if p.Enabled && p.covers(d) && (rule == nil || p.specificity() > rule.specificity()) {
			rule = p
		}
	}
	list, err := s.List(ctx, deviceID, 1000)
	if err != nil {
		return err
	}
	now := s.now()
	var drop []*Backup
	kept := 0
	for _, b := range list {
		if b.Status != "ok" {
			if now.Sub(b.At) > s.opt.FailedKeep {
				drop = append(drop, b)
			}
			continue
		}
		kept++
		if b.Locked || kept == 1 || rule == nil {
			continue
		}
		if (rule.KeepCount > 0 && kept > rule.KeepCount) || (rule.KeepDays > 0 && now.Sub(b.At) > time.Duration(rule.KeepDays)*24*time.Hour) {
			drop = append(drop, b)
		}
	}
	return s.remove(ctx, drop)
}

// RunDue runs every policy whose schedule has come due.
func (s *Service) RunDue(ctx context.Context) error {
	policies, err := s.ListPolicies(ctx)
	if err != nil {
		return err
	}
	now := s.now()
	var devices []*inventory.Device
	for _, p := range policies {
		if !p.Enabled || p.NextRunAt == nil || p.NextRunAt.After(now) {
			continue
		}
		if _, err := s.db.Exec(ctx, `UPDATE backup_policies SET last_run_at = $2 WHERE id = $1`, p.ID, now); err != nil {
			return err
		}
		if devices == nil {
			if devices, err = s.inv.List(ctx, inventory.Filter{}); err != nil {
				return err
			}
		}
		sem := make(chan struct{}, s.opt.Concurrency)
		var wg sync.WaitGroup
		for _, d := range devices {
			if !p.covers(d) || !d.Operational() {
				continue
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(d *inventory.Device) {
				defer wg.Done()
				defer func() { <-sem }()
				if _, _, err := s.Run(ctx, d.ID, ReasonScheduled, "policy:"+p.Name); err != nil {
					s.log.Warn("scheduled backup failed", "device", d.Name, "err", err)
				}
			}(d)
		}
		wg.Wait()
	}
	return nil
}

// Scheduler runs due policies every minute until ctx ends.
func (s *Service) Scheduler(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		if err := s.RunDue(ctx); err != nil && ctx.Err() == nil {
			s.log.Error("backup scheduler", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
