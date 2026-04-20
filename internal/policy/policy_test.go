package policy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeEmitter struct {
	mu    sync.Mutex
	calls []map[string]any
	err   error
}

func (f *fakeEmitter) EmitPolicyDeny(_ context.Context, _ string, payload any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, payload.(map[string]any))
	return f.err
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestPolicyDenier_EmitsLedgerRow(t *testing.T) {
	fake := &fakeEmitter{}
	d, err := New(fake, testLogger())
	require.NoError(t, err)

	// Pin the clock so the test can assert denied_at exactly.
	SetClock(d, func() time.Time {
		return time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	})

	require.NoError(t, d.Deny(context.Background(),
		"exec.sandbox.shell", "vm.spawn", "sandbox policy blocks interactive shells"))

	fake.mu.Lock()
	defer fake.mu.Unlock()
	require.Len(t, fake.calls, 1)
	pl := fake.calls[0]
	require.Equal(t, "exec.sandbox.shell", pl["rule"])
	require.Equal(t, "vm.spawn", pl["action"])
	require.Equal(t, "sandbox policy blocks interactive shells", pl["reason"])
	require.Equal(t, "2026-04-19T12:00:00Z", pl["denied_at"])
}

func TestPolicyDenier_RejectsEmptyRule(t *testing.T) {
	d, err := New(&fakeEmitter{}, testLogger())
	require.NoError(t, err)
	if err := d.Deny(context.Background(), "", "vm.spawn", "nope"); err == nil {
		t.Fatalf("expected error for empty rule")
	}
}

func TestPolicyDenier_RejectsEmptyAction(t *testing.T) {
	d, err := New(&fakeEmitter{}, testLogger())
	require.NoError(t, err)
	if err := d.Deny(context.Background(), "exec.sandbox.shell", "", "nope"); err == nil {
		t.Fatalf("expected error for empty action")
	}
}

func TestPolicyDenier_NewRejectsNilEmitter(t *testing.T) {
	_, err := New(nil, testLogger())
	if err == nil {
		t.Fatalf("expected error when emitter is nil (no silent no-op)")
	}
}

func TestPolicyDenier_PropagatesEmitError(t *testing.T) {
	fake := &fakeEmitter{err: errors.New("ledger unavailable")}
	d, err := New(fake, testLogger())
	require.NoError(t, err)

	err = d.Deny(context.Background(), "rule", "action", "reason")
	if err == nil {
		t.Fatalf("expected propagated emit error")
	}
	require.Contains(t, err.Error(), "ledger unavailable")
}
