package logs

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/farshadfighter/taktaplus/internal/chstore"
)

// View is a log viewer type: a table, an optional fixed condition and the
// columns shown.
type View struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Columns []Column `json:"columns"`
	table   string
	where   string
	// fields are every column that can be filtered on directly.
	fields []string
}

var views = []*View{
	{ID: "traffic", Title: "ترافیک", table: TableTraffic,
		Columns: []Column{{"srcip", "مبدأ", "ip"}, {"dstip", "مقصد", "ip"}, {"dstport", "پورت", "number"}, {"service", "سرویس", "text"},
			{"app", "اپلیکیشن", "text"}, {"policyid", "Policy", "number"}, {"action", "اقدام", "action"}, {"sentbyte", "ارسال", "bytes"}, {"rcvdbyte", "دریافت", "bytes"}},
		fields: []string{"vd", "subtype", "level", "action", "policyid", "srcip", "srcport", "srcintf", "srccountry", "dstip", "dstport",
			"dstintf", "dstcountry", "proto", "service", "app", "appcat", "username"}},
	{ID: "threat", Title: "تهدید", table: TableThreat,
		Columns: []Column{{"subtype", "نوع", "text"}, {"threat", "تهدید", "text"}, {"severity", "شدت", "severity"}, {"srcip", "مبدأ", "ip"},
			{"dstip", "مقصد", "ip"}, {"hostname", "میزبان", "text"}, {"username", "کاربر", "text"}, {"action", "اقدام", "action"}},
		fields: []string{"vd", "subtype", "eventtype", "level", "severity", "action", "policyid", "srcip", "srcport", "srccountry",
			"dstip", "dstport", "dstcountry", "username", "threat", "category", "hostname", "url"}},
	{ID: "event", Title: "رویداد", table: TableEvent, where: "subtype != 'vpn'",
		Columns: []Column{{"subtype", "نوع", "text"}, {"level", "سطح", "severity"}, {"logdesc", "رویداد", "text"}, {"username", "کاربر", "text"},
			{"action", "اقدام", "text"}, {"msg", "پیام", "text"}},
		fields: []string{"vd", "subtype", "level", "action", "logdesc", "username", "srcip", "msg"}},
	{ID: "vpn", Title: "VPN", table: TableEvent, where: "subtype = 'vpn'",
		Columns: []Column{{"username", "کاربر", "text"}, {"srcip", "مبدأ", "ip"}, {"tunneltype", "نوع", "text"}, {"action", "اقدام", "action"},
			{"sentbyte", "ارسال", "bytes"}, {"rcvdbyte", "دریافت", "bytes"}, {"duration", "مدت", "duration"}},
		fields: []string{"vd", "level", "action", "logdesc", "username", "srcip", "tunneltype", "msg"}},
	{ID: "waf", Title: "WAF", table: TableWAF,
		Columns: []Column{{"attack", "حمله", "text"}, {"severity", "شدت", "severity"}, {"srcip", "مبدأ", "ip"}, {"srccountry", "کشور", "country"},
			{"http_host", "سایت", "text"}, {"http_method", "متد", "text"}, {"http_url", "URL", "text"}, {"action", "اقدام", "action"}},
		fields: []string{"subtype", "severity", "action", "attack", "signature", "policy", "srcip", "srcport", "srccountry",
			"dstip", "dstport", "http_host", "http_method", "http_url", "msg"}},
}

// Views lists the log viewer types.
func Views() []View {
	out := make([]View, len(views))
	for i, v := range views {
		out[i] = *v
	}
	return out
}

func viewByID(id string) *View {
	for _, v := range views {
		if v.ID == id {
			return v
		}
	}
	return nil
}

// Filter is one condition of a search.
type Filter struct {
	Field string `json:"field"`
	Op    string `json:"op"` // eq, ne, contains
	Value string `json:"value"`
}

// SearchQuery selects log records.
type SearchQuery struct {
	Type    string
	Range   Range
	Filters []Filter
	Limit   int
	Offset  int
}

// Record is one log line for the viewer.
type Record map[string]any

// SearchResult is a page of records.
type SearchResult struct {
	Records []Record `json:"records"`
	Total   uint64   `json:"total"`
	// Capped means there are at least Total records; counting stopped.
	Capped bool    `json:"capped"`
	TookMS float64 `json:"took_ms"`
}

const countCap = 100_000

