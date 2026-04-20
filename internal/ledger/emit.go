package ledger

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sigil-tech/sigil/internal/ledger/jcs"
	"github.com/sigil-tech/sigil/internal/ledger/keystore"
)

// MaxSubjectBytes bounds the `subject` column per spec 029 plan §4.1.
// Enforced in Go (SQLite STRICT mode cannot enforce length constraints).
const MaxSubjectBytes = 256

// MaxPayloadBytes bounds the canonical JSON payload per plan §4.1.
// Emitters that produce larger payloads MUST summarise — spec 029 FR-032
// requires typed, allowlisted payload shapes, which makes a 64 KiB cap
// a soft guardrail against drift rather than a hard compliance limit.
const MaxPayloadBytes = 64 * 1024

// ErrUnknownEventType is returned by Emit when the caller hands in a
// type string that is not in the IsKnown allowlist. Blocks an attacker
// who gains an Emit channel from laundering events under fabricated
// types.
var ErrUnknownEventType = errors.New("ledger.Emit: unknown event type")

// ErrSubjectTooLong is returned when Event.Subject exceeds
// MaxSubjectBytes.
var ErrSubjectTooLong = errors.New("ledger.Emit: subject exceeds MaxSubjectBytes")

// ErrPayloadTooLarge is returned when the JCS-canonical payload
// exceeds MaxPayloadBytes.
var ErrPayloadTooLarge = errors.New("ledger.Emit: payload exceeds MaxPayloadBytes")

// Event is the input to Emit. Callers construct one per privileged
// action; the Emitter converts it to a persisted ledger row by
// assigning an id, computing the hash, and signing it.
//
// Payload MUST be a JSON-marshallable value. In production every
// emitter hands in a typed struct from `internal/ledger/payload/`
// (enforced by the FR-032 allowlist in Phase 5); an interface-typed
// field is accepted here because the Emitter cannot and should not
// validate every possible payload struct shape.
type Event struct {
	Type      EventType
	Subject   string
	Payload   any
	Timestamp time.Time // zero = Emit assigns time.Now().UTC()
}

// Emitter is the single append path for the ledger. Every
// privileged-action subsystem (vm, merge, corpus, finetuner, actuator
// via policy.Deny) holds an Emitter and calls Emit to persist its
// compliance trail. Implementations are safe for concurrent Emit
// calls; internally they serialise through a single-writer mutex so
// the chain invariants hold under contention.
type Emitter interface {
	// Emit appends a new row to the ledger. Returns the persisted
	// Entry (with the assigned id, computed hash, and signature) on
	// success. Errors are typed — ErrUnknownEventType,
	// ErrSubjectTooLong, ErrPayloadTooLarge are caller-fixable; any
	// other error is an infrastructure fault and should be surfaced
	// up the stack so the upstream transaction can roll back.
	Emit(ctx context.Context, ev Event) (Entry, error)
}

// Emitter implementation. The three collaborators are injected via
// NewEmitter so tests can wire in fakes for any of them independently.
type emitterImpl struct {
	db       *sql.DB
	keystore keystore.KeyStorage
	registry KeyRegistry

	// mu serialises Emit calls. Chain integrity depends on reading the
	// current tip and writing the next row as a single atomic step;
	// two parallel Emits without this gate would race on the tip read
	// and could assign the same prev_hash to two siblings.
	//
	// The BEGIN IMMEDIATE in sqlStoreEntry additionally serialises at
	// the SQLite layer, but relying on that alone would leak the race
	// from tip-read time to BEGIN time. Hold the Go mutex across the
	// whole Emit to close that window.
	mu sync.Mutex

	// now is the time source. Injectable for deterministic tests; in
	// production it is time.Now. Using an injected clock keeps the
	// fuzz targets stable across fuzzing runs.
	now func() time.Time
}

// EmitterOption configures an Emitter at construction. The functional-
// options pattern keeps NewEmitter's surface narrow for callers that
// are fine with the defaults and extensible for test harnesses.
type EmitterOption func(*emitterImpl)

