// Package system backs up TaktaPlus itself and stages signed upgrade
// bundles for the updater.
package system

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/robfig/cron/v3"

	"github.com/farshadfighter/taktaplus-lic/pkg/keys"
	"github.com/farshadfighter/taktaplus-lic/pkg/release"
	"github.com/farshadfighter/taktaplus-lic/pkg/selfbackup"

	"github.com/farshadfighter/taktaplus/internal/buildinfo"
	"github.com/farshadfighter/taktaplus/internal/settings"
)

//go:embed all:keys
var embeddedKeys embed.FS

// TrustedKeys returns the release signing keys built into the binary.
func TrustedKeys() (keys.Keyring, error) {
	ring := keys.Keyring{}
	entries, err := fs.ReadDir(embeddedKeys, "keys")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pub") {
			continue
		}
		data, err := fs.ReadFile(embeddedKeys, "keys/"+e.Name())
		if err != nil {
			return nil, err
		}
		part, err := keys.ParseKeyring(string(data))
		if err != nil {
			return nil, fmt.Errorf("system: %s: %w", e.Name(), err)
		}
		for kid, pub := range part {
			if err := ring.Add(kid, pub); err != nil {
				return nil, err
			}
		}
	}
	return ring, nil
}

// Directories under the data directory. They are left out of backups.
const (
	BackupDir  = "system-backups"
	UpgradeDir = "upgrade"
	filePrefix = "taktaplus-"
)

// Excluded are the data directory entries a backup leaves out.
var Excluded = []string{BackupDir, UpgradeDir, "tmp"}

// InputError is a validation failure to show to the user.
type InputError struct{ msg string }

func (e InputError) Error() string { return e.msg }

func invalidf(format string, a ...any) error { return InputError{fmt.Sprintf(format, a...)} }

// ErrNotFound means the backup file or upgrade does not exist.
var ErrNotFound = errors.New("not found")

// ErrBusy means a backup or an upgrade is already running.
var ErrBusy = errors.New("a backup or upgrade is already in progress")

// Settings configure scheduled backups.
type Settings struct {
	Enabled       bool            `json:"enabled"`
	Cron          string          `json:"cron"`
	Keep          int             `json:"keep"`
	SFTPEnabled   bool            `json:"sftp_enabled"`
	SFTP          selfbackup.SFTP `json:"sftp"`
	SFTPKeep      int             `json:"sftp_keep"`
	NotifyChannel *int64          `json:"notify_channel_id"`
	NotifyTo      []string        `json:"notify_to"`
}

// View is Settings as shown in the panel: secrets are masked.
type View struct {
	Settings
	HasPassword   bool       `json:"has_password"`
	HasPrivateKey bool       `json:"has_private_key"`
	KeySet        bool       `json:"key_set"`
	KeySetAt      *time.Time `json:"key_set_at"`
}

var defaults = Settings{Cron: "0 3 * * *", Keep: 7, SFTPKeep: 14, NotifyTo: []string{}}

const (
	settingsKey = "system_backup"
	keyKey      = "system_backup_key"
)

// Options configure the service.
type Options struct {
	DataDir     string
	DatabaseURL string
	MasterKey   string // TAKTAPLUS_MASTER_KEY, stored inside backups
	PgDump      string
	Hostname    string
}

// Service is the self-backup and upgrade module.
type Service struct {
	db       *pgxpool.Pool
	settings *settings.Store
	opt      Options
	ring     keys.Keyring
	log      *slog.Logger
	notify   func(ctx context.Context, channelID int64, to []string, subject, body string) error
	mu       sync.Mutex // one backup or staging at a time
	now      func() time.Time
}

// New returns the service.
func New(db *pgxpool.Pool, st *settings.Store, ring keys.Keyring, opt Options, log *slog.Logger) *Service {
	if opt.Hostname == "" {
		opt.Hostname, _ = os.Hostname()
	}
	return &Service{db: db, settings: st, opt: opt, ring: ring, log: log, now: time.Now}
}

// SetNotifier sets how failures are reported.
func (s *Service) SetNotifier(f func(ctx context.Context, channelID int64, to []string, subject, body string) error) {
	s.notify = f
}

func (s *Service) backupDir() string  { return filepath.Join(s.opt.DataDir, BackupDir) }
func (s *Service) upgradeDir() string { return filepath.Join(s.opt.DataDir, UpgradeDir) }

func (s *Service) load(ctx context.Context) (Settings, error) {
	st := defaults
	err := s.settings.Get(ctx, settingsKey, &st)
	if st.NotifyTo == nil {
		st.NotifyTo = []string{}
	}
	return st, err
}

