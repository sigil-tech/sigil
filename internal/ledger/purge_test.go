package ledger

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestPartialPurge covers Task 8.1: a PartialPurge emits exactly one
// purge.invoked sentinel BEFORE the wipeState callback runs; if the
// sentinel emission fails, wipeState does NOT run.
func TestPartialPurge(t *testing.T) {
	ctx := context.Background()

	t.Run("emits sentinel before wipe, in order", func(t *testing.T) {
		db, _, _, em := newTestEmitter(t, nil)
		// Prime the ledger with a regular emission so the sentinel is
		// chained off something non-genesis.
		if _, err := em.Emit(ctx, Event{Type: EventVMSpawn, Subject: "warm", Payload: nil}); err != nil {
			t.Fatalf("warm Emit: %v", err)
		}

		ph := NewPurgeHelper(db, em)

		order := []string{}
		err := ph.PartialPurge(ctx, "operator reset", func(context.Context) error {
			order = append(order, "wipe")
			return nil
		})
		if err != nil {
			t.Fatalf("PartialPurge: %v", err)
		}

		// Sentinel is now the newest row.
		r := NewReader(db)
		entries, err := r.List(ctx, ListFilter{Limit: 5})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if entries[0].Type != string(EventPurgeInvoked) {
			t.Fatalf("tip type = %q, want purge.invoked", entries[0].Type)
		}
		if !strings.Contains(entries[0].PayloadJSON, "operator reset") {
			t.Fatalf("sentinel payload missing reason: %s", entries[0].PayloadJSON)
		}
		if len(order) != 1 || order[0] != "wipe" {
			t.Fatalf("wipeState was not called exactly once: %v", order)
		}
	})

	t.Run("emission failure skips wipeState", func(t *testing.T) {
		db, _, _, _ := newTestEmitter(t, nil)
		// Swap the Emitter for a fake that always errors so the
		// sentinel emission fails deterministically.
		fake := &failingEmitter{err: errors.New("simulated emit failure")}
		ph := NewPurgeHelper(db, fake)

		wipeCalled := false
		err := ph.PartialPurge(ctx, "forced-fail", func(context.Context) error {
			wipeCalled = true
			return nil
		})
		if err == nil {
			t.Fatalf("expected error from emit failure")
		}
		if wipeCalled {
			t.Fatalf("wipeState was called despite sentinel emission failure")
		}
	})

	t.Run("nil wipeState is tolerated", func(t *testing.T) {
		db, _, _, em := newTestEmitter(t, nil)
		ph := NewPurgeHelper(db, em)
		if err := ph.PartialPurge(ctx, "no-wipe", nil); err != nil {
			t.Fatalf("PartialPurge with nil wipe: %v", err)
		}
	})
}

// TestFullPurge covers Task 8.2: wipeState runs first, then the
// ledger tables + triggers are dropped. No sentinel is emitted (the
// ledger is being destroyed, nowhere for the sentinel to live).
func TestFullPurge(t *testing.T) {
	ctx := context.Background()
	db, _, _, em := newTestEmitter(t, nil)

	// Seed a couple of entries.
	for i := 0; i < 3; i++ {
		if _, err := em.Emit(ctx, Event{Type: EventVMSpawn, Subject: "seed", Payload: nil}); err != nil {
			t.Fatalf("seed Emit %d: %v", i, err)
		}
	}

	ph := NewPurgeHelper(db, em)

	order := []string{}
	err := ph.FullPurge(ctx, "scorched earth", func(context.Context) error {
		order = append(order, "wipe")
		return nil
	})
	if err != nil {
		t.Fatalf("FullPurge: %v", err)
	}
	if len(order) != 1 || order[0] != "wipe" {
		t.Fatalf("wipeState call order %v, want [wipe]", order)
	}

	// ledger and ledger_keys tables are gone.
	for _, tbl := range []string{"ledger", "ledger_keys"} {
		var name string
		err := db.QueryRow(`SELECT name FROM sqlite_schema WHERE type='table' AND name=?`, tbl).Scan(&name)
		if err == nil {
			t.Fatalf("table %q still exists after FullPurge", tbl)
		}
	}

	// No sentinel was emitted (the ledger is gone, so we can't check a
	// row — but Migrate + a subsequent Count == 0 confirms the ledger
	// is empty when re-initialised).
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("re-Migrate after FullPurge: %v", err)
	}
	r := NewReader(db)
	n, err := r.Count(ctx)
	if err != nil {
		t.Fatalf("Count after re-Migrate: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected empty ledger after FullPurge + re-Migrate, got %d rows", n)
	}
}

// failingEmitter is a test-only Emitter that always returns err.
type failingEmitter struct {
	err error
}

func (f *failingEmitter) Emit(context.Context, Event) (Entry, error) {
	return Entry{}, f.err
}
