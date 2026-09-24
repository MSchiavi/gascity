package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

const sseShutdownWriteGrace = time.Second

// sseStreamRegistry owns the lifecycle of active typed SSE responses. Stop
// cancels their request contexts and gives in-progress writes a short,
// shutdown-only grace period before their deadlines expire.
type sseStreamRegistry struct {
	mu       sync.Mutex
	stopping bool
	active   map[*sseStreamLease]struct{}
	stopDone chan struct{}
	stopErr  error
}

func newSSEStreamRegistry() *sseStreamRegistry {
	return &sseStreamRegistry{
		active:   make(map[*sseStreamLease]struct{}),
		stopDone: make(chan struct{}),
	}
}

// begin returns a stream-only context derived from the request context. The
// returned finish function unregisters the stream and clears any shutdown
// deadline before net/http finalizes the response.
func (r *sseStreamRegistry) begin(parent context.Context, writer http.ResponseWriter) (context.Context, func()) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	lease := &sseStreamLease{cancel: cancel}
	if writer != nil {
		lease.response = http.NewResponseController(writer)
	}

	r.register(lease)

	var once sync.Once
	finish := func() {
		once.Do(func() {
			r.unregister(lease)
			if err := lease.finish(); err != nil {
				log.Printf("api: clearing SSE shutdown write deadline: %v", err)
			}
		})
	}
	return ctx, finish
}

// register adds a live response unless shutdown has started. A late response
// is canceled immediately, covering requests that passed Huma precheck before
// Shutdown but entered their stream body after it began.
func (r *sseStreamRegistry) register(lease *sseStreamLease) {
	r.mu.Lock()
	if r.stopping {
		r.mu.Unlock()
		lease.cancelOnly()
		return
	}
	r.active[lease] = struct{}{}
	r.mu.Unlock()
}

func (r *sseStreamRegistry) unregister(lease *sseStreamLease) {
	r.mu.Lock()
	delete(r.active, lease)
	r.mu.Unlock()
}

// stop cancels every active stream and gives its response writer one shared
// future deadline. It does not hold the registry lock while touching a writer;
// finish and stop serialize through each lease so a late deadline cannot affect
// a keep-alive request after the stream has returned.
func (r *sseStreamRegistry) stop() error {
	r.mu.Lock()
	if r.stopping {
		done := r.stopDone
		r.mu.Unlock()
		<-done
		r.mu.Lock()
		err := r.stopErr
		r.mu.Unlock()
		return err
	}
	r.stopping = true
	leases := make([]*sseStreamLease, 0, len(r.active))
	for lease := range r.active {
		leases = append(leases, lease)
	}
	clear(r.active)
	r.mu.Unlock()

	deadline := time.Now().Add(sseShutdownWriteGrace)
	for _, lease := range leases {
		lease.cancelOnly()
	}
	var stopErr error
	for _, lease := range leases {
		stopErr = errors.Join(stopErr, lease.setWriteDeadline(deadline))
	}

	r.mu.Lock()
	r.stopErr = stopErr
	close(r.stopDone)
	r.mu.Unlock()
	if stopErr != nil {
		log.Printf("api: stopping SSE streams: %v", stopErr)
	}
	return stopErr
}

type sseStreamLease struct {
	mu          sync.Mutex
	finished    bool
	deadlineSet bool
	cancel      context.CancelFunc
	response    *http.ResponseController
}

func (l *sseStreamLease) cancelOnly() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.finished {
		l.cancel()
	}
}

func (l *sseStreamLease) setWriteDeadline(deadline time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.finished {
		return nil
	}
	if l.response == nil {
		return errors.New("response writer does not implement http.ResponseWriter")
	}
	if err := l.response.SetWriteDeadline(deadline); err != nil {
		return fmt.Errorf("set response write deadline: %w", err)
	}
	l.deadlineSet = true
	return nil
}

func (l *sseStreamLease) finish() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.finished {
		return nil
	}
	l.finished = true
	l.cancel()
	if !l.deadlineSet {
		return nil
	}
	l.deadlineSet = false
	if err := l.response.SetWriteDeadline(time.Time{}); err != nil {
		return fmt.Errorf("reset response write deadline: %w", err)
	}
	return nil
}
