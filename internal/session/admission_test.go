package session

import (
	"testing"

	"github.com/gastownhall/gascity/internal/runtime"
)

func TestAdmissionQueryRetainsOriginalFenceAfterRestart(t *testing.T) {
	info := Info{ID: "gc-one", SessionKey: "native", ContinuationEpoch: "2", InstanceToken: "new", State: StateActive}
	req := runtime.AdmissionRequest{ProviderSessionID: "native", Fence: runtime.AdmissionFence{SessionID: "gc-one", ContinuationEpoch: "1", RuntimeToken: "old"}}
	if err := validateAdmissionSession(info, req, true); err != nil {
		t.Fatalf("original receipt should remain readable: %v", err)
	}
	if err := validateAdmissionSession(info, req, false); err == nil {
		t.Fatal("old incarnation must not submit")
	}
	info.SessionKey = "replacement"
	if err := validateAdmissionSession(info, req, true); err == nil {
		t.Fatal("replacement provider session must not settle old receipt")
	}
	info.SessionKey = ""
	if err := validateAdmissionSession(info, req, true); err != nil {
		t.Fatalf("host-owned native receipt identity: %v", err)
	}
}
