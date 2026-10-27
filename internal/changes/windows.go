package changes

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Window is a recurring maintenance window.
type Window struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	GroupID   *int64 `json:"group_id"`
	Weekdays  []int  `json:"weekdays"`   // 0 = Sunday
	StartTime string `json:"start_time"` // HH:MM
	Minutes   int    `json:"minutes"`
	Enabled   bool   `json:"enabled"`
}

func (w *Window) validate() error {
	w.Name = strings.TrimSpace(w.Name)
	if w.Name == "" {
		return invalidf("window name is required")
	}
	if len(w.Weekdays) == 0 {
		return invalidf("choose at least one weekday")
	}
	for _, d := range w.Weekdays {
		if d < 0 || d > 6 {
			return invalidf("weekdays are 0 (Sunday) to 6 (Saturday)")
		}
	}
	if _, _, err := w.clock(); err != nil {
		return invalidf("start time must be HH:MM")
	}
	if w.Minutes < 15 || w.Minutes > 1440 {
		return invalidf("duration must be between 15 minutes and 24 hours")
	}
	slices.Sort(w.Weekdays)
	w.Weekdays = slices.Compact(w.Weekdays)
	return nil
}

func (w *Window) clock() (int, int, error) {
	h, m, ok := strings.Cut(w.StartTime, ":")
	if !ok {
		return 0, 0, fmt.Errorf("bad time")
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, 0, fmt.Errorf("bad time")
	}
	return hh, mm, nil
}

// openAt returns the start of the occurrence containing t, if any.
func (w *Window) openAt(t time.Time) (time.Time, bool) {
	hh, mm, err := w.clock()
	if err != nil || !w.Enabled {
		return time.Time{}, false
	}
	for back := 0; back <= 1; back++ {
		day := t.AddDate(0, 0, -back)
		start := time.Date(day.Year(), day.Month(), day.Day(), hh, mm, 0, 0, t.Location())
		if !slices.Contains(w.Weekdays, int(start.Weekday())) {
			continue
		}
		if !t.Before(start) && t.Before(start.Add(time.Duration(w.Minutes)*time.Minute)) {
			return start, true
		}
	}
	return time.Time{}, false
}

// next returns the next occurrence start at or after t.
func (w *Window) next(t time.Time) (time.Time, bool) {
	if s, ok := w.openAt(t); ok {
		return s, true
	}
	hh, mm, err := w.clock()
	if err != nil || !w.Enabled {
		return time.Time{}, false
	}
	for ahead := 0; ahead <= 7; ahead++ {
		day := t.AddDate(0, 0, ahead)
		start := time.Date(day.Year(), day.Month(), day.Day(), hh, mm, 0, 0, t.Location())
		if start.After(t) && slices.Contains(w.Weekdays, int(start.Weekday())) {
			return start, true
		}
	}
	return time.Time{}, false
}

// covers reports whether the window applies to a device in group.
func (w *Window) covers(group *int64) bool {
	return w.GroupID == nil || (group != nil && *group == *w.GroupID)
}

// inWindow reports whether a device in group may be changed at t: when no
// enabled window covers it, or one of its windows is open.
func inWindow(windows []Window, group *int64, t time.Time) bool {
	covered := false
	for i := range windows {
		w := &windows[i]
		if !w.Enabled || !w.covers(group) {
			continue
		}
		covered = true
		if _, ok := w.openAt(t); ok {
			return true
		}
	}
	return !covered
}

// ListWindows returns the maintenance windows.
func (s *Service) ListWindows(ctx context.Context) ([]Window, error) {
	rows, err := s.db.Query(ctx, `SELECT id, name, group_id, weekdays, start_time, minutes, enabled FROM maintenance_windows ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Window])
}

// SaveWindow creates (ID 0) or updates a window.
func (s *Service) SaveWindow(ctx context.Context, w Window) (int64, error) {
	if err := w.validate(); err != nil {
		return 0, err
	}
	if w.ID == 0 {
		err := s.db.QueryRow(ctx, `INSERT INTO maintenance_windows (name, group_id, weekdays, start_time, minutes, enabled)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`, w.Name, w.GroupID, w.Weekdays, w.StartTime, w.Minutes, w.Enabled).Scan(&w.ID)
		return w.ID, err
	}
	tag, err := s.db.Exec(ctx, `UPDATE maintenance_windows SET name = $2, group_id = $3, weekdays = $4, start_time = $5, minutes = $6, enabled = $7
		WHERE id = $1`, w.ID, w.Name, w.GroupID, w.Weekdays, w.StartTime, w.Minutes, w.Enabled)
	if err == nil && tag.RowsAffected() == 0 {
		return 0, ErrNotFound
	}
	return w.ID, err
}

// DeleteWindow removes a window.
func (s *Service) DeleteWindow(ctx context.Context, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM maintenance_windows WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// Policy is the approval requirement for a group (nil: default).
type Policy struct {
	ID       int64  `json:"id"`
	GroupID  *int64 `json:"group_id"`
	Required int    `json:"required"`
}

// ListPolicies returns approval policies.
func (s *Service) ListPolicies(ctx context.Context) ([]Policy, error) {
	rows, err := s.db.Query(ctx, `SELECT id, group_id, required FROM approval_policies ORDER BY group_id NULLS FIRST`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Policy])
}

// SetPolicy sets the approvals required for a group (nil: the default).
func (s *Service) SetPolicy(ctx context.Context, group *int64, required int) error {
	if required < 0 || required > 2 {
		return invalidf("required approvals must be 0, 1 or 2")
	}
	_, err := s.db.Exec(ctx, `INSERT INTO approval_policies (group_id, required) VALUES ($1, $2)
		ON CONFLICT (coalesce(group_id, 0)) DO UPDATE SET required = EXCLUDED.required`, group, required)
	return err
}

// DeletePolicy removes a group's policy so the default applies.
func (s *Service) DeletePolicy(ctx context.Context, group int64) error {
	_, err := s.db.Exec(ctx, `DELETE FROM approval_policies WHERE group_id = $1`, group)
	return err
}

func (s *Service) requiredFor(ctx context.Context, groups []*int64) (int, error) {
	list, err := s.ListPolicies(ctx)
	if err != nil {
		return 0, err
	}
	def := 1
	byGroup := map[int64]int{}
	for _, p := range list {
		if p.GroupID == nil {
			def = p.Required
		} else {
			byGroup[*p.GroupID] = p.Required
		}
	}
	need := 0
	for _, g := range groups {
		r := def
		if g != nil {
			if v, ok := byGroup[*g]; ok {
				r = v
			}
		}
		need = max(need, r)
	}
	return need, nil
}
