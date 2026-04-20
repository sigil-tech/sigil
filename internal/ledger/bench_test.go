package ledger

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// benchOpenMemoryDB mirrors openMemoryDB but takes a testing.TB so it
// can serve both benchmark and test callers. Kept here rather than
// alongside openMemoryDB to avoid rippling the signature change
// through the non-bench test files for no functional gain.
func benchOpenMemoryDB(tb testing.TB) *sql.DB {
	tb.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		tb.Fatalf("open memory db: %v", err)
	}
	db.SetMaxOpenConns(1)
	tb.Cleanup(func() { _ = db.Close() })
	return db
}

func benchSetupEmitter(tb testing.TB) (*sql.DB, KeyRegistry, Emitter) {
	tb.Helper()
	ctx := context.Background()
	db := benchOpenMemoryDB(tb)
	if err := Migrate(ctx, db); err != nil {
		tb.Fatalf("Migrate: %v", err)
	}
	ks := newMemoryKeystore()
	reg := NewKeyRegistry(db)
	return db, reg, NewEmitter(db, ks, reg)
}

// BenchmarkVerify10K covers Task 4.6 / spec 029 SC-003: verifying a
// 10 000-entry chain MUST finish in under 200ms p95 on CI hardware.
// The benchmark emits 10 000 rows via the real Emit path (single
// signing key, no rotation), then repeatedly runs full-chain Verify
// with a fresh Verifier each iteration so the session cache never
// serves a hit.
//
// Run with: `go test -bench=BenchmarkVerify10K -benchtime=5s ./internal/ledger/`.
func BenchmarkVerify10K(b *testing.B) {
	const N = 10000
	ctx := context.Background()
	db, reg, em := benchSetupEmitter(b)

	for i := range N {
		if _, err := em.Emit(ctx, Event{
			Type:    EventVMSpawn,
			Subject: "bench",
			Payload: map[string]any{"i": i},
		}); err != nil {
			b.Fatalf("Emit %d: %v", i, err)
		}
	}

	b.ResetTimer()
	for b.Loop() {
		// Fresh Verifier per iteration → no cache hits → cold verify.
		v := NewVerifier(db, reg)
		r, err := v.Verify(ctx, VerifyScope{Full: true})
		if err != nil {
			b.Fatalf("Verify: %v", err)
		}
		if !r.OK || r.EntriesChecked != N {
			b.Fatalf("Verify: %+v", r)
		}
	}
}

// BenchmarkVerify10K_Cached measures the session-cache hot path.
// First Verify builds the cache, subsequent iterations hit it. The
// delta against BenchmarkVerify10K tells us how much the cache buys.
func BenchmarkVerify10K_Cached(b *testing.B) {
	const N = 10000
	ctx := context.Background()
	db, reg, em := benchSetupEmitter(b)
	for i := range N {
		if _, err := em.Emit(ctx, Event{
			Type:    EventVMSpawn,
			Subject: "bench",
			Payload: map[string]any{"i": i},
		}); err != nil {
			b.Fatalf("Emit %d: %v", i, err)
		}
	}

	v := NewVerifier(db, reg)
	if _, err := v.Verify(ctx, VerifyScope{Full: true}); err != nil {
		b.Fatalf("warm: %v", err)
	}

	b.ResetTimer()
	for b.Loop() {
		if _, err := v.Verify(ctx, VerifyScope{Full: true}); err != nil {
			b.Fatalf("Verify: %v", err)
		}
	}
}
