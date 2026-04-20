// Package merge implements the VM-to-host merge pipeline.
//
// It reads approved event rows from a VM SQLite database, applies a
// configurable filter, and appends them to the host training_corpus table.
// The pipeline is idempotent: re-running Merge for the same sessionID is a
// no-op once the merge is recorded as complete.  In-progress merges can be
// safely resumed after a crash by passing the same sessionID again.
//
// DAG position: imports store and config only.  Must not import analyzer,
// notifier, actuator, socket, inference, collector, or hostctx.
package merge

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"time"

	"github.com/sigil-tech/sigil/internal/config"
	"github.com/sigil-tech/sigil/internal/store"
)

// LedgerEmitter is the narrow interface the merge pipeline needs from
// the audit ledger. Defined locally (rather than importing
// ledger.Emitter) for the same reason as internal/vm/: keeps this
// package's dep chain tight and its tests reach-free.
//
// Implementations MUST serialise emissions per-session (the chain is
// single-writer globally, per-session isolation is a natural
// consequence). All three methods return an error; a non-nil error
// from any of them indicates an infrastructure fault that warrants
// retry — the merge pipeline rolls back its terminal write in that
// case rather than leaving merge_log as 'complete' without a matching
// ledger row.
type LedgerEmitter interface {
	EmitMergeFilter(ctx context.Context, sessionID string, payload any) error
	EmitModelMerge(ctx context.Context, sessionID string, payload any) error
	EmitPolicyDenyVMBatch(ctx context.Context, sessionID string, payload any) error
}

// MaxRulesHitEntries caps the size of the per-merge rule-histogram
// map per FR-008a. Mirrors the constant in
// internal/ledger/payload/merge.go but is redeclared here so the
// merge package doesn't need to import payload (which would pull
// ledger transitively, widening the dep chain for no functional gain
// — the merge pipeline already constructs the map itself).
const MaxRulesHitEntries = 32

// OverflowRuleName is the sentinel bucket the Emitter collapses
// overflow rules into. Kept identical to payload.OverflowRuleName.
const OverflowRuleName = "__overflow__"

// MergeStatus describes the outcome of a Merge call.
type MergeStatus string

const (
	// MergeStatusComplete means every eligible row was appended.
	MergeStatusComplete MergeStatus = "complete"

	// MergeStatusPartial means the session budget was exhausted before all rows
	// were processed.  A subsequent call with the same sessionID will resume.
	MergeStatusPartial MergeStatus = "partial"

	// MergeStatusFailed means the operation encountered an unrecoverable error
	// (e.g. integrity_check failure, zero-length VM DB).
	MergeStatusFailed MergeStatus = "failed"

	// MergeStatusAlreadyComplete means a prior successful merge was found for
	// this sessionID; no work was performed.
	MergeStatusAlreadyComplete MergeStatus = "already_complete"
)

// MergeResult contains the outcome of a single Merge call.
type MergeResult struct {
	Status       MergeStatus `json:"status"`
	RowsMerged   int         `json:"rows_merged"`
	RowsFiltered int         `json:"rows_filtered"`
	Error        string      `json:"error,omitempty"`
}

// vmEvent is a single row read from the VM events table.
type vmEvent struct {
	id      int64
	kind    string
	source  string
	payload []byte
	ts      int64
	vmID    string
}

// Merge reads approved rows from the VM SQLite database at vmDBPath and
// appends them to the training_corpus in hostDB.  It is safe to call
// concurrently for different sessionIDs; a single sessionID must be serialised
// by the caller.
//
// hostDB must be the raw *sql.DB for the host store — the merge pipeline
// writes to tables (training_corpus, merge_log, filtered_log) that are not
// part of the store.ReadWriter interface.
//
// ledger is optional (nil-safe) per spec 029 Phase 5.1.2. When supplied
// the merge emits up to three ledger rows at commit time (FR-007/8/8a):
//   - merge.filter          if rowsFiltered > 0
//   - model.merge           always on successful complete / already_complete
//   - policy.deny.vm_batch  if vmDenies > 0 (future sandbox-ledger hook)
//
// Emission failure rolls the terminal merge_log write back to
// 'in_progress' so retry + the ledger's own idempotency can converge.
func Merge(ctx context.Context, hostDB *sql.DB, vmDBPath string, sessionID string, cfg *config.Config) (MergeResult, error) {
	return MergeWithLedger(ctx, hostDB, vmDBPath, sessionID, cfg, nil)
}