func (s *Service) storedKey(ctx context.Context) (*selfbackup.StoredKey, error) {
	var k selfbackup.StoredKey
	if err := s.settings.Get(ctx, keyKey, &k); err != nil {
		return nil, err
	}
	if len(k.Secret) == 0 {
		return nil, nil
	}
	return &k, nil
}

// Settings returns the settings with secrets masked.
func (s *Service) Settings(ctx context.Context) (*View, error) {
	st, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	v := &View{Settings: st, HasPassword: st.SFTP.Password != "", HasPrivateKey: st.SFTP.PrivateKey != ""}
	v.SFTP.Password, v.SFTP.PrivateKey = "", ""
	k, err := s.storedKey(ctx)
	if err != nil {
		return nil, err
	}
	if k != nil {
		v.KeySet = true
		v.KeySetAt = &k.SetAt
	}
	return v, nil
}

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// withSecrets fills empty SFTP secrets from the stored settings, unless the
// server or user changed.
func (s *Service) withSecrets(ctx context.Context, in selfbackup.SFTP) (selfbackup.SFTP, error) {
	old, err := s.load(ctx)
	if err != nil {
		return in, err
	}
	if old.SFTP.Host == in.Host && old.SFTP.User == in.User {
		if in.Password == "" {
			in.Password = old.SFTP.Password
		}
		if in.PrivateKey == "" {
			in.PrivateKey = old.SFTP.PrivateKey
		}
		if in.HostKey == "" && old.SFTP.Port == in.Port {
			in.HostKey = old.SFTP.HostKey
		}
	}
	return in, nil
}

// SaveSettings validates and stores the settings. Empty SFTP secrets keep
// the stored ones.
func (s *Service) SaveSettings(ctx context.Context, in Settings) error {
	in.Cron = strings.TrimSpace(in.Cron)
	if _, err := cronParser.Parse(in.Cron); err != nil {
		return invalidf("schedule is not a valid cron expression (minute hour day month weekday)")
	}
	if in.Keep < 1 || in.Keep > 365 || in.SFTPKeep < 0 || in.SFTPKeep > 3650 {
		return invalidf("keep between 1 and 365 local backups")
	}
	if in.SFTPEnabled {
		if in.SFTP.Host == "" || in.SFTP.User == "" || in.SFTP.Dir == "" {
			return invalidf("SFTP needs a host, a user and a directory")
		}
		if in.SFTP.Port == 0 {
			in.SFTP.Port = 22
		}
	}
	sftp, err := s.withSecrets(ctx, in.SFTP)
	if err != nil {
		return err
	}
	in.SFTP = sftp
	if in.SFTPEnabled && in.SFTP.Password == "" && in.SFTP.PrivateKey == "" {
		return invalidf("SFTP needs a password or a private key")
	}
	if in.NotifyTo == nil {
		in.NotifyTo = []string{}
	}
	if in.Enabled {
		if k, err := s.storedKey(ctx); err != nil {
			return err
		} else if k == nil {
			return invalidf("set a backup passphrase before enabling scheduled backups")
		}
	}
	return s.settings.Set(ctx, settingsKey, in)
}

// SetPassphrase derives and stores a new backup key. Older backups still
// open with the passphrase they were made with.
func (s *Service) SetPassphrase(ctx context.Context, passphrase string) error {
	k, err := selfbackup.NewKey(passphrase)
	if err != nil {
		return InputError{err.Error()}
	}
	return s.settings.Set(ctx, keyKey, k.Store())
}

// TestSFTP connects with the given settings (stored secrets fill empty
// ones) and returns the server's host key for pinning.
func (s *Service) TestSFTP(ctx context.Context, in selfbackup.SFTP) (string, error) {
	in, err := s.withSecrets(ctx, in)
	if err != nil {
		return "", err
	}
	in.HostKey = "" // report the key the server presents now
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return in.Test(ctx)
}

// Run is a backup run.
type Run struct {
	ID         int64      `json:"id"`
	Trigger    string     `json:"trigger"`
	Status     string     `json:"status"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	File       string     `json:"file"`
	Size       int64      `json:"size"`
	SFTPStatus string     `json:"sftp_status"`
	Error      string     `json:"error"`
	CreatedBy  string     `json:"created_by"`
	Available  bool       `json:"available"` // the file is still on disk
}

// Runs lists recent backup runs.
func (s *Service) Runs(ctx context.Context) ([]Run, error) {
	rows, err := s.db.Query(ctx, `SELECT id, trigger, status, started_at, finished_at, file, size, sftp_status, error, created_by
		FROM system_backups ORDER BY started_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Run, error) {
		var x Run
		err := r.Scan(&x.ID, &x.Trigger, &x.Status, &x.StartedAt, &x.FinishedAt, &x.File, &x.Size, &x.SFTPStatus, &x.Error, &x.CreatedBy)
		return x, err
	})
	if err != nil {
		return nil, err
	}
	files, _ := selfbackup.List(s.backupDir(), filePrefix)
	for i := range out {
		out[i].Available = slices.ContainsFunc(files, func(f selfbackup.FileInfo) bool { return f.Name == out[i].File })
	}
	return out, nil
}

