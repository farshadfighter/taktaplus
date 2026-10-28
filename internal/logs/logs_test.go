package logs

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/farshadfighter/taktaplus-lic/pkg/licformat"

	"github.com/farshadfighter/taktaplus/internal/chstore"
	"github.com/farshadfighter/taktaplus/internal/chstore/chtest"
	"github.com/farshadfighter/taktaplus/internal/device"
	"github.com/farshadfighter/taktaplus/internal/device/devicetest"
	"github.com/farshadfighter/taktaplus/internal/inventory"
	"github.com/farshadfighter/taktaplus/internal/license/licensetest"
	"github.com/farshadfighter/taktaplus/internal/secretbox"
	"github.com/farshadfighter/taktaplus/internal/settings"
	"github.com/farshadfighter/taktaplus/internal/testdb"
)

func TestParse(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name, line, serial, table string
		at                        time.Time
		check                     func(map[string]any) string
	}{
		{"fortios default", `<189>date=2026-10-28 time=10:21:44 devname="FGT-HQ" devid="FG100F0001" eventtime=1793182904123456789 tz="+0330" logid="0000000013" type="traffic" subtype="forward" level="notice" vd="root" srcip=10.10.21.45 srcport=51234 srcintf="port1" dstip=10.20.4.7 dstport=445 action="deny" policyid=42 service="SMB" sentbyte=0 rcvdbyte=0 msg="say \"hi\""`,
			"FG100F0001", TableTraffic, time.Unix(0, 1793182904123456789).UTC(),
			func(r map[string]any) string {
				if r["dstport"] != uint64(445) || r["action"] != "deny" || r["policyid"] != uint64(42) || r["app"] != "SMB" {
					return fmt.Sprint(r)
				}
				if f := r["fields"].(map[string]string); f["msg"] != `say "hi"` {
					return "quoted value: " + f["msg"]
				}
				return ""
			}},
		{"rfc5424 header and tz", `<190>1 2026-10-28T06:51:44Z fgt - - - - date=2026-10-28 time=10:21:44 devid="FG100F0001" tz="+0330" type="utm" subtype="ips" level="alert" severity="critical" attack="Log4j.RCE" action="dropped" srcip=1.2.3.4 dstip=10.0.0.5`,
			"FG100F0001", TableThreat, time.Date(2026, 10, 28, 6, 51, 44, 0, time.UTC),
			func(r map[string]any) string {
				if r["threat"] != "Log4j.RCE" || r["severity"] != "critical" || r["action"] != "dropped" {
					return fmt.Sprint(r)
				}
				return ""
			}},
		{"fortios 5 utm type", `devid=FG60E0002 type=webfilter level=warning catdesc="Gambling" hostname="bet.example" action=blocked user="ali" date=2026-10-28 time=09:00:00`,
			"FG60E0002", TableThreat, time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC),
			func(r map[string]any) string {
				if r["subtype"] != "webfilter" || r["threat"] != "Gambling" || r["category"] != "Gambling" || r["username"] != "ali" {
					return fmt.Sprint(r)
				}
				return ""
			}},
		{"vpn event", `<189>devid="FG100F0001" eventtime=1793182904 type="event" subtype="vpn" level="information" logdesc="SSL VPN tunnel down" action="tunnel-down" tunneltype="ssl-tunnel" remip=5.6.7.8 user="sara" sentbyte=1000 rcvdbyte=2000 duration=60`,
			"FG100F0001", TableEvent, time.Unix(1793182904, 0).UTC(),
			func(r map[string]any) string {
				if r["srcip"] != "5.6.7.8" || r["username"] != "sara" || r["duration"] != uint64(60) || r["subtype"] != "vpn" {
					return fmt.Sprint(r)
				}
				return ""
			}},
		{"fortiweb attack", `<185>date=2026-10-28 time=10:00:00 log_id=20000008 msg_id=000001 device_id=FV-1KD3A0001 vd="root" timezone="(GMT+3:30)Tehran" type=attack subtype="waf_signature_detection" pri=alert severity_level=High proto=tcp service=https action=Alert_Deny policy="web" src=9.9.9.9 src_port=4444 dst=10.0.0.80 dst_port=443 http_method=get http_host="shop.example" http_url="/?id=1'" main_type="SQL Injection" signature_id="030000136" srccountry="Germany" msg="SQLi"`,
			"FV-1KD3A0001", TableWAF, time.Date(2026, 10, 28, 6, 30, 0, 0, time.UTC),
			func(r map[string]any) string {
				if r["attack"] != "SQL Injection" || r["severity"] != "high" || r["action"] != "alert_deny" || r["http_method"] != "GET" || r["srcip"] != "9.9.9.9" {
					return fmt.Sprint(r)
				}
				return ""
			}},
		{"future time ignored", `devid=FG1 type=event subtype=system eventtime=4102444800`, "FG1", TableEvent, now, nil},
	}
	for _, c := range cases {
		m, err := Parse([]byte(c.line), now)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if m.Serial != c.serial || !m.Time.Equal(c.at) {
			t.Fatalf("%s: serial %s time %s", c.name, m.Serial, m.Time)
		}
		table, row := Row(m, 1)
		if table != c.table {
			t.Fatalf("%s: table %s", c.name, table)
		}
		if c.check != nil {
			if msg := c.check(row); msg != "" {
				t.Fatalf("%s: %s", c.name, msg)
			}
		}
	}
	for _, bad := range []string{"", "hello world", "<13>Oct 28 host sshd[1]: accepted", `type=traffic srcip=1.1.1.1`} {
		if _, err := Parse([]byte(bad), now); err == nil {
			t.Fatalf("%q parsed", bad)
		}
	}
}

