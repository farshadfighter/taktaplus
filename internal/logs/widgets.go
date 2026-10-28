package logs

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/farshadfighter/taktaplus/internal/chstore"
)

// Column describes one value of a widget row. Type tells the UI how to show
// it: text, number, bytes, duration, ip, country, severity, action, device.
type Column struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Type  string `json:"type"`
}

// Widget kinds.
const (
	KindKPIs   = "kpis"   // one row; each column is a figure
	KindSeries = "series" // rows ordered by "t"; other columns are series
	KindBars   = "bars"   // rows with "name" and "value"
	KindTable  = "table"
)

// Widget is a dashboard or report building block.
type Widget struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Kind    string   `json:"kind"`
	Group   string   `json:"group"` // security, traffic, web, vpn, waf, events
	Columns []Column `json:"columns"`
	// Drill names the log view type and the column used to filter it when a
	// row is clicked.
	Drill *Drill `json:"drill,omitempty"`

	sql func(bucket string) string
}

// Drill links a widget row to the log viewer.
type Drill struct {
	Type  string `json:"type"`
	Field string `json:"field"` // log column to filter on
	Key   string `json:"key"`   // row key that holds the value
}

// Data is a widget with its rows.
type Data struct {
	Widget
	Rows []map[string]any `json:"rows"`
}

// Range is the scope of a query.
type Range struct {
	From      time.Time
	To        time.Time
	DeviceIDs []int64 // empty means all devices
}

const blockedThreat = "action IN ('blocked','dropped','reset','block','deny','clear_session','reset-client','reset-server','quarantine')"
const blockedWAF = "action NOT IN ('alert','pass','accept','erase','')"

// scope is the WHERE clause shared by every rollup query.
const scope = "hour >= toStartOfHour({from:DateTime}) AND hour <= {to:DateTime} AND (empty({devs:Array(UInt32)}) OR has({devs:Array(UInt32)}, device_id))"

func top(n int) string { return fmt.Sprintf(" LIMIT %d", n) }

