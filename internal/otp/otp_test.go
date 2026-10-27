package otp

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/farshadfighter/taktaplus-lic/pkg/licformat"

	"github.com/farshadfighter/taktaplus/internal/license/licensetest"
	"github.com/farshadfighter/taktaplus/internal/secretbox"
	"github.com/farshadfighter/taktaplus/internal/settings"
	"github.com/farshadfighter/taktaplus/internal/sms"
	"github.com/farshadfighter/taktaplus/internal/testdb"
)

// FakeSMS records codes per mobile.
type FakeSMS struct {
	mu    sync.Mutex
	codes map[string]string
}

func (f *FakeSMS) SendCode(_ context.Context, mobile, code string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.codes[mobile] = code
	return "1", nil
}
func (f *FakeSMS) Credit(context.Context) (float64, error) { return 100, nil }
func (f *FakeSMS) last(m string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.codes[m]
}

// fakeDir is a directory with one user (alice / ad-secret).
type fakeDir struct{ bound string }

func (d *fakeDir) Bind(user, pass string) error {
	switch {
	case user == "cn=svc" && pass == "svc-pass", user == "cn=alice,dc=x" && pass == "ad-secret":
		d.bound = user
		return nil
	}
	return &ldap.Error{ResultCode: ldap.LDAPResultInvalidCredentials}
}
func (d *fakeDir) Search(req *ldap.SearchRequest) (*ldap.SearchResult, error) {
	alice := ldap.NewEntry("cn=alice,dc=x", map[string][]string{"sAMAccountName": {"alice"}, "mobile": {"+98 912 000 1111"}, "displayName": {"Alice"}})
	bob := ldap.NewEntry("cn=bob,dc=x", map[string][]string{"sAMAccountName": {"bob"}, "mobile": {"none"}, "displayName": {"Bob"}})
	if strings.Contains(req.Filter, "sAMAccountName=alice") {
		return &ldap.SearchResult{Entries: []*ldap.Entry{alice}}, nil
	}
	if strings.Contains(req.Filter, "sAMAccountName=") {
		return &ldap.SearchResult{}, nil
	}
	return &ldap.SearchResult{Entries: []*ldap.Entry{alice, bob}}, nil
}
func (d *fakeDir) Close() error { return nil }

func setup(t *testing.T, capacity int) (*Service, *FakeSMS) {
	t.Helper()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secretbox.New(key)
	db := testdb.New(t)
	st := settings.New(db, box)
	smsSvc := sms.New(db, st)
	fake := &FakeSMS{codes: map[string]string{}}
	smsSvc.SetProviderFactory(func(sms.Config) sms.Provider { return fake })
	if err := smsSvc.SaveConfig(context.Background(), sms.Config{APIKey: "k", TemplateID: 1, Param: "CODE"}); err != nil {
		t.Fatal(err)
	}
	guard := licensetest.GuardOTP(t, capacity, licformat.NewDate(time.Now().Year()+1, 1, 1))
	s := New(db, st, smsSvc, guard)
	s.ldap = func(LDAPConfig) (ldapConn, error) { return &fakeDir{}, nil }
	return s, fake
}