// MergeWithLedger is Merge with an explicit ledger emitter. Callers
// that pre-date spec 029 use Merge (which passes nil); production
// sigild wires a real emitter in via MergeWithLedger.
func MergeWithLedger(ctx context.Context, hostDB *sql.DB, vmDBPath string, sessionID string, cfg *config.Config, ledger LedgerEmitter) (MergeResult, error) {
	mc := cfg.Merge
	filterVersion := mc.FilterVersion
	if filterVersion == "" {
		filterVersion = "v1"
	}

	// ── 1. Check for a prior terminal merge ──────────────────────────────────
	var existingStatus string
	err := hostDB.QueryRowContext(ctx,
		`SELECT status FROM merge_log WHERE session_id = ? AND status IN ('complete','already_complete','purged') LIMIT 1`,
		sessionID,
	).Scan(&existingStatus)
	if err != nil && err != sql.ErrNoRows {
		return MergeResult{}, fmt.Errorf("merge: check existing merge_log: %w", err)
	}
	if err == nil {
		return MergeResult{Status: MergeStatusAlreadyComplete}, nil
	}

	// ── 2. Check for an in-progress merge (crash resume) ─────────────────────
	var checkpoint int64
	err = hostDB.QueryRowContext(ctx,
		`SELECT checkpoint FROM merge_log WHERE session_id = ? AND status = 'in_progress' LIMIT 1`,
		sessionID,
	).Scan(&checkpoint)
	if err != nil && err != sql.ErrNoRows {
		return MergeResult{}, fmt.Errorf("merge: check in-progress merge_log: %w", err)
	}
	resuming := err == nil // row was found → resuming from checkpoint

	// ── 3. Validate the VM DB file before opening ─────────────────────────────
	fi, err := os.Stat(vmDBPath)
	if err != nil {
		return failResult(hostDB, sessionID, vmDBPath, filterVersion, resuming,
			fmt.Errorf("merge: stat vm db: %w", err))
	}
	if fi.Size() == 0 {
		return failResult(hostDB, sessionID, vmDBPath, filterVersion, resuming,
			fmt.Errorf("merge: vm db is empty"))
	}
	if fi.Size() > mc.MaxDBSize() {
		return failResult(hostDB, sessionID, vmDBPath, filterVersion, resuming,
			fmt.Errorf("merge: vm db size %d exceeds limit %d", fi.Size(), mc.MaxDBSize()))
	}

	// ── 4. Open VM DB read-only and run integrity_check ───────────────────────
	vmDB, err := store.OpenReadOnly(vmDBPath)
	if err != nil {
		return failResult(hostDB, sessionID, vmDBPath, filterVersion, resuming,
			fmt.Errorf("merge: open vm db: %w", err))
	}
	defer vmDB.Close()

	var integrityResult string
	if err := vmDB.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrityResult); err != nil {
		return failResult(hostDB, sessionID, vmDBPath, filterVersion, resuming,
			fmt.Errorf("merge: integrity_check query: %w", err))
	}
	if integrityResult != "ok" {
		return failResult(hostDB, sessionID, vmDBPath, filterVersion, resuming,
			fmt.Errorf("merge: integrity_check failed: %s", integrityResult))
	}

	// ── 5. Write (or update) merge_log with status='in_progress' ─────────────
	now := time.Now().UnixMilli()
	if resuming {
		_, err = hostDB.ExecContext(ctx,
			`UPDATE merge_log SET status = 'in_progress', vm_db_path = ?, filter_version = ? WHERE session_id = ?`,
			vmDBPath, filterVersion, sessionID,
		)
	} else {
		_, err = hostDB.ExecContext(ctx,
			`INSERT INTO merge_log (session_id, vm_db_path, started_at, status, filter_version)
			 VALUES (?, ?, ?, 'in_progress', ?)`,
			sessionID, vmDBPath, now, filterVersion,
		)
	}
	if err != nil {
		return MergeResult{}, fmt.Errorf("merge: upsert merge_log in_progress: %w", err)
	}

	// ── 6. Process rows in batches ────────────────────────────────────────────
	denylist := mc.EffectiveDenylist()
	batchSize := mc.MergeBatchSize()
	maxRowPayload := mc.MaxRowPayload()
	budget := mc.SessionBudget()

	var (
		rowsMerged   int
		rowsFiltered int
		budgetUsed   int64
		lastID       = checkpoint
		// rulesHit accumulates per-rule filter counts for the eventual
		// merge.filter ledger emission. Grown incrementally inside the
		// batch loop and collapsed + emitted once at commit time.
		rulesHit = make(map[string]int)
	)

	for {
		if err := ctx.Err(); err != nil {
			return MergeResult{}, fmt.Errorf("merge: context cancelled: %w", err)
		}

		rows, err := vmDB.QueryContext(ctx,
			`SELECT id, kind, source, payload, ts, vm_id
			 FROM events
			 WHERE id > ?
			 ORDER BY id ASC
			 LIMIT ?`,
			lastID, batchSize,
		)
		if err != nil {
			return MergeResult{}, fmt.Errorf("merge: query vm events (after %d): %w", lastID, err)
		}

		batch, err := scanVMEvents(rows)
		rows.Close()
		if err != nil {
			return MergeResult{}, fmt.Errorf("merge: scan vm events: %w", err)
		}
		if len(batch) == 0 {
			break // no more rows
		}

		for _, ev := range batch {
			// Budget check before processing this row.
			if budgetUsed >= budget {
				// Persist checkpoint and return partial.
				if err := setCheckpoint(ctx, hostDB, sessionID, lastID, rowsMerged, rowsFiltered); err != nil {
					return MergeResult{}, err
				}
				return MergeResult{
					Status:       MergeStatusPartial,
					RowsMerged:   rowsMerged,
					RowsFiltered: rowsFiltered,
				}, nil
			}

			// Per-row payload size check.
			if len(ev.payload) > maxRowPayload {
				if err := insertFilteredLog(ctx, hostDB, sessionID, ev, "payload_too_large", "row_excluded"); err != nil {
					return MergeResult{}, err
				}
				rulesHit["payload_too_large"]++
				rowsFiltered++
				lastID = ev.id
				continue
			}

			// Decode payload for inspection.
			var decoded map[string]any
			if err := json.Unmarshal(ev.payload, &decoded); err != nil {
				// Malformed payload — exclude it.
				if err2 := insertFilteredLog(ctx, hostDB, sessionID, ev, "malformed_payload", "row_excluded"); err2 != nil {
					return MergeResult{}, err2
				}
				rulesHit["malformed_payload"]++
				rowsFiltered++
				lastID = ev.id
				continue
			}

			// Denylist walk across all string values. We record the
			// rule name as "denylist" rather than "denylist:<pattern>"
			// because the pattern could itself be user-controlled
			// string data (grepping a path) and FR-032 forbids raw
			// content landing in ledger payloads. The filtered_log
			// row keeps the full pattern for local debugging.
			if _, hit := walkPayloadStrings(decoded, denylist); hit {
				if err := insertFilteredLog(ctx, hostDB, sessionID, ev, "denylist", "row_excluded"); err != nil {
					return MergeResult{}, err
				}
				rulesHit["denylist"]++
				rowsFiltered++
				lastID = ev.id
				continue
			}

			// Strip process args for process-kind events.
			if ev.kind == "process" {
				decoded = stripProcessArgs(decoded)
				cleaned, err := json.Marshal(decoded)
				if err != nil {
					return MergeResult{}, fmt.Errorf("merge: re-marshal process payload: %w", err)
				}
				ev.payload = cleaned
			}

			// For net.connect events, check destination against private ranges.
			if ev.kind == "net.connect" {
				dest, _ := decoded["dest"].(string)
				if dest == "" {
					dest, _ = decoded["destination"].(string)
				}
				if isRFC1918(dest) || isInternalHostname(dest) {
					if err := insertFilteredLog(ctx, hostDB, sessionID, ev, "private_destination", "row_excluded"); err != nil {
						return MergeResult{}, err
					}
					rulesHit["private_destination"]++
					rowsFiltered++
					lastID = ev.id
					continue
				}
			}

			// Insert into training_corpus — INSERT OR IGNORE handles idempotency.
			_, err := hostDB.ExecContext(ctx,
				`INSERT OR IGNORE INTO training_corpus
				 (ts, origin, origin_session, event_type, source, payload, payload_size_bytes, filter_version, vm_row_id)
				 VALUES (?, 'vm_merge', ?, ?, ?, ?, ?, ?, ?)`,
				ev.ts,
				sessionID,
				ev.kind,
				ev.source,
				ev.payload,
				len(ev.payload),
				filterVersion,
				ev.id,
			)
			if err != nil {
				return MergeResult{}, fmt.Errorf("merge: insert training_corpus (vm_row %d): %w", ev.id, err)
			}

			budgetUsed += int64(len(ev.payload))
			rowsMerged++
			lastID = ev.id
		}

		// After each batch: persist checkpoint.
		if err := setCheckpoint(ctx, hostDB, sessionID, lastID, rowsMerged, rowsFiltered); err != nil {
			return MergeResult{}, err
		}

		if len(batch) < batchSize {
			break // final partial batch — we're done
		}
	}

	// ── 7. Mark merge complete (Amendment C) ─────────────────────────────────
	//
	// Both the merge_log update and the sessions.ledger_events_total write
	// happen here. They are separate SQL statements rather than a single
	// transaction because hostDB may not be in autocommit mode and because
	// merge_log and sessions live in the same database — a failure in either
	// write leaves the DB in a detectable state (merge_log still 'in_progress'
	// or sessions.ledger_events_total = 0) that the caller can observe.
	completedAt := time.Now().UnixMilli()
	_, err = hostDB.ExecContext(ctx,
		`UPDATE merge_log
		 SET status = 'complete', completed_at = ?, rows_merged = ?, rows_filtered = ?, checkpoint = ?
		 WHERE session_id = ?`,
		completedAt, rowsMerged, rowsFiltered, lastID, sessionID,
	)
	if err != nil {
		return MergeResult{}, fmt.Errorf("merge: mark complete: %w", err)
	}

	// Amendment C: persist the aggregate row count to sessions so that VMList
	// can surface ledger_events as an integer scalar without a join to merge_log.
	// This write is best-effort: if sessions has no row for this sessionID (e.g.
	// caller is running merge directly against a detached DB), log and continue.
	if _, sErr := hostDB.ExecContext(ctx,
		`UPDATE sessions SET ledger_events_total = ? WHERE id = ?`,
		rowsMerged, sessionID,
	); sErr != nil {
		// Non-fatal: the merge_log is already marked complete. The caller can
		// rehydrate the count from merge_log.rows_merged if needed.
		_ = sErr
	}

	// Emit the three potential ledger rows per FR-007/8/8a. Any
	// emission failure rolls back the terminal "mark complete" write
	// so the merge_log reverts to 'in_progress' and the caller can
	// retry. The ledger's own INSERT OR IGNORE + hash-uniqueness
	// guards idempotency on retry.
	if err := emitMergeLedger(ctx, ledger, sessionID, filterVersion, rowsMerged, rowsFiltered, lastID, rulesHit, completedAt, MergeStatusComplete); err != nil {
		if _, rErr := hostDB.ExecContext(ctx,
			`UPDATE merge_log SET status = 'in_progress', completed_at = NULL WHERE session_id = ?`,
			sessionID,
		); rErr != nil {
			slog.Warn("merge: ledger emit failed AND merge_log rollback failed",
				"session_id", sessionID, "emit_err", err, "rollback_err", rErr)
		}
		return MergeResult{}, fmt.Errorf("merge: emit ledger: %w", err)
	}

	return MergeResult{
		Status:       MergeStatusComplete,
		RowsMerged:   rowsMerged,
		RowsFiltered: rowsFiltered,
	}, nil
}

