// Package scripts is the CLI script library (F1): templates with global,
// group and device variables, run over SSH through the change workflow, or
// directly when read-only.
package scripts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/farshadfighter/taktaplus/internal/changes"
	"github.com/farshadfighter/taktaplus/internal/device"
	"github.com/farshadfighter/taktaplus/internal/inventory"
)

// Kind is the change kind for scripts.
const Kind = "script"

// ErrNotFound means the script or variable does not exist.
var ErrNotFound = errors.New("not found")

func invalidf(format string, a ...any) error { return changes.Invalid(fmt.Sprintf(format, a...)) }

// Script is a library entry.
type Script struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Platform    string    `json:"platform"`
	Body        string    `json:"body"`
	ReadOnly    bool      `json:"read_only"`
	UpdatedBy   string    `json:"updated_by"`
	UpdatedAt   time.Time `json:"updated_at"`
}

var readOnlyRe = regexp.MustCompile(`(?i)^(get|show|diagnose|diag|execute (ping|ping6|traceroute|tracert|log display|log filter))\b`)

// IsReadOnly reports whether every command line only reads state.
// Template actions are ignored; their output is checked when rendered.
func IsReadOnly(text string) bool {
	any := false
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(l, "{{") && strings.HasSuffix(l, "}}") {
			continue
		}
		if !readOnlyRe.MatchString(l) {
			return false
		}
		any = true
	}
	return any
}

func parse(body string) (*template.Template, error) {
	return template.New("script").Option("missingkey=error").Parse(body)
}

// Service manages scripts and variables.
type Service struct {
	db  *pgxpool.Pool
	inv *inventory.Service
}

// New returns the service.
func New(db *pgxpool.Pool, inv *inventory.Service) *Service { return &Service{db: db, inv: inv} }

// List returns all scripts.
func (s *Service) List(ctx context.Context) ([]Script, error) {
	rows, err := s.db.Query(ctx, `SELECT id, name, description, platform, body, read_only, updated_by, updated_at FROM scripts ORDER BY lower(name)`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Script])
}

// Get returns one script.
func (s *Service) Get(ctx context.Context, id int64) (*Script, error) {
	rows, err := s.db.Query(ctx, `SELECT id, name, description, platform, body, read_only, updated_by, updated_at FROM scripts WHERE id = $1`, id)
	if err != nil {
		return nil, err
	}
	sc, err := pgx.CollectExactlyOneRow(rows, pgx.RowToAddrOfStructByPos[Script])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return sc, err
}

