package compliance

import (
	"context"
	"crypto/rand"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/farshadfighter/taktaplus-lic/pkg/licformat"

	"github.com/farshadfighter/taktaplus/internal/backup"
	"github.com/farshadfighter/taktaplus/internal/changes"
	"github.com/farshadfighter/taktaplus/internal/device"
	"github.com/farshadfighter/taktaplus/internal/device/devicetest"
	"github.com/farshadfighter/taktaplus/internal/fortiosconf"
	"github.com/farshadfighter/taktaplus/internal/inventory"
	"github.com/farshadfighter/taktaplus/internal/license/licensetest"
	"github.com/farshadfighter/taktaplus/internal/scripts"
	"github.com/farshadfighter/taktaplus/internal/secretbox"
	"github.com/farshadfighter/taktaplus/internal/testdb"
)

const fgConfig = `config system global
    set hostname "FGT-BR-07"
    set admintimeout 30
end
config system snmp community
    edit 1
        set name "monitoring"
        set status disable
        config hosts
            edit 1
                set ip 0.0.0.0 0.0.0.0
            next
        end
    next
end
config system interface
    edit "wan1"
        set role wan
        set allowaccess ping https http
    next
    edit "port1"
        set allowaccess ping https http
    next
end
config system admin
    edit "admin"
        set password ENC xyz
    next
    edit "ops"
        set trusthost1 10.0.0.0 255.0.0.0
    next
end
config firewall policy
    edit 1
        set srcaddr "all"
        set dstaddr "all"
        set action accept
        set service "ALL"
        set logtraffic disable
    next
    edit 2
        set srcaddr "lan"
        set dstaddr "all"
        set action accept
        set service "HTTPS"
    next
end
`

func TestRuleChecks(t *testing.T) {
	root := fortiosconf.Parse(fgConfig)
	cases := []struct {
		rule  Rule
		count int
		entry string
	}{
		// Forbidden any-any-accept policy.
		{Rule{Section: "firewall policy", Scope: "each", Match: []Cond{{Key: "action", Op: "eq", Value: "accept", Default: "deny"},
			{Key: "srcaddr", Op: "contains", Value: "all"}, {Key: "dstaddr", Op: "contains", Value: "all"}, {Key: "service", Op: "contains", Value: "ALL"}}}, 1, "1"},
		// HTTP on WAN only.
		{Rule{Section: "system interface", Scope: "each", Match: []Cond{{Key: "role", Op: "eq", Value: "wan", Default: "undefined"}},
			Require: []Cond{{Key: "allowaccess", Op: "not_contains", Value: "http"}}}, 1, "wan1"},
		// Missing section: defaults apply.
		{Rule{Section: "system password-policy", Scope: "section", Require: []Cond{{Key: "status", Op: "eq", Value: "enable", Default: "disable"}}}, 1, ""},
		// Defaults that comply.
		{Rule{Section: "system global", Scope: "section", Require: []Cond{{Key: "strong-crypto", Op: "eq", Value: "enable", Default: "enable"}}}, 0, ""},
		{Rule{Section: "system global", Scope: "section", Require: []Cond{{Key: "admintimeout", Op: "lte", Value: "15", Default: "5"}}}, 1, ""},
		{Rule{Section: "system admin", Scope: "each", Require: []Cond{{Key: "trusthost1", Op: "ne", Value: "0.0.0.0 0.0.0.0", Default: "0.0.0.0 0.0.0.0"}}}, 1, "admin"},
		{Rule{Section: "firewall policy", Scope: "each", Match: []Cond{{Key: "action", Op: "eq", Value: "accept"}},
			Require: []Cond{{Key: "logtraffic", Op: "ne", Value: "disable", Default: "utm"}}}, 1, "1"},
	}
	for i, c := range cases {
		c.rule.Name, c.rule.Severity, c.rule.Platform = "r", "high", "fortigate"
		if err := c.rule.validate(); err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		f := c.rule.Check(root)
		if len(f) != c.count || (c.count == 1 && f[0].Entry != c.entry) {
			t.Fatalf("case %d: findings %+v", i, f)
		}
	}
	if strings.Count(cases[4].rule.Check(root)[0].Detail, "30") != 1 {
		t.Fatalf("detail = %q", cases[4].rule.Check(root)[0].Detail)
	}
	bad := Rule{Name: "x", Severity: "high", Platform: "fortigate", Section: "system global", Scope: "section", Require: []Cond{{Key: "a", Op: "gte", Value: "many"}}}
	if bad.validate() == nil {
		t.Fatal("non-numeric gte accepted")
	}
}