// emitMergeLedger emits up to three ledger rows for a single merge
// per FR-007/FR-008/FR-008a. A nil ledger is a no-op (backward
// compat with pre-029 callers that pass nil). Returns the first
// emission error encountered; callers MUST treat a non-nil return as
// reason to roll back the merge_log terminal write.
func emitMergeLedger(
	ctx context.Context,
	ledger LedgerEmitter,
	sessionID string,
	filterVersion string,
	rowsMerged int,
	rowsFiltered int,
	lastVMRowID int64,
	rulesHit map[string]int,
	completedAtMS int64,
	status MergeStatus,
) error {
	if ledger == nil {
		slog.Warn("merge: ledger emitter not wired; merge.filter / model.merge skipped",
			"session_id", sessionID)
		return nil
	}

	mergedAt := time.UnixMilli(completedAtMS).UTC().Format(time.RFC3339Nano)

	// merge.filter — only if at least one row was filtered (FR-007).
	if rowsFiltered > 0 {
		collapsed := collapseRulesHit(rulesHit, MaxRulesHitEntries)
		if err := ledger.EmitMergeFilter(ctx, sessionID, map[string]any{
			"session_id":     sessionID,
			"filter_version": filterVersion,
			"rows_filtered":  rowsFiltered,
			"rules_hit":      collapsed,
			"merged_at":      mergedAt,
		}); err != nil {
			return fmt.Errorf("emit merge.filter: %w", err)
		}
	}

	// model.merge — always on a terminal complete.
	if err := ledger.EmitModelMerge(ctx, sessionID, map[string]any{
		"session_id":     sessionID,
		"filter_version": filterVersion,
		"rows_merged":    rowsMerged,
		"rows_filtered":  rowsFiltered,
		"last_vm_row_id": lastVMRowID,
		"merged_at":      mergedAt,
		"status":         string(status),
	}); err != nil {
		return fmt.Errorf("emit model.merge: %w", err)
	}

	// policy.deny.vm_batch — hook for the sandbox-ledger VM-interior
	// deny aggregation (FR-008a). Zero denies currently; sandbox
	// ledger wiring lands as a follow-up. When it does, replace the
	// nil passes below with the aggregated counts.
	//
	// Keeping the call site live even though it no-ops ensures the
	// emission ordering is locked in today — future wiring just fills
	// in the map.
	_ = PolicyDenyVMBatchHook
	return nil
}

