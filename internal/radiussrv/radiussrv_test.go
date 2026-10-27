package radiussrv

import (
	"context"
	"crypto/rand"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"layeh.com/radius"
	"layeh.com/radius/rfc2865"

	"github.com/farshadfighter/taktaplus-lic/pkg/licformat"

	"github.com/farshadfighter/taktaplus/internal/device"
	"github.com/farshadfighter/taktaplus/internal/device/devicetest"
	"github.com/farshadfighter/taktaplus/internal/inventory"
	"github.com/farshadfighter/taktaplus/internal/license/licensetest"
	"github.com/farshadfighter/taktaplus/internal/otp"
	"github.com/farshadfighter/taktaplus/internal/secretbox"
	"github.com/farshadfighter/taktaplus/internal/settings"
	"github.com/farshadfighter/taktaplus/internal/sms"
	"github.com/farshadfighter/taktaplus/internal/testdb"
)

type fakeSMS struct {
	mu   sync.Mutex
	last string
}

func (f *fakeSMS) SendCode(_ context.Context, _, code string) (string, error) {
	f.mu.Lock()
	f.last = code
	f.mu.Unlock()
	return "1", nil
}
func (f *fakeSMS) Credit(context.Context) (float64, error) { return 1, nil }

func TestRADIUSChallenge(t *testing.T) {
	ctx := context.Background()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secretbox.New(key)
	db := testdb.New(t)
	st := settings.New(db, box)
	smsSvc := sms.New(db, st)
	fake := &fakeSMS{}
	smsSvc.SetProviderFactory(func(sms.Config) sms.Provider { return fake })
	_ = smsSvc.SaveConfig(ctx, sms.Config{APIKey: "k", TemplateID: 1, Param: "CODE"})
	exp := licformat.NewDate(time.Now().Year()+1, 1, 1)
	guard := licensetest.GuardOTP(t, 10, exp)
	o := otp.New(db, st, smsSvc, guard)
	if _, err := o.Create(ctx, otp.UserInput{Username: "reza", Mobile: "09121234567", Password: "correct horse battery", FortinetGroup: "VPN-Users", Active: true}); err != nil {
		t.Fatal(err)
	}

	// The FortiGate is registered at 127.0.0.1.
	inv := inventory.New(db, box, guard)
	inv.SetDriverFactory(devicetest.Fleet{"127.0.0.1": {Info: device.Info{Serial: "FGOTP0001"}}}.Factory())
	id, _ := inv.Create(ctx, inventory.Input{Name: "fg", Kind: device.KindFortiGate, Host: "127.0.0.1", Credentials: &inventory.Credentials{APIToken: "t"}})

	srv := New(db, box, st, o, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := srv.SetClient(ctx, id, true, ""); err != nil {
		t.Fatal(err)
	}
	clients, _ := srv.ListClients(ctx)
	secret := []byte(clients[0].secret)
	cfg, err := srv.FortiGateConfig(ctx, id, "10.0.0.5", "")
	if err != nil || !contains(cfg, `set server "10.0.0.5"`) || !contains(cfg, "set auth-type pap") {
		t.Fatalf("config = %q %v", cfg, err)
	}

	conn, _ := net.ListenPacket("udp", "127.0.0.1:0")
	go func() { _ = srv.ServeConn(conn) }()
	defer srv.Shutdown(ctx)
	addr := conn.LocalAddr().String()

	withMA := true
	exchange := func(user, pass, state string, sec []byte) *radius.Packet {
		p := radius.New(radius.CodeAccessRequest, sec)
		_ = rfc2865.UserName_SetString(p, user)
		_ = rfc2865.UserPassword_SetString(p, pass)
		if state != "" {
			_ = rfc2865.State_SetString(p, state)
		}
		if withMA {
			signResponse(p) // same computation as for requests
		}
		cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		resp, err := radius.Exchange(cctx, p, addr)
		if err != nil {
			return nil
		}
		return resp
	}

	if r := exchange("reza", "wrong password", "", secret); r == nil || r.Code != radius.CodeAccessReject {
		t.Fatalf("wrong password = %v", r)
	}
	r := exchange("reza", "correct horse battery", "", secret)
	if r == nil || r.Code != radius.CodeAccessChallenge {
		t.Fatalf("challenge = %v", r)
	}
	state := rfc2865.State_GetString(r)
	if state == "" || rfc2865.ReplyMessage_GetString(r) == "" {
		t.Fatal("challenge without state or message")
	}
	if r := exchange("reza", "000000x", state, secret); r == nil || r.Code != radius.CodeAccessChallenge {
		t.Fatalf("wrong code = %v", r)
	}
	r = exchange("reza", fake.last, state, secret)
	if r == nil || r.Code != radius.CodeAccessAccept {
		t.Fatalf("accept = %v", r)
	}
	vsa := r.Get(rfc2865.VendorSpecific_Type)
	vendor, val, err := radius.VendorSpecific(vsa)
	if err != nil || vendor != FortinetVendorID || string(val[2:]) != "VPN-Users" {
		t.Fatalf("group vsa = %d %q %v", vendor, val, err)
	}
	if _, ok := r.Attributes.Lookup(messageAuthenticatorType); !ok {
		t.Fatal("response without Message-Authenticator")
	}
	// A wrong shared secret, or no Message-Authenticator, is dropped
	// without counting against the user.
	if r := exchange("reza", "correct horse battery", "", []byte("wrong-secret")); r != nil {
		t.Fatalf("wrong secret answered: %v", r.Code)
	}
	withMA = false
	if r := exchange("reza", "correct horse battery", "", secret); r != nil {
		t.Fatalf("request without Message-Authenticator answered: %v", r.Code)
	}
	events, _ := o.Events(ctx, 0)
	if len(events) != 4 || events[0].Event != "accept" {
		t.Fatalf("events = %+v", events)
	}
	// With the requirement off, such requests are served.
	_ = srv.SaveConfig(ctx, Config{RequireMessageAuthenticator: false})
	if r := exchange("reza", "correct horse battery", "", secret); r == nil || r.Code != radius.CodeAccessChallenge {
		t.Fatalf("optional MA = %v", r)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
