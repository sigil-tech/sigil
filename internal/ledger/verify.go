package ledger

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/sigil-tech/sigil/internal/ledger/jcs"
)

// VerifyBreakReason is a stable machine-readable enum surfaced by the
// Verifier on failure. Strings are part of the spec 029 wire contract
// (contracts/ledger-wire.md — populated in Phase 6) so they MUST NOT
// change once shipped.
type VerifyBreakReason string

const (
	// VerifyBreakHashMismatch — the recomputed hash does not match the
	// stored hash. Either a row was rewritten after signing, or the
	// hash input format drifted.
	VerifyBreakHashMismatch VerifyBreakReason = "hash_mismatch"

	// VerifyBreakPrevHashMismatch — row N's prev_hash does not match
	// row N-1's hash. Indicates a row was deleted or reordered.
	VerifyBreakPrevHashMismatch VerifyBreakReason = "prev_hash_mismatch"

	// VerifyBreakSignatureMismatch — the signature does not verify
	// under the row's signing_key_fp. Either the row was re-signed
	// with a different key, or the hash was tampered and resigned
	// but the public key was not updated.
	VerifyBreakSignatureMismatch VerifyBreakReason = "signature_mismatch"

	// VerifyBreakUnknownKey — the row's signing_key_fp is not in
	// the `ledger_keys` registry. Either the key was deleted from
	// the registry (the append-only trigger should block this) or a
	// row from a different ledger was spliced in.
	VerifyBreakUnknownKey VerifyBreakReason = "unknown_key"

	// VerifyBreakIDDiscontinuity — row ids are not monotonically
	// increasing by 1. Either a row was deleted, or a row was
	// inserted with a non-sequential id.
	VerifyBreakIDDiscontinuity VerifyBreakReason = "id_discontinuity"

	// VerifyBreakCanonicalPayload — the stored payload is not in JCS
	// canonical form. This would only happen if raw SQL bypassed the
	// Emitter; we still report it because the hash is computed over
	// the canonical form and a drift would fail hash verification
	// anyway — this reason simply gives the operator a more useful
	// handle than "hash mismatch" when the root cause is the payload
	// encoding.
	VerifyBreakCanonicalPayload VerifyBreakReason = "non_canonical_payload"
)

// VerifyResult is the outcome of a Verifier.Verify call. OK=true
// means every row in the requested scope verified cleanly; OK=false
// means one or more rows failed and BreakAtID points at the first
// problem encountered (in ascending id order).
type VerifyResult struct {
	OK             bool              `json:"ok"`
	EntriesChecked int               `json:"entries_checked"`
	BreakAtID      int64             `json:"break_at_id,omitempty"`
	Reason         VerifyBreakReason `json:"reason,omitempty"`
	Detail         string            `json:"detail,omitempty"`
}

// VerifyScope selects which entries the Verifier inspects.
//
//   - Full=true ignores FromID/ToID and walks every row in the ledger.
//   - Otherwise the walk covers rows with FromID ≤ id ≤ ToID. FromID=0
//     means "from the beginning"; ToID=0 means "to the tip".
//
// A range that only covers a suffix of the chain still verifies
// correctly because every row's prev_hash references the preceding
// hash — the Verifier walks from FromID-1 (or genesis if FromID==1)
// so it can validate the continuity of the first row in the range.
//
// Single-entry verification is expressed as FromID=ToID=id.
type VerifyScope struct {
	Full   bool
	FromID int64
	ToID   int64
}

// Verifier inspects the ledger chain and reports whether it is
// internally consistent.
type Verifier interface {
	Verify(ctx context.Context, scope VerifyScope) (VerifyResult, error)
}

// verifierImpl caches verification outcomes across calls within the
// same daemon lifetime. The cache key is (tip_id, key_registry_digest):
// either a new emit or a key rotation mutates one of those and
// invalidates the cache line. See spec 029 plan §4.3 and the Q3
// clarification ("session cache keyed by (tip_id, key_registry_digest)").
type verifierImpl struct {
	db       *sql.DB
	registry KeyRegistry
	reader   Reader

	cacheMu sync.Mutex
	cache   map[verifierCacheKey]VerifyResult
}

