// Package changes is the change workflow shared by signature deployments,
// CLI scripts and firmware upgrades: draft, approval, scheduling in
// maintenance windows, and staged execution with a backup before each
// device.
package changes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/farshadfighter/taktaplus/internal/audit"
	"github.com/farshadfighter/taktaplus/internal/backup"
	"github.com/farshadfighter/taktaplus/internal/inventory"
)

// Statuses of a change.
const (
	StatusDraft     = "draft"
	StatusPending   = "pending" // waiting for approvals
	StatusApproved  = "approved"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusPartial   = "partial"
	StatusCancelled = "cancelled"
	StatusRejected  = "rejected"
)

var (
	// ErrNotFound means the change does not exist.
	ErrNotFound = errors.New("not found")
	// ErrState means the change is not in a state that allows the action.
	ErrState = errors.New("the change is not in a state that allows this")
)

// InputError is a validation failure to show to the user.
type InputError struct{ msg string }

func (e InputError) Error() string { return e.msg }

func invalidf(format string, a ...any) error { return InputError{fmt.Sprintf(format, a...)} }

// Runner executes one kind of change.
type Runner interface {
	// Preview validates spec for a device and describes what will run there.
	Preview(ctx context.Context, spec json.RawMessage, d *inventory.Device) (string, error)
	// Run applies the change to one device, writing its output to out.
	Run(ctx context.Context, c *Change, d *inventory.Device, out io.Writer) error
}

// Actor is who acts on a change.
type Actor struct {
	UserID   int64
	Username string
	IP       string
}

// Change is a change request.
type Change struct {
	ID           int64           `json:"id"`
	Kind         string          `json:"kind"`
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	Spec         json.RawMessage `json:"spec"`
	Status       string          `json:"status"`
	RunMode      string          `json:"run_mode"`
	RunAt        *time.Time      `json:"run_at"`
	UrgentReason string          `json:"urgent_reason"`
	Concurrency  int             `json:"concurrency"`
	Canary       int             `json:"canary"`
	MaxFailures  int             `json:"max_failures"`
	BackupFirst  bool            `json:"backup_first"`
	Required     int             `json:"required_approvals"`
	CreatedBy    *int64          `json:"created_by"`
	Creator      string          `json:"creator"`
	CreatedAt    time.Time       `json:"created_at"`
	SubmittedAt  *time.Time      `json:"submitted_at"`
	StartedAt    *time.Time      `json:"started_at"`
	FinishedAt   *time.Time      `json:"finished_at"`
	// Summaries, filled by List and Get.
	Targets   int        `json:"targets"`
	Succeeded int        `json:"succeeded_targets"`
	Failed    int        `json:"failed_targets"`
	Approvals int        `json:"approvals"`
	NextRunAt *time.Time `json:"next_run_at"`
}

// Target is one device of a change.
type Target struct {
	ID          int64      `json:"id"`
	DeviceID    int64      `json:"device_id"`
	DeviceName  string     `json:"device_name"`
	Position    int        `json:"position"`
	Status      string     `json:"status"`
	Preview     string     `json:"preview"`
	Output      string     `json:"output"`
	Error       string     `json:"error"`
	PreBackupID *int64     `json:"pre_backup_id"`
	StartedAt   *time.Time `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at"`
}

// Approval is one approver's decision.
type Approval struct {
	Username string    `json:"username"`
	Decision string    `json:"decision"`
	Comment  string    `json:"comment"`
	At       time.Time `json:"at"`
}

// Service manages changes.
type Service struct {
	db      *pgxpool.Pool
	inv     *inventory.Service
	backups *backup.Service
	log     *slog.Logger
	now     func() time.Time
	runners map[string]Runner

	mu      sync.Mutex
	running map[int64]bool // device ids under a running change
	cancel  map[int64]bool // running changes asked to stop
}

// New returns the service; register runners before use.
func New(db *pgxpool.Pool, inv *inventory.Service, backups *backup.Service, log *slog.Logger) *Service {
	return &Service{db: db, inv: inv, backups: backups, log: log, now: time.Now, runners: map[string]Runner{},
		running: map[int64]bool{}, cancel: map[int64]bool{}}
}

