package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/farshadfighter/taktaplus-lic/pkg/apitoken"

	"github.com/farshadfighter/taktaplus/internal/auth"
	"github.com/farshadfighter/taktaplus/internal/events"
)

// Live event stream settings.
const (
	eventsPing     = 25 * time.Second
	eventsLifetime = 15 * time.Minute // the browser reconnects; the session is checked again
)

// liveEvents streams events the user may see as Server-Sent Events. A panel
// session is checked without marking it active, so an idle tab still times
// out; the stream ends when the session does.
func (s *srv) liveEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}
	ctx := r.Context()
	sess, _ := sessionFrom(ctx)
	cookie := ""
	if c, err := r.Cookie(sessionCookie); err == nil {
		cookie = c.Value
	}
	alive := func() bool {
		if cookie == "" { // API token: checked on connect
			return true
		}
		_, err := s.auth.Peek(ctx, cookie)
		return err == nil
	}
	if apitoken.FromHeader(r.Header.Get("Authorization")) == "" && !alive() {
		writeError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	ch, behind, stop := events.Default.Subscribe(func(p string) bool { return sess.User.Role.Can(auth.Perm(p)) })
	defer stop()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no") // nginx and similar proxies
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, "retry: 5000\n: connected\n\n")
	fl.Flush()

	ping := time.NewTicker(eventsPing)
	defer ping.Stop()
	end := time.NewTimer(eventsLifetime)
	defer end.Stop()
	send := func(typ string, data any) bool {
		b, err := json.Marshal(data)
		if err != nil {
			return true
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", typ, b); err != nil {
			return false
		}
		fl.Flush()
		return true
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-end.C:
			return
		case <-ping.C:
			if !alive() {
				_ = send("signed_out", map[string]any{})
				return
			}
			if behind() && !send(events.Resync, map[string]any{}) {
				return
			}
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			fl.Flush()
		case e := <-ch:
			if !send(e.Type, e.Data) {
				return
			}
			if behind() && !send(events.Resync, map[string]any{}) {
				return
			}
		}
	}
}