type verifierCacheKey struct {
	TipID   int64
	RegHash string // SHA-256 of the registry row set
	Scope   VerifyScope
}

// NewVerifier constructs a Verifier wired to the supplied database.
// The Verifier holds a Reader handle so it can share IterateAll's
// tip-snapshot discipline without replicating the query.
func NewVerifier(db *sql.DB, reg KeyRegistry) Verifier {
	return &verifierImpl{
		db:       db,
		registry: reg,
		reader:   NewReader(db),
		cache:    make(map[verifierCacheKey]VerifyResult),
	}
}

// Verify walks the selected scope and checks every invariant:
//
//  1. ids are monotonically increasing by 1 (no gaps)
//  2. recomputed hash matches stored hash
//  3. prev_hash on row N matches stored hash on row N-1
//  4. signature verifies under the row's signing_key_fp (looked up
//     in the registry)
//  5. the registered public key exists
//
// On the first violation Verify returns a failed result with the
// offending id and a stable machine-readable reason.
func (v *verifierImpl) Verify(ctx context.Context, scope VerifyScope) (VerifyResult, error) {
	tipID, regHash, err := v.snapshotState(ctx)
	if err != nil {
		return VerifyResult{}, err
	}
	key := verifierCacheKey{TipID: tipID, RegHash: regHash, Scope: scope}

	v.cacheMu.Lock()
	cached, ok := v.cache[key]
	v.cacheMu.Unlock()
	if ok {
		return cached, nil
	}

	result, err := v.verifyUncached(ctx, scope, tipID)
	if err != nil {
		return VerifyResult{}, err
	}

	v.cacheMu.Lock()
	v.cache[key] = result
	v.cacheMu.Unlock()
	return result, nil
}

