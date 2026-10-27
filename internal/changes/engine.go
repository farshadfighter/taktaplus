package changes

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/farshadfighter/taktaplus/internal/backup"
)

// maxOutput bounds the output kept per device.
const maxOutput = 256 << 10

type devInfo struct {
	id    int64
	group *int64
}

func (s *Service) targetDevices(ctx context.Context, id int64) ([]devInfo, error) {
	rows, err := s.db.Query(ctx, `SELECT d.id, d.group_id FROM change_targets t JOIN devices d ON d.id = t.device_id WHERE t.change_id = $1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []devInfo
	for rows.Next() {
		var d devInfo
		if err := rows.Scan(&d.id, &d.group); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// dueAt reports whether an approved change may start at t.
func (s *Service) dueAt(c *Change, devs []devInfo, windows []Window, t time.Time) bool {
	switch c.RunMode {
	case "now":
		return true
	case "at":
		return c.RunAt != nil && !t.Before(*c.RunAt)
	}
	for _, d := range devs {
		if !inWindow(windows, d.group, t) {
			return false
		}
	}
	return true
}

// nextRun estimates when an approved change will start.
func (s *Service) nextRun(ctx context.Context, c *Change) *time.Time {
	now := s.now()
	if c.RunMode == "now" {
		return &now
	}
	if c.RunMode == "at" {
		return c.RunAt
	}
	devs, err := s.targetDevices(ctx, c.ID)
	if err != nil {
		return nil
	}
	windows, err := s.ListWindows(ctx)
	if err != nil {
		return nil
	}
	// Candidate start times: now and every window opening in the next week.
	cands := []time.Time{now}
	for i := range windows {
		t := now
		for range 8 {
			n, ok := windows[i].next(t)
			if !ok {
				break
			}
			cands = append(cands, n)
			t = n.Add(time.Minute)
		}
	}
	var best *time.Time
	for _, t := range cands {
		if s.dueAt(c, devs, windows, t) && (best == nil || t.Before(*best)) {
			tt := t
			best = &tt
		}
	}
	return best
}

// Tick starts every approved change that is due and does not overlap a
// running change's devices. It returns once they are started.
func (s *Service) Tick(ctx context.Context, wg *sync.WaitGroup) error {
	list, err := s.List(ctx, "", StatusApproved)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		return nil
	}
	windows, err := s.ListWindows(ctx)
	if err != nil {
		return err
	}
	now := s.now()
	for i := len(list) - 1; i >= 0; i-- { // oldest first
		c := list[i]
		devs, err := s.targetDevices(ctx, c.ID)
		if err != nil {
			return err
		}
		if !s.dueAt(c, devs, windows, now) || !s.lock(devs) {
			continue
		}
		tag, err := s.db.Exec(ctx, `UPDATE changes SET status = 'running', started_at = now() WHERE id = $1 AND status = 'approved'`, c.ID)
		if err != nil || tag.RowsAffected() == 0 {
			s.unlock(devs)
			if err != nil {
				return err
			}
			continue
		}
		wg.Add(1)
		go func(c *Change, devs []devInfo) {
			defer wg.Done()
			defer s.unlock(devs)
			s.execute(context.WithoutCancel(ctx), c)
		}(c, devs)
	}
	return nil
}

func (s *Service) lock(devs []devInfo) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range devs {
		if s.running[d.id] {
			return false
		}
	}
	for _, d := range devs {
		s.running[d.id] = true
	}
	return true
}

func (s *Service) unlock(devs []devInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range devs {
		delete(s.running, d.id)
	}
}

func (s *Service) cancelled(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancel[id]
}

// execute runs a change's targets: canary devices first, one at a time,
// then the rest in parallel, stopping when failures exceed the limit.
func (s *Service) execute(ctx context.Context, c *Change) {
	sys := Actor{Username: "system"}
	targets, err := s.Targets(ctx, c.ID)
	if err != nil {
		s.log.Error("load change targets", "change", c.ID, "err", err)
		return
	}
	var mu sync.Mutex
	failures := 0
	stop := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return failures > c.MaxFailures || s.cancelled(c.ID)
	}
	runOne := func(t Target) {
		if err := s.runTarget(ctx, c, t); err != nil {
			mu.Lock()
			failures++
			mu.Unlock()
		}
	}
	canary := min(c.Canary, len(targets))
	for _, t := range targets[:canary] {
		if stop() {
			break
		}
		runOne(t)
	}
	canaryFailed := failures > 0
	if !canaryFailed {
		sem := make(chan struct{}, c.Concurrency)
		var wg sync.WaitGroup
		for _, t := range targets[canary:] {
			if stop() {
				break
			}
			sem <- struct{}{}
			wg.Add(1)
			go func(t Target) {
				defer wg.Done()
				defer func() { <-sem }()
				if !stop() {
					runOne(t)
				}
			}(t)
		}
		wg.Wait()
	}

	if _, err := s.db.Exec(ctx, `UPDATE change_targets SET status = 'skipped',
		error = CASE WHEN $2 THEN 'cancelled' ELSE 'not run: too many failures' END
		WHERE change_id = $1 AND status = 'pending'`, c.ID, s.cancelled(c.ID)); err != nil {
		s.log.Error("skip targets", "change", c.ID, "err", err)
	}
	var ok, failed int
	_ = s.db.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status = 'succeeded'), count(*) FILTER (WHERE status IN ('failed', 'skipped'))
		FROM change_targets WHERE change_id = $1`, c.ID).Scan(&ok, &failed)
	status := StatusSucceeded
	switch {
	case s.cancelled(c.ID):
		status = StatusCancelled
	case ok == 0:
		status = StatusFailed
	case failed > 0:
		status = StatusPartial
	}
	s.mu.Lock()
	delete(s.cancel, c.ID)
	s.mu.Unlock()
	if _, err := s.db.Exec(ctx, `UPDATE changes SET status = $2, finished_at = now() WHERE id = $1`, c.ID, status); err != nil {
		s.log.Error("finish change", "change", c.ID, "err", err)
	}
	_ = s.log_(ctx, s.db, sys, "change.finish", c.ID, map[string]any{"status": status, "succeeded": ok, "failed": failed})
}

