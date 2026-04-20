package ledger

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRotate(t *testing.T) {
	ctx := context.Background()

	t.Run("rotation with no active key fails", func(t *testing.T) {
		_, ks, reg, em := newTestEmitter(t, nil)
		rot, err := NewRotator(em, reg, ks)
		if err != nil {
			t.Fatalf("NewRotator: %v", err)
		}
		_, err = rot.Rotate(ctx, "first test")
		if err == nil || !strings.Contains(err.Error(), "no active signing key") {
			t.Fatalf("Rotate on empty: got %v, want no-active-key error", err)
		}
	})

	t.Run("rotation emits key.rotate sentinel, swaps active key", func(t *testing.T) {
		_, ks, reg, em := newTestEmitter(t, nil)
		// Prime the keystore + registry with an emit.
		if _, err := em.Emit(ctx, Event{Type: EventVMSpawn, Subject: "warm-up", Payload: nil}); err != nil {
			t.Fatalf("warm-up Emit: %v", err)
		}
		oldActive, _ := reg.Active(ctx)
		oldFP := oldActive.Fingerprint

		rot, err := NewRotator(em, reg, ks)
		if err != nil {
			t.Fatalf("NewRotator: %v", err)
		}
		result, err := rot.Rotate(ctx, "manual rotation test")
		if err != nil {
			t.Fatalf("Rotate: %v", err)
		}

		if result.OldFingerprint != oldFP {
			t.Fatalf("OldFingerprint = %q, want %q", result.OldFingerprint, oldFP)
		}
		if result.NewFingerprint == oldFP {
			t.Fatalf("NewFingerprint equals OldFingerprint — no rotation happened")
		}
		if result.RotationEntryID < 2 {
			t.Fatalf("RotationEntryID = %d, want ≥ 2", result.RotationEntryID)
		}

		// Old key is now retired, new key is active.
		old, err := reg.LookupByFingerprint(ctx, oldFP)
		if err != nil {
			t.Fatalf("Lookup old: %v", err)
		}
		if old.Active() {
			t.Fatalf("old key still active after rotation")
		}
		active, err := reg.Active(ctx)
		if err != nil {
			t.Fatalf("Active: %v", err)
		}
		if active.Fingerprint != result.NewFingerprint {
			t.Fatalf("active fp = %q, want %q", active.Fingerprint, result.NewFingerprint)
		}
	})

	t.Run("sentinel row is signed by OUTGOING key and verifies", func(t *testing.T) {
		db, ks, reg, em := newTestEmitter(t, nil)
		if _, err := em.Emit(ctx, Event{Type: EventVMSpawn, Subject: "warm", Payload: nil}); err != nil {
			t.Fatalf("warm Emit: %v", err)
		}
		oldActive, _ := reg.Active(ctx)

		rot, _ := NewRotator(em, reg, ks)
		result, err := rot.Rotate(ctx, "audit rotation")
		if err != nil {
			t.Fatalf("Rotate: %v", err)
		}

		// The sentinel row must reference the OUTGOING fingerprint.
		r := NewReader(db)
		sentinel, err := r.Get(ctx, result.RotationEntryID)
		if err != nil {
			t.Fatalf("Get sentinel: %v", err)
		}
		if sentinel.Type != string(EventKeyRotate) {
			t.Fatalf("sentinel type = %q, want key.rotate", sentinel.Type)
		}
		if sentinel.SigningKeyFP != oldActive.Fingerprint {
			t.Fatalf("sentinel signed by %q, want OUTGOING %q", sentinel.SigningKeyFP, oldActive.Fingerprint)
		}

		// Full chain verifies, including the sentinel.
		v := NewVerifier(db, reg)
		vr, err := v.Verify(ctx, VerifyScope{Full: true})
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if !vr.OK {
			t.Fatalf("Verify: %+v", vr)
		}
	})
}

// TestVerifyAcrossRotation covers Task 4.7: rotate mid-emission, then
// verify pre-rotation entries under the retired public key and
// post-rotation entries under the new key. The entire chain MUST
// verify end-to-end because the registry preserves every public key
// ever used to sign a row.
func TestVerifyAcrossRotation(t *testing.T) {
	ctx := context.Background()
	db, ks, reg, em := newTestEmitter(t, nil)

	// Emit 5 pre-rotation rows.
	for i := 1; i <= 5; i++ {
		if _, err := em.Emit(ctx, Event{Type: EventVMSpawn, Subject: "pre", Payload: map[string]any{"i": i}}); err != nil {
			t.Fatalf("pre-rotation Emit %d: %v", i, err)
		}
	}
	oldActive, _ := reg.Active(ctx)

	// Rotate.
	rot, err := NewRotator(em, reg, ks)
	if err != nil {
		t.Fatalf("NewRotator: %v", err)
	}
	result, err := rot.Rotate(ctx, "mid-session rotation")
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	// Emit 3 post-rotation rows.
	for i := 1; i <= 3; i++ {
		if _, err := em.Emit(ctx, Event{Type: EventVMTeardown, Subject: "post", Payload: map[string]any{"i": i}}); err != nil {
			t.Fatalf("post-rotation Emit %d: %v", i, err)
		}
	}

	v := NewVerifier(db, reg)

	t.Run("full chain verifies across the rotation boundary", func(t *testing.T) {
		r, err := v.Verify(ctx, VerifyScope{Full: true})
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if !r.OK {
			t.Fatalf("Verify: %+v", r)
		}
		if r.EntriesChecked != 9 { // 5 pre + 1 sentinel + 3 post
			t.Fatalf("EntriesChecked = %d, want 9", r.EntriesChecked)
		}
	})

	t.Run("pre-rotation range verifies under retired key", func(t *testing.T) {
		r, err := v.Verify(ctx, VerifyScope{FromID: 1, ToID: 5})
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if !r.OK || r.EntriesChecked != 5 {
			t.Fatalf("pre-rotation Verify: %+v", r)
		}
	})

	t.Run("post-rotation range verifies under new key", func(t *testing.T) {
		from := result.RotationEntryID + 1
		r, err := v.Verify(ctx, VerifyScope{FromID: from, ToID: from + 2})
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if !r.OK || r.EntriesChecked != 3 {
			t.Fatalf("post-rotation Verify: %+v", r)
		}
	})

	t.Run("sentinel row carries the correct fingerprints", func(t *testing.T) {
		r := NewReader(db)
		s, err := r.Get(ctx, result.RotationEntryID)
		if err != nil {
			t.Fatalf("Get sentinel: %v", err)
		}
		if !strings.Contains(s.PayloadJSON, oldActive.Fingerprint) {
			t.Fatalf("sentinel payload missing old fp: %s", s.PayloadJSON)
		}
		if !strings.Contains(s.PayloadJSON, result.NewFingerprint) {
			t.Fatalf("sentinel payload missing new fp: %s", s.PayloadJSON)
		}
	})

	// Defensive: time passes, do another rotation, whole chain must
	// continue to verify.
	t.Run("double rotation still verifies", func(t *testing.T) {
		time.Sleep(1 * time.Millisecond)
		if _, err := rot.Rotate(ctx, "second rotation"); err != nil {
			t.Fatalf("second Rotate: %v", err)
		}
		r, err := v.Verify(ctx, VerifyScope{Full: true})
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if !r.OK {
			t.Fatalf("Verify after double rotation: %+v", r)
		}
	})
}
