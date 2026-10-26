package backup

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/farshadfighter/taktaplus/internal/device"
)

// RestoreJob tracks one restore.
type RestoreJob struct {
	ID          int64      `json:"id"`
	DeviceID    int64      `json:"device_id"`
	DeviceName  string     `json:"device_name"`
	BackupID    int64      `json:"backup_id"`
	PreBackupID *int64     `json:"pre_backup_id"`
	Actor       string     `json:"actor"`
	Status      string     `json:"status"` // running, waiting (for reboot), succeeded, failed
	Error       string     `json:"error"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at"`
}

// GetRestore returns one job.
func (s *Service) GetRestore(ctx context.Context, id int64) (*RestoreJob, error) {
	list, err := s.listRestores(ctx, `WHERE j.id = $1`, id)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return list[0], nil
}

// ListRestores returns recent jobs; deviceID 0 lists all.
func (s *Service) ListRestores(ctx context.Context, deviceID int64) ([]*RestoreJob, error) {
	return s.listRestores(ctx, `WHERE ($1 = 0 OR j.device_id = $1) ORDER BY j.started_at DESC LIMIT 100`, deviceID)
}

func (s *Service) listRestores(ctx context.Context, where string, args ...any) ([]*RestoreJob, error) {
	rows, err := s.db.Query(ctx, `SELECT j.id, j.device_id, d.name, j.backup_id, j.pre_backup_id, j.actor, j.status, j.error, j.started_at, j.finished_at
		FROM restore_jobs j JOIN devices d ON d.id = j.device_id `+where, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (*RestoreJob, error) {
		var j RestoreJob
		err := r.Scan(&j.ID, &j.DeviceID, &j.DeviceName, &j.BackupID, &j.PreBackupID, &j.Actor, &j.Status, &j.Error, &j.StartedAt, &j.FinishedAt)
		return &j, err
	})
}

func (s *Service) finish(ctx context.Context, jobID int64, status, msg string) {
	if _, err := s.db.Exec(context.WithoutCancel(ctx), `UPDATE restore_jobs SET status = $2, error = $3, finished_at = now() WHERE id = $1`,
		jobID, status, truncate(msg, 500)); err != nil {
		s.log.Error("update restore job", "job", jobID, "err", err)
	}
}

// Restore uploads a stored backup to its device. It first stores a backup
// of the running configuration, then restores; the device reboots, and a
// background watcher marks the job succeeded once the device answers again.
// The caller must have re-authenticated the user.
func (s *Service) Restore(ctx context.Context, backupID int64, actor string) (*RestoreJob, error) {
	cfg, b, err := s.Content(ctx, backupID)
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
	var jobID int64
	err = s.db.QueryRow(ctx, `INSERT INTO restore_jobs (device_id, backup_id, actor) VALUES ($1, $2, $3) RETURNING id`,
		d.ID, backupID, actor).Scan(&jobID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return nil, ErrRestoreRunning
	}
	if err != nil {
		return nil, err
	}

	pre, _, err := s.Run(ctx, d.ID, ReasonPreRestore, actor)
	if err != nil {
		s.finish(ctx, jobID, "failed", "safety backup before restore failed: "+err.Error())
		return s.GetRestore(context.WithoutCancel(ctx), jobID)
	}
	if _, err := s.db.Exec(ctx, `UPDATE restore_jobs SET pre_backup_id = $2 WHERE id = $1`, jobID, pre.ID); err != nil {
		return nil, err
	}

	rctx, cancel := context.WithTimeout(ctx, s.opt.Timeout)
	err = s.inv.Driver(d).RestoreConfig(rctx, cfg)
	cancel()
	var he device.HTTPError
	switch {
	case errors.Is(err, device.ErrUnsupported):
		s.finish(ctx, jobID, "failed", "restore needs REST API access to the device")
		return s.GetRestore(context.WithoutCancel(ctx), jobID)
	case errors.As(err, &he):
		s.finish(ctx, jobID, "failed", err.Error())
		return s.GetRestore(context.WithoutCancel(ctx), jobID)
	case err != nil:
		// A dropped connection can mean the device accepted the file and
		// began rebooting; the watcher decides.
		s.log.Warn("restore call ended with an error; waiting for the device", "device", d.Name, "err", err)
	}
	if _, err := s.db.Exec(ctx, `UPDATE restore_jobs SET status = 'waiting' WHERE id = $1`, jobID); err != nil {
		return nil, err
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.watch(context.WithoutCancel(ctx), jobID, d.ID)
	}()
	return s.GetRestore(ctx, jobID)
}

// watch waits for a restored device to come back.
func (s *Service) watch(ctx context.Context, jobID, deviceID int64) {
	deadline := time.Now().Add(s.opt.RebootLimit)
	time.Sleep(s.opt.RebootWait)
	var lastErr error
	for time.Now().Before(deadline) {
		pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, err := s.inv.Probe(pctx, deviceID)
		cancel()
		if err == nil {
			s.finish(ctx, jobID, "succeeded", "")
			return
		}
		lastErr = err
		time.Sleep(s.opt.RebootPoll)
	}
	s.finish(ctx, jobID, "failed", fmt.Sprintf("device did not come back within %s: %v", s.opt.RebootLimit, lastErr))
}

// RecoverJobs fails restores left running by a previous process, so they
// do not block new ones. Call it once at startup.
func (s *Service) RecoverJobs(ctx context.Context) error {
	_, err := s.db.Exec(ctx, `UPDATE restore_jobs SET status = 'failed', error = 'interrupted by a TaktaPlus restart; check the device', finished_at = now()
		WHERE status IN ('running', 'waiting')`)
	return err
}
