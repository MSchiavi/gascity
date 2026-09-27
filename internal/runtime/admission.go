package runtime

import (
	"errors"
	"fmt"
	"strings"
)

// ErrAdmissionUnsupported means the runtime does not offer durable command receipts.
var ErrAdmissionUnsupported = errors.New("runtime command admission is unsupported")

// AdmissionFence binds an attempt to one Gas City runtime incarnation.
type AdmissionFence struct {
	SessionID         string `json:"session_id"`
	ContinuationEpoch string `json:"continuation_epoch"`
	RuntimeToken      string `json:"runtime_token"`
}

// AdmissionTarget is the native identity read from a live receipt-capable host.
type AdmissionTarget struct {
	ProviderSessionID string         `json:"provider_session_id"`
	Fence             AdmissionFence `json:"fence"`
}

// AdmissionRequest identifies one immutable submission. Retrying an ambiguous
// request must preserve its command ID, fence, provider session, and text.
type AdmissionRequest struct {
	CommandID         string         `json:"command_id"`
	ProviderSessionID string         `json:"provider_session_id"`
	Fence             AdmissionFence `json:"fence"`
	Text              string         `json:"text"`
}

// Validate requires a complete target before any external submission.
func (r AdmissionRequest) Validate() error {
	if strings.TrimSpace(r.CommandID) == "" || strings.TrimSpace(r.ProviderSessionID) == "" || strings.TrimSpace(r.Fence.SessionID) == "" || strings.TrimSpace(r.Fence.ContinuationEpoch) == "" || strings.TrimSpace(r.Fence.RuntimeToken) == "" || strings.TrimSpace(r.Text) == "" {
		return fmt.Errorf("command admission requires text and a complete immutable identity")
	}
	return nil
}

// AdmissionReceipt separates protocol acceptance from execution outcome.
// TurnID may differ from CommandID when a provider absorbs a queued input.
type AdmissionReceipt struct {
	CommandID         string         `json:"command_id"`
	ProviderSessionID string         `json:"provider_session_id"`
	Fence             AdmissionFence `json:"fence"`
	State             string         `json:"state"`
	ReceiptID         string         `json:"receipt_id,omitempty"`
	TurnID            string         `json:"turn_id,omitempty"`
	Disposition       string         `json:"disposition,omitempty"`
	Terminal          string         `json:"terminal,omitempty"`
	Reason            string         `json:"reason,omitempty"`
}
