package worker

import (
	"context"
	"fmt"

	"github.com/gastownhall/gascity/internal/runtime"
	runtimeexec "github.com/gastownhall/gascity/internal/runtime/exec"
)

// SupportsNudgeAdmission keeps the concrete RPP capability behind the worker boundary.
func SupportsNudgeAdmission(handle Handle) bool {
	switch h := handle.(type) {
	case *SessionHandle:
		return h.manager.SupportsAdmission(h.currentSessionID())
	case *RuntimeHandle:
		return runtimeexec.SupportsAdmission(h.provider, h.sessionName)
	default:
		return false
	}
}

// NudgeAdmissionTarget resolves the exact live host before an attempt is pinned.
func NudgeAdmissionTarget(_ context.Context, handle Handle) (runtime.AdmissionTarget, error) {
	switch h := handle.(type) {
	case *SessionHandle:
		return h.manager.NudgeAdmissionTarget(h.currentSessionID())
	case *RuntimeHandle:
		return runtimeexec.AdmissionTarget(h.provider, h.sessionName)
	default:
		return runtime.AdmissionTarget{}, runtime.ErrAdmissionUnsupported
	}
}

// ReadNudgeReceipt reads evidence without retrying the submitted command.
func ReadNudgeReceipt(ctx context.Context, handle Handle, req runtime.AdmissionRequest) (runtime.AdmissionReceipt, error) {
	return nudgeAdmission(ctx, handle, req, true)
}

func nudgeAdmission(ctx context.Context, handle Handle, req runtime.AdmissionRequest, query bool) (runtime.AdmissionReceipt, error) {
	switch h := handle.(type) {
	case *SessionHandle:
		id := h.currentSessionID()
		if id == "" {
			return runtime.AdmissionReceipt{}, fmt.Errorf("command admission requires a session identity")
		}
		return h.manager.NudgeAdmission(ctx, id, req, query)
	case *RuntimeHandle:
		return runtimeexec.Admission(ctx, h.provider, h.sessionName, req, query)
	default:
		return runtime.AdmissionReceipt{}, runtime.ErrAdmissionUnsupported
	}
}

func admitNudge(ctx context.Context, handle Handle, req NudgeRequest) (NudgeResult, error) {
	if req.Admission.Text != req.Text {
		return NudgeResult{}, fmt.Errorf("nudge and admission text differ")
	}
	receipt, err := nudgeAdmission(ctx, handle, *req.Admission, false)
	if err != nil {
		return NudgeResult{}, err
	}
	// Accepted is deliberately not Delivered: the queue retains the obligation
	// and reconciles terminal evidence through ReadNudgeReceipt.
	return NudgeResult{Admission: &receipt}, nil
}
