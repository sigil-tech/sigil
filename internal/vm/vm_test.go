package vm

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	// Create sessions table (migration v2)
	_, err = db.Exec(`
		PRAGMA journal_mode = WAL;
		PRAGMA foreign_keys = ON;
		CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			started_at INTEGER NOT NULL,
			ended_at INTEGER,
			status TEXT NOT NULL,
			merge_outcome TEXT NOT NULL DEFAULT 'pending',
			disk_image_path TEXT NOT NULL,
			overlay_path TEXT NOT NULL DEFAULT '',
			vm_db_path TEXT NOT NULL DEFAULT '',
			vsock_cid INTEGER NOT NULL DEFAULT 0,
			filter_version TEXT NOT NULL DEFAULT ''
		);
		CREATE INDEX IF NOT EXISTS idx_sessions_status ON sessions(status);
	`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestStartSession(t *testing.T) {
	db := testDB(t)
	mgr := NewManager(db)
	ctx := context.Background()

	sess, err := mgr.Start(ctx, StartRequest{
		DiskImagePath: "/images/base.qcow2",
		OverlayPath:   "/tmp/overlay.qcow2",
		VMDBPath:      "/tmp/vm-db/sigild.db",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if sess.ID == "" {
		t.Fatal("expected non-empty session ID")
	}
	if sess.Status != StateBooting {
		t.Errorf("expected status booting, got %s", sess.Status)
	}
}

func TestSingleVMConstraint(t *testing.T) {
	db := testDB(t)
	mgr := NewManager(db)
	ctx := context.Background()

	_, err := mgr.Start(ctx, StartRequest{DiskImagePath: "/images/base.qcow2"})
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}

	_, err = mgr.Start(ctx, StartRequest{DiskImagePath: "/images/base.qcow2"})
	if err == nil {
		t.Fatal("expected error on second Start")
	}
	vmErr, ok := err.(*VMError)
	if !ok {
		t.Fatalf("expected *VMError, got %T", err)
	}
	if vmErr.Code != ErrSessionActive {
		t.Errorf("expected error code %s, got %s", ErrSessionActive, vmErr.Code)
	}
}

func TestStopSession(t *testing.T) {
	db := testDB(t)
	mgr := NewManager(db)
	ctx := context.Background()

	sess, _ := mgr.Start(ctx, StartRequest{DiskImagePath: "/images/base.qcow2"})

	if err := mgr.Stop(ctx, sess.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	got, err := mgr.Status(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got.Status != StateStopping {
		t.Errorf("expected stopping, got %s", got.Status)
	}
}

func TestStopNotFound(t *testing.T) {
	db := testDB(t)
	mgr := NewManager(db)
	ctx := context.Background()

	err := mgr.Stop(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error")
	}
	vmErr, ok := err.(*VMError)
	if !ok {
		t.Fatalf("expected *VMError, got %T", err)
	}
	if vmErr.Code != ErrSessionNotFound {
		t.Errorf("expected %s, got %s", ErrSessionNotFound, vmErr.Code)
	}
}

func TestFinalizeSession(t *testing.T) {
	db := testDB(t)
	mgr := NewManager(db)
	ctx := context.Background()

	sess, _ := mgr.Start(ctx, StartRequest{DiskImagePath: "/images/base.qcow2"})
	_ = mgr.Stop(ctx, sess.ID)
	_ = mgr.Finalize(ctx, sess.ID, MergeOutcomeComplete)

	got, err := mgr.Status(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got.Status != StateStopped {
		t.Errorf("expected stopped, got %s", got.Status)
	}
	if got.MergeOutcome != MergeOutcomeComplete {
		t.Errorf("expected complete, got %s", got.MergeOutcome)
	}
	if got.EndedAt == nil {
		t.Error("expected EndedAt to be set")
	}
}

func TestListSessions(t *testing.T) {
	db := testDB(t)
	mgr := NewManager(db)
	ctx := context.Background()

	// Start and finalize a session so we can start another.
	s1, _ := mgr.Start(ctx, StartRequest{DiskImagePath: "/images/a.qcow2"})
	_ = mgr.Stop(ctx, s1.ID)
	_ = mgr.Finalize(ctx, s1.ID, MergeOutcomeComplete)

	_, _ = mgr.Start(ctx, StartRequest{DiskImagePath: "/images/b.qcow2"})

	list, err := mgr.List(ctx, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(list))
	}
}

func TestActiveSession(t *testing.T) {
	db := testDB(t)
	mgr := NewManager(db)
	ctx := context.Background()

	// No active session.
	s, err := mgr.ActiveSession(ctx)
	if err != nil {
		t.Fatalf("ActiveSession: %v", err)
	}
	if s != nil {
		t.Fatal("expected nil active session")
	}

	// Start one.
	sess, _ := mgr.Start(ctx, StartRequest{DiskImagePath: "/images/base.qcow2"})

	s, err = mgr.ActiveSession(ctx)
	if err != nil {
		t.Fatalf("ActiveSession: %v", err)
	}
	if s == nil {
		t.Fatal("expected active session")
	}
	if s.ID != sess.ID {
		t.Errorf("expected session %s, got %s", sess.ID, s.ID)
	}
}

func TestLifecycleStateHelpers(t *testing.T) {
	tests := []struct {
		state    LifecycleState
		active   bool
		terminal bool
	}{
		{StateBooting, true, false},
		{StateReady, true, false},
		{StateConnecting, true, false},
		{StateStopping, true, false},
		{StateStopped, false, true},
		{StateFailed, false, true},
	}
	for _, tt := range tests {
		if got := tt.state.IsActive(); got != tt.active {
			t.Errorf("%s.IsActive() = %v, want %v", tt.state, got, tt.active)
		}
		if got := tt.state.IsTerminal(); got != tt.terminal {
			t.Errorf("%s.IsTerminal() = %v, want %v", tt.state, got, tt.terminal)
		}
	}
}