func TestCollectorAndQueries(t *testing.T) {
	ch := chtest.New(t)
	db := testdb.New(t)
	ctx := context.Background()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secretbox.New(key)
	future := licformat.NewDate(time.Now().Year()+1, 1, 1)
	guard := licensetest.Guard(t, map[string]licformat.Date{"FGLOG001": future, "FGLOG002": future, "FVLOG003": future})
	inv := inventory.New(db, box, guard)
	fleet := devicetest.Fleet{
		"127.0.0.1": {Info: device.Info{Serial: "FGLOG001"}},
		"10.9.9.9":  {Info: device.Info{Serial: "FGLOG002"}},
		"127.0.0.2": {Info: device.Info{Serial: "FGNOLIC9"}},
		"127.0.0.3": {Info: device.Info{Serial: "FVLOG003"}},
	}
	inv.SetDriverFactory(fleet.Factory())
	ids := map[string]int64{}
	for host, kind := range map[string]device.Kind{"127.0.0.1": device.KindFortiGate, "10.9.9.9": device.KindFortiGate, "127.0.0.2": device.KindFortiGate, "127.0.0.3": device.KindFortiWeb} {
		id, err := inv.Create(ctx, inventory.Input{Name: "dev-" + host, Kind: kind, Host: host, Credentials: &inventory.Credentials{APIToken: "t"}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := inv.Probe(ctx, id); err != nil {
			t.Fatal(err)
		}
		ids[host] = id
	}
	geoDir := t.TempDir()
	svc := New(ch, settings.New(db, box), inv, NewGeoIP(geoDir), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := svc.Load(ctx); err != nil {
		t.Fatal(err)
	}
	col := svc.Collector()
	if err := col.Start(ctx, Listeners{UDP: "127.0.0.1:0", TCP: "127.0.0.1:0"}); err != nil {
		t.Fatal(err)
	}
	defer col.Close()

	now := time.Now().UTC()
	et := func(d time.Duration) int64 { return now.Add(-d).UnixNano() }
	lines := []string{
		fmt.Sprintf(`<189>devid="FGLOG001" eventtime=%d type="traffic" subtype="forward" srcip=10.0.0.1 dstip=8.8.8.8 dstport=443 dstcountry="United States" action="accept" app="HTTPS" sentbyte=1000 rcvdbyte=5000 user="ali"`, et(time.Minute)),
		fmt.Sprintf(`<189>devid="FGLOG001" eventtime=%d type="traffic" subtype="forward" srcip=10.0.0.1 dstip=8.8.4.4 dstport=443 dstcountry="United States" action="accept" app="HTTPS" sentbyte=500 rcvdbyte=500 user="ali"`, et(2*time.Minute)),
		fmt.Sprintf(`<189>devid="FGLOG001" eventtime=%d type="traffic" subtype="forward" srcip=10.0.0.2 dstip=10.20.4.7 dstport=445 action="deny" service="SMB" app="SMB" policyid=42`, et(3*time.Minute)),
		fmt.Sprintf(`<189>devid="FGLOG001" eventtime=%d type="utm" subtype="ips" severity="critical" attack="Log4j.RCE" action="dropped" srcip=10.0.0.2 dstip=1.1.1.1`, et(time.Minute)),
		fmt.Sprintf(`<189>devid="FGLOG001" eventtime=%d type="utm" subtype="webfilter" catdesc="Gambling" hostname="bet.example" action="blocked" user="ali" srcip=10.0.0.1`, et(time.Minute)),
		fmt.Sprintf(`<189>devid="FGLOG001" eventtime=%d type="event" subtype="vpn" action="tunnel-up" tunneltype="ssl-tunnel" remip=5.6.7.8 user="sara"`, et(time.Minute)),
		fmt.Sprintf(`<189>devid="FGLOG001" eventtime=%d type="event" subtype="vpn" action="ssl-login-fail" remip=6.6.6.6 user="admin"`, et(time.Minute)),
		fmt.Sprintf(`<189>devid="FGLOG001" eventtime=%d type="event" subtype="system" level="critical" logdesc="Power supply failure"`, et(time.Minute)),
	}
	// UDP from the registered address.
	udp, err := net.Dial("udp", col.Addrs()["udp"])
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		_, _ = udp.Write([]byte(l))
	}
	udp.Close()
	// TCP: one octet-counted frame, one newline-terminated, both from
	// FortiWeb's address (127.0.0.3), plus spoofed and rejected senders on
	// 127.0.0.1.
	waf := fmt.Sprintf(`device_id=FVLOG003 type=attack severity_level=High action=Alert_Deny src=9.9.9.9 http_host="shop.example" main_type="SQL Injection" srccountry="Germany" eventtime=%d`, et(time.Minute))
	dialFrom := func(local string) net.Conn {
		d := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(local)}}
		c, err := d.Dial("tcp", col.Addrs()["tcp"])
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	c3 := dialFrom("127.0.0.3")
	fmt.Fprintf(c3, "%d %s", len(waf), waf)
	fmt.Fprintf(c3, "%s\n", waf)
	c3.Close()
	c1 := dialFrom("127.0.0.1")
	fmt.Fprintf(c1, "devid=FGLOG002 type=traffic srcip=1.1.1.1\n")  // registered with 10.9.9.9: untrusted
	fmt.Fprintf(c1, "devid=FGNOLIC9 type=traffic srcip=1.1.1.1\n")  // no license
	fmt.Fprintf(c1, "devid=FGUNKNOWN type=traffic srcip=1.1.1.1\n") // not registered
	fmt.Fprintf(c1, "just noise\n")
	c1.Close()

	deadline := time.Now().Add(5 * time.Second)
	for {
		st := col.Stats()
		if st.Received == uint64(len(lines))+6 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stats = %+v", st)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := col.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	st := col.Stats()
	if st.Accepted != uint64(len(lines))+2 || st.Dropped[DropUntrusted] != 1 || st.Dropped[DropUnlicensed] != 1 ||
		st.Dropped[DropUnknown] != 1 || st.Dropped[DropUnparsable] != 1 || len(st.Rejected) != 3 || st.EPS <= 0 {
		t.Fatalf("stats = %+v", st)
	}

	// Trusting the address lets FGLOG002 in.
	if err := svc.SaveSettings(ctx, Settings{Retention: chstore.Retention{RawDays: 30, RollupDays: 60}, TrustedSources: []string{"127.0.0.0/8"}}); err != nil {
		t.Fatal(err)
	}
	col.Handle([]byte("devid=FGLOG002 type=traffic srcip=1.1.1.1"), netip.MustParseAddr("127.0.0.1"))
	if col.Stats().Accepted != uint64(len(lines))+3 {
		t.Fatal("trusted source still rejected")
	}
	if err := svc.SaveSettings(ctx, Settings{Retention: chstore.Retention{RawDays: 30, RollupDays: 60}, TrustedSources: []string{"nonsense"}}); err == nil {
		t.Fatal("bad source accepted")
	}

	r := Range{From: now.Add(-time.Hour), To: now.Add(time.Minute)}
	data, err := svc.Query(ctx, []string{"kpi_overview", "top_sources", "top_threats", "kpi_web", "kpi_vpn", "vpn_failures", "kpi_waf", "waf_sources", "kpi_events", "ts_bandwidth", "top_countries"}, r)
	if err != nil {
		t.Fatal(err)
	}
	get := func(i int) map[string]any { return data[i].Rows[0] }
	if k := get(0); k["traffic"] != float64(7000) || k["sessions"] != float64(3) || k["blocked"] != float64(1) || k["waf"] != float64(2) {
		t.Fatalf("overview = %v", k)
	}
	if s := get(1); s["srcip"] != "10.0.0.1" || s["username"] != "ali" || s["bytes"] != float64(7000) {
		t.Fatalf("top sources = %v", data[1].Rows)
	}
	if th := get(2); th["threat"] != "Log4j.RCE" || th["severity"] != "critical" {
		t.Fatalf("threats = %v", data[2].Rows)
	}
	if w := get(3); w["blocked"] != float64(1) || w["users"] != float64(1) {
		t.Fatalf("web = %v", w)
	}
	if v := get(4); v["connections"] != float64(1) || v["failed"] != float64(1) {
		t.Fatalf("vpn = %v", v)
	}
	if f := get(5); f["username"] != "admin" || f["srcip"] != "6.6.6.6" {
		t.Fatalf("vpn failures = %v", data[5].Rows)
	}
	if w := get(6); w["attacks"] != float64(2) || w["blocked"] != float64(2) {
		t.Fatalf("waf = %v", w)
	}
	if w := get(7); w["srccountry"] != "Germany" {
		t.Fatalf("waf sources = %v", data[7].Rows)
	}
	if e := get(8); e["critical"] != float64(1) {
		t.Fatalf("events = %v", e)
	}
	if n := len(data[9].Rows); n != 2 {
		t.Fatalf("series has %d points", n)
	}
	if c := get(10); c["name"] != "United States" {
		t.Fatalf("countries = %v", data[10].Rows)
	}
	// Device scope.
	other, err := svc.Query(ctx, []string{"kpi_overview"}, Range{From: r.From, To: r.To, DeviceIDs: []int64{ids["127.0.0.3"]}})
	if err != nil {
		t.Fatal(err)
	}
	if k := other[0].Rows[0]; k["sessions"] != float64(0) || k["waf"] != float64(2) {
		t.Fatalf("scoped overview = %v", k)
	}

	// Log view.
	res, err := svc.Search(ctx, SearchQuery{Type: "traffic", Range: r, Filters: []Filter{{Field: "action", Op: "eq", Value: "deny"}, {Field: "policyid", Value: "42"}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 1 || res.Records[0]["dstip"] != "10.20.4.7" {
		t.Fatalf("search = %+v", res)
	}
	res, err = svc.Search(ctx, SearchQuery{Type: "traffic", Range: r, Filters: []Filter{{Field: "user", Op: "contains", Value: "AL"}}})
	if err != nil || res.Total != 2 {
		t.Fatalf("raw field search = %+v %v", res, err)
	}
	res, err = svc.Search(ctx, SearchQuery{Type: "vpn", Range: r})
	if err != nil || res.Total != 2 {
		t.Fatalf("vpn search = %+v %v", res, err)
	}
	if _, err := svc.Search(ctx, SearchQuery{Type: "traffic", Range: r, Filters: []Filter{{Field: "x' OR 1=1", Value: "a"}}}); err == nil {
		t.Fatal("bad field accepted")
	}
	var csvOut bytes.Buffer
	if err := svc.ExportCSV(ctx, SearchQuery{Type: "waf", Range: r}, &csvOut); err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSpace(csvOut.String()), "\n"); len(lines) != 3 || !strings.Contains(lines[1], "dev-127.0.0.3") {
		t.Fatalf("csv = %q", csvOut.String())
	}
}
