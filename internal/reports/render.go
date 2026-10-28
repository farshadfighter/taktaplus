package reports

import (
	"bytes"
	"context"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"html/template"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/farshadfighter/taktaplus/internal/logs"
)

//go:embed assets
var assets embed.FS

// Document is everything a report shows.
type Document struct {
	Title       string
	Description string
	From, To    time.Time // local time
	Scope       string    // devices covered
	GeneratedAt time.Time
	Widgets     []logs.Data
	DeviceNames map[int64]string
}

var severityFa = map[string]string{"critical": "بحرانی", "high": "زیاد", "medium": "متوسط", "low": "کم", "info": "اطلاعات",
	"information": "اطلاعات", "notice": "اطلاع", "warning": "هشدار", "error": "خطا", "alert": "هشدار فوری", "emergency": "اضطراری"}

// formatValue renders a widget value for people.
func formatValue(typ string, v any) string {
	switch typ {
	case "bytes":
		return faDigits.Replace(humanBytes(toFloat(v)))
	case "number":
		return faNumber(toFloat(v))
	case "duration":
		return faDuration(toFloat(v))
	case "severity":
		s := fmt.Sprint(v)
		if fa := severityFa[strings.ToLower(s)]; fa != "" {
			return fa
		}
		return s
	}
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// plainValue is the spreadsheet form: numbers stay numbers.
func plainValue(typ string, v any) any {
	switch typ {
	case "bytes", "number", "duration":
		return toFloat(v)
	}
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case uint64:
		return float64(x)
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	}
	return 0
}

func humanBytes(n float64) string {
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	i := 0
	for n >= 1024 && i < len(units)-1 {
		n /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0f %s", n, units[i])
	}
	return strings.Replace(fmt.Sprintf("%.1f %s", n, units[i]), ".", "٫", 1)
}

func faNumber(n float64) string {
	s := strconv.FormatInt(int64(math.Round(n)), 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteRune('٬')
		}
		b.WriteRune(c)
	}
	out := faDigits.Replace(b.String())
	if neg {
		out = "-" + out
	}
	return out
}

func faDuration(sec float64) string {
	d := time.Duration(sec) * time.Second
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	switch {
	case h > 0:
		return faDigits.Replace(fmt.Sprintf("%d ساعت %d دقیقه", h, m))
	case m > 0:
		return faDigits.Replace(fmt.Sprintf("%d دقیقه", m))
	}
	return faDigits.Replace(fmt.Sprintf("%d ثانیه", int(sec)))
}

type kpi struct{ Label, Value string }
type bar struct {
	Name, Value string
	Pct         float64
}
type tableView struct {
	Headers []string
	Rows    [][]string
}
type widgetView struct {
	Title string
	Kind  string
	Empty bool
	KPIs  []kpi
	Bars  []bar
	Table tableView
	Chart template.HTML
}

func cellText(w logs.Data, c logs.Column, row map[string]any, names map[int64]string) string {
	v := row[c.Key]
	if c.Type == "device" {
		if n := names[int64(toFloat(v))]; n != "" {
			return n
		}
	}
	return formatValue(c.Type, v)
}

func viewOf(w logs.Data, names map[int64]string, loc *time.Location) widgetView {
	v := widgetView{Title: w.Title, Kind: w.Kind}
	switch w.Kind {
	case logs.KindKPIs:
		if len(w.Rows) == 0 {
			v.Empty = true
			return v
		}
		for _, c := range w.Columns {
			v.KPIs = append(v.KPIs, kpi{Label: c.Title, Value: cellText(w, c, w.Rows[0], names)})
		}
	case logs.KindBars:
		maxV := 0.0
		for _, r := range w.Rows {
			maxV = math.Max(maxV, toFloat(r["value"]))
		}
		for _, r := range w.Rows {
			pct := 0.0
			if maxV > 0 {
				pct = toFloat(r["value"]) / maxV * 100
			}
			v.Bars = append(v.Bars, bar{Name: cellText(w, w.Columns[0], r, names), Value: cellText(w, w.Columns[1], r, names), Pct: pct})
		}
		v.Empty = len(v.Bars) == 0
	case logs.KindSeries:
		v.Chart, v.Empty = seriesSVG(w, loc)
	default:
		for _, c := range w.Columns {
			v.Table.Headers = append(v.Table.Headers, c.Title)
		}
		for _, r := range w.Rows {
			var cells []string
			for _, c := range w.Columns {
				cells = append(cells, cellText(w, c, r, names))
			}
			v.Table.Rows = append(v.Table.Rows, cells)
		}
		v.Empty = len(v.Table.Rows) == 0
	}
	return v
}

