package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/farshadfighter/taktaplus-lic/pkg/keys"
	"github.com/farshadfighter/taktaplus-lic/pkg/licformat"

	"github.com/farshadfighter/taktaplus/internal/auth"
	"github.com/farshadfighter/taktaplus/internal/license"
	"github.com/farshadfighter/taktaplus/internal/secretbox"
	"github.com/farshadfighter/taktaplus/internal/testdb"
)

type harness struct {
	t      *testing.T
	db     *pgxpool.Pool
	auth   *auth.Service
	ts     *httptest.Server
	client *http.Client
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
	mgr := license.NewManager(license.NewGuard(keys.Keyring{"lic-test": pub}, id), db, log, licSrv.URL, t.TempDir(), nil)
	if err := mgr.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	ui := fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")}}
	ts := httptest.NewServer(New(Deps{Log: log, DB: db, Auth: svc, License: mgr, UI: ui}))
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	return &harness{t: t, db: db, auth: svc, ts: ts, client: &http.Client{Jar: jar}}
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
