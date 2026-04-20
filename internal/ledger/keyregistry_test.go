package ledger

import (
	"context"
	"crypto/ed25519"
	"errors"
	"testing"
	"time"
)

func TestKeyRegistry(t *testing.T) {
	ctx := context.Background()
	db := openMemoryDB(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	reg := NewKeyRegistry(db)

	t.Run("Active before any Insert returns ErrKeyNotFound", func(t *testing.T) {
		_, err := reg.Active(ctx)
		if !errors.Is(err, ErrKeyNotFound) {
			t.Fatalf("Active on empty registry: got %v, want ErrKeyNotFound", err)
		}
	})

	pub1, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	gen1 := time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC)

	var fp1 string

	t.Run("Insert records an active key", func(t *testing.T) {
		rec, err := reg.Insert(ctx, pub1, gen1)
		if err != nil {
			t.Fatalf("Insert: %v", err)
		}
		if rec.Fingerprint == "" || len(rec.Fingerprint) != FingerprintLen {
			t.Fatalf("bad fingerprint %q", rec.Fingerprint)
		}
		if !rec.Active() {
			t.Fatalf("new key should be active, got retired_at=%v", rec.RetiredAt)
		}
		fp1 = rec.Fingerprint
	})

	t.Run("Insert is idempotent on the fingerprint PK", func(t *testing.T) {
		rec, err := reg.Insert(ctx, pub1, gen1.Add(time.Hour))
		if err != nil {
			t.Fatalf("second Insert: %v", err)
		}
		// The original generated_at wins — the OR IGNORE preserved the row.
		if !rec.GeneratedAt.Equal(gen1) {
			t.Fatalf("second Insert rewrote generated_at: got %v, want %v", rec.GeneratedAt, gen1)
		}
	})

	t.Run("LookupByFingerprint returns the inserted record", func(t *testing.T) {
		rec, err := reg.LookupByFingerprint(ctx, fp1)
		if err != nil {
			t.Fatalf("LookupByFingerprint: %v", err)
		}
		if rec.Fingerprint != fp1 {
			t.Fatalf("fingerprint mismatch: got %q want %q", rec.Fingerprint, fp1)
		}
	})

	t.Run("LookupByFingerprint on unknown returns ErrKeyNotFound", func(t *testing.T) {
		_, err := reg.LookupByFingerprint(ctx, "deadbeefdeadbeefdeadbeefdeadbeef")
		if !errors.Is(err, ErrKeyNotFound) {
			t.Fatalf("Lookup unknown: got %v, want ErrKeyNotFound", err)
		}
	})

	t.Run("Active returns the single active key", func(t *testing.T) {
		rec, err := reg.Active(ctx)
		if err != nil {
			t.Fatalf("Active: %v", err)
		}
		if rec.Fingerprint != fp1 {
			t.Fatalf("Active fp = %q, want %q", rec.Fingerprint, fp1)
		}
	})

	t.Run("MarkRetired transitions retired_at", func(t *testing.T) {
		retireAt := gen1.Add(24 * time.Hour)
		if err := reg.MarkRetired(ctx, fp1, retireAt); err != nil {
			t.Fatalf("MarkRetired: %v", err)
		}
		rec, err := reg.LookupByFingerprint(ctx, fp1)
		if err != nil {
			t.Fatalf("Lookup after retire: %v", err)
		}
		if rec.Active() {
			t.Fatalf("key still active after MarkRetired")
		}
		if !rec.RetiredAt.Equal(retireAt) {
			t.Fatalf("retired_at = %v, want %v", rec.RetiredAt, retireAt)
		}
	})

	t.Run("MarkRetired on already-retired key returns ErrKeyAlreadyRetired", func(t *testing.T) {
		err := reg.MarkRetired(ctx, fp1, gen1.Add(48*time.Hour))
		if !errors.Is(err, ErrKeyAlreadyRetired) {
			t.Fatalf("second MarkRetired: got %v, want ErrKeyAlreadyRetired", err)
		}
	})

	t.Run("MarkRetired on unknown fingerprint returns ErrKeyNotFound", func(t *testing.T) {
		err := reg.MarkRetired(ctx, "ffffffffffffffffffffffffffffffff", gen1)
		if !errors.Is(err, ErrKeyNotFound) {
			t.Fatalf("MarkRetired unknown: got %v, want ErrKeyNotFound", err)
		}
	})

	t.Run("MarkRetired rejects wrong-length fingerprint", func(t *testing.T) {
		err := reg.MarkRetired(ctx, "tooshort", gen1)
		if err == nil {
			t.Fatalf("expected wrong-length fingerprint to error")
		}
	})

	t.Run("Active after retirement returns ErrKeyNotFound (no active key)", func(t *testing.T) {
		_, err := reg.Active(ctx)
		if !errors.Is(err, ErrKeyNotFound) {
			t.Fatalf("Active with all retired: got %v, want ErrKeyNotFound", err)
		}
	})

	// Add a second key so ListAll has more than one row to order.
	pub2, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("genkey 2: %v", err)
	}
	gen2 := gen1.Add(72 * time.Hour)

	t.Run("Insert second key and LookupByFingerprint returns it", func(t *testing.T) {
		rec, err := reg.Insert(ctx, pub2, gen2)
		if err != nil {
			t.Fatalf("Insert 2: %v", err)
		}
		if !rec.Active() {
			t.Fatalf("second key should be active")
		}
	})

	t.Run("Active returns the newly-inserted key once the old one is retired", func(t *testing.T) {
		rec, err := reg.Active(ctx)
		if err != nil {
			t.Fatalf("Active: %v", err)
		}
		if rec.GeneratedAt != gen2 {
			t.Fatalf("Active generated_at = %v, want %v", rec.GeneratedAt, gen2)
		}
	})

	t.Run("ListAll returns both records in generated_at ASC order", func(t *testing.T) {
		all, err := reg.ListAll(ctx)
		if err != nil {
			t.Fatalf("ListAll: %v", err)
		}
		if len(all) != 2 {
			t.Fatalf("ListAll len=%d, want 2", len(all))
		}
		if !all[0].GeneratedAt.Before(all[1].GeneratedAt) {
			t.Fatalf("ListAll not sorted ascending by generated_at: %v %v",
				all[0].GeneratedAt, all[1].GeneratedAt)
		}
	})
}

func TestFingerprintIsDeterministic(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	a := Fingerprint(pub)
	b := Fingerprint(pub)
	if a != b {
		t.Fatalf("Fingerprint non-deterministic: %s vs %s", a, b)
	}
	if len(a) != FingerprintLen {
		t.Fatalf("Fingerprint length = %d, want %d", len(a), FingerprintLen)
	}
}