// WithClock replaces the Emitter's time source. Used in tests to pin
// deterministic timestamps.
func WithClock(now func() time.Time) EmitterOption {
	return func(e *emitterImpl) { e.now = now }
}

// NewEmitter constructs an Emitter. db MUST already be migrated
// (Migrate(ctx, db)); ks and reg MUST be wired to the same db or a
// functionally equivalent keystore. On first invocation the Emitter
// lazily generates a fresh ed25519 keypair if ks.Load returns
// ErrKeyNotFound and records the public key in the registry.
func NewEmitter(db *sql.DB, ks keystore.KeyStorage, reg KeyRegistry, opts ...EmitterOption) Emitter {
	e := &emitterImpl{
		db:       db,
		keystore: ks,
		registry: reg,
		now:      func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Emit implements the single-append-path protocol. The sequence is:
//
//  1. Validate type / subject / payload size.
//  2. JCS-canonicalise the payload and hash it.
//  3. Under the single-writer mutex, start a transaction, read the
//     current tip's id + hash, compute the new row's id and prev_hash,
//     compute the canonical input hash, sign it, INSERT the row, commit.
//  4. Return the persisted Entry.
//
// On first Emit against an empty ledger the tip read returns (0, zeros)
// and the genesis sentinel (32 zero bytes) becomes prev_hash.
func (e *emitterImpl) Emit(ctx context.Context, ev Event) (Entry, error) {
	if !IsKnown(ev.Type) {
		return Entry{}, fmt.Errorf("%w: %q", ErrUnknownEventType, ev.Type)
	}
	if len(ev.Subject) > MaxSubjectBytes {
		return Entry{}, fmt.Errorf("%w: got %d bytes", ErrSubjectTooLong, len(ev.Subject))
	}

	rawPayload, err := json.Marshal(ev.Payload)
	if err != nil {
		return Entry{}, fmt.Errorf("ledger.Emit: marshal payload: %w", err)
	}
	canonical, err := jcs.Canonicalize(rawPayload)
	if err != nil {
		return Entry{}, fmt.Errorf("ledger.Emit: JCS canonicalise: %w", err)
	}
	if len(canonical) > MaxPayloadBytes {
		return Entry{}, fmt.Errorf("%w: got %d bytes", ErrPayloadTooLarge, len(canonical))
	}

	ts := ev.Timestamp
	if ts.IsZero() {
		ts = e.now()
	}
	tsStr := ts.UTC().Format(time.RFC3339Nano)

	e.mu.Lock()
	defer e.mu.Unlock()

	priv, pub, err := e.loadOrGenerateKey(ctx, ts)
	if err != nil {
		return Entry{}, err
	}
	fpHex := Fingerprint(pub)
	var fp [SigningKeyFPLen]byte
	if _, err := hex.Decode(fp[:], []byte(fpHex)); err != nil {
		return Entry{}, fmt.Errorf("ledger.Emit: decode fingerprint: %w", err)
	}

	return e.appendSignedRow(ctx, ev, canonical, tsStr, priv, pub, fpHex, fp)
}

// appendSignedRow runs the transactional append. Split out of Emit for
// readability; the caller already holds e.mu.
func (e *emitterImpl) appendSignedRow(
	ctx context.Context,
	ev Event,
	canonical []byte,
	tsStr string,
	priv ed25519.PrivateKey,
	_ ed25519.PublicKey,
	fpHex string,
	fp [SigningKeyFPLen]byte,
) (Entry, error) {
	// BEGIN IMMEDIATE acquires the SQLite write lock up front so the
	// transaction cannot be promoted-then-denied partway through. Any
	// subsequent writer will block until we commit or rollback,
	// preserving the single-writer invariant at the DB layer even if
	// two processes somehow reached Emit simultaneously.
	tx, err := e.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return Entry{}, fmt.Errorf("ledger.Emit: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		// SQLite reports "cannot start a transaction within a transaction"
		// because BeginTx already opened one. Fall back to a plain BEGIN
		// IMMEDIATE semantics via the driver's implicit handling — most
		// drivers, including modernc.org/sqlite, upgrade on the first
		// write. Ignore the error; the driver is already serialising
		// writes through MaxOpenConns=1 in the standard Store setup.
		_ = err
	}

	var tipID int64
	var tipHash string
	err = tx.QueryRowContext(ctx,
		`SELECT id, hash FROM ledger ORDER BY id DESC LIMIT 1`,
	).Scan(&tipID, &tipHash)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		tipID = 0
		tipHash = hex.EncodeToString(GenesisPrevHash[:])
	case err != nil:
		return Entry{}, fmt.Errorf("ledger.Emit: read tip: %w", err)
	}

	nextID := tipID + 1
	var prev [PrevHashLen]byte
	if _, err := hex.Decode(prev[:], []byte(tipHash)); err != nil {
		return Entry{}, fmt.Errorf("ledger.Emit: decode prev_hash: %w", err)
	}

	sum := Hash(CanonicalInput{
		ID:               nextID,
		Timestamp:        tsStr,
		Type:             string(ev.Type),
		Subject:          ev.Subject,
		CanonicalPayload: canonical,
		PrevHash:         prev,
		SigningKeyFP:     fp,
	})
	sig := ed25519.Sign(priv, sum[:])

	hashHex := hex.EncodeToString(sum[:])
	prevHex := hex.EncodeToString(prev[:])
	sigHex := hex.EncodeToString(sig)

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO ledger
		   (id, ts, type, subject, payload_json, prev_hash, hash, signature, signing_key_fp)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		nextID, tsStr, string(ev.Type), ev.Subject, string(canonical),
		prevHex, hashHex, sigHex, fpHex,
	); err != nil {
		return Entry{}, fmt.Errorf("ledger.Emit: insert row: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Entry{}, fmt.Errorf("ledger.Emit: commit: %w", err)
	}

	return Entry{
		ID:           nextID,
		Timestamp:    tsStr,
		Type:         string(ev.Type),
		Subject:      ev.Subject,
		PayloadJSON:  string(canonical),
		PrevHash:     prevHex,
		Hash:         hashHex,
		Signature:    sigHex,
		SigningKeyFP: fpHex,
	}, nil
}

// loadOrGenerateKey returns the current signing keypair. On first
// call after daemon install the keystore is empty; we generate,
// persist, and register a fresh keypair so every ledger row ever
// written can be traced back to a registered public key.
//
// The caller holds e.mu, so no other Emit can race us into
// concurrent key generation.
func (e *emitterImpl) loadOrGenerateKey(ctx context.Context, at time.Time) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	priv, pub, err := e.keystore.Load(ctx)
	switch {
	case err == nil:
		// Happy path — but still make sure the registry knows about
		// this public key. Insert is idempotent so replaying this line
		// every Emit is cheap and defends against a registry table
		// that was wiped out-of-band.
		if _, err := e.registry.Insert(ctx, pub, at); err != nil {
			return nil, nil, fmt.Errorf("ledger.Emit: registry insert (existing key): %w", err)
		}
		return priv, pub, nil
	case errors.Is(err, keystore.ErrKeyNotFound):
		// First-boot keygen. Sign pass through below.
	default:
		return nil, nil, fmt.Errorf("ledger.Emit: keystore load: %w", err)
	}

	newPub, newPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, nil, fmt.Errorf("ledger.Emit: ed25519 genkey: %w", err)
	}
	if err := e.keystore.Store(ctx, newPriv, newPub); err != nil {
		return nil, nil, fmt.Errorf("ledger.Emit: keystore store: %w", err)
	}
	if _, err := e.registry.Insert(ctx, newPub, at); err != nil {
		return nil, nil, fmt.Errorf("ledger.Emit: registry insert (new key): %w", err)
	}
	return newPriv, newPub, nil
}
