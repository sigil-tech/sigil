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
	"os"
	"time"

	"github.com/sigil-tech/sigil/internal/config"
	"github.com/sigil-tech/sigil/internal/store"
)

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
func Merge(ctx context.Context, hostDB *sql.DB, vmDBPath string, sessionID string, cfg *config.Config) (MergeResult, error) {
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
				rowsFiltered++
				lastID = ev.id
				continue
			}

			// Denylist walk across all string values.
			if pattern, hit := walkPayloadStrings(decoded, denylist); hit {
				if err := insertFilteredLog(ctx, hostDB, sessionID, ev, "denylist:"+pattern, "row_excluded"); err != nil {
					return MergeResult{}, err
				}
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

	return MergeResult{
		Status:       MergeStatusComplete,
		RowsMerged:   rowsMerged,
		RowsFiltered: rowsFiltered,
	}, nil
}

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
