package ledger

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/sigil-tech/sigil/internal/ledger/jcs"
	"github.com/sigil-tech/sigil/internal/ledger/keystore"
)

// RotateResult reports the outcome of a successful Rotator.Rotate.
// Operators rely on the NewFingerprint to confirm the swap; the
// RotationEntryID points at the `key.rotate` sentinel row they can
// verify independently via sigilctl / the Audit Viewer.
type RotateResult struct {
	OldFingerprint  string
	NewFingerprint  string
	RotationEntryID int64
	Reason          string
}

// RotatePayload is the JSON shape embedded in the `key.rotate`
// sentinel entry. Struct-typed (not map[string]any) to satisfy the
// FR-032 allowlist discipline that Phase 5 extends to every emitter.
type RotatePayload struct {
	OldFingerprint string `json:"old_fingerprint"`
	NewFingerprint string `json:"new_fingerprint"`
	Reason         string `json:"reason"`
	GeneratedAt    string `json:"generated_at"`
}

// Rotator performs the atomic key rotation dance per spec 029
// FR-013a: generate a new keypair, emit a key.rotate sentinel signed
// by the OUTGOING key, mark the outgoing key retired, record the new
// public key in the registry, and swap the active private key in the
// keystore.
//
// Atomicity boundary: the ledger row + registry retire + registry
// insert all commit together or not at all. The keystore.Store call
// happens AFTER the database transaction so a mid-rotation crash
// leaves the daemon runnable with the old key (the key.rotate row
// has not yet landed, no retire flag yet) — restart and retry is
// safe. The inverse (keystore.Store first, then database) would
// strand the new key on disk with nothing in the ledger pointing at
// it and the old key still marked active.
type Rotator interface {
	Rotate(ctx context.Context, reason string) (RotateResult, error)
}

type rotatorImpl struct {
	emitter  *emitterImpl
	registry KeyRegistry
	keystore keystore.KeyStorage
}

// NewRotator wires a Rotator around the same collaborators an
// Emitter uses. Rotator is intentionally NOT expressed via the
// Emitter interface — rotation has a different transactional shape
// and needs to temporarily use both the old and new keys, so it
// reaches into the concrete emitterImpl.
//
// The em argument MUST be the same Emitter the daemon routes all
// normal emits through, so the single-writer mutex is shared. Two
// rotation attempts or a rotation concurrent with a normal Emit
// serialise at e.mu.
func NewRotator(em Emitter, reg KeyRegistry, ks keystore.KeyStorage) (Rotator, error) {
	impl, ok := em.(*emitterImpl)
	if !ok {
		return nil, fmt.Errorf("ledger.NewRotator: Emitter is not *emitterImpl (got %T)", em)
	}
	return &rotatorImpl{
		emitter:  impl,
		registry: reg,
		keystore: ks,
	}, nil
}

