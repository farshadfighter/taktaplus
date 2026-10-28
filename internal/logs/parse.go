// Package logs receives FortiGate and FortiWeb logs over syslog, turns them
// into rows for the ClickHouse log store and answers log queries.
package logs

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// Limits on what one message may carry into `fields`.
const (
	maxFields     = 300
	maxValueBytes = 4096
)

// Message is one parsed log line.
type Message struct {
	Serial string            // devid (FortiGate) or device_id (FortiWeb)
	Fields map[string]string // every key=value pair
	Time   time.Time         // event time (UTC)
}

var errNotFortinet = errors.New("not a Fortinet key=value log")

// Parse parses one syslog message in FortiOS/FortiWeb "default" format,
// with or without an RFC 3164/5424 header. now is the receive time, used
// when the message carries no usable time.
func Parse(line []byte, now time.Time) (*Message, error) {
	s := strings.TrimRight(string(line), "\r\n\x00")
	if strings.HasPrefix(s, "<") {
		if i := strings.IndexByte(s, '>'); i > 0 && i <= 4 {
			s = s[i+1:]
		}
	}
	start := kvStart(s)
	if start < 0 {
		return nil, errNotFortinet
	}
	fields := parseKV(s[start:])
	serial := fields["devid"]
	if serial == "" {
		serial = fields["device_id"]
	}
	if serial == "" || fields["type"] == "" {
		return nil, errNotFortinet
	}
	return &Message{Serial: serial, Fields: fields, Time: eventTime(fields, now)}, nil
}

// kvStart finds the first token that looks like key=value, skipping any
// syslog header.
func kvStart(s string) int {
	for i := 0; i < len(s); {
		j := i
		for j < len(s) && isKeyChar(s[j]) {
			j++
		}
		if j > i && j < len(s) && s[j] == '=' {
			return i
		}
		k := strings.IndexByte(s[i:], ' ')
		if k < 0 {
			return -1
		}
		i += k + 1
	}
	return -1
}

func isKeyChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.'
}

// parseKV reads key=value pairs; values may be double-quoted with \"
// escapes.
func parseKV(s string) map[string]string {
	out := map[string]string{}
	i := 0
	for i < len(s) && len(out) < maxFields {
		for i < len(s) && s[i] == ' ' {
			i++
		}
		k := i
		for i < len(s) && isKeyChar(s[i]) {
			i++
		}
		if i >= len(s) || s[i] != '=' || i == k {
			// Not a pair: skip the token.
			for i < len(s) && s[i] != ' ' {
				i++
			}
			continue
		}
		key := s[k:i]
		i++
		var val string
		if i < len(s) && s[i] == '"' {
			i++
			var b strings.Builder
			for i < len(s) && s[i] != '"' {
				if s[i] == '\\' && i+1 < len(s) {
					i++
				}
				b.WriteByte(s[i])
				i++
			}
			i++ // closing quote
			val = b.String()
		} else {
			v := i
			for i < len(s) && s[i] != ' ' {
				i++
			}
			val = s[v:i]
		}
		if len(val) > maxValueBytes {
			val = val[:maxValueBytes]
		}
		out[key] = val
	}
	return out
}

// eventTime prefers eventtime (s, ms, µs or ns since the epoch), then
// date+time with the tz offset, then the receive time. Times far in the
// future are not trusted.
func eventTime(f map[string]string, now time.Time) time.Time {
	t := time.Time{}
	if et := f["eventtime"]; et != "" {
		if n, err := strconv.ParseInt(et, 10, 64); err == nil && n > 0 {
			switch {
			case len(et) >= 19:
				t = time.Unix(0, n)
			case len(et) >= 16:
				t = time.UnixMicro(n)
			case len(et) >= 13:
				t = time.UnixMilli(n)
			default:
				t = time.Unix(n, 0)
			}
		}
	}
	if t.IsZero() && f["date"] != "" && f["time"] != "" {
		loc := time.UTC
		if off, ok := parseOffset(f["tz"]); ok {
			loc = time.FixedZone("", off)
		} else if off, ok := parseGMT(f["timezone"]); ok {
			loc = time.FixedZone("", off)
		}
		if p, err := time.ParseInLocation("2006-01-02 15:04:05", f["date"]+" "+f["time"], loc); err == nil {
			t = p
		}
	}
	if t.IsZero() || t.After(now.Add(10*time.Minute)) {
		t = now
	}
	return t.UTC()
}

// parseOffset reads "+0330" or "-0500".
func parseOffset(s string) (int, bool) {
	s = strings.Trim(s, `"`)
	if len(s) != 5 || (s[0] != '+' && s[0] != '-') {
		return 0, false
	}
	h, err1 := strconv.Atoi(s[1:3])
	m, err2 := strconv.Atoi(s[3:5])
	if err1 != nil || err2 != nil {
		return 0, false
	}
	off := h*3600 + m*60
	if s[0] == '-' {
		off = -off
	}
	return off, true
}

// parseGMT reads FortiWeb's "(GMT+3:30)Tehran".
func parseGMT(s string) (int, bool) {
	i := strings.Index(s, "GMT")
	if i < 0 {
		return 0, false
	}
	rest := s[i+3:]
	if j := strings.IndexByte(rest, ')'); j >= 0 {
		rest = rest[:j]
	}
	if rest == "" {
		return 0, true
	}
	sign := 1
	switch rest[0] {
	case '+':
	case '-':
		sign = -1
	default:
		return 0, false
	}
	hm := strings.SplitN(rest[1:], ":", 2)
	h, err := strconv.Atoi(hm[0])
	if err != nil {
		return 0, false
	}
	m := 0
	if len(hm) == 2 {
		if m, err = strconv.Atoi(hm[1]); err != nil {
			return 0, false
		}
	}
	return sign * (h*3600 + m*60), true
}

