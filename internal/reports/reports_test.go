package reports

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/farshadfighter/taktaplus-lic/pkg/licformat"

	"github.com/farshadfighter/taktaplus/internal/alerts"
	"github.com/farshadfighter/taktaplus/internal/chstore/chtest"
	"github.com/farshadfighter/taktaplus/internal/device"
	"github.com/farshadfighter/taktaplus/internal/device/devicetest"
	"github.com/farshadfighter/taktaplus/internal/inventory"
	"github.com/farshadfighter/taktaplus/internal/license/licensetest"
	"github.com/farshadfighter/taktaplus/internal/logs"
	"github.com/farshadfighter/taktaplus/internal/secretbox"
	"github.com/farshadfighter/taktaplus/internal/settings"
	"github.com/farshadfighter/taktaplus/internal/testdb"
)

var tehran, _ = time.LoadLocation("Asia/Tehran")

func TestJalali(t *testing.T) {
	cases := []struct{ gy, gm, gd, jy, jm, jd int }{
		{2026, 3, 21, 1405, 1, 1}, {2026, 10, 28, 1405, 8, 6}, {2025, 3, 20, 1403, 12, 30}, {2024, 3, 20, 1403, 1, 1}, {2027, 3, 20, 1405, 12, 29},
	}
	for _, c := range cases {
		if jy, jm, jd := toJalali(c.gy, c.gm, c.gd); jy != c.jy || jm != c.jm || jd != c.jd {
			t.Fatalf("toJalali(%d-%d-%d) = %d/%d/%d", c.gy, c.gm, c.gd, jy, jm, jd)
		}
		if gy, gm, gd := toGregorian(c.jy, c.jm, c.jd); gy != c.gy || gm != c.gm || gd != c.gd {
			t.Fatalf("toGregorian(%d/%d/%d) = %d-%d-%d", c.jy, c.jm, c.jd, gy, gm, gd)
		}
	}
	// Round trip over several years.
	for d := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC); d.Year() < 2035; d = d.AddDate(0, 0, 1) {
		jy, jm, jd := toJalali(d.Year(), int(d.Month()), d.Day())
		if gy, gm, gd := toGregorian(jy, jm, jd); gy != d.Year() || gm != int(d.Month()) || gd != d.Day() {
			t.Fatalf("round trip %s", d)
		}
	}
	if got := FaDate(time.Date(2026, 10, 28, 9, 0, 0, 0, tehran)); got != "۱۴۰۵/۰۸/۰۶" {
		t.Fatalf("FaDate = %s", got)
	}
}

func TestScheduleTimes(t *testing.T) {
	now := time.Date(2026, 10, 28, 10, 0, 0, 0, tehran) // Wednesday, 6 Aban 1405
	daily := Schedule{Frequency: "daily", AtMinute: 8 * 60}
	if got := daily.NextRun(now, tehran).In(tehran); !got.Equal(time.Date(2026, 10, 29, 8, 0, 0, 0, tehran)) {
		t.Fatalf("daily next = %s", got)
	}
	from, to := daily.Period(time.Date(2026, 10, 29, 8, 0, 0, 0, tehran), tehran)
	if !from.Equal(time.Date(2026, 10, 28, 0, 0, 0, 0, tehran)) || !to.Equal(time.Date(2026, 10, 29, 0, 0, 0, 0, tehran)) {
		t.Fatalf("daily period %s – %s", from, to)
	}
	weekly := Schedule{Frequency: "weekly", Weekday: 6, AtMinute: 7 * 60}
	if got := weekly.NextRun(now, tehran).In(tehran); !got.Equal(time.Date(2026, 10, 31, 7, 0, 0, 0, tehran)) {
		t.Fatalf("weekly next = %s", got)
	}
	monthly := Schedule{Frequency: "monthly", Monthday: 1, AtMinute: 0}
	next := monthly.NextRun(now, tehran).In(tehran) // 1 Azar 1405 = 22 Nov 2026
	if !next.Equal(time.Date(2026, 11, 22, 0, 0, 0, 0, tehran)) {
		t.Fatalf("monthly next = %s", next)
	}
	from, to = monthly.Period(next, tehran) // all of Aban
	if !from.Equal(time.Date(2026, 10, 23, 0, 0, 0, 0, tehran)) || !to.Equal(next) {
		t.Fatalf("monthly period %s – %s", from, to)
	}
	// The schedule time itself is not "after" it.
	if got := daily.NextRun(time.Date(2026, 10, 29, 8, 0, 0, 0, tehran), tehran).In(tehran); got.Day() != 30 {
		t.Fatalf("next after a run = %s", got)
	}
}

