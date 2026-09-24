package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/sse"
	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/testutil"
)

// shutdownSSERecorder reports the point at which a stream has committed its
// headers, without waiting for an event or a keepalive frame.
type shutdownSSERecorder struct {
	*httptest.ResponseRecorder
	flushed chan struct{}
	once    sync.Once
}

type shutdownFrameCapture struct {
	buf        bytes.Buffer
	firstFrame chan struct{}
	once       sync.Once
}

func (w *shutdownFrameCapture) Write(p []byte) (int, error) {
	n, err := w.buf.Write(p)
	if bytes.Contains(w.buf.Bytes(), []byte("data: ")) && bytes.Contains(w.buf.Bytes(), []byte("\n\n")) {
		w.once.Do(func() { close(w.firstFrame) })
	}
	return n, err
}

func (w *shutdownSSERecorder) Flush() {
	w.ResponseRecorder.Flush()
	w.once.Do(func() { close(w.flushed) })
}

func (w *shutdownSSERecorder) SetWriteDeadline(time.Time) error { return nil }

// blockedSSEWriter models a peer that stops reading just as an event is sent.
// Context cancellation alone cannot release Write; only a stream-scoped write
// deadline does. The fake honors the actual deadline rather than releasing as
// soon as one is set, so a future shutdown grace remains observable.
type blockedSSEWriter struct {
	header          http.Header
	flushed         chan struct{}
	writing         chan struct{}
	deadlineSet     chan struct{}
	deadlineCleared chan struct{}
	release         chan struct{}
	flushOnce       sync.Once
	writeOnce       sync.Once
	deadlineOnce    sync.Once
	clearOnce       sync.Once
	releaseOnce     sync.Once
	deadlineMu      sync.Mutex
	deadlineTimer   *time.Timer
}

func newBlockedSSEWriter() *blockedSSEWriter {
	return &blockedSSEWriter{
		header:          make(http.Header),
		flushed:         make(chan struct{}),
		writing:         make(chan struct{}),
		deadlineSet:     make(chan struct{}),
		deadlineCleared: make(chan struct{}),
		release:         make(chan struct{}),
	}
}

func (w *blockedSSEWriter) Header() http.Header { return w.header }

func (w *blockedSSEWriter) WriteHeader(int) {}

func (w *blockedSSEWriter) Write([]byte) (int, error) {
	w.writeOnce.Do(func() { close(w.writing) })
	<-w.release
	return 0, os.ErrDeadlineExceeded
}

func (w *blockedSSEWriter) Flush() {
	w.flushOnce.Do(func() { close(w.flushed) })
}

func (w *blockedSSEWriter) SetWriteDeadline(deadline time.Time) error {
	if deadline.IsZero() {
		w.deadlineMu.Lock()
		if w.deadlineTimer != nil {
			w.deadlineTimer.Stop()
			w.deadlineTimer = nil
		}
		w.deadlineMu.Unlock()
		w.clearOnce.Do(func() { close(w.deadlineCleared) })
		return nil
	}
	w.deadlineOnce.Do(func() { close(w.deadlineSet) })
	delay := time.Until(deadline)
	if delay <= 0 {
		w.unblock()
		return nil
	}
	w.deadlineMu.Lock()
	if w.deadlineTimer != nil {
		w.deadlineTimer.Stop()
	}
	w.deadlineTimer = time.AfterFunc(delay, w.unblock)
	w.deadlineMu.Unlock()
	return nil
}

func (w *blockedSSEWriter) unblock() {
	w.deadlineMu.Lock()
	if w.deadlineTimer != nil {
		w.deadlineTimer.Stop()
		w.deadlineTimer = nil
	}
	w.deadlineMu.Unlock()
	w.releaseOnce.Do(func() { close(w.release) })
}

