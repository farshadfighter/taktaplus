package scripts

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/farshadfighter/taktaplus-lic/pkg/licformat"

	"github.com/farshadfighter/taktaplus/internal/changes"
	"github.com/farshadfighter/taktaplus/internal/device"
	"github.com/farshadfighter/taktaplus/internal/device/devicetest"
	"github.com/farshadfighter/taktaplus/internal/inventory"
	"github.com/farshadfighter/taktaplus/internal/license/licensetest"
	"github.com/farshadfighter/taktaplus/internal/secretbox"
	"github.com/farshadfighter/taktaplus/internal/testdb"
)

func TestIsReadOnly(t *testing.T) {
	for text, want := range map[string]bool{
		"get system status\nshow firewall policy":             true,
		"diagnose sys top 1 1\n# comment\n{{ if .Vars.x }}\n": true,
		"config system global\nset hostname x\nend":           false,
		"get system status\nexecute reboot":                   false,
		"":                                                    false,
	} {
		if IsReadOnly(text) != want {
			t.Errorf("IsReadOnly(%q) != %v", text, want)
		}
	}
}

func TestScripts(t *testing.T) {
	ctx := context.Background()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secretbox.New(key)
	db := testdb.New(t)
	guard := licensetest.Guard(t, map[string]licformat.Date{"FG1": licformat.NewDate(time.Now().Year()+1, 1, 1), "FG2": licformat.NewDate(time.Now().Year()+1, 1, 1)})
	inv := inventory.New(db, box, guard)
	fleet := devicetest.Fleet{
		"a": {Info: device.Info{Serial: "FG1", Hostname: "fw-a"}, ScriptOutput: "FG # get system status\nVersion: x\n"},
		"b": {Info: device.Info{Serial: "FG2", Hostname: "fw-b"}},
	}
	inv.SetDriverFactory(fleet.Factory())
	gid, _ := inv.SaveGroup(ctx, 0, "Branches", "")
	ids := map[string]int64{}
	for _, h := range []string{"a", "b"} {
		in := inventory.Input{Name: h, Kind: device.KindFortiGate, Host: h, Credentials: &inventory.Credentials{SSHUser: "admin", SSHPassword: "pw"}}
		if h == "b" {
			in.GroupID = &gid
		}
		id, _ := inv.Create(ctx, in)
		_, _ = inv.Probe(ctx, id)
		ids[h] = id
	}
	s := New(db, inv)

	if _, err := s.Save(ctx, Script{Name: "bad", Body: "{{ .Vars.x "}, "root"); err == nil {
		t.Fatal("broken template accepted")
	}
	roID, err := s.Save(ctx, Script{Name: "Status", Body: "get system status"}, "root")
	if err != nil {
		t.Fatal(err)
	}
	rwID, _ := s.Save(ctx, Script{Name: "DNS", Body: "config system dns\n  set primary {{ .Vars.dns }}\nend\n# {{ .Device.Hostname }}"}, "root")
	rw, _ := s.Get(ctx, rwID)
	if rw.ReadOnly {
		t.Fatal("config script marked read-only")
	}

	// Variables: global, overridden by group, overridden by device.
	_ = s.SetVariable(ctx, Variable{Scope: "global", Name: "dns", Value: "1.1.1.1"})
	_ = s.SetVariable(ctx, Variable{Scope: "group", ScopeID: gid, Name: "dns", Value: "10.0.0.53"})
	da, _ := inv.Get(ctx, ids["a"])
	db2, _ := inv.Get(ctx, ids["b"])
	out, err := s.Render(ctx, rw.Body, da, nil)
	if err != nil || !strings.Contains(out, "set primary 1.1.1.1") || !strings.Contains(out, "# fw-a") {
		t.Fatalf("render a = %q %v", out, err)
	}
	if out, _ := s.Render(ctx, rw.Body, db2, nil); !strings.Contains(out, "10.0.0.53") {
		t.Fatalf("render b = %q", out)
	}
	_ = s.SetVariable(ctx, Variable{Scope: "device", ScopeID: ids["b"], Name: "dns", Value: "9.9.9.9"})
	if out, _ := s.Render(ctx, rw.Body, db2, map[string]string{}); !strings.Contains(out, "9.9.9.9") {
		t.Fatalf("device override = %q", out)
	}
	if _, err := s.Render(ctx, "set x {{ .Vars.missing }}", da, nil); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing var = %v", err)
	}

	// Read-only runs skip the workflow; config scripts do not.
	runs, err := s.RunReadOnly(ctx, roID, []int64{ids["a"], ids["b"]}, "root")
	if err != nil || len(runs) != 2 || !runs[0].OK || !strings.Contains(runs[0].Output, "Version") {
		t.Fatalf("read-only = %+v %v", runs, err)
	}
	if _, err := s.RunReadOnly(ctx, rwID, []int64{ids["a"]}, "root"); err == nil {
		t.Fatal("config script ran without a change")
	}
	if list, _ := s.Runs(ctx, roID); len(list) != 2 {
		t.Fatalf("runs = %d", len(list))
	}

	// As a change.
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ch := changes.New(db, inv, nil, log)
	ch.Register(Kind, NewRunner(s))
	_ = ch.SetPolicy(ctx, nil, 0)
	spec, _ := json.Marshal(Spec{ScriptID: rwID, Body: rw.Body})
	c, err := ch.Create(ctx, changes.CreateInput{Kind: Kind, Title: "DNS", Spec: spec, DeviceIDs: []int64{ids["a"], ids["b"]}, BackupFirst: new(bool)}, changes.Actor{Username: "op"})
	if err != nil {
		t.Fatal(err)
	}
	ts, _ := ch.Targets(ctx, c.ID)
	if !strings.Contains(ts[1].Preview, "9.9.9.9") {
		t.Fatalf("preview = %q", ts[1].Preview)
	}
	fleet["b"].Set(func(f *devicetest.Fake) {
		f.OnScript = func(_ *devicetest.Fake, script string) (string, error) {
			return "value parse error", device.CLIError{Lines: []string{"value parse error"}}
		}
	})
	_ = ch.Submit(ctx, c.ID, changes.Actor{Username: "op"})
	var wg sync.WaitGroup
	_ = ch.Tick(ctx, &wg)
	wg.Wait()
	c, _ = ch.Get(ctx, c.ID)
	ts, _ = ch.Targets(ctx, c.ID)
	if c.Status != changes.StatusPartial || ts[0].Status != "succeeded" || !strings.Contains(ts[1].Error, "value parse error") {
		t.Fatalf("change = %+v %+v", c, ts)
	}
	if got := fleet["a"].Scripts; len(got) != 2 || !strings.Contains(got[1], "set primary 1.1.1.1") {
		t.Fatalf("scripts sent = %q", got)
	}
}