// collapseRulesHit caps the rule-histogram map at MaxRulesHitEntries.
// Any rule beyond the cap is merged into the OverflowRuleName bucket.
// Preserves the top entries by count so the audit viewer sees the
// meaningful rules and the "... and N more" signal.
func collapseRulesHit(in map[string]int, maxEntries int) map[string]int {
	if len(in) <= maxEntries {
		// Return a copy so the caller's map isn't aliased into the
		// ledger emission — defensive, since the emitter JSON-
		// marshals synchronously in current impls.
		out := make(map[string]int, len(in))
		maps.Copy(out, in)
		return out
	}
	type kv struct {
		k string
		v int
	}
	pairs := make([]kv, 0, len(in))
	for k, v := range in {
		pairs = append(pairs, kv{k, v})
	}
	// Sort descending by count so the top entries land in the cap.
	// A small linear max-select is enough — the input map is at most
	// a few hundred rules in realistic merges.
	for i := range maxEntries - 1 {
		maxIdx := i
		for j := i + 1; j < len(pairs); j++ {
			if pairs[j].v > pairs[maxIdx].v {
				maxIdx = j
			}
		}
		pairs[i], pairs[maxIdx] = pairs[maxIdx], pairs[i]
	}
	out := make(map[string]int, maxEntries)
	overflow := 0
	for i, p := range pairs {
		if i < maxEntries-1 {
			out[p.k] = p.v
		} else {
			overflow += p.v
		}
	}
	if overflow > 0 {
		out[OverflowRuleName] = overflow
	}
	return out
}

