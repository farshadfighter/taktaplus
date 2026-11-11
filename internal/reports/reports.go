// Package reports builds PDF and Excel reports from the log store, on demand
// or on a schedule, and emails them.
package reports

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/farshadfighter/taktaplus/internal/alerts"
	"github.com/farshadfighter/taktaplus/internal/inventory"
	"github.com/farshadfighter/taktaplus/internal/logs"
)

// InputError is a validation failure to show to the user.
type InputError struct{ msg string }

func (e InputError) Error() string { return e.msg }

func invalidf(format string, a ...any) error { return InputError{fmt.Sprintf(format, a...)} }

// ErrNotFound means the template, schedule or run does not exist.
var ErrNotFound = errors.New("not found")

// Formats.
const (
	FormatPDF  = "pdf"
	FormatXLSX = "xlsx"
)

// Run statuses.
const (
	StatusRunning = "running"
	StatusReady   = "ready"
	StatusSent    = "sent"
	StatusFailed  = "failed"
)

// Template is a report layout: an ordered list of widgets.
type Template struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Widgets     []string  `json:"widgets"`
	Builtin     bool      `json:"builtin"`
	UpdatedBy   string    `json:"updated_by"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Schedule runs a template regularly and emails the result.
type Schedule struct {
	ID         int64     `json:"id"`
	TemplateID int64     `json:"template_id"`
	Template   string    `json:"template"`
	Frequency  string    `json:"frequency"` // daily, weekly, monthly (Jalali month)
	Weekday    int       `json:"weekday"`   // 0 = Sunday … 6 = Saturday
	Monthday   int       `json:"monthday"`  // 1–29, day of the Jalali month
	AtMinute   int       `json:"at_minute"` // minutes after local midnight
	DeviceIDs  []int64   `json:"device_ids"`
	GroupID    *int64    `json:"group_id"`
	Formats    []string  `json:"formats"`
	ChannelID  *int64    `json:"channel_id"`
	Recipients []string  `json:"recipients"`
	Enabled    bool      `json:"enabled"`
	NextRunAt  time.Time `json:"next_run_at"`
	CreatedBy  string    `json:"created_by"`
}

// File is one generated output.
type File struct {
	Format string `json:"format"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
}