var widgets = []*Widget{
	{ID: "kpi_overview", Title: "نمای کلی", Kind: KindKPIs, Group: "security",
		Columns: []Column{{"traffic", "ترافیک کل", "bytes"}, {"sessions", "Sessionها", "number"}, {"blocked", "تهدیدهای مسدودشده", "number"}, {"waf", "حملات WAF", "number"}},
		sql: func(string) string {
			return `SELECT (SELECT sum(sent + rcvd) FROM r_traffic_app WHERE ` + scope + `) AS traffic,
				(SELECT sum(sessions) FROM r_traffic_app WHERE ` + scope + `) AS sessions,
				(SELECT sum(hits) FROM r_threat WHERE ` + scope + ` AND ` + blockedThreat + `) AS blocked,
				(SELECT sum(hits) FROM r_waf WHERE ` + scope + `) AS waf`
		}},
	{ID: "ts_bandwidth_threats", Title: "پهنای باند و تهدیدها در طول زمان", Kind: KindSeries, Group: "security",
		Columns: []Column{{"bytes", "ترافیک", "bytes"}, {"threats", "تهدید مسدودشده", "number"}},
		sql: func(b string) string {
			return `SELECT t, sum(bytes) AS bytes, sum(threats) AS threats FROM (
				SELECT ` + b + `(hour) AS t, sum(sent + rcvd) AS bytes, toUInt64(0) AS threats FROM r_traffic_app WHERE ` + scope + ` GROUP BY t
				UNION ALL
				SELECT ` + b + `(hour) AS t, toUInt64(0), sum(hits) FROM r_threat WHERE ` + scope + ` AND ` + blockedThreat + ` GROUP BY t)
				GROUP BY t ORDER BY t`
		}},
	{ID: "top_apps", Title: "پرمصرف‌ترین اپلیکیشن‌ها", Kind: KindBars, Group: "traffic",
		Columns: []Column{{"name", "اپلیکیشن", "text"}, {"value", "ترافیک", "bytes"}},
		Drill:   &Drill{Type: "traffic", Field: "app", Key: "name"},
		sql: func(string) string {
			return `SELECT if(app = '', 'unknown', app) AS name, sum(sent + rcvd) AS value FROM r_traffic_app WHERE ` + scope +
				` GROUP BY name ORDER BY value DESC` + top(10)
		}},
	{ID: "top_threats", Title: "مهم‌ترین تهدیدها", Kind: KindTable, Group: "security",
		Columns: []Column{{"threat", "تهدید", "text"}, {"subtype", "نوع", "text"}, {"severity", "شدت", "severity"}, {"hits", "تعداد", "number"}, {"action", "اقدام", "action"}},
		Drill:   &Drill{Type: "threat", Field: "threat", Key: "threat"},
		sql: func(string) string {
			return `SELECT threat, any(subtype) AS subtype, any(severity) AS severity, sum(hits) AS hits, argMax(action, r_threat.hits) AS action
				FROM r_threat WHERE ` + scope + ` AND threat != '' GROUP BY threat ORDER BY hits DESC` + top(10)
		}},
	{ID: "risky_hosts", Title: "میزبان‌های پرخطر", Kind: KindTable, Group: "security",
		Columns: []Column{{"srcip", "مبدأ", "ip"}, {"hits", "تهدیدها", "number"}, {"threats", "انواع تهدید", "number"}, {"blocked", "مسدودشده", "number"}},
		Drill:   &Drill{Type: "threat", Field: "srcip", Key: "srcip"},
		sql: func(string) string {
			return `SELECT srcip, sum(hits) AS hits, uniqExact(threat) AS threats, sumIf(r_threat.hits, ` + blockedThreat + `) AS blocked
				FROM r_threat WHERE ` + scope + ` AND srcip != '' GROUP BY srcip ORDER BY hits DESC` + top(10)
		}},
	{ID: "top_sources", Title: "پرترافیک‌ترین مبدأها", Kind: KindTable, Group: "traffic",
		Columns: []Column{{"srcip", "مبدأ", "ip"}, {"username", "کاربر", "text"}, {"bytes", "ترافیک", "bytes"}, {"sessions", "Session", "number"}},
		Drill:   &Drill{Type: "traffic", Field: "srcip", Key: "srcip"},
		sql: func(string) string {
			return `SELECT srcip, argMax(username, sent + rcvd) AS username, sum(sent + rcvd) AS bytes, sum(sessions) AS sessions
				FROM r_traffic_src WHERE ` + scope + ` GROUP BY srcip ORDER BY bytes DESC` + top(10)
		}},
	{ID: "top_destinations", Title: "پرترافیک‌ترین مقصدها", Kind: KindTable, Group: "traffic",
		Columns: []Column{{"dstip", "مقصد", "ip"}, {"dstcountry", "کشور", "country"}, {"bytes", "ترافیک", "bytes"}, {"sessions", "Session", "number"}},
		Drill:   &Drill{Type: "traffic", Field: "dstip", Key: "dstip"},
		sql: func(string) string {
			return `SELECT dstip, any(dstcountry) AS dstcountry, sum(sent + rcvd) AS bytes, sum(sessions) AS sessions
				FROM r_traffic_dst WHERE ` + scope + ` GROUP BY dstip ORDER BY bytes DESC` + top(10)
		}},
	{ID: "top_countries", Title: "کشورهای مقصد", Kind: KindBars, Group: "traffic",
		Columns: []Column{{"name", "کشور", "country"}, {"value", "ترافیک", "bytes"}},
		Drill:   &Drill{Type: "traffic", Field: "dstcountry", Key: "name"},
		sql: func(string) string {
			return `SELECT dstcountry AS name, sum(sent + rcvd) AS value FROM r_traffic_dst WHERE ` + scope +
				` AND dstcountry NOT IN ('', 'Reserved') GROUP BY name ORDER BY value DESC` + top(10)
		}},
	{ID: "ts_bandwidth", Title: "پهنای باند (ارسال و دریافت)", Kind: KindSeries, Group: "traffic",
		Columns: []Column{{"sent", "ارسال", "bytes"}, {"rcvd", "دریافت", "bytes"}},
		sql: func(b string) string {
			return `SELECT ` + b + `(hour) AS t, sum(sent) AS sent, sum(rcvd) AS rcvd FROM r_traffic_app WHERE ` + scope + ` GROUP BY t ORDER BY t`
		}},
	{ID: "denied_apps", Title: "ترافیک ردشده بر اساس سرویس", Kind: KindBars, Group: "traffic",
		Columns: []Column{{"name", "سرویس", "text"}, {"value", "Session", "number"}},
		Drill:   &Drill{Type: "traffic", Field: "app", Key: "name"},
		sql: func(string) string {
			return `SELECT if(app = '', 'unknown', app) AS name, sum(sessions) AS value FROM r_traffic_app WHERE ` + scope +
				` AND action = 'deny' GROUP BY name ORDER BY value DESC` + top(10)
		}},
	{ID: "kpi_web", Title: "WebFilter", Kind: KindKPIs, Group: "web",
		Columns: []Column{{"requests", "درخواست‌ها", "number"}, {"blocked", "مسدودشده", "number"}, {"categories", "دسته‌ها", "number"}, {"users", "کاربران", "number"}},
		sql: func(string) string {
			return `SELECT sum(hits) AS requests, sumIf(hits, action = 'blocked') AS blocked, uniqExact(category) AS categories,
				uniqExactIf(username, username != '') AS users FROM r_web WHERE ` + scope
		}},
	{ID: "web_categories", Title: "دسته‌های وب", Kind: KindTable, Group: "web",
		Columns: []Column{{"category", "دسته", "text"}, {"hits", "درخواست", "number"}, {"blocked", "مسدودشده", "number"}},
		Drill:   &Drill{Type: "threat", Field: "category", Key: "category"},
		sql: func(string) string {
			return `SELECT category, sum(hits) AS hits, sumIf(r_web.hits, action = 'blocked') AS blocked FROM r_web WHERE ` + scope +
				` GROUP BY category ORDER BY hits DESC` + top(15)
		}},
	{ID: "web_blocked_hosts", Title: "سایت‌های مسدودشده", Kind: KindTable, Group: "web",
		Columns: []Column{{"hostname", "سایت", "text"}, {"category", "دسته", "text"}, {"hits", "تعداد", "number"}},
		Drill:   &Drill{Type: "threat", Field: "hostname", Key: "hostname"},
		sql: func(string) string {
			return `SELECT hostname, any(category) AS category, sum(hits) AS hits FROM r_web WHERE ` + scope +
				` AND action = 'blocked' GROUP BY hostname ORDER BY hits DESC` + top(15)
		}},
	{ID: "web_users", Title: "کاربران پرتکرار", Kind: KindTable, Group: "web",
		Columns: []Column{{"username", "کاربر", "text"}, {"hits", "درخواست", "number"}, {"blocked", "مسدودشده", "number"}},
		Drill:   &Drill{Type: "threat", Field: "username", Key: "username"},
		sql: func(string) string {
			return `SELECT username, sum(hits) AS hits, sumIf(r_web.hits, action = 'blocked') AS blocked FROM r_web WHERE ` + scope +
				` AND username != '' GROUP BY username ORDER BY hits DESC` + top(15)
		}},
	{ID: "kpi_vpn", Title: "VPN", Kind: KindKPIs, Group: "vpn",
		Columns: []Column{{"connections", "اتصال‌ها", "number"}, {"users", "کاربران", "number"}, {"failed", "ورود ناموفق", "number"}, {"bytes", "حجم", "bytes"}},
		sql: func(string) string {
			return `SELECT sumIf(events, action = 'tunnel-up') AS connections, uniqExactIf(username, username != '' AND action = 'tunnel-up') AS users,
				sumIf(events, positionCaseInsensitive(action, 'fail') > 0) AS failed, sum(sent + rcvd) AS bytes FROM r_vpn WHERE ` + scope
		}},
	{ID: "vpn_users", Title: "کاربران VPN", Kind: KindTable, Group: "vpn",
		Columns: []Column{{"username", "کاربر", "text"}, {"tunneltype", "نوع", "text"}, {"connections", "اتصال", "number"}, {"bytes", "حجم", "bytes"}, {"duration", "مدت", "duration"}},
		Drill:   &Drill{Type: "vpn", Field: "username", Key: "username"},
		sql: func(string) string {
			return `SELECT username, any(tunneltype) AS tunneltype, sumIf(events, action = 'tunnel-up') AS connections,
				sum(sent + rcvd) AS bytes, sum(duration) AS duration FROM r_vpn WHERE ` + scope +
				` AND username != '' GROUP BY username ORDER BY connections DESC, bytes DESC` + top(20)
		}},
	{ID: "vpn_failures", Title: "ورودهای ناموفق VPN", Kind: KindTable, Group: "vpn",
		Columns: []Column{{"username", "کاربر", "text"}, {"srcip", "مبدأ", "ip"}, {"failures", "تعداد", "number"}},
		Drill:   &Drill{Type: "vpn", Field: "srcip", Key: "srcip"},
		sql: func(string) string {
			return `SELECT username, srcip, sum(events) AS failures FROM r_vpn WHERE ` + scope +
				` AND positionCaseInsensitive(action, 'fail') > 0 GROUP BY username, srcip ORDER BY failures DESC` + top(20)
		}},
	{ID: "kpi_waf", Title: "حملات WAF", Kind: KindKPIs, Group: "waf",
		Columns: []Column{{"attacks", "حملات", "number"}, {"blocked", "مسدودشده", "number"}, {"sources", "مبدأهای یکتا", "number"}, {"hosts", "سایت‌های هدف", "number"}},
		sql: func(string) string {
			return `SELECT sum(hits) AS attacks, sumIf(hits, ` + blockedWAF + `) AS blocked, uniqExact(srcip) AS sources,
				uniqExactIf(http_host, http_host != '') AS hosts FROM r_waf WHERE ` + scope
		}},
	{ID: "ts_waf", Title: "حملات WAF در طول زمان", Kind: KindSeries, Group: "waf",
		Columns: []Column{{"attacks", "حملات", "number"}, {"blocked", "مسدودشده", "number"}},
		sql: func(b string) string {
			return `SELECT ` + b + `(hour) AS t, sum(hits) AS attacks, sumIf(hits, ` + blockedWAF + `) AS blocked FROM r_waf WHERE ` + scope + ` GROUP BY t ORDER BY t`
		}},
	{ID: "waf_attacks", Title: "انواع حمله", Kind: KindBars, Group: "waf",
		Columns: []Column{{"name", "نوع حمله", "text"}, {"value", "تعداد", "number"}},
		Drill:   &Drill{Type: "waf", Field: "attack", Key: "name"},
		sql: func(string) string {
			return `SELECT attack AS name, sum(hits) AS value FROM r_waf WHERE ` + scope + ` GROUP BY name ORDER BY value DESC` + top(10)
		}},
	{ID: "waf_sources", Title: "مبدأهای حمله", Kind: KindTable, Group: "waf",
		Columns: []Column{{"srcip", "مبدأ", "ip"}, {"srccountry", "کشور", "country"}, {"hits", "تعداد", "number"}, {"blocked", "مسدودشده", "number"}},
		Drill:   &Drill{Type: "waf", Field: "srcip", Key: "srcip"},
		sql: func(string) string {
			return `SELECT srcip, any(srccountry) AS srccountry, sum(hits) AS hits, sumIf(r_waf.hits, ` + blockedWAF + `) AS blocked FROM r_waf WHERE ` + scope +
				` GROUP BY srcip ORDER BY hits DESC` + top(15)
		}},
	{ID: "waf_hosts", Title: "سایت‌های هدف", Kind: KindTable, Group: "waf",
		Columns: []Column{{"http_host", "سایت", "text"}, {"hits", "حملات", "number"}, {"attacks", "انواع حمله", "number"}},
		Drill:   &Drill{Type: "waf", Field: "http_host", Key: "http_host"},
		sql: func(string) string {
			return `SELECT http_host, sum(hits) AS hits, uniqExact(attack) AS attacks FROM r_waf WHERE ` + scope +
				` AND http_host != '' GROUP BY http_host ORDER BY hits DESC` + top(15)
		}},
	{ID: "waf_countries", Title: "کشورهای مبدأ حمله", Kind: KindBars, Group: "waf",
		Columns: []Column{{"name", "کشور", "country"}, {"value", "حملات", "number"}},
		Drill:   &Drill{Type: "waf", Field: "srccountry", Key: "name"},
		sql: func(string) string {
			return `SELECT srccountry AS name, sum(hits) AS value FROM r_waf WHERE ` + scope +
				` AND srccountry NOT IN ('', 'Reserved') GROUP BY name ORDER BY value DESC` + top(10)
		}},
	{ID: "kpi_events", Title: "رویدادها", Kind: KindKPIs, Group: "events",
		Columns: []Column{{"events", "رویدادها", "number"}, {"critical", "بحرانی/خطا", "number"}, {"warnings", "هشدار", "number"}},
		sql: func(string) string {
			return `SELECT sum(hits) AS events, sumIf(hits, level IN ('emergency','alert','critical','error')) AS critical,
				sumIf(hits, level = 'warning') AS warnings FROM r_event WHERE ` + scope
		}},
	{ID: "events_summary", Title: "رویدادهای سیستمی", Kind: KindTable, Group: "events",
		Columns: []Column{{"logdesc", "رویداد", "text"}, {"subtype", "نوع", "text"}, {"level", "سطح", "severity"}, {"hits", "تعداد", "number"}},
		Drill:   &Drill{Type: "event", Field: "logdesc", Key: "logdesc"},
		sql: func(string) string {
			return `SELECT logdesc, subtype, level, sum(hits) AS hits FROM r_event WHERE ` + scope +
				` AND subtype != 'vpn' GROUP BY logdesc, subtype, level ORDER BY hits DESC` + top(20)
		}},
}

