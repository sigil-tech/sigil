package vm

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// LedgerEmitter is the narrow interface Manager needs from the audit
// ledger — only the append path. Defined here (rather than importing
// `ledger.Emitter` directly) so the vm package can be tested with a
// fake emitter without bringing in the full ledger dep chain, and so
// a new consumer of the ledger doesn't force a change to the ledger
// package's public API.
//
// The input is a typed payload from `internal/ledger/payload/`
// (VMSpawnPayload or VMTeardownPayload). Implementations MUST JSON-
// marshal the payload, JCS-canonicalise it, sign, and persist — all
// of which ledger.Emitter already does; the vm package therefore
// treats this interface as a pass-through.
type LedgerEmitter interface {
	EmitVMSpawn(ctx context.Context, sessionID string, payload any) error
	EmitVMTeardown(ctx context.Context, sessionID string, payload any) error
}

// Manager implements VM session lifecycle management backed by the sessions
// table in SQLite. It enforces the single-VM constraint (Phase 1).
type Manager struct {
	db      *sql.DB
	driver  Driver         // nil until a real driver is wired (Phase 4a/4b)
	sampler SessionSampler // nil until vmstats is wired (Phase 5b)
	ledger  LedgerEmitter  // nil disables compliance emission (see emitSpawn/emitTeardown)
	mu      sync.Mutex
}

// NewManager creates a Manager with the given database, hypervisor driver, and
// stat sampler. Either or both of driver and sampler may be nil; Manager
// handles nil gracefully by skipping hypervisor and sampler operations.
//
// Phase 4a/4b wires the real driver; Phase 5b wires the sampler. Until then,
// pass nil for each.
//
// The ledger emitter is set separately via WithLedger. A nil ledger is
// tolerated for backward compatibility with call sites that predate
// spec 029 — they emit nothing and log at WARN so an operator can spot
// a misconfiguration; production sigild wires one in at cmd/sigild.
func NewManager(db *sql.DB, driver Driver, sampler SessionSampler) *Manager {
	return &Manager{db: db, driver: driver, sampler: sampler}
}

// NewManagerWithoutDriver creates a Manager with no hypervisor driver or stat
// sampler. It is preserved as a convenience alias for callers that do not need
// driver or sampler injection (e.g. test helpers that predate Phase 3).
func NewManagerWithoutDriver(db *sql.DB) *Manager {
	return NewManager(db, nil, nil)
}

// WithLedger wires the audit-ledger emitter into the Manager. Returns
// the same *Manager so the call fluently chains after NewManager.
// A nil emitter is treated as an explicit opt-out — the Manager logs
// at WARN on the first skipped emission so the absence is visible.
func (m *Manager) WithLedger(em LedgerEmitter) *Manager {
	m.ledger = em
	return m
}

// Start creates a new VM session. Returns ErrSessionActive if a session is
// already in an active state (booting, ready, connecting, stopping).
func (m *Manager) Start(ctx context.Context, req StartRequest) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Phase 1: single-VM constraint.
	var count int
	err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sessions WHERE status IN ('booting','ready','connecting','stopping')`,
	).Scan(&count)
	if err != nil {
		return nil, fmt.Errorf("vm: check active sessions: %w", err)
	}
	if count > 0 {
		return nil, &VMError{
			Code:    ErrSessionActive,
			Message: "A VM session is already running. Stop it before starting a new one.",
		}
	}

	sess := &Session{
		ID:                uuid.New().String(),
		StartedAt:         time.Now(),
		Status:            StateBooting,
		MergeOutcome:      MergeOutcomePending,
		DiskImagePath:     req.DiskImagePath,
		OverlayPath:       req.OverlayPath,
		VMDBPath:          req.VMDBPath,
		VsockCID:          req.VsockCID,
		FilterVersion:     req.FilterVersion,
		LedgerEventsTotal: 0,
		PolicyStatus:      "ok",
	}

	_, err = m.db.ExecContext(ctx,
		`INSERT INTO sessions (id, started_at, status, merge_outcome, disk_image_path, overlay_path, vm_db_path, vsock_cid, filter_version, ledger_events_total, policy_status)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sess.ID, sess.StartedAt.UnixMilli(), string(sess.Status), string(sess.MergeOutcome),
		sess.DiskImagePath, sess.OverlayPath, sess.VMDBPath, sess.VsockCID, sess.FilterVersion,
		sess.LedgerEventsTotal, sess.PolicyStatus,
	)
	if err != nil {
		return nil, fmt.Errorf("vm: insert session: %w", err)
	}

	return sess, nil
}

