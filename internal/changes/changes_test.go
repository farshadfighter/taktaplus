package changes

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/farshadfighter/taktaplus-lic/pkg/licformat"

	"github.com/farshadfighter/taktaplus/internal/auth"
	"github.com/farshadfighter/taktaplus/internal/backup"
	"github.com/farshadfighter/taktaplus/internal/device"
	"github.com/farshadfighter/taktaplus/internal/device/devicetest"
	"github.com/farshadfighter/taktaplus/internal/inventory"
	"github.com/farshadfighter/taktaplus/internal/license/licensetest"
	"github.com/farshadfighter/taktaplus/internal/secretbox"
	"github.com/farshadfighter/taktaplus/internal/testdb"
)

// echoRunner fails on devices whose name contains "bad".
type echoRunner struct {
	mu   sync.Mutex
	runs []string
}

func (r *echoRunner) Preview(_ context.Context, spec json.RawMessage, d *inventory.Device) (string, error) {
	var s struct{ Cmd string }
	_ = json.Unmarshal(spec, &s)
	if s.Cmd == "" {
		return "", InputError{"command is required"}
	}
	return s.Cmd + " on " + d.Name, nil
}

func (r *echoRunner) Run(_ context.Context, _ *Change, d *inventory.Device, out io.Writer) error {
	r.mu.Lock()
	r.runs = append(r.runs, d.Name)
	r.mu.Unlock()
	fmt.Fprintf(out, "ran on %s\n", d.Name)
	if strings.Contains(d.Name, "bad") {
		return errors.New("command failed")
	}
	return nil
}

type env struct {
	s     *Service
	r     *echoRunner
	ids   map[string]int64
	users map[string]int64
	auth  *auth.Service
}

