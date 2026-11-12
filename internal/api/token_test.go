package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/farshadfighter/taktaplus/internal/auth"
)

// bearer calls the API with a token only: no cookie, no CSRF header.
func (h *harness) bearer(token, method, path string, body any) (int, map[string]any) {
	h.t.Helper()
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	req, _ := http.NewRequest(method, h.ts.URL+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (h *harness) newToken(in map[string]any) string {
	h.t.Helper()
	for k, v := range h.reauth() {
		in[k] = v
	}
	code, body := h.do("POST", "/api/v1/tokens", in)
	if code != http.StatusCreated {
		h.t.Fatalf("create token = %d %v", code, body)
	}
	return body["value"].(string)
}

func TestAPITokens(t *testing.T) {
	h := newHarness(t)
	h.signIn("root", auth.RoleSuperAdmin)

	// Creating needs the password and code again.
	if code, _ := h.do("POST", "/api/v1/tokens", map[string]any{"name": "x", "role": "viewer"}); code != http.StatusForbidden {
		t.Fatalf("create without reauth = %d", code)
	}
	admin := h.newToken(map[string]any{"name": "SOC", "role": "admin", "days": 30})
	viewerRO := h.newToken(map[string]any{"name": "dashboards", "role": "viewer", "read_only": true})
	elsewhere := h.newToken(map[string]any{"name": "remote", "role": "admin", "allowed_nets": []string{"10.0.0.0/8"}})

	// Tokens work without cookies or the CSRF header.
	if code, body := h.bearer(admin, "GET", "/api/v1/devices", nil); code != http.StatusOK {
		t.Fatalf("token GET = %d %v", code, body)
	}
	if code, body := h.bearer(admin, "POST", "/api/v1/groups", map[string]string{"name": "از API", "description": ""}); code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("token POST = %d %v", code, body)
	}
	// User management stays in the panel.
	if code, _ := h.bearer(admin, "GET", "/api/v1/users", nil); code != http.StatusForbidden {
		t.Fatalf("token on users = %d", code)
	}
	if code, _ := h.bearer(viewerRO, "GET", "/api/v1/devices", nil); code != http.StatusOK {
		t.Fatalf("read-only GET = %d", code)
	}
	if code, _ := h.bearer(viewerRO, "POST", "/api/v1/groups", map[string]string{"name": "x"}); code != http.StatusForbidden {
		t.Fatalf("read-only POST = %d", code)
	}
	// Panel-only actions refuse tokens even with the right role.
	if code, body := h.bearer(admin, "DELETE", "/api/v1/devices/1", map[string]string{"password": "correct horse battery", "code": "000000"}); code != http.StatusForbidden || body["error"] != errPanelOnly {
		t.Fatalf("delete device by token = %d %v", code, body)
	}
	// Source address and bad tokens.
	if code, _ := h.bearer(elsewhere, "GET", "/api/v1/devices", nil); code != http.StatusUnauthorized {
		t.Fatalf("token from another network = %d", code)
	}
	if code, _ := h.bearer(admin[:len(admin)-2]+"xx", "GET", "/api/v1/devices", nil); code != http.StatusUnauthorized {
		t.Fatalf("altered token = %d", code)
	}
	// Audit names the token.
	_, body := h.do("GET", "/api/v1/audit", nil)
	found := false
	for _, e := range body["entries"].([]any) {
		if m := e.(map[string]any); m["actor"] == "root (token: SOC)" && m["source"] == "api" {
			found = true
		}
	}
	if !found {
		t.Fatalf("audit does not name the token: %v", body["entries"])
	}
	// List shows last use; revoke stops the token.
	_, body = h.do("GET", "/api/v1/tokens", nil)
	items := body["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("tokens %v", items)
	}
	var socID float64
	for _, it := range items {
		m := it.(map[string]any)
		if m["name"] == "SOC" {
			socID = m["id"].(float64)
			if m["last_used_at"] == nil || m["last_ip"] != "127.0.0.1" {
				t.Fatalf("last use %v", m)
			}
		}
	}
	if code, _ := h.do("DELETE", "/api/v1/tokens/"+strconv.FormatInt(int64(socID), 10), nil); code != http.StatusNoContent {
		t.Fatalf("revoke = %d", code)
	}
	if code, _ := h.bearer(admin, "GET", "/api/v1/devices", nil); code != http.StatusUnauthorized {
		t.Fatalf("revoked token = %d", code)
	}
	// A token cannot manage tokens, and a weaker user cannot mint a stronger token.
	if code, _ := h.bearer(viewerRO, "GET", "/api/v1/tokens", nil); code != http.StatusForbidden {
		t.Fatalf("token listing tokens = %d", code)
	}
}

func TestTokenRoleCap(t *testing.T) {
	if !auth.RoleSuperAdmin.Covers(auth.RoleAdmin) || auth.RoleOperator.Covers(auth.RoleAdmin) || !auth.RoleAdmin.Covers(auth.RoleViewer) {
		t.Fatal("Covers")
	}
}