var fieldKey = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// where builds the condition and parameters for a query.
func (q SearchQuery) where(v *View) (string, chstore.Params, error) {
	p := q.Range.params()
	conds := []string{"ts >= {from:DateTime64(3)}", "ts <= {to:DateTime64(3)}", "(empty({devs:Array(UInt32)}) OR has({devs:Array(UInt32)}, device_id))"}
	if v.where != "" {
		conds = append(conds, v.where)
	}
	for i, f := range q.Filters {
		f.Field = strings.TrimSpace(f.Field)
		if f.Field == "device_id" {
			id, err := strconv.ParseUint(f.Value, 10, 32)
			if err != nil {
				return "", nil, invalidf("device filter must be a device id")
			}
			op := "="
			if f.Op == "ne" {
				op = "!="
			}
			conds = append(conds, fmt.Sprintf("device_id %s %d", op, id))
			continue
		}
		var expr string
		switch {
		case slices.Contains(v.fields, f.Field):
			expr = "toString(" + f.Field + ")"
		case fieldKey.MatchString(f.Field):
			// Any other key the device sent, from the raw fields.
			p[fmt.Sprintf("k%d", i)] = f.Field
			expr = fmt.Sprintf("fields[{k%d:String}]", i)
		default:
			return "", nil, invalidf("unknown field %q", f.Field)
		}
		name := fmt.Sprintf("f%d", i)
		p[name] = f.Value
		switch f.Op {
		case "", "eq":
			conds = append(conds, fmt.Sprintf("%s = {%s:String}", expr, name))
		case "ne":
			conds = append(conds, fmt.Sprintf("%s != {%s:String}", expr, name))
		case "contains":
			conds = append(conds, fmt.Sprintf("positionCaseInsensitive(%s, {%s:String}) > 0", expr, name))
		default:
			return "", nil, invalidf("unknown filter operator %q", f.Op)
		}
	}
	p["from"] = q.Range.From.UTC().Format("2006-01-02 15:04:05.000")
	p["to"] = q.Range.To.UTC().Format("2006-01-02 15:04:05.000")
	return strings.Join(conds, " AND "), p, nil
}

// Search returns a page of records, newest first.
func (s *Service) Search(ctx context.Context, q SearchQuery) (*SearchResult, error) {
	if s.ch == nil {
		return nil, ErrDisabled
	}
	v := viewByID(q.Type)
	if v == nil {
		return nil, invalidf("unknown log type %q", q.Type)
	}
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 100
	}
	if q.Offset < 0 || q.Offset > 10_000 {
		return nil, invalidf("page is out of range; narrow the search")
	}
	where, p, err := q.where(v)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	recs, err := chstore.Select[Record](ctx, s.ch, fmt.Sprintf(
		"SELECT * FROM %s WHERE %s ORDER BY ts DESC LIMIT %d OFFSET %d", v.table, where, q.Limit, q.Offset), p)
	if err != nil {
		return nil, err
	}
	type cnt struct {
		N uint64 `json:"n"`
	}
	c, err := chstore.Select[cnt](ctx, s.ch, fmt.Sprintf(
		"SELECT count() AS n FROM (SELECT 1 FROM %s WHERE %s LIMIT %d)", v.table, where, countCap), p)
	if err != nil {
		return nil, err
	}
	res := &SearchResult{Records: recs, TookMS: float64(time.Since(start).Microseconds()) / 1000}
	if len(c) == 1 {
		res.Total = c[0].N
		res.Capped = c[0].N >= countCap
	}
	return res, nil
}

// ExportLimit is the most rows a CSV export holds.
const ExportLimit = 100_000

// ExportCSV writes the matching records (newest first) as CSV with a UTF-8
// BOM so spreadsheet programs read Persian text correctly.
func (s *Service) ExportCSV(ctx context.Context, q SearchQuery, w io.Writer) error {
	if s.ch == nil {
		return ErrDisabled
	}
	v := viewByID(q.Type)
	if v == nil {
		return invalidf("unknown log type %q", q.Type)
	}
	where, p, err := q.where(v)
	if err != nil {
		return err
	}
	names, err := s.DeviceNames(ctx)
	if err != nil {
		return err
	}
	rows, err := s.ch.Query(ctx, fmt.Sprintf("SELECT * FROM %s WHERE %s ORDER BY ts DESC LIMIT %d", v.table, where, ExportLimit), p)
	if err != nil {
		return err
	}
	defer rows.Close()
	if _, err := io.WriteString(w, "\xEF\xBB\xBF"); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	header := []string{"time", "device"}
	for _, c := range v.Columns {
		header = append(header, c.Key)
	}
	if err := cw.Write(header); err != nil {
		return err
	}
	for {
		var r Record
		if !rows.Next(&r) {
			break
		}
		rec := []string{fmt.Sprint(r["ts"]), deviceName(names, r["device_id"])}
		for _, c := range v.Columns {
			rec = append(rec, csvSafe(fmt.Sprint(r[c.Key])))
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	if err := rows.Err(); err != nil {
		return err
	}
	return cw.Error()
}

func deviceName(names map[int64]string, id any) string {
	n, _ := id.(float64)
	if name := names[int64(n)]; name != "" {
		return name
	}
	return fmt.Sprint(id)
}

// csvSafe stops spreadsheet programs from running device-supplied text as
// a formula.
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}
