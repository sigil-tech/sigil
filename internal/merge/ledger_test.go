package merge

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sigil-tech/sigil/internal/config"
)

// fakeMergeLedgerEmitter records every emit so tests can assert the
// Emit* sequence MergeWithLedger produces. Safe for concurrent
// emission; merge serialises its own calls, but the mutex here
// future-proofs against test drift.
type fakeMergeLedgerEmitter struct {
	mu            sync.Mutex
	filters       []map[string]any
	merges        []map[string]any
	vmBatches     []map[string]any
	filterErr     error
	modelMergeErr error
	vmBatchErr    error
}

func (f *fakeMergeLedgerEmitter) EmitMergeFilter(_ context.Context, _ string, payload any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.filters = append(f.filters, payload.(map[string]any))
	return f.filterErr
}
func (f *fakeMergeLedgerEmitter) EmitModelMerge(_ context.Context, _ string, payload any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.merges = append(f.merges, payload.(map[string]any))
	return f.modelMergeErr
}
func (f *fakeMergeLedgerEmitter) EmitPolicyDenyVMBatch(_ context.Context, _ string, payload any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vmBatches = append(f.vmBatches, payload.(map[string]any))
	return f.vmBatchErr
}

// TestMergeFilterEmitsLedger covers Task 5.6: a merge that filters at
// least one row emits merge.filter exactly once with a rules_hit
// histogram.
func TestMergeFilterEmitsLedger(t *testing.T) {
	ctx := context.Background()
	vmDB := createVMDB(t, []testEvent{
		{
			kind:    "process",
			source:  "collector",
			payload: map[string]any{"cmd": "password is hunter2"}, // trips default denylist
			ts:      1,
		},
		{
			kind:    "process",
			source:  "collector",
			payload: map[string]any{"cmd": "ls"},
			ts:      2,
		},
	})
	hostDB := createHostDB(t)
	cfg := &config.Config{}
	cfg.Merge.Denylist = []string{"*password*"}
	cfg.Merge.FilterVersion = "test-v1"

	fake := &fakeMergeLedgerEmitter{}
	res, err := MergeWithLedger(ctx, hostDB, vmDB, "sess-filter", cfg, fake)
	require.NoError(t, err)
	require.Equal(t, MergeStatusComplete, res.Status)
	require.Equal(t, 1, res.RowsMerged)
	require.Equal(t, 1, res.RowsFiltered)

	fake.mu.Lock()
	defer fake.mu.Unlock()
	require.Len(t, fake.filters, 1, "exactly one merge.filter expected")
	require.Len(t, fake.merges, 1, "exactly one model.merge expected")

	rules, _ := fake.filters[0]["rules_hit"].(map[string]int)
	require.Equal(t, 1, rules["denylist"])
	require.Equal(t, "test-v1", fake.filters[0]["filter_version"])
}

// TestModelMergeEmitsLedger covers Task 5.7: a zero-filter merge
// STILL emits model.merge (success marker) but NOT merge.filter.
func TestModelMergeEmitsLedger(t *testing.T) {
	ctx := context.Background()
	vmDB := createVMDB(t, []testEvent{
		{kind: "process", source: "collector", payload: map[string]any{"cmd": "ls"}, ts: 1},
	})
	hostDB := createHostDB(t)
	cfg := &config.Config{}

	fake := &fakeMergeLedgerEmitter{}
	res, err := MergeWithLedger(ctx, hostDB, vmDB, "sess-model", cfg, fake)
	require.NoError(t, err)
	require.Equal(t, MergeStatusComplete, res.Status)

	fake.mu.Lock()
	defer fake.mu.Unlock()
	require.Len(t, fake.filters, 0, "no merge.filter when zero rows filtered")
	require.Len(t, fake.merges, 1, "exactly one model.merge expected")

	merged := fake.merges[0]
	require.Equal(t, "complete", merged["status"])
	require.EqualValues(t, 1, merged["rows_merged"])
	require.EqualValues(t, 0, merged["rows_filtered"])
}

