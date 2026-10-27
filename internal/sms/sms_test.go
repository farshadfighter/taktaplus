package sms

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/farshadfighter/taktaplus/internal/secretbox"
	"github.com/farshadfighter/taktaplus/internal/settings"
	"github.com/farshadfighter/taktaplus/internal/testdb"
)

func TestNormalizeMobile(t *testing.T) {
	for in, want := range map[string]string{"09121234567": "09121234567", "+98 912 123 4567": "09121234567", "989121234567": "09121234567",
		"9121234567": "09121234567", "00989121234567": "09121234567"} {
		if got, err := NormalizeMobile(in); err != nil || got != want {
			t.Errorf("NormalizeMobile(%q) = %q %v", in, got, err)
		}
	}
	if _, err := NormalizeMobile("02188776655"); err == nil {
		t.Error("landline accepted")
	}
}

func TestParseCode(t *testing.T) {
	for in, want := range map[string]string{"Your FortiToken code is 482913": "482913", "code:12345\n": "12345", "no code here": "", "AuthCode: 0042 expires": "0042"} {
		if got := ParseCode(in); got != want {
			t.Errorf("ParseCode(%q) = %q", in, got)
		}
	}
}

func TestSMSIR(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "KEY" {
			w.WriteHeader(401)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": 0, "message": "invalid api key"})
			return
		}
		switch r.URL.Path {
		case "/v1/send/verify":
			_ = json.NewDecoder(r.Body).Decode(&got)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": 1, "message": "ok", "data": map[string]any{"messageId": 98765, "cost": 1.5}})
		case "/v1/credit":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": 1, "message": "ok", "data": 1234.5})
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secretbox.New(key)
	db := testdb.New(t)
	s := New(db, settings.New(db, box))

	if err := s.Send(ctx, "09121234567", "123456", "test", "u", nil); err != ErrNotConfigured {
		t.Fatalf("unconfigured = %v", err)
	}
	if err := s.SaveConfig(ctx, Config{APIKey: "KEY", TemplateID: 100200, Param: "CODE", BaseURL: srv.URL}); err != nil {
		t.Fatal(err)
	}
	cfg, _ := s.Config(ctx)
	if cfg.Redacted().APIKey != "********" {
		t.Fatal("key not redacted")
	}
	_ = s.SaveConfig(ctx, cfg.Redacted())
	if cfg, _ = s.Config(ctx); cfg.APIKey != "KEY" {
		t.Fatal("redacted save lost the key")
	}
	if err := s.Send(ctx, "+989121234567", "123456", "test", "u", nil); err != nil {
		t.Fatal(err)
	}
	params := got["parameters"].([]any)[0].(map[string]any)
	if got["mobile"] != "09121234567" || got["templateId"] != 100200.0 || params["name"] != "CODE" || params["value"] != "123456" {
		t.Fatalf("request = %v", got)
	}
	if c, err := s.Credit(ctx); err != nil || c != 1234.5 {
		t.Fatalf("credit = %v %v", c, err)
	}
	_ = s.SaveConfig(ctx, Config{APIKey: "WRONG", TemplateID: 1, Param: "CODE", BaseURL: srv.URL})
	if err := s.Send(ctx, "09121234567", "1", "test", "u", nil); err == nil {
		t.Fatal("bad key accepted")
	}
	msgs, _ := s.Messages(ctx, 0)
	if len(msgs) != 3 || msgs[0].Status != "failed" || msgs[1].ProviderID != "98765" {
		t.Fatalf("log = %+v", msgs)
	}
	if n, _ := s.SentSince(ctx, "09121234567", time.Now().Add(-time.Minute)); n != 1 {
		t.Fatalf("sent since = %d", n)
	}
	st, _ := s.Stats(ctx)
	if st["sent_day"] != 1 || st["failed_day"] != 2 {
		t.Fatalf("stats = %v", st)
	}
}
