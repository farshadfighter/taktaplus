package backup

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/farshadfighter/taktaplus-lic/pkg/licformat"

	"github.com/farshadfighter/taktaplus/internal/device"
	"github.com/farshadfighter/taktaplus/internal/device/devicetest"
	"github.com/farshadfighter/taktaplus/internal/inventory"
	"github.com/farshadfighter/taktaplus/internal/license/licensetest"
	"github.com/farshadfighter/taktaplus/internal/secretbox"
	"github.com/farshadfighter/taktaplus/internal/testdb"
)

type env struct {
	s     *Service
	inv   *inventory.Service
	fleet devicetest.Fleet
	ids   map[string]int64
	dir   string
}

func setup(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secretbox.New(key)
	db := testdb.New(t)
	guard := licensetest.Guard(t, map[string]licformat.Date{"FG1": licformat.NewDate(time.Now().Year()+1, 1, 1)})
	inv := inventory.New(db, box, guard)
	fleet := devicetest.Fleet{
		"a": {Info: device.Info{Serial: "FG1", Firmware: "7.4.4"}, Config: []byte(cfgA)},
		"b": {Info: device.Info{Serial: "FG404"}, Config: []byte(cfgA)},
	}
	inv.SetDriverFactory(fleet.Factory())
	ids := map[string]int64{}
	for _, h := range []string{"a", "b"} {
		id, err := inv.Create(ctx, inventory.Input{Name: h, Kind: device.KindFortiGate, Host: h, Credentials: &inventory.Credentials{APIToken: "t"}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := inv.Probe(ctx, id); err != nil {
			t.Fatal(err)
		}
		ids[h] = id
	}
	dir := t.TempDir()
	s := New(db, box, inv, dir, slog.New(slog.NewTextHandler(io.Discard, nil)),
		Options{RebootWait: time.Millisecond, RebootPoll: 5 * time.Millisecond, RebootLimit: 200 * time.Millisecond})
	return &env{s: s, inv: inv, fleet: fleet, ids: ids, dir: dir}
}

func TestBackupRunDedupAndContent(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	a := e.ids["a"]

	b1, unchanged, err := e.s.Run(ctx, a, ReasonManual, "root")
	if err != nil || unchanged || b1.Status != "ok" || b1.Firmware != "7.4.4" || b1.Size != int64(len(cfgA)) {
		t.Fatalf("first = %+v %v %v", b1, unchanged, err)
	}
	// The file on disk is encrypted.
	raw, _ := os.ReadFile(filepath.Join(e.dir, b1.path))
	if len(raw) == 0 || strings.Contains(string(raw), "FG-HQ") {
		t.Fatal("backup file is not encrypted")
	}
	// Only a volatile line changed: no new backup.
	e.fleet["a"].Set(func(f *devicetest.Fake) {
		f.Config = []byte(strings.Replace(cfgA, "#conf_file_ver=111", "#conf_file_ver=999", 1))
	})
	b2, unchanged, err := e.s.Run(ctx, a, ReasonScheduled, "policy")
	if err != nil || !unchanged || b2.ID != b1.ID {
		t.Fatalf("dedup = %+v %v %v", b2, unchanged, err)
	}
	changed := strings.Replace(cfgA, "admintimeout 5", "admintimeout 30", 1)
	e.fleet["a"].Set(func(f *devicetest.Fake) { f.Config = []byte(changed) })
	b3, unchanged, err := e.s.Run(ctx, a, ReasonManual, "root")
	if err != nil || unchanged || b3.ID == b1.ID {
		t.Fatalf("changed = %+v %v %v", b3, unchanged, err)
	}
	cfg, _, err := e.s.Content(ctx, b3.ID)
	if err != nil || string(cfg) != changed {
		t.Fatalf("content = %v", err)
	}
	diff, err := e.s.DiffBackups(ctx, b1.ID, b3.ID)
	if err != nil || len(diff) != 1 || diff[0].Key != "admintimeout" {
		t.Fatalf("diff = %+v %v", diff, err)
	}

	// Failure is recorded.
	e.fleet["a"].Set(func(f *devicetest.Fake) { f.Down = true })
	if _, _, err := e.s.Run(ctx, a, ReasonManual, "root"); err == nil {
		t.Fatal("backup of a down device succeeded")
	}
	list, _ := e.s.List(ctx, a, 0)
	if len(list) != 3 || list[0].Status != "failed" || list[0].Error == "" {
		t.Fatalf("list = %+v", list)
	}

	// Unlicensed devices are not backed up.
	if _, _, err := e.s.Run(ctx, e.ids["b"], ReasonManual, "root"); !errors.Is(err, ErrNotLicensed) {
		t.Fatalf("unlicensed = %v", err)
	}

	// Locked backups cannot be deleted.
	if err := e.s.SetLock(ctx, b1.ID, true, "golden"); err != nil {
		t.Fatal(err)
	}
	var ie InputError
	if err := e.s.Delete(ctx, b1.ID); !errors.As(err, &ie) {
		t.Fatalf("delete locked = %v", err)
	}
	if err := e.s.Delete(ctx, b3.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.dir, b3.path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("file not removed")
	}
}

func TestRetentionAndSchedule(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	a := e.ids["a"]
	policies, _ := e.s.ListPolicies(ctx)
	if len(policies) != 1 || policies[0].NextRunAt == nil {
		t.Fatalf("default policy = %+v", policies)
	}
	if _, err := e.s.SavePolicy(ctx, Policy{Name: "bad", Schedule: "every day"}); err == nil {
		t.Fatal("bad cron accepted")
	}
	// Device-specific policy: keep 2.
	pid, err := e.s.SavePolicy(ctx, Policy{Name: "fg", DeviceID: &a, Schedule: "*/5 * * * *", KeepCount: 2, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	var first *Backup
	for i := range 4 {
		e.fleet["a"].Set(func(f *devicetest.Fake) { f.Config = []byte(cfgA + "# rev " + string(rune('a'+i)) + "\n") })
		b, _, err := e.s.Run(ctx, a, ReasonManual, "root")
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = b
			_ = e.s.SetLock(ctx, b.ID, true, "")
		}
	}
	list, _ := e.s.List(ctx, a, 0)
	// Two newest plus the locked first one.
	if len(list) != 3 || list[2].ID != first.ID {
		t.Fatalf("after retention = %d", len(list))
	}

	// Schedule: the policy's next run is due 5+ minutes after creation.
	now := time.Now().Add(6 * time.Minute)
	e.s.now = func() time.Time { return now }
	e.fleet["a"].Set(func(f *devicetest.Fake) { f.Config = []byte(cfgA + "# scheduled\n") })
	calls := e.fleet["a"].Calls["backup"]
	if err := e.s.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	if e.fleet["a"].Calls["backup"] != calls+1 {
		t.Fatalf("scheduled backup not run: %v", e.fleet["a"].Calls)
	}
	if err := e.s.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	if e.fleet["a"].Calls["backup"] != calls+1 {
		t.Fatal("policy ran twice in the same slot")
	}
	if err := e.s.DeletePolicy(ctx, pid); err != nil {
		t.Fatal(err)
	}
}

func TestRestore(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	a := e.ids["a"]
	old, _, err := e.s.Run(ctx, a, ReasonManual, "root")
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(cfgA, "admintimeout 5", "admintimeout 30", 1)
	e.fleet["a"].Set(func(f *devicetest.Fake) { f.Config = []byte(changed) })

	preview, err := e.s.DiffCurrent(ctx, old.ID)
	if err != nil || len(preview) != 1 || !strings.Contains(preview[0].New, "admintimeout 5") {
		t.Fatalf("preview = %+v %v", preview, err)
	}

	job, err := e.s.Restore(ctx, old.ID, "root")
	if err != nil || job.Status != "waiting" || job.PreBackupID == nil {
		t.Fatalf("restore = %+v %v", job, err)
	}
	if _, err := e.s.Restore(ctx, old.ID, "root"); !errors.Is(err, ErrRestoreRunning) {
		t.Fatalf("second restore = %v", err)
	}
	e.s.Wait()
	job, _ = e.s.GetRestore(ctx, job.ID)
	if job.Status != "succeeded" {
		t.Fatalf("job = %+v", job)
	}
	if got := e.fleet["a"].Restored; len(got) != 1 || string(got[0]) != cfgA {
		t.Fatal("device did not receive the backup")
	}
	pre, _, _ := e.s.Content(ctx, *job.PreBackupID)
	if string(pre) != changed {
		t.Fatal("safety backup does not hold the running configuration")
	}

	// A device that never returns fails the job. The first check waits long
	// enough for the test to take the device down.
	e.s.opt.RebootWait = 100 * time.Millisecond
	job, err = e.s.Restore(ctx, old.ID, "root")
	if err != nil {
		t.Fatal(err)
	}
	e.fleet["a"].Set(func(f *devicetest.Fake) { f.Down = true })
	e.s.Wait()
	job, _ = e.s.GetRestore(ctx, job.ID)
	if job.Status != "failed" || !strings.Contains(job.Error, "did not come back") {
		t.Fatalf("job = %+v", job)
	}
	jobs, _ := e.s.ListRestores(ctx, a)
	if len(jobs) != 2 {
		t.Fatalf("jobs = %d", len(jobs))
	}
}