// Run is one generated report.
type Run struct {
	ID         int64      `json:"id"`
	TemplateID *int64     `json:"template_id"`
	ScheduleID *int64     `json:"schedule_id"`
	Title      string     `json:"title"`
	From       time.Time  `json:"period_from"`
	To         time.Time  `json:"period_to"`
	DeviceIDs  []int64    `json:"device_ids"`
	Formats    []string   `json:"formats"`
	Status     string     `json:"status"`
	Error      string     `json:"error"`
	Files      []File     `json:"files"`
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

// Options configure the service.
type Options struct {
	Dir      string         // where generated files are kept
	TmpDir   string         // scratch space for the PDF renderer
	Chromium string         // browser binary for PDF output
	Location *time.Location // time zone of schedules and report dates
	KeepDays int            // generated reports older than this are removed; default 365
}

// Service manages templates, schedules and runs.
type Service struct {
	db     *pgxpool.Pool
	logs   *logs.Service
	alerts *alerts.Service
	inv    *inventory.Service
	log    *slog.Logger
	opt    Options
	now    func() time.Time
	// pdf renders HTML to PDF; tests replace it.
	pdf func(ctx context.Context, page []byte) ([]byte, error)

	wg      sync.WaitGroup
	running sync.Map // run ID → struct{}
}

// New returns the service.
func New(db *pgxpool.Pool, lg *logs.Service, al *alerts.Service, inv *inventory.Service, log *slog.Logger, opt Options) *Service {
	if opt.Location == nil {
		opt.Location = time.UTC
	}
	if opt.KeepDays == 0 {
		opt.KeepDays = 365
	}
	if opt.TmpDir == "" {
		opt.TmpDir = os.TempDir()
	}
	s := &Service{db: db, logs: lg, alerts: al, inv: inv, log: log, opt: opt, now: time.Now}
	s.pdf = func(ctx context.Context, page []byte) ([]byte, error) {
		return PDF(ctx, opt.Chromium, opt.TmpDir, page)
	}
	return s
}

// Location is the time zone of schedules and report dates.
func (s *Service) Location() *time.Location { return s.opt.Location }

// --- templates ---

func scanTemplate(row pgx.Row) (*Template, error) {
	var t Template
	err := row.Scan(&t.ID, &t.Name, &t.Description, &t.Widgets, &t.Builtin, &t.UpdatedBy, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &t, err
}

const templateCols = `id, name, description, widgets, builtin, updated_by, updated_at`

// Templates lists templates, built-in first.
func (s *Service) Templates(ctx context.Context) ([]*Template, error) {
	rows, err := s.db.Query(ctx, `SELECT `+templateCols+` FROM report_templates ORDER BY builtin DESC, id`)
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
	return scanTemplate(s.db.QueryRow(ctx, `SELECT `+templateCols+` FROM report_templates WHERE id = $1`, id))
}

// SaveTemplate creates (ID 0) or updates a custom template.
func (s *Service) SaveTemplate(ctx context.Context, t Template, by string) (int64, error) {
	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" || len(t.Name) > 100 {
		return 0, invalidf("template name is required (at most 100 characters)")
	}
	if len(t.Widgets) == 0 || len(t.Widgets) > 30 {
		return 0, invalidf("choose 1–30 widgets")
	}
	seen := map[string]bool{}
	for _, w := range t.Widgets {
		if !logs.KnownWidget(w) {
			return 0, invalidf("unknown widget %q", w)
		}
		if seen[w] {
			return 0, invalidf("widget %q is listed twice", w)
		}
		seen[w] = true
	}
	if t.ID != 0 {
		old, err := s.Template(ctx, t.ID)
		if err != nil {
			return 0, err
		}
		if old.Builtin {
			return 0, invalidf("built-in templates cannot be changed; save a copy instead")
		}
		_, err = s.db.Exec(ctx, `UPDATE report_templates SET name = $2, description = $3, widgets = $4, updated_by = $5, updated_at = now() WHERE id = $1`,
			t.ID, t.Name, t.Description, t.Widgets, by)
		return t.ID, mapUnique(err)
	}
	err := s.db.QueryRow(ctx, `INSERT INTO report_templates (name, description, widgets, updated_by) VALUES ($1, $2, $3, $4) RETURNING id`,
		t.Name, t.Description, t.Widgets, by).Scan(&t.ID)
	return t.ID, mapUnique(err)
}

func mapUnique(err error) error {
	if err != nil && strings.Contains(err.Error(), "report_templates_name_key") {
		return invalidf("a template with this name already exists")
	}
	return err
}

// DeleteTemplate removes a custom template and its schedules.
func (s *Service) DeleteTemplate(ctx context.Context, id int64) error {
	t, err := s.Template(ctx, id)
	if err != nil {
		return err
	}
	if t.Builtin {
		return invalidf("built-in templates cannot be deleted")
	}
	_, err = s.db.Exec(ctx, `DELETE FROM report_templates WHERE id = $1`, id)
	return err
}

// --- schedules ---

func (sc *Schedule) normalize() error {
	switch sc.Frequency {
	case "daily":
	case "weekly":
		if sc.Weekday < 0 || sc.Weekday > 6 {
			return invalidf("weekday must be 0–6")
		}
	case "monthly":
		if sc.Monthday < 1 || sc.Monthday > 29 {
			return invalidf("day of month must be 1–29")
		}
	default:
		return invalidf("frequency must be daily, weekly or monthly")
	}
	if sc.AtMinute < 0 || sc.AtMinute >= 24*60 {
		return invalidf("time of day is out of range")
	}
	if len(sc.Formats) == 0 {
		sc.Formats = []string{FormatPDF}
	}
	for _, f := range sc.Formats {
		if f != FormatPDF && f != FormatXLSX {
			return invalidf("formats must be pdf and/or xlsx")
		}
	}
	slices.Sort(sc.Formats)
	sc.Formats = slices.Compact(sc.Formats)
	var rcpt []string
	for _, r := range sc.Recipients {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if _, err := mail.ParseAddress(r); err != nil {
			return invalidf("recipient %q is not a valid email address", r)
		}
		rcpt = append(rcpt, r)
	}
	sc.Recipients = rcpt
	if len(sc.Recipients) > 0 && sc.ChannelID == nil {
		return invalidf("choose the email channel that sends the report")
	}
	if sc.DeviceIDs == nil {
		sc.DeviceIDs = []int64{}
	}
	if sc.Recipients == nil {
		sc.Recipients = []string{}
	}
	return nil
}

// NextRun returns the first run time strictly after t.
func (sc *Schedule) NextRun(t time.Time, loc *time.Location) time.Time {
	lt := t.In(loc)
	at := func(day time.Time) time.Time {
		return time.Date(day.Year(), day.Month(), day.Day(), 0, sc.AtMinute, 0, 0, loc)
	}
	day := time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, loc)
	for i := 0; i < 400; i++ {
		d := day.AddDate(0, 0, i)
		ok := false
		switch sc.Frequency {
		case "daily":
			ok = true
		case "weekly":
			ok = int(d.Weekday()) == sc.Weekday
		case "monthly":
			_, _, jd := toJalali(d.Year(), int(d.Month()), d.Day())
			ok = jd == sc.Monthday
		}
		if ok && at(d).After(t) {
			return at(d).UTC()
		}
	}
	return t.Add(24 * time.Hour)
}