// File opens a local backup file for download.
func (s *Service) File(name string) (*os.File, error) {
	files, err := selfbackup.List(s.backupDir(), filePrefix)
	if err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(files, func(f selfbackup.FileInfo) bool { return f.Name == name }) {
		return nil, ErrNotFound
	}
	return os.Open(filepath.Join(s.backupDir(), name))
}

// Start begins a backup in the background and returns its run ID.
func (s *Service) Start(ctx context.Context, trigger, by string) (int64, error) {
	k, err := s.storedKey(ctx)
	if err != nil {
		return 0, err
	}
	if k == nil {
		return 0, invalidf("set a backup passphrase first")
	}
	if !s.mu.TryLock() {
		return 0, ErrBusy
	}
	var id int64
	if err := s.db.QueryRow(ctx, `INSERT INTO system_backups (trigger, created_by) VALUES ($1, $2) RETURNING id`, trigger, by).Scan(&id); err != nil {
		s.mu.Unlock()
		return 0, err
	}
	go func() {
		defer s.mu.Unlock()
		s.run(context.WithoutCancel(ctx), id)
	}()
	return id, nil
}

// Backup runs a backup and waits for it (CLI and pre-upgrade use).
func (s *Service) Backup(ctx context.Context, trigger, by string) (*Run, error) {
	if !s.mu.TryLock() {
		return nil, ErrBusy
	}
	defer s.mu.Unlock()
	var id int64
	if err := s.db.QueryRow(ctx, `INSERT INTO system_backups (trigger, created_by) VALUES ($1, $2) RETURNING id`, trigger, by).Scan(&id); err != nil {
		return nil, err
	}
	s.run(ctx, id)
	runs, err := s.Runs(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range runs {
		if r.ID == id {
			if r.Status != "ok" {
				return &r, errors.New(r.Error)
			}
			return &r, nil
		}
	}
	return nil, ErrNotFound
}

func (s *Service) run(ctx context.Context, id int64) {
	st, err := s.load(ctx)
	var res *selfbackup.Result
	if err == nil {
		var sk *selfbackup.StoredKey
		sk, err = s.storedKey(ctx)
		if err == nil && sk == nil {
			err = errors.New("set a backup passphrase first")
		}
		var key *selfbackup.Key
		if err == nil {
			key, err = sk.Key()
		}
		if err == nil {
			job := selfbackup.Job{
				Key: key,
				Options: selfbackup.Options{DatabaseURL: s.opt.DatabaseURL, PgDump: s.opt.PgDump, DataDir: s.opt.DataDir,
					Exclude: Excluded, Extra: map[string][]byte{"master.key": []byte(s.opt.MasterKey)}},
				Header: selfbackup.Header{Product: release.ProductApp, Version: buildinfo.Version, Hostname: s.opt.Hostname},
				Dir:    s.backupDir(), Prefix: filePrefix, Keep: st.Keep,
			}
			if st.SFTPEnabled {
				sftp := st.SFTP
				job.SFTP, job.SFTPKeep = &sftp, st.SFTPKeep
			}
			res, err = selfbackup.Run(ctx, job)
		}
	}
	if err != nil {
		s.log.Error("system backup failed", "err", err)
		_, _ = s.db.Exec(ctx, `UPDATE system_backups SET status = 'failed', error = $2, finished_at = now() WHERE id = $1`, id, err.Error())
		s.alert(ctx, st, "بکاپ TaktaPlus ناموفق بود", err.Error())
		return
	}
	sftpStatus, sftpErr := "", ""
	if st.SFTPEnabled {
		sftpStatus = "ok"
		if res.SFTPErr != nil {
			sftpStatus, sftpErr = "failed", res.SFTPErr.Error()
			s.alert(ctx, st, "ارسال بکاپ TaktaPlus به SFTP ناموفق بود", sftpErr)
		}
	}
	_, _ = s.db.Exec(ctx, `UPDATE system_backups SET status = 'ok', file = $2, size = $3, sftp_status = $4, error = $5, finished_at = now() WHERE id = $1`,
		id, res.File, res.Size, sftpStatus, sftpErr)
	s.log.Info("system backup done", "file", res.File, "size", res.Size)
}

func (s *Service) alert(ctx context.Context, st Settings, subject, detail string) {
	if s.notify == nil || st.NotifyChannel == nil {
		return
	}
	body := fmt.Sprintf("%s\n\nسرور: %s\nزمان: %s\nخطا: %s", subject, s.opt.Hostname, s.now().Format(time.RFC3339), detail)
	if err := s.notify(ctx, *st.NotifyChannel, st.NotifyTo, subject, body); err != nil {
		s.log.Warn("system backup notice", "err", err)
	}
}

// Recover marks runs interrupted by a restart as failed.
func (s *Service) Recover(ctx context.Context) error {
	_, err := s.db.Exec(ctx, `UPDATE system_backups SET status = 'failed', error = 'interrupted by a restart', finished_at = now() WHERE status = 'running'`)
	return err
}

// Scheduler runs scheduled backups until ctx ends.
func (s *Service) Scheduler(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	last := s.now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			st, err := s.load(ctx)
			if err == nil && st.Enabled {
				if sch, err := cronParser.Parse(st.Cron); err == nil && !sch.Next(last).After(now) {
					if _, err := s.Start(ctx, "schedule", ""); err != nil && !errors.Is(err, ErrBusy) {
						s.log.Error("scheduled system backup", "err", err)
						s.alert(ctx, st, "بکاپ زمان‌بندی‌شده‌ی TaktaPlus اجرا نشد", err.Error())
					}
				}
			}
			last = now
			s.syncUpgrade(ctx)
		}
	}
}

