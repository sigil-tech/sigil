package payload

import "testing"

func TestMergePayloadAllowlist(t *testing.T) {
	t.Run("MergeFilterPayload field set", func(t *testing.T) {
		// Rule names in RulesHit are DATA, not struct field names —
		// the top-level-keys allowlist check is what protects us.
		// We keep rule names here that don't themselves contain a
		// denylist substring just so assertMarshalledKeys' broader
		// JSON-body scan stays clean.
		assertMarshalledKeys(t, MergeFilterPayload{
			SessionID:     "s",
			FilterVersion: "v1",
			RowsFiltered:  3,
			RulesHit:      map[string]int{"malformed_payload": 1, "private_dest_redacted": 2},
			MergedAt:      "2026-04-19T00:00:00Z",
		}, []string{"session_id", "filter_version", "rows_filtered", "rules_hit", "merged_at"})
	})

	t.Run("ModelMergePayload field set", func(t *testing.T) {
		assertMarshalledKeys(t, ModelMergePayload{
			SessionID:     "s",
			FilterVersion: "v1",
			RowsMerged:    10,
			RowsFiltered:  3,
			LastVMRowID:   99,
			MergedAt:      "2026-04-19T00:00:00Z",
			Status:        "complete",
		}, []string{
			"session_id", "filter_version", "rows_merged", "rows_filtered",
			"last_vm_row_id", "merged_at", "status",
		})
	})

	t.Run("PolicyDenyVMBatchPayload field set", func(t *testing.T) {
		assertMarshalledKeys(t, PolicyDenyVMBatchPayload{
			SessionID:    "s",
			TotalDenies:  5,
			DeniesByRule: map[string]int{"net.connect": 3, "exec.shell": 2},
			MergedAt:     "2026-04-19T00:00:00Z",
		}, []string{"session_id", "total_denies", "denies_by_rule", "merged_at"})
	})
}