var seriesColors = []string{"#900A3C", "#6B7280", "#D97706"}

// seriesSVG draws the series as lines on one chart, each scaled to its own
// peak so a byte count and an event count can share it. Time runs right to
// left. The legend and time labels are HTML so they lay out right to left.
func seriesSVG(w logs.Data, loc *time.Location) (template.HTML, bool) {
	const W, H, pad = 680.0, 170.0, 8.0
	n := len(w.Rows)
	if n == 0 {
		return "", true
	}
	total := 0.0
	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %.0f %.0f" width="100%%" xmlns="http://www.w3.org/2000/svg" role="img">`, W, H)
	for i := 0; i <= 4; i++ {
		y := pad + (H-2*pad)*float64(i)/4
		fmt.Fprintf(&b, `<line x1="0" x2="%.0f" y1="%.1f" y2="%.1f" stroke="#E5E7EB" stroke-width="1"/>`, W, y, y)
	}
	x := func(i int) float64 {
		if n == 1 {
			return W / 2
		}
		return W - pad - (W-2*pad)*float64(i)/float64(n-1)
	}
	var legend strings.Builder
	for si, c := range w.Columns {
		maxV := 0.0
		for _, r := range w.Rows {
			maxV = math.Max(maxV, toFloat(r[c.Key]))
			total += toFloat(r[c.Key])
		}
		scale := maxV
		if scale == 0 {
			scale = 1
		}
		var pts []string
		for i, r := range w.Rows {
			pts = append(pts, fmt.Sprintf("%.1f,%.1f", x(i), pad+(H-2*pad)*(1-toFloat(r[c.Key])/scale)))
		}
		color := seriesColors[si%len(seriesColors)]
		fmt.Fprintf(&b, `<polyline fill="none" stroke="%s" stroke-width="2" points="%s"/>`, color, strings.Join(pts, " "))
		fmt.Fprintf(&legend, `<span class="lg"><i style="background: %s"></i>%s — بیشینه: %s</span>`,
			color, html.EscapeString(c.Title), html.EscapeString(formatValue(c.Type, maxV)))
	}
	b.WriteString(`</svg>`)
	first, _ := time.Parse(time.RFC3339, fmt.Sprint(w.Rows[0]["t"]))
	last, _ := time.Parse(time.RFC3339, fmt.Sprint(w.Rows[n-1]["t"]))
	label := func(i int) string {
		t, err := time.Parse(time.RFC3339, fmt.Sprint(w.Rows[i]["t"]))
		if err != nil {
			return ""
		}
		t = t.In(loc)
		if last.Sub(first) <= 48*time.Hour {
			return faDigits.Replace(t.Format("15:04"))
		}
		return FaDate(t)
	}
	fmt.Fprintf(&b, `<div class="axis"><span>%s</span><span>%s</span></div><div class="legend">%s</div>`,
		html.EscapeString(label(0)), html.EscapeString(label(n-1)), legend.String())
	return template.HTML(b.String()), total == 0
}

