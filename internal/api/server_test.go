package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

type fakeDB struct{ err error }

func (f fakeDB) Ping(context.Context) error { return f.err }

func get(t *testing.T, h http.Handler, path string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	body, _ := io.ReadAll(rec.Body)
	return rec.Code, string(body)
}

func TestRoutes(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ui := fstest.MapFS{
		"index.html":    {Data: []byte("<html>app</html>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	}
	h := New(log, fakeDB{}, ui)

	if code, _ := get(t, h, "/healthz"); code != http.StatusOK {
		t.Errorf("healthz = %d", code)
	}
	if code, body := get(t, h, "/api/v1/version"); code != http.StatusOK || body == "" {
		t.Errorf("version = %d %q", code, body)
	}
	if code, _ := get(t, h, "/api/v1/missing"); code != http.StatusNotFound {
		t.Errorf("unknown api route = %d, want 404", code)
	}
	if code, body := get(t, h, "/assets/app.js"); code != http.StatusOK || body != "console.log(1)" {
		t.Errorf("asset = %d %q", code, body)
	}
	if code, body := get(t, h, "/devices/42"); code != http.StatusOK || body != "<html>app</html>" {
		t.Errorf("spa fallback = %d %q", code, body)
	}

	down := New(log, fakeDB{err: errors.New("down")}, fstest.MapFS{})
	if code, _ := get(t, down, "/healthz"); code != http.StatusServiceUnavailable {
		t.Errorf("healthz with db down = %d", code)
	}
	if code, _ := get(t, down, "/"); code != http.StatusServiceUnavailable {
		t.Errorf("unbuilt ui = %d", code)
	}
}
