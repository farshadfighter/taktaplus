package compliance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/farshadfighter/taktaplus/internal/backup"
	"github.com/farshadfighter/taktaplus/internal/changes"
	"github.com/farshadfighter/taktaplus/internal/fortiosconf"
	"github.com/farshadfighter/taktaplus/internal/inventory"
	"github.com/farshadfighter/taktaplus/internal/scripts"
)

// InputError is a validation failure to show to the user.
type InputError struct{ msg string }

func (e InputError) Error() string { return e.msg }

func invalidf(format string, a ...any) error { return InputError{fmt.Sprintf(format, a...)} }

// ErrNotFound means the template, rule, result or exception does not exist.
var ErrNotFound = errors.New("not found")

// Template is a configuration baseline applied to groups of devices.
type Template struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Platform    string    `json:"platform"`
	Body        string    `json:"body"`
	GroupIDs    []int64   `json:"group_ids"` // empty: all devices of the platform
	AutoFix     bool      `json:"auto_fix"`
	Version     int       `json:"version"`
	UpdatedBy   string    `json:"updated_by"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (t *Template) appliesTo(d *inventory.Device) bool {
	if string(d.Kind) != t.Platform {
		return false
	}
	return len(t.GroupIDs) == 0 || (d.GroupID != nil && slices.Contains(t.GroupIDs, *d.GroupID))
}

// Drift is how a device departs from one template.
type Drift struct {
	TemplateID  int64       `json:"template_id"`
	Template    string      `json:"template"`
	Differences []DriftItem `json:"differences"`
	Error       string      `json:"error,omitempty"` // the template could not be rendered for the device
}

// DriftItem is one difference, possibly accepted as an exception.
type DriftItem struct {
	fortiosconf.Difference
	Excepted bool `json:"excepted"`
}

func driftRef(templateID int64, d fortiosconf.Difference) string {
	return fmt.Sprintf("drift:%d:%s|%s", templateID, d.Path, d.Key)
}

// Result is the latest evaluation of a device.
type Result struct {
	DeviceID    int64     `json:"device_id"`
	DeviceName  string    `json:"device_name"`
	Kind        string    `json:"kind"`
	BackupID    *int64    `json:"backup_id"`
	EvaluatedAt time.Time `json:"evaluated_at"`
	Score       int       `json:"score"`
	Drift       []Drift   `json:"drift"`
	Violations  []Finding `json:"violations"`
	Error       string    `json:"error"`
	Templates   []string  `json:"templates"`
}

// Counts summarizes open (not excepted) problems.
func (r *Result) Counts() (driftTemplates, differences, rules int) {
	broken := map[int64]bool{}
	for _, d := range r.Drift {
		n := 0
		for _, x := range d.Differences {
			if !x.Excepted {
				n++
			}
		}
		if n > 0 || d.Error != "" {
			driftTemplates++
		}
		differences += n
	}
	for _, v := range r.Violations {
		if !v.Excepted {
			broken[v.RuleID] = true
		}
	}
	return driftTemplates, differences, len(broken)
}

// Service evaluates compliance and keeps templates, rules and results.
type Service struct {
	db      *pgxpool.Pool
	inv     *inventory.Service
	backups *backup.Service
	scripts *scripts.Service
	changes *changes.Service
	log     *slog.Logger
	now     func() time.Time

	mu      sync.Mutex
	pending map[int64]bool // devices queued for evaluation
	queue   chan int64
}

// New returns the service.
func New(db *pgxpool.Pool, inv *inventory.Service, b *backup.Service, sc *scripts.Service, ch *changes.Service, log *slog.Logger) *Service {
	return &Service{db: db, inv: inv, backups: b, scripts: sc, changes: ch, log: log, now: time.Now,
		pending: map[int64]bool{}, queue: make(chan int64, 1024)}
}

// --- templates ---

const templateCols = `id, name, description, platform, body, group_ids, auto_fix, version, updated_by, updated_at`

func scanTemplate(row pgx.Row) (*Template, error) {
	var t Template
	err := row.Scan(&t.ID, &t.Name, &t.Description, &t.Platform, &t.Body, &t.GroupIDs, &t.AutoFix, &t.Version, &t.UpdatedBy, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &t, err
}

// Templates lists templates.
func (s *Service) Templates(ctx context.Context) ([]*Template, error) {
	rows, err := s.db.Query(ctx, `SELECT `+templateCols+` FROM compliance_templates ORDER BY lower(name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Template{}
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Template returns one template.
func (s *Service) Template(ctx context.Context, id int64) (*Template, error) {
	return scanTemplate(s.db.QueryRow(ctx, `SELECT `+templateCols+` FROM compliance_templates WHERE id = $1`, id))
}

// SaveTemplate creates (ID 0) or updates a template; every change bumps
// its version.
func (s *Service) SaveTemplate(ctx context.Context, t Template, by string) (int64, error) {
	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" || len(t.Name) > 100 {
		return 0, invalidf("template name is required")
	}
	if t.Platform != "fortigate" && t.Platform != "fortiweb" {
		return 0, invalidf("platform must be fortigate or fortiweb")
	}
	if strings.TrimSpace(t.Body) == "" {
		return 0, invalidf("the template is empty")
	}
	if len(fortiosconf.Parse(t.Body).Children) == 0 {
		return 0, invalidf("the template needs at least one \"config … end\" section")
	}
	if t.GroupIDs == nil {
		t.GroupIDs = []int64{}
	}
	var err error
	if t.ID == 0 {
		err = s.db.QueryRow(ctx, `INSERT INTO compliance_templates (name, description, platform, body, group_ids, auto_fix, updated_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`, t.Name, t.Description, t.Platform, t.Body, t.GroupIDs, t.AutoFix, by).Scan(&t.ID)
	} else {
		var tag interface{ RowsAffected() int64 }
		tag, err = s.db.Exec(ctx, `UPDATE compliance_templates SET name = $2, description = $3, platform = $4, body = $5, group_ids = $6,
			auto_fix = $7, updated_by = $8, updated_at = now(), version = version + 1 WHERE id = $1`,
			t.ID, t.Name, t.Description, t.Platform, t.Body, t.GroupIDs, t.AutoFix, by)
		if err == nil && tag.RowsAffected() == 0 {
			return 0, ErrNotFound
		}
	}
	if err != nil && strings.Contains(err.Error(), "compliance_templates_name_key") {
		return 0, invalidf("a template with this name already exists")
	}
	return t.ID, err
}

