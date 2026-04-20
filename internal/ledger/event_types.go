package ledger

// EventType is the enum of strings that appear in the `type` column of
// the `ledger` table. Two families of types live side by side:
//
//   - Privileged-action types, emitted by the sigild subsystems that
//     spec 029 FR-004 through FR-009 enumerate. These are the
//     user-visible events security officers care about.
//
//   - Sentinel types, emitted only by ledger internals (rotation,
//     purge). They use the same append + hash + sign machinery so a
//     verifier of a retired-but-preserved ledger can still reason
//     about the sequence of internal events.
//
// The `type` column is TEXT in the database (STRICT mode still allows
// any string); this enum is the canonical Go-side value set and an
// allowlist for validation at the Emitter boundary.
type EventType string

const (
	// EventVMSpawn — emitted when a sandbox VM transitions to booting,
	// before VZ/QEMU is invoked (FR-004).
	EventVMSpawn EventType = "vm.spawn"

	// EventVMTeardown — emitted on transition to stopped or failed
	// terminal state, synchronous with commit (FR-005).
	EventVMTeardown EventType = "vm.teardown"

	// EventPolicyDeny — emitted by the centralised policy.Denier for
	// every deny decision (FR-006).
	EventPolicyDeny EventType = "policy.deny"

	// EventPolicyDenyVMBatch — emitted once per merge when the sandbox
	// ledger carried ≥1 VM-interior denies; batched with per-rule
	// counts and a 32-entry overflow collapse (FR-008a).
	EventPolicyDenyVMBatch EventType = "policy.deny.vm_batch"

	// EventMergeFilter — emitted once per merge when ≥1 row was
	// filtered by the denylist (FR-007).
	EventMergeFilter EventType = "merge.filter"

	// EventModelMerge — emitted once per successful idempotent commit
	// of a VM session merge (FR-008).
	EventModelMerge EventType = "model.merge"

	// EventTrainingTune — emitted at LoRA fine-tune start / end with a
	// phase discriminator (FR-009).
	EventTrainingTune EventType = "training.tune"

	// EventKeyRotate — sentinel, emitted by the Rotator signing the
	// rotation entry with the outgoing (about-to-be-retired) key
	// before stamping the retirement (FR-013a).
	EventKeyRotate EventType = "key.rotate"

	// EventPurgeInvoked — sentinel, emitted by the PartialPurge helper
	// as the last act before wiping non-ledger state. Absent from the
	// FullPurge path — full purge drops the ledger itself, so there
	// is nothing left to host the sentinel (FR-035a).
	EventPurgeInvoked EventType = "purge.invoked"
)

// privilegedActionTypes is the set of types from the seven FR-004 —
// FR-009 privileged-action entries. Used by IsPrivilegedAction and by
// the allowlist check at the Emitter boundary.
var privilegedActionTypes = map[EventType]struct{}{
	EventVMSpawn:           {},
	EventVMTeardown:        {},
	EventPolicyDeny:        {},
	EventPolicyDenyVMBatch: {},
	EventMergeFilter:       {},
	EventModelMerge:        {},
	EventTrainingTune:      {},
}

// sentinelTypes is the set of ledger-internal types. Used by
// IsSentinel.
var sentinelTypes = map[EventType]struct{}{
	EventKeyRotate:    {},
	EventPurgeInvoked: {},
}

// IsPrivilegedAction returns true iff t is one of the seven
// user-visible privileged-action event types.
func IsPrivilegedAction(t EventType) bool {
	_, ok := privilegedActionTypes[t]
	return ok
}

// IsSentinel returns true iff t is a ledger-internal sentinel type
// (key.rotate or purge.invoked).
func IsSentinel(t EventType) bool {
	_, ok := sentinelTypes[t]
	return ok
}

// IsKnown returns true iff t is either a privileged-action or a
// sentinel type. Unknown types MUST be rejected at the Emitter
// boundary so an attacker who reaches the Emit path cannot write
// ledger rows under a fabricated type name.
func IsKnown(t EventType) bool {
	return IsPrivilegedAction(t) || IsSentinel(t)
}