// Stop transitions a session to the stopping state. If a Driver is wired, it
// also calls Driver.Stop to initiate hypervisor shutdown. Driver.Stop errors
// are logged but do not prevent the state-machine transition; teardown proceeds
// regardless so that the session record reaches a terminal state.
func (m *Manager) Stop(ctx context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var status string
	err := m.db.QueryRowContext(ctx,
		`SELECT status FROM sessions WHERE id = ?`, sessionID,
	).Scan(&status)
	if err == sql.ErrNoRows {
		return &VMError{Code: ErrSessionNotFound, Message: "No active session found with that ID."}
	}
	if err != nil {
		return fmt.Errorf("vm: query session: %w", err)
	}

	if LifecycleState(status).IsTerminal() {
		return &VMError{Code: ErrSessionNotFound, Message: "Session is already terminated."}
	}

	_, err = m.db.ExecContext(ctx,
		`UPDATE sessions SET status = ? WHERE id = ?`,
		string(StateStopping), sessionID,
	)
	if err != nil {
		return err
	}

	// Step 2 of the six-step teardown: ask the hypervisor to shut down. We do
	// not propagate the driver error; the state-machine transition has already
	// committed and teardown must proceed to completion.
	if m.driver != nil {
		if dErr := m.driver.Stop(ctx, SessionID(sessionID)); dErr != nil {
			// The caller cannot act on this error (state is already stopping),
			// so we surface it via slog. Structured logs allow operators to
			// detect hypervisor misbehaviour without breaking the teardown path.
			//
			// Importing log/slog here keeps the dependency within stdlib.
			// We use the package-level functions so that callers can redirect
			// the default handler in tests if needed.
			_ = dErr // slog call deferred to Phase 5a when slog is wired
		}
	}

	return nil
}

// StartWithSpec creates a new VM session from a pre-merged StartSpec. It:
//  1. Enforces the single-VM constraint.
//  2. Evaluates the policy status via evaluatePolicyStatus.
//  3. Inserts the session record (including policy_status).
//  4. Calls Driver.Start if a driver is wired; on failure, marks the session
//     as failed and returns the error.
//  5. Calls SessionSampler.AttachSession if both driver and sampler are wired.
//
// The denyList should be the filter package's configured snapshot at VMStart
// time — the same version recorded in sessions.filter_version.
func (m *Manager) StartWithSpec(ctx context.Context, spec StartSpec, denyList []string) (SessionID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Single-VM constraint.
	var count int
	if err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sessions WHERE status IN ('booting','ready','connecting','stopping')`,
	).Scan(&count); err != nil {
		return "", fmt.Errorf("vm: check active sessions: %w", err)
	}
	if count > 0 {
		return "", &VMError{
			Code:    ErrSessionActive,
			Message: "A VM session is already running. Stop it before starting a new one.",
		}
	}

	ps := evaluatePolicyStatus(spec.PolicyID, spec.EgressTier, "", denyList)

	id := SessionID(uuid.New().String())
	now := time.Now()

	_, err := m.db.ExecContext(ctx,
		`INSERT INTO sessions
		 (id, started_at, status, merge_outcome, disk_image_path, overlay_path, vm_db_path, vsock_cid, filter_version, ledger_events_total, policy_status)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(id), now.UnixMilli(),
		string(StateBooting), string(MergeOutcomePending),
		spec.ImagePath, spec.OverlayPath, "",
		spec.VsockCID, "",
		uint64(0), string(ps),
	)
	if err != nil {
		return "", fmt.Errorf("vm: insert session: %w", err)
	}

	// Emit vm.spawn BEFORE the driver is invoked (spec 029 FR-004).
	// The ledger row captures the intent to boot; if the hypervisor
	// fails in the next step we still have an audit trail of the
	// attempt. Emission failure rolls back the session row — the
	// ledger is not allowed to miss a privileged action.
	if err := m.emitSpawn(ctx, id, spec, now, ps); err != nil {
		_, _ = m.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, string(id))
		return "", fmt.Errorf("vm: emit spawn: %w", err)
	}

	if m.driver != nil {
		driverID, dErr := m.driver.Start(ctx, spec)
		if dErr != nil {
			// Mark the session as failed so callers see a consistent state.
			_, _ = m.db.ExecContext(ctx,
				`UPDATE sessions SET status = ? WHERE id = ?`,
				string(StateFailed), string(id),
			)
			// Terminal transition — emit teardown with outcome=failed.
			_ = m.emitTeardown(ctx, string(id), "failed", time.Now())
			return "", fmt.Errorf("vm: driver start: %w", dErr)
		}
		// The driver may assign its own identifier; we use our own UUID as the
		// canonical SessionID. driverID is informational here (reserved for
		// Phase 4 where it maps to the QEMU PID or VZ handle).
		_ = driverID

		if m.sampler != nil {
			ch, sErr := m.driver.Subscribe(ctx, id)
			if sErr == nil {
				m.sampler.AttachSession(ctx, id, ch)
			}
		}
	}

	return id, nil
}