// limitedWriter keeps the first max bytes.
type limitedWriter struct {
	buf bytes.Buffer
	max int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if room := w.max - w.buf.Len(); room > 0 {
		w.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (s *Service) runTarget(ctx context.Context, c *Change, t Target) (err error) {
	if _, err := s.db.Exec(ctx, `UPDATE change_targets SET status = 'running', started_at = now() WHERE id = $1`, t.ID); err != nil {
		return err
	}
	out := &limitedWriter{max: maxOutput}
	var preBackup *int64
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("internal error: %v", r)
		}
		status, msg := "succeeded", ""
		if err != nil {
			status, msg = "failed", err.Error()
		}
		if _, dbErr := s.db.Exec(context.WithoutCancel(ctx), `UPDATE change_targets SET status = $2, output = $3, error = $4, pre_backup_id = $5,
			finished_at = now() WHERE id = $1`, t.ID, status, out.buf.String(), truncate(msg, 2000), preBackup); dbErr != nil {
			s.log.Error("record change target", "target", t.ID, "err", dbErr)
		}
	}()
	d, err := s.inv.Get(ctx, t.DeviceID)
	if err != nil {
		return err
	}
	if !d.Operational() {
		return errors.New("the device's license is not active")
	}
	if c.BackupFirst && s.backups != nil {
		b, _, err := s.backups.Run(ctx, d.ID, backup.ReasonPreChange, fmt.Sprintf("change #%d", c.ID))
		if err != nil {
			return fmt.Errorf("backup before the change failed: %w", err)
		}
		preBackup = &b.ID
		fmt.Fprintf(out, "# backup #%d taken before the change\n", b.ID)
	}
	r := s.runners[c.Kind]
	if r == nil {
		return fmt.Errorf("no runner for %s changes", c.Kind)
	}
	return r.Run(ctx, c, d, out)
}

func truncate(v string, n int) string {
	if len(v) > n {
		return v[:n]
	}
	return v
}

// Run starts due changes every 20 seconds until ctx ends, and fails
// changes left running by a previous process.
func (s *Service) Run(ctx context.Context) {
	if _, err := s.db.Exec(ctx, `UPDATE change_targets SET status = 'failed', error = 'interrupted by a TaktaPlus restart', finished_at = now()
		WHERE status = 'running'`); err != nil {
		s.log.Error("recover change targets", "err", err)
	}
	if _, err := s.db.Exec(ctx, `UPDATE change_targets SET status = 'skipped', error = 'interrupted by a TaktaPlus restart'
		WHERE status = 'pending' AND change_id IN (SELECT id FROM changes WHERE status = 'running')`); err != nil {
		s.log.Error("recover change targets", "err", err)
	}
	if _, err := s.db.Exec(ctx, `UPDATE changes SET status = 'failed', finished_at = now() WHERE status = 'running'`); err != nil {
		s.log.Error("recover changes", "err", err)
	}
	var wg sync.WaitGroup
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	for {
		if err := s.Tick(ctx, &wg); err != nil && ctx.Err() == nil {
			s.log.Error("change scheduler", "err", err)
		}
		select {
		case <-ctx.Done():
			// Running changes are not waited for: the next start marks them
			// interrupted.
			return
		case <-t.C:
		}
	}
}