func TestSupervisorShutdownDrainsTypedSSEStreams(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
	}{
		{name: "city integer cursor", path: "/v0/city/seeded/events/stream"},
		{name: "supervisor string cursor", path: "/v0/events/stream"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := newFakeState(t)
			state.cityName = "seeded"
			sm := newTestSupervisorMux(t, map[string]*fakeState{"seeded": state}).WithAnyHostAllowed()

			requestCtx, cancelRequest := context.WithCancel(context.Background())
			defer cancelRequest()
			w := &shutdownSSERecorder{ResponseRecorder: httptest.NewRecorder(), flushed: make(chan struct{})}
			done := make(chan struct{})
			go func() {
				defer close(done)
				sm.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil).WithContext(requestCtx))
			}()
			t.Cleanup(func() {
				cancelRequest()
				select {
				case <-done:
				case <-time.After(testutil.GoroutineRaceTimeout):
					t.Error("SSE handler did not exit during test cleanup")
				}
			})

			select {
			case <-w.flushed:
			case <-done:
				t.Fatalf("SSE handler returned before headers; status = %d", w.Code)
			case <-time.After(testutil.GoroutineRaceTimeout):
				t.Fatal("SSE headers were not committed")
			}

			shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), testutil.GoroutineRaceTimeout)
			defer cancelShutdown()
			if err := sm.Shutdown(shutdownCtx); err != nil {
				t.Fatalf("shutdown: %v", err)
			}
			select {
			case <-done:
			case <-shutdownCtx.Done():
				t.Fatal("SSE handler stayed active after supervisor shutdown")
			}
		})
	}
}

func TestSupervisorShutdownLeavesOrdinaryRequestActive(t *testing.T) {
	sm := newTestSupervisorMux(t, nil).WithAnyHostAllowed()
	entered := make(chan context.Context, 1)
	release := make(chan struct{})
	sm.WithAPIPlane(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- r.Context()
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))

	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		sm.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/held", nil))
	}()
	releaseRequest := sync.OnceFunc(func() { close(release) })
	t.Cleanup(func() {
		releaseRequest()
		select {
		case <-done:
		case <-time.After(testutil.GoroutineRaceTimeout):
			t.Error("ordinary request did not exit during test cleanup")
		}
	})

	var requestCtx context.Context
	select {
	case requestCtx = <-entered:
	case <-time.After(testutil.GoroutineRaceTimeout):
		t.Fatal("ordinary request did not enter handler")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), testutil.GoroutineRaceTimeout)
	defer cancel()
	if err := sm.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if err := requestCtx.Err(); err != nil {
		t.Fatalf("ordinary request context was canceled by SSE shutdown: %v", err)
	}
	releaseRequest()
	select {
	case <-done:
	case <-shutdownCtx.Done():
		t.Fatal("ordinary request did not complete after release")
	}
	if w.Code != http.StatusNoContent {
		t.Fatalf("ordinary request status = %d, want 204", w.Code)
	}
}

