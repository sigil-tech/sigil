package vm

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeLedgerEmitter records every emit so tests can assert the shape
// and ordering of vm.spawn / vm.teardown calls. Safe for concurrent
// emits — covers the case where two tests share a Manager.
type fakeLedgerEmitter struct {
	mu          sync.Mutex
	spawns      []fakeEmitCall
	teardowns   []fakeEmitCall
	spawnErr    error
	teardownErr error
}

type fakeEmitCall struct {
	SessionID string
	Payload   map[string]any
}

func (f *fakeLedgerEmitter) EmitVMSpawn(_ context.Context, sessionID string, payload any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.spawns = append(f.spawns, fakeEmitCall{SessionID: sessionID, Payload: payloadAsMap(payload)})
	return f.spawnErr
}

func (f *fakeLedgerEmitter) EmitVMTeardown(_ context.Context, sessionID string, payload any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.teardowns = append(f.teardowns, fakeEmitCall{SessionID: sessionID, Payload: payloadAsMap(payload)})
	return f.teardownErr
}

func payloadAsMap(p any) map[string]any {
	if m, ok := p.(map[string]any); ok {
		return m
	}
	return nil
}

// TestVMSpawnEmitsLedger covers Task 5.2: a successful StartWithSpec
// MUST emit vm.spawn exactly once with the session id, policy id,
// and egress tier. Emission happens BEFORE the driver is invoked
// (FR-004) — verified here by checking the spawn row was recorded
// under a nil driver, where there is no driver.Start call to race
// with.
func TestVMSpawnEmitsLedger(t *testing.T) {
	db := testDB(t)
	fake := &fakeLedgerEmitter{}
	mgr := NewManager(db, nil, nil).WithLedger(fake)
	ctx := context.Background()

	spec := StartSpec{
		Name:       "emit-test",
		PolicyID:   "sandbox-default",
		EgressTier: "restricted",
		ImagePath:  "/img.qcow2",
		VsockCID:   42,
	}
	id, err := mgr.StartWithSpec(ctx, spec, nil)
	require.NoError(t, err)

	fake.mu.Lock()
	defer fake.mu.Unlock()
	require.Len(t, fake.spawns, 1, "exactly one vm.spawn emission expected")
	require.Len(t, fake.teardowns, 0, "no teardown expected for a still-booting session")

	s := fake.spawns[0]
	require.Equal(t, string(id), s.SessionID)
	require.Equal(t, "sandbox-default", s.Payload["policy_id"])
	require.Equal(t, "restricted", s.Payload["egress_tier"])
	require.Equal(t, "/img.qcow2", s.Payload["image_path"])
	require.EqualValues(t, 42, s.Payload["vsock_cid"])
}

// TestVMTeardownEmitsLedger covers Task 5.3: Finalize emits vm.teardown
// synchronously with the state change (FR-005). We also exercise
// SetStatus(StateFailed) to confirm the failed-terminal branch also
// emits.
func TestVMTeardownEmitsLedger(t *testing.T) {
	t.Run("Finalize emits vm.teardown", func(t *testing.T) {
		db := testDB(t)
		fake := &fakeLedgerEmitter{}
		mgr := NewManager(db, nil, nil).WithLedger(fake)
		ctx := context.Background()

		id, err := mgr.StartWithSpec(ctx, StartSpec{Name: "s", ImagePath: "/i"}, nil)
		require.NoError(t, err)

		if err := mgr.Finalize(ctx, string(id), MergeOutcomeComplete); err != nil {
			t.Fatalf("Finalize: %v", err)
		}

		fake.mu.Lock()
		defer fake.mu.Unlock()
		require.Len(t, fake.teardowns, 1)
		td := fake.teardowns[0]
		require.Equal(t, string(id), td.SessionID)
		require.Equal(t, "stopped", td.Payload["outcome"])
	})

	t.Run("SetStatus(StateFailed) emits vm.teardown with failed outcome", func(t *testing.T) {
		db := testDB(t)
		fake := &fakeLedgerEmitter{}
		mgr := NewManager(db, nil, nil).WithLedger(fake)
		ctx := context.Background()

		id, err := mgr.StartWithSpec(ctx, StartSpec{Name: "f", ImagePath: "/i"}, nil)
		require.NoError(t, err)
		if err := mgr.SetStatus(ctx, string(id), StateFailed); err != nil {
			t.Fatalf("SetStatus: %v", err)
		}

		fake.mu.Lock()
		defer fake.mu.Unlock()
		require.Len(t, fake.teardowns, 1)
		require.Equal(t, "failed", fake.teardowns[0].Payload["outcome"])
	})

	t.Run("SetStatus(StateReady) does NOT emit vm.teardown", func(t *testing.T) {
		db := testDB(t)
		fake := &fakeLedgerEmitter{}
		mgr := NewManager(db, nil, nil).WithLedger(fake)
		ctx := context.Background()

		id, err := mgr.StartWithSpec(ctx, StartSpec{Name: "r", ImagePath: "/i"}, nil)
		require.NoError(t, err)
		if err := mgr.SetStatus(ctx, string(id), StateReady); err != nil {
			t.Fatalf("SetStatus ready: %v", err)
		}

		fake.mu.Lock()
		defer fake.mu.Unlock()
		require.Len(t, fake.teardowns, 0, "non-terminal transition must not emit")
	})
}

// TestManager_EmitSpawnFailureRollsBackSession asserts that a ledger
// emission failure on vm.spawn removes the session row — the system
// must never have a session with no matching ledger entry.
func TestManager_EmitSpawnFailureRollsBackSession(t *testing.T) {
	db := testDB(t)
	fake := &fakeLedgerEmitter{spawnErr: errEmitFail}
	mgr := NewManager(db, nil, nil).WithLedger(fake)
	ctx := context.Background()

	_, err := mgr.StartWithSpec(ctx, StartSpec{Name: "fails", ImagePath: "/i"}, nil)
	if err == nil {
		t.Fatalf("StartWithSpec: expected error from emit failure")
	}

	// No session should exist.
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("session row not rolled back after emit failure: count=%d", count)
	}
}

var errEmitFail = &simpleError{"fake emit failure"}

type simpleError struct{ msg string }

func (e *simpleError) Error() string { return e.msg }