// --- upgrades ---

// Upgrade is an uploaded bundle and its progress.
type Upgrade struct {
	ID          int64      `json:"id"`
	Version     string     `json:"version"`
	FromVersion string     `json:"from_version"`
	Notes       string     `json:"notes"`
	Status      string     `json:"status"`
	Step        string     `json:"step"`
	Message     string     `json:"message"`
	Log         string     `json:"log"`
	UploadedBy  string     `json:"uploaded_by"`
	CreatedAt   time.Time  `json:"created_at"`
	FinishedAt  *time.Time `json:"finished_at"`
}

// request is what the updater picks up.
type request struct {
	ID      int64  `json:"id"`
	Version string `json:"version"`
	Dir     string `json:"dir"` // relative to the data directory
}

// status is what the updater reports.
type status struct {
	ID      int64  `json:"id"`
	State   string `json:"state"` // running, done, failed, rolled_back
	Step    string `json:"step"`
	Message string `json:"message"`
}

// Stage verifies a bundle, extracts it under the upgrade directory and, with
// queue, hands it to the updater. It returns the upgrade and the directory.
func (s *Service) Stage(ctx context.Context, r io.Reader, by string, queue bool) (*Upgrade, string, error) {
	if len(s.ring) == 0 {
		return nil, "", invalidf("this build has no release signing key; signed bundles cannot be verified")
	}
	if !s.mu.TryLock() {
		return nil, "", ErrBusy
	}
	defer s.mu.Unlock()
	if queue {
		if _, err := os.Stat(filepath.Join(s.upgradeDir(), "request.json")); err == nil {
			return nil, "", invalidf("an upgrade is already queued")
		}
	}
	if err := os.MkdirAll(s.upgradeDir(), 0o700); err != nil {
		return nil, "", err
	}
	tmp, err := os.MkdirTemp(s.upgradeDir(), "incoming-")
	if err != nil {
		return nil, "", err
	}
	m, err := release.Open(r, s.ring, tmp, func(m *release.Manifest) error {
		return m.CheckUpgrade(release.ProductApp, buildinfo.Version)
	})
	if err != nil {
		os.RemoveAll(tmp)
		return nil, "", InputError{err.Error()}
	}
	u := &Upgrade{Version: m.Version, FromVersion: buildinfo.Version, Notes: m.Notes, UploadedBy: by, Status: "staged"}
	if queue {
		u.Status = "queued"
	}
	if err := s.db.QueryRow(ctx, `INSERT INTO system_upgrades (version, from_version, notes, status, uploaded_by) VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at`, u.Version, u.FromVersion, u.Notes, u.Status, by).Scan(&u.ID, &u.CreatedAt); err != nil {
		os.RemoveAll(tmp)
		return nil, "", err
	}
	rel := "staged-" + strconv.FormatInt(u.ID, 10)
	dir := filepath.Join(s.upgradeDir(), rel)
	_ = os.RemoveAll(dir)
	if err := os.Rename(tmp, dir); err != nil {
		os.RemoveAll(tmp)
		return nil, "", err
	}
	if queue {
		data, _ := json.Marshal(request{ID: u.ID, Version: u.Version, Dir: filepath.ToSlash(filepath.Join(UpgradeDir, rel))})
		if err := os.WriteFile(filepath.Join(s.upgradeDir(), "request.json.tmp"), data, 0o600); err != nil {
			return nil, "", err
		}
		if err := os.Rename(filepath.Join(s.upgradeDir(), "request.json.tmp"), filepath.Join(s.upgradeDir(), "request.json")); err != nil {
			return nil, "", err
		}
	}
	return u, dir, nil
}