func TestSupervisorShutdownPreservesOrdinaryHTTPDrain(t *testing.T) {
	sm := newTestSupervisorMux(t, nil).WithAnyHostAllowed()
	entered := make(chan context.Context, 1)
	release := make(chan struct{})
	releaseRequest := sync.OnceFunc(func() { close(release) })
	defer releaseRequest()
	sm.WithAPIPlane(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- r.Context()
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	srv := newSupervisorHTTPTestServer(t, sm)

	clientCtx, cancelClient := context.WithTimeout(context.Background(), testutil.GoroutineRaceTimeout)
	defer cancelClient()
	req, err := http.NewRequestWithContext(clientCtx, http.MethodGet, srv.URL+"/api/held", nil)
	if err != nil {
		t.Fatalf("new ordinary request: %v", err)
	}
	type response struct {
		status int
		err    error
	}
	responseDone := make(chan response, 1)
	clientExited := make(chan struct{})
	go func() {
		defer close(clientExited)
		resp, err := srv.Client().Do(req)
		if err != nil {
			responseDone <- response{err: err}
			return
		}
		defer resp.Body.Close() //nolint:errcheck
		_, err = io.Copy(io.Discard, resp.Body)
		responseDone <- response{status: resp.StatusCode, err: err}
	}()
	t.Cleanup(func() {
		releaseRequest()
		cancelClient()
		select {
		case <-clientExited:
		case <-time.After(testutil.GoroutineRaceTimeout):
			t.Error("ordinary HTTP client did not exit during cleanup")
		}
	})

	var requestCtx context.Context
	select {
	case requestCtx = <-entered:
	case <-clientCtx.Done():
		t.Fatal("ordinary request did not reach the server")
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), testutil.GoroutineRaceTimeout)
	defer cancelShutdown()
	if err := sm.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("mux shutdown: %v", err)
	}
	shutdownStarted := make(chan struct{})
	srv.Config.RegisterOnShutdown(func() { close(shutdownStarted) })
	shutdownDone := make(chan error, 1)
	shutdownExited := make(chan struct{})
	go func() {
		defer close(shutdownExited)
		shutdownDone <- srv.Config.Shutdown(shutdownCtx)
	}()
	t.Cleanup(func() {
		releaseRequest()
		select {
		case <-shutdownExited:
		case <-time.After(testutil.GoroutineRaceTimeout):
			t.Error("external HTTP shutdown did not exit during cleanup")
		}
	})
	select {
	case <-shutdownStarted:
	case <-shutdownCtx.Done():
		t.Fatal("external HTTP server did not begin graceful shutdown")
	}
	if err := requestCtx.Err(); err != nil {
		t.Fatalf("ordinary in-flight request context was canceled: %v", err)
	}
	select {
	case err := <-shutdownDone:
		t.Fatalf("shutdown returned before ordinary request was released: %v", err)
	default:
	}

	releaseRequest()
	select {
	case result := <-responseDone:
		if result.err != nil || result.status != http.StatusNoContent {
			t.Fatalf("ordinary request result = %#v, want 204 without error", result)
		}
	case <-shutdownCtx.Done():
		t.Fatal("ordinary request did not complete after release")
	}
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("graceful external shutdown: %v", err)
		}
	case <-shutdownCtx.Done():
		t.Fatal("external HTTP shutdown did not complete after ordinary request")
	}
}

func TestSupervisorShutdownKeepsOrdinaryHTTPConnectionUsable(t *testing.T) {
	state := newFakeState(t)
	state.cityName = "seeded"
	sm := newTestSupervisorMux(t, map[string]*fakeState{"seeded": state}).WithAnyHostAllowed()
	sm.WithAPIPlane(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	srv := newSupervisorHTTPTestServer(t, sm)
	clientCtx, cancelClient := context.WithTimeout(context.Background(), testutil.GoroutineRaceTimeout)
	defer cancelClient()
	client := srv.Client()

	streamReq, err := http.NewRequestWithContext(clientCtx, http.MethodGet, srv.URL+"/v0/city/seeded/events/stream", nil)
	if err != nil {
		t.Fatalf("new stream request: %v", err)
	}
	streamResp, err := client.Do(streamReq)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer streamResp.Body.Close() //nolint:errcheck
	if streamResp.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d, want 200", streamResp.StatusCode)
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), testutil.GoroutineRaceTimeout)
	defer cancelShutdown()
	if err := sm.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("mux shutdown: %v", err)
	}
	if _, err := io.Copy(io.Discard, streamResp.Body); err != nil {
		t.Fatalf("stream did not end with clean EOF: %v", err)
	}
	if err := streamResp.Body.Close(); err != nil {
		t.Fatalf("close stream body: %v", err)
	}

	reused := false
	trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused }}
	ordinaryReq, err := http.NewRequestWithContext(httptrace.WithClientTrace(clientCtx, trace), http.MethodGet, srv.URL+"/api/ping", nil)
	if err != nil {
		t.Fatalf("new ordinary request: %v", err)
	}
	ordinaryResp, err := client.Do(ordinaryReq)
	if err != nil {
		t.Fatalf("ordinary request after SSE stop: %v", err)
	}
	defer ordinaryResp.Body.Close() //nolint:errcheck
	if ordinaryResp.StatusCode != http.StatusNoContent {
		t.Fatalf("ordinary response status = %d, want 204", ordinaryResp.StatusCode)
	}
	if !reused {
		t.Fatal("ordinary request did not reuse the stopped stream connection")
	}
}

