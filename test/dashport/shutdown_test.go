//go:build integration

package dashport_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/testutil"
)

// ServeSeededCity is hosted by an external http.Server in the browser harness.
// Its parent cancellation and stop callback must drain typed streams before
// that server's graceful shutdown waits for active requests to finish.
func TestSeededSSEDrainsBeforeExternalShutdown(t *testing.T) {
	for _, tc := range []struct {
		name    string
		path    func(*harness) string
		stopSSE func(*harness)
	}{
		{
			name:    "city stream on parent cancellation",
			path:    func(h *harness) string { return h.cityURL("/events/stream") },
			stopSSE: func(h *harness) { h.cancel() },
		},
		{
			name: "global stream on stop callback",
			path: func(h *harness) string { return h.rootURL("/v0/events/stream") },
			stopSSE: func(h *harness) {
				h.stop()
				h.stop() // teardown is safe to repeat
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			requestCtx, cancelRequest := context.WithTimeout(context.Background(), testutil.GoroutineRaceTimeout)
			defer cancelRequest()
			req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, tc.path(h), nil)
			if err != nil {
				t.Fatalf("new stream request: %v", err)
			}
			resp, err := h.client.Do(req)
			if err != nil {
				t.Fatalf("open stream: %v", err)
			}
			defer resp.Body.Close() //nolint:errcheck
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("stream status = %d, want 200", resp.StatusCode)
			}
			if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
				t.Fatalf("stream content type = %q, want text/event-stream", got)
			}

			tc.stopSSE(h)
			// Five seconds is the seeded binary's actual shutdown budget, not
			// a sleep or a test polling interval.
			shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelShutdown()
			if err := h.server.Config.Shutdown(shutdownCtx); err != nil {
				t.Fatalf("graceful shutdown with active SSE: %v", err)
			}
			if _, err := io.Copy(io.Discard, resp.Body); err != nil {
				t.Fatalf("stream did not end with clean EOF: %v", err)
			}
		})
	}
}