// TestPolicyDenyVMBatchEmitsLedger covers Task 5.8 (partial): the
// hook is plumbed but emits nothing today because the sandbox-ledger
// VM-interior deny aggregation is a follow-up. A zero-denies merge
// MUST NOT emit policy.deny.vm_batch — verified here so an accidental
// emission would surface as a test failure in a future wiring change.
func TestPolicyDenyVMBatchEmitsLedger(t *testing.T) {
	ctx := context.Background()
	vmDB := createVMDB(t, []testEvent{
		{kind: "process", source: "collector", payload: map[string]any{"cmd": "ls"}, ts: 1},
	})
	hostDB := createHostDB(t)
	cfg := &config.Config{}

	fake := &fakeMergeLedgerEmitter{}
	if _, err := MergeWithLedger(ctx, hostDB, vmDB, "sess-vmbatch", cfg, fake); err != nil {
		t.Fatalf("MergeWithLedger: %v", err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	require.Len(t, fake.vmBatches, 0, "zero denies must not produce a policy.deny.vm_batch row")
}

// TestMergeAtomicLedgerEmission covers Task 5.9: if ledger emission
// fails mid-sequence, the merge_log terminal write is rolled back to
// 'in_progress' so retry + the ledger's own idempotency can converge.
func TestMergeAtomicLedgerEmission(t *testing.T) {
	ctx := context.Background()
	vmDB := createVMDB(t, []testEvent{
		{kind: "process", source: "collector", payload: map[string]any{"cmd": "ls"}, ts: 1},
	})
	hostDB := createHostDB(t)
	cfg := &config.Config{}

	fake := &fakeMergeLedgerEmitter{modelMergeErr: errors.New("ledger unavailable")}
	_, err := MergeWithLedger(ctx, hostDB, vmDB, "sess-atomic", cfg, fake)
	if err == nil {
		t.Fatalf("expected error when ledger emission fails")
	}

	// merge_log status must have been rolled back to in_progress.
	var status string
	if err := hostDB.QueryRow(
		`SELECT status FROM merge_log WHERE session_id = ?`, "sess-atomic",
	).Scan(&status); err != nil {
		t.Fatalf("query merge_log: %v", err)
	}
	if status != "in_progress" {
		t.Fatalf("merge_log status = %q, want in_progress (post-ledger-fail rollback)", status)
	}
}

// TestMergeWithoutLedger covers the backward-compat path: Merge() (no
// ledger emitter) still completes successfully and logs a WARN.
func TestMergeWithoutLedger(t *testing.T) {
	ctx := context.Background()
	vmDB := createVMDB(t, []testEvent{
		{kind: "process", source: "collector", payload: map[string]any{"cmd": "ls"}, ts: 1},
	})
	hostDB := createHostDB(t)
	cfg := &config.Config{}

	res, err := Merge(ctx, hostDB, vmDB, "sess-noledger", cfg)
	require.NoError(t, err)
	require.Equal(t, MergeStatusComplete, res.Status)
}

// TestCollapseRulesHit exercises the 32-entry cap + __overflow__
// collapse rule in isolation.
func TestCollapseRulesHit(t *testing.T) {
	t.Run("no collapse when under cap", func(t *testing.T) {
		in := map[string]int{"a": 1, "b": 2, "c": 3}
		out := collapseRulesHit(in, MaxRulesHitEntries)
		require.Equal(t, 3, len(out))
	})
	t.Run("collapse when over cap", func(t *testing.T) {
		in := map[string]int{}
		for i := range 50 {
			in[fmtRule(i)] = i + 1
		}
		out := collapseRulesHit(in, 32)
		require.LessOrEqual(t, len(out), 32)
		require.Contains(t, out, OverflowRuleName, "overflow bucket must be present")
	})
}

func fmtRule(i int) string {
	// Small helper to keep the test body readable.
	// Rule names are "rule000".."rule049".
	const prefix = "rule"
	d := i
	b := []byte{'0', '0', '0'}
	for k := 2; k >= 0 && d > 0; k-- {
		b[k] = byte('0' + d%10)
		d /= 10
	}
	return prefix + string(b)
}