// DeleteTemplate removes a template.
func (s *Service) DeleteTemplate(ctx context.Context, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM compliance_templates WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// --- rules ---

const ruleCols = `id, name, description, severity, platform, group_id, section, scope, match, require, builtin, enabled`

func scanRule(row pgx.Row) (*Rule, error) {
	var r Rule
	var match, require []byte
	err := row.Scan(&r.ID, &r.Name, &r.Description, &r.Severity, &r.Platform, &r.GroupID, &r.Section, &r.Scope, &match, &require, &r.Builtin, &r.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(match, &r.Match)
	_ = json.Unmarshal(require, &r.Require)
	return &r, nil
}

// Rules lists rules, built-in first.
func (s *Service) Rules(ctx context.Context) ([]*Rule, error) {
	rows, err := s.db.Query(ctx, `SELECT `+ruleCols+` FROM compliance_rules ORDER BY builtin DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Rule{}
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SaveRule creates (ID 0) or updates a rule. Built-in rules keep their
// conditions; only severity, scope group and enabled can change.
func (s *Service) SaveRule(ctx context.Context, r Rule) (int64, error) {
	if r.ID != 0 {
		old, err := scanRule(s.db.QueryRow(ctx, `SELECT `+ruleCols+` FROM compliance_rules WHERE id = $1`, r.ID))
		if err != nil {
			return 0, err
		}
		if old.Builtin {
			old.Severity, old.GroupID, old.Enabled = r.Severity, r.GroupID, r.Enabled
			r = *old
		}
	}
	if err := r.validate(); err != nil {
		return 0, err
	}
	match, _ := json.Marshal(r.Match)
	require, _ := json.Marshal(r.Require)
	if r.ID == 0 {
		err := s.db.QueryRow(ctx, `INSERT INTO compliance_rules (name, description, severity, platform, group_id, section, scope, match, require, enabled)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id`,
			r.Name, r.Description, r.Severity, r.Platform, r.GroupID, r.Section, r.Scope, match, require, r.Enabled).Scan(&r.ID)
		return r.ID, err
	}
	_, err := s.db.Exec(ctx, `UPDATE compliance_rules SET name = $2, description = $3, severity = $4, platform = $5, group_id = $6,
		section = $7, scope = $8, match = $9, require = $10, enabled = $11, updated_at = now() WHERE id = $1`,
		r.ID, r.Name, r.Description, r.Severity, r.Platform, r.GroupID, r.Section, r.Scope, match, require, r.Enabled)
	return r.ID, err
}

// DeleteRule removes a custom rule.
func (s *Service) DeleteRule(ctx context.Context, id int64) error {
	var builtin bool
	if err := s.db.QueryRow(ctx, `SELECT builtin FROM compliance_rules WHERE id = $1`, id).Scan(&builtin); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if builtin {
		return invalidf("built-in rules cannot be deleted; disable them instead")
	}
	_, err := s.db.Exec(ctx, `DELETE FROM compliance_rules WHERE id = $1`, id)
	return err
}

// --- exceptions ---

// Exception accepts one drift item or rule finding on a device.
type Exception struct {
	ID        int64     `json:"id"`
	DeviceID  int64     `json:"device_id"`
	Ref       string    `json:"ref"`
	Reason    string    `json:"reason"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

// AddException records an accepted deviation and re-scores the device.
func (s *Service) AddException(ctx context.Context, deviceID int64, ref, reason, by string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return invalidf("a reason is required")
	}
	if !strings.HasPrefix(ref, "rule:") && !strings.HasPrefix(ref, "drift:") {
		return invalidf("invalid exception reference")
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO compliance_exceptions (device_id, ref, reason, created_by) VALUES ($1, $2, $3, $4)
		ON CONFLICT (device_id, ref) DO UPDATE SET reason = EXCLUDED.reason, created_by = EXCLUDED.created_by, created_at = now()`,
		deviceID, ref, reason, by); err != nil {
		return err
	}
	_, err := s.Evaluate(ctx, deviceID)
	return err
}

// Exceptions lists the exceptions of a device.
func (s *Service) Exceptions(ctx context.Context, deviceID int64) ([]Exception, error) {
	rows, err := s.db.Query(ctx, `SELECT id, device_id, ref, reason, created_by, created_at FROM compliance_exceptions
		WHERE device_id = $1 ORDER BY created_at DESC`, deviceID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Exception])
}

// DeleteException removes an exception and re-scores the device.
func (s *Service) DeleteException(ctx context.Context, id int64) error {
	var dev int64
	if err := s.db.QueryRow(ctx, `DELETE FROM compliance_exceptions WHERE id = $1 RETURNING device_id`, id).Scan(&dev); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	_, err := s.Evaluate(ctx, dev)
	return err
}

// --- evaluation ---

// score is 100 minus a penalty per broken rule (by severity) and 5 per
// drifting template, never below zero.
func score(r *Result) int {
	sev := map[int64]string{}
	for _, v := range r.Violations {
		if !v.Excepted {
			sev[v.RuleID] = v.Severity
		}
	}
	p := 0
	for _, s := range sev {
		p += severityPenalty[s]
	}
	drifting, _, _ := r.Counts()
	p += 5 * drifting
	return max(0, 100-p)
}

// Evaluate checks the device's latest configuration backup and stores the
// result.
func (s *Service) Evaluate(ctx context.Context, deviceID int64) (*Result, error) {
	d, err := s.inv.Get(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	res := &Result{DeviceID: d.ID, DeviceName: d.Name, Kind: string(d.Kind), EvaluatedAt: s.now(), Drift: []Drift{}, Violations: []Finding{}, Templates: []string{}}
	var backupID int64
	err = s.db.QueryRow(ctx, `SELECT id FROM backups WHERE device_id = $1 AND status = 'ok' ORDER BY at DESC, id DESC LIMIT 1`, deviceID).Scan(&backupID)
	if errors.Is(err, pgx.ErrNoRows) {
		res.Error = "no configuration backup yet"
		return res, s.store(ctx, res)
	} else if err != nil {
		return nil, err
	}
	res.BackupID = &backupID
	cfg, _, err := s.backups.Content(ctx, backupID)
	if err != nil {
		return nil, err
	}
	root := fortiosconf.Parse(string(cfg))
	excepted := map[string]bool{}
	rows, err := s.db.Query(ctx, `SELECT ref FROM compliance_exceptions WHERE device_id = $1`, deviceID)
	if err != nil {
		return nil, err
	}
	refs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	for _, r := range refs {
		excepted[r] = true
	}

	tpls, err := s.Templates(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range tpls {
		if !t.appliesTo(d) {
			continue
		}
		res.Templates = append(res.Templates, t.Name)
		dr := Drift{TemplateID: t.ID, Template: t.Name, Differences: []DriftItem{}}
		body, err := s.scripts.Render(ctx, t.Body, d, nil)
		if err != nil {
			dr.Error = err.Error()
			res.Drift = append(res.Drift, dr)
			continue
		}
		for _, x := range fortiosconf.Compare(fortiosconf.Parse(body), root) {
			dr.Differences = append(dr.Differences, DriftItem{Difference: x, Excepted: excepted[driftRef(t.ID, x)]})
		}
		if len(dr.Differences) > 0 {
			res.Drift = append(res.Drift, dr)
		}
	}
	rules, err := s.Rules(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range rules {
		if !r.Enabled || r.Platform != string(d.Kind) || (r.GroupID != nil && (d.GroupID == nil || *d.GroupID != *r.GroupID)) {
			continue
		}
		for _, f := range r.Check(root) {
			f.Excepted = excepted[f.ref()]
			res.Violations = append(res.Violations, f)
		}
	}
	res.Score = score(res)
	if err := s.store(ctx, res); err != nil {
		return nil, err
	}
	s.autoFix(ctx, d, res, tpls)
	return res, nil
}

func (s *Service) store(ctx context.Context, r *Result) error {
	drift, _ := json.Marshal(r.Drift)
	viol, _ := json.Marshal(r.Violations)
	_, err := s.db.Exec(ctx, `INSERT INTO compliance_results (device_id, backup_id, evaluated_at, score, drift, violations, error)
		VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT (device_id) DO UPDATE SET backup_id = EXCLUDED.backup_id,
		evaluated_at = EXCLUDED.evaluated_at, score = EXCLUDED.score, drift = EXCLUDED.drift, violations = EXCLUDED.violations, error = EXCLUDED.error`,
		r.DeviceID, r.BackupID, r.EvaluatedAt, r.Score, drift, viol, r.Error)
	return err
}

// Results returns the latest result of every device, worst first.
func (s *Service) Results(ctx context.Context) ([]*Result, error) {
	rows, err := s.db.Query(ctx, `SELECT r.device_id, d.name, d.kind, r.backup_id, r.evaluated_at, r.score, r.drift, r.violations, r.error
		FROM compliance_results r JOIN devices d ON d.id = r.device_id ORDER BY r.error = '' DESC, r.score, lower(d.name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Result{}
	for rows.Next() {
		var r Result
		var drift, viol []byte
		if err := rows.Scan(&r.DeviceID, &r.DeviceName, &r.Kind, &r.BackupID, &r.EvaluatedAt, &r.Score, &drift, &viol, &r.Error); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(drift, &r.Drift)
		_ = json.Unmarshal(viol, &r.Violations)
		out = append(out, &r)
	}
	return out, rows.Err()
}

// Result returns the latest result of a device.
func (s *Service) Result(ctx context.Context, deviceID int64) (*Result, error) {
	list, err := s.Results(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range list {
		if r.DeviceID == deviceID {
			return r, nil
		}
	}
	return nil, ErrNotFound
}

// EvaluateAll evaluates every licensed device.
func (s *Service) EvaluateAll(ctx context.Context) error {
	list, err := s.inv.List(ctx, inventory.Filter{})
	if err != nil {
		return err
	}
	for _, d := range list {
		if !d.Operational() {
			continue
		}
		if _, err := s.Evaluate(ctx, d.ID); err != nil {
			s.log.Warn("compliance evaluation", "device", d.Name, "err", err)
		}
	}
	return nil
}

// Queue asks for a device to be evaluated soon (after a backup).
func (s *Service) Queue(deviceID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending[deviceID] {
		return
	}
	select {
	case s.queue <- deviceID:
		s.pending[deviceID] = true
	default:
	}
}

// Run evaluates queued devices, and every device daily.
func (s *Service) Run(ctx context.Context) {
	daily := time.NewTicker(24 * time.Hour)
	defer daily.Stop()
	if err := s.EvaluateAll(ctx); err != nil && ctx.Err() == nil {
		s.log.Warn("compliance evaluation", "err", err)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-s.queue:
			s.mu.Lock()
			delete(s.pending, id)
			s.mu.Unlock()
			if _, err := s.Evaluate(ctx, id); err != nil && ctx.Err() == nil {
				s.log.Warn("compliance evaluation", "device", id, "err", err)
			}
		case <-daily.C:
			if err := s.EvaluateAll(ctx); err != nil && ctx.Err() == nil {
				s.log.Warn("compliance evaluation", "err", err)
			}
		}
	}
}

// --- fixing drift ---

func fixTitle(t *Template, d *inventory.Device) string {
	return fmt.Sprintf("اصلاح انحراف از قالب «%s» (نسخه %d) روی %s", t.Name, t.Version, d.Name)
}

// Fix opens a change request that applies the template to the device and
// submits it for approval.
func (s *Service) Fix(ctx context.Context, deviceID, templateID int64, by changes.Actor) (*changes.Change, error) {
	d, err := s.inv.Get(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	t, err := s.Template(ctx, templateID)
	if err != nil {
		return nil, err
	}
	if !t.appliesTo(d) {
		return nil, invalidf("the template does not apply to this device")
	}
	spec, _ := json.Marshal(scripts.Spec{Body: t.Body})
	c, err := s.changes.Create(ctx, changes.CreateInput{Kind: scripts.Kind, Title: fixTitle(t, d),
		Description: "قالب پیکربندی دوباره روی دستگاه اعمال می‌شود تا انحراف برطرف شود.", Spec: spec, DeviceIDs: []int64{d.ID}}, by)
	if err != nil {
		return nil, err
	}
	if err := s.changes.Submit(ctx, c.ID, by); err != nil {
		return nil, err
	}
	return s.changes.Get(ctx, c.ID)
}

// autoFix opens fix changes for drifting templates marked auto_fix, unless
// one is already open.
func (s *Service) autoFix(ctx context.Context, d *inventory.Device, res *Result, tpls []*Template) {
	byID := map[int64]*Template{}
	for _, t := range tpls {
		byID[t.ID] = t
	}
	for _, dr := range res.Drift {
		t := byID[dr.TemplateID]
		if t == nil || !t.AutoFix || dr.Error != "" {
			continue
		}
		open := false
		for _, x := range dr.Differences {
			if !x.Excepted {
				open = true
			}
		}
		if !open {
			continue
		}
		var exists bool
		if err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM changes WHERE title = $1 AND status IN ('draft', 'pending', 'approved', 'running'))`,
			fixTitle(t, d)).Scan(&exists); err != nil || exists {
			continue
		}
		if _, err := s.Fix(ctx, d.ID, t.ID, changes.Actor{Username: "compliance"}); err != nil {
			s.log.Warn("automatic drift fix", "device", d.Name, "template", t.Name, "err", err)
		}
	}
}
