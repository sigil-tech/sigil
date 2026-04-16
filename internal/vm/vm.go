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
	ID            string         `json:"id"`
	StartedAt     time.Time      `json:"started_at"`
	EndedAt       *time.Time     `json:"ended_at,omitempty"`
	Status        LifecycleState `json:"status"`
	MergeOutcome  MergeOutcome   `json:"merge_outcome"`
	DiskImagePath string         `json:"disk_image_path"`
	OverlayPath   string         `json:"overlay_path,omitempty"`
	VMDBPath      string         `json:"vm_db_path,omitempty"`
	VsockCID      int            `json:"vsock_cid,omitempty"`
	FilterVersion string         `json:"filter_version,omitempty"`
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
	ErrSessionActive   = "ERR_SESSION_ACTIVE"
	ErrImageMissing    = "ERR_IMAGE_MISSING"
	ErrSessionNotFound = "ERR_SESSION_NOT_FOUND"
)

// VMError represents a structured VM operation error.
type VMError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *VMError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}
