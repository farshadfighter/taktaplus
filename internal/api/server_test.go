package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/farshadfighter/taktaplus-lic/pkg/keys"
	"github.com/farshadfighter/taktaplus-lic/pkg/licformat"

	"github.com/farshadfighter/taktaplus/internal/alerts"
	"github.com/farshadfighter/taktaplus/internal/auth"
	"github.com/farshadfighter/taktaplus/internal/backup"
	"github.com/farshadfighter/taktaplus/internal/changes"
	"github.com/farshadfighter/taktaplus/internal/device"
	"github.com/farshadfighter/taktaplus/internal/device/devicetest"
	"github.com/farshadfighter/taktaplus/internal/inventory"
	"github.com/farshadfighter/taktaplus/internal/license"
	"github.com/farshadfighter/taktaplus/internal/monitor"
	"github.com/farshadfighter/taktaplus/internal/scripts"
	"github.com/farshadfighter/taktaplus/internal/secretbox"
	"github.com/farshadfighter/taktaplus/internal/testdb"
)

type harness struct {
	t      *testing.T
	db     *pgxpool.Pool
	auth   *auth.Service
	ts     *httptest.Server
	client *http.Client
	fleet  devicetest.Fleet
	secret []byte // TOTP secret of the signed-in user
	ch     *changes.Service
}

