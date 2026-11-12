package api

import (
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-chi/chi/v5"

	"github.com/farshadfighter/taktaplus-lic/pkg/apidoc"

	"github.com/farshadfighter/taktaplus/internal/policy"
	"github.com/farshadfighter/taktaplus/internal/system"
)

// TestOpenAPICoversRoutes keeps apidoc_routes.go in step with the router.
func TestOpenAPICoversRoutes(t *testing.T) {
	h := New(Deps{Log: slog.New(slog.DiscardHandler), UI: fstest.MapFS{}, Policy: &policy.Service{}, System: &system.Service{}})
	var keys []string
	err := chi.Walk(h.(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if p, ok := strings.CutPrefix(route, "/api/v1"); ok && p != "/*" {
			keys = append(keys, method+" "+p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	missing, stale := apidoc.Diff(apiRoutes, keys)
	if len(missing) > 0 || len(stale) > 0 {
		t.Fatalf("regenerate apidoc_routes.go: undocumented %v, stale %v", missing, stale)
	}
	for _, r := range apiRoutes {
		if apiTags[r.Tag] == "" {
			t.Errorf("tag %q of %s has no description in apiTags", r.Tag, r.Key())
		}
		if r.Access == apidoc.Session && r.Perm == "" && r.Path != "/me" {
			t.Errorf("%s needs a session but names no permission", r.Key())
		}
	}
}
