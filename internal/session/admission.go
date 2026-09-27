package session

import (
	"context"
	"fmt"

	"github.com/gastownhall/gascity/internal/runtime"
	runtimeexec "github.com/gastownhall/gascity/internal/runtime/exec"
)

// SupportsAdmission reports whether this manager's runtime retains command receipts.
func (m *Manager) SupportsAdmission(id string) bool {
	info, err := m.Get(id)
	return err == nil && runtimeexec.SupportsAdmission(m.sp, info.SessionName)
}

// NudgeAdmissionTarget reads the provider-owned UUID only after the live host's
// complete incarnation matches the current bead. The host owns native resumption
// per GC session and continuation epoch; this read never mutates session metadata.
func (m *Manager) NudgeAdmissionTarget(id string) (runtime.AdmissionTarget, error) {
	var target runtime.AdmissionTarget
	err := withSessionMutationLock(id, func() error {
		info, err := m.Get(id)
		if err != nil {
			return err
		}
		target, err = runtimeexec.AdmissionTarget(m.sp, info.SessionName)
		if err != nil {
			return err
		}
		want := runtime.AdmissionFence{SessionID: info.ID, ContinuationEpoch: info.ContinuationEpoch, RuntimeToken: info.InstanceToken}
		if target.Fence != want || (info.SessionKey != "" && info.SessionKey != target.ProviderSessionID) {
			return fmt.Errorf("admission host identity does not match session")
		}
		return nil
	})
	return target, err
}

// NudgeAdmission sends or reads one previously persisted submission under the
// session mutation lock. It never wakes, resets, or retargets the session.
func (m *Manager) NudgeAdmission(ctx context.Context, id string, req runtime.AdmissionRequest, query bool) (runtime.AdmissionReceipt, error) {
	var receipt runtime.AdmissionReceipt
	err := withSessionMutationLock(id, func() error {
		info, err := m.Get(id)
		if err != nil {
			return err
		}
		if err := validateAdmissionSession(info, req, query); err != nil {
			return err
		}
		receipt, err = runtimeexec.Admission(ctx, m.sp, info.SessionName, req, query)
		return err
	})
	return receipt, err
}

func validateAdmissionSession(info Info, req runtime.AdmissionRequest, query bool) error {
	if info.ID != req.Fence.SessionID || (info.SessionKey != "" && info.SessionKey != req.ProviderSessionID) {
		return fmt.Errorf("command admission session fence mismatch")
	}
	// Receipt queries retain the original attempt's fence across restart. The
	// host and receipt decoder still require that exact original command/fence.
	if query {
		return nil
	}
	if info.ContinuationEpoch != req.Fence.ContinuationEpoch || info.InstanceToken != req.Fence.RuntimeToken {
		return fmt.Errorf("command admission incarnation changed")
	}
	if info.State != StateActive && info.State != StateAwake {
		return fmt.Errorf("command admission requires an active session")
	}
	return nil
}