// Register adds the runner for a kind.
func (s *Service) Register(kind string, r Runner) { s.runners[kind] = r }

// CreateInput describes a new change.
type CreateInput struct {
	Kind         string          `json:"kind"`
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	Spec         json.RawMessage `json:"spec"`
	DeviceIDs    []int64         `json:"device_ids"`
	RunMode      string          `json:"run_mode"`
	RunAt        *time.Time      `json:"run_at"`
	UrgentReason string          `json:"urgent_reason"`
	Concurrency  int             `json:"concurrency"`
	Canary       int             `json:"canary"`
	MaxFailures  int             `json:"max_failures"`
	BackupFirst  *bool           `json:"backup_first"`
}

func (in *CreateInput) normalize(now time.Time) error {
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" || len(in.Title) > 200 {
		return invalidf("title is required (at most 200 characters)")
	}
	// Keep the given order: the first devices are the canaries.
	var ids []int64
	for _, id := range in.DeviceIDs {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	in.DeviceIDs = ids
	if len(in.DeviceIDs) == 0 {
		return invalidf("choose at least one device")
	}
	if in.RunMode == "" {
		in.RunMode = "window"
	}
	switch in.RunMode {
	case "window":
		in.RunAt = nil
	case "at":
		if in.RunAt == nil || in.RunAt.Before(now.Add(-time.Minute)) {
			return invalidf("choose a run time in the future")
		}
	case "now":
		if strings.TrimSpace(in.UrgentReason) == "" {
			return invalidf("running outside the maintenance window needs a reason")
		}
	default:
		return invalidf("run mode must be window, at or now")
	}
	if in.Concurrency == 0 {
		in.Concurrency = 4
	}
	if in.Concurrency < 1 || in.Concurrency > 50 || in.Canary < 0 || in.MaxFailures < 0 {
		return invalidf("concurrency must be 1–50; canary and failure limits cannot be negative")
	}
	return nil
}

