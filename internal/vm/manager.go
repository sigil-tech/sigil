package vm

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Manager implements VM session lifecycle management backed by the sessions
// table in SQLite. It enforces the single-VM constraint (Phase 1).
type Manager struct {
	db *sql.DB
	mu sync.Mutex
}

// NewManager creates a Manager that operates on the given database.
func NewManager(db *sql.DB) *Manager {
	return &Manager{db: db}
}

// NewManagerWithoutDriver is an alias for NewManager. It exists to provide a
// stable call site for Phase 0 handlers while Phase 3 will introduce a
// two-argument NewManager(db, driver) form. Callers that should NOT have a
// driver (e.g. stub handlers) call this; Phase 3 migration replaces each call
// site in one commit.
func NewManagerWithoutDriver(db *sql.DB) *Manager {
	return NewManager(db)
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

// Stop transitions a session to the stopping state.
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
	return err
}

// SetStatus updates the lifecycle state of a session.
func (m *Manager) SetStatus(ctx context.Context, sessionID string, status LifecycleState) error {
	_, err := m.db.ExecContext(ctx,
		`UPDATE sessions SET status = ? WHERE id = ?`,
		string(status), sessionID,
	)
	return err
}

// Finalize marks a session as stopped with the given merge outcome and sets ended_at.
func (m *Manager) Finalize(ctx context.Context, sessionID string, outcome MergeOutcome) error {
	now := time.Now().UnixMilli()
	_, err := m.db.ExecContext(ctx,
		`UPDATE sessions SET status = ?, ended_at = ?, merge_outcome = ? WHERE id = ?`,
		string(StateStopped), now, string(outcome), sessionID,
	)
	return err
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
