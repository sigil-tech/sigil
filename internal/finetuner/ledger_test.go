package finetuner

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeLedgerEmitter records every training.tune emission.
type fakeLedgerEmitter struct {
	mu       sync.Mutex
	payloads []map[string]any
	err      error
}

func (f *fakeLedgerEmitter) EmitTrainingTune(_ context.Context, _ string, payload any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.payloads = append(f.payloads, payload.(map[string]any))
	return f.err
}

// TestTrainingTuneStartEmit asserts emitTrainingStart builds the
// payload with phase=start, running status, zero duration/loss/sha,
// and the correct run id.
func TestTrainingTuneStartEmit(t *testing.T) {
	db := openTestDB(t)
	fake := &fakeLedgerEmitter{}
	ft := New(db, nil, nil, testLogger()).WithLedger(fake)

	if err := ft.emitTrainingStart(context.Background(), "run-1", 500); err != nil {
		t.Fatalf("emitTrainingStart: %v", err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	require.Len(t, fake.payloads, 1)
	pl := fake.payloads[0]
	require.Equal(t, "start", pl["phase"])
	require.Equal(t, "run-1", pl["run_id"])
	require.Equal(t, "running", pl["status"])
	require.EqualValues(t, 500, pl["corpus_row_count"])
	require.Zero(t, pl["duration_seconds"])
	require.Zero(t, pl["loss_final"])
	require.Equal(t, "", pl["adapter_sha256"])
}

// TestTrainingTuneEndEmit exercises the "end" emission on the
// happy (complete) path: SHA, loss, duration are all carried.
func TestTrainingTuneEndEmit(t *testing.T) {
	db := openTestDB(t)
	fake := &fakeLedgerEmitter{}
	ft := New(db, nil, nil, testLogger()).WithLedger(fake)

	err := ft.emitTrainingEnd(
		context.Background(),
		"run-2", "llama-3-8b-q4", 500, "complete",
		/*durationSec*/ 123 /*lossFinal*/, 0.075,
		"abc123def",
	)
	require.NoError(t, err)

	fake.mu.Lock()
	defer fake.mu.Unlock()
	require.Len(t, fake.payloads, 1)
	pl := fake.payloads[0]
	require.Equal(t, "end", pl["phase"])
	require.Equal(t, "run-2", pl["run_id"])
	require.Equal(t, "complete", pl["status"])
	require.EqualValues(t, 123, pl["duration_seconds"])
	require.EqualValues(t, 0.075, pl["loss_final"])
	require.Equal(t, "abc123def", pl["adapter_sha256"])
}

// TestTrainingTuneEndEmit_FailedStatus confirms the end-emit carries
// the failed status (no SHA, no loss) for a failed run.
func TestTrainingTuneEndEmit_FailedStatus(t *testing.T) {
	db := openTestDB(t)
	fake := &fakeLedgerEmitter{}
	ft := New(db, nil, nil, testLogger()).WithLedger(fake)

	err := ft.emitTrainingEnd(
		context.Background(),
		"run-3", "llama-3-8b-q4", 250, "failed",
		0, 0, "",
	)
	require.NoError(t, err)

	fake.mu.Lock()
	defer fake.mu.Unlock()
	require.Equal(t, "failed", fake.payloads[0]["status"])
	require.Equal(t, "", fake.payloads[0]["adapter_sha256"])
}

// TestTrainingTuneStartEmit_NilLedgerIsBestEffort confirms the
// backward-compat path: a nil ledger emitter logs a WARN and returns
// nil (does not block the finetuner).
func TestTrainingTuneStartEmit_NilLedgerIsBestEffort(t *testing.T) {
	db := openTestDB(t)
	ft := New(db, nil, nil, testLogger()) // no WithLedger
	require.NoError(t, ft.emitTrainingStart(context.Background(), "run-x", 100))
	require.NoError(t, ft.emitTrainingEnd(context.Background(), "run-x", "", 100, "complete", 1, 0.1, "sha"))
}