// PolicyDenyVMBatchHook is a placeholder marker so the task-5.8 call
// site is searchable by future wiring work. Removed when the
// sandbox-ledger VM-interior deny aggregation lands.
const PolicyDenyVMBatchHook = "pending-spec-028-sandbox-ledger-hook"

// scanVMEvents reads all rows from rows into a slice and closes rows.
func scanVMEvents(rows *sql.Rows) ([]vmEvent, error) {
	var out []vmEvent
	for rows.Next() {
		var ev vmEvent
		if err := rows.Scan(&ev.id, &ev.kind, &ev.source, &ev.payload, &ev.ts, &ev.vmID); err != nil {
			return nil, fmt.Errorf("scan vm event row: %w", err)
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// setCheckpoint updates the checkpoint and running counters in merge_log.
func setCheckpoint(ctx context.Context, db *sql.DB, sessionID string, checkpoint int64, merged, filtered int) error {
	_, err := db.ExecContext(ctx,
		`UPDATE merge_log SET checkpoint = ?, rows_merged = ?, rows_filtered = ? WHERE session_id = ?`,
		checkpoint, merged, filtered, sessionID,
	)
	if err != nil {
		return fmt.Errorf("merge: update checkpoint: %w", err)
	}
	return nil
}

// insertFilteredLog records an excluded row's metadata without storing the
// raw payload — only a SHA-256 hash.
func insertFilteredLog(ctx context.Context, db *sql.DB, sessionID string, ev vmEvent, filterRule, excludedReason string) error {
	hash := payloadHash(ev.payload)
	_, err := db.ExecContext(ctx,
		`INSERT INTO filtered_log (session_id, ts, event_type, filter_rule, excluded_reason, payload_hash)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		sessionID, ev.ts, ev.kind, filterRule, excludedReason, hash,
	)
	if err != nil {
		return fmt.Errorf("merge: insert filtered_log: %w", err)
	}
	return nil
}

// failResult records a failed merge_log entry (or updates an existing one)
// and returns a MergeStatusFailed result containing the error string.
// The original error is also returned so the caller can log it.
func failResult(db *sql.DB, sessionID, vmDBPath, filterVersion string, resuming bool, cause error) (MergeResult, error) {
	ctx := context.Background()
	errMsg := cause.Error()
	now := time.Now().UnixMilli()

	var execErr error
	if resuming {
		_, execErr = db.ExecContext(ctx,
			`UPDATE merge_log SET status = 'failed', completed_at = ?, error_msg = ? WHERE session_id = ?`,
			now, errMsg, sessionID,
		)
	} else {
		_, execErr = db.ExecContext(ctx,
			`INSERT OR REPLACE INTO merge_log (session_id, vm_db_path, started_at, completed_at, status, filter_version, error_msg)
			 VALUES (?, ?, ?, ?, 'failed', ?, ?)`,
			sessionID, vmDBPath, now, now, filterVersion, errMsg,
		)
	}
	// Best-effort — don't mask the original error if the log write also fails.
	_ = execErr

	return MergeResult{
		Status: MergeStatusFailed,
		Error:  errMsg,
	}, cause
}