// verifyUncached is the core walk. Keeps the cache discipline out of
// the invariant-checking logic.
func (v *verifierImpl) verifyUncached(ctx context.Context, scope VerifyScope, tipID int64) (VerifyResult, error) {
	from, to := scope.FromID, scope.ToID
	if scope.Full || (from == 0 && to == 0) {
		from = 1
		to = tipID
	}
	if to == 0 {
		to = tipID
	}
	if from == 0 {
		from = 1
	}
	if tipID == 0 {
		return VerifyResult{OK: true, EntriesChecked: 0}, nil
	}
	if from > to {
		return VerifyResult{}, fmt.Errorf("ledger.Verify: from=%d > to=%d", from, to)
	}

	// Seed prevHash from row (from-1) so the walk can validate
	// continuity at the range boundary. For from==1 the seed is the
	// genesis sentinel.
	prevHash := hex.EncodeToString(GenesisPrevHash[:])
	if from > 1 {
		seed, err := v.seedPrevHash(ctx, from-1)
		if err != nil {
			return VerifyResult{}, err
		}
		prevHash = seed
	}

	pubCache, err := v.loadKeyCache(ctx)
	if err != nil {
		return VerifyResult{}, err
	}

	rows, err := v.db.QueryContext(ctx,
		`SELECT `+selectColumns+` FROM ledger WHERE id BETWEEN ? AND ? ORDER BY id ASC`,
		from, to,
	)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("ledger.Verify: query: %w", err)
	}
	defer rows.Close()

	var (
		prevID  int64
		checked int
	)
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return VerifyResult{}, err
		}
		e, err := scanEntry(rows)
		if err != nil {
			return VerifyResult{}, fmt.Errorf("ledger.Verify: scan: %w", err)
		}

		// Continuity: id sequence. The first row in the range must
		// equal `from`; thereafter every row's id must be prev+1.
		if prevID == 0 {
			if e.ID != from {
				return VerifyResult{
					OK: false, EntriesChecked: checked, BreakAtID: e.ID,
					Reason: VerifyBreakIDDiscontinuity,
					Detail: fmt.Sprintf("range starts at id=%d, want %d", e.ID, from),
				}, nil
			}
		} else if e.ID != prevID+1 {
			return VerifyResult{
				OK: false, EntriesChecked: checked, BreakAtID: e.ID,
				Reason: VerifyBreakIDDiscontinuity,
				Detail: fmt.Sprintf("expected id=%d, got id=%d", prevID+1, e.ID),
			}, nil
		}

		// Continuity: prev_hash chaining
		if e.PrevHash != prevHash {
			return VerifyResult{
				OK: false, EntriesChecked: checked, BreakAtID: e.ID,
				Reason: VerifyBreakPrevHashMismatch,
				Detail: fmt.Sprintf("row %d prev_hash=%s, want %s", e.ID, e.PrevHash, prevHash),
			}, nil
		}

		// Signing key must be registered BEFORE we recompute the hash
		// so a row referencing a fingerprint unknown to the registry
		// is flagged as unknown_key rather than hash_mismatch. The
		// hash will fail the moment any single field drifts, so the
		// ordering here matters for the operator's root-cause UX.
		pubHex, keyKnown := pubCache[e.SigningKeyFP]
		if !keyKnown {
			return VerifyResult{
				OK: false, EntriesChecked: checked, BreakAtID: e.ID,
				Reason: VerifyBreakUnknownKey,
				Detail: fmt.Sprintf("row %d signing_key_fp=%s not in registry", e.ID, e.SigningKeyFP),
			}, nil
		}

		// Canonical-payload + hash recomputation. A JCS-decode failure
		// is surfaced as non_canonical_payload rather than a raw Go
		// error — a malformed stored payload is still a chain break
		// the operator needs to see, not an infrastructure fault.
		canonical, err := jcs.Canonicalize([]byte(e.PayloadJSON))
		if err != nil {
			return VerifyResult{
				OK: false, EntriesChecked: checked, BreakAtID: e.ID,
				Reason: VerifyBreakCanonicalPayload,
				Detail: fmt.Sprintf("row %d payload JCS decode: %v", e.ID, err),
			}, nil
		}
		if string(canonical) != e.PayloadJSON {
			return VerifyResult{
				OK: false, EntriesChecked: checked, BreakAtID: e.ID,
				Reason: VerifyBreakCanonicalPayload,
				Detail: fmt.Sprintf("row %d payload is not JCS-canonical", e.ID),
			}, nil
		}

		var prev [PrevHashLen]byte
		if _, err := hex.Decode(prev[:], []byte(e.PrevHash)); err != nil {
			return VerifyResult{}, fmt.Errorf("ledger.Verify: decode prev_hash row %d: %w", e.ID, err)
		}
		var fp [SigningKeyFPLen]byte
		if _, err := hex.Decode(fp[:], []byte(e.SigningKeyFP)); err != nil {
			return VerifyResult{}, fmt.Errorf("ledger.Verify: decode fingerprint row %d: %w", e.ID, err)
		}
		sum := Hash(CanonicalInput{
			ID:               e.ID,
			Timestamp:        e.Timestamp,
			Type:             e.Type,
			Subject:          e.Subject,
			CanonicalPayload: canonical,
			PrevHash:         prev,
			SigningKeyFP:     fp,
		})
		if hex.EncodeToString(sum[:]) != e.Hash {
			return VerifyResult{
				OK: false, EntriesChecked: checked, BreakAtID: e.ID,
				Reason: VerifyBreakHashMismatch,
				Detail: fmt.Sprintf("row %d recomputed hash ≠ stored hash", e.ID),
			}, nil
		}

		// Signature
		pub, err := hex.DecodeString(pubHex)
		if err != nil {
			return VerifyResult{}, fmt.Errorf("ledger.Verify: decode pubkey row %d: %w", e.ID, err)
		}
		sigBytes, err := hex.DecodeString(e.Signature)
		if err != nil {
			return VerifyResult{}, fmt.Errorf("ledger.Verify: decode signature row %d: %w", e.ID, err)
		}
		if !ed25519.Verify(pub, sum[:], sigBytes) {
			return VerifyResult{
				OK: false, EntriesChecked: checked, BreakAtID: e.ID,
				Reason: VerifyBreakSignatureMismatch,
				Detail: fmt.Sprintf("row %d signature does not verify", e.ID),
			}, nil
		}

		checked++
		prevID = e.ID
		prevHash = e.Hash
	}
	if err := rows.Err(); err != nil {
		return VerifyResult{}, fmt.Errorf("ledger.Verify: rows: %w", err)
	}

	return VerifyResult{OK: true, EntriesChecked: checked}, nil
}