var widgetByID = func() map[string]*Widget {
	m := map[string]*Widget{}
	for _, w := range widgets {
		m[w.ID] = w
	}
	return m
}()

// Tabs lists the widgets of each dashboard tab.
var Tabs = map[string][]string{
	"security": {"kpi_overview", "ts_bandwidth_threats", "top_apps", "top_threats", "top_sources", "top_countries", "risky_hosts"},
	"traffic":  {"ts_bandwidth", "top_sources", "top_destinations", "top_apps", "top_countries", "denied_apps"},
	"web":      {"kpi_web", "web_categories", "web_blocked_hosts", "web_users"},
	"vpn":      {"kpi_vpn", "vpn_users", "vpn_failures"},
	"waf":      {"kpi_waf", "ts_waf", "waf_attacks", "waf_sources", "waf_hosts", "waf_countries"},
	"events":   {"kpi_events", "events_summary"},
}

// Widgets lists every widget (for building custom report templates).
func Widgets() []Widget {
	out := make([]Widget, len(widgets))
	for i, w := range widgets {
		out[i] = *w
	}
	return out
}

// KnownWidget reports whether id names a widget.
func KnownWidget(id string) bool { return widgetByID[id] != nil }

func (r Range) params() chstore.Params {
	devs := make([]uint32, 0, len(r.DeviceIDs))
	for _, id := range r.DeviceIDs {
		devs = append(devs, uint32(id))
	}
	return chstore.Params{"from": r.From.UTC().Format("2006-01-02 15:04:05"), "to": r.To.UTC().Format("2006-01-02 15:04:05"), "devs": devs}
}