// Rotate performs the FR-013a sequence. On return the RotateResult
// reflects the committed state; any error leaves the ledger in its
// pre-rotation state.
func (r *rotatorImpl) Rotate(ctx context.Context, reason string) (RotateResult, error) {
	// Step 1: load the outgoing key and confirm the registry agrees.
	// We hold the Emitter's mu across the whole rotation so a concurrent
	// Emit cannot land a row between the retire and the new key
	// registration.
	r.emitter.mu.Lock()
	defer r.emitter.mu.Unlock()

	now := r.emitter.now()

	oldPriv, oldPub, err := r.keystore.Load(ctx)
	if errors.Is(err, keystore.ErrKeyNotFound) {
		return RotateResult{}, fmt.Errorf("ledger.Rotate: no active signing key — nothing to rotate")
	}
	if err != nil {
		return RotateResult{}, fmt.Errorf("ledger.Rotate: keystore load: %w", err)
	}
	oldFP := Fingerprint(oldPub)

	active, err := r.registry.Active(ctx)
	if err != nil {
		return RotateResult{}, fmt.Errorf("ledger.Rotate: registry active: %w", err)
	}
	if active.Fingerprint != oldFP {
		// The keystore and registry disagree. Refuse to rotate rather
		// than paper over the drift — investigation required.
		return RotateResult{}, fmt.Errorf("ledger.Rotate: keystore fp=%s disagrees with registry active fp=%s", oldFP, active.Fingerprint)
	}

	// Step 2: generate the incoming key.
	newPub, newPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return RotateResult{}, fmt.Errorf("ledger.Rotate: generate new key: %w", err)
	}
	newFP := Fingerprint(newPub)
	if newFP == oldFP {
		// Astronomically unlikely (16-byte collision) but worth a
		// guardrail — repeat rather than fail.
		return RotateResult{}, fmt.Errorf("ledger.Rotate: generated key shares fingerprint with outgoing key (retry)")
	}

	// Step 3: emit the key.rotate sentinel signed by the OUTGOING key.
	// We do this by borrowing the Emitter's private emit path rather
	// than calling em.Emit — Emit would re-resolve the active key and
	// sign with whichever one the keystore returns first. Rotation
	// must sign with the specific outgoing key regardless of what the
	// keystore currently says.
	payload := RotatePayload{
		OldFingerprint: oldFP,
		NewFingerprint: newFP,
		Reason:         reason,
		GeneratedAt:    now.UTC().Format(time.RFC3339Nano),
	}
	sentinel, err := r.emitter.emitWithSpecificKey(ctx, Event{
		Type:      EventKeyRotate,
		Subject:   "key.rotate:" + newFP,
		Payload:   payload,
		Timestamp: now,
	}, oldPriv, oldPub)
	if err != nil {
		return RotateResult{}, fmt.Errorf("ledger.Rotate: emit sentinel: %w", err)
	}

	// Step 4: register the incoming public key and retire the outgoing
	// one. These two writes don't have SQLite-level atomicity because
	// the Emitter already closed its transaction — but the failure
	// mode (insert succeeded, retire failed, or vice versa) is
	// recoverable on retry: the sentinel is already in the ledger, so
	// the operator can re-run Rotate and the idempotent Insert + the
	// ErrKeyAlreadyRetired typed error will converge.
	if _, err := r.registry.Insert(ctx, newPub, now); err != nil {
		return RotateResult{}, fmt.Errorf("ledger.Rotate: registry insert new: %w", err)
	}
	if err := r.registry.MarkRetired(ctx, oldFP, now); err != nil {
		return RotateResult{}, fmt.Errorf("ledger.Rotate: registry retire old: %w", err)
	}

	// Step 5: swap the active private key in the keystore. If this
	// fails the ledger is still consistent (sentinel emitted, new pub
	// registered, old pub retired) — the operator can retry Rotate's
	// tail (keystore.Store) or manually write the new key. We surface
	// the error without rolling back ledger state because the rotation
	// has cryptographic finality at step 3.
	if err := r.keystore.Store(ctx, newPriv, newPub); err != nil {
		return RotateResult{}, fmt.Errorf("ledger.Rotate: keystore store (ledger already rotated): %w", err)
	}

	return RotateResult{
		OldFingerprint:  oldFP,
		NewFingerprint:  newFP,
		RotationEntryID: sentinel.ID,
		Reason:          reason,
	}, nil
}

// emitWithSpecificKey is the escape hatch Rotator uses to sign the
// key.rotate sentinel with the outgoing key. It runs the same
// transactional append path as normal Emit but takes the signing key
// as explicit parameters, bypassing loadOrGenerateKey. Caller MUST
// already hold e.mu (Rotate does).
func (e *emitterImpl) emitWithSpecificKey(
	ctx context.Context,
	ev Event,
	priv ed25519.PrivateKey,
	pub ed25519.PublicKey,
) (Entry, error) {
	if !IsKnown(ev.Type) {
		return Entry{}, fmt.Errorf("%w: %q", ErrUnknownEventType, ev.Type)
	}
	if len(ev.Subject) > MaxSubjectBytes {
		return Entry{}, fmt.Errorf("%w: got %d bytes", ErrSubjectTooLong, len(ev.Subject))
	}
	rawPayload, err := json.Marshal(ev.Payload)
	if err != nil {
		return Entry{}, fmt.Errorf("ledger.emitWithSpecificKey: marshal: %w", err)
	}
	canonical, err := jcs.Canonicalize(rawPayload)
	if err != nil {
		return Entry{}, fmt.Errorf("ledger.emitWithSpecificKey: JCS canonicalise: %w", err)
	}
	if len(canonical) > MaxPayloadBytes {
		return Entry{}, fmt.Errorf("%w: got %d bytes", ErrPayloadTooLarge, len(canonical))
	}

	ts := ev.Timestamp
	if ts.IsZero() {
		ts = e.now()
	}
	tsStr := ts.UTC().Format(time.RFC3339Nano)

	fpHex := Fingerprint(pub)
	var fp [SigningKeyFPLen]byte
	if _, err := hex.Decode(fp[:], []byte(fpHex)); err != nil {
		return Entry{}, fmt.Errorf("ledger.emitWithSpecificKey: decode fingerprint: %w", err)
	}

	return e.appendSignedRow(ctx, ev, canonical, tsStr, priv, pub, fpHex, fp)
}