func TestSupervisorShutdownDuringEventDeliveryKeepsHealthyStreamFramed(t *testing.T) {
	state := newFakeState(t)
	state.cityName = "seeded"
	sm := newTestSupervisorMux(t, map[string]*fakeState{"seeded": state}).WithAnyHostAllowed()
	srv := newSupervisorHTTPTestServer(t, sm)
	requestCtx, cancelRequest := context.WithTimeout(context.Background(), testutil.GoroutineRaceTimeout)
	defer cancelRequest()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, srv.URL+"/v0/city/seeded/events/stream", nil)
	if err != nil {
		t.Fatalf("new stream request: %v", err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d, want 200", resp.StatusCode)
	}

	captured := &shutdownFrameCapture{firstFrame: make(chan struct{})}
	type streamResult struct {
		body []byte
		err  error
	}
	readDone := make(chan streamResult, 1)
	readExited := make(chan struct{})
	go func() {
		defer close(readExited)
		_, err := io.Copy(captured, resp.Body)
		readDone <- streamResult{body: append([]byte(nil), captured.buf.Bytes()...), err: err}
	}()
	t.Cleanup(func() {
		cancelRequest()
		_ = resp.Body.Close()
		select {
		case <-readExited:
		case <-time.After(testutil.GoroutineRaceTimeout):
			t.Error("healthy SSE reader did not exit during cleanup")
		}
	})

	subject := strings.Repeat("x", 8192)
	recorder := state.eventProv.(*events.Fake)
	recorder.Record(events.Event{Type: events.SessionWoke, Actor: "gc", Subject: subject})
	select {
	case <-captured.firstFrame:
	case <-requestCtx.Done():
		t.Fatal("reader did not receive a complete event before shutdown")
	}
	for i := 0; i < 64; i++ {
		recorder.Record(events.Event{Type: events.SessionWoke, Actor: "gc", Subject: subject})
	}
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), testutil.GoroutineRaceTimeout)
	defer cancelShutdown()
	if err := sm.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("mux shutdown: %v", err)
	}
	select {
	case result := <-readDone:
		if result.err != nil {
			t.Fatalf("healthy SSE reader ended with error: %v", result.err)
		}
		if !bytes.HasSuffix(result.body, []byte("\n\n")) {
			t.Fatalf("SSE stream ended with an incomplete frame (%d bytes)", len(result.body))
		}
	case <-shutdownCtx.Done():
		t.Fatal("healthy SSE reader stayed open after shutdown")
	}
}

func TestSupervisorShutdownLateTypedSSEReturns(t *testing.T) {
	state := newFakeState(t)
	state.cityName = "seeded"
	sm := newTestSupervisorMux(t, map[string]*fakeState{"seeded": state}).WithAnyHostAllowed()
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), testutil.GoroutineRaceTimeout)
	defer cancelShutdown()
	if err := sm.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	requestCtx, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()
	w := &shutdownSSERecorder{ResponseRecorder: httptest.NewRecorder(), flushed: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		defer close(done)
		sm.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v0/city/seeded/events/stream", nil).WithContext(requestCtx))
	}()
	t.Cleanup(func() {
		cancelRequest()
		select {
		case <-done:
		case <-time.After(testutil.GoroutineRaceTimeout):
			t.Error("late SSE handler did not exit during test cleanup")
		}
	})
	select {
	case <-done:
	case <-shutdownCtx.Done():
		t.Fatal("late SSE request stayed active after supervisor shutdown")
	}
}