// Create plans a change as a draft: it checks every device and records
// what will run on each.
func (s *Service) Create(ctx context.Context, in CreateInput, by Actor) (*Change, error) {
	r, ok := s.runners[in.Kind]
	if !ok {
		return nil, invalidf("unknown change kind %q", in.Kind)
	}
	if err := in.normalize(s.now()); err != nil {
		return nil, err
	}
	type plan struct {
		d       *inventory.Device
		preview string
	}
	var plans []plan
	var groups []*int64
	var problems []string
	for _, id := range in.DeviceIDs {
		d, err := s.inv.Get(ctx, id)
		if errors.Is(err, inventory.ErrNotFound) {
			return nil, invalidf("device %d does not exist", id)
		} else if err != nil {
			return nil, err
		}
		if !d.Operational() {
			problems = append(problems, fmt.Sprintf("%s: the device's license is not active", d.Name))
			continue
		}
		preview, err := r.Preview(ctx, in.Spec, d)
		if err != nil {
			var ie InputError
			if errors.As(err, &ie) {
				problems = append(problems, fmt.Sprintf("%s: %s", d.Name, err.Error()))
				continue
			}
			return nil, err
		}
		plans = append(plans, plan{d, preview})
		groups = append(groups, d.GroupID)
	}
	if len(problems) > 0 {
		return nil, invalidf("%s", strings.Join(problems, "; "))
	}
	required, err := s.requiredFor(ctx, groups)
	if err != nil {
		return nil, err
	}
	backupFirst := true
	if in.BackupFirst != nil {
		backupFirst = *in.BackupFirst
	}
	spec := in.Spec
	if len(spec) == 0 {
		spec = json.RawMessage("{}")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO changes (kind, title, description, spec, run_mode, run_at, urgent_reason, concurrency, canary,
		max_failures, backup_first, required_approvals, created_by, creator)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14) RETURNING id`,
		in.Kind, in.Title, in.Description, spec, in.RunMode, in.RunAt, strings.TrimSpace(in.UrgentReason), in.Concurrency, in.Canary,
		in.MaxFailures, backupFirst, required, nullID(by.UserID), by.Username).Scan(&id)
	if err != nil {
		return nil, err
	}
	for i, p := range plans {
		if _, err := tx.Exec(ctx, `INSERT INTO change_targets (change_id, device_id, position, preview) VALUES ($1, $2, $3, $4)`,
			id, p.d.ID, i, p.preview); err != nil {
			return nil, err
		}
	}
	if err := s.log_(ctx, tx, by, "change.create", id, map[string]any{"kind": in.Kind, "title": in.Title, "devices": len(plans)}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

func nullID(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}

func (s *Service) log_(ctx context.Context, db audit.Execer, by Actor, action string, id int64, details map[string]any) error {
	src := audit.SourcePanel
	if by.UserID == 0 {
		src = audit.SourceSystem
	}
	return audit.Log(ctx, db, audit.Entry{Source: src, ActorID: nullID(by.UserID), Actor: by.Username, IP: by.IP, Action: action,
		ObjectType: "change", ObjectID: fmt.Sprint(id), Details: details})
}

const changeCols = `c.id, c.kind, c.title, c.description, c.spec, c.status, c.run_mode, c.run_at, c.urgent_reason, c.concurrency, c.canary,
	c.max_failures, c.backup_first, c.required_approvals, c.created_by, c.creator, c.created_at, c.submitted_at, c.started_at, c.finished_at,
	(SELECT count(*) FROM change_targets t WHERE t.change_id = c.id),
	(SELECT count(*) FROM change_targets t WHERE t.change_id = c.id AND t.status = 'succeeded'),
	(SELECT count(*) FROM change_targets t WHERE t.change_id = c.id AND t.status = 'failed'),
	(SELECT count(*) FROM change_approvals a WHERE a.change_id = c.id AND a.decision = 'approve')`

func scanChange(row pgx.Row) (*Change, error) {
	var c Change
	err := row.Scan(&c.ID, &c.Kind, &c.Title, &c.Description, &c.Spec, &c.Status, &c.RunMode, &c.RunAt, &c.UrgentReason, &c.Concurrency,
		&c.Canary, &c.MaxFailures, &c.BackupFirst, &c.Required, &c.CreatedBy, &c.Creator, &c.CreatedAt, &c.SubmittedAt, &c.StartedAt,
		&c.FinishedAt, &c.Targets, &c.Succeeded, &c.Failed, &c.Approvals)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &c, err
}

// List returns changes, newest first; kind and status may be empty.
func (s *Service) List(ctx context.Context, kind, status string) ([]*Change, error) {
	rows, err := s.db.Query(ctx, `SELECT `+changeCols+` FROM changes c WHERE ($1 = '' OR c.kind = $1) AND ($2 = '' OR c.status = $2)
		ORDER BY c.created_at DESC LIMIT 200`, kind, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Change{}
	for rows.Next() {
		c, err := scanChange(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Get returns one change.
func (s *Service) Get(ctx context.Context, id int64) (*Change, error) {
	c, err := scanChange(s.db.QueryRow(ctx, `SELECT `+changeCols+` FROM changes c WHERE c.id = $1`, id))
	if err != nil {
		return nil, err
	}
	if c.Status == StatusApproved {
		c.NextRunAt = s.nextRun(ctx, c)
	}
	return c, nil
}

// Targets returns a change's devices in run order.
func (s *Service) Targets(ctx context.Context, id int64) ([]Target, error) {
	rows, err := s.db.Query(ctx, `SELECT t.id, t.device_id, d.name, t.position, t.status, t.preview, t.output, t.error, t.pre_backup_id,
		t.started_at, t.finished_at FROM change_targets t JOIN devices d ON d.id = t.device_id WHERE t.change_id = $1 ORDER BY t.position`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Target])
}

// Approvals returns the decisions recorded on a change.
func (s *Service) Approvals(ctx context.Context, id int64) ([]Approval, error) {
	rows, err := s.db.Query(ctx, `SELECT username, decision, comment, at FROM change_approvals WHERE change_id = $1 ORDER BY at`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Approval])
}

// transition moves a change from one of from to to, inside tx.
func transition(ctx context.Context, tx pgx.Tx, id int64, to string, from ...string) error {
	tag, err := tx.Exec(ctx, `UPDATE changes SET status = $2 WHERE id = $1 AND status = ANY($3)`, id, to, from)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		_ = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM changes WHERE id = $1)`, id).Scan(&exists)
		if !exists {
			return ErrNotFound
		}
		return ErrState
	}
	return nil
}

