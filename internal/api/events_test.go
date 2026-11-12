package api

import (
	"bufio"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/farshadfighter/taktaplus/internal/auth"
	"github.com/farshadfighter/taktaplus/internal/events"
)

// readEvents collects "event:" names from an SSE stream until stop says so.
func readEvents(t *testing.T, resp *http.Response, want int) []string {
	t.Helper()
	var got []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if name, ok := strings.CutPrefix(sc.Text(), "event: "); ok {
				got = append(got, name)
				if len(got) == want {
					return
				}
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out; got %v", got)
	}
	return got
}

func TestLiveEvents(t *testing.T) {
	h := newHarness(t)
	h.signIn("viewer", auth.RoleViewer)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", h.ts.URL+"/api/v1/events", nil)
	resp, err := h.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("events = %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	// Wait for the subscription, then publish: the viewer must not see the
	// upgrade event (settings.manage) but must see the device and alert events.
	deadline := time.Now().Add(3 * time.Second)
	for events.Default.Subscribers() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	events.Publish(events.Upgrade, string(auth.PermSettings), map[string]any{"id": 1})
	events.Publish(events.Device, string(auth.PermDevicesRead), map[string]any{"id": 7})
	events.Publish(events.Alert, "", map[string]any{"id": 3, "severity": "critical"})
	got := readEvents(t, resp, 2)
	if strings.Join(got, ",") != "device,alert" {
		t.Fatalf("events %v", got)
	}

	// Without a session there is no stream.
	resp2, err := http.Get(h.ts.URL + "/api/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", resp2.StatusCode)
	}
}