func TestLocalUserChallenge(t *testing.T) {
	s, fake := setup(t, 2)
	ctx := context.Background()
	if _, err := s.Create(ctx, UserInput{Username: "bad", Mobile: "021", Password: "x", Active: true}); err == nil {
		t.Fatal("bad mobile accepted")
	}
	u, err := s.Create(ctx, UserInput{Username: "reza", Name: "Reza", Mobile: "09121234567", Password: "correct horse battery", FortinetGroup: "vpn", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, why, err := s.CheckPassword(ctx, "reza", "wrong password!!"); !errors.Is(err, ErrRejected) || why != "wrong password" {
		t.Fatalf("wrong password = %v %s", err, why)
	}
	got, _, err := s.CheckPassword(ctx, "REZA", "correct horse battery")
	if err != nil || got.ID != u.ID {
		t.Fatalf("password = %v", err)
	}
	state, err := s.StartChallenge(ctx, got, nil)
	if err != nil {
		t.Fatal(err)
	}
	code := fake.last("09121234567")
	if len(code) != 6 {
		t.Fatalf("code = %q", code)
	}
	if _, _, err := s.VerifyChallenge(ctx, state, "000000"); !errors.Is(err, ErrWrongCode) && code != "000000" {
		t.Fatalf("wrong code = %v", err)
	}
	ok, _, err := s.VerifyChallenge(ctx, state, code)
	if err != nil || ok.FortinetGroup != "vpn" {
		t.Fatalf("verify = %v", err)
	}
	if _, _, err := s.VerifyChallenge(ctx, state, code); !errors.Is(err, ErrRejected) {
		t.Fatal("state reused")
	}

	// Too many wrong codes end the challenge.
	state, _ = s.StartChallenge(ctx, got, nil)
	for i := 0; i < 3; i++ {
		_, _, err = s.VerifyChallenge(ctx, state, "x")
	}
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("exhausted = %v", err)
	}
	// Send limit: three codes per ten minutes.
	if _, err := s.StartChallenge(ctx, got, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartChallenge(ctx, got, nil); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("rate limit = %v", err)
	}
	// Capacity: two users fit, a third does not.
	if _, err := s.Create(ctx, UserInput{Username: "u2", Mobile: "09120000002", Password: "correct horse battery", Active: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, UserInput{Username: "u3", Mobile: "09120000003", Password: "correct horse battery", Active: true}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("capacity = %v", err)
	}
}

func TestLockout(t *testing.T) {
	s, _ := setup(t, 5)
	ctx := context.Background()
	_ = s.SavePolicy(ctx, Policy{Length: 6, TTLSeconds: 120, MaxAttempts: 3, MaxSends: 3, MaxFailures: 2, LockoutMinutes: 10})
	u, _ := s.Create(ctx, UserInput{Username: "x", Mobile: "09120000009", Password: "correct horse battery", Active: true})
	s.CheckPassword(ctx, "x", "nope")
	s.CheckPassword(ctx, "x", "nope")
	if _, why, err := s.CheckPassword(ctx, "x", "correct horse battery"); !errors.Is(err, ErrRejected) || why != "user is locked" {
		t.Fatalf("locked = %v %s", err, why)
	}
	_ = s.Unlock(ctx, u.ID)
	if _, _, err := s.CheckPassword(ctx, "x", "correct horse battery"); err != nil {
		t.Fatalf("after unlock = %v", err)
	}
}

func TestLDAPUsers(t *testing.T) {
	s, _ := setup(t, 5)
	ctx := context.Background()
	if err := s.SaveLDAP(ctx, LDAPConfig{URL: "ldap://dc", BindDN: "cn=svc", BindPassword: "svc-pass", BaseDN: "dc=x",
		UserFilter: DefaultLDAP.UserFilter, ImportFilter: DefaultLDAP.ImportFilter, UserAttr: "sAMAccountName", MobileAttr: "mobile", NameAttr: "displayName"}); err != nil {
		t.Fatal(err)
	}
	found, err := s.SearchDirectory(ctx)
	if err != nil || len(found) != 2 || !found[0].Valid || found[0].Mobile != "09120001111" || found[1].Valid {
		t.Fatalf("search = %+v %v", found, err)
	}
	n, err := s.ImportDirectory(ctx, []string{"alice", "bob"}, "vpn-ad")
	if err != nil || n != 1 {
		t.Fatalf("import = %d %v", n, err)
	}
	if _, _, err := s.CheckPassword(ctx, "alice", "ad-secret"); err != nil {
		t.Fatalf("ldap password = %v", err)
	}
	if _, why, err := s.CheckPassword(ctx, "alice", "wrong"); !errors.Is(err, ErrRejected) || why != "wrong password" {
		t.Fatalf("ldap wrong = %v %s", err, why)
	}
	if _, _, err := s.CheckPassword(ctx, "alice", ""); !errors.Is(err, ErrRejected) {
		t.Fatal("empty password accepted (anonymous bind)")
	}
	cfg, _ := s.LDAP(ctx)
	if cfg.Redacted().BindPassword != "********" {
		t.Fatal("not redacted")
	}
}
