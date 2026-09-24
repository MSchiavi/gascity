package main

import (
	"errors"
	"syscall"
	"testing"
	"testing/synctest"
)

func TestShutdownBeadsProviderRefusesWhilePollerSurvives(t *testing.T) {
	want := errors.New("poller still running")
	previous := stopCityNudgePollers
	t.Cleanup(func() { stopCityNudgePollers = previous })
	stopCityNudgePollers = func(cityPath string) error {
		if cityPath != "/offline-test-city" {
			t.Fatalf("city = %q", cityPath)
		}
		return want
	}
	if err := shutdownBeadsProvider("/offline-test-city"); !errors.Is(err, want) {
		t.Fatalf("shutdown error = %v, want poller failure before opening storage", err)
	}
}

func TestStopNudgePollerIdentityFailureDoesNotSignal(t *testing.T) {
	want := errors.New("identity unavailable")
	err := stopNudgePoller(42, func() (bool, error) { return false, want }, func(syscall.Signal) error {
		t.Fatal("signaled an unverified process")
		return nil
	})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want identity failure", err)
	}
}

func TestStopNudgePollerDoesNotEscalateAfterIdentityChanges(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		owned := true
		var signals []syscall.Signal
		err := stopNudgePoller(42, func() (bool, error) { return owned, nil }, func(sig syscall.Signal) error {
			signals = append(signals, sig)
			owned = false
			return nil
		})
		if err != nil || len(signals) != 1 || signals[0] != syscall.SIGTERM {
			t.Fatalf("signals=%v error=%v, want TERM only", signals, err)
		}
	})
}

func TestStopNudgePollerConfirmsExit(t *testing.T) {
	for _, tc := range []struct {
		name        string
		matches     bool
		exitOn      syscall.Signal
		wantSignals int
		wantErr     bool
	}{
		{name: "unrelated process", wantSignals: 0},
		{name: "graceful exit", matches: true, exitOn: syscall.SIGTERM, wantSignals: 1},
		{name: "forced exit", matches: true, exitOn: syscall.SIGKILL, wantSignals: 2},
		{name: "survivor", matches: true, wantSignals: 2, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				live := tc.matches
				var signals []syscall.Signal
				err := stopNudgePoller(42, func() (bool, error) { return live, nil }, func(sig syscall.Signal) error {
					signals = append(signals, sig)
					if sig == tc.exitOn {
						live = false
					}
					return nil
				})
				if (err != nil) != tc.wantErr || len(signals) != tc.wantSignals {
					t.Fatalf("signals=%v error=%v, want %d signals and error=%v", signals, err, tc.wantSignals, tc.wantErr)
				}
			})
		})
	}
}
