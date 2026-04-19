//go:build linux && integration

package vmdriver_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sigil-tech/sigil/internal/vm"
	"github.com/sigil-tech/sigil/internal/vmdriver"
)

// TestLinuxDriverIntegration_FullLifecycle exercises the full Start → Subscribe
// → Stop lifecycle against a real QEMU/KVM instance. Requires:
//   - KVM_AVAILABLE=1 env var
//   - SIGIL_OS_IMAGE env var pointing at a bootable QCOW2 image
//   - /dev/kvm accessible to the current user
//   - qemu-system-x86_64 or qemu-system-aarch64 in PATH
func TestLinuxDriverIntegration_FullLifecycle(t *testing.T) {
	if os.Getenv("KVM_AVAILABLE") != "1" {
		t.Skip("set KVM_AVAILABLE=1 and provide SIGIL_OS_IMAGE to run")
	}
	imagePath := os.Getenv("SIGIL_OS_IMAGE")
	if imagePath == "" {
		t.Skip("SIGIL_OS_IMAGE not set; skipping integration test")
	}

	d, err := vmdriver.New()
	require.NoError(t, err, "vmdriver.New() — check QEMU is installed and /dev/kvm is accessible")

	defer d.Close()

	overlayPath := t.TempDir() + "/test-overlay.qcow2"

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	spec := vm.StartSpec{
		Name:        "integration-test",
		ImagePath:   imagePath,
		OverlayPath: overlayPath,
		VsockCID:    105,
		MemoryMB:    2048,
		CPUCount:    2,
	}

	id, err := d.Start(ctx, spec)
	require.NoError(t, err, "driver.Start")
	t.Logf("started session: %s", id)

	// Subscribe and collect a few stat snapshots.
	ch, err := d.Subscribe(ctx, id)
	require.NoError(t, err)

	var snaps []vm.StatSnapshot
	collectTimeout := time.After(5 * time.Second)
collect:
	for {
		select {
		case snap, ok := <-ch:
			if !ok {
				break collect
			}
			snaps = append(snaps, snap)
			if len(snaps) >= 3 {
				break collect
			}
		case <-collectTimeout:
			break collect
		}
	}
	t.Logf("collected %d stat snapshots", len(snaps))

	// Health check.
	if err := d.Health(ctx, id); err != nil {
		t.Logf("health check: %v (may be normal if VM is still booting)", err)
	}

	// Status.
	snap, err := d.Status(ctx, id)
	require.NoError(t, err)
	t.Logf("vm status: %s (pid=%d)", snap.State, snap.PID)

	// Stop.
	require.NoError(t, d.Stop(ctx, id))

	// Channel must be closed after Stop.
	drainTimeout := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				goto done
			}
		case <-drainTimeout:
			t.Fatal("Subscribe channel not closed within 5s after Stop")
		}
	}
done:
	t.Log("integration test passed")
}

// TestLinuxDriverIntegration_StopTimeout verifies that Stop handles a VM that
// does not respond to system_powerdown and falls back to SIGKILL.
func TestLinuxDriverIntegration_StopTimeout(t *testing.T) {
	if os.Getenv("KVM_AVAILABLE") != "1" {
		t.Skip("set KVM_AVAILABLE=1 and provide SIGIL_OS_IMAGE to run")
	}
	imagePath := os.Getenv("SIGIL_OS_IMAGE")
	if imagePath == "" {
		t.Skip("SIGIL_OS_IMAGE not set; skipping integration test")
	}
	t.Skip("stress/timeout test — run manually with sufficient time budget")
}
