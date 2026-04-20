//go:build darwin

package vmdriver_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sigil-tech/sigil/internal/vm"
	"github.com/sigil-tech/sigil/internal/vmdriver"
)

// TestIntelGracefulDegrade is the SC-009b gate. When sigild runs on an Intel
// Mac (or SIGILD_VZ_TEST_ARCH=intel forces the Intel path), Start must return
// ErrImageMissing with an Apple-Silicon-required message before spawning
// sigild-vz or reading the disk image. See ADR-028d.
//
// This test is complementary to TestDarwinDriverIntelDegrade in
// vmdriver_darwin_test.go: that sibling uses the fake sigild-vz harness to
// cover the general driver surface; this one is the explicit SC-009b gate
// invoked from CI with SIGILD_VZ_TEST_ARCH=intel so the test is a no-op on
// normal arm64 runs and a hard failure when the env guard is active.
func TestIntelGracefulDegrade(t *testing.T) {
	if os.Getenv("SIGILD_VZ_TEST_ARCH") != "intel" {
		t.Skip("SIGILD_VZ_TEST_ARCH=intel required; the SC-009b gate only runs when the Intel path is forced")
	}

	// Point sigild-vz at this test binary running in fake mode so New()
	// succeeds and we can exercise the Start short-circuit.
	fakeEnv(t, "happy")
	t.Setenv("SIGILD_VZ_TEST_ARCH", "intel")

	drv, err := vmdriver.New()
	require.NoError(t, err, "New() must succeed on Intel — the degrade happens at Start, not construction")
	t.Cleanup(func() { _ = drv.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = drv.Start(ctx, vm.StartSpec{
		Name:        "intel-gate",
		ImagePath:   "/does/not/exist/sigil-os.qcow2",
		OverlayPath: "/tmp/intel-gate-overlay.qcow2",
		MemoryMB:    4096,
		CPUCount:    4,
	})

	var vmErr *vm.VMError
	require.ErrorAs(t, err, &vmErr)
	assert.Equal(t, vm.ErrImageMissing, vmErr.Code,
		"Intel host with ARM64 image must return ErrImageMissing")
	assert.Contains(t, vmErr.Message, "Apple Silicon",
		"error message must explain the Apple-Silicon requirement")
}