// Cancel removes a queued upgrade the updater has not picked up yet.
func (s *Service) Cancel(ctx context.Context, id int64) error {
	p := filepath.Join(s.upgradeDir(), "request.json")
	data, err := os.ReadFile(p)
	if err != nil {
		return invalidf("the upgrade is no longer queued")
	}
	var req request
	if json.Unmarshal(data, &req) != nil || req.ID != id {
		return invalidf("the upgrade is no longer queued")
	}
	if err := os.Remove(p); err != nil {
		return err
	}
	_ = os.RemoveAll(filepath.Join(s.upgradeDir(), "staged-"+strconv.FormatInt(id, 10)))
	_, err = s.db.Exec(ctx, `UPDATE system_upgrades SET status = 'cancelled', finished_at = now() WHERE id = $1`, id)
	return err
}

// syncUpgrade copies the updater's status into the upgrade's row.
func (s *Service) syncUpgrade(ctx context.Context) {
	data, err := os.ReadFile(filepath.Join(s.upgradeDir(), "status.json"))
	if err != nil {
		return
	}
	var st status
	if json.Unmarshal(data, &st) != nil || st.ID == 0 {
		return
	}
	logText, _ := os.ReadFile(filepath.Join(s.upgradeDir(), "upgrade-"+strconv.FormatInt(st.ID, 10)+".log"))
	if len(logText) > 64<<10 {
		logText = logText[len(logText)-64<<10:]
	}
	finished := st.State == "done" || st.State == "failed" || st.State == "rolled_back"
	_, err = s.db.Exec(ctx, `UPDATE system_upgrades SET status = $2, step = $3, message = $4, log = $5,
		finished_at = CASE WHEN $6 THEN coalesce(finished_at, now()) ELSE NULL END WHERE id = $1`,
		st.ID, st.State, st.Step, st.Message, string(logText), finished)
	if err != nil {
		s.log.Warn("upgrade status", "err", err)
	}
}

// Upgrades lists uploaded bundles, newest first.
func (s *Service) Upgrades(ctx context.Context) ([]Upgrade, error) {
	s.syncUpgrade(ctx)
	rows, err := s.db.Query(ctx, `SELECT id, version, from_version, notes, status, step, message, log, uploaded_by, created_at, finished_at
		FROM system_upgrades ORDER BY id DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Upgrade, error) {
		var u Upgrade
		err := r.Scan(&u.ID, &u.Version, &u.FromVersion, &u.Notes, &u.Status, &u.Step, &u.Message, &u.Log, &u.UploadedBy, &u.CreatedAt, &u.FinishedAt)
		return u, err
	})
}

// UpdaterSeen reports whether the updater has written its heartbeat in the
// last two minutes; without it, queued upgrades wait.
func (s *Service) UpdaterSeen() (bool, *time.Time) {
	st, err := os.Stat(filepath.Join(s.upgradeDir(), "updater.alive"))
	if err != nil {
		return false, nil
	}
	t := st.ModTime()
	return s.now().Sub(t) < 2*time.Minute, &t
}

// Info is the installation's version and disk usage.
type Info struct {
	Version     string     `json:"version"`
	Commit      string     `json:"commit"`
	BuildDate   string     `json:"build_date"`
	CanVerify   bool       `json:"can_verify"` // a release key is built in
	Updater     bool       `json:"updater"`
	UpdaterSeen *time.Time `json:"updater_seen"`
	BackupBytes int64      `json:"backup_bytes"`
	FreeBytes   uint64     `json:"free_bytes"`
}

// Info returns version, updater and disk information.
func (s *Service) Info() Info {
	in := Info{Version: buildinfo.Version, Commit: buildinfo.Commit, BuildDate: buildinfo.Date, CanVerify: len(s.ring) > 0}
	in.Updater, in.UpdaterSeen = s.UpdaterSeen()
	files, _ := selfbackup.List(s.backupDir(), filePrefix)
	for _, f := range files {
		in.BackupBytes += f.Size
	}
	in.FreeBytes = freeBytes(s.opt.DataDir)
	return in
}
