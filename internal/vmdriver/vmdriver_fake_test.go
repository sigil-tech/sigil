package vmdriver_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sigil-tech/sigil/internal/vm"
	"github.com/sigil-tech/sigil/internal/vmdriver"
)

func TestFakeDriver_Start(t *testing.T) {
	d := vmdriver.NewFake()
	defer d.Close()

	ctx := context.Background()

	id, err := d.Start(ctx, vm.StartSpec{Name: "my-session"})
	require.NoError(t, err)
	require.NotEmpty(t, string(id))
}

func TestFakeDriver_Start_EmptyNameFails(t *testing.T) {
	d := vmdriver.NewFake()
	defer d.Close()

	_, err := d.Start(context.Background(), vm.StartSpec{Name: ""})
	require.Error(t, err)
}

func TestFakeDriver_StopAndStatus(t *testing.T) {
	d := vmdriver.NewFake()
	defer d.Close()

	ctx := context.Background()
	id, err := d.Start(ctx, vm.StartSpec{Name: "stop-test"})
	require.NoError(t, err)

	snap, err := d.Status(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, vm.StateBooting, snap.State)

	require.NoError(t, d.Stop(ctx, id))

	snap, err = d.Status(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, vm.StateStopped, snap.State)
}

func TestFakeDriver_Stop_UnknownSession(t *testing.T) {
	d := vmdriver.NewFake()
	defer d.Close()

	err := d.Stop(context.Background(), vm.SessionID("nonexistent"))
	require.Error(t, err)
}

func TestFakeDriver_Health(t *testing.T) {
	d := vmdriver.NewFake()
	defer d.Close()

	ctx := context.Background()
	id, _ := d.Start(ctx, vm.StartSpec{Name: "health-session"})

	require.NoError(t, d.Health(ctx, id))
	require.Error(t, d.Health(ctx, vm.SessionID("unknown")))
}

func TestFakeDriver_Subscribe_EmitsSnapshots(t *testing.T) {
	d := vmdriver.NewFake()
	defer d.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	id, err := d.Start(ctx, vm.StartSpec{Name: "sub-session"})
	require.NoError(t, err)

	ch, err := d.Subscribe(ctx, id)
	require.NoError(t, err)

	var received []vm.StatSnapshot
	for snap := range ch {
		received = append(received, snap)
	}

	// At 10 Hz over 500 ms we expect at least 3 snapshots.
	assert.GreaterOrEqual(t, len(received), 3, "expected at least 3 snapshots in 500ms window")
	for _, s := range received {
		assert.Equal(t, float64(12.5), s.CPUPercent)
		assert.Equal(t, uint8(2), s.CPUCores)
		assert.Equal(t, uint64(512), s.MemoryUsedMB)
		assert.Equal(t, uint64(1024), s.MemoryAllocatedMB)
	}
}

func TestFakeDriver_Subscribe_ClosedOnStop(t *testing.T) {
	d := vmdriver.NewFake()
	defer d.Close()

	ctx := context.Background()
	id, err := d.Start(ctx, vm.StartSpec{Name: "close-on-stop"})
	require.NoError(t, err)

	ch, err := d.Subscribe(ctx, id)
	require.NoError(t, err)

	// Stop the session; the Subscribe channel should eventually close.
	require.NoError(t, d.Stop(ctx, id))

	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // channel closed — pass
			}
		case <-deadline:
			t.Fatal("Subscribe channel did not close within 2s after Stop")
		}
	}
}

func TestFakeDriver_Close_IdempotentAndBlocksNewCalls(t *testing.T) {
	d := vmdriver.NewFake()

	require.NoError(t, d.Close())
	require.NoError(t, d.Close()) // idempotent

	_, err := d.Start(context.Background(), vm.StartSpec{Name: "after-close"})
	require.Error(t, err)
}
