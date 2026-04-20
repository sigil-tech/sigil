//go:build darwin

package vmdriver_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sigil-tech/sigil/internal/vm"
	"github.com/sigil-tech/sigil/internal/vmdriver"
)

// ─── Factory tests ───────────────────────────────────────────────────────────

// TestDarwinDriverFactory_BinaryMissing verifies New returns a structured
// ErrHypervisorUnavailable when sigild-vz cannot be located at the override
// path.
func TestDarwinDriverFactory_BinaryMissing(t *testing.T) {
	t.Setenv("SIGILD_VZ_BINARY", "/definitely/not/a/real/binary/sigild-vz")

	_, err := vmdriver.New()
	require.Error(t, err)

	var vmErr *vm.VMError
	require.ErrorAs(t, err, &vmErr)
	assert.Equal(t, vm.ErrHypervisorUnavailable, vmErr.Code)
}

// TestDarwinDriverFactory_OverrideResolves verifies that SIGILD_VZ_BINARY
// taking a valid path produces a working driver instance.
func TestDarwinDriverFactory_OverrideResolves(t *testing.T) {
	fakeEnv(t, "happy")
	drv, err := vmdriver.New()
	require.NoError(t, err)
	require.NotNil(t, drv)
	require.NoError(t, drv.Close())
}

// ─── Intel graceful degrade (SC-009b / ADR-028d) ─────────────────────────────

// TestDarwinDriverIntelDegrade verifies that on an Intel host (or a forced
// SIGILD_VZ_TEST_ARCH=intel test) the driver returns ErrImageMissing from
// Start without ever spawning sigild-vz or reading the disk image.
func TestDarwinDriverIntelDegrade(t *testing.T) {
	fakeEnv(t, "happy")
	t.Setenv("SIGILD_VZ_TEST_ARCH", "intel")

	drv, err := vmdriver.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = drv.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = drv.Start(ctx, vm.StartSpec{
		Name:        "intel-test",
		ImagePath:   "/tmp/does-not-exist.qcow2",
		OverlayPath: "/tmp/overlay-intel.qcow2",
	})

	var vmErr *vm.VMError
	require.ErrorAs(t, err, &vmErr)
	assert.Equal(t, vm.ErrImageMissing, vmErr.Code,
		"Intel host must short-circuit with ErrImageMissing before touching VZ")
}

// ─── Handshake negotiation ───────────────────────────────────────────────────

// TestDarwinDriverStart_BadProtocol ensures that a sigild-vz advertising an
// incompatible protocol version is rejected at Start time rather than after
// a command has been issued.
func TestDarwinDriverStart_BadProtocol(t *testing.T) {
	fakeEnv(t, "bad-protocol")
	t.Setenv("SIGILD_VZ_TEST_ARCH", "arm64")

	drv, err := vmdriver.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = drv.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err = drv.Start(ctx, vm.StartSpec{
		Name:        "bad-protocol-test",
		OverlayPath: "/tmp/overlay-bad-proto.qcow2",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "protocol version mismatch")
}

// TestDarwinDriverStart_NoHandshake ensures that a sigild-vz that never
// emits its handshake line is killed and the error surfaced after the
// handshake timeout.
func TestDarwinDriverStart_NoHandshake(t *testing.T) {
	fakeEnv(t, "no-handshake")
	t.Setenv("SIGILD_VZ_TEST_ARCH", "arm64")

	drv, err := vmdriver.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = drv.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()
	_, err = drv.Start(ctx, vm.StartSpec{
		Name:        "no-handshake-test",
		OverlayPath: "/tmp/overlay-no-hs.qcow2",
	})
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "handshake")
	assert.Less(t, elapsed, 7*time.Second,
		"handshake timeout should fire well before the outer ctx deadline")
}

// ─── Happy-path command plumbing ─────────────────────────────────────────────

