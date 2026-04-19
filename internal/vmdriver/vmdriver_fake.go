// Package vmdriver provides implementations of the vm.Driver interface.
// This file contains the in-memory fake implementation used by tests; it
// carries no build tag so it is available on all platforms.
package vmdriver

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sigil-tech/sigil/internal/vm"
)

// fakeSession holds the in-memory state for a session managed by fakeDriver.
type fakeSession struct {
	spec      vm.StartSpec
	state     vm.LifecycleState
	startedAt time.Time
	cancel    context.CancelFunc // cancels the Subscribe goroutine
}

// fakeDriver is an in-memory implementation of vm.Driver for use in tests.
// All methods are goroutine-safe. Subscribe emits a canned StatSnapshot every
// 100 ms (10 Hz) until ctx is cancelled or Close is called.
//
// Obtain via NewFake.
type fakeDriver struct {
	mu       sync.Mutex
	sessions map[vm.SessionID]*fakeSession
	closed   bool
}

// NewFake returns an in-memory vm.Driver suitable for unit and integration
// tests. It requires no hypervisor and emits deterministic stat snapshots.
func NewFake() vm.Driver {
	return &fakeDriver{
		sessions: make(map[vm.SessionID]*fakeSession),
	}
}

// Start validates the spec (non-empty Name is required) and records an
// in-memory session in the booting state.
func (d *fakeDriver) Start(ctx context.Context, spec vm.StartSpec) (vm.SessionID, error) {
	if err := d.checkClosed(); err != nil {
		return "", err
	}
	if spec.Name == "" {
		return "", fmt.Errorf("vmdriver fake: Name must not be empty")
	}

	id := vm.SessionID("fake-" + spec.Name)

	d.mu.Lock()
	defer d.mu.Unlock()
	d.sessions[id] = &fakeSession{
		spec:      spec,
		state:     vm.StateBooting,
		startedAt: time.Now(),
	}
	return id, nil
}

// Stop transitions the session to stopped. Returns an error if the session is
// not found or the driver is closed.
func (d *fakeDriver) Stop(ctx context.Context, id vm.SessionID) error {
	if err := d.checkClosed(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	sess, ok := d.sessions[id]
	if !ok {
		return fmt.Errorf("vmdriver fake: session %s not found", id)
	}
	if sess.cancel != nil {
		sess.cancel()
	}
	sess.state = vm.StateStopped
	return nil
}

// Status returns a Snapshot for the session. EndedAt is zero for non-terminal
// sessions.
func (d *fakeDriver) Status(ctx context.Context, id vm.SessionID) (vm.Snapshot, error) {
	if err := d.checkClosed(); err != nil {
		return vm.Snapshot{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	sess, ok := d.sessions[id]
	if !ok {
		return vm.Snapshot{}, fmt.Errorf("vmdriver fake: session %s not found", id)
	}
	snap := vm.Snapshot{
		State:     sess.state,
		StartedAt: sess.startedAt,
		VsockCID:  sess.spec.VsockCID,
		PID:       1, // arbitrary non-zero sentinel
	}
	return snap, nil
}

// Subscribe returns a channel that emits a canned StatSnapshot every 100 ms
// until ctx is cancelled, Stop is called for this session, or Close is called.
// The channel is closed when emission stops.
func (d *fakeDriver) Subscribe(ctx context.Context, id vm.SessionID) (<-chan vm.StatSnapshot, error) {
	if err := d.checkClosed(); err != nil {
		return nil, err
	}

	d.mu.Lock()
	sess, ok := d.sessions[id]
	if !ok {
		d.mu.Unlock()
		return nil, fmt.Errorf("vmdriver fake: session %s not found", id)
	}

	subCtx, cancel := context.WithCancel(ctx)
	sess.cancel = cancel
	d.mu.Unlock()

	ch := make(chan vm.StatSnapshot, 4)
	go func() {
		defer close(ch)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-subCtx.Done():
				return
			case t := <-ticker.C:
				snap := vm.StatSnapshot{
					Timestamp:         t,
					CPUPercent:        12.5,
					CPUCores:          2,
					MemoryUsedMB:      512,
					MemoryAllocatedMB: 1024,
				}
				select {
				case ch <- snap:
				case <-subCtx.Done():
					return
				}
			}
		}
	}()

	return ch, nil
}

// Health returns nil for any known session, and an error for unknown sessions
// or a closed driver.
func (d *fakeDriver) Health(ctx context.Context, id vm.SessionID) error {
	if err := d.checkClosed(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, ok := d.sessions[id]; !ok {
		return fmt.Errorf("vmdriver fake: session %s not found", id)
	}
	return nil
}

// Close cancels all active Subscribe goroutines and marks the driver as closed.
// Subsequent calls to any method return an error.
func (d *fakeDriver) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return nil
	}
	d.closed = true
	for _, sess := range d.sessions {
		if sess.cancel != nil {
			sess.cancel()
		}
	}
	return nil
}

func (d *fakeDriver) checkClosed() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return fmt.Errorf("vmdriver fake: driver is closed")
	}
	return nil
}