func TestEvaluateExceptionsAndFix(t *testing.T) {
	ctx := context.Background()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secretbox.New(key)
	db := testdb.New(t)
	guard := licensetest.Guard(t, map[string]licformat.Date{"FGBR07": licformat.NewDate(time.Now().Year()+1, 1, 1)})
	inv := inventory.New(db, box, guard)
	fake := &devicetest.Fake{Info: device.Info{Serial: "FGBR07"}, Config: []byte(fgConfig)}
	inv.SetDriverFactory(devicetest.Fleet{"br7": fake}.Factory())
	gid, err := inv.SaveGroup(ctx, 0, "شعب", "")
	if err != nil {
		t.Fatal(err)
	}
	dev, err := inv.Create(ctx, inventory.Input{Name: "FGT-BR-07", Kind: device.KindFortiGate, Host: "br7", GroupID: &gid,
		Credentials: &inventory.Credentials{APIToken: "t", SSHUser: "admin", SSHPassword: "p"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inv.Probe(ctx, dev); err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	backups := backup.New(db, box, inv, t.TempDir(), quiet, backup.Options{})
	sc := scripts.New(db, inv)
	ch := changes.New(db, inv, backups, quiet)
	ch.Register(scripts.Kind, scripts.NewRunner(sc))
	svc := New(db, inv, backups, sc, ch, quiet)
	queued := make(chan int64, 4)
	backups.OnStored(func(d, _ int64) { queued <- d })

	// No backup yet.
	r, err := svc.Evaluate(ctx, dev)
	if err != nil || r.Error == "" {
		t.Fatalf("without backup: %+v %v", r, err)
	}
	if _, _, err := backups.Run(ctx, dev, backup.ReasonManual, "t"); err != nil {
		t.Fatal(err)
	}
	if <-queued != dev {
		t.Fatal("backup hook not called")
	}
	if err := sc.SetVariable(ctx, scripts.Variable{Scope: "group", ScopeID: gid, Name: "snmp_host", Value: "10.0.5.20 255.255.255.255"}); err != nil {
		t.Fatal(err)
	}
	tplID, err := svc.SaveTemplate(ctx, Template{Name: "پایه‌ی شعب", Platform: "fortigate", GroupIDs: []int64{gid}, Body: `config system snmp community
    edit 1
        set status enable
        config hosts
            edit 1
                set ip {{ .Vars.snmp_host }}
            next
        end
    next
end`}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveTemplate(ctx, Template{Name: "x", Platform: "fortigate", Body: "set a b"}, "admin"); err == nil {
		t.Fatal("template without a section accepted")
	}
	r, err = svc.Evaluate(ctx, dev)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Drift) != 1 || len(r.Drift[0].Differences) != 2 {
		t.Fatalf("drift = %+v", r.Drift)
	}
	// Built-in rules broken: any-any policy, HTTP on WAN, password policy,
	// admin trusted host, syslog, logtraffic, admintimeout.
	_, _, broken := r.Counts()
	if broken != 7 {
		t.Fatalf("broken rules = %d: %+v", broken, r.Violations)
	}
	wantScore := 100 - (25 + 25 + 15 + 15 + 8 + 3 + 3) - 5
	if r.Score != max(0, wantScore) {
		t.Fatalf("score = %d, want %d", r.Score, max(0, wantScore))
	}
	// Excepting a finding re-scores the device.
	var ref string
	for _, v := range r.Violations {
		if strings.Contains(v.Rule, "Trusted Host") {
			ref = v.ref()
		}
	}
	if err := svc.AddException(ctx, dev, ref, "دسترسی از کنسول", "admin"); err != nil {
		t.Fatal(err)
	}
	r, _ = svc.Result(ctx, dev)
	if _, _, b := r.Counts(); b != 6 {
		t.Fatalf("after exception: %d broken", b)
	}
	// Disabling a built-in rule is allowed; deleting it is not.
	rules, _ := svc.Rules(ctx)
	if err := svc.DeleteRule(ctx, rules[0].ID); err == nil {
		t.Fatal("built-in rule deleted")
	}
	// Fix: a change request with the template, awaiting approval.
	c, err := svc.Fix(ctx, dev, tplID, changes.Actor{UserID: 0, Username: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Kind != scripts.Kind || !strings.Contains(c.Title, "پایه‌ی شعب") {
		t.Fatalf("fix change = %+v", c)
	}
	// Auto-fix opens one change, not one per evaluation.
	tpl, _ := svc.Template(ctx, tplID)
	tpl.AutoFix = true
	if _, err := svc.SaveTemplate(ctx, *tpl, "admin"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := svc.Evaluate(ctx, dev); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	_ = db.QueryRow(ctx, `SELECT count(*) FROM changes WHERE title LIKE '%پایه‌ی شعب%' AND title LIKE '%نسخه 2%'`).Scan(&n)
	if n != 1 {
		t.Fatalf("auto-fix changes = %d", n)
	}
	// When the device matches, drift is gone.
	fake.Set(func(f *devicetest.Fake) {
		f.Config = []byte(strings.Replace(strings.Replace(fgConfig, "set status disable", "set status enable", 1), "set ip 0.0.0.0 0.0.0.0", "set ip 10.0.5.20 255.255.255.255", 1))
	})
	if _, _, err := backups.Run(ctx, dev, backup.ReasonManual, "t"); err != nil {
		t.Fatal(err)
	}
	r, _ = svc.Evaluate(ctx, dev)
	if d, _, _ := r.Counts(); d != 0 {
		t.Fatalf("drift after fix = %+v", r.Drift)
	}
}