func sampleDoc() Document {
	return Document{
		Title: "خلاصه‌ی امنیتی", From: time.Date(2026, 10, 27, 0, 0, 0, 0, tehran), To: time.Date(2026, 10, 28, 0, 0, 0, 0, tehran),
		Scope: "همه‌ی دستگاه‌ها", GeneratedAt: time.Date(2026, 10, 28, 7, 0, 0, 0, tehran),
		Widgets: []logs.Data{
			{Widget: logs.Widget{ID: "k", Title: "نمای کلی", Kind: logs.KindKPIs, Columns: []logs.Column{{Key: "traffic", Title: "ترافیک", Type: "bytes"}, {Key: "n", Title: "تعداد", Type: "number"}}},
				Rows: []map[string]any{{"traffic": float64(5 << 40), "n": float64(1234567)}}},
			{Widget: logs.Widget{ID: "s", Title: "روند", Kind: logs.KindSeries, Columns: []logs.Column{{Key: "bytes", Title: "ترافیک", Type: "bytes"}}},
				Rows: []map[string]any{{"t": "2026-10-26T20:30:00Z", "bytes": float64(10)}, {"t": "2026-10-26T21:30:00Z", "bytes": float64(30)}}},
			{Widget: logs.Widget{ID: "b", Title: "اپلیکیشن‌ها", Kind: logs.KindBars, Columns: []logs.Column{{Key: "name", Title: "نام", Type: "text"}, {Key: "value", Title: "ترافیک", Type: "bytes"}}},
				Rows: []map[string]any{{"name": "HTTPS", "value": float64(2048)}, {"name": "<script>", "value": float64(1024)}}},
			{Widget: logs.Widget{ID: "t", Title: "تهدیدها", Kind: logs.KindTable, Columns: []logs.Column{{Key: "threat", Title: "تهدید", Type: "text"}, {Key: "severity", Title: "شدت", Type: "severity"}}},
				Rows: []map[string]any{{"threat": "Log4j", "severity": "critical"}}},
			{Widget: logs.Widget{ID: "e", Title: "خالی", Kind: logs.KindTable, Columns: []logs.Column{{Key: "x", Title: "x", Type: "text"}}}, Rows: []map[string]any{}},
		},
	}
}

func TestRenderers(t *testing.T) {
	doc := sampleDoc()
	page, err := HTML(doc, tehran)
	if err != nil {
		t.Fatal(err)
	}
	s := string(page)
	for _, want := range []string{"۵٫۰ TB", "۱٬۲۳۴٬۵۶۷", "بحرانی", "&lt;script&gt;", "داده‌ای در این بازه نیست", "۱۴۰۵/۰۸/۰۵ ۰۰:۰۰ تا ۱۴۰۵/۰۸/۰۶ ۰۰:۰۰", "<svg", "font-family:Vazirmatn"} {
		if !strings.Contains(s, want) {
			t.Fatalf("HTML lacks %q", want)
		}
	}
	if strings.Contains(s, "<script>") {
		t.Fatal("device text not escaped")
	}
	data, err := XLSX(doc)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		names[f.Name] = string(b)
	}
	if len(names) != 5+1+len(doc.Widgets) {
		t.Fatalf("xlsx parts: %d", len(names))
	}
	if !strings.Contains(names["xl/worksheets/sheet2.xml"], `<v>5497558138880</v>`) || !strings.Contains(names["xl/worksheets/sheet4.xml"], "&lt;script&gt;") ||
		!strings.Contains(names["xl/worksheets/sheet1.xml"], `rightToLeft="1"`) {
		t.Fatalf("sheets: %v", names["xl/worksheets/sheet2.xml"])
	}
	if bin := os.Getenv("TAKTAPLUS_TEST_CHROMIUM"); bin != "" {
		pdf, err := PDF(context.Background(), bin, t.TempDir(), page)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(pdf, []byte("%PDF-")) || len(pdf) < 10_000 {
			t.Fatalf("pdf: %d bytes", len(pdf))
		}
		if out := os.Getenv("TAKTAPLUS_TEST_PDF_OUT"); out != "" {
			_ = os.WriteFile(out, pdf, 0o644)
		}
	}
	if _, err := PDF(context.Background(), "no-such-browser", t.TempDir(), page); err != ErrNoBrowser {
		t.Fatalf("missing browser: %v", err)
	}
}

