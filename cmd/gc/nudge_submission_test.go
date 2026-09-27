package main

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/nudgequeue"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/runtime/tmux"
	"github.com/gastownhall/gascity/internal/session"
	"github.com/gastownhall/gascity/internal/worker"
)

type recordingNudgeProvider struct {
	*runtime.Fake
	send func()
	err  error
}

func (p *recordingNudgeProvider) Nudge(_ string, _ []runtime.ContentBlock) error {
	p.send()
	return p.err
}

// A possibly admitted send must already be non-replayable when the provider is called.
func TestQueuedSubmissionPersistsBeforeProviderCall(t *testing.T) {
	t.Setenv("GC_BEADS", "file")
	dir := t.TempDir()
	item := newQueuedNudge("worker", "check work", time.Now().Add(-time.Minute))
	if err := enqueueQueuedNudge(dir, item); err != nil {
		t.Fatal(err)
	}
	p := &recordingNudgeProvider{Fake: runtime.NewFake(), err: tmux.ErrNudgeSubmitUnconfirmed}
	if err := p.Start(context.Background(), "worker", runtime.Config{}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	p.send = func() {
		calls++
		state, err := nudgequeue.LoadState(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(state.Pending) != 0 || len(state.InFlight) != 0 || len(state.Dead) != 1 || state.Dead[0].LastError == "" {
			t.Fatalf("provider entered before durable quarantine: pending/in-flight/dead=%d/%d/%d", len(state.Pending), len(state.InFlight), len(state.Dead))
		}
	}
	target := nudgeTarget{cityPath: dir, agent: config.Agent{Name: "worker"}, sessionName: "worker"}
	delivered, err := tryDeliverQueuedNudgesByPoller(target, nil, nil, p, 0, worker.LiveObservation{Running: true})
	if delivered || !errors.Is(err, tmux.ErrNudgeSubmitUnconfirmed) {
		t.Fatalf("delivery=%v err=%v", delivered, err)
	}
	if calls != 1 {
		t.Fatalf("provider calls=%d, want 1", calls)
	}
	// A replacement dispatcher must not submit it again, even after all timers expire.
	state, err := nudgequeue.LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := recoverExpiredInFlightNudges(&state, nil, time.Now().Add(48*time.Hour), noMaintenanceDeadline()); err != nil {
		t.Fatal(err)
	}
	if len(state.Pending) != 0 || len(state.Dead) != 1 {
		t.Fatalf("uncertain send became replayable: %+v", state)
	}
}

func TestQueuedSubmissionSurvivesCallerCrash(t *testing.T) {
	t.Setenv("GC_BEADS", "file")
	dir := t.TempDir()
	item := newQueuedNudge("worker", "check work", time.Now().Add(-time.Minute))
	if err := enqueueQueuedNudge(dir, item); err != nil {
		t.Fatal(err)
	}
	p := &recordingNudgeProvider{Fake: runtime.NewFake()}
	if err := p.Start(context.Background(), "worker", runtime.Config{}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	p.send = func() { calls++; panic("caller crashed after transport admitted input") }
	target := nudgeTarget{cityPath: dir, agent: config.Agent{Name: "worker"}, sessionName: "worker"}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("crash fixture did not reach transport")
			}
		}()
		_, _ = tryDeliverQueuedNudgesByPoller(target, nil, nil, p, 0, worker.LiveObservation{Running: true})
	}()
	if _, err := tryDeliverQueuedNudgesByPoller(target, nil, nil, p, 0, worker.LiveObservation{Running: true}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("replacement dispatcher replayed submission: calls=%d", calls)
	}
	state, err := nudgequeue.LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, blocked := splitBlockedNudgeSubmissions(state.Dead)
	if len(blocked) != 1 || blocked[0].State != "submission_unconfirmed" {
		t.Fatalf("crash is not visibly blocked: %+v", blocked)
	}
}