// fakeLicenseServer signs a one-device license for any activation request.
func fakeLicenseServer(t *testing.T, priv ed25519.PrivateKey) *httptest.Server {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Request string `json:"request"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		req, err := licformat.ParseRequest(body.Request)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		if req.Type == licformat.RequestActivate && req.LicenseKey != "TPK-GOOD" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "activation key is not valid"})
			return
		}
		exp := licformat.NewDate(time.Now().Year()+1, 1, 1)
		l := &licformat.License{
			Format: licformat.FormatVersion, KeyID: "lic-test", LicenseID: "LIC-000007", Customer: licformat.Customer{ID: "C-1", Name: "Test"},
			InstallationID: req.InstallationID, InstallationPubKey: req.InstallationPubKey, IssuedAt: time.Now().UTC().Truncate(time.Second),
			NotAfter: exp.EndOfDay().AddDate(0, 0, 14), GraceDays: 14,
			Online:  &licformat.Online{HeartbeatURL: srv.URL + "/api/v1/heartbeat", IntervalHours: 24},
			Devices: []licformat.DeviceItem{{Serial: "FG100F0001", Platform: licformat.PlatformFortiGate, Expires: exp}},
		}
		tok, err := l.Sign(priv)
		if err != nil {
			t.Error(err)
		}
		status := "updated"
		if strings.HasSuffix(r.URL.Path, "/activate") {
			status = ""
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"license": tok, "status": status})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	db := testdb.New(t)
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secretbox.New(key)
	svc := auth.NewService(db, box)

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	licSrv := fakeLicenseServer(t, priv)
	id, err := license.LoadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	guard := license.NewGuard(keys.Keyring{"lic-test": pub}, id)
	inv := inventory.New(db, box, guard)
	fleet := devicetest.Fleet{}
	inv.SetDriverFactory(fleet.Factory())
	mgr := license.NewManager(guard, db, log, licSrv.URL, t.TempDir(), inv.Usage)
	if err := mgr.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	al := alerts.New(db, box, inv, guard, log)
	mon := monitor.New(inv, al, log, monitor.Options{})
	bk := backup.New(db, box, inv, t.TempDir(), log, backup.Options{RebootWait: time.Millisecond, RebootPoll: time.Millisecond, RebootLimit: time.Second})
	t.Cleanup(bk.Wait)

	ui := fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")}}
	ch := changes.New(db, inv, bk, log)
	sc := scripts.New(db, inv)
	ch.Register(scripts.Kind, scripts.NewRunner(sc))
	ts := httptest.NewServer(New(Deps{Log: log, DB: db, Auth: svc, License: mgr, UI: ui, Inventory: inv, Alerts: al, Monitor: mon, Backups: bk,
		Changes: ch, Scripts: sc}))
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	return &harness{t: t, db: db, auth: svc, ts: ts, client: &http.Client{Jar: jar}, fleet: fleet, ch: ch}
}

func (h *harness) do(method, path string, body any) (int, map[string]any) {
	h.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, h.ts.URL+path, r)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(csrfHeader, csrfValue)
	resp, err := h.client.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	data, _ := io.ReadAll(resp.Body)
	if json.Unmarshal(data, &out) != nil {
		out["raw"] = string(data)
	}
	return resp.StatusCode, out
}

// signIn creates a user with the role and completes password + TOTP login.
func (h *harness) signIn(username string, role auth.Role) {
	h.t.Helper()
	if _, err := h.auth.CreateUser(context.Background(), username, "Test "+username, "", role, "correct horse battery"); err != nil {
		h.t.Fatal(err)
	}
	code, body := h.do("POST", "/api/v1/auth/login", map[string]string{"username": username, "password": "correct horse battery"})
	if code != http.StatusOK || body["next"] != "mfa_setup" {
		h.t.Fatalf("login = %d %v", code, body)
	}
	_, body = h.do("POST", "/api/v1/auth/mfa/setup", nil)
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(body["secret"].(string))
	if err != nil {
		h.t.Fatal(err)
	}
	if code, body := h.do("POST", "/api/v1/auth/mfa/enable", map[string]string{"code": auth.TOTPCode(secret, time.Now())}); code != http.StatusOK {
		h.t.Fatalf("mfa = %d %v", code, body)
	}
	h.secret = secret
}

func TestLoginAndRoles(t *testing.T) {
	h := newHarness(t)
	if code, _ := h.do("GET", "/api/v1/me", nil); code != http.StatusUnauthorized {
		t.Fatalf("anonymous /me = %d", code)
	}
	h.signIn("viewer1", auth.RoleViewer)
	code, body := h.do("GET", "/api/v1/me", nil)
	if code != http.StatusOK || body["user"].(map[string]any)["username"] != "viewer1" {
		t.Fatalf("/me = %d %v", code, body)
	}
	if code, _ := h.do("GET", "/api/v1/users", nil); code != http.StatusForbidden {
		t.Fatalf("viewer listing users = %d", code)
	}
	if code, _ := h.do("GET", "/api/v1/license", nil); code != http.StatusOK {
		t.Fatalf("viewer reading license = %d", code)
	}
	if code, _ := h.do("POST", "/api/v1/license/activate", map[string]string{"key": "TPK-GOOD"}); code != http.StatusForbidden {
		t.Fatalf("viewer activating = %d", code)
	}
}

func TestUserAdministration(t *testing.T) {
	h := newHarness(t)
	h.signIn("root", auth.RoleSuperAdmin)
	code, body := h.do("POST", "/api/v1/users", map[string]any{"username": "Op1", "name": "Operator", "role": "operator", "password": "another long password"})
	if code != http.StatusCreated {
		t.Fatalf("create = %d %v", code, body)
	}
	id := int64(body["id"].(float64))
	if code, _ := h.do("POST", "/api/v1/users", map[string]any{"username": "op1", "name": "Dup", "role": "operator", "password": "another long password"}); code != http.StatusBadRequest {
		t.Fatalf("duplicate username = %d", code)
	}
	path := "/api/v1/users/" + strconv.FormatInt(id, 10)
	if code, _ := h.do("PUT", path, map[string]any{"name": "Op", "role": "approver", "active": true}); code != http.StatusNoContent {
		t.Fatalf("update = %d", code)
	}
	if code, _ := h.do("POST", path+"/reset-mfa", nil); code != http.StatusNoContent {
		t.Fatalf("reset mfa = %d", code)
	}
	_, body = h.do("GET", "/api/v1/users", nil)
	if n := len(body["items"].([]any)); n != 2 {
		t.Fatalf("users = %d", n)
	}
	// Superadmins cannot lock themselves out.
	_, me := h.do("GET", "/api/v1/me", nil)
	myID := int64(me["user"].(map[string]any)["id"].(float64))
	mePath := "/api/v1/users/" + strconv.FormatInt(myID, 10)
	if code, _ := h.do("PUT", mePath, map[string]any{"name": "x", "role": "viewer", "active": true}); code != http.StatusBadRequest {
		t.Fatalf("self-demotion = %d", code)
	}
}

func TestLicenseActivationFlow(t *testing.T) {
	h := newHarness(t)
	h.signIn("root", auth.RoleSuperAdmin)

	_, body := h.do("GET", "/api/v1/license", nil)
	if body["license"] != nil || body["installation_id"] == "" {
		t.Fatalf("initial license = %v", body)
	}
	code, body := h.do("POST", "/api/v1/license/activate", map[string]string{"key": "TPK-BAD"})
	if code != http.StatusBadGateway || !strings.Contains(body["error"].(string), "not valid") {
		t.Fatalf("bad key = %d %v", code, body)
	}
	code, body = h.do("POST", "/api/v1/license/activate", map[string]string{"key": "TPK-GOOD"})
	if code != http.StatusOK {
		t.Fatalf("activate = %d %v", code, body)
	}
	lic := body["license"].(map[string]any)
	items := lic["items"].([]any)
	if lic["license_id"] != "LIC-000007" || len(items) != 1 || items[0].(map[string]any)["status"] != "active" {
		t.Fatalf("license = %v", lic)
	}

	code, body = h.do("POST", "/api/v1/license/heartbeat", nil)
	if code != http.StatusOK || body["state"].(map[string]any)["last_heartbeat_status"] != "updated" {
		t.Fatalf("heartbeat = %d %v", code, body)
	}

	code, body = h.do("POST", "/api/v1/license/request", map[string]string{"key": "TPK-OFFLINE"})
	if code != http.StatusOK || !strings.HasPrefix(body["raw"].(string), "TPREQ1.") {
		t.Fatalf("offline request = %d %v", code, body)
	}
	if code, _ := h.do("POST", "/api/v1/license/upload", map[string]string{"license": "TPLIC1.bad.sig"}); code != http.StatusBadRequest {
		t.Fatalf("bad upload = %d", code)
	}
}

func TestDevices(t *testing.T) {
	h := newHarness(t)
	h.fleet["10.1.1.1"] = &devicetest.Fake{
		Info:    device.Info{Serial: "FG100F0001", Model: "FG100F", Firmware: "7.4.4"},
		HealthV: device.Health{CPU: 12, Mem: 40, Disk: -1},
		Sigs:    []device.Signature{{Name: "ips_db", Version: "28.00948"}},
	}
	h.signIn("root", auth.RoleSuperAdmin)
	if code, body := h.do("POST", "/api/v1/license/activate", map[string]string{"key": "TPK-GOOD"}); code != http.StatusOK {
		t.Fatalf("activate = %d %v", code, body)
	}

	input := map[string]any{"name": "FG-HQ", "kind": "fortigate", "host": "10.1.1.1", "tls_pin": strings.Repeat("ab", 32),
		"credentials": map[string]string{"api_token": "secret-token"}}
	code, body := h.do("POST", "/api/v1/devices/discover", input)
	if code != http.StatusOK || body["license"] != "active" || body["info"].(map[string]any)["serial"] != "FG100F0001" {
		t.Fatalf("discover = %d %v", code, body)
	}
	code, body = h.do("POST", "/api/v1/devices", input)
	if code != http.StatusCreated || body["serial"] != "FG100F0001" || body["status"] != "online" {
		t.Fatalf("create = %d %v", code, body)
	}
	if strings.Contains(fmt.Sprint(body), "secret-token") {
		t.Fatal("credentials leaked")
	}
	path := "/api/v1/devices/" + strconv.Itoa(int(body["id"].(float64)))

	code, body = h.do("GET", path, nil)
	st, _ := body["status"].(map[string]any)
	if code != http.StatusOK || st == nil || st["cpu"] != 12.0 || len(st["signatures"].([]any)) != 1 {
		t.Fatalf("get = %d %v", code, body)
	}
	code, body = h.do("GET", "/api/v1/devices", nil)
	if code != http.StatusOK || len(body["items"].([]any)) != 1 {
		t.Fatalf("list = %d %v", code, body)
	}
	if code, body := h.do("GET", path+"/metrics?hours=1", nil); code != http.StatusOK || len(body["items"].([]any)) != 1 {
		t.Fatalf("metrics = %d %v", code, body)
	}

	// The device appears in license usage.
	code, body = h.do("POST", "/api/v1/license/request", map[string]string{})
	if code != http.StatusOK {
		t.Fatalf("refresh request = %d %v", code, body)
	}

	// Unreachable device: refresh reports the device error.
	h.fleet["10.1.1.1"].Set(func(f *devicetest.Fake) { f.Down = true })
	if code, body := h.do("POST", path+"/refresh", nil); code != http.StatusBadGateway || !strings.Contains(body["error"].(string), "refused") {
		t.Fatalf("refresh down = %d %v", code, body)
	}

	if code, _ := h.do("POST", "/api/v1/devices", map[string]any{"name": "x", "kind": "fortigate", "host": "1.2.3.4"}); code != http.StatusBadRequest {
		t.Fatalf("no credentials = %d", code)
	}

	code, body = h.do("POST", "/api/v1/channels", map[string]any{"name": "hook", "kind": "webhook", "enabled": true, "config": map[string]any{"url": "https://example.invalid/x"}})
	if code != http.StatusOK {
		t.Fatalf("channel = %d %v", code, body)
	}
	if code, body := h.do("GET", "/api/v1/alert-rules", nil); code != http.StatusOK || len(body["items"].([]any)) != 6 {
		t.Fatalf("rules = %d %v", code, body)
	}
	if code, _ := h.do("GET", "/api/v1/alerts?state=open", nil); code != http.StatusOK {
		t.Fatalf("alerts = %d", code)
	}

	if code, _ := h.do("DELETE", path, map[string]string{"password": "wrong", "code": "000000"}); code != http.StatusForbidden {
		t.Fatalf("delete without reauth = %d", code)
	}
	if code, body := h.do("DELETE", path, h.reauth()); code != http.StatusNoContent {
		t.Fatalf("delete = %d %v", code, body)
	}

	// Viewers can read but not change.
	h2 := &harness{t: t, db: h.db, auth: h.auth, ts: h.ts, fleet: h.fleet}
	jar, _ := cookiejar.New(nil)
	h2.client = &http.Client{Jar: jar}
	h2.signIn("viewer", auth.RoleViewer)
	if code, _ := h2.do("GET", "/api/v1/devices", nil); code != http.StatusOK {
		t.Fatalf("viewer list = %d", code)
	}
	if code, _ := h2.do("POST", "/api/v1/devices", input); code != http.StatusForbidden {
		t.Fatalf("viewer create = %d", code)
	}
	if code, _ := h2.do("GET", "/api/v1/channels", nil); code != http.StatusForbidden {
		t.Fatalf("viewer channels = %d", code)
	}
}

func (h *harness) reauth() map[string]string {
	return map[string]string{"password": "correct horse battery", "code": auth.TOTPCode(h.secret, time.Now())}
}

func TestBackups(t *testing.T) {
	h := newHarness(t)
	cfg := "#config-version=FG100F-7.04\nconfig system admin\n    edit \"admin\"\n        set password ENC SH2secret\n    next\nend\n"
	h.fleet["10.1.1.1"] = &devicetest.Fake{Info: device.Info{Serial: "FG100F0001"}, Config: []byte(cfg)}
	h.signIn("root", auth.RoleSuperAdmin)
	if code, body := h.do("POST", "/api/v1/license/activate", map[string]string{"key": "TPK-GOOD"}); code != http.StatusOK {
		t.Fatalf("activate = %d %v", code, body)
	}
	code, body := h.do("POST", "/api/v1/devices", map[string]any{"name": "FG", "kind": "fortigate", "host": "10.1.1.1",
		"credentials": map[string]string{"api_token": "t"}})
	if code != http.StatusCreated {
		t.Fatalf("create = %d %v", code, body)
	}
	dev := strconv.Itoa(int(body["id"].(float64)))

	code, body = h.do("POST", "/api/v1/devices/"+dev+"/backup", nil)
	if code != http.StatusOK || body["unchanged"] != false {
		t.Fatalf("backup = %d %v", code, body)
	}
	bid := strconv.Itoa(int(body["backup"].(map[string]any)["id"].(float64)))
	if code, body := h.do("POST", "/api/v1/devices/"+dev+"/backup", nil); code != http.StatusOK || body["unchanged"] != true {
		t.Fatalf("second backup = %d %v", code, body)
	}

	code, body = h.do("GET", "/api/v1/backups/"+bid+"/content", nil)
	if code != http.StatusOK || strings.Contains(body["content"].(string), "SH2secret") {
		t.Fatalf("content = %d %v", code, body)
	}
	if code, body := h.do("GET", "/api/v1/backups/"+bid+"/download", nil); code != http.StatusOK || !strings.Contains(body["raw"].(string), "SH2secret") {
		t.Fatalf("download = %d %v", code, body)
	}
	if code, body := h.do("GET", "/api/v1/backups?device="+dev, nil); code != http.StatusOK || len(body["items"].([]any)) != 1 {
		t.Fatalf("list = %d %v", code, body)
	}

	h.fleet["10.1.1.1"].Set(func(f *devicetest.Fake) { f.Config = []byte(strings.Replace(cfg, "SH2secret", "SH2other", 1)) })
	code, body = h.do("GET", "/api/v1/backups/"+bid+"/diff-current", nil)
	if code != http.StatusOK || len(body["changes"].([]any)) != 1 || strings.Contains(fmt.Sprint(body), "SH2") {
		t.Fatalf("diff-current = %d %v", code, body)
	}

	if code, _ := h.do("POST", "/api/v1/backups/"+bid+"/restore", map[string]string{"password": "correct horse battery", "code": "000000"}); code != http.StatusForbidden {
		t.Fatalf("restore with bad code = %d", code)
	}
	code, body = h.do("POST", "/api/v1/backups/"+bid+"/restore", h.reauth())
	if code != http.StatusAccepted || body["status"] != "waiting" {
		t.Fatalf("restore = %d %v", code, body)
	}
	job := strconv.Itoa(int(body["id"].(float64)))
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, body = h.do("GET", "/api/v1/restores/"+job, nil)
		if body["status"] == "succeeded" || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if body["status"] != "succeeded" {
		t.Fatalf("restore job = %v", body)
	}

	if code, body := h.do("POST", "/api/v1/backup-policies", map[string]any{"name": "Hourly", "schedule": "0 * * * *", "keep_count": 48, "enabled": true}); code != http.StatusOK {
		t.Fatalf("policy = %d %v", code, body)
	}
	if code, body := h.do("GET", "/api/v1/backup-policies", nil); code != http.StatusOK || len(body["items"].([]any)) != 2 {
		t.Fatalf("policies = %d %v", code, body)
	}
}

func TestChangeWorkflowAPI(t *testing.T) {
	h := newHarness(t)
	h.fleet["10.1.1.1"] = &devicetest.Fake{Info: device.Info{Serial: "FG100F0001"}, Config: []byte("#config-version=x\nconfig system global\nend\n")}
	h.signIn("op", auth.RoleAdmin)
	// A superadmin activates the license and approves; the admin requests.
	root := &harness{t: t, db: h.db, auth: h.auth, ts: h.ts, fleet: h.fleet, ch: h.ch}
	jar, _ := cookiejar.New(nil)
	root.client = &http.Client{Jar: jar}
	root.signIn("root", auth.RoleSuperAdmin)
	if code, body := root.do("POST", "/api/v1/license/activate", map[string]string{"key": "TPK-GOOD"}); code != http.StatusOK {
		t.Fatalf("activate = %d %v", code, body)
	}
	code, body := h.do("POST", "/api/v1/devices", map[string]any{"name": "FG", "kind": "fortigate", "host": "10.1.1.1",
		"credentials": map[string]string{"ssh_user": "admin", "ssh_password": "pw"}})
	if code != http.StatusCreated {
		t.Fatalf("device = %d %v", code, body)
	}
	dev := body["id"].(float64)

	code, body = h.do("POST", "/api/v1/scripts", map[string]any{"name": "Hostname", "body": "config system global\n  set hostname {{ .Vars.name }}\nend"})
	if code != http.StatusOK || body["read_only"] != false {
		t.Fatalf("script = %d %v", code, body)
	}
	sid := body["id"].(float64)
	if code, _ := h.do("POST", "/api/v1/scripts/"+strconv.Itoa(int(sid))+"/run", map[string]any{"device_ids": []float64{dev}}); code != http.StatusBadRequest {
		t.Fatalf("read-only run of a config script = %d", code)
	}
	code, body = h.do("POST", "/api/v1/changes", map[string]any{"kind": "script", "title": "Rename", "device_ids": []float64{dev},
		"spec": map[string]any{"script_id": sid, "vars": map[string]string{"name": "FG-NEW"}}})
	if code != http.StatusCreated || body["status"] != "draft" || body["required_approvals"] != 1.0 {
		t.Fatalf("create change = %d %v", code, body)
	}
	cid := strconv.Itoa(int(body["id"].(float64)))
	code, body = h.do("GET", "/api/v1/changes/"+cid, nil)
	target := body["targets"].([]any)[0].(map[string]any)
	if code != http.StatusOK || !strings.Contains(target["preview"].(string), "set hostname FG-NEW") {
		t.Fatalf("get change = %d %v", code, body)
	}
	if code, body := h.do("POST", "/api/v1/changes/"+cid+"/submit", map[string]any{}); code != http.StatusOK || body["change"].(map[string]any)["status"] != "pending" {
		t.Fatalf("submit = %d %v", code, body)
	}
	if code, _ := h.do("POST", "/api/v1/changes/"+cid+"/approve", map[string]string{"comment": "mine"}); code != http.StatusConflict {
		t.Fatalf("self approval = %d", code)
	}
	if code, body := root.do("POST", "/api/v1/changes/"+cid+"/approve", map[string]string{"comment": "ok"}); code != http.StatusOK ||
		body["change"].(map[string]any)["status"] != "approved" {
		t.Fatalf("approve = %d %v", code, body)
	}
	var wg sync.WaitGroup
	if err := h.ch.Tick(context.Background(), &wg); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	_, body = h.do("GET", "/api/v1/changes/"+cid, nil)
	if body["change"].(map[string]any)["status"] != "succeeded" || len(h.fleet["10.1.1.1"].Scripts) != 1 {
		t.Fatalf("after run = %v", body)
	}
	if code, body := h.do("POST", "/api/v1/maintenance/windows", map[string]any{"name": "Night", "weekdays": []int{4}, "start_time": "22:00", "minutes": 240, "enabled": true}); code != http.StatusForbidden {
		t.Fatalf("admin editing windows = %d %v", code, body)
	}
	if code, _ := root.do("POST", "/api/v1/maintenance/windows", map[string]any{"name": "Night", "weekdays": []int{4}, "start_time": "22:00", "minutes": 240, "enabled": true}); code != http.StatusOK {
		t.Fatalf("window = %d", code)
	}
	if code, body := h.do("GET", "/api/v1/maintenance", nil); code != http.StatusOK || len(body["windows"].([]any)) != 1 {
		t.Fatalf("maintenance = %d %v", code, body)
	}
}