func (s *Service) inTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Submit asks for approval; a change needing none is approved at once.
func (s *Service) Submit(ctx context.Context, id int64, by Actor) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if err := transition(ctx, tx, id, StatusPending, StatusDraft); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE changes SET submitted_at = now(),
			status = CASE WHEN required_approvals = 0 THEN 'approved' ELSE status END WHERE id = $1`, id); err != nil {
			return err
		}
		return s.log_(ctx, tx, by, "change.submit", id, nil)
	})
}

// ErrOwnChange means a requester tried to approve their own change.
var ErrOwnChange = errors.New("you cannot approve your own change")

// Decide records an approval or rejection. A rejection ends the change;
// enough approvals approve it.
func (s *Service) Decide(ctx context.Context, id int64, by Actor, approve bool, comment string) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		var status string
		var createdBy *int64
		if err := tx.QueryRow(ctx, `SELECT status, created_by FROM changes WHERE id = $1 FOR UPDATE`, id).Scan(&status, &createdBy); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if status != StatusPending {
			return ErrState
		}
		if createdBy != nil && *createdBy == by.UserID {
			return ErrOwnChange
		}
		decision := map[bool]string{true: "approve", false: "reject"}[approve]
		if _, err := tx.Exec(ctx, `INSERT INTO change_approvals (change_id, user_id, username, decision, comment) VALUES ($1, $2, $3, $4, $5)`,
			id, by.UserID, by.Username, decision, strings.TrimSpace(comment)); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return invalidf("you have already decided on this change")
			}
			return err
		}
		if approve {
			if _, err := tx.Exec(ctx, `UPDATE changes SET status = 'approved' WHERE id = $1 AND required_approvals <=
				(SELECT count(*) FROM change_approvals WHERE change_id = $1 AND decision = 'approve')`, id); err != nil {
				return err
			}
		} else if err := transition(ctx, tx, id, StatusRejected, StatusPending); err != nil {
			return err
		}
		return s.log_(ctx, tx, by, "change."+decision, id, map[string]any{"comment": comment})
	})
}

// Cancel stops a change. A running change finishes the devices in progress
// and skips the rest.
func (s *Service) Cancel(ctx context.Context, id int64, by Actor) error {
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := transition(ctx, tx, id, StatusCancelled, StatusDraft, StatusPending, StatusApproved); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE change_targets SET status = 'skipped' WHERE change_id = $1 AND status = 'pending'`, id); err != nil {
			return err
		}
		return s.log_(ctx, tx, by, "change.cancel", id, nil)
	})
	if errors.Is(err, ErrState) {
		c, gerr := s.Get(ctx, id)
		if gerr == nil && c.Status == StatusRunning {
			s.mu.Lock()
			s.cancel[id] = true
			s.mu.Unlock()
			return s.log_(ctx, s.db, by, "change.cancel", id, map[string]any{"while": "running"})
		}
	}
	return err
}

// RunNow lets an approved change skip its maintenance window.
func (s *Service) RunNow(ctx context.Context, id int64, by Actor, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return invalidf("running outside the maintenance window needs a reason")
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE changes SET run_mode = 'now', urgent_reason = $2 WHERE id = $1 AND status IN ('approved', 'pending', 'draft')`, id, reason)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrState
		}
		return s.log_(ctx, tx, by, "change.run_now", id, map[string]any{"reason": reason})
	})
}