// Period returns the range a run at t reports on: the previous day, the
// previous seven days, or the previous Jalali month, ending at local
// midnight.
func (sc *Schedule) Period(t time.Time, loc *time.Location) (time.Time, time.Time) {
	lt := t.In(loc)
	end := time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, loc)
	switch sc.Frequency {
	case "weekly":
		return end.AddDate(0, 0, -7), end
	case "monthly":
		start := jalaliMonthStart(lt, -1)
		return start, jalaliMonthStart(lt, 0)
	}
	return end.AddDate(0, 0, -1), end
}

const scheduleCols = `s.id, s.template_id, t.name, s.frequency, s.weekday, s.monthday, s.at_minute, s.device_ids, s.group_id,
	s.formats, s.channel_id, s.recipients, s.enabled, s.next_run_at, s.created_by`

func scanSchedule(row pgx.Row) (*Schedule, error) {
	var sc Schedule
	err := row.Scan(&sc.ID, &sc.TemplateID, &sc.Template, &sc.Frequency, &sc.Weekday, &sc.Monthday, &sc.AtMinute, &sc.DeviceIDs,
		&sc.GroupID, &sc.Formats, &sc.ChannelID, &sc.Recipients, &sc.Enabled, &sc.NextRunAt, &sc.CreatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &sc, err
}

// Schedules lists schedules.
func (s *Service) Schedules(ctx context.Context) ([]*Schedule, error) {
	rows, err := s.db.Query(ctx, `SELECT `+scheduleCols+` FROM report_schedules s JOIN report_templates t ON t.id = s.template_id ORDER BY s.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Schedule{}
	for rows.Next() {
		sc, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

func (s *Service) schedule(ctx context.Context, id int64) (*Schedule, error) {
	return scanSchedule(s.db.QueryRow(ctx, `SELECT `+scheduleCols+` FROM report_schedules s JOIN report_templates t ON t.id = s.template_id WHERE s.id = $1`, id))
}

// SaveSchedule creates (ID 0) or updates a schedule.
func (s *Service) SaveSchedule(ctx context.Context, sc Schedule, by string) (int64, error) {
	if err := sc.normalize(); err != nil {
		return 0, err
	}
	if _, err := s.Template(ctx, sc.TemplateID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return 0, invalidf("choose a report template")
		}
		return 0, err
	}
	next := sc.NextRun(s.now(), s.opt.Location)
	if sc.ID == 0 {
		err := s.db.QueryRow(ctx, `INSERT INTO report_schedules (template_id, frequency, weekday, monthday, at_minute, device_ids, group_id,
			formats, channel_id, recipients, enabled, next_run_at, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING id`,
			sc.TemplateID, sc.Frequency, sc.Weekday, sc.Monthday, sc.AtMinute, sc.DeviceIDs, sc.GroupID, sc.Formats, sc.ChannelID,
			sc.Recipients, sc.Enabled, next, by).Scan(&sc.ID)
		return sc.ID, err
	}
	tag, err := s.db.Exec(ctx, `UPDATE report_schedules SET template_id = $2, frequency = $3, weekday = $4, monthday = $5, at_minute = $6,
		device_ids = $7, group_id = $8, formats = $9, channel_id = $10, recipients = $11, enabled = $12, next_run_at = $13 WHERE id = $1`,
		sc.ID, sc.TemplateID, sc.Frequency, sc.Weekday, sc.Monthday, sc.AtMinute, sc.DeviceIDs, sc.GroupID, sc.Formats, sc.ChannelID,
		sc.Recipients, sc.Enabled, next)
	if err == nil && tag.RowsAffected() == 0 {
		return 0, ErrNotFound
	}
	return sc.ID, err
}

// DeleteSchedule removes a schedule; its past runs stay.
func (s *Service) DeleteSchedule(ctx context.Context, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM report_schedules WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// --- runs ---

const runCols = `id, template_id, schedule_id, title, period_from, period_to, device_ids, formats, status, error, files, created_by, created_at, finished_at`

func scanRun(row pgx.Row) (*Run, error) {
	var r Run
	var files []byte
	err := row.Scan(&r.ID, &r.TemplateID, &r.ScheduleID, &r.Title, &r.From, &r.To, &r.DeviceIDs, &r.Formats, &r.Status, &r.Error,
		&files, &r.CreatedBy, &r.CreatedAt, &r.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.Files = []File{}
	_ = json.Unmarshal(files, &r.Files)
	return &r, nil
}

// Runs lists recent runs, newest first.
func (s *Service) Runs(ctx context.Context, limit int) ([]*Run, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `SELECT `+runCols+` FROM report_runs ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Run{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetRun returns one run.
func (s *Service) GetRun(ctx context.Context, id int64) (*Run, error) {
	return scanRun(s.db.QueryRow(ctx, `SELECT `+runCols+` FROM report_runs WHERE id = $1`, id))
}

// FilePath returns the stored file of a run in a format.
func (s *Service) FilePath(r *Run, format string) (string, File, bool) {
	for _, f := range r.Files {
		if f.Format == format {
			return filepath.Join(s.opt.Dir, fmt.Sprintf("%d.%s", r.ID, format)), f, true
		}
	}
	return "", File{}, false
}

// RunInput asks for a report now.
type RunInput struct {
	TemplateID int64     `json:"template_id"`
	From       time.Time `json:"from"`
	To         time.Time `json:"to"`
	DeviceIDs  []int64   `json:"device_ids"`
	Formats    []string  `json:"formats"`
}

// Start generates a report in the background and returns its run.
func (s *Service) Start(ctx context.Context, in RunInput, by string) (*Run, error) {
	if !s.logs.Enabled() {
		return nil, logs.ErrDisabled
	}
	t, err := s.Template(ctx, in.TemplateID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, invalidf("choose a report template")
		}
		return nil, err
	}
	if !in.To.After(in.From) || in.To.Sub(in.From) > 400*24*time.Hour {
		return nil, invalidf("the period must be positive and at most 400 days")
	}
	sc := Schedule{Frequency: "daily", Formats: in.Formats}
	if err := sc.normalize(); err != nil {
		return nil, err
	}
	if in.DeviceIDs == nil {
		in.DeviceIDs = []int64{}
	}
	run := &Run{TemplateID: &t.ID, Title: t.Name, From: in.From, To: in.To, DeviceIDs: in.DeviceIDs, Formats: sc.Formats}
	if err := s.insertRun(ctx, run, by); err != nil {
		return nil, err
	}
	s.generateAsync(run, t, nil)
	return run, nil
}

func (s *Service) insertRun(ctx context.Context, r *Run, by string) error {
	r.Status, r.CreatedBy, r.Files = StatusRunning, by, []File{}
	return s.db.QueryRow(ctx, `INSERT INTO report_runs (template_id, schedule_id, title, period_from, period_to, device_ids, formats, status, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id, created_at`,
		r.TemplateID, r.ScheduleID, r.Title, r.From, r.To, r.DeviceIDs, r.Formats, r.Status, by).Scan(&r.ID, &r.CreatedAt)
}

func (s *Service) generateAsync(run *Run, t *Template, sc *Schedule) {
	s.running.Store(run.ID, struct{}{})
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.running.Delete(run.ID)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		s.generate(ctx, run, t, sc)
	}()
}

// Wait blocks until background generation finishes (for tests and
// shutdown).
func (s *Service) Wait() { s.wg.Wait() }

func (s *Service) finish(ctx context.Context, r *Run, status, msg string) {
	r.Status, r.Error = status, msg
	now := s.now()
	r.FinishedAt = &now
	files, _ := json.Marshal(r.Files)
	if _, err := s.db.Exec(ctx, `UPDATE report_runs SET status = $2, error = $3, files = $4, finished_at = $5 WHERE id = $1`,
		r.ID, status, msg, files, now); err != nil {
		s.log.Error("saving report run", "run", r.ID, "err", err)
	}
}

// scopeName describes the devices a run covers.
func scopeName(ids []int64, names map[int64]string) string {
	if len(ids) == 0 {
		return "همه‌ی دستگاه‌ها"
	}
	var parts []string
	for _, id := range ids {
		if n := names[id]; n != "" {
			parts = append(parts, n)
		} else {
			parts = append(parts, fmt.Sprintf("#%d", id))
		}
	}
	if len(parts) > 8 {
		return strings.Join(parts[:8], "، ") + fmt.Sprintf(" و %s دستگاه دیگر", faNumber(float64(len(parts)-8)))
	}
	return strings.Join(parts, "، ")
}

func (s *Service) generate(ctx context.Context, r *Run, t *Template, sc *Schedule) {
	data, err := s.logs.Query(ctx, t.Widgets, logs.Range{From: r.From, To: r.To, DeviceIDs: r.DeviceIDs})
	if err != nil {
		s.finish(ctx, r, StatusFailed, "log query: "+err.Error())
		return
	}
	names, err := s.logs.DeviceNames(ctx)
	if err != nil {
		s.finish(ctx, r, StatusFailed, err.Error())
		return
	}
	loc := s.opt.Location
	doc := Document{Title: t.Name, Description: t.Description, From: r.From.In(loc), To: r.To.In(loc),
		Scope: scopeName(r.DeviceIDs, names), GeneratedAt: s.now().In(loc), Widgets: data, DeviceNames: names}
	if err := os.MkdirAll(s.opt.Dir, 0o750); err != nil {
		s.finish(ctx, r, StatusFailed, err.Error())
		return
	}
	var attachments []alerts.Attachment
	base := fmt.Sprintf("taktaplus-report-%d-%s", r.ID, jalaliStamp(doc.To.Add(-time.Second)))
	r.Files = []File{}
	for _, f := range r.Formats {
		var out []byte
		var ctype string
		switch f {
		case FormatPDF:
			page, err := HTML(doc, loc)
			if err == nil {
				out, err = s.pdf(ctx, page)
			}
			if err != nil {
				s.finish(ctx, r, StatusFailed, "PDF: "+err.Error())
				return
			}
			ctype = "application/pdf"
		case FormatXLSX:
			out, err = XLSX(doc)
			if err != nil {
				s.finish(ctx, r, StatusFailed, "Excel: "+err.Error())
				return
			}
			ctype = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
		}
		if err := os.WriteFile(filepath.Join(s.opt.Dir, fmt.Sprintf("%d.%s", r.ID, f)), out, 0o640); err != nil {
			s.finish(ctx, r, StatusFailed, err.Error())
			return
		}
		name := base + "." + f
		r.Files = append(r.Files, File{Format: f, Name: name, Size: int64(len(out))})
		attachments = append(attachments, alerts.Attachment{Name: name, ContentType: ctype, Data: out})
	}
	if sc == nil || sc.ChannelID == nil {
		s.finish(ctx, r, StatusReady, "")
		return
	}
	subject := fmt.Sprintf("[TaktaPlus] %s — %s", t.Name, FaDate(doc.To.Add(-time.Second)))
	body := fmt.Sprintf("گزارش «%s»\nبازه: %s تا %s\nدامنه: %s\n\nفایل گزارش پیوست شده است.\n— TaktaPlus\n",
		t.Name, FaDateTime(doc.From), FaDateTime(doc.To), doc.Scope)
	if err := s.alerts.SendMail(ctx, *sc.ChannelID, sc.Recipients, subject, body, attachments); err != nil {
		s.finish(ctx, r, StatusFailed, "email: "+err.Error())
		return
	}
	s.finish(ctx, r, StatusSent, "")
}

// Retry generates a failed run again (and sends it, for scheduled runs).
func (s *Service) Retry(ctx context.Context, id int64) error {
	r, err := s.GetRun(ctx, id)
	if err != nil {
		return err
	}
	if r.Status != StatusFailed {
		return invalidf("only failed reports can be retried")
	}
	if r.TemplateID == nil {
		return invalidf("the template of this report was deleted")
	}
	t, err := s.Template(ctx, *r.TemplateID)
	if err != nil {
		return err
	}
	var sc *Schedule
	if r.ScheduleID != nil {
		if sc, err = s.schedule(ctx, *r.ScheduleID); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	tag, err := s.db.Exec(ctx, `UPDATE report_runs SET status = 'running', error = '', finished_at = NULL WHERE id = $1 AND status = 'failed'`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return invalidf("the report is already running")
	}
	r.Status, r.Error = StatusRunning, ""
	s.generateAsync(r, t, sc)
	return nil
}

// DeleteRun removes a run and its files.
func (s *Service) DeleteRun(ctx context.Context, id int64) error {
	if _, busy := s.running.Load(id); busy {
		return invalidf("the report is still being generated")
	}
	r, err := s.GetRun(ctx, id)
	if err != nil {
		return err
	}
	for _, f := range r.Files {
		p, _, _ := s.FilePath(r, f.Format)
		_ = os.Remove(p)
	}
	_, err = s.db.Exec(ctx, `DELETE FROM report_runs WHERE id = $1`, id)
	return err
}

// --- scheduler ---

// RunDue starts every schedule whose time has come.
func (s *Service) RunDue(ctx context.Context) error {
	now := s.now()
	rows, err := s.db.Query(ctx, `SELECT `+scheduleCols+` FROM report_schedules s JOIN report_templates t ON t.id = s.template_id
		WHERE s.enabled AND s.next_run_at <= $1 ORDER BY s.next_run_at`, now)
	if err != nil {
		return err
	}
	var due []*Schedule
	for rows.Next() {
		sc, err := scanSchedule(rows)
		if err != nil {
			rows.Close()
			return err
		}
		due = append(due, sc)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, sc := range due {
		// Claim the slot first so a crash never sends a report twice.
		next := sc.NextRun(now, s.opt.Location)
		tag, err := s.db.Exec(ctx, `UPDATE report_schedules SET next_run_at = $2 WHERE id = $1 AND next_run_at = $3`, sc.ID, next, sc.NextRunAt)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		t, err := s.Template(ctx, sc.TemplateID)
		if err != nil {
			return err
		}
		devs := sc.DeviceIDs
		if sc.GroupID != nil {
			list, err := s.inv.List(ctx, inventory.Filter{GroupID: sc.GroupID})
			if err != nil {
				return err
			}
			devs = []int64{}
			for _, d := range list {
				devs = append(devs, d.ID)
			}
		}
		from, to := sc.Period(sc.NextRunAt, s.opt.Location)
		run := &Run{TemplateID: &t.ID, ScheduleID: &sc.ID, Title: t.Name, From: from, To: to, DeviceIDs: devs, Formats: sc.Formats}
		if err := s.insertRun(ctx, run, "schedule"); err != nil {
			return err
		}
		if !s.logs.Enabled() {
			s.finish(ctx, run, StatusFailed, logs.ErrDisabled.Error())
			continue
		}
		s.generateAsync(run, t, sc)
	}
	return nil
}

// Cleanup removes runs older than KeepDays, with their files, and marks
// runs interrupted by a restart as failed.
func (s *Service) Cleanup(ctx context.Context) error {
	rows, err := s.db.Query(ctx, `SELECT `+runCols+` FROM report_runs WHERE created_at < $1`, s.now().AddDate(0, 0, -s.opt.KeepDays))
	if err != nil {
		return err
	}
	var old []*Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			rows.Close()
			return err
		}
		old = append(old, r)
	}
	rows.Close()
	for _, r := range old {
		if err := s.DeleteRun(ctx, r.ID); err != nil {
			return err
		}
	}
	return nil
}

// RecoverRuns marks runs left "running" by a previous process as failed.
func (s *Service) RecoverRuns(ctx context.Context) error {
	_, err := s.db.Exec(ctx, `UPDATE report_runs SET status = 'failed', error = 'interrupted by a restart', finished_at = now() WHERE status = 'running'`)
	return err
}

// Scheduler runs due schedules every minute and cleans up daily.
func (s *Service) Scheduler(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	lastClean := time.Time{}
	for {
		if err := s.RunDue(ctx); err != nil && ctx.Err() == nil {
			s.log.Error("report schedules", "err", err)
		}
		if s.now().Sub(lastClean) > 24*time.Hour {
			if err := s.Cleanup(ctx); err != nil && ctx.Err() == nil {
				s.log.Error("report cleanup", "err", err)
			}
			lastClean = s.now()
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
