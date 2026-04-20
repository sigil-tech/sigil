package ledger

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestSC001SixThousandActions covers spec 029 SC-001: 6000 scripted
// privileged actions (roughly even split across the seven event
// types) all land in the ledger and the full chain verifies.
//
// Runs as a regular Go test rather than a subprocess-spawning E2E
// harness because the primitives are what SC-001 actually tests —
// "can the chain hold 6000 emits". The socket / sigilctl / Kenaz
// wiring is covered by per-layer tests in their own packages. A
// future subprocess harness is tracked as a follow-up task.
func TestSC001SixThousandActions(t *testing.T) {
	if testing.Short() {
		t.Skip("SC-001 runs ~6000 emits; skip under -short")
	}
	ctx := context.Background()
	db, _, reg, em := newTestEmitter(t, nil)

	types := []EventType{
		EventVMSpawn,
		EventVMTeardown,
		EventMergeFilter,
		EventModelMerge,
		EventPolicyDenyVMBatch,
		EventTrainingTune,
		EventPolicyDeny,
	}

	const total = 6000
	start := time.Now()
	for i := 0; i < total; i++ {
		typ := types[i%len(types)]
		if _, err := em.Emit(ctx, Event{
			Type:    typ,
			Subject: "sc-001",
			Payload: map[string]any{"i": i},
		}); err != nil {
			t.Fatalf("Emit %d (type=%s): %v", i, typ, err)
		}
	}
	emitDur := time.Since(start)
	t.Logf("SC-001: %d emits in %s (%.0f emits/sec)", total, emitDur, float64(total)/emitDur.Seconds())

	r := NewReader(db)
	n, err := r.Count(ctx)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != int64(total) {
		t.Fatalf("Count = %d, want %d (not every emit landed)", n, total)
	}

	// Full chain verifies.
	v := NewVerifier(db, reg)
	vr, err := v.Verify(ctx, VerifyScope{Full: true})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !vr.OK || vr.EntriesChecked != total {
		t.Fatalf("Verify: %+v (want ok=true, entries=%d)", vr, total)
	}
}

// BenchmarkSC003_List100K covers spec 029 SC-003 / SC-006: listing
// the newest page against a 100 000-entry chain. The Reader path is
// index-backed (idx_ledger_ts) so the benchmark should run in
// low-milliseconds per iteration regardless of the full-chain size.
//
// Run with `go test -bench=BenchmarkSC003_List100K -benchtime=5s`.
func BenchmarkSC003_List100K(b *testing.B) {
	ctx := context.Background()
	const N = 100000
	db, _, em := benchSetupEmitter(b)

	for i := range N {
		if _, err := em.Emit(ctx, Event{
			Type: EventVMSpawn, Subject: "bench", Payload: map[string]any{"i": i},
		}); err != nil {
			b.Fatalf("Emit %d: %v", i, err)
		}
	}

	r := NewReader(db)
	b.ResetTimer()
	for b.Loop() {
		entries, err := r.List(ctx, ListFilter{Limit: 100})
		if err != nil {
			b.Fatalf("List: %v", err)
		}
		if len(entries) != 100 {
			b.Fatalf("len=%d, want 100", len(entries))
		}
	}
}

// BenchmarkSC006_Verify100K covers SC-006's "full verify < 15s at
// 100k entries" budget. On dev hardware this runs well under the
// budget because verify is linear in the number of entries and the
// hot path is ed25519.Verify (~80µs/row on typical x86_64). A
// pedigreed 100k chain verifies in ~8s on the dev workstation.
func BenchmarkSC006_Verify100K(b *testing.B) {
	ctx := context.Background()
	const N = 100000
	db, reg, em := benchSetupEmitter(b)

	for i := range N {
		if _, err := em.Emit(ctx, Event{
			Type: EventVMSpawn, Subject: "bench", Payload: map[string]any{"i": i},
		}); err != nil {
			b.Fatalf("Emit %d: %v", i, err)
		}
	}

	b.ResetTimer()
	for b.Loop() {
		v := NewVerifier(db, reg)
		r, err := v.Verify(ctx, VerifyScope{Full: true})
		if err != nil {
			b.Fatalf("Verify: %v", err)
		}
		if !r.OK {
			b.Fatalf("Verify: %+v", r)
		}
	}
}

// benchSetupEmitter mirrors the one in bench_test.go but is kept
// accessible here via the same package — no wrapper needed.
//
// Use BenchmarkSC003_List100K and BenchmarkSC006_Verify100K as the
// ones the CI gate (`make bench-sc`) exercises. The regression
// budget is encoded in the Makefile target.

// Phase 12 documentation: if a future run surfaces a SC-003/006
// regression, the fix site is internal/ledger/verify.go's hot loop
// and internal/ledger/read.go's SELECT + scanEntry path.
var _ = fmt.Sprintf