var page = template.Must(template.New("report").Parse(`<!doctype html>
<html lang="fa" dir="rtl"><head><meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data:; font-src data:; style-src 'unsafe-inline'">
<title>{{.Title}}</title>
<style>
{{.FontCSS}}
@page { size: A4; margin: 14mm 12mm 16mm; }
* { box-sizing: border-box; }
body { font-family: Vazirmatn, Tahoma, sans-serif; color: #1F2937; font-size: 12px; margin: 0; }
.head { display: flex; align-items: center; gap: 14px; border-bottom: 3px solid #900A3C; padding-bottom: 10px; margin-bottom: 16px; }
.head img { height: 46px; }
.head h1 { margin: 0; font-size: 20px; color: #900A3C; }
.meta { color: #4B5563; margin-top: 4px; line-height: 1.8; }
.desc { color: #6B7280; margin: 0 0 12px; }
section { break-inside: avoid; margin-bottom: 18px; }
h2 { font-size: 14px; margin: 0 0 8px; color: #111827; border-right: 4px solid #900A3C; padding-right: 8px; }
.kpis { display: flex; gap: 10px; }
.kpi { flex: 1; border: 1px solid #E5E7EB; border-radius: 8px; padding: 10px 12px; }
.kpi .l { color: #6B7280; font-size: 11px; }
.kpi .v { font-size: 18px; font-weight: 700; margin-top: 4px; }
table { width: 100%; border-collapse: collapse; }
th { background: #F3F4F6; text-align: right; font-weight: 700; padding: 6px 8px; font-size: 11px; color: #374151; }
td { padding: 5px 8px; border-bottom: 1px solid #F3F4F6; unicode-bidi: plaintext; text-align: right; }
.bars .row { display: flex; align-items: center; gap: 8px; margin: 4px 0; }
.bars .n { width: 30%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; unicode-bidi: plaintext; text-align: right; }
.bars .t { flex: 1; height: 9px; background: #F3F4F6; border-radius: 4px; }
.bars .f { height: 9px; background: #900A3C; border-radius: 4px; margin-right: 0; }
.bars .v { width: 18%; text-align: left; color: #374151; }
.empty { color: #9CA3AF; padding: 8px 0; }
.axis { display: flex; justify-content: space-between; color: #6B7280; font-size: 10px; margin-top: 2px; }
.legend { margin-top: 6px; color: #374151; font-size: 11px; }
.lg { margin-left: 18px; }
.lg i { display: inline-block; width: 10px; height: 10px; margin-left: 6px; vertical-align: middle; }
.foot { color: #9CA3AF; font-size: 10px; margin-top: 24px; border-top: 1px solid #E5E7EB; padding-top: 6px; }
</style></head><body>
<div class="head"><img src="{{.Logo}}" alt=""><div><h1>{{.Title}}</h1>
<div class="meta">بازه: {{.Period}}<br>دامنه: {{.Scope}}</div></div></div>
{{if .Description}}<p class="desc">{{.Description}}</p>{{end}}
{{range .Widgets}}<section><h2>{{.Title}}</h2>
{{if .Empty}}<div class="empty">داده‌ای در این بازه نیست.</div>
{{else if eq .Kind "kpis"}}<div class="kpis">{{range .KPIs}}<div class="kpi"><div class="l">{{.Label}}</div><div class="v">{{.Value}}</div></div>{{end}}</div>
{{else if eq .Kind "bars"}}<div class="bars">{{range .Bars}}<div class="row"><div class="n">{{.Name}}</div><div class="t"><div class="f" style="width: {{printf "%.1f" .Pct}}%"></div></div><div class="v">{{.Value}}</div></div>{{end}}</div>
{{else if eq .Kind "series"}}{{.Chart}}
{{else}}<table><thead><tr>{{range .Table.Headers}}<th>{{.}}</th>{{end}}</tr></thead><tbody>{{range .Table.Rows}}<tr>{{range .}}<td>{{.}}</td>{{end}}</tr>{{end}}</tbody></table>{{end}}
</section>{{end}}
<div class="foot">تولید شده در {{.Generated}} توسط TaktaPlus</div>
</body></html>`))

func fontCSS() template.CSS {
	var b strings.Builder
	for _, f := range []struct {
		file   string
		weight int
		rng    string
	}{
		{"vazirmatn-arabic-400-normal.woff2", 400, "U+0600-06FF,U+200C-200E,U+2010-2011,U+204F,U+2E41,U+FB50-FDFF,U+FE80-FEFC"},
		{"vazirmatn-arabic-700-normal.woff2", 700, "U+0600-06FF,U+200C-200E,U+2010-2011,U+204F,U+2E41,U+FB50-FDFF,U+FE80-FEFC"},
		{"vazirmatn-latin-400-normal.woff2", 400, "U+0000-00FF,U+0131,U+0152-0153,U+02BB-02BC,U+02C6,U+02DA,U+02DC,U+2000-206F,U+2212"},
		{"vazirmatn-latin-700-normal.woff2", 700, "U+0000-00FF,U+0131,U+0152-0153,U+02BB-02BC,U+02C6,U+02DA,U+02DC,U+2000-206F,U+2212"},
	} {
		data, _ := assets.ReadFile("assets/" + f.file)
		fmt.Fprintf(&b, "@font-face{font-family:Vazirmatn;font-weight:%d;src:url(data:font/woff2;base64,%s) format('woff2');unicode-range:%s}\n",
			f.weight, base64.StdEncoding.EncodeToString(data), f.rng)
	}
	return template.CSS(b.String())
}

