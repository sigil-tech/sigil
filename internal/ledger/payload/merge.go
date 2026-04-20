package payload

// MergeFilterPayload summarises the filtered rows from a single
// VM-to-host merge call. Emitted at most once per merge (FR-007) and
// ONLY when at least one row was filtered — a zero-row merge produces
// no `merge.filter` entry. Per-row raw content never leaves the VM's
// own database; the payload carries only counts and filter-rule
// histograms.
//
// Fields:
//
//   - SessionID          same UUID as the VM session this merge
//     drained. Lets the audit viewer tie the filter event back to
//     the spawn / teardown pair.
//   - FilterVersion      opaque identifier of the active denylist at
//     merge time (mc.FilterVersion). Spec 024a amendment A.
//   - RowsFiltered       total number of rows excluded.
//   - RulesHit           map of filter-rule name → count of rows
//     excluded under that rule. The Emitter MUST enforce a 32-entry
//     cap per FR-008a; overflow is collapsed into a single
//     "__overflow__" bucket so the payload-size gate doesn't blow.
//   - MergedAt           RFC 3339 UTC timestamp of the merge commit.
type MergeFilterPayload struct {
	SessionID     string         `json:"session_id"`
	FilterVersion string         `json:"filter_version"`
	RowsFiltered  int            `json:"rows_filtered"`
	RulesHit      map[string]int `json:"rules_hit"`
	MergedAt      string         `json:"merged_at"`
}

// ModelMergePayload records a successful VM-to-host merge commit —
// the idempotent "we consumed rows N..M from the sandbox ledger" fact.
// Emitted exactly once per successful merge per FR-008.
//
// Fields:
//
//   - SessionID       VM session whose rows were merged.
//   - FilterVersion   active denylist version at merge time.
//   - RowsMerged      count of rows appended to training_corpus.
//   - RowsFiltered    count of rows rejected by the filter.
//   - LastVMRowID     highest id consumed from the VM events table;
//     lets an auditor replay the merge bound.
//   - MergedAt        RFC 3339 UTC timestamp of the merge commit.
//   - Status          "complete" | "already_complete" — the two
//     terminal states that justify emission. "partial" and "failed"
//     do NOT emit.
type ModelMergePayload struct {
	SessionID     string `json:"session_id"`
	FilterVersion string `json:"filter_version"`
	RowsMerged    int    `json:"rows_merged"`
	RowsFiltered  int    `json:"rows_filtered"`
	LastVMRowID   int64  `json:"last_vm_row_id"`
	MergedAt      string `json:"merged_at"`
	Status        string `json:"status"`
}

// PolicyDenyVMBatchPayload summarises the VM-interior deny events
// observed in the sandbox ledger during a single merge. Emitted at
// most once per merge (FR-008a); zero denies → no entry.
//
// Fields:
//
//   - SessionID        VM session whose sandbox ledger was merged.
//   - TotalDenies      aggregate count across all rules.
//   - DeniesByRule     map of rule name → count. Capped at 32
//     entries; overflow is collapsed into "__overflow__" the same
//     way MergeFilterPayload.RulesHit handles it.
//   - MergedAt         RFC 3339 UTC timestamp.
type PolicyDenyVMBatchPayload struct {
	SessionID    string         `json:"session_id"`
	TotalDenies  int            `json:"total_denies"`
	DeniesByRule map[string]int `json:"denies_by_rule"`
	MergedAt     string         `json:"merged_at"`
}

// MaxRulesHitEntries is the per-payload cap on the `RulesHit` /
// `DeniesByRule` maps per FR-008a. The overflow bucket uses the
// constant string OverflowRuleName so readers can detect collapse.
const (
	MaxRulesHitEntries = 32
	OverflowRuleName   = "__overflow__"
)
