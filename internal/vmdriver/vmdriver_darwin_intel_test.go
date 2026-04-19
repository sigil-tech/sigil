//go:build darwin

package vmdriver_test

import (
	"os"
	"testing"
)

// TestIntelGracefulDegrade verifies SC-009b: when sigild detects it is running
// on an Intel Mac (SIGILD_VZ_TEST_ARCH=intel), the macOS hypervisor driver
// reports the arch and VMStart returns ERR_IMAGE_MISSING rather than
// attempting to boot a mismatched ARM64 image.
//
// This test is scaffolded for Phase 9 (Task 9.7). It is skipped until Phase 4b
// lands the Swift sigild-vz extraction and vmdriver_darwin.go is implemented.
//
// To run manually once Phase 4b is complete:
//
//	SIGILD_VZ_TEST_ARCH=intel go test -tags=darwin -run TestIntelGracefulDegrade \
//	    ./internal/vmdriver/
//
// Per ADR-028d: SIGILD_VZ_TEST_ARCH=intel overrides the driver's arch detection
// so the test does not require real Intel hardware.
func TestIntelGracefulDegrade(t *testing.T) {
	t.Skip(
		"Phase 4b BLOCKED: requires sigild-vz Swift extraction (vmdriver_darwin.go)." +
			" Set SIGILD_VZ_TEST_ARCH=intel once Phase 4b lands to run this gate.",
	)

	// Guard: env override must be set for the test to be meaningful.
	if os.Getenv("SIGILD_VZ_TEST_ARCH") != "intel" {
		t.Skip("SIGILD_VZ_TEST_ARCH=intel required; test not meaningful without arch override")
	}

	// TODO(Phase 4b): instantiate the darwin vmdriver, call StartVM with an
	// ARM64 image path, and assert the return value is vm.ErrImageMissing
	// (or the equivalent ERR_IMAGE_MISSING sentinel).
	//
	// Expected sequence per ADR-028d:
	//   1. Driver reads SIGILD_VZ_TEST_ARCH env and reports "intel".
	//   2. Driver detects ARM64 image against Intel host.
	//   3. StartVM returns vm.ErrImageMissing immediately, no VZ API calls made.
	//
	// Example (uncomment when vmdriver_darwin.go exists):
	//
	//   drv, err := vmdriver.NewDarwin(vmdriver.DarwinConfig{
	//       VZBinary: os.Getenv("SIGILD_VZ_BINARY"),
	//   })
	//   require.NoError(t, err)
	//
	//   _, err = drv.StartVM(context.Background(), vm.StartSpec{
	//       DiskImagePath: "testdata/sigil-os.qcow2",
	//       MemoryMB:      4096,
	//       CPUCount:      4,
	//   })
	//   require.ErrorIs(t, err, vm.ErrImageMissing,
	//       "Intel host with ARM64 image must return ErrImageMissing")
}
