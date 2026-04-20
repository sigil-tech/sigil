package ledger

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// FingerprintLen is the hex-character length of a ledger signing-key
// fingerprint (32 hex chars = 16 raw bytes, the first half of a
// SHA-256 pubkey digest). The raw byte count is SigningKeyFPLen (16).
const FingerprintLen = SigningKeyFPLen * 2

// ErrKeyNotFound is returned by LookupByFingerprint when no row
// matches. Distinct from keystore.ErrKeyNotFound (which is about the
// private key at rest) — this one is about the registry row.
var ErrKeyNotFound = errors.New("ledger.KeyRegistry: key not found")

// ErrKeyAlreadyRetired is returned by MarkRetired when the target key
// was already retired. Rotation is a one-shot event per key; a second
// rotation against the same fingerprint is a bug.
var ErrKeyAlreadyRetired = errors.New("ledger.KeyRegistry: key already retired")

// KeyRecord is one row of the `ledger_keys` table. Fingerprint and
// PublicKey are lowercase hex strings per the schema contract.
// RetiredAt is zero when the key is active.
type KeyRecord struct {
	Fingerprint string
	PublicKey   string    // 64 hex chars (raw 32-byte ed25519 public key)
	GeneratedAt time.Time // RFC 3339 UTC
	RetiredAt   time.Time // zero = active
}

// Active returns true iff the key has no retired_at timestamp.
func (k KeyRecord) Active() bool { return k.RetiredAt.IsZero() }

// KeyRegistry manages the `ledger_keys` append-only public-key
// registry. Implementations are safe for concurrent reads; writes
// (Insert, MarkRetired) serialise through a single writer upstream
// (the Rotator in Phase 4).
type KeyRegistry interface {
	// Insert records a newly-generated public key. Idempotent on the
	// fingerprint PK — a duplicate Insert for the same fingerprint is
	// a no-op (supports resuming after a mid-rotation crash where the
	// row was committed but the caller never heard the ack).
	Insert(ctx context.Context, pub []byte, generatedAt time.Time) (KeyRecord, error)

	// MarkRetired sets retired_at on the given fingerprint. Returns
	// ErrKeyNotFound if the fingerprint is unknown, or
	// ErrKeyAlreadyRetired if retired_at is already set — the schema
	// trigger also blocks re-retirement but we surface a typed error
	// rather than a raw SQLITE_CONSTRAINT to keep the caller path
	// clean.
	MarkRetired(ctx context.Context, fingerprint string, retiredAt time.Time) error

	// LookupByFingerprint returns the registry record for the given
	// fingerprint, or ErrKeyNotFound. Used by the Verifier to pick the
	// public key to validate a given row's signature.
	LookupByFingerprint(ctx context.Context, fingerprint string) (KeyRecord, error)

	// Active returns the currently-active (retired_at IS NULL) key, or
	// ErrKeyNotFound if none exists (first boot). Used by the Emitter
	// to find the key that should sign the next row.
	Active(ctx context.Context) (KeyRecord, error)

	// ListAll returns every registry row in Insert order (oldest first).
	// Used by the `sigilctl ledger key` command to surface the full
	// rotation history to operators.
	ListAll(ctx context.Context) ([]KeyRecord, error)
}

// keyRegistry is the database-backed KeyRegistry.
type keyRegistry struct {
	db *sql.DB
}

// NewKeyRegistry wires a KeyRegistry to the supplied database. The
// database MUST already be migrated.
func NewKeyRegistry(db *sql.DB) KeyRegistry {
	return &keyRegistry{db: db}
}

// Fingerprint computes the canonical 32-hex-char fingerprint for an
// ed25519 public key (first 16 bytes of SHA-256(pubkey), hex-encoded).
// Exported so the Emitter and Rotator can compute fingerprints without
// depending on an implementation of KeyRegistry.
func Fingerprint(pub []byte) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:SigningKeyFPLen])
}

