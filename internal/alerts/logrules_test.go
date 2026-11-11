package alerts

import (
	"context"
	"crypto/rand"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
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

type fakeLogs struct {
	last map[int64]time.Time
	hits map[string]map[int64][]LogHit
}

func (f *fakeLogs) LastSeen(context.Context) (map[int64]time.Time, error) { return f.last, nil }
func (f *fakeLogs) Hits(_ context.Context, kind string, _ time.Duration, _ int) (map[int64][]LogHit, error) {
	return f.hits[kind], nil
}

func newService(t *testing.T) (*Service, int64) {
	ctx := context.Background()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secretbox.New(key)
	db := testdb.New(t)
	guard := licensetest.Guard(t, map[string]licformat.Date{"FG1": licformat.NewDate(time.Now().Year()+1, 1, 1)})
	inv := inventory.New(db, box, guard)
	inv.SetDriverFactory(devicetest.Fleet{"a": {Info: device.Info{Serial: "FG1"}}}.Factory())
	id, _ := inv.Create(ctx, inventory.Input{Name: "FGT-HQ", Kind: device.KindFortiGate, Host: "a", Credentials: &inventory.Credentials{APIToken: "t"}})
	if _, err := inv.Probe(ctx, id); err != nil {
		t.Fatal(err)
	}
	return New(db, box, inv, guard, slog.New(slog.NewTextHandler(io.Discard, nil))), id
}

func TestLogRules(t *testing.T) {
	ctx := context.Background()
	s, dev := newService(t)
	out := &outbox{}
	s.send = out.send
	rules, _ := s.ListRules(ctx)
	for _, r := range rules {
		isLog := r.Kind == KindLogSilence || r.Kind == KindLogAttacks || r.Kind == KindVPNFailures
		r.Enabled = isLog
		r.ChannelIDs = []int64{}
		if _, err := s.SaveRule(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	ch, _ := s.SaveChannel(ctx, Channel{Name: "ops", Kind: ChannelWebhook, Config: ChannelConfig{URL: "https://hooks.example/x"}, Enabled: true})
	rules, _ = s.ListRules(ctx)
	for _, r := range rules {
		r.ChannelIDs = []int64{ch}
		_, _ = s.SaveRule(ctx, r)
	}
	// Without a log source the log rules do nothing.
	if err := s.Evaluate(ctx); err != nil || len(out.take()) != 0 {
		t.Fatal("log rules ran without a log store")
	}
	now := time.Now()
	s.now = func() time.Time { return now }
	fl := &fakeLogs{last: map[int64]time.Time{dev: now.Add(-90 * time.Minute)}, hits: map[string]map[int64][]LogHit{
		KindLogAttacks:  {dev: {{Subject: "45.155.205.233", Count: 140, Detail: "SQL Injection"}}},
		KindVPNFailures: {dev: {{Subject: "admin @ 6.6.6.6", Count: 12, Detail: "SSL VPN login fail"}}},
	}}
	s.SetLogSource(fl)
	if err := s.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	msgs := out.take()
	if len(msgs) != 3 {
		t.Fatalf("messages = %+v", msgs)
	}
	text := ""
	for _, m := range msgs {
		text += m.Text + "\n"
	}
	for _, want := range []string{"no logs received for 90 minutes", "140 attacks from 45.155.205.233 in 10 minutes (SQL Injection)", "12 failed VPN logins by admin @ 6.6.6.6 in 15 minutes"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %s", want, text)
		}
	}
	// The burst ends and logs arrive again: everything resolves.
	fl.last[dev] = now
	fl.hits = map[string]map[int64][]LogHit{}
	if err := s.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	if msgs := out.take(); len(msgs) != 3 || msgs[0].Event != "resolved" {
		t.Fatalf("resolve = %+v", msgs)
	}
	if _, err := s.SaveRule(ctx, Rule{Name: "x", Kind: KindLogAttacks, Threshold: 5, WindowMinutes: 0}); err == nil {
		t.Fatal("attack rule without a window accepted")
	}
}

func TestMessengers(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t)
	type call struct{ path, chat, text, file string }
	var mu sync.Mutex
	var calls []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := call{path: r.URL.Path}
		ct, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if ct == "multipart/form-data" {
			mr := multipart.NewReader(r.Body, params["boundary"])
			for {
				p, err := mr.NextPart()
				if err != nil {
					break
				}
				b, _ := io.ReadAll(p)
				switch p.FormName() {
				case "chat_id":
					c.chat = string(b)
				case "document", "file":
					c.file = p.FileName() + ":" + string(b)
				}
			}
		} else {
			_ = r.ParseForm()
			c.chat, c.text = r.Form.Get("chat_id"), r.Form.Get("text")
		}
		mu.Lock()
		calls = append(calls, c)
		mu.Unlock()
		if strings.Contains(r.URL.Path, "bad-token") {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"ok":false,"description":"Unauthorized"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer srv.Close()

	bale, err := s.SaveChannel(ctx, Channel{Name: "bale", Kind: ChannelBale, Enabled: true, Config: ChannelConfig{BotToken: "123:abc", ChatID: "@soc", APIBase: srv.URL}})
	if err != nil {
		t.Fatal(err)
	}
	eitaa, _ := s.SaveChannel(ctx, Channel{Name: "eitaa", Kind: ChannelEitaa, Enabled: true, Config: ChannelConfig{BotToken: "bot99", ChatID: "soc_channel", APIBase: srv.URL}})
	if _, err := s.SaveChannel(ctx, Channel{Name: "x", Kind: ChannelBale, Config: ChannelConfig{ChatID: "1"}}); err == nil {
		t.Fatal("channel without a token accepted")
	}
	// Tokens are redacted and kept on update.
	list, _ := s.ListChannels(ctx)
	for _, c := range list {
		if c.Kind == ChannelBale && c.Config.BotToken != "********" {
			t.Fatal("token not redacted")
		}
	}
	if _, err := s.SaveChannel(ctx, Channel{ID: bale, Name: "bale", Kind: ChannelBale, Enabled: true, Config: ChannelConfig{BotToken: "********", ChatID: "@soc", APIBase: srv.URL}}); err != nil {
		t.Fatal(err)
	}
	if err := s.TestChannel(ctx, bale); err != nil {
		t.Fatal(err)
	}
	files := []Attachment{{Name: "report.pdf", ContentType: "application/pdf", Data: []byte("%PDF")}}
	if err := s.Deliver(ctx, eitaa, nil, "گزارش", "بدنه", files); err != nil {
		t.Fatal(err)
	}
	want := []call{
		{path: "/bot123:abc/sendMessage", chat: "@soc"},
		{path: "/bot99/sendMessage", chat: "soc_channel", text: "گزارش\n\nبدنه"},
		{path: "/bot99/sendFile", chat: "soc_channel", file: "report.pdf:%PDF"},
	}
	if len(calls) != 3 {
		t.Fatalf("calls = %+v", calls)
	}
	for i, w := range want {
		c := calls[i]
		if c.path != w.path || c.chat != w.chat || (w.text != "" && c.text != w.text) || c.file != w.file {
			t.Fatalf("call %d = %+v, want %+v", i, c, w)
		}
	}
	if !strings.Contains(calls[0].text, "Test") {
		t.Fatalf("test message = %q", calls[0].text)
	}
	// Errors carry the API's description but never the token.
	bad, _ := s.SaveChannel(ctx, Channel{Name: "bad", Kind: ChannelBale, Enabled: true, Config: ChannelConfig{BotToken: "bad-token", ChatID: "1", APIBase: srv.URL}})
	err = s.TestChannel(ctx, bad)
	if err == nil || !strings.Contains(err.Error(), "Unauthorized") || strings.Contains(err.Error(), "bad-token") {
		t.Fatalf("error = %v", err)
	}
}