// HTML renders the printable report.
func HTML(doc Document, loc *time.Location) ([]byte, error) {
	logo, _ := assets.ReadFile("assets/logo.png")
	views := make([]widgetView, 0, len(doc.Widgets))
	for _, w := range doc.Widgets {
		views = append(views, viewOf(w, doc.DeviceNames, loc))
	}
	var buf bytes.Buffer
	err := page.Execute(&buf, map[string]any{
		"Title": doc.Title, "Description": doc.Description, "Scope": doc.Scope, "Widgets": views,
		"Period":    FaDateTime(doc.From) + " تا " + FaDateTime(doc.To),
		"Generated": FaDateTime(doc.GeneratedAt), "FontCSS": fontCSS(),
		"Logo": template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(logo)),
	})
	return buf.Bytes(), err
}

// XLSX writes one sheet per widget after a summary sheet.
func XLSX(doc Document) ([]byte, error) {
	sheets := []sheet{{Name: "گزارش", Rows: [][]any{
		{"گزارش", doc.Title}, {"بازه", FaDateTime(doc.From) + " تا " + FaDateTime(doc.To)},
		{"دامنه", doc.Scope}, {"تولید", FaDateTime(doc.GeneratedAt)},
	}}}
	for _, w := range doc.Widgets {
		sh := sheet{Name: w.Title}
		if w.Kind == logs.KindSeries {
			sh.Header = append(sh.Header, "زمان")
		}
		for _, c := range w.Columns {
			sh.Header = append(sh.Header, c.Title)
		}
		for _, r := range w.Rows {
			var cells []any
			if w.Kind == logs.KindSeries {
				t, _ := time.Parse(time.RFC3339, fmt.Sprint(r["t"]))
				cells = append(cells, FaDateTime(t.In(doc.From.Location())))
			}
			for _, c := range w.Columns {
				cells = append(cells, plainValue(c.Type, r[c.Key]))
			}
			sh.Rows = append(sh.Rows, cells)
		}
		sheets = append(sheets, sh)
	}
	var buf bytes.Buffer
	err := writeXLSX(&buf, sheets)
	return buf.Bytes(), err
}

// ErrNoBrowser means PDF output needs a Chromium binary that is missing.
var ErrNoBrowser = errors.New("PDF output needs Chromium (TAKTAPLUS_CHROMIUM)")

// PDF prints the HTML with headless Chromium. The page is self-contained;
// its Content-Security-Policy forbids scripts and every fetch, and the
// browser gets an unreachable proxy as well.
func PDF(ctx context.Context, chromium, tmpDir string, page []byte) ([]byte, error) {
	bin := findBrowser(chromium)
	if bin == "" {
		return nil, ErrNoBrowser
	}
	dir, err := os.MkdirTemp(tmpDir, "report-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	in := filepath.Join(dir, "report.html")
	out := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(in, page, 0o600); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--headless", "--no-sandbox", "--disable-gpu", "--disable-dev-shm-usage",
		"--no-first-run", "--no-default-browser-check", "--disable-extensions", "--mute-audio",
		"--proxy-server=127.0.0.1:9", "--proxy-bypass-list=<-loopback>",
		"--user-data-dir="+filepath.Join(dir, "profile"), "--no-pdf-header-footer", "--print-to-pdf="+out, "file://"+in)
	cmd.Env = append(os.Environ(), "HOME="+dir)
	if msg, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("chromium: %v: %s", err, lastLine(string(msg)))
	}
	return os.ReadFile(out)
}

// findBrowser returns the configured browser, or the first Chromium found
// on PATH.
func findBrowser(configured string) string {
	names := []string{configured}
	if configured == "" {
		names = []string{"chromium-browser", "chromium", "chrome-headless-shell", "google-chrome"}
	}
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