// Table names.
const (
	TableTraffic = "log_traffic"
	TableThreat  = "log_threat"
	TableEvent   = "log_event"
	TableWAF     = "log_waf"
)

// utmTypes are FortiOS 5.x type values that 6.x+ reports as type=utm with
// the same subtype.
var utmTypes = map[string]bool{"ips": true, "virus": true, "webfilter": true, "app-ctrl": true, "dns": true,
	"anomaly": true, "emailfilter": true, "dlp": true, "ssl": true, "waf": true, "file-filter": true, "voip": true,
	"icap": true, "ssh": true, "cifs": true, "virtual-patch": true}

// Row turns a message into the table it belongs to and the row to insert.
// FortiWeb messages (device_id rather than devid) use their own field names.
func Row(m *Message, deviceID int64) (string, map[string]any) {
	f := m.Fields
	g := func(keys ...string) string {
		for _, k := range keys {
			if v := f[k]; v != "" {
				return v
			}
		}
		return ""
	}
	num := func(keys ...string) uint64 {
		n, _ := strconv.ParseUint(g(keys...), 10, 64)
		return n
	}
	port := func(keys ...string) uint64 { return min(num(keys...), 65535) }
	row := map[string]any{"ts": m.Time.Format("2006-01-02T15:04:05.000Z"), "device_id": deviceID, "fields": f}
	set := func(kv ...any) {
		for i := 0; i < len(kv); i += 2 {
			row[kv[i].(string)] = kv[i+1]
		}
	}
	typ, sub := f["type"], f["subtype"]
	fortiweb := f["device_id"] != "" && f["devid"] == ""
	if fortiweb {
		common := func() {
			set("srcip", g("src"), "srcport", port("src_port"), "dstip", g("dst"), "dstport", port("dst_port"))
		}
		switch typ {
		case "attack":
			common()
			set("subtype", sub, "severity", strings.ToLower(g("severity_level", "pri")), "action", strings.ToLower(g("action")),
				"attack", g("main_type", "sub_type", sub), "signature", g("signature_id", "signature_subclass"),
				"policy", g("policy"), "srccountry", g("srccountry"), "http_host", g("http_host"),
				"http_method", strings.ToUpper(g("http_method")), "http_url", g("http_url"), "msg", g("msg"))
			return TableWAF, row
		case "traffic":
			common()
			set("subtype", sub, "level", g("pri"), "action", strings.ToLower(g("action")), "service", g("service"),
				"app", g("http_host"), "srccountry", g("srccountry"), "policyid", uint64(0),
				"sentbyte", num("sent_bytes", "sentbyte"), "rcvdbyte", num("received_bytes", "rcvdbyte"))
			return TableTraffic, row
		default:
			set("subtype", g("subtype", typ), "level", g("pri"), "action", g("action"), "logdesc", g("msg_id", "log_id"),
				"username", g("user"), "srcip", g("ui", "src"), "msg", g("msg"))
			return TableEvent, row
		}
	}
	if utmTypes[typ] {
		sub, typ = typ, "utm"
	}
	user := g("user", "srcuser", "unauthuser")
	switch typ {
	case "traffic":
		set("vd", g("vd"), "subtype", sub, "level", g("level"), "action", g("action"), "policyid", num("policyid"),
			"srcip", g("srcip"), "srcport", port("srcport"), "srcintf", g("srcintf"), "srccountry", g("srccountry"),
			"dstip", g("dstip"), "dstport", port("dstport"), "dstintf", g("dstintf"), "dstcountry", g("dstcountry"),
			"proto", min(num("proto"), 255), "service", g("service"), "app", g("app", "service"), "appcat", g("appcat"),
			"username", user, "sentbyte", num("sentbyte"), "rcvdbyte", num("rcvdbyte"), "duration", num("duration"))
		return TableTraffic, row
	case "utm":
		threat := map[string]string{"ips": g("attack"), "anomaly": g("attack"), "virus": g("virus"), "app-ctrl": g("app"),
			"webfilter": g("catdesc"), "dns": g("qname"), "dlp": g("filtername"), "file-filter": g("filtername")}[sub]
		if threat == "" {
			threat = g("attack", "virus", "app", "msg")
		}
		set("vd", g("vd"), "subtype", sub, "eventtype", g("eventtype"), "level", g("level"),
			"severity", strings.ToLower(g("severity", "crlevel", "apprisk", "level")), "action", g("action"),
			"policyid", num("policyid"), "srcip", g("srcip"), "srcport", port("srcport"), "srccountry", g("srccountry"),
			"dstip", g("dstip"), "dstport", port("dstport"), "dstcountry", g("dstcountry"), "username", user,
			"threat", threat, "category", g("catdesc", "appcat", "dtype", "ref"), "hostname", g("hostname", "qname"), "url", g("url"))
		return TableThreat, row
	default:
		set("vd", g("vd"), "subtype", g("subtype", typ), "level", g("level"), "action", g("action"),
			"logdesc", g("logdesc", "msg"), "username", user, "srcip", g("remip", "srcip"), "tunneltype", g("tunneltype"),
			"sentbyte", num("sentbyte"), "rcvdbyte", num("rcvdbyte"), "duration", num("duration"), "msg", g("msg"))
		return TableEvent, row
	}
}
