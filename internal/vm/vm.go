// Package vm defines the VM lifecycle state machine, session types, and the
// VMManager interface used by the socket server to manage VM sandbox sessions.
package vm

import (
	"fmt"
	"time"
)

// LifecycleState is the canonical vocabulary for VM session state.
// Used in: sessions table, socket protocol (VMStatus response),
// sigilctl output, launcher UI display names, push event payloads.
type LifecycleState string

const (
	StateBooting    LifecycleState = "booting"
	StateReady      LifecycleState = "ready"
	StateConnecting LifecycleState = "connecting"
	StateStopping   LifecycleState = "stopping"
	StateStopped    LifecycleState = "stopped"
	StateFailed     LifecycleState = "failed"
)

// IsActive returns true if the state represents an active (non-terminal) session.
func (s LifecycleState) IsActive() bool {
	switch s {
	case StateBooting, StateReady, StateConnecting, StateStopping:
		return true
	}
	return false
}

// IsTerminal returns true if the state represents a terminal (finished) session.
func (s LifecycleState) IsTerminal() bool {
	return s == StateStopped || s == StateFailed
}

// MergeOutcome describes the result of the VM-to-host merge for a session.
type MergeOutcome string

const (
	MergeOutcomePending  MergeOutcome = "pending"
	MergeOutcomeComplete MergeOutcome = "complete"
	MergeOutcomePartial  MergeOutcome = "partial"
	MergeOutcomeFailed   MergeOutcome = "failed"
	MergeOutcomeSkipped  MergeOutcome = "skipped"
)

// Session represents a VM sandbox session record in the sessions table.
type Session struct {
	ID                string         `json:"id"`
	StartedAt         time.Time      `json:"started_at"`
	EndedAt           *time.Time     `json:"ended_at,omitempty"`
	Status            LifecycleState `json:"status"`
	MergeOutcome      MergeOutcome   `json:"merge_outcome"`
	DiskImagePath     string         `json:"disk_image_path"`
	OverlayPath       string         `json:"overlay_path,omitempty"`
	VMDBPath          string         `json:"vm_db_path,omitempty"`
	VsockCID          int            `json:"vsock_cid,omitempty"`
	FilterVersion     string         `json:"filter_version,omitempty"`
	LedgerEventsTotal uint64         `json:"ledger_events_total"`
	PolicyStatus      string         `json:"policy_status"`
}

// StartRequest contains the parameters for starting a new VM session.
type StartRequest struct {
	DiskImagePath string `json:"disk_image_path"`
	OverlayPath   string `json:"overlay_path"`
	VMDBPath      string `json:"vm_db_path"`
	VsockCID      int    `json:"vsock_cid,omitempty"`
	FilterVersion string `json:"filter_version,omitempty"`
}

// Error codes returned by VM operations.
const (
	ErrSessionActive         = "ERR_SESSION_ACTIVE"
	ErrImageMissing          = "ERR_IMAGE_MISSING"
	ErrSessionNotFound       = "ERR_SESSION_NOT_FOUND"
	ErrHypervisorUnavailable = "ERR_HYPERVISOR_UNAVAILABLE"
	// ErrProfileMissing is returned by VMStart when the LauncherProfile JSON
	// file does not exist on disk. The Kenaz UI uses this code to present a
	// "configure the launcher" prompt rather than a generic error message.
	ErrProfileMissing = "ERR_PROFILE_MISSING"
)

// PolicyStatus is the static per-session policy verdict recorded in
// sessions.policy_status at VMStart time. The value reflects the policy
// configuration verdict for the submitted profile; it is written once and
// never mutated during the session's lifetime. See ADR-028c.
type PolicyStatus string

const (
	// PolicyStatusOK means the policy check passed; the session profile is
	// valid, the policyID is not revoked, and egress is compatible.
	PolicyStatusOK PolicyStatus = "ok"

	// PolicyStatusPending is reserved for post-MVP approval workflows. In the
	// MVP, all approvals are resolved launcher-side before VMStart is called,
	// so this value is unreachable in practice.
	PolicyStatusPending PolicyStatus = "pending"

	// PolicyStatusDenied means the policyID was found in the denylist at
	// VMStart time, or the requested egressTier is incompatible with the
	// policy's declared tier.
	PolicyStatusDenied PolicyStatus = "denied"

	// PolicyStatusNotApplicable means no policyID was supplied (free-form
	// session; no policy governance applied).
	PolicyStatusNotApplicable PolicyStatus = "not_applicable"
)

// VMError represents a structured VM operation error.
type VMError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *VMError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}