func TestServiceRunsAndSchedules(t *testing.T) {
	ch := chtest.New(t)
	db := testdb.New(t)
	ctx := context.Background()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secretbox.New(key)
	future := licformat.NewDate(time.Now().Year()+1, 1, 1)
	guard := licensetest.Guard(t, map[string]licformat.Date{"FGREP001": future})
	inv := inventory.New(db, box, guard)
	inv.SetDriverFactory(devicetest.Fleet{"127.0.0.1": {Info: device.Info{Serial: "FGREP001"}}}.Factory())
	devID, _ := inv.Create(ctx, inventory.Input{Name: "FGT-HQ", Kind: device.KindFortiGate, Host: "127.0.0.1", Credentials: &inventory.Credentials{APIToken: "t"}})
	if _, err := inv.Probe(ctx, devID); err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	lg := logs.New(ch, settings.New(db, box), inv, logs.NewGeoIP(t.TempDir()), quiet)
	col := lg.Collector()
	if err := col.RefreshDevices(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	col.Handle([]byte(fmt.Sprintf(`devid=FGREP001 type=utm subtype=ips severity=high attack="Bad.Thing" action=dropped srcip=10.1.1.1 eventtime=%d`, now.Add(-2*time.Hour).Unix())), netip.MustParseAddr("127.0.0.1"))
	col.Handle([]byte(fmt.Sprintf(`devid=FGREP001 type=traffic srcip=10.1.1.1 dstip=8.8.8.8 app=DNS sentbyte=100 rcvdbyte=200 eventtime=%d`, now.Add(-2*time.Hour).Unix())), netip.MustParseAddr("127.0.0.1"))
	if err := col.Flush(ctx); err != nil {
		t.Fatal(err)
	}

	al := alerts.New(db, box, inv, guard, quiet)
	type sent struct {
		to      []string
		subject string
		files   []alerts.Attachment
	}
	var mails []sent
	failMail := false
	al.SetMailer(func(_ context.Context, _ alerts.ChannelConfig, to []string, subject, _ string, files []alerts.Attachment, _ time.Time) error {
		if failMail {
			return fmt.Errorf("smtp down")
		}
		mails = append(mails, sent{to, subject, files})
		return nil
	})
	chID, err := al.SaveChannel(ctx, alerts.Channel{Name: "mail", Kind: alerts.ChannelEmail, Enabled: true,
		Config: alerts.ChannelConfig{SMTPHost: "mail.example.com", From: "tp@example.com", To: []string{"it@example.com"}}})
	if err != nil {
		t.Fatal(err)
	}
	svc := New(db, lg, al, inv, quiet, Options{Dir: t.TempDir(), Location: tehran})
	var pages [][]byte
	svc.pdf = func(_ context.Context, page []byte) ([]byte, error) {
		pages = append(pages, page)
		return []byte("%PDF-1.7 fake"), nil
	}

	tpls, err := svc.Templates(ctx)
	if err != nil || len(tpls) != 6 || !tpls[0].Builtin {
		t.Fatalf("templates = %v, %v", tpls, err)
	}
	if _, err := svc.SaveTemplate(ctx, Template{ID: tpls[0].ID, Name: "x", Widgets: []string{"kpi_overview"}}, "admin"); err == nil {
		t.Fatal("built-in template changed")
	}
	if _, err := svc.SaveTemplate(ctx, Template{Name: "bad", Widgets: []string{"nope"}}, "admin"); err == nil {
		t.Fatal("unknown widget accepted")
	}
	custom, err := svc.SaveTemplate(ctx, Template{Name: "تهدیدهای من", Widgets: []string{"top_threats", "top_apps"}}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveTemplate(ctx, Template{Name: "تهدیدهای من", Widgets: []string{"top_apps"}}, "admin"); err == nil {
		t.Fatal("duplicate name accepted")
	}

	run, err := svc.Start(ctx, RunInput{TemplateID: custom, From: now.Add(-24 * time.Hour), To: now, Formats: []string{"pdf", "xlsx"}}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	svc.Wait()
	run, _ = svc.GetRun(ctx, run.ID)
	if run.Status != StatusReady || len(run.Files) != 2 {
		t.Fatalf("run = %+v", run)
	}
	if len(pages) != 1 || !bytes.Contains(pages[0], []byte("Bad.Thing")) {
		t.Fatal("report lacks the threat")
	}
	p, f, ok := svc.FilePath(run, "xlsx")
	if st, err := os.Stat(p); !ok || err != nil || st.Size() != f.Size || !strings.HasSuffix(f.Name, ".xlsx") {
		t.Fatalf("xlsx file %s %+v", p, f)
	}

	// A schedule that is due now runs, emails and moves on.
	sid, err := svc.SaveSchedule(ctx, Schedule{TemplateID: tpls[0].ID, Frequency: "daily", AtMinute: 7 * 60, Formats: []string{"pdf"},
		ChannelID: &chID, Recipients: []string{"ciso@example.com"}, Enabled: true}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveSchedule(ctx, Schedule{TemplateID: tpls[0].ID, Frequency: "daily", Recipients: []string{"ciso@example.com"}}, "admin"); err == nil {
		t.Fatal("recipients without a channel accepted")
	}
	if _, err := db.Exec(ctx, `UPDATE report_schedules SET next_run_at = now() - interval '1 minute' WHERE id = $1`, sid); err != nil {
		t.Fatal(err)
	}
	if err := svc.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	svc.Wait()
	if len(mails) == 0 {
		runs, _ := svc.Runs(ctx, 1)
		t.Fatalf("no mail; run = %+v", runs[0])
	}
	if len(mails) != 1 || mails[0].to[0] != "ciso@example.com" || len(mails[0].files) != 1 || !strings.Contains(mails[0].subject, "خلاصه‌ی امنیتی") {
		t.Fatalf("mails = %+v", mails)
	}
	runs, _ := svc.Runs(ctx, 10)
	if runs[0].Status != StatusSent || runs[0].ScheduleID == nil || *runs[0].ScheduleID != sid {
		t.Fatalf("scheduled run = %+v", runs[0])
	}
	scheds, _ := svc.Schedules(ctx)
	if !scheds[0].NextRunAt.After(time.Now()) {
		t.Fatal("schedule not advanced")
	}
	// Nothing more is due.
	if err := svc.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	svc.Wait()
	if len(mails) != 1 {
		t.Fatal("ran twice")
	}

	// Failed email: the run fails and can be retried.
	failMail = true
	_, _ = db.Exec(ctx, `UPDATE report_schedules SET next_run_at = now() - interval '1 minute' WHERE id = $1`, sid)
	_ = svc.RunDue(ctx)
	svc.Wait()
	runs, _ = svc.Runs(ctx, 10)
	if runs[0].Status != StatusFailed || !strings.Contains(runs[0].Error, "smtp down") {
		t.Fatalf("failed run = %+v", runs[0])
	}
	failMail = false
	if err := svc.Retry(ctx, runs[0].ID); err != nil {
		t.Fatal(err)
	}
	svc.Wait()
	r, _ := svc.GetRun(ctx, runs[0].ID)
	if r.Status != StatusSent || len(mails) != 2 {
		t.Fatalf("retried run = %+v", r)
	}
	if err := svc.Retry(ctx, r.ID); err == nil {
		t.Fatal("retried a sent run")
	}
	if err := svc.DeleteRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("file left behind")
	}
	if err := svc.DeleteTemplate(ctx, tpls[0].ID); err == nil {
		t.Fatal("built-in template deleted")
	}
}
