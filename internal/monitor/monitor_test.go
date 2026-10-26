package monitor

import (
	"context"
	"crypto/rand"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/farshadfighter/taktaplus-lic/pkg/licformat"

	"github.com/farshadfighter/taktaplus/internal/alerts"
	"github.com/farshadfighter/taktaplus/internal/device"
	"github.com/farshadfighter/taktaplus/internal/device/devicetest"
	"github.com/farshadfighter/taktaplus/internal/inventory"
	"github.com/farshadfighter/taktaplus/internal/license/licensetest"
	"github.com/farshadfighter/taktaplus/internal/secretbox"
	"github.com/farshadfighter/taktaplus/internal/testdb"
)

func TestSchedule(t *testing.T) {
	ctx := context.Background()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secretbox.New(key)
	db := testdb.New(t)
	guard := licensetest.Guard(t, map[string]licformat.Date{"FG1": licformat.NewDate(time.Now().Year()+1, 1, 1)})
	inv := inventory.New(db, box, guard)
	fleet := devicetest.Fleet{
		"a": {Info: device.Info{Serial: "FG1"}, HealthV: device.Health{CPU: 33, Disk: -1}, Sigs: []device.Signature{{Name: "ips_db", Version: "1"}}},
		"b": {Info: device.Info{Serial: "FG2"}},
		"c": {Down: true},
	}
	inv.SetDriverFactory(fleet.Factory())
	ids := map[string]int64{}
	for _, h := range []string{"a", "b", "c"} {
		id, err := inv.Create(ctx, inventory.Input{Name: h, Kind: device.KindFortiGate, Host: h, Credentials: &inventory.Credentials{APIToken: "t"}})
		if err != nil {
			t.Fatal(err)
		}
		ids[h] = id
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := New(inv, alerts.New(db, box, inv, guard, log), log, Options{})
	sem := make(chan struct{}, 2)
	var wg sync.WaitGroup

	// First round: nothing has a serial yet, so everything is probed.
	m.schedule(ctx, sem, &wg)
	wg.Wait()
	st, err := inv.Status(ctx, ids["a"])
	if err != nil || st == nil || st.CPU != 33 || len(st.Signatures) != 1 {
		t.Fatalf("a status = %+v, %v", st, err)
	}
	if fleet["b"].Calls["probe"] != 1 || fleet["b"].Calls["health"] != 0 {
		t.Fatalf("unlicensed device polled: %v", fleet["b"].Calls)
	}
	if c, _ := inv.Get(ctx, ids["c"]); c.Status != "offline" {
		t.Fatalf("c = %s", c.Status)
	}

	// Second round right away: nothing is due.
	m.schedule(ctx, sem, &wg)
	wg.Wait()
	if fleet["a"].Calls["health"] != 1 || fleet["c"].Calls["probe"] != 1 {
		t.Fatalf("polled too early: a=%v c=%v", fleet["a"].Calls, fleet["c"].Calls)
	}

	// Health comes due for the licensed device only.
	m.opt.HealthEvery = time.Nanosecond
	m.schedule(ctx, sem, &wg)
	wg.Wait()
	if fleet["a"].Calls["health"] != 2 || fleet["a"].Calls["probe"] != 1 || fleet["b"].Calls["health"] != 0 {
		t.Fatalf("health round: a=%v b=%v", fleet["a"].Calls, fleet["b"].Calls)
	}

	// The refresh button goes through even when nothing is due.
	if err := m.Refresh(ctx, ids["a"]); err != nil || fleet["a"].Calls["probe"] != 2 {
		t.Fatalf("refresh = %v, %v", err, fleet["a"].Calls)
	}
}