// Save creates (ID 0) or updates a script. Whether it is read-only is
// derived from its lines.
func (s *Service) Save(ctx context.Context, sc Script, by string) (int64, error) {
	sc.Name = strings.TrimSpace(sc.Name)
	sc.Body = strings.TrimSpace(strings.ReplaceAll(sc.Body, "\r\n", "\n"))
	if sc.Name == "" {
		return 0, invalidf("script name is required")
	}
	if sc.Body == "" {
		return 0, invalidf("the script is empty")
	}
	if sc.Platform == "" {
		sc.Platform = "fortigate"
	}
	if sc.Platform != "fortigate" && sc.Platform != "fortiweb" {
		return 0, invalidf("platform must be fortigate or fortiweb")
	}
	if _, err := parse(sc.Body); err != nil {
		return 0, invalidf("template error: %v", err)
	}
	sc.ReadOnly = IsReadOnly(sc.Body)
	var err error
	if sc.ID == 0 {
		err = s.db.QueryRow(ctx, `INSERT INTO scripts (name, description, platform, body, read_only, updated_by) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
			sc.Name, sc.Description, sc.Platform, sc.Body, sc.ReadOnly, by).Scan(&sc.ID)
	} else {
		var tag pgconn.CommandTag
		tag, err = s.db.Exec(ctx, `UPDATE scripts SET name = $2, description = $3, platform = $4, body = $5, read_only = $6, updated_by = $7, updated_at = now()
			WHERE id = $1`, sc.ID, sc.Name, sc.Description, sc.Platform, sc.Body, sc.ReadOnly, by)
		if err == nil && tag.RowsAffected() == 0 {
			return 0, ErrNotFound
		}
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return 0, invalidf("a script with this name already exists")
	}
	return sc.ID, err
}

// Delete removes a script.
func (s *Service) Delete(ctx context.Context, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM scripts WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// Variable is a template variable.
type Variable struct {
	ID      int64  `json:"id"`
	Scope   string `json:"scope"`
	ScopeID int64  `json:"scope_id"`
	Name    string `json:"name"`
	Value   string `json:"value"`
}

var varName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// Variables returns all variables.
func (s *Service) Variables(ctx context.Context) ([]Variable, error) {
	rows, err := s.db.Query(ctx, `SELECT id, scope, scope_id, name, value FROM variables ORDER BY scope, scope_id, name`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Variable])
}

// SetVariable creates or replaces a variable.
func (s *Service) SetVariable(ctx context.Context, v Variable) error {
	if !varName.MatchString(v.Name) {
		return invalidf("variable names use letters, digits and underscores and start with a letter")
	}
	switch v.Scope {
	case "global":
		v.ScopeID = 0
	case "group", "device":
		if v.ScopeID <= 0 {
			return invalidf("choose the %s", v.Scope)
		}
	default:
		return invalidf("scope must be global, group or device")
	}
	_, err := s.db.Exec(ctx, `INSERT INTO variables (scope, scope_id, name, value) VALUES ($1, $2, $3, $4)
		ON CONFLICT (scope, scope_id, name) DO UPDATE SET value = EXCLUDED.value`, v.Scope, v.ScopeID, v.Name, v.Value)
	return err
}

// DeleteVariable removes a variable.
func (s *Service) DeleteVariable(ctx context.Context, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM variables WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// DeviceData is what templates see as .Device.
type DeviceData struct {
	Name, Host, Serial, Model, Firmware, Hostname, Group string
	Tags                                                 []string
}

// vars merges global, group and device variables, then overrides.
func (s *Service) vars(ctx context.Context, d *inventory.Device, overrides map[string]string) (map[string]string, error) {
	var group int64
	if d.GroupID != nil {
		group = *d.GroupID
	}
	rows, err := s.db.Query(ctx, `SELECT scope, name, value FROM variables
		WHERE scope = 'global' OR (scope = 'group' AND scope_id = $1) OR (scope = 'device' AND scope_id = $2)`, group, d.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rank := map[string]int{"global": 0, "group": 1, "device": 2}
	type entry struct {
		value string
		rank  int
	}
	merged := map[string]entry{}
	for rows.Next() {
		var scope, name, value string
		if err := rows.Scan(&scope, &name, &value); err != nil {
			return nil, err
		}
		if e, ok := merged[name]; !ok || rank[scope] >= e.rank {
			merged[name] = entry{value, rank[scope]}
		}
	}
	out := map[string]string{}
	for k, e := range merged {
		out[k] = e.value
	}
	for k, v := range overrides {
		out[k] = v
	}
	return out, rows.Err()
}

// Render fills a script body for a device.
func (s *Service) Render(ctx context.Context, body string, d *inventory.Device, overrides map[string]string) (string, error) {
	t, err := parse(body)
	if err != nil {
		return "", invalidf("template error: %v", err)
	}
	vars, err := s.vars(ctx, d, overrides)
	if err != nil {
		return "", err
	}
	data := map[string]any{
		"Device": DeviceData{Name: d.Name, Host: d.Host, Serial: d.Serial, Model: d.Model, Firmware: d.Firmware, Hostname: d.Hostname,
			Group: d.GroupName, Tags: d.Tags},
		"Vars": vars,
	}
	var b bytes.Buffer
	if err := t.Execute(&b, data); err != nil {
		msg := err.Error()
		if i := strings.Index(msg, "map has no entry for key"); i >= 0 {
			msg = "variable " + strings.TrimPrefix(msg[i:], "map has no entry for key ") + " is not defined for this device"
		}
		return "", invalidf("%s", msg)
	}
	return strings.TrimSpace(b.String()), nil
}

// Spec is a script change: the script text is copied into the change so
// approvers approve exactly what runs.
type Spec struct {
	ScriptID int64             `json:"script_id"`
	Body     string            `json:"body"`
	Vars     map[string]string `json:"vars"`
}

// Runner runs scripts as changes.
type Runner struct{ s *Service }

// NewRunner returns the change runner.
func NewRunner(s *Service) *Runner { return &Runner{s: s} }

// Preview renders the script for the device.
func (r *Runner) Preview(ctx context.Context, raw json.RawMessage, d *inventory.Device) (string, error) {
	var sp Spec
	if err := json.Unmarshal(raw, &sp); err != nil || strings.TrimSpace(sp.Body) == "" {
		return "", invalidf("the script is empty")
	}
	if !d.HasSSH {
		return "", invalidf("scripts need SSH credentials on the device")
	}
	return r.s.Render(ctx, sp.Body, d, sp.Vars)
}

// Run renders and runs the script over SSH.
func (r *Runner) Run(ctx context.Context, c *changes.Change, d *inventory.Device, out io.Writer) error {
	text, err := r.Preview(ctx, c.Spec, d)
	if err != nil {
		return err
	}
	cli, ok := device.AsCLI(r.s.inv.Driver(d))
	if !ok {
		return errors.New("the device has no SSH access")
	}
	res, err := cli.RunScript(ctx, text)
	fmt.Fprintln(out, res)
	return err
}

// Run is a read-only run's result for one device.
type Run struct {
	ID         int64     `json:"id"`
	ScriptID   *int64    `json:"script_id"`
	DeviceID   int64     `json:"device_id"`
	DeviceName string    `json:"device_name"`
	At         time.Time `json:"at"`
	Actor      string    `json:"actor"`
	Script     string    `json:"script"`
	Output     string    `json:"output"`
	OK         bool      `json:"ok"`
	Error      string    `json:"error"`
}

// RunReadOnly runs a read-only script on devices at once, without the
// change workflow, and stores the outputs.
func (s *Service) RunReadOnly(ctx context.Context, id int64, deviceIDs []int64, actor string) ([]Run, error) {
	sc, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !sc.ReadOnly {
		return nil, invalidf("this script changes configuration; run it through a change request")
	}
	if len(deviceIDs) == 0 || len(deviceIDs) > 200 {
		return nil, invalidf("choose between 1 and 200 devices")
	}
	results := make([]Run, len(deviceIDs))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, did := range deviceIDs {
		wg.Add(1)
		go func(i int, did int64) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res := Run{ScriptID: &sc.ID, DeviceID: did, Actor: actor, At: time.Now()}
			defer func() { results[i] = res }()
			d, err := s.inv.Get(ctx, did)
			if err != nil {
				res.Error = err.Error()
				return
			}
			res.DeviceName = d.Name
			if !d.Operational() {
				res.Error = "the device's license is not active"
				return
			}
			text, err := s.Render(ctx, sc.Body, d, nil)
			if err != nil {
				res.Error = err.Error()
				return
			}
			res.Script = text
			if !IsReadOnly(text) {
				res.Error = "the rendered script is not read-only"
				return
			}
			cli, ok := device.AsCLI(s.inv.Driver(d))
			if !ok {
				res.Error = "the device has no SSH access"
				return
			}
			rctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			res.Output, err = cli.RunScript(rctx, text)
			res.OK = err == nil
			if err != nil {
				res.Error = err.Error()
			}
		}(i, did)
	}
	wg.Wait()
	for i := range results {
		r := &results[i]
		if r.DeviceName == "" && r.Error != "" {
			continue // unknown device
		}
		if err := s.db.QueryRow(ctx, `INSERT INTO script_runs (script_id, device_id, at, actor, script, output, ok, error)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`, r.ScriptID, r.DeviceID, r.At, r.Actor, r.Script, r.Output, r.OK, r.Error).Scan(&r.ID); err != nil {
			return nil, err
		}
	}
	return results, nil
}

// Runs returns recent read-only runs of a script, newest first.
func (s *Service) Runs(ctx context.Context, scriptID int64) ([]Run, error) {
	rows, err := s.db.Query(ctx, `SELECT r.id, r.script_id, r.device_id, d.name, r.at, r.actor, r.script, r.output, r.ok, r.error
		FROM script_runs r JOIN devices d ON d.id = r.device_id WHERE r.script_id = $1 ORDER BY r.at DESC LIMIT 200`, scriptID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Run])
}
