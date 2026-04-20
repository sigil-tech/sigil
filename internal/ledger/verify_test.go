package ledger

import (
	"context"
	"strings"
	"testing"
)

// emitN writes n rows and returns them; used by Verify tests to
// establish a known-good chain before tampering.
func emitN(t *testing.T, em Emitter, n int) []Entry {
	t.Helper()
	ctx := context.Background()
	var out []Entry
	for i := 1; i <= n; i++ {
		entry, err := em.Emit(ctx, Event{
			Type:    EventVMSpawn,
			Subject: "s",
			Payload: map[string]any{"i": i},
		})
		if err != nil {
			t.Fatalf("Emit %d: %v", i, err)
		}
		out = append(out, entry)
	}
	return out
}

func TestVerify(t *testing.T) {
	ctx := context.Background()

	t.Run("empty ledger verifies cleanly", func(t *testing.T) {
		db, _, reg, _ := newTestEmitter(t, nil)
		v := NewVerifier(db, reg)
		r, err := v.Verify(ctx, VerifyScope{Full: true})
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if !r.OK || r.EntriesChecked != 0 {
			t.Fatalf("empty Verify: %+v", r)
		}
	})

	t.Run("happy-path chain of 10 verifies", func(t *testing.T) {
		db, _, reg, em := newTestEmitter(t, nil)
		emitN(t, em, 10)
		v := NewVerifier(db, reg)
		r, err := v.Verify(ctx, VerifyScope{Full: true})
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if !r.OK || r.EntriesChecked != 10 {
			t.Fatalf("Verify result: %+v", r)
		}
	})

	t.Run("single-entry scope", func(t *testing.T) {
		db, _, reg, em := newTestEmitter(t, nil)
		emitN(t, em, 5)
		v := NewVerifier(db, reg)
		r, err := v.Verify(ctx, VerifyScope{FromID: 3, ToID: 3})
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if !r.OK || r.EntriesChecked != 1 {
			t.Fatalf("single-entry Verify: %+v", r)
		}
	})

	t.Run("range scope [3..7]", func(t *testing.T) {
		db, _, reg, em := newTestEmitter(t, nil)
		emitN(t, em, 10)
		v := NewVerifier(db, reg)
		r, err := v.Verify(ctx, VerifyScope{FromID: 3, ToID: 7})
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if !r.OK || r.EntriesChecked != 5 {
			t.Fatalf("range Verify: %+v", r)
		}
	})

	t.Run("tampered hash is detected", func(t *testing.T) {
		db, _, reg, em := newTestEmitter(t, nil)
		emitN(t, em, 5)

		// Tamper with row 3's subject by going around the triggers: we
		// temporarily drop the update trigger, poke the value, restore
		// the trigger. This is what the fuzz test in Task 4.4 will do
		// per-column; here we exercise the Verifier on one shape.
		ctx := context.Background()
		if _, err := db.ExecContext(ctx, `DROP TRIGGER ledger_no_update`); err != nil {
			t.Fatalf("drop trigger: %v", err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE ledger SET subject='TAMPERED' WHERE id=3`); err != nil {
			t.Fatalf("tamper: %v", err)
		}

		v := NewVerifier(db, reg)
		r, err := v.Verify(ctx, VerifyScope{Full: true})
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if r.OK {
			t.Fatalf("Verify should have detected tamper")
		}
		if r.BreakAtID != 3 {
			t.Fatalf("BreakAtID = %d, want 3", r.BreakAtID)
		}
		if r.Reason != VerifyBreakHashMismatch {
			t.Fatalf("Reason = %q, want %q", r.Reason, VerifyBreakHashMismatch)
		}
	})

	t.Run("tampered signature is detected", func(t *testing.T) {
		db, _, reg, em := newTestEmitter(t, nil)
		emitN(t, em, 3)

		ctx := context.Background()
		if _, err := db.ExecContext(ctx, `DROP TRIGGER ledger_no_update`); err != nil {
			t.Fatalf("drop trigger: %v", err)
		}
		// Flip the last byte of the signature — keep the hash intact, so
		// the failure MUST be signature mismatch, not hash mismatch.
		if _, err := db.ExecContext(ctx,
			`UPDATE ledger SET signature = substr(signature,1,126) || 'ff' WHERE id=2`,
		); err != nil {
			t.Fatalf("tamper: %v", err)
		}
		v := NewVerifier(db, reg)
		r, err := v.Verify(ctx, VerifyScope{Full: true})
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if r.OK {
			t.Fatalf("Verify should have detected signature tamper")
		}
		if r.BreakAtID != 2 {
			t.Fatalf("BreakAtID = %d, want 2", r.BreakAtID)
		}
		if r.Reason != VerifyBreakSignatureMismatch {
			t.Fatalf("Reason = %q, want %q", r.Reason, VerifyBreakSignatureMismatch)
		}
	})

	t.Run("unknown key is detected", func(t *testing.T) {
		// The cleanest way to trip the unknown-key path: drop the
		// append-only delete trigger on ledger_keys and DELETE the
		// active key row. Every existing ledger row then references
		// a fingerprint that the registry no longer knows about.
		// This mirrors the real failure mode a spliced-from-a-
		// different-host row would produce.
		db, _, reg, em := newTestEmitter(t, nil)
		emitN(t, em, 2)
		ctx := context.Background()
		if _, err := db.ExecContext(ctx, `DROP TRIGGER ledger_keys_no_delete`); err != nil {
			t.Fatalf("drop trigger: %v", err)
		}
		if _, err := db.ExecContext(ctx, `DELETE FROM ledger_keys`); err != nil {
			t.Fatalf("delete keys: %v", err)
		}
		v := NewVerifier(db, reg)
		r, err := v.Verify(ctx, VerifyScope{Full: true})
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if r.Reason != VerifyBreakUnknownKey {
			t.Fatalf("Reason = %q, want %q", r.Reason, VerifyBreakUnknownKey)
		}
	})

	t.Run("prev_hash mismatch detected", func(t *testing.T) {
		db, _, reg, em := newTestEmitter(t, nil)
		emitN(t, em, 3)
		ctx := context.Background()
		if _, err := db.ExecContext(ctx, `DROP TRIGGER ledger_no_update`); err != nil {
			t.Fatalf("drop trigger: %v", err)
		}
		// Rewrite row 2's prev_hash to something that doesn't match row 1's hash.
		if _, err := db.ExecContext(ctx,
			`UPDATE ledger SET prev_hash = ? WHERE id=2`,
			strings.Repeat("ab", 32),
		); err != nil {
			t.Fatalf("tamper: %v", err)
		}
		v := NewVerifier(db, reg)
		r, err := v.Verify(ctx, VerifyScope{Full: true})
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if r.Reason != VerifyBreakPrevHashMismatch {
			t.Fatalf("Reason = %q, want %q", r.Reason, VerifyBreakPrevHashMismatch)
		}
	})

	t.Run("session cache invalidates on new emit", func(t *testing.T) {
		db, _, reg, em := newTestEmitter(t, nil)
		emitN(t, em, 3)
		v := NewVerifier(db, reg)
		// First verify — no cache hit, computes fresh result.
		r1, err := v.Verify(ctx, VerifyScope{Full: true})
		if err != nil || !r1.OK || r1.EntriesChecked != 3 {
			t.Fatalf("first Verify: %+v err=%v", r1, err)
		}
		// Emit one more row — this changes the tip, so the next verify
		// must NOT return the stale cached count of 3.
		if _, err := em.Emit(ctx, Event{Type: EventVMSpawn, Subject: "post", Payload: nil}); err != nil {
			t.Fatalf("Emit: %v", err)
		}
		r2, err := v.Verify(ctx, VerifyScope{Full: true})
		if err != nil || !r2.OK {
			t.Fatalf("second Verify: %+v err=%v", r2, err)
		}
		if r2.EntriesChecked != 4 {
			t.Fatalf("cache served stale result: EntriesChecked=%d, want 4", r2.EntriesChecked)
		}
	})
}
