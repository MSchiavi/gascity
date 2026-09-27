package exec

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/gastownhall/gascity/internal/runtime"
	runtimeauto "github.com/gastownhall/gascity/internal/runtime/auto"
	"github.com/gastownhall/gascity/internal/runtime/hybrid"
)

// Admission uses the concrete RPP implementation until another native runtime
// needs the same contract. Unsupported providers never fall back to keystrokes.
func Admission(ctx context.Context, provider runtime.Provider, name string, req runtime.AdmissionRequest, query bool) (runtime.AdmissionReceipt, error) {
	p := admissionProvider(provider, name)
	if p == nil || !p.handshakeCapability("message-admission") {
		return runtime.AdmissionReceipt{}, runtime.ErrAdmissionUnsupported
	}
	if err := req.Validate(); err != nil {
		return runtime.AdmissionReceipt{}, err
	}
	data, err := json.Marshal(req)
	if err != nil {
		return runtime.AdmissionReceipt{}, err
	}
	args := []string{"admit", name}
	if query {
		args = []string{"receipt", name, req.CommandID}
	}
	out, err := p.runWithContext(ctx, p.timeout, data, args...)
	if err != nil {
		return runtime.AdmissionReceipt{}, err
	}
	return decodeAdmissionReceipt(req, []byte(out))
}

// SupportsAdmission reports the real executable's declared receipt capability.
func SupportsAdmission(provider runtime.Provider, name string) bool {
	p := admissionProvider(provider, name)
	return p != nil && p.handshakeCapability("message-admission")
}

// AdmissionTarget reads an initialized host's identity, never transcript discovery.
func AdmissionTarget(provider runtime.Provider, name string) (runtime.AdmissionTarget, error) {
	p := admissionProvider(provider, name)
	if p == nil || !p.handshakeCapability("message-admission") {
		return runtime.AdmissionTarget{}, runtime.ErrAdmissionUnsupported
	}
	var target runtime.AdmissionTarget
	out, err := p.run(nil, "status", name)
	if err != nil {
		return target, err
	}
	if err := json.Unmarshal([]byte(out), &target); err != nil {
		return target, fmt.Errorf("decode admission target: %w", err)
	}
	if target.ProviderSessionID == "" || target.Fence.SessionID == "" || target.Fence.ContinuationEpoch == "" || target.Fence.RuntimeToken == "" {
		return runtime.AdmissionTarget{}, fmt.Errorf("admission host has no complete incarnation")
	}
	return target, nil
}

func admissionProvider(provider runtime.Provider, name string) *Provider {
	switch p := provider.(type) {
	case *Provider:
		return p
	case *seamBackedProvider:
		return p.raw
	case *runtimeauto.Provider:
		return admissionProvider(p.SelectedProvider(name), name)
	case *hybrid.Provider:
		return admissionProvider(p.SelectedProvider(name), name)
	default:
		return nil
	}
}

func decodeAdmissionReceipt(req runtime.AdmissionRequest, data []byte) (runtime.AdmissionReceipt, error) {
	var r runtime.AdmissionReceipt
	if err := json.Unmarshal(data, &r); err != nil {
		return r, fmt.Errorf("decode command receipt: %w", err)
	}
	if r.CommandID != req.CommandID || r.ProviderSessionID != req.ProviderSessionID || r.Fence != req.Fence {
		return runtime.AdmissionReceipt{}, fmt.Errorf("command receipt identity mismatch")
	}
	switch r.State {
	case "accepted", "not_admitted", "unknown":
	default:
		return runtime.AdmissionReceipt{}, fmt.Errorf("invalid command receipt state %q", r.State)
	}
	switch r.Terminal {
	case "", "completed", "failed", "canceled", "abandoned", "terminal_no_effect":
	default:
		return runtime.AdmissionReceipt{}, fmt.Errorf("invalid command receipt terminal %q", r.Terminal)
	}
	return r, nil
}