type recordingAdmissionHandle struct {
	worker.Handle
	send func(worker.NudgeRequest) (worker.NudgeResult, error)
}

func (h *recordingAdmissionHandle) Nudge(_ context.Context, req worker.NudgeRequest) (worker.NudgeResult, error) {
	return h.send(req)
}

func TestQueuedSubmissionAdmissionWaitsForObservedOutcome(t *testing.T) {
	t.Setenv("GC_BEADS", "file")
	dir := t.TempDir()
	store := beads.NewMemStore()
	fake := runtime.NewFake()
	mgr := newSessionManagerWithConfig(dir, beads.NudgesStore{Store: store}, fake, nil)
	info, err := mgr.CreateSession(context.Background(), session.CreateOptions{Template: "worker", Command: "codex", Provider: "codex", WorkDir: dir, ExtraMeta: map[string]string{"session_origin": "manual"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Start(context.Background(), info.ID, "", runtime.Config{WorkDir: dir}); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"instance_token": "instance-one", "continuation_epoch": "1"} {
		if err := store.SetMetadata(info.ID, k, v); err != nil {
			t.Fatal(err)
		}
	}
	target := nudgeTarget{cityPath: dir, agent: config.Agent{Name: "worker"}, sessionName: info.SessionName, sessionID: info.ID, continuationEpoch: "1"}
	item := newQueuedNudge("worker", "check work", time.Now().Add(-time.Minute))
	if err := enqueueQueuedNudgeWithStore(dir, beads.NudgesStore{Store: store}, item); err != nil {
		t.Fatal(err)
	}
	oldHandle, oldSupports, oldRead, oldTarget := nudgeWorkerHandleForTarget, nudgeSupportsAdmission, nudgeReadReceipt, nudgeAdmissionTarget
	t.Cleanup(func() {
		nudgeWorkerHandleForTarget, nudgeSupportsAdmission, nudgeReadReceipt, nudgeAdmissionTarget = oldHandle, oldSupports, oldRead, oldTarget
	})
	calls := 0
	var submitted runtime.AdmissionRequest
	handle := &recordingAdmissionHandle{send: func(req worker.NudgeRequest) (worker.NudgeResult, error) {
		calls++
		if req.Admission == nil {
			t.Fatal("managed queue omitted stable admission identity")
		}
		submitted = *req.Admission
		state, err := nudgequeue.LoadState(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(state.Dead) != 1 || len(state.InFlight) != 0 {
			t.Fatal("managed request entered before persisted attempt")
		}
		s, err := nudgequeue.DecodeSubmission(state.Dead[0])
		if err != nil {
			t.Fatal(err)
		}
		if s.Attempt.ID != submitted.CommandID || s.Attempt.Text != submitted.Text {
			t.Fatal("transport identity/body differ from durable attempt")
		}
		if err := submitted.Validate(); err != nil {
			t.Fatal(err)
		}
		r := runtime.AdmissionReceipt{CommandID: submitted.CommandID, ProviderSessionID: submitted.ProviderSessionID, Fence: submitted.Fence, State: "accepted", TurnID: "turn-one", Disposition: "started"}
		return worker.NudgeResult{Admission: &r}, nil
	}}
	nudgeWorkerHandleForTarget = func(nudgeTarget, beads.Store, runtime.Provider) (worker.Handle, error) { return handle, nil }
	nudgeSupportsAdmission = func(worker.Handle) bool { return true }
	nudgeAdmissionTarget = func(context.Context, worker.Handle) (runtime.AdmissionTarget, error) {
		return runtime.AdmissionTarget{ProviderSessionID: "provider-uuid", Fence: runtime.AdmissionFence{SessionID: info.ID, ContinuationEpoch: "1", RuntimeToken: "instance-one"}}, nil
	}
	nudgeReadReceipt = func(_ context.Context, _ worker.Handle, req runtime.AdmissionRequest) (runtime.AdmissionReceipt, error) {
		if req != submitted {
			t.Fatal("receipt query changed original immutable request")
		}
		return runtime.AdmissionReceipt{CommandID: req.CommandID, ProviderSessionID: req.ProviderSessionID, Fence: req.Fence, State: "accepted", TurnID: "turn-one", Terminal: "completed"}, nil
	}
	// Busy state cannot prevent protocol queue admission. It must still retain accepted input.
	obs := worker.LiveObservation{Running: true}
	if _, err := tryDeliverQueuedNudgesByPoller(target, store, store, fake, time.Hour, obs); err != nil {
		t.Fatal(err)
	}
	state, err := nudgequeue.LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, blocked := splitBlockedNudgeSubmissions(state.Dead)
	if len(blocked) != 1 || blocked[0].State != "admitted_awaiting_outcome" {
		t.Fatalf("accepted response prematurely acked: %+v", state)
	}
	if !shouldKeepNudgePollerAlive(target, time.Time{}, time.Now()) {
		t.Fatal("standalone poller exits with an unresolved managed receipt")
	}
	if !nudgePollTargetHasDueWork(target, time.Now()) {
		t.Fatal("held receipt has no polling path")
	}
	// A runtime restart advances the live epoch. Receipt lookup must retain the
	// original request and fence instead of submitting again or abandoning it.
	target.continuationEpoch = "2"
	if err := store.SetMetadata(info.ID, "continuation_epoch", "2"); err != nil {
		t.Fatal(err)
	}
	if _, err := tryDeliverQueuedNudgesByPoller(target, store, store, fake, time.Hour, obs); err != nil {
		t.Fatal(err)
	}
	state, err = nudgequeue.LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Dead)+len(state.Pending)+len(state.InFlight) != 0 || calls != 1 {
		t.Fatalf("receipt recovery lost/replayed work: state=%+v calls=%d", state, calls)
	}
}

func heldSubmissionFixture(t *testing.T) (string, nudgequeue.SubmissionAttempt) {
	t.Helper()
	t.Setenv("GC_BEADS", "file")
	dir := t.TempDir()
	a := nudgequeue.SubmissionAttempt{ID: "attempt-one", NudgeIDs: []string{"one"}, SessionName: "worker", SessionID: "gc-session", ContinuationEpoch: "1", RuntimeToken: "instance", ProviderSessionID: "provider", ContentSHA256: "digest", Text: "body", StartedAt: time.Now(), ReceiptCapable: true}
	err := withNudgeQueueState(dir, func(s *nudgeQueueState) error {
		s.InFlight = append(s.InFlight, queuedNudge{ID: "one", Agent: "worker", Message: "body"})
		return nudgequeue.BeginSubmission(s, a)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dir, a
}

func TestQueuedSubmissionNotAdmittedUsesBoundedBackoff(t *testing.T) {
	dir, a := heldSubmissionFixture(t)
	r := runtime.AdmissionReceipt{CommandID: a.ID, ProviderSessionID: a.ProviderSessionID, Fence: queuedNudgeAdmissionRequest(a).Fence, State: "not_admitted", Reason: "command rejected"}
	if err := applyQueuedNudgeAdmissionReceipt(dir, nil, a, r); err != nil {
		t.Fatal(err)
	}
	s, err := nudgequeue.LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Pending) != 1 || s.Pending[0].Attempts != 1 || !s.Pending[0].DeliverAfter.After(time.Now()) {
		t.Fatalf("rejected admission retried without bounded backoff: %+v", s)
	}
}

func TestQueuedSubmissionUnknownPreservesAcceptedAndOperatorFence(t *testing.T) {
	dir, a := heldSubmissionFixture(t)
	r := runtime.AdmissionReceipt{CommandID: a.ID, ProviderSessionID: a.ProviderSessionID, Fence: queuedNudgeAdmissionRequest(a).Fence, State: "accepted", TurnID: "turn"}
	if err := applyQueuedNudgeAdmissionReceipt(dir, nil, a, r); err != nil {
		t.Fatal(err)
	}
	r.State = "unknown"
	r.Reason = "reader unavailable"
	if err := applyQueuedNudgeAdmissionReceipt(dir, nil, a, r); err != nil {
		t.Fatal(err)
	}
	s, err := nudgequeue.LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	held, err := nudgequeue.DecodeSubmission(s.Dead[0])
	if err != nil {
		t.Fatal(err)
	}
	if held.Admission != "accepted" {
		t.Fatalf("unknown query erased durable acceptance: %+v", held)
	}
	r.State = "not_admitted"
	if err := applyQueuedNudgeAdmissionReceipt(dir, nil, a, r); !errors.Is(err, nudgequeue.ErrSubmissionUnconfirmed) {
		t.Fatalf("negative receipt contradicted durable acceptance: %v", err)
	}
	if err := doNudgeResolve(dir, "one", "stale-command", "retry"); !errors.Is(err, nudgequeue.ErrSubmissionUnconfirmed) {
		t.Fatalf("stale operator resolution: %v", err)
	}
	if err := doNudgeResolve(dir, "one", a.ID, "retry"); err != nil {
		t.Fatal(err)
	}
	s, err = nudgequeue.LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Pending) != 1 || len(s.Dead) != 0 {
		t.Fatalf("explicit retry did not release exact held work: %+v", s)
	}
}

func TestQueuedSubmissionFailedOutcomeDoesNotReplay(t *testing.T) {
	dir, a := heldSubmissionFixture(t)
	r := runtime.AdmissionReceipt{CommandID: a.ID, ProviderSessionID: a.ProviderSessionID, Fence: queuedNudgeAdmissionRequest(a).Fence, State: "accepted", TurnID: "turn", Terminal: "canceled", Reason: "orphaned by process loss"}
	if err := applyQueuedNudgeAdmissionReceipt(dir, nil, a, r); err != nil {
		t.Fatal(err)
	}
	s, err := nudgequeue.LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Pending) != 0 || len(s.Dead) != 1 || nudgequeue.IsSubmission(s.Dead[0]) || s.Dead[0].DeadAt.IsZero() {
		t.Fatalf("failed observed outcome replayed or lost: %+v", s)
	}
}

func TestQueuedSubmissionMaintenanceCannotExpireHeldWork(t *testing.T) {
	dir, a := heldSubmissionFixture(t)
	now := time.Now().Add(30 * 24 * time.Hour)
	if err := withNudgeQueueState(dir, func(s *nudgeQueueState) error {
		// Even an old maintenance implementation setting a dead timestamp must not
		// authorize the new reader to delete unresolved transport evidence.
		s.Dead[0].DeadAt = time.Now().Add(-30 * 24 * time.Hour)
		if err := recoverExpiredInFlightNudges(s, nil, now, noMaintenanceDeadline()); err != nil {
			return err
		}
		if err := pruneExpiredQueuedNudges(s, nil, now, noMaintenanceDeadline()); err != nil {
			return err
		}
		return pruneDeadQueuedNudges(s, nil, now, noMaintenanceDeadline())
	}); err != nil {
		t.Fatal(err)
	}
	s, err := nudgequeue.LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := nudgequeue.SubmissionItems(&s, a); err != nil {
		t.Fatalf("maintenance lost held attempt: %v", err)
	}
}

func TestQueuedSubmissionSurvivesSupersession(t *testing.T) {
	dir, a := heldSubmissionFixture(t)
	if err := withNudgeQueueState(dir, func(s *nudgeQueueState) error {
		s.Dead[0].Source = "mail"
		s.Dead[0].Reference = &nudgequeue.Reference{Kind: "mail", ID: "mail-one"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	next := newQueuedNudge("worker", "new reminder", time.Now())
	next.Source = "mail"
	next.Reference = &nudgequeue.Reference{Kind: "mail", ID: "mail-one"}
	if err := enqueueQueuedNudge(dir, next); err != nil {
		t.Fatal(err)
	}
	s, err := nudgequeue.LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := nudgequeue.SubmissionItems(&s, a); err != nil {
		t.Fatalf("supersession removed uncertain receipt: %v", err)
	}
	if len(s.Pending) != 1 || len(s.Dead) != 1 {
		t.Fatalf("new intent changed held attempt: %+v", s)
	}
}

func TestQueuedSubmissionNotAdmittedStopsAtAttemptLimit(t *testing.T) {
	dir, a := heldSubmissionFixture(t)
	if err := withNudgeQueueState(dir, func(s *nudgeQueueState) error { s.Dead[0].Attempts = defaultQueuedNudgeMaxAttempts - 1; return nil }); err != nil {
		t.Fatal(err)
	}
	r := runtime.AdmissionReceipt{CommandID: a.ID, ProviderSessionID: a.ProviderSessionID, Fence: queuedNudgeAdmissionRequest(a).Fence, State: "not_admitted", Reason: "command rejected"}
	if err := applyQueuedNudgeAdmissionReceipt(dir, nil, a, r); err != nil {
		t.Fatal(err)
	}
	s, err := nudgequeue.LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Pending) != 0 || len(s.Dead) != 1 || nudgequeue.IsSubmission(s.Dead[0]) || s.Dead[0].Attempts != defaultQueuedNudgeMaxAttempts {
		t.Fatalf("rejected command churned beyond limit: %+v", s)
	}
}

func TestWorkerHandleForNudgeTargetBindsAdmissionToSession(t *testing.T) {
	t.Setenv("GC_BEADS", "file")
	dir := t.TempDir()
	store := beads.NewMemStore()
	fake := runtime.NewFake()
	mgr := newSessionManagerWithConfig(dir, beads.NudgesStore{Store: store}, fake, nil)
	info, err := mgr.CreateSession(context.Background(), session.CreateOptions{Template: "worker", Command: "codex", Provider: "codex", WorkDir: dir, ExtraMeta: map[string]string{"session_origin": "manual"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Start(context.Background(), info.ID, "", runtime.Config{WorkDir: dir}); err != nil {
		t.Fatal(err)
	}
	target := nudgeTarget{cityPath: dir, agent: config.Agent{Name: "worker"}, sessionName: info.SessionName, sessionID: info.ID}
	old := nudgeSupportsAdmission
	t.Cleanup(func() { nudgeSupportsAdmission = old })
	nudgeSupportsAdmission = func(worker.Handle) bool { return true }
	handle, err := workerHandleForNudgeTarget(target, store, fake)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := handle.(*worker.SessionHandle); !ok {
		t.Fatalf("managed known session uses %T; current bead fence cannot be validated", handle)
	}
}

func TestQueuedSubmissionStatusSchemaIncludesBlockedIdentity(t *testing.T) {
	dir, a := heldSubmissionFixture(t)
	s, err := nudgequeue.LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	dead, blocked := splitBlockedNudgeSubmissions(s.Dead)
	var output bytes.Buffer
	if err := writeCLIJSONLine(&output, nudgeStatusJSON{SchemaVersion: "1", Command: "nudge status", CityPath: dir, Agent: "worker", Session: "worker", Counts: nudgeStatusCounts{Blocked: 1}, Pending: nonNilQueuedNudges(nil), InFlight: nonNilQueuedNudges(nil), Dead: nonNilQueuedNudges(dead), Blocked: blocked}); err != nil {
		t.Fatal(err)
	}
	validateJSONResultSchema(t, []string{"nudge", "status"}, output.Bytes())
	if len(blocked) != 1 || blocked[0].Submission.Attempt.ID != a.ID {
		t.Fatalf("status lost immutable attempt: %+v", blocked)
	}
}
