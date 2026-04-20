package ledger

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// PurgeHelper implements the spec 029 kill-switch integration (FR-035a).
// Two code paths:
//
//   - PartialPurge: wipe daemon state but KEEP the audit ledger.
//     Emits a `purge.invoked` sentinel row BEFORE the wipe so the
//     ledger records the kill-switch invocation itself. Default path.
//
//   - FullPurge: wipe daemon state AND drop the ledger + ledger_keys
//     tables + their append-only triggers. No sentinel — the ledger
//     is itself being destroyed. Invoked only when the operator
//     explicitly passes `--purge-ledger`.
//
// PurgeHelper is the ONLY sanctioned site that DELETEs or DROPs the
// ledger tables. The `make check-ledger-append-only` CI gate
// excludes this file; any new site that touches the ledger tables
// outside this file is a violation.
type PurgeHelper struct {
	db      *sql.DB
	emitter Emitter
}

// NewPurgeHelper constructs a PurgeHelper. The emitter MUST be the
// same one the daemon routes every other privileged-action emission
// through so the `purge.invoked` sentinel is signed with the active
// key and chains correctly from the last normal row.
func NewPurgeHelper(db *sql.DB, em Emitter) *PurgeHelper {
	return &PurgeHelper{db: db, emitter: em}
}

// PartialPurgePayload is the typed body of the `purge.invoked`
// sentinel ledger row emitted by PartialPurge. Kept here (not in
// internal/ledger/payload/) because it is a sentinel type emitted
// only by the purge helper — moving it out would force the payload
// package to import purge semantics for a one-off shape.
type PartialPurgePayload struct {
	Full      bool   `json:"full"`
	Reason    string `json:"reason"`
	InvokedAt string `json:"invoked_at"`
}

// PartialPurge emits the `purge.invoked` sentinel then wipes every
// non-ledger table the operator wants cleared. The caller supplies
// the wipeState callback — PurgeHelper does NOT know what "daemon
// state" means (events, suggestions, patterns, etc.); it only
// guarantees the sentinel lands FIRST and the rest happens AFTER.
//
// If the sentinel emission fails, wipeState is NOT called — the
// operator retries and the ledger's own idempotency converges.
func (p *PurgeHelper) PartialPurge(ctx context.Context, reason string, wipeState func(ctx context.Context) error) error {
	payload := PartialPurgePayload{
		Full:      false,
		Reason:    reason,
		InvokedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if _, err := p.emitter.Emit(ctx, Event{
		Type:    EventPurgeInvoked,
		Subject: "purge.invoked:partial",
		Payload: payload,
	}); err != nil {
		return fmt.Errorf("ledger.PartialPurge: emit sentinel: %w", err)
	}
	if wipeState != nil {
		if err := wipeState(ctx); err != nil {
			return fmt.Errorf("ledger.PartialPurge: wipe state: %w", err)
		}
	}
	return nil
}

// FullPurge wipes non-ledger state first, then drops ledger +
// ledger_keys + triggers as the FINAL step. No sentinel is emitted
// — the ledger is being destroyed, there is nowhere for the
// sentinel to live. Callers log a WARN-level `ledger.purge.invoked
// full=true` BEFORE calling so the invocation is recorded somewhere
// (daemon logs) even though the ledger itself will not carry it.
//
// Order matters: non-ledger wipe first so an interrupted FullPurge
// still leaves an intact ledger the operator can inspect. Dropping
// the triggers before the tables is required because the
// ledger_no_delete trigger would otherwise block the DROP.
func (p *PurgeHelper) FullPurge(ctx context.Context, reason string, wipeState func(ctx context.Context) error) error {
	_ = reason // recorded in the daemon log by the caller; not stored in the ledger
	if wipeState != nil {
		if err := wipeState(ctx); err != nil {
			return fmt.Errorf("ledger.FullPurge: wipe non-ledger state: %w", err)
		}
	}
	// Drop the append-only triggers first so the DROP TABLE itself
	// can run. Each is an IF EXISTS so a partial prior purge does
	// not fail the retry.
	stmts := []string{
		`DROP TRIGGER IF EXISTS ledger_no_update`,
		`DROP TRIGGER IF EXISTS ledger_no_delete`,
		`DROP TRIGGER IF EXISTS ledger_keys_no_delete`,
		`DROP TRIGGER IF EXISTS ledger_keys_single_update_path`,
		`DROP TABLE IF EXISTS ledger`,
		`DROP TABLE IF EXISTS ledger_keys`,
	}
	for _, s := range stmts {
		if _, err := p.db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("ledger.FullPurge: %q: %w", s, err)
		}
	}
	return nil
}
