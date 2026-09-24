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
// These assertions reuse an existing harness so the shutdown coverage does
// not create additional listeners outside resourcecensus ownership.
func assertSeededSSEDrainsBeforeExternalShutdown(t *testing.T, h *harness, path string, stop func()) {
	t.Helper()
	requestCtx, cancelRequest := context.WithTimeout(context.Background(), testutil.GoroutineRaceTimeout)
	defer cancelRequest()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, path, nil)
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

	stop()
	// Five seconds is the seeded binary's actual shutdown budget, not a sleep
	// or a test polling interval.
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	if err := h.server.Config.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("graceful shutdown with active SSE: %v", err)
	}
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatalf("stream did not end with clean EOF: %v", err)
	}
}