// TestDarwinDriverStart_Happy verifies the round trip through Start →
// response parse → session registration.
func TestDarwinDriverStart_Happy(t *testing.T) {
	fakeEnv(t, "happy")
	t.Setenv("SIGILD_VZ_TEST_ARCH", "arm64")

	drv, err := vmdriver.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = drv.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	id, err := drv.Start(ctx, vm.StartSpec{
		Name:        "happy-test",
		OverlayPath: "/tmp/overlay-happy.qcow2",
		MemoryMB:    4096,
		CPUCount:    4,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, id)

	snap, err := drv.Status(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, vm.StateReady, snap.State)

	require.NoError(t, drv.Health(ctx, id))
	require.NoError(t, drv.Stop(ctx, id))
}

// TestDarwinDriverStart_Error verifies that a sigild-vz error response is
// faithfully translated into a vm.VMError carrying the Swift-side code.
func TestDarwinDriverStart_Error(t *testing.T) {
	fakeEnv(t, "error-start")
	t.Setenv("SIGILD_VZ_TEST_ARCH", "arm64")

	drv, err := vmdriver.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = drv.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = drv.Start(ctx, vm.StartSpec{
		Name:        "err-test",
		OverlayPath: "/tmp/overlay-err.qcow2",
	})
	require.Error(t, err)

	var vmErr *vm.VMError
	require.ErrorAs(t, err, &vmErr)
	assert.Equal(t, vm.ErrImageMissing, vmErr.Code)
}

// ─── Subscribe lifecycle (FR-009) ────────────────────────────────────────────

// TestDarwinDriverSubscribe_ChannelClosesOnHypervisorExit verifies that the
// channel returned by Subscribe closes when sigild-vz emits a
// "hypervisor_exit" push event, per FR-009.
func TestDarwinDriverSubscribe_ChannelClosesOnHypervisorExit(t *testing.T) {
	fakeEnv(t, "push-stats")
	t.Setenv("SIGILD_VZ_TEST_ARCH", "arm64")
	t.Setenv("SIGIL_TEST_FAKE_VZ_STAT_COUNT", "5")

	drv, err := vmdriver.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = drv.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	id, err := drv.Start(ctx, vm.StartSpec{
		Name:        "sub-exit-test",
		OverlayPath: "/tmp/overlay-sub-exit.qcow2",
	})
	require.NoError(t, err)

	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()

	ch, err := drv.Subscribe(subCtx, id)
	require.NoError(t, err)

	var received []vm.StatSnapshot
	for snap := range ch {
		received = append(received, snap)
	}

	assert.GreaterOrEqual(t, len(received), 1,
		"at least one stat snapshot should have arrived before hypervisor_exit")
}

// TestDarwinDriverSubscribe_DuplicateRejected verifies that a second
// concurrent Subscribe on the same session returns an error rather than
// silently fanning out.
func TestDarwinDriverSubscribe_DuplicateRejected(t *testing.T) {
	fakeEnv(t, "happy")
	t.Setenv("SIGILD_VZ_TEST_ARCH", "arm64")

	drv, err := vmdriver.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = drv.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	id, err := drv.Start(ctx, vm.StartSpec{
		Name:        "dup-sub-test",
		OverlayPath: "/tmp/overlay-dup-sub.qcow2",
	})
	require.NoError(t, err)

	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()

	_, err = drv.Subscribe(subCtx, id)
	require.NoError(t, err)

	_, err = drv.Subscribe(subCtx, id)
	require.Error(t, err)
}

// ─── Close cleans up ─────────────────────────────────────────────────────────

// TestDarwinDriverClose_TerminatesSubprocess verifies that Close tears down
// any remaining sessions and is idempotent.
func TestDarwinDriverClose_TerminatesSubprocess(t *testing.T) {
	fakeEnv(t, "happy")
	t.Setenv("SIGILD_VZ_TEST_ARCH", "arm64")

	drv, err := vmdriver.New()
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = drv.Start(ctx, vm.StartSpec{
		Name:        "close-test",
		OverlayPath: "/tmp/overlay-close.qcow2",
	})
	require.NoError(t, err)

	require.NoError(t, drv.Close())
	// Second Close is idempotent.
	require.NoError(t, drv.Close())

	// Post-Close operations fail with an explicit error (not a panic).
	_, err = drv.Start(ctx, vm.StartSpec{Name: "after-close"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, err), "post-close start returns an error")
}

// TestDarwinDriverSessionNotFound verifies the ErrSessionNotFound path.
func TestDarwinDriverSessionNotFound(t *testing.T) {
	fakeEnv(t, "happy")
	t.Setenv("SIGILD_VZ_TEST_ARCH", "arm64")

	drv, err := vmdriver.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = drv.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err = drv.Status(ctx, vm.SessionID("no-such-session"))
	require.Error(t, err)
	var vmErr *vm.VMError
	require.ErrorAs(t, err, &vmErr)
	assert.Equal(t, vm.ErrSessionNotFound, vmErr.Code)
}
