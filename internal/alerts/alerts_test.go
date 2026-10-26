package alerts

import (
	"context"
	"crypto/rand"
	"io"
	"log/slog"
	"sync"
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

type outbox struct {
	mu   sync.Mutex
	msgs []Message
}

func (o *outbox) send(_ context.Context, _ string, _ ChannelConfig, m Message) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.msgs = append(o.msgs, m)
	return nil
}

func (o *outbox) take() []Message {
	o.mu.Lock()
	defer o.mu.Unlock()
	m := o.msgs
	o.msgs = nil
	return m
}

func TestEvaluate(t *testing.T) {
	ctx := context.Background()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secretbox.New(key)
	db := testdb.New(t)
	in10 := time.Now().AddDate(0, 0, 10)
	soon := licformat.NewDate(in10.Year(), in10.Month(), in10.Day())
	guard := licensetest.Guard(t, map[string]licformat.Date{"FG1": licformat.NewDate(time.Now().Year()+1, 1, 1), "FG2": soon})
	inv := inventory.New(db, box, guard)
	fleet := devicetest.Fleet{"a": {Info: device.Info{Serial: "FG1"}}, "b": {Info: device.Info{Serial: "FG2"}}, "c": {Info: device.Info{Serial: "FG404"}}}
	inv.SetDriverFactory(fleet.Factory())
	ids := map[string]int64{}
	for _, h := range []string{"a", "b", "c"} {
		id, err := inv.Create(ctx, inventory.Input{Name: "dev-" + h, Kind: device.KindFortiGate, Host: h, Credentials: &inventory.Credentials{APIToken: "t"}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := inv.Probe(ctx, id); err != nil {
			t.Fatal(err)
		}
		ids[h] = id
	}

	s := New(db, box, inv, guard, slog.New(slog.NewTextHandler(io.Discard, nil)))
	out := &outbox{}
	s.send = out.send
	now := time.Now()
	s.now = func() time.Time { return now }

	chID, err := s.SaveChannel(ctx, Channel{Name: "ops", Kind: ChannelWebhook, Config: ChannelConfig{URL: "https://hooks.example/x", Secret: "s3cret"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveChannel(ctx, Channel{Name: "bad", Kind: ChannelEmail, Config: ChannelConfig{SMTPHost: "mx"}}); err == nil {
		t.Fatal("email channel without addresses accepted")
	}
	chans, _ := s.ListChannels(ctx)
	if chans[0].Config.Secret != "********" {
		t.Fatalf("secret not redacted: %+v", chans[0])
	}
	// Saving the redacted form keeps the secret.
	if _, err := s.SaveChannel(ctx, chans[0]); err != nil {
		t.Fatal(err)
	}
	stored, _ := s.loadChannels(ctx, "")
	if stored[0].Config.Secret != "s3cret" {
		t.Fatalf("secret lost: %+v", stored[0].Config)
	}

	rules, _ := s.ListRules(ctx)
	for _, r := range rules {
		r.ChannelIDs = []int64{chID}
		if _, err := s.SaveRule(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	// dev-a: high CPU (rule needs 5 minutes) and an expiring contract.
	exp := now.Add(5 * 24 * time.Hour)
	_ = inv.SaveHealth(ctx, ids["a"], device.Health{CPU: 97, Mem: 10, Disk: -1})
	_ = inv.SaveSignatures(ctx, ids["a"], nil, []device.Contract{{Name: "ips", Status: "licensed", Expires: &exp}})
	// dev-b: unreachable; license expires in 10 days.
	_ = inv.MarkUnreachable(ctx, ids["b"], devicetest.ErrDown)

	if err := s.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	msgs := out.take()
	got := map[string]bool{}
	for _, m := range msgs {
		got[m.Device+"/"+m.Rule] = true
	}
	// Offline needs 3 minutes and CPU 5, so only the immediate rules fire.
	if len(msgs) != 2 || !got["dev-a/FortiGuard contract expiring"] || !got["dev-b/TaktaPlus license expiring"] {
		t.Fatalf("first pass = %+v", msgs)
	}

	now = now.Add(6 * time.Minute)
	if err := s.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	msgs = out.take()
	got = map[string]bool{}
	for _, m := range msgs {
		got[m.Device+"/"+m.Rule] = true
	}
	// dev-a's CPU sample is now 6 minutes old but still fresh (< 10 min).
	if len(msgs) != 2 || !got["dev-a/High CPU"] || !got["dev-b/Device unreachable"] {
		t.Fatalf("second pass = %+v", msgs)
	}
	counts, _ := s.OpenCounts(ctx)
	if counts["critical"] != 1 || counts["warning"] != 3 {
		t.Fatalf("counts = %v", counts)
	}

	// Nothing new: no notifications (repeat is off).
	if err := s.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	if m := out.take(); len(m) != 0 {
		t.Fatalf("repeat = %+v", m)
	}

	// dev-b comes back: resolved notification.
	_ = inv.MarkSeen(ctx, ids["b"])
	if err := s.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	msgs = out.take()
	if len(msgs) != 1 || msgs[0].Event != "resolved" || msgs[0].Device != "dev-b" {
		t.Fatalf("resolve = %+v", msgs)
	}

	list, err := s.List(ctx, "open", 0, 0)
	if err != nil || len(list) != 3 {
		t.Fatalf("open alerts = %d, %v", len(list), err)
	}
	if err := s.Ack(ctx, list[0].ID, "root"); err != nil {
		t.Fatal(err)
	}
	// dev-c is not licensed: no alerts at all.
	if l, _ := s.List(ctx, "", ids["c"], 0); len(l) != 0 {
		t.Fatalf("unlicensed device alerts = %+v", l)
	}
	if err := s.DeleteChannel(ctx, chID); err != nil {
		t.Fatal(err)
	}
	rules, _ = s.ListRules(ctx)
	if len(rules[0].ChannelIDs) != 0 {
		t.Fatalf("channel still linked: %+v", rules[0])
	}
}
