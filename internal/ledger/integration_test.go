package ledger

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sigil-tech/sigil/internal/ledger/payload"
)

// TestAllSevenEmittersEndToEnd covers Task 5.18: run every privileged
// event type through the real Emit path, assert exactly seven rows
// land (the seven privileged actions), assert the chain verifies
// end-to-end, and assert every payload key matches a field name on
// the corresponding typed payload struct. The last check is the
// FR-032 guardrail: an integrator that slips in an unvetted key
// (via a map literal instead of a typed struct) fails this test.
func TestAllSevenEmittersEndToEnd(t *testing.T) {
	ctx := context.Background()
	db, _, reg, em := newTestEmitter(t, nil)

	// Every privileged-action type gets one emission, with a payload
	// built from the typed struct and JSON-round-tripped into the
	// Emitter (which canonicalises again via JCS).
	events := []struct {
		Type    EventType
		Subject string
		Payload any
	}{
		{EventVMSpawn, "vm-aaa", payload.VMSpawnPayload{
			SessionID: "vm-aaa", ImagePath: "/img", PolicyID: "p1",
			EgressTier: "deny", VsockCID: 101, FilterVersion: "v1",
			StartedAt: "2026-04-19T00:00:00Z",
		}},
		{EventVMTeardown, "vm-aaa", payload.VMTeardownPayload{
			SessionID: "vm-aaa", Outcome: "stopped", DurationSeconds: 10,
			LedgerEventsTotal: 42, PolicyStatus: "ok",
			EndedAt: "2026-04-19T00:00:10Z",
		}},
		{EventMergeFilter, "merge-bbb", payload.MergeFilterPayload{
			SessionID: "merge-bbb", FilterVersion: "v1",
			RowsFiltered: 3,
			RulesHit: map[string]int{
				"payload_too_large": 1,
				"denylist":          2,
			},
			MergedAt: "2026-04-19T00:01:00Z",
		}},
		{EventModelMerge, "merge-bbb", payload.ModelMergePayload{
			SessionID: "merge-bbb", FilterVersion: "v1",
			RowsMerged: 10, RowsFiltered: 3, LastVMRowID: 13,
			MergedAt: "2026-04-19T00:01:00Z", Status: "complete",
		}},
		{EventPolicyDenyVMBatch, "merge-bbb", payload.PolicyDenyVMBatchPayload{
			SessionID: "merge-bbb", TotalDenies: 5,
			DeniesByRule: map[string]int{"net.connect": 3, "exec.shell": 2},
			MergedAt:     "2026-04-19T00:01:00Z",
		}},
		{EventTrainingTune, "run-ccc", payload.TrainingTunePayload{
			Phase:          payload.TrainingTunePhaseEnd,
			RunID:          "run-ccc",
			BaseModelVer:   "llama-3-8b-q4",
			CorpusRowCount: 500,
			Status:         "complete",
			DurationSec:    600,
			LossFinal:      0.12,
			AdapterSHA256:  "deadbeef",
			EmittedAt:      "2026-04-19T00:05:00Z",
		}},
		{EventPolicyDeny, "exec.sudo", payload.PolicyDenyPayload{
			Rule:     "exec.sandbox.shell",
			Action:   "sudo",
			Reason:   "sandbox policy blocks interactive shells",
			DeniedAt: "2026-04-19T00:02:00Z",
		}},
	}
	require.Len(t, events, 7, "must exercise all seven privileged event types")

	for _, e := range events {
		entry, err := em.Emit(ctx, Event{Type: e.Type, Subject: e.Subject, Payload: e.Payload})
		require.NoError(t, err, "Emit %s: %v", e.Type, err)
		require.NotEmpty(t, entry.Hash)
	}

	// Exactly seven rows in the ledger.
	r := NewReader(db)
	n, err := r.Count(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 7, n)

	// Full chain verifies end-to-end.
	v := NewVerifier(db, reg)
	vr, err := v.Verify(ctx, VerifyScope{Full: true})
	require.NoError(t, err)
	require.True(t, vr.OK, "full chain must verify: %+v", vr)
	require.Equal(t, 7, vr.EntriesChecked)

	// Per-row payload key conformance: every top-level key in the
	// stored payload_json MUST match a field name on the corresponding
	// typed struct. This catches an integrator that bypassed the
	// typed-struct path and emitted a raw map with extra keys.
	expectedKeys := map[EventType]map[string]bool{
		EventVMSpawn:           mustJSONKeys(payload.VMSpawnPayload{}),
		EventVMTeardown:        mustJSONKeys(payload.VMTeardownPayload{}),
		EventMergeFilter:       mustJSONKeys(payload.MergeFilterPayload{}),
		EventModelMerge:        mustJSONKeys(payload.ModelMergePayload{}),
		EventPolicyDenyVMBatch: mustJSONKeys(payload.PolicyDenyVMBatchPayload{}),
		EventTrainingTune:      mustJSONKeys(payload.TrainingTunePayload{}),
		EventPolicyDeny:        mustJSONKeys(payload.PolicyDenyPayload{}),
	}

	err = r.IterateAll(ctx, func(e Entry) error {
		want := expectedKeys[EventType(e.Type)]
		require.NotNil(t, want, "no expected keys for type %q", e.Type)

		var decoded map[string]any
		require.NoError(t, json.Unmarshal([]byte(e.PayloadJSON), &decoded))
		for k := range decoded {
			if !want[k] {
				t.Fatalf("row %d (type=%s) carries unvetted payload key %q (FR-032 violation)", e.ID, e.Type, k)
			}
		}
		return nil
	})
	require.NoError(t, err)
}

// mustJSONKeys returns the set of JSON tag names declared on the
// exported fields of v. Panics if called on a non-struct; tests
// hand in zero-valued payload structs so this is an invariant.
func mustJSONKeys(v any) map[string]bool {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		panic(err)
	}
	out := make(map[string]bool, len(decoded))
	for k := range decoded {
		out[k] = true
	}
	return out
}