func TestSupervisorShutdownSkipsLateTypedSSECallbacks(t *testing.T) {
	sm := newTestSupervisorMux(t, nil).WithAnyHostAllowed()
	called := 0
	registerSSE(sm.humaAPI, huma.Operation{
		OperationID: "test-late-integer-sse",
		Method:      http.MethodGet,
		Path:        "/test/late-integer-sse",
	}, map[string]any{"heartbeat": HeartbeatEvent{}}, nil, sm.sseStreams,
		func(_ huma.Context, _ *struct{}, send sse.Sender) {
			called++
			_ = send(sse.Message{Data: HeartbeatEvent{}})
		})
	registerSSEStringID(sm.humaAPI, huma.Operation{
		OperationID: "test-late-string-sse",
		Method:      http.MethodGet,
		Path:        "/test/late-string-sse",
	}, map[string]any{"heartbeat": HeartbeatEvent{}}, nil, sm.sseStreams,
		func(_ huma.Context, _ *struct{}, send StringIDSender) {
			called++
			_ = send(StringIDMessage{Data: HeartbeatEvent{}})
		})
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), testutil.GoroutineRaceTimeout)
	defer cancelShutdown()
	if err := sm.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	for _, path := range []string{"/test/late-integer-sse", "/test/late-string-sse"} {
		w := &shutdownSSERecorder{ResponseRecorder: httptest.NewRecorder(), flushed: make(chan struct{})}
		sm.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK || w.Body.Len() != 0 {
			t.Fatalf("late %s response = status %d, body %q; want 200 and no frames", path, w.Code, w.Body.String())
		}
	}
	if called != 0 {
		t.Fatalf("late SSE callback ran %d times after shutdown, want none", called)
	}
}

func TestSSEStreamCallbackIsNotInvokedAfterStop(t *testing.T) {
	streams := newSSEStreamRegistry()
	streamCtx, lease, finish := streams.begin(context.Background(), nil)
	defer finish()

	if err := streams.stop(); err != nil {
		t.Fatalf("stop streams: %v", err)
	}
	called := false
	if streams.invoke(lease, streamCtx, func() { called = true }) || called {
		t.Fatal("stream callback ran after stop")
	}
}

func TestSeededSSEStreamContextCancelsWithParentSynchronously(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	streams := newSSEStreamRegistry()
	streams.setParent(parent)
	streamCtx, _, finish := streams.begin(context.Background(), nil)
	defer finish()

	cancelParent()
	if streamCtx.Err() == nil {
		t.Fatal("stream context was not canceled with its parent")
	}
}

func TestSupervisorShutdownUnblocksStalledSSEWrite(t *testing.T) {
	state := newFakeState(t)
	state.cityName = "seeded"
	sm := newTestSupervisorMux(t, map[string]*fakeState{"seeded": state}).WithAnyHostAllowed()
	w := newBlockedSSEWriter()
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		sm.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v0/city/seeded/events/stream", nil).WithContext(requestCtx))
	}()
	t.Cleanup(func() {
		w.unblock()
		cancelRequest()
		select {
		case <-done:
		case <-time.After(testutil.GoroutineRaceTimeout):
			t.Error("blocked SSE handler did not exit during test cleanup")
		}
	})

	select {
	case <-w.flushed:
	case <-time.After(testutil.GoroutineRaceTimeout):
		t.Fatal("SSE headers were not committed before blocked-write setup")
	}
	state.eventProv.(*events.Fake).Record(events.Event{Type: events.SessionWoke, Actor: "gc", Subject: "worker"})
	select {
	case <-w.writing:
	case <-time.After(testutil.GoroutineRaceTimeout):
		t.Fatal("event did not reach the SSE writer")
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), testutil.GoroutineRaceTimeout)
	defer cancelShutdown()
	if err := sm.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	select {
	case <-w.deadlineSet:
	case <-shutdownCtx.Done():
		t.Fatal("shutdown did not deadline the stalled SSE write")
	}
	select {
	case <-done:
	case <-shutdownCtx.Done():
		t.Fatal("SSE handler remained blocked in Write after shutdown")
	}
	select {
	case <-w.deadlineCleared:
	default:
		t.Fatal("SSE write deadline was not cleared before handler returned")
	}
}