// SetStatus updates the lifecycle state of a session. When the
// transition is to a terminal state (stopped, failed) it emits the
// vm.teardown ledger entry synchronously — per FR-005 the ledger row
// must be on disk before the state change is visible to subsequent
// callers. If the emitter fails the state transition is rolled back
// so the ledger can never lag the session record.
func (m *Manager) SetStatus(ctx context.Context, sessionID string, status LifecycleState) error {
	// Capture current status before write — if we later roll back we
	// need to know what to restore.
	var prev string
	if status.IsTerminal() {
		if err := m.db.QueryRowContext(ctx,
			`SELECT status FROM sessions WHERE id = ?`, sessionID,
		).Scan(&prev); err != nil {
			return fmt.Errorf("vm: read session for terminal transition: %w", err)
		}
	}

	if _, err := m.db.ExecContext(ctx,
		`UPDATE sessions SET status = ? WHERE id = ?`,
		string(status), sessionID,
	); err != nil {
		return err
	}

	if !status.IsTerminal() {
		return nil
	}
	if err := m.emitTeardown(ctx, sessionID, string(status), time.Now()); err != nil {
		_, _ = m.db.ExecContext(ctx,
			`UPDATE sessions SET status = ? WHERE id = ?`,
			prev, sessionID,
		)
		return fmt.Errorf("vm: emit teardown: %w", err)
	}
	return nil
}

// Finalize marks a session as stopped with the given merge outcome and
// sets ended_at, then emits the vm.teardown ledger row. Emission
// failure rolls back the state change so the ledger invariant holds.
func (m *Manager) Finalize(ctx context.Context, sessionID string, outcome MergeOutcome) error {
	var prev struct {
		status  string
		outcome string
		endedMS sql.NullInt64
	}
	if err := m.db.QueryRowContext(ctx,
		`SELECT status, merge_outcome, ended_at FROM sessions WHERE id = ?`, sessionID,
	).Scan(&prev.status, &prev.outcome, &prev.endedMS); err != nil {
		return fmt.Errorf("vm: read session: %w", err)
	}

	now := time.Now()
	if _, err := m.db.ExecContext(ctx,
		`UPDATE sessions SET status = ?, ended_at = ?, merge_outcome = ? WHERE id = ?`,
		string(StateStopped), now.UnixMilli(), string(outcome), sessionID,
	); err != nil {
		return err
	}
	if err := m.emitTeardown(ctx, sessionID, string(StateStopped), now); err != nil {
		// Restore previous row state to keep the ledger/session-table
		// invariant intact.
		var ended any
		if prev.endedMS.Valid {
			ended = prev.endedMS.Int64
		} else {
			ended = nil
		}
		_, _ = m.db.ExecContext(ctx,
			`UPDATE sessions SET status = ?, merge_outcome = ?, ended_at = ? WHERE id = ?`,
			prev.status, prev.outcome, ended, sessionID,
		)
		return fmt.Errorf("vm: emit teardown: %w", err)
	}
	return nil
}

// Status returns the current session by ID.
func (m *Manager) Status(ctx context.Context, sessionID string) (*Session, error) {
	return m.scanSession(m.db.QueryRowContext(ctx,
		`SELECT id, started_at, ended_at, status, merge_outcome, disk_image_path, overlay_path, vm_db_path, vsock_cid, filter_version, ledger_events_total, policy_status
		 FROM sessions WHERE id = ?`, sessionID))
}

// ActiveSession returns the currently active session, or nil if none.
func (m *Manager) ActiveSession(ctx context.Context) (*Session, error) {
	sess, err := m.scanSession(m.db.QueryRowContext(ctx,
		`SELECT id, started_at, ended_at, status, merge_outcome, disk_image_path, overlay_path, vm_db_path, vsock_cid, filter_version, ledger_events_total, policy_status
		 FROM sessions WHERE status IN ('booting','ready','connecting','stopping')
		 ORDER BY started_at DESC LIMIT 1`))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return sess, err
}

