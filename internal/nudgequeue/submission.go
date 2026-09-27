package nudgequeue

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// ErrSubmissionUnconfirmed means a submission may have reached the provider.
// Only a matching result or an explicit operator decision can release it.
var ErrSubmissionUnconfirmed = errors.New("queued nudge submission unconfirmed")

const submissionPrefix = "gc-submission-v1:"

// SubmissionAttempt identifies one immutable transport call, not model execution.
type SubmissionAttempt struct {
	ID                string    `json:"id"`
	NudgeIDs          []string  `json:"nudge_ids"`
	SessionID         string    `json:"session_id,omitempty"`
	ContinuationEpoch string    `json:"continuation_epoch,omitempty"`
	SessionName       string    `json:"session_name"`
	RuntimeToken      string    `json:"runtime_token,omitempty"`
	ProviderSessionID string    `json:"provider_session_id,omitempty"`
	ContentSHA256     string    `json:"content_sha256"`
	Text              string    `json:"text"`
	StartedAt         time.Time `json:"started_at"`
	ClaimedAt         time.Time `json:"claimed_at"`
	ReceiptCapable    bool      `json:"receipt_capable,omitempty"`
}

// Submission is the typed projection of a quarantined transport attempt.
type Submission struct {
	Attempt   SubmissionAttempt `json:"attempt"`
	Reason    string            `json:"reason"`
	Admission string            `json:"admission,omitempty"`
	TurnID    string            `json:"turn_id,omitempty"`
}

// IsSubmission recognizes the envelope even if damaged, so corrupt evidence
// cannot authorize automatic resubmission or deletion.
func IsSubmission(item Item) bool { return strings.HasPrefix(item.LastError, submissionPrefix) }

// DecodeSubmission reads the compatibility envelope in LastError. It lives in
// the existing dead bucket with a zero DeadAt: older binaries preserve LastError
// and cannot retry or prune the record. A new ignored field would allow replay.
func DecodeSubmission(item Item) (Submission, error) {
	var s Submission
	if !IsSubmission(item) {
		return s, fmt.Errorf("nudge %q has no held submission", item.ID)
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(item.LastError, submissionPrefix)), &s); err != nil {
		return s, fmt.Errorf("decode submission for %q: %w", item.ID, err)
	}
	return s, nil
}

func submissionEnvelope(s Submission) (string, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return submissionPrefix + string(b), nil
}

// BeginSubmission moves the complete immutable batch out of all retryable
// buckets. Call under WithState and commit before entering the transport.
func BeginSubmission(state *State, attempt SubmissionAttempt) error {
	if attempt.ID == "" || attempt.SessionName == "" || attempt.ContentSHA256 == "" || len(attempt.NudgeIDs) == 0 {
		return errors.New("submission requires identity, target, content digest, and nudge IDs")
	}
	want := make(map[string]bool, len(attempt.NudgeIDs))
	for _, id := range attempt.NudgeIDs {
		if id == "" || want[id] {
			return errors.New("submission has empty or duplicate nudge ID")
		}
		want[id] = true
	}
	found := 0
	for _, item := range state.InFlight {
		if want[item.ID] {
			if !item.ClaimedAt.Equal(attempt.ClaimedAt) {
				return fmt.Errorf("submission claim changed for nudge %q", item.ID)
			}
			found++
		}
	}
	if found != len(want) {
		return errors.New("submission batch is no longer wholly claimed")
	}
	envelope, err := submissionEnvelope(Submission{Attempt: attempt, Reason: "transport result pending"})
	if err != nil {
		return err
	}
	kept := state.InFlight[:0]
	for _, item := range state.InFlight {
		if !want[item.ID] {
			kept = append(kept, item)
			continue
		}
		item.LastError = envelope
		item.DeadAt = time.Time{}
		item.LastAttemptAt = attempt.StartedAt
		state.Dead = append(state.Dead, item)
	}
	state.InFlight = kept
	return nil
}

func sameAttempt(a, b SubmissionAttempt) bool {
	return a.ID == b.ID && slices.Equal(a.NudgeIDs, b.NudgeIDs) && a.SessionID == b.SessionID && a.ContinuationEpoch == b.ContinuationEpoch && a.SessionName == b.SessionName && a.RuntimeToken == b.RuntimeToken && a.ProviderSessionID == b.ProviderSessionID && a.ContentSHA256 == b.ContentSHA256 && a.Text == b.Text && a.StartedAt.Equal(b.StartedAt) && a.ClaimedAt.Equal(b.ClaimedAt) && a.ReceiptCapable == b.ReceiptCapable
}

// SubmissionItems returns a complete batch only when its entire original fence
// matches. Mutable current target state cannot acknowledge an earlier attempt.
func SubmissionItems(state *State, attempt SubmissionAttempt) ([]Item, error) {
	var items []Item
	for _, item := range state.Dead {
		if !slices.Contains(attempt.NudgeIDs, item.ID) {
			continue
		}
		s, err := DecodeSubmission(item)
		if err != nil || !sameAttempt(s.Attempt, attempt) {
			return nil, fmt.Errorf("submission fence for nudge %q: %w", item.ID, ErrSubmissionUnconfirmed)
		}
		items = append(items, item)
	}
	if len(items) != len(attempt.NudgeIDs) {
		return nil, fmt.Errorf("submission batch no longer complete: %w", ErrSubmissionUnconfirmed)
	}
	return items, nil
}

// HoldSubmission records uncertainty without changing the pinned attempt.
func HoldSubmission(state *State, attempt SubmissionAttempt, reason string) error {
	items, err := SubmissionItems(state, attempt)
	if err != nil {
		return err
	}
	s, err := DecodeSubmission(items[0])
	if err != nil {
		return err
	}
	s.Reason = reason
	return UpdateSubmission(state, s)
}

// UpdateSubmission records correlated admission evidence without releasing the
// obligation. Accepted input remains quarantined until a terminal observation.
func UpdateSubmission(state *State, s Submission) error {
	if _, err := SubmissionItems(state, s.Attempt); err != nil {
		return err
	}
	envelope, err := submissionEnvelope(s)
	if err != nil {
		return err
	}
	for i := range state.Dead {
		if slices.Contains(s.Attempt.NudgeIDs, state.Dead[i].ID) {
			state.Dead[i].LastError = envelope
		}
	}
	return nil
}

// ResolveSubmission removes a fenced quarantine. retry is only for a proven
// no-send result or an explicit operator authorization; never timer recovery.
func ResolveSubmission(state *State, attempt SubmissionAttempt, retry bool) ([]Item, error) {
	items, err := SubmissionItems(state, attempt)
	if err != nil {
		return nil, err
	}
	kept := state.Dead[:0]
	for _, item := range state.Dead {
		if !slices.Contains(attempt.NudgeIDs, item.ID) {
			kept = append(kept, item)
		}
	}
	state.Dead = kept
	if retry {
		for _, item := range items {
			item.LastError = ""
			item.ClaimedAt = time.Time{}
			item.LeaseUntil = time.Time{}
			state.Pending = append(state.Pending, item)
		}
	}
	return items, nil
}
