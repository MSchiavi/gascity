package exec

import (
	"encoding/json"
	"testing"

	"github.com/gastownhall/gascity/internal/runtime"
	runtimeauto "github.com/gastownhall/gascity/internal/runtime/auto"
	"github.com/gastownhall/gascity/internal/runtime/hybrid"
)

func TestAdmissionSelectsOnlyRoutedBackend(t *testing.T) {
	managed := NewProvider("unused")
	managed.handshakeOnce.Do(func() {
		managed.handshakeInfo = runtime.ProtocolInfo{Capabilities: []string{"message-admission"}}
	})
	local := runtime.NewFake()
	router := hybrid.New(local, managed, func(name string) bool { return name == "canary" })
	provider := runtimeauto.New(router, local)
	if !SupportsAdmission(provider, "canary") || SupportsAdmission(provider, "other") {
		t.Fatal("admission capability did not follow the selected runtime")
	}
	provider.RouteACP("canary")
	if SupportsAdmission(provider, "canary") {
		t.Fatal("ACP route inherited unrelated managed capability")
	}
	if len(local.Calls) != 0 {
		t.Fatalf("capability lookup drove the local runtime: %+v", local.Calls)
	}
}

func TestAdmissionReceiptRejectsUncorrelatedEvidence(t *testing.T) {
	req := runtime.AdmissionRequest{CommandID: "command", ProviderSessionID: "provider", Fence: runtime.AdmissionFence{SessionID: "gc-one", ContinuationEpoch: "1", RuntimeToken: "token"}, Text: "work"}
	valid := runtime.AdmissionReceipt{CommandID: req.CommandID, ProviderSessionID: req.ProviderSessionID, Fence: req.Fence, State: "accepted", TurnID: "different-turn"}
	for _, tc := range []struct {
		name    string
		change  func(*runtime.AdmissionReceipt)
		wantErr bool
	}{
		{"absorbed turn is not command identity", func(*runtime.AdmissionReceipt) {}, false},
		{"wrong command", func(r *runtime.AdmissionReceipt) { r.CommandID = "other" }, true},
		{"wrong provider session", func(r *runtime.AdmissionReceipt) { r.ProviderSessionID = "other" }, true},
		{"replacement runtime", func(r *runtime.AdmissionReceipt) { r.Fence.RuntimeToken = "new" }, true},
		{"empty response", func(r *runtime.AdmissionReceipt) { *r = runtime.AdmissionReceipt{} }, true},
		{"unknown state", func(r *runtime.AdmissionReceipt) { r.State = "delivered" }, true},
		{"unknown terminal", func(r *runtime.AdmissionReceipt) { r.Terminal = "maybe" }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := valid
			tc.change(&r)
			data, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			_, err = decodeAdmissionReceipt(req, data)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}
