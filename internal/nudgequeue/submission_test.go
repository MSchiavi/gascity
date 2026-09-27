package nudgequeue

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func submissionFixture() (State, SubmissionAttempt) {
	a := SubmissionAttempt{ID: "attempt-1", NudgeIDs: []string{"nudge-1", "nudge-2"}, SessionID: "session-1", ContinuationEpoch: "3", SessionName: "worker", RuntimeToken: "instance-1", ProviderSessionID: "provider-1", ContentSHA256: "digest-1", StartedAt: time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)}
	s := State{InFlight: []Item{{ID: "nudge-1", SessionID: a.SessionID, ContinuationEpoch: a.ContinuationEpoch, Message: "same text"}, {ID: "nudge-2", SessionID: a.SessionID, ContinuationEpoch: a.ContinuationEpoch, Message: "same text"}}}
	return s, a
}

func TestSubmissionRoundTripSurvivesLegacyReader(t *testing.T) {
	s, a := submissionFixture()
	if err := BeginSubmission(&s, a); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	// The deployed reader knows these existing fields and ignores additions.
	// Its retry and retention paths inspect only in_flight and nonzero dead_at.
	type legacyItem struct {
		ID        string    `json:"id"`
		LastError string    `json:"last_error,omitempty"`
		DeadAt    time.Time `json:"dead_at,omitempty"`
	}
	var old struct {
		Pending  []legacyItem `json:"pending,omitempty"`
		InFlight []legacyItem `json:"in_flight,omitempty"`
		Dead     []legacyItem `json:"dead,omitempty"`
	}
	if err := json.Unmarshal(b, &old); err != nil {
		t.Fatal(err)
	}
	if len(old.Pending)+len(old.InFlight) != 0 || len(old.Dead) != 2 || !old.Dead[0].DeadAt.IsZero() {
		t.Fatalf("old reader can retry/prune held work: %+v", old)
	}
	b, err = json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	var restored State
	if err := json.Unmarshal(b, &restored); err != nil {
		t.Fatal(err)
	}
	if _, err := SubmissionItems(&restored, a); err != nil {
		t.Fatalf("legacy rewrite lost immutable attempt: %v", err)
	}
}

func TestSubmissionRejectsStaleResultWithoutPartialMutation(t *testing.T) {
	s, a := submissionFixture()
	if err := BeginSubmission(&s, a); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(s)
	for _, change := range []func(*SubmissionAttempt){
		func(a *SubmissionAttempt) { a.ID = "older-attempt" },
		func(a *SubmissionAttempt) { a.RuntimeToken = "replacement-instance" },
		func(a *SubmissionAttempt) { a.ProviderSessionID = "other-provider-session" },
		func(a *SubmissionAttempt) { a.ContentSHA256 = "different-body" },
		func(a *SubmissionAttempt) { a.NudgeIDs = []string{"nudge-1"} },
	} {
		wrong := a
		change(&wrong)
		if _, err := ResolveSubmission(&s, wrong, true); !errors.Is(err, ErrSubmissionUnconfirmed) {
			t.Fatalf("stale result error=%v", err)
		}
		after, _ := json.Marshal(s)
		if !reflect.DeepEqual(before, after) {
			t.Fatal("stale result mutated held work")
		}
	}
}

func TestSubmissionBeginValidatesWholeBatch(t *testing.T) {
	s, a := submissionFixture()
	a.NudgeIDs = append(a.NudgeIDs, "missing")
	before, _ := json.Marshal(s)
	if err := BeginSubmission(&s, a); err == nil {
		t.Fatal("partial batch accepted")
	}
	after, _ := json.Marshal(s)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("partial batch validation mutated state")
	}
}

func TestSubmissionRepeatedTextAndExplicitRetry(t *testing.T) {
	s, a := submissionFixture()
	if err := BeginSubmission(&s, a); err != nil {
		t.Fatal(err)
	}
	if err := HoldSubmission(&s, a, "no receipt"); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveSubmission(&s, a, true); err != nil {
		t.Fatal(err)
	}
	if len(s.Pending) != 2 || s.Pending[0].ID == s.Pending[1].ID || len(s.Dead) != 0 {
		t.Fatalf("retry merged distinct identical messages: %+v", s)
	}
}

func TestSubmissionExcludedFromWaitWithdrawal(t *testing.T) {
	s, a := submissionFixture()
	if err := BeginSubmission(&s, a); err != nil {
		t.Fatal(err)
	}
	if got := queuedWaitNudgeCandidates(&s, map[string]bool{"nudge-1": true, "nudge-2": true}); len(got) != 0 {
		t.Fatalf("wait withdrawal targets uncertain transport: %+v", got)
	}
	if _, err := SubmissionItems(&s, a); err != nil {
		t.Fatal(err)
	}
}