func setup(t *testing.T, names ...string) *env {
	t.Helper()
	ctx := context.Background()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secretbox.New(key)
	db := testdb.New(t)
	serials := map[string]licformat.Date{}
	fleet := devicetest.Fleet{}
	for i, n := range names {
		serial := fmt.Sprintf("FG%04d", i)
		serials[serial] = licformat.NewDate(time.Now().Year()+1, 1, 1)
		fleet[n] = &devicetest.Fake{Info: device.Info{Serial: serial}, Config: []byte("#config-version=x\nconfig system global\nend\n")}
	}
	guard := licensetest.Guard(t, serials)
	inv := inventory.New(db, box, guard)
	inv.SetDriverFactory(fleet.Factory())
	ids := map[string]int64{}
	for _, n := range names {
		id, err := inv.Create(ctx, inventory.Input{Name: n, Kind: device.KindFortiGate, Host: n, Credentials: &inventory.Credentials{APIToken: "t"}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := inv.Probe(ctx, id); err != nil {
			t.Fatal(err)
		}
		ids[n] = id
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	bk := backup.New(db, box, inv, t.TempDir(), log, backup.Options{})
	s := New(db, inv, bk, log)
	r := &echoRunner{}
	s.Register("echo", r)
	as := auth.NewService(db, box)
	users := map[string]int64{}
	for _, u := range []string{"op", "ap1", "ap2"} {
		usr, err := as.CreateUser(ctx, u, u, "", auth.RoleAdmin, "correct horse battery")
		if err != nil {
			t.Fatal(err)
		}
		users[u] = usr.ID
	}
	return &env{s: s, r: r, ids: ids, users: users, auth: as}
}

func (e *env) actor(name string) Actor { return Actor{UserID: e.users[name], Username: name} }

func (e *env) runAll(t *testing.T) {
	var wg sync.WaitGroup
	if err := e.s.Tick(context.Background(), &wg); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
}

func TestWorkflowAndExecution(t *testing.T) {
	e := setup(t, "fg1", "fg2", "fg-bad", "fg4")
	ctx := context.Background()
	spec := json.RawMessage(`{"cmd":"get system status"}`)

	if _, err := e.s.Create(ctx, CreateInput{Kind: "echo", Title: "x", Spec: json.RawMessage(`{}`), DeviceIDs: []int64{e.ids["fg1"]}}, e.actor("op")); !isInput(err) {
		t.Fatalf("invalid spec = %v", err)
	}
	if _, err := e.s.Create(ctx, CreateInput{Kind: "echo", Title: "x", Spec: spec, DeviceIDs: []int64{e.ids["fg1"]}, RunMode: "now"}, e.actor("op")); !isInput(err) {
		t.Fatalf("run now without reason = %v", err)
	}

	// Default policy needs one approval.
	c, err := e.s.Create(ctx, CreateInput{Kind: "echo", Title: "Status", Spec: spec, DeviceIDs: []int64{e.ids["fg1"], e.ids["fg2"], e.ids["fg-bad"], e.ids["fg4"]},
		Concurrency: 2, Canary: 1, MaxFailures: 5}, e.actor("op"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != StatusDraft || c.Required != 1 || c.Targets != 4 {
		t.Fatalf("created = %+v", c)
	}
	targets, _ := e.s.Targets(ctx, c.ID)
	if targets[0].Preview != "get system status on fg1" {
		t.Fatalf("preview = %q", targets[0].Preview)
	}
	if err := e.s.Decide(ctx, c.ID, e.actor("ap1"), true, ""); !errors.Is(err, ErrState) {
		t.Fatalf("approve draft = %v", err)
	}
	if err := e.s.Submit(ctx, c.ID, e.actor("op")); err != nil {
		t.Fatal(err)
	}
	if err := e.s.Decide(ctx, c.ID, e.actor("op"), true, ""); !errors.Is(err, ErrOwnChange) {
		t.Fatalf("self approval = %v", err)
	}
	// Not approved yet: nothing runs.
	e.runAll(t)
	if len(e.r.runs) != 0 {
		t.Fatal("ran before approval")
	}
	if err := e.s.Decide(ctx, c.ID, e.actor("ap1"), true, "ok"); err != nil {
		t.Fatal(err)
	}
	c, _ = e.s.Get(ctx, c.ID)
	if c.Status != StatusApproved || c.NextRunAt == nil {
		t.Fatalf("approved = %+v", c)
	}
	e.runAll(t)
	c, _ = e.s.Get(ctx, c.ID)
	if c.Status != StatusPartial || c.Succeeded != 3 || c.Failed != 1 {
		t.Fatalf("finished = %+v", c)
	}
	targets, _ = e.s.Targets(ctx, c.ID)
	if targets[0].PreBackupID == nil || !strings.Contains(targets[0].Output, "ran on fg1") || targets[2].Error != "command failed" {
		t.Fatalf("targets = %+v", targets)
	}
	if e.r.runs[0] != "fg1" {
		t.Fatalf("canary did not run first: %v", e.r.runs)
	}

	// A failing canary stops the rest.
	e.r.runs = nil
	c2, _ := e.s.Create(ctx, CreateInput{Kind: "echo", Title: "Canary", Spec: spec, DeviceIDs: []int64{e.ids["fg-bad"], e.ids["fg1"]}, Canary: 1, RunMode: "now",
		UrgentReason: "incident"}, e.actor("op"))
	_ = e.s.Submit(ctx, c2.ID, e.actor("op"))
	_ = e.s.Decide(ctx, c2.ID, e.actor("ap1"), true, "")
	e.runAll(t)
	c2, _ = e.s.Get(ctx, c2.ID)
	ts, _ := e.s.Targets(ctx, c2.ID)
	if c2.Status != StatusFailed || len(e.r.runs) != 1 || ts[1].Status != "skipped" {
		t.Fatalf("canary failure = %+v %v %+v", c2, e.r.runs, ts)
	}

	// Rejection ends a change.
	c3, _ := e.s.Create(ctx, CreateInput{Kind: "echo", Title: "Rejected", Spec: spec, DeviceIDs: []int64{e.ids["fg1"]}}, e.actor("op"))
	_ = e.s.Submit(ctx, c3.ID, e.actor("op"))
	if err := e.s.Decide(ctx, c3.ID, e.actor("ap2"), false, "not now"); err != nil {
		t.Fatal(err)
	}
	c3, _ = e.s.Get(ctx, c3.ID)
	appr, _ := e.s.Approvals(ctx, c3.ID)
	if c3.Status != StatusRejected || len(appr) != 1 || appr[0].Comment != "not now" {
		t.Fatalf("rejected = %+v %+v", c3, appr)
	}
	if err := e.s.Cancel(ctx, c3.ID, e.actor("op")); !errors.Is(err, ErrState) {
		t.Fatalf("cancel rejected = %v", err)
	}
}

func TestPoliciesAndWindows(t *testing.T) {
	e := setup(t, "fg1")
	ctx := context.Background()
	spec := json.RawMessage(`{"cmd":"x"}`)

	// No approvals needed: submit approves.
	if err := e.s.SetPolicy(ctx, nil, 0); err != nil {
		t.Fatal(err)
	}
	c, _ := e.s.Create(ctx, CreateInput{Kind: "echo", Title: "w", Spec: spec, DeviceIDs: []int64{e.ids["fg1"]}}, e.actor("op"))
	if err := e.s.Submit(ctx, c.ID, e.actor("op")); err != nil {
		t.Fatal(err)
	}
	// A window that is closed now holds the change.
	now := time.Now()
	closed := now.Add(3 * time.Hour)
	if _, err := e.s.SaveWindow(ctx, Window{Name: "Night", Weekdays: []int{0, 1, 2, 3, 4, 5, 6}, StartTime: closed.Format("15:04"), Minutes: 60, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	e.runAll(t)
	c, _ = e.s.Get(ctx, c.ID)
	if c.Status != StatusApproved || c.NextRunAt == nil || c.NextRunAt.Sub(now) < 2*time.Hour {
		t.Fatalf("held = %+v", c)
	}
	// Inside the window it runs.
	e.s.now = func() time.Time { return closed.Add(10 * time.Minute) }
	e.runAll(t)
	c, _ = e.s.Get(ctx, c.ID)
	if c.Status != StatusSucceeded {
		t.Fatalf("in window = %+v", c)
	}
	// Run now skips the window.
	e.s.now = time.Now
	c2, _ := e.s.Create(ctx, CreateInput{Kind: "echo", Title: "u", Spec: spec, DeviceIDs: []int64{e.ids["fg1"]}}, e.actor("op"))
	_ = e.s.Submit(ctx, c2.ID, e.actor("op"))
	if err := e.s.RunNow(ctx, c2.ID, e.actor("ap1"), "outage"); err != nil {
		t.Fatal(err)
	}
	e.runAll(t)
	if c2, _ = e.s.Get(ctx, c2.ID); c2.Status != StatusSucceeded {
		t.Fatalf("run now = %+v", c2)
	}
	if _, err := e.s.SaveWindow(ctx, Window{Name: "bad", Weekdays: []int{7}, StartTime: "25:00", Minutes: 5}); !isInput(err) {
		t.Fatalf("bad window = %v", err)
	}
}

func TestWindowMath(t *testing.T) {
	loc := time.UTC
	w := Window{Weekdays: []int{4}, StartTime: "22:00", Minutes: 240, Enabled: true} // Thursday 22:00–02:00
	thu := time.Date(2026, 10, 29, 23, 0, 0, 0, loc)                                 // a Thursday
	if _, ok := w.openAt(thu); !ok {
		t.Fatal("Thursday 23:00 should be open")
	}
	if _, ok := w.openAt(thu.Add(2 * time.Hour)); !ok {
		t.Fatal("Friday 01:00 is still inside Thursday's window")
	}
	if _, ok := w.openAt(thu.Add(4 * time.Hour)); ok {
		t.Fatal("Friday 03:00 should be closed")
	}
	n, ok := w.next(thu.Add(4 * time.Hour))
	if !ok || n.Weekday() != time.Thursday || n.Sub(thu) < 6*24*time.Hour {
		t.Fatalf("next = %v", n)
	}
	g := int64(5)
	if !inWindow([]Window{{GroupID: &g, Weekdays: []int{4}, StartTime: "22:00", Minutes: 60, Enabled: true}}, nil, thu.Add(-5*time.Hour)) {
		t.Fatal("a device not covered by any window may run")
	}
}

func isInput(err error) bool {
	var ie InputError
	return errors.As(err, &ie)
}
