package ledger

import (
	"context"
	"database/sql"
	"fmt"
)

// SQL statements that define the ledger storage layer. They are executed
// in order by Migrate and are safe to re-run — every table and trigger is
// created with IF NOT EXISTS. Changing these statements after a release
// is a schema break and requires a forward migration; the ledger is
// append-only and has no supported rollback.

// schemaLedger creates the `ledger` table. STRICT mode binds declared
// column types at the database layer (SQLite 3.37+) so a payload-shape
// drift in a future emitter cannot quietly store the wrong type. Per
// spec 029 plan §4.1 the primary columns are:
//
//   - id              monotonic append-only row id (INTEGER PRIMARY KEY
//     AUTOINCREMENT keeps old ids out of reuse after a DELETE — not that
//     we ever DELETE, but defense in depth)
//   - ts              RFC 3339 nanosecond UTC timestamp
//   - type            EventType enum (9 string values)
//   - subject         ≤256 UTF-8 bytes (length enforced in code before
//     INSERT; STRICT mode cannot enforce length)
//   - payload_json    RFC 8785 canonical JSON; FR-032 allowlist keeps
//     this typed at the struct layer, not here
//   - prev_hash       64 hex chars; sentinel (genesis) is 64 zeros
//   - hash            64 hex chars, UNIQUE so two rows cannot share a
//     hash even under a wild chain-split bug
//   - signature       128 hex chars (64-byte ed25519 signature)
//   - signing_key_fp  32 hex chars, FK-like to ledger_keys.fingerprint
//     (not a real FK — ledger_keys is a sibling table; enforcement is
//     via the signing code and the verifier)
const schemaLedger = `
CREATE TABLE IF NOT EXISTS ledger (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    ts               TEXT    NOT NULL,
    type             TEXT    NOT NULL,
    subject          TEXT    NOT NULL,
    payload_json     TEXT    NOT NULL,
    prev_hash        TEXT    NOT NULL,
    hash             TEXT    NOT NULL UNIQUE,
    signature        TEXT    NOT NULL,
    signing_key_fp   TEXT    NOT NULL
) STRICT;
`

const schemaLedgerIndexTs = `
CREATE INDEX IF NOT EXISTS idx_ledger_ts ON ledger(ts DESC);
`

const schemaLedgerIndexType = `
CREATE INDEX IF NOT EXISTS idx_ledger_type ON ledger(type);
`

// triggerLedgerNoUpdate blocks every UPDATE against the ledger table at
// the storage layer. The package API exposes only Emit (INSERT), and a
// CI grep rule (`make check-ledger-append-only`) keeps the source clean
// — this trigger is the third layer of defense in case both of those
// regress. Only purge.go's full-wipe transaction drops the trigger.
const triggerLedgerNoUpdate = `
CREATE TRIGGER IF NOT EXISTS ledger_no_update
BEFORE UPDATE ON ledger
BEGIN
    SELECT RAISE(ABORT, 'ledger is append-only: UPDATE rejected');
END;
`

const triggerLedgerNoDelete = `
CREATE TRIGGER IF NOT EXISTS ledger_no_delete
BEFORE DELETE ON ledger
BEGIN
    SELECT RAISE(ABORT, 'ledger is append-only: DELETE rejected');
END;
`

// schemaLedgerKeys tracks every ed25519 public key ever used to sign a
// ledger entry. Rotations never forget old keys — the verifier needs
// the retired pubkey to validate pre-rotation rows. Per plan §4.2:
//
//   - fingerprint   32 hex chars, PK; first 16 bytes of SHA-256(pubkey)
//   - public_key    64 hex chars, raw 32-byte ed25519 public key
//   - generated_at  RFC 3339 UTC, creation time
//   - retired_at    NULL while active; set exactly once on rotation
const schemaLedgerKeys = `
CREATE TABLE IF NOT EXISTS ledger_keys (
    fingerprint   TEXT    PRIMARY KEY,
    public_key    TEXT    NOT NULL UNIQUE,
    generated_at  TEXT    NOT NULL,
    retired_at    TEXT
) STRICT;
`

// triggerLedgerKeysNoDelete enforces that no key is ever removed. Purge
// removes the table wholesale; normal operation never DELETEs a row.
const triggerLedgerKeysNoDelete = `
CREATE TRIGGER IF NOT EXISTS ledger_keys_no_delete
BEFORE DELETE ON ledger_keys
BEGIN
    SELECT RAISE(ABORT, 'ledger_keys is append-only: DELETE rejected');
END;
`

// triggerLedgerKeysSingleUpdatePath restricts UPDATE to the exact
// rotation case: retired_at transitions from NULL → a non-NULL value,
// and no other column may change. Two abort paths:
//
//  1. retired_at is already set — the key has been retired already and
//     cannot be "un-retired" or re-stamped.
//  2. any other column changes — fingerprint / public_key /
//     generated_at are immutable.
const triggerLedgerKeysSingleUpdatePath = `
CREATE TRIGGER IF NOT EXISTS ledger_keys_single_update_path
BEFORE UPDATE ON ledger_keys
BEGIN
    SELECT CASE
        WHEN OLD.retired_at IS NOT NULL
            THEN RAISE(ABORT, 'ledger_keys: retired_at already set, key immutable')
        WHEN NEW.fingerprint  IS NOT OLD.fingerprint
          OR NEW.public_key   IS NOT OLD.public_key
          OR NEW.generated_at IS NOT OLD.generated_at
            THEN RAISE(ABORT, 'ledger_keys: only retired_at may be updated')
        WHEN NEW.retired_at IS NULL
            THEN RAISE(ABORT, 'ledger_keys: retired_at must be set on UPDATE')
    END;
END;
`

// ledgerMigrations is the ordered list of SQL statements that bring a
// fresh SQLite database up to the latest ledger schema. Ordering
// matters: tables first, then indexes, then triggers. Re-running the
// list is a no-op because every statement uses IF NOT EXISTS.
var ledgerMigrations = []struct {
	name string
	stmt string
}{
	{"create ledger table", schemaLedger},
	{"create ledger ts index", schemaLedgerIndexTs},
	{"create ledger type index", schemaLedgerIndexType},
	{"create ledger no-update trigger", triggerLedgerNoUpdate},
	{"create ledger no-delete trigger", triggerLedgerNoDelete},
	{"create ledger_keys table", schemaLedgerKeys},
	{"create ledger_keys no-delete trigger", triggerLedgerKeysNoDelete},
	{"create ledger_keys single-update-path trigger", triggerLedgerKeysSingleUpdatePath},
}

// Migrate applies every ledger migration statement to the supplied
// database handle. It is idempotent — all statements are CREATE … IF
// NOT EXISTS — so callers may invoke it on every daemon start without
// harm. The full sequence runs inside a single transaction so a partial
// failure cannot leave the ledger with tables but no triggers (or vice
// versa), which would be a security-relevant gap.
func Migrate(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ledger.Migrate: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, m := range ledgerMigrations {
		if _, err := tx.ExecContext(ctx, m.stmt); err != nil {
			return fmt.Errorf("ledger.Migrate: %s: %w", m.name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ledger.Migrate: commit: %w", err)
	}
	return nil
}