func (r *keyRegistry) Insert(ctx context.Context, pub []byte, generatedAt time.Time) (KeyRecord, error) {
	if len(pub) == 0 {
		return KeyRecord{}, fmt.Errorf("ledger.KeyRegistry.Insert: empty public key")
	}
	fp := Fingerprint(pub)
	pubHex := hex.EncodeToString(pub)
	ts := generatedAt.UTC().Format(time.RFC3339Nano)

	// OR IGNORE makes Insert idempotent on the fingerprint PK —
	// re-running rotation after a mid-write crash does not fail with
	// UNIQUE violation.
	if _, err := r.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO ledger_keys (fingerprint, public_key, generated_at, retired_at)
		 VALUES (?, ?, ?, NULL)`,
		fp, pubHex, ts,
	); err != nil {
		return KeyRecord{}, fmt.Errorf("ledger.KeyRegistry.Insert fp=%s: %w", fp, err)
	}

	// Re-read the canonical row so the caller sees the version that
	// actually landed (matters on the OR IGNORE path — a pre-existing
	// row keeps its original generated_at).
	return r.LookupByFingerprint(ctx, fp)
}

func (r *keyRegistry) MarkRetired(ctx context.Context, fingerprint string, retiredAt time.Time) error {
	if len(fingerprint) != FingerprintLen {
		return fmt.Errorf("ledger.KeyRegistry.MarkRetired: fingerprint %q has %d chars, want %d", fingerprint, len(fingerprint), FingerprintLen)
	}
	ts := retiredAt.UTC().Format(time.RFC3339Nano)

	// Check existence and retired-state first so callers get the
	// typed errors instead of a generic SQLite constraint failure
	// bubbled through from the trigger.
	existing, err := r.LookupByFingerprint(ctx, fingerprint)
	if err != nil {
		return err
	}
	if !existing.RetiredAt.IsZero() {
		return fmt.Errorf("%w: fp=%s retired_at=%s", ErrKeyAlreadyRetired, fingerprint, existing.RetiredAt.Format(time.RFC3339))
	}

	res, err := r.db.ExecContext(ctx,
		`UPDATE ledger_keys SET retired_at=? WHERE fingerprint=? AND retired_at IS NULL`,
		ts, fingerprint,
	)
	if err != nil {
		return fmt.Errorf("ledger.KeyRegistry.MarkRetired fp=%s: %w", fingerprint, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("ledger.KeyRegistry.MarkRetired RowsAffected: %w", err)
	}
	if n == 0 {
		// Lost the retire race with another caller — surface as
		// already-retired rather than swallow.
		return fmt.Errorf("%w: fp=%s (lost retire race)", ErrKeyAlreadyRetired, fingerprint)
	}
	return nil
}

func (r *keyRegistry) LookupByFingerprint(ctx context.Context, fingerprint string) (KeyRecord, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT fingerprint, public_key, generated_at, retired_at
		   FROM ledger_keys WHERE fingerprint = ?`,
		fingerprint,
	)
	rec, err := scanKeyRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return KeyRecord{}, fmt.Errorf("%w: fp=%s", ErrKeyNotFound, fingerprint)
	}
	if err != nil {
		return KeyRecord{}, fmt.Errorf("ledger.KeyRegistry.LookupByFingerprint fp=%s: %w", fingerprint, err)
	}
	return rec, nil
}

func (r *keyRegistry) Active(ctx context.Context) (KeyRecord, error) {
	// Multiple active keys would be a drift bug — the Emitter's single-
	// writer discipline prevents it, but we defend here too with LIMIT 1
	// and a tie-break by generated_at DESC (newer wins if somehow two
	// rows end up active).
	row := r.db.QueryRowContext(ctx,
		`SELECT fingerprint, public_key, generated_at, retired_at
		   FROM ledger_keys
		  WHERE retired_at IS NULL
		  ORDER BY generated_at DESC
		  LIMIT 1`,
	)
	rec, err := scanKeyRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return KeyRecord{}, fmt.Errorf("%w: no active key", ErrKeyNotFound)
	}
	if err != nil {
		return KeyRecord{}, fmt.Errorf("ledger.KeyRegistry.Active: %w", err)
	}
	return rec, nil
}

func (r *keyRegistry) ListAll(ctx context.Context) ([]KeyRecord, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT fingerprint, public_key, generated_at, retired_at
		   FROM ledger_keys
		  ORDER BY generated_at ASC, fingerprint ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("ledger.KeyRegistry.ListAll: %w", err)
	}
	defer rows.Close()

	var out []KeyRecord
	for rows.Next() {
		rec, err := scanKeyRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("ledger.KeyRegistry.ListAll scan: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ledger.KeyRegistry.ListAll rows: %w", err)
	}
	return out, nil
}

// scanKeyRecord decodes a key-registry row. retired_at is NULLable; a
// NULL value leaves KeyRecord.RetiredAt as the zero time.Time.
func scanKeyRecord(scanner interface {
	Scan(dest ...any) error
}) (KeyRecord, error) {
	var (
		rec       KeyRecord
		generated string
		retired   sql.NullString
	)
	if err := scanner.Scan(&rec.Fingerprint, &rec.PublicKey, &generated, &retired); err != nil {
		return KeyRecord{}, err
	}
	gen, err := time.Parse(time.RFC3339Nano, generated)
	if err != nil {
		return KeyRecord{}, fmt.Errorf("decode generated_at %q: %w", generated, err)
	}
	rec.GeneratedAt = gen
	if retired.Valid {
		ret, err := time.Parse(time.RFC3339Nano, retired.String)
		if err != nil {
			return KeyRecord{}, fmt.Errorf("decode retired_at %q: %w", retired.String, err)
		}
		rec.RetiredAt = ret
	}
	return rec, nil
}
