package main

import (
	"errors"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/worker"
)

type nudgeIdleSnapshotProvider struct {
	*runtime.Fake
	idle bool
	err  error
}

func (p *nudgeIdleSnapshotProvider) SnapshotIdle(string) (bool, error) {
	return p.idle, p.err
}

func TestPollerSessionIdleEnoughUsesInteractiveBoundary(t *testing.T) {
	for _, tc := range []struct {
		name        string
		idle        bool
		err         error
		activityAge time.Duration
		want        bool
	}{
		{name: "idle repainting prompt", idle: true, want: true},
		{name: "busy silent terminal", activityAge: time.Hour},
		{name: "unknown boundary", idle: true, err: errors.New("capture failed"), activityAge: time.Hour},
		{name: "unsupported route retains activity fallback", err: runtime.ErrInteractionUnsupported, activityAge: time.Hour, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sp := &nudgeIdleSnapshotProvider{Fake: runtime.NewFake(), idle: tc.idle, err: tc.err}
			last := time.Now().Add(-tc.activityAge)
			got := pollerSessionIdleEnough(nudgeTarget{sessionName: "worker"}, sp, 3*time.Second, worker.LiveObservation{LastActivity: &last})
			if got != tc.want {
				t.Fatalf("pollerSessionIdleEnough = %v, want %v", got, tc.want)
			}
		})
	}
}
