package smtpgw

import (
	"context"
	"crypto/rand"
	"io"
	"log/slog"
	"net"
	"net/smtp"
	"strings"
	"sync"
	"testing"
	"time"

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
	sent map[string]string
}

func (f *fakeSMS) SendCode(_ context.Context, m, code string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent[m] = code
	return "1", nil
}
func (f *fakeSMS) Credit(context.Context) (float64, error) { return 1, nil }

func TestGateway(t *testing.T) {
	ctx := context.Background()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secretbox.New(key)
	db := testdb.New(t)
	st := settings.New(db, box)
	smsSvc := sms.New(db, st)
	fake := &fakeSMS{sent: map[string]string{}}
	smsSvc.SetProviderFactory(func(sms.Config) sms.Provider { return fake })
	_ = smsSvc.SaveConfig(ctx, sms.Config{APIKey: "k", TemplateID: 1, Param: "CODE"})
	guard := licensetest.GuardOTP(t, 1, licformat.NewDate(time.Now().Year()+1, 1, 1))
	o := otp.New(db, st, smsSvc, guard)
	inv := inventory.New(db, box, guard)
	fleet := devicetest.Fleet{"127.0.0.1": {Info: device.Info{Serial: "FGOTP0001"}, LocalUsers: []device.LocalUser{
		{Name: "ali", TwoFactor: "sms", Phone: "+989121234567", Enabled: true},
		{Name: "token-user", TwoFactor: "fortitoken", Enabled: true},
	}}}
	inv.SetDriverFactory(fleet.Factory())
	id, _ := inv.Create(ctx, inventory.Input{Name: "fg", Kind: device.KindFortiGate, Host: "127.0.0.1", Credentials: &inventory.Credentials{APIToken: "t"}})
	_, _ = inv.Probe(ctx, id)

	g := New(db, st, smsSvc, o, inv, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var relayed []string
	g.relay = func(_ Config, from string, to []string, msg []byte) error {
		relayed = append(relayed, from+">"+strings.Join(to, ",")+":"+strings.SplitN(string(msg), "\n", 2)[0])
		return nil
	}
	if err := g.SaveConfig(ctx, Config{Domain: "sms.taktaplus.local", RelayHost: "mail.example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := g.SyncUsers(ctx); err != nil {
		t.Fatal(err)
	}
	users, _ := g.SMSUsers(ctx)
	if len(users) != 1 || users[0].Mobile != "09121234567" {
		t.Fatalf("synced = %+v", users)
	}
	if n, _ := o.Usage(ctx); n != 1 {
		t.Fatalf("usage = %d", n)
	}

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	go func() { _ = g.ServeListener(ln) }()
	defer g.Close()
	send := func(to, body string) error {
		return smtp.SendMail(ln.Addr().String(), nil, "fortigate@fg.local", []string{to}, []byte("Subject: FortiGate\r\n\r\n"+body+"\r\n"))
	}
	if err := send("09121234567@sms.taktaplus.local", "Your FortiGate token code is 482913. It expires in 60 seconds."); err != nil {
		t.Fatal(err)
	}
	if fake.sent["09121234567"] != "482913" {
		t.Fatalf("sms = %v", fake.sent)
	}
	if err := send("09120000000@sms.taktaplus.local", "code 111111"); err == nil {
		t.Fatal("unknown mobile accepted")
	}
	if err := send("noc@example.com", "FortiGate alert: link down"); err != nil {
		t.Fatal(err)
	}
	if len(relayed) != 1 || !strings.HasPrefix(relayed[0], "fortigate@fg.local>noc@example.com") {
		t.Fatalf("relayed = %v", relayed)
	}
	msgs, _ := smsSvc.Messages(ctx, 0)
	if len(msgs) != 2 || msgs[0].Status != "blocked" || msgs[1].Purpose != "gateway" || msgs[1].Username != "ali" {
		t.Fatalf("log = %+v", msgs)
	}

	// Over capacity: a second counted user blocks gateway messages.
	_, err := db.Exec(ctx, `INSERT INTO fortigate_sms_users (device_id, username, mobile) VALUES ($1, 'second', '09129999999')`, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := send("09121234567@sms.taktaplus.local", "code 222222"); err == nil {
		t.Fatal("sent over capacity")
	}

	cfg, _ := g.FortiGateConfig(ctx, "10.0.0.5", 0)
	if !strings.Contains(cfg, `set mail-server "sms.taktaplus.local"`) {
		t.Fatalf("config = %s", cfg)
	}
}

func TestMessageText(t *testing.T) {
	multi := "Subject: x\r\nContent-Type: multipart/alternative; boundary=b\r\n\r\n--b\r\nContent-Type: text/html\r\n\r\n<b>999999</b>\r\n--b\r\nContent-Type: text/plain\r\n\r\nYour code: 123456\r\n--b--\r\n"
	if got := sms.ParseCode(messageText([]byte(multi))); got != "123456" {
		t.Fatalf("multipart = %q", got)
	}
}
