package ledger

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sigil-tech/sigil/internal/ledger/keystore"
)

// memoryKeystore is an in-process KeyStorage used throughout the Emit /
// Verify / Rotate tests. The real file-backed backends exist to
// survive daemon restarts; tests never restart the daemon.
type memoryKeystore struct {
	mu   sync.Mutex
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

func newMemoryKeystore() *memoryKeystore { return &memoryKeystore{} }

func (m *memoryKeystore) Load(context.Context) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.priv == nil {
		return nil, nil, keystore.ErrKeyNotFound
	}
	return m.priv, m.pub, nil
}

func (m *memoryKeystore) Store(_ context.Context, priv ed25519.PrivateKey, pub ed25519.PublicKey) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.priv = priv
	m.pub = pub
	return nil
}

func (m *memoryKeystore) Backend() string { return "memory" }

// Verify memoryKeystore satisfies keystore.KeyStorage at compile time.
var _ keystore.KeyStorage = (*memoryKeystore)(nil)

// newTestEmitter wires a fresh Emitter with an in-memory keystore and
// the shared openMemoryDB helper. Returns the Emitter and the DB so
// tests can observe the persisted rows directly.
func newTestEmitter(t *testing.T, now func() time.Time) (*sql.DB, *memoryKeystore, KeyRegistry, Emitter) {
	t.Helper()
	ctx := context.Background()
	db := openMemoryDB(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	ks := newMemoryKeystore()
	reg := NewKeyRegistry(db)
	opts := []EmitterOption{}
	if now != nil {
		opts = append(opts, WithClock(now))
	}
	return db, ks, reg, NewEmitter(db, ks, reg, opts...)
}

func TestEmit(t *testing.T) {
	ctx := context.Background()

	t.Run("first Emit generates key, registers it, and writes genesis row", func(t *testing.T) {
		db, ks, reg, em := newTestEmitter(t, nil)

		entry, err := em.Emit(ctx, Event{
			Type:    EventVMSpawn,
			Subject: "vm-abc",
			Payload: map[string]any{"sandbox_id": "abc", "host": "mbp-2017"},
		})
		if err != nil {
			t.Fatalf("Emit: %v", err)
		}

		if entry.ID != 1 {
			t.Fatalf("first Emit id = %d, want 1", entry.ID)
		}
		if entry.PrevHash != strings.Repeat("0", 64) {
			t.Fatalf("genesis prev_hash = %q, want 64 zeros", entry.PrevHash)
		}
		if len(entry.Hash) != 64 {
			t.Fatalf("hash length = %d, want 64", len(entry.Hash))
		}
		if len(entry.Signature) != 128 {
			t.Fatalf("signature length = %d, want 128", len(entry.Signature))
		}
		if len(entry.SigningKeyFP) != FingerprintLen {
			t.Fatalf("fingerprint length = %d, want %d", len(entry.SigningKeyFP), FingerprintLen)
		}

		// Keystore now holds a keypair.
		priv, pub, err := ks.Load(ctx)
		if err != nil {
			t.Fatalf("Load after Emit: %v", err)
		}
		if priv == nil || pub == nil {
			t.Fatalf("keypair not materialised after first Emit")
		}

		// Registry carries the active key.
		active, err := reg.Active(ctx)
		if err != nil {
			t.Fatalf("registry.Active: %v", err)
		}
		if active.Fingerprint != entry.SigningKeyFP {
			t.Fatalf("registry fp=%q, entry fp=%q", active.Fingerprint, entry.SigningKeyFP)
		}

		// Row is visible via Reader.
		r := NewReader(db)
		got, err := r.Get(ctx, 1)
		if err != nil {
			t.Fatalf("Reader.Get: %v", err)
		}
		if got.Hash != entry.Hash {
			t.Fatalf("Reader hash mismatch: %q vs %q", got.Hash, entry.Hash)
		}
	})

	t.Run("successive Emits maintain the hash chain", func(t *testing.T) {
		_, _, _, em := newTestEmitter(t, nil)
		prev := ""
		for i := 1; i <= 5; i++ {
			entry, err := em.Emit(ctx, Event{
				Type:    EventVMSpawn,
				Subject: "seq",
				Payload: map[string]any{"i": i},
			})
			if err != nil {
				t.Fatalf("Emit %d: %v", i, err)
			}
			if entry.ID != int64(i) {
				t.Fatalf("id = %d, want %d", entry.ID, i)
			}
			switch i {
			case 1:
				if entry.PrevHash != strings.Repeat("0", 64) {
					t.Fatalf("row 1 prev_hash should be genesis sentinel")
				}
			default:
				if entry.PrevHash != prev {
					t.Fatalf("row %d prev_hash = %q, want %q", i, entry.PrevHash, prev)
				}
			}
			prev = entry.Hash
		}
	})

	t.Run("signature verifies against the registry public key", func(t *testing.T) {
		_, _, reg, em := newTestEmitter(t, nil)
		entry, err := em.Emit(ctx, Event{
			Type:    EventModelMerge,
			Subject: "merge-xyz",
			Payload: map[string]any{"session_id": "xyz"},
		})
		if err != nil {
			t.Fatalf("Emit: %v", err)
		}
		rec, err := reg.LookupByFingerprint(ctx, entry.SigningKeyFP)
		if err != nil {
			t.Fatalf("Lookup: %v", err)
		}
		pub, err := hex.DecodeString(rec.PublicKey)
		if err != nil {
			t.Fatalf("decode pubkey: %v", err)
		}
		hashBytes, err := hex.DecodeString(entry.Hash)
		if err != nil {
			t.Fatalf("decode hash: %v", err)
		}
		sigBytes, err := hex.DecodeString(entry.Signature)
		if err != nil {
			t.Fatalf("decode sig: %v", err)
		}
		if !ed25519.Verify(pub, hashBytes, sigBytes) {
			t.Fatalf("signature does not verify")
		}
	})

	t.Run("unknown type is rejected", func(t *testing.T) {
		_, _, _, em := newTestEmitter(t, nil)
		_, err := em.Emit(ctx, Event{Type: "some.made.up.type", Subject: "x", Payload: nil})
		if !errors.Is(err, ErrUnknownEventType) {
			t.Fatalf("expected ErrUnknownEventType, got %v", err)
		}
	})

	t.Run("oversized subject is rejected", func(t *testing.T) {
		_, _, _, em := newTestEmitter(t, nil)
		big := strings.Repeat("a", MaxSubjectBytes+1)
		_, err := em.Emit(ctx, Event{Type: EventVMSpawn, Subject: big, Payload: nil})
		if !errors.Is(err, ErrSubjectTooLong) {
			t.Fatalf("expected ErrSubjectTooLong, got %v", err)
		}
	})

	t.Run("oversized payload is rejected", func(t *testing.T) {
		_, _, _, em := newTestEmitter(t, nil)
		blob := strings.Repeat("x", MaxPayloadBytes+100)
		_, err := em.Emit(ctx, Event{Type: EventVMSpawn, Subject: "x", Payload: map[string]string{"blob": blob}})
		if !errors.Is(err, ErrPayloadTooLarge) {
			t.Fatalf("expected ErrPayloadTooLarge, got %v", err)
		}
	})

	t.Run("sentinel event types are accepted", func(t *testing.T) {
		_, _, _, em := newTestEmitter(t, nil)
		for _, typ := range []EventType{EventKeyRotate, EventPurgeInvoked} {
			if _, err := em.Emit(ctx, Event{Type: typ, Subject: "sentinel", Payload: nil}); err != nil {
				t.Fatalf("Emit sentinel %q: %v", typ, err)
			}
		}
	})

	t.Run("timestamp passed through, UTC-coerced", func(t *testing.T) {
		pinned := time.Date(2026, 4, 19, 12, 34, 56, 789000000, time.UTC)
		_, _, _, em := newTestEmitter(t, func() time.Time { return pinned })
		entry, err := em.Emit(ctx, Event{Type: EventVMSpawn, Subject: "x", Payload: nil})
		if err != nil {
			t.Fatalf("Emit: %v", err)
		}
		got, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
		if err != nil {
			t.Fatalf("parse ts: %v", err)
		}
		if !got.Equal(pinned) {
			t.Fatalf("ts = %v, want %v", got, pinned)
		}
	})

	t.Run("explicit event timestamp overrides clock", func(t *testing.T) {
		_, _, _, em := newTestEmitter(t, func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) })
		want := time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC)
		entry, err := em.Emit(ctx, Event{Type: EventVMSpawn, Subject: "x", Payload: nil, Timestamp: want})
		if err != nil {
			t.Fatalf("Emit: %v", err)
		}
		got, _ := time.Parse(time.RFC3339Nano, entry.Timestamp)
		if !got.Equal(want) {
			t.Fatalf("explicit ts ignored: got %v want %v", got, want)
		}
	})

	_ = slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestEmit_NonJSONPayload asserts the Emit path tolerates a variety of
// payload-value shapes (map, struct-pointer, slice, primitive) by
// routing each through json.Marshal + jcs.Canonicalize. A failure
// here points at a regression in the Emit→JCS glue.
func TestEmit_NonJSONPayload(t *testing.T) {
	ctx := context.Background()
	_, _, _, em := newTestEmitter(t, nil)

	cases := []struct {
		name    string
		payload any
	}{
		{"nil", nil},
		{"map", map[string]any{"k": "v", "n": 7}},
		{"slice", []string{"a", "b", "c"}},
		{"primitive string", "hello"},
		{"primitive number", 42},
		{"nested", map[string]any{"outer": map[string]any{"inner": []int{1, 2, 3}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := em.Emit(ctx, Event{
				Type: EventVMSpawn, Subject: tc.name, Payload: tc.payload,
			}); err != nil {
				t.Fatalf("Emit: %v", err)
			}
		})
	}
}
