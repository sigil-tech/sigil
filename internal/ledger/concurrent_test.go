package ledger

import (
	"context"
	"sync"
	"testing"
)

// TestConcurrentEmit is Task 4.5 — spec 029 SC-004: 100 goroutines
// each call Emit 10 times. The result MUST be exactly 1000 rows, with
// ids 1..1000 contiguous, prev_hash chaining intact, and the full
// chain verifying.
//
// Run with `go test -run TestConcurrentEmit -count=100 -race ./...`
// to repeat the property-style test and let the race detector surface
// any silent ordering issue across the mutex + SQLite tx boundary.
func TestConcurrentEmit(t *testing.T) {
	ctx := context.Background()
	db, _, reg, em := newTestEmitter(t, nil)

	const (
		goroutines   = 100
		perGoroutine = 10
		total        = goroutines * perGoroutine
	)

	var wg sync.WaitGroup
	errs := make(chan error, total)

	for g := range goroutines {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for j := range perGoroutine {
				_, err := em.Emit(ctx, Event{
					Type:    EventModelMerge,
					Subject: "concurrent",
					Payload: map[string]any{"g": g, "j": j},
				})
				if err != nil {
					errs <- err
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Emit: %v", err)
		}
	}

	// Exactly `total` rows.
	r := NewReader(db)
	n, err := r.Count(ctx)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != int64(total) {
		t.Fatalf("Count = %d, want %d", n, total)
	}

	// ids are contiguous 1..total.
	seen := make(map[int64]bool, total)
	if err := r.IterateAll(ctx, func(e Entry) error {
		if seen[e.ID] {
			t.Fatalf("duplicate id %d", e.ID)
		}
		seen[e.ID] = true
		return nil
	}); err != nil {
		t.Fatalf("IterateAll: %v", err)
	}
	for i := int64(1); i <= int64(total); i++ {
		if !seen[i] {
			t.Fatalf("missing id %d", i)
		}
	}

	// Full-chain verification passes.
	v := NewVerifier(db, reg)
	vr, err := v.Verify(ctx, VerifyScope{Full: true})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !vr.OK {
		t.Fatalf("Verify: %+v", vr)
	}
	if vr.EntriesChecked != total {
		t.Fatalf("EntriesChecked = %d, want %d", vr.EntriesChecked, total)
	}
}