// seedPrevHash looks up row id's hash for prev_hash-continuity seeding
// at a range scan boundary. Returns ErrEntryNotFound if the row is
// missing (a hole in the chain prior to the requested scope).
func (v *verifierImpl) seedPrevHash(ctx context.Context, id int64) (string, error) {
	var h string
	err := v.db.QueryRowContext(ctx, `SELECT hash FROM ledger WHERE id = ?`, id).Scan(&h)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("ledger.Verify: seed row id=%d: %w", id, ErrEntryNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("ledger.Verify: seed row id=%d: %w", id, err)
	}
	return h, nil
}

// snapshotState reads the current tip id and a digest of the key
// registry. The digest covers every row's (fingerprint, retired_at)
// tuple so a rotation (even without a new emit) invalidates the cache.
func (v *verifierImpl) snapshotState(ctx context.Context) (int64, string, error) {
	var tipID int64
	if err := v.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM ledger`).Scan(&tipID); err != nil {
		return 0, "", fmt.Errorf("ledger.Verify: tip read: %w", err)
	}

	rows, err := v.db.QueryContext(ctx,
		`SELECT fingerprint, COALESCE(retired_at,'')
		   FROM ledger_keys
		  ORDER BY fingerprint ASC`,
	)
	if err != nil {
		return 0, "", fmt.Errorf("ledger.Verify: registry digest: %w", err)
	}
	defer rows.Close()

	h := sha256.New()
	for rows.Next() {
		var fp, retired string
		if err := rows.Scan(&fp, &retired); err != nil {
			return 0, "", fmt.Errorf("ledger.Verify: registry scan: %w", err)
		}
		h.Write([]byte(fp))
		h.Write([]byte{0})
		h.Write([]byte(retired))
		h.Write([]byte{0})
	}
	if err := rows.Err(); err != nil {
		return 0, "", fmt.Errorf("ledger.Verify: registry rows: %w", err)
	}
	return tipID, hex.EncodeToString(h.Sum(nil)), nil
}

// loadKeyCache pulls every registered public key into memory so the
// hot Verify loop does not round-trip to SQLite per row. The cache is
// scoped to one Verify call; it is rebuilt from scratch on the next
// call (which may see a rotation-updated registry).
func (v *verifierImpl) loadKeyCache(ctx context.Context) (map[string]string, error) {
	all, err := v.registry.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("ledger.Verify: load registry: %w", err)
	}
	out := make(map[string]string, len(all))
	for _, k := range all {
		out[k.Fingerprint] = k.PublicKey
	}
	return out, nil
}

// sortable guard to keep go vet happy about unused "sort" imports when
// refactors remove the only consumer. Currently no live use, but the
// import has been useful in intermediate shapes; keep the side-effect
// reference small and isolated.
var _ = sort.Strings

// ErrNoRowsInScope is returned when a non-full scope has no matching
// rows. Exposed so callers (e.g., sigilctl) can distinguish "range
// was empty" from "range was broken".
var ErrNoRowsInScope = errors.New("ledger.Verify: no rows in scope")
