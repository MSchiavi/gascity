package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/gastownhall/gascity/internal/runtime"
)

func TestAdmissionNeverFallsBackToUntrackedNudge(t *testing.T) {
	p := runtime.NewFake()
	h, err := NewRuntimeHandle(RuntimeHandleConfig{Provider: p, SessionName: "live"})
	if err != nil {
		t.Fatal(err)
	}
	req := runtime.AdmissionRequest{CommandID: "command", ProviderSessionID: "provider", Fence: runtime.AdmissionFence{SessionID: "gc-one", ContinuationEpoch: "1", RuntimeToken: "token"}, Text: "work"}
	result, err := h.Nudge(context.Background(), NudgeRequest{Text: "work", Admission: &req})
	if !errors.Is(err, runtime.ErrAdmissionUnsupported) || result.Delivered || result.Admission != nil {
		t.Fatalf("unsupported admission = %+v, %v", result, err)
	}
	if len(p.Calls) != 0 {
		t.Fatalf("unsupported admission called provider: %+v", p.Calls)
	}
}

func TestAdmissionRejectsDifferentTextBeforeProvider(t *testing.T) {
	p := runtime.NewFake()
	h, err := NewRuntimeHandle(RuntimeHandleConfig{Provider: p, SessionName: "live"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.Nudge(context.Background(), NudgeRequest{Text: "replacement", Admission: &runtime.AdmissionRequest{Text: "original"}})
	if err == nil || errors.Is(err, runtime.ErrAdmissionUnsupported) || len(p.Calls) != 0 {
		t.Fatalf("changed command must fail before provider: error=%v calls=%+v", err, p.Calls)
	}
}
