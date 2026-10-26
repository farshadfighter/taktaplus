package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/farshadfighter/taktaplus/internal/auth"
)

const (
	sessionCookie = "taktaplus_session"
	// csrfHeader must accompany every state-changing browser request. A
	// cross-site page cannot set it without a CORS preflight, which we never
	// allow, so together with SameSite=Strict cookies this blocks CSRF.
	csrfHeader = "X-Requested-With"
	csrfValue  = "taktaplus"
	maxBody    = 1 << 20
)

type ctxKey int

const sessionKey ctxKey = iota

func sessionFrom(ctx context.Context) (auth.Session, bool) {
	s, ok := ctx.Value(sessionKey).(auth.Session)
	return s, ok
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// decodeJSON reads a size-limited JSON body into v, rejecting unknown fields.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// internalError logs err and answers with a generic message.
func internalError(log *slog.Logger, w http.ResponseWriter, r *http.Request, err error) {
	log.Error("request failed", "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}

func csrfGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if r.Header.Get(csrfHeader) != csrfValue {
				writeError(w, http.StatusForbidden, "missing "+csrfHeader+" header")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *srv) loadSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err == nil && c.Value != "" {
			sess, err := s.auth.Authenticate(r.Context(), c.Value)
			switch {
			case err == nil:
				r = r.WithContext(context.WithValue(r.Context(), sessionKey, sess))
			case !errors.Is(err, auth.ErrNoSession):
				internalError(s.log, w, r, err)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// requireUser admits fully signed-in users (password and MFA).
func requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, ok := sessionFrom(r.Context())
		if !ok || !sess.MFAPassed {
			writeError(w, http.StatusUnauthorized, "not signed in")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requirePerm admits signed-in users whose role grants p.
func requirePerm(p auth.Perm) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return requireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sess, _ := sessionFrom(r.Context())
			if !sess.User.Role.Can(p) {
				writeError(w, http.StatusForbidden, "permission denied")
				return
			}
			next.ServeHTTP(w, r)
		}))
	}
}

func isAPI(path string) bool { return strings.HasPrefix(path, "/api/") }