// bucket picks the time series resolution.
func (r Range) bucket() string {
	if r.To.Sub(r.From) <= 3*24*time.Hour {
		return "toStartOfHour"
	}
	return "toStartOfDay"
}

// Query runs the widgets in ids for the range.
func (s *Service) Query(ctx context.Context, ids []string, r Range) ([]Data, error) {
	if s.ch == nil {
		return nil, ErrDisabled
	}
	out := make([]Data, 0, len(ids))
	for _, id := range ids {
		w := widgetByID[id]
		if w == nil {
			return nil, fmt.Errorf("unknown widget %q", id)
		}
		rows, err := chstore.Select[map[string]any](ctx, s.ch, w.sql(r.bucket()), r.params())
		if err != nil {
			return nil, fmt.Errorf("widget %s: %w", id, err)
		}
		if w.Kind == KindSeries {
			rows = fillSeries(rows, w, r)
		}
		out = append(out, Data{Widget: *w, Rows: rows})
	}
	return out, nil
}

// fillSeries adds zero points so charts show gaps as zero.
func fillSeries(rows []map[string]any, w *Widget, r Range) []map[string]any {
	step := time.Hour
	start := r.From.UTC().Truncate(time.Hour)
	if r.bucket() == "toStartOfDay" {
		step = 24 * time.Hour
		start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	}
	have := map[string]map[string]any{}
	for _, row := range rows {
		if t, ok := row["t"].(string); ok {
			if p, err := time.Parse(time.RFC3339, strings.TrimSuffix(t, "Z")+"Z"); err == nil {
				have[p.UTC().Format(time.RFC3339)] = row
			}
		}
	}
	var out []map[string]any
	for t := start; !t.After(r.To); t = t.Add(step) {
		key := t.Format(time.RFC3339)
		if row, ok := have[key]; ok {
			row["t"] = key
			out = append(out, row)
			continue
		}
		row := map[string]any{"t": key}
		for _, c := range w.Columns {
			row[c.Key] = 0
		}
		out = append(out, row)
		if len(out) > 2000 {
			break
		}
	}
	return out
}