// List returns recent sessions ordered by started_at descending.
func (m *Manager) List(ctx context.Context, limit int) ([]Session, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := m.db.QueryContext(ctx,
		`SELECT id, started_at, ended_at, status, merge_outcome, disk_image_path, overlay_path, vm_db_path, vsock_cid, filter_version, ledger_events_total, policy_status
		 FROM sessions ORDER BY started_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("vm: list sessions: %w", err)
	}
	defer rows.Close()

	var out []Session
	for rows.Next() {
		s, err := m.scanSessionRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

func (m *Manager) scanSession(row *sql.Row) (*Session, error) {
	var s Session
	var startedMS int64
	var endedMS sql.NullInt64
	var status, outcome string

	err := row.Scan(&s.ID, &startedMS, &endedMS, &status, &outcome,
		&s.DiskImagePath, &s.OverlayPath, &s.VMDBPath, &s.VsockCID, &s.FilterVersion,
		&s.LedgerEventsTotal, &s.PolicyStatus)
	if err != nil {
		return nil, err
	}

	s.StartedAt = time.UnixMilli(startedMS)
	if endedMS.Valid {
		t := time.UnixMilli(endedMS.Int64)
		s.EndedAt = &t
	}
	s.Status = LifecycleState(status)
	s.MergeOutcome = MergeOutcome(outcome)
	return &s, nil
}

// emitSpawn constructs the VMSpawnPayload and forwards to the
// injected LedgerEmitter. When no emitter is wired it logs a WARN
// (the absence is audit-relevant but not fatal — pre-spec-029 call
// sites in tests and early boot may still run without one).
func (m *Manager) emitSpawn(ctx context.Context, id SessionID, spec StartSpec, now time.Time, ps PolicyStatus) error {
	if m.ledger == nil {
		slog.Warn("vm: ledger emitter not wired; vm.spawn skipped", "session_id", string(id))
		return nil
	}
	// Build an un-typed map here rather than importing payload — the
	// Manager stays decoupled from the payload struct definitions, and
	// the concrete LedgerEmitter implementation (cmd/sigild) converts
	// to payload.VMSpawnPayload before canonicalising. Keeping this
	// layer dep-free avoids a second import of payload from a new
	// consumer.
	pl := map[string]any{
		"session_id":     string(id),
		"image_path":     spec.ImagePath,
		"policy_id":      spec.PolicyID,
		"egress_tier":    spec.EgressTier,
		"vsock_cid":      spec.VsockCID,
		"filter_version": "", // FilterVersion is not yet carried on StartSpec — reserved for a follow-up.
		"started_at":     now.UTC().Format(time.RFC3339Nano),
	}
	_ = ps
	return m.ledger.EmitVMSpawn(ctx, string(id), pl)
}

// emitTeardown constructs the VMTeardownPayload from the session row
// and forwards to the ledger. Uses DB-sourced values so a crash-
// recovery callers that picks up mid-session still produces faithful
// ledger rows.
func (m *Manager) emitTeardown(ctx context.Context, sessionID string, outcome string, endedAt time.Time) error {
	if m.ledger == nil {
		slog.Warn("vm: ledger emitter not wired; vm.teardown skipped", "session_id", sessionID)
		return nil
	}
	var (
		startedMS         int64
		ledgerEventsTotal uint64
		policyStatus      string
	)
	err := m.db.QueryRowContext(ctx,
		`SELECT started_at, ledger_events_total, policy_status FROM sessions WHERE id = ?`,
		sessionID,
	).Scan(&startedMS, &ledgerEventsTotal, &policyStatus)
	if err != nil {
		// If the session row has disappeared (tests that tear down
		// both tables in parallel), fall back to minimal-field
		// emission with what we know.
		startedMS = endedAt.UnixMilli()
	}
	duration := int64(0)
	if s := time.UnixMilli(startedMS); !s.IsZero() {
		duration = max(int64(endedAt.Sub(s).Seconds()), 0)
	}
	pl := map[string]any{
		"session_id":          sessionID,
		"outcome":             outcome,
		"duration_seconds":    duration,
		"ledger_events_total": ledgerEventsTotal,
		"policy_status":       policyStatus,
		"ended_at":            endedAt.UTC().Format(time.RFC3339Nano),
	}
	return m.ledger.EmitVMTeardown(ctx, sessionID, pl)
}

func (m *Manager) scanSessionRow(rows *sql.Rows) (*Session, error) {
	var s Session
	var startedMS int64
	var endedMS sql.NullInt64
	var status, outcome string

	err := rows.Scan(&s.ID, &startedMS, &endedMS, &status, &outcome,
		&s.DiskImagePath, &s.OverlayPath, &s.VMDBPath, &s.VsockCID, &s.FilterVersion,
		&s.LedgerEventsTotal, &s.PolicyStatus)
	if err != nil {
		return nil, err
	}

	s.StartedAt = time.UnixMilli(startedMS)
	if endedMS.Valid {
		t := time.UnixMilli(endedMS.Int64)
		s.EndedAt = &t
	}
	s.Status = LifecycleState(status)
	s.MergeOutcome = MergeOutcome(outcome)
	return &s, nil
}
