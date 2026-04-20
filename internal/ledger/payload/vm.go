package payload

// VMSpawnPayload is written to the ledger's `payload_json` cell when a
// sandbox VM transitions to `booting` (before VZ/QEMU is invoked per
// FR-004). Every field is allowlisted; do not add an `Extras` /
// `Metadata` / interface-typed field without a spec change, because
// FR-032 forbids unconstrained payloads.
//
// Field meanings:
//
//   - SessionID       the daemon's UUIDv4 for this VM session. Also
//     appears in the ledger row's `subject` column; keeping it in the
//     payload too lets verifiers assert the two match.
//   - ImagePath       absolute path to the disk image being booted.
//   - PolicyID        named policy the session was started under
//     (sandboxed-default, read-only-shell, etc.). Required for audit
//     reconstruction.
//   - EgressTier      one of "deny", "local-only", "public". Drives
//     the iptables/nftables rules attached to the sandbox.
//   - VsockCID        guest-side CID used for host ↔ VM IPC.
//   - FilterVersion   hex digest of the active denylist applied at
//     spawn time. Lets the audit viewer show which filter rules the
//     sandbox could not have violated.
//   - StartedAt       RFC 3339 UTC spawn timestamp (redundant with
//     the ledger row's own ts column — kept for compliance readers
//     that export the payload in isolation).
type VMSpawnPayload struct {
	SessionID     string `json:"session_id"`
	ImagePath     string `json:"image_path"`
	PolicyID      string `json:"policy_id"`
	EgressTier    string `json:"egress_tier"`
	VsockCID      uint32 `json:"vsock_cid"`
	FilterVersion string `json:"filter_version"`
	StartedAt     string `json:"started_at"`
}

// VMTeardownPayload is written when a sandbox VM transitions to
// `stopped` or `failed` (FR-005). Captures the outcome and a minimal
// set of counters that compliance needs — anything richer (per-event
// breakdowns, command transcripts) belongs in the VM's own vm.db,
// not in the host ledger.
//
// Field meanings:
//
//   - SessionID         matches the spawn row's SessionID.
//   - Outcome           "stopped" | "failed" | "merged" | "denied".
//     The latter two are set by the merge pipeline; the VM manager
//     only writes "stopped" or "failed" on its teardown path.
//   - DurationSeconds   elapsed wall-clock time from booting to
//     teardown. Rounded down to whole seconds.
//   - LedgerEventsTotal cumulative count of VM-interior ledger-like
//     events (spec 028 sandbox ledger). The host ledger records the
//     count, not the contents.
//   - PolicyStatus      final status value from the VM policy
//     evaluator ("ok", "degraded", "fatal"). Operators look at this
//     first when triaging failures.
//   - EndedAt           RFC 3339 UTC teardown timestamp.
type VMTeardownPayload struct {
	SessionID         string `json:"session_id"`
	Outcome           string `json:"outcome"`
	DurationSeconds   int64  `json:"duration_seconds"`
	LedgerEventsTotal uint64 `json:"ledger_events_total"`
	PolicyStatus      string `json:"policy_status"`
	EndedAt           string `json:"ended_at"`
}
