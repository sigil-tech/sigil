//go:build integration

// Package integration contains acceptance test stubs for spec 028:
// Kenaz VM Sandbox Integration.
//
// These tests document the full human-QA test intent for US1–US4 and
// SC-001 through SC-014. Every test body calls t.Skip with a description
// of what real execution would require (KVM runner, sigil-os guest image,
// VZ binary). The stubs keep the test binary buildable and the CI job
// green, while making the intent explicit for the human who will run the
// full suite once the CI runners are available.
//
// Full execution requirements:
//   - Linux (KVM): KVM_AVAILABLE=1, SIGIL_OS_IMAGE=/path/to/sigil-os.qcow2,
//     /dev/kvm accessible, qemu-system-x86_64 in PATH.
//   - macOS (VZ):  VZ_AVAILABLE=1, SIGILD_VZ_BINARY=/path/to/sigild-vz,
//     macOS 13+ Apple Silicon. Phase 4b BLOCKED — sigild-vz not yet extracted.
//
// Run (Linux KVM, when env is configured):
//
//	KVM_AVAILABLE=1 SIGIL_OS_IMAGE=./testdata/sigil-os.qcow2 \
//	    go test -tags=integration -v -timeout=600s ./test/integration/
//
// See specs/028-kenaz-vm-integration/spec.md for the authoritative
// acceptance criteria and success criteria referenced here.
package integration

import (
	"os"
	"testing"
)

// skipUnlessKVMAvailable skips the test when the Linux KVM integration
// prerequisites are absent. This keeps the CI job green on runners that
// do not have /dev/kvm or a sigil-os image.
func skipUnlessKVMAvailable(t *testing.T) {
	t.Helper()
	if os.Getenv("KVM_AVAILABLE") != "1" {
		t.Skip("set KVM_AVAILABLE=1 and SIGIL_OS_IMAGE to run KVM integration tests")
	}
	if os.Getenv("SIGIL_OS_IMAGE") == "" {
		t.Skip("SIGIL_OS_IMAGE env var required; run `make fetch-sigil-os-image` first")
	}
}

// skipUnlessVZAvailable skips the test when the macOS VZ prerequisites are
// absent. Phase 4b is BLOCKED so this always skips for now.
func skipUnlessVZAvailable(t *testing.T) {
	t.Helper()
	t.Skip(
		"Phase 4b BLOCKED: requires sigild-vz Swift extraction." +
			" Set VZ_AVAILABLE=1 and SIGILD_VZ_BINARY once Phase 4b lands.",
	)
}

// ---------------------------------------------------------------------------
// User Story 1 — Launch a fresh VM workspace (P1)
// ---------------------------------------------------------------------------

// TestUS1_VMBootReady verifies that sigilctl vm start provisions a VM, boots
// it to ready state via the ADR-002 vsock handshake, and reports
// VMLifecycleState.ready within 60 seconds.
//
// Human QA equivalent:
//  1. Start sigild with a fresh database.
//  2. Run `sigilctl vm start`.
//  3. Poll `sigilctl vm status` until status == "ready" or 60s elapses.
//  4. Assert status == "ready".
//  5. Assert merge_outcome == "pending" (session still active).
func TestUS1_VMBootReady(t *testing.T) {
	skipUnlessKVMAvailable(t)
	// TODO(Phase 9 full execution): implement using sigild test harness.
	// See specs/028-kenaz-vm-integration/spec.md §US1 SC-1.
}

// TestUS1_VMAlreadyActive verifies that a second VMStart while a session is
// active returns ERR_SESSION_ACTIVE within 100ms (SC-006).
//
// Human QA equivalent:
//  1. Start a VM session.
//  2. Attempt `sigilctl vm start` again while the first is in booting/ready state.
//  3. Assert the response contains error code ERR_SESSION_ACTIVE.
//  4. Assert no new row in sessions table.
func TestUS1_VMAlreadyActive(t *testing.T) {
	skipUnlessKVMAvailable(t)
	// TODO(Phase 9 full execution): implement using sigild test harness.
	// See specs/028-kenaz-vm-integration/spec.md §US1 SC-3, FR-011.
}

// TestUS1_VMStatusFields verifies that sigilctl vm status returns the correct
// fields for a running session: session ID, uptime, disk image path, overlay
// path, and vsock health probe status.
func TestUS1_VMStatusFields(t *testing.T) {
	skipUnlessKVMAvailable(t)
	// TODO(Phase 9 full execution): boot VM, call status, assert fields.
	// See specs/028-kenaz-vm-integration/spec.md §US1 SC-2.
}

// ---------------------------------------------------------------------------
// User Story 2 — Tear down VM and preserve session context (P1)
// ---------------------------------------------------------------------------

// TestUS2_CleanTeardown verifies the 6-step teardown sequence: VM process
// terminates within 30 seconds, VMMerge completes within 60 seconds, host
// SQLite gains all non-filtered rows, merge_log.status == "complete".
//
// Human QA equivalent:
//  1. Start a VM, generate 20+ synthetic observer events.
//  2. Run `sigilctl vm stop`.
//  3. Assert sessions.status == "stopped" within 30s (SC-002a).
//  4. Assert merge_log.status == "complete" within 90s (SC-002b).
//  5. Assert training_corpus row count matches expected (accounting for filter).
//  6. Assert overlay file is deleted from disk.
func TestUS2_CleanTeardown(t *testing.T) {
	skipUnlessKVMAvailable(t)
	// TODO(Phase 9 full execution): implement using sigild test harness.
	// See specs/028-kenaz-vm-integration/spec.md §US2 SC-1.
}

// TestUS2_UncleanShutdownRecovery verifies that sigild detects an in-progress
// merge on restart and resumes from merge_log.checkpoint without duplicating rows.
//
// Human QA equivalent:
//  1. Start a VM, generate events.
//  2. Initiate teardown; inject SIGKILL to sigild mid-merge.
//  3. Restart sigild.
//  4. Assert sigild detects merge_log.status == "in_progress".
//  5. Assert merge completes with status "complete" or "partial".
//  6. Assert no duplicate rows in training_corpus.
func TestUS2_UncleanShutdownRecovery(t *testing.T) {
	skipUnlessKVMAvailable(t)
	// TODO(Phase 9 full execution): implement crash injection.
	// See specs/028-kenaz-vm-integration/spec.md §US2 SC-2, SC-004.
}

// TestUS2_VMListShowsStopped verifies that `sigilctl vm list` shows a
// terminated VM with status "stopped", teardown timestamp, and merge_outcome.
func TestUS2_VMListShowsStopped(t *testing.T) {
	skipUnlessKVMAvailable(t)
	// TODO(Phase 9 full execution).
	// See specs/028-kenaz-vm-integration/spec.md §US2 SC-3.
}

// ---------------------------------------------------------------------------
// User Story 3 — Configure workbench apps before launch (P2)
// ---------------------------------------------------------------------------

// TestUS3_WorkbenchConfigPersists verifies that setting a workbench config
// field via `sigilctl vm config set` persists across daemon restarts.
func TestUS3_WorkbenchConfigPersists(t *testing.T) {
	skipUnlessKVMAvailable(t)
	// TODO(Phase 9 full execution).
	// See specs/028-kenaz-vm-integration/spec.md §US3 SC-1.
}

// TestUS3_EditorAvailableInGuest verifies that a configured editor is
// resolvable on the guest PATH after VM boot.
func TestUS3_EditorAvailableInGuest(t *testing.T) {
	skipUnlessKVMAvailable(t)
	// TODO(Phase 9 full execution): boot VM with LauncherProfile.editor set,
	// verify editor binary is on guest PATH via vsock RPC.
	// See specs/028-kenaz-vm-integration/spec.md §US3 SC-2.
}

// TestUS3_InvalidEditorSkippedWithWarning verifies that an invalid editor
// name in LauncherProfile does not cause the VM boot to fail.
func TestUS3_InvalidEditorSkippedWithWarning(t *testing.T) {
	skipUnlessKVMAvailable(t)
	// TODO(Phase 9 full execution).
	// See specs/028-kenaz-vm-integration/spec.md §US3 SC-3.
}

// ---------------------------------------------------------------------------
// User Story 4 — Disk image management (P3)
// ---------------------------------------------------------------------------

// TestUS4_ImageUpdateVerifiesChecksum verifies that `sigilctl vm image update`
// downloads and SHA-256 verifies the new image before activating it.
func TestUS4_ImageUpdateVerifiesChecksum(t *testing.T) {
	skipUnlessKVMAvailable(t)
	// TODO(Phase 9 full execution).
	// See specs/028-kenaz-vm-integration/spec.md §US4 SC-1.
}

// TestUS4_MissingImageReturnsError verifies that VMStart with no base image
// present returns ERR_IMAGE_MISSING with the download command.
func TestUS4_MissingImageReturnsError(t *testing.T) {
	skipUnlessKVMAvailable(t)
	// TODO(Phase 9 full execution): point sigild at an empty image dir, call
	// VMStart, assert ERR_IMAGE_MISSING in response.
	// See specs/028-kenaz-vm-integration/spec.md §US4 SC-2, spec 017 error table.
}

// ---------------------------------------------------------------------------
// Success Criteria SC-001 through SC-014
// ---------------------------------------------------------------------------

// TestSC001_BootReady60s verifies that the VM reaches VMLifecycleState.ready
// within 60 seconds in ≥95% of launches on reference hardware.
//
// Human QA equivalent: run 20 launch attempts, assert ≥19 reach ready < 60s.
func TestSC001_BootReady60s(t *testing.T) {
	skipUnlessKVMAvailable(t)
	// TODO(Phase 9 full execution): requires KVM runner + sigil-os image.
	// See specs/017-vm-sandbox-lifecycle/spec.md §SC-001.
}

// TestSC002a_VMProcessTerminates30s verifies that the VM process terminates
// within 30 seconds of VMStop in ≥99% of clean shutdown scenarios.
func TestSC002a_VMProcessTerminates30s(t *testing.T) {
	skipUnlessKVMAvailable(t)
	// TODO(Phase 9 full execution).
	// See specs/017-vm-sandbox-lifecycle/spec.md §SC-002a.
}

// TestSC002b_SessionFinalized90s verifies that full session finalization
// (merge complete + overlay discarded) completes within 90 seconds of VMStop.
func TestSC002b_SessionFinalized90s(t *testing.T) {
	skipUnlessKVMAvailable(t)
	// TODO(Phase 9 full execution).
	// See specs/017-vm-sandbox-lifecycle/spec.md §SC-002b.
}

// TestSC003_ZeroHostWritesOutsideScratch verifies that no host filesystem
// writes from inside the VM occur outside the virtio-fs scratch directory.
//
// Human QA equivalent: use inotifywait to monitor host FS during a VM session;
// assert zero events outside the session scratch path.
func TestSC003_ZeroHostWritesOutsideScratch(t *testing.T) {
	skipUnlessKVMAvailable(t)
	// TODO(Phase 9 full execution): requires inotifywait + known scratch path.
	// See specs/017-vm-sandbox-lifecycle/spec.md §SC-003.
}

// TestSC004_UncleanShutdownNeverUndefined verifies that sigild never enters
// an undefined state after any crash scenario.
//
// Human QA equivalent: crash at each step of the teardown sequence, restart,
// assert session has a terminal status and merge_log is consistent.
func TestSC004_UncleanShutdownNeverUndefined(t *testing.T) {
	skipUnlessKVMAvailable(t)
	// TODO(Phase 9 full execution): crash injection at steps 2, 3, 4, 5, 6.
	// See specs/017-vm-sandbox-lifecycle/spec.md §SC-004.
}

// TestSC005_StatusWithin1s verifies that sigilctl vm status returns accurate
// status within 1 second of any lifecycle state transition.
func TestSC005_StatusWithin1s(t *testing.T) {
	skipUnlessKVMAvailable(t)
	// TODO(Phase 9 full execution): measure p99 latency between state write
	// and sigilctl vm status round-trip.
	// See specs/017-vm-sandbox-lifecycle/spec.md §SC-005.
}

// TestSC006_SessionActiveRejected100ms verifies that ERR_SESSION_ACTIVE is
// returned within 100ms when a session is already active.
//
// Human QA equivalent: start a session, time a second VMStart call, assert
// response < 100ms wall clock.
func TestSC006_SessionActiveRejected100ms(t *testing.T) {
	skipUnlessKVMAvailable(t)
	// TODO(Phase 9 full execution).
	// See specs/017-vm-sandbox-lifecycle/spec.md §SC-006.
}

// TestSC007_MergeIdempotent verifies that repeated VMMerge calls for the
// same session produce zero duplicate rows in training_corpus.
//
// Human QA equivalent: call VMMerge 100 times for the same session,
// assert training_corpus row count is stable after the first call.
func TestSC007_MergeIdempotent(t *testing.T) {
	skipUnlessKVMAvailable(t)
	// TODO(Phase 9 full execution): see also unit TestMergeIdempotency in
	// internal/merge/ which covers this without a real VM.
	// See specs/019-vm-sqlite-merge/spec.md §SC-002.
}

// TestSC008_FilterDenylistExclusion100pct verifies that 100% of rows with
// payload strings matching the default denylist are excluded from training_corpus.
//
// Human QA equivalent: inject synthetic events with *.pem paths through the
// VM ledger; run merge; assert zero denylist rows in training_corpus.
func TestSC008_FilterDenylistExclusion100pct(t *testing.T) {
	skipUnlessKVMAvailable(t)
	// TODO(Phase 9 full execution): see also unit TestFilterDenylist in
	// internal/merge/ which covers this without a real VM.
	// See specs/019-vm-sqlite-merge/spec.md §SC-004.
}

// TestSC009a_LinuxDriverStartStop verifies the Linux QEMU driver lifecycle:
// StartVM, health probe, StopVM — all against a real KVM instance.
func TestSC009a_LinuxDriverStartStop(t *testing.T) {
	skipUnlessKVMAvailable(t)
	// TODO(Phase 9 full execution): delegates to vmdriver_linux_integration_test.go
	// TestLinuxDriverIntegration_FullLifecycle; this SC wrapper ensures
	// the acceptance criterion is exercised as part of the spec 028 suite.
	// See specs/028-kenaz-vm-integration/spec.md §SC-009a.
}

// TestSC009b_IntelMacGracefulDegrade verifies SC-009b: running sigild on an
// Intel Mac returns ERR_IMAGE_MISSING rather than attempting to boot an ARM64
// image. This is the unit-level gate; see vmdriver_darwin_intel_test.go.
//
// Phase 4b BLOCKED — this test skips until sigild-vz is extracted.
func TestSC009b_IntelMacGracefulDegrade(t *testing.T) {
	skipUnlessVZAvailable(t)
	// TODO(Phase 4b): covered by TestIntelGracefulDegrade in
	// internal/vmdriver/vmdriver_darwin_intel_test.go.
	// See specs/028-kenaz-vm-integration/spec.md §SC-009b.
}

// TestSC010_PrivacyFilterProperty verifies the FR-019 property test: 4
// pattern classes are never present in vm-events output.
//
// Human QA equivalent: run the property-based test in kenazproto/
// and confirm all 4 classes pass.
func TestSC010_PrivacyFilterProperty(t *testing.T) {
	skipUnlessKVMAvailable(t)
	// TODO(Phase 9 full execution): the unit-level property test in
	// internal/kenazproto/ covers this. This SC wrapper runs the same
	// assertions against events produced by a real VM session.
	// See specs/028-kenaz-vm-integration/spec.md §FR-019.
}

// TestSC011_VMEventsTopicFiltering verifies that the vm-events push topic
// correctly filters events by vm_id and closes the stream on
// vm.session_terminal sentinel.
func TestSC011_VMEventsTopicFiltering(t *testing.T) {
	skipUnlessKVMAvailable(t)
	// TODO(Phase 9 full execution): subscribe to vm-events, boot a VM,
	// assert vm_id filter works, stop VM, assert stream closes on sentinel.
	// See specs/028-kenaz-vm-integration/spec.md §vm-events topic.
}

// TestSC012_MetricsFR022 verifies that FR-022 metrics are emitted and
// have correct values: vm_sessions_active, vm_merge_duration_seconds,
// vm_events_per_sec, topic_drops_total.
func TestSC012_MetricsFR022(t *testing.T) {
	skipUnlessKVMAvailable(t)
	// TODO(Phase 9 full execution): boot VM, run merge, scrape metrics
	// endpoint, assert all four gauges/counters are non-zero and plausible.
	// See specs/028-kenaz-vm-integration/spec.md §FR-022.
}

// TestSC013_LauncherProfileRoundTrip verifies the FR-013a round-trip:
// a LauncherProfile written by the launcher is read back by sigild with
// all fields intact.
func TestSC013_LauncherProfileRoundTrip(t *testing.T) {
	// This SC has a unit-level gate in internal/launcherprofile/.
	// The integration test would verify end-to-end through the socket API.
	skipUnlessKVMAvailable(t)
	// TODO(Phase 9 full execution): write settings.json, call VMStart,
	// query LauncherProfile via socket, assert field equality.
	// See specs/028-kenaz-vm-integration/spec.md §FR-013a.
}

// TestSC014_ProtocolVersion3 verifies that connected clients negotiate
// protocol version 3 and that version-2 clients receive an appropriate
// downgrade or rejection response.
func TestSC014_ProtocolVersion3(t *testing.T) {
	skipUnlessKVMAvailable(t)
	// TODO(Phase 9 full execution): connect with a v2 header, assert
	// the response indicates version mismatch or negotiated version.
	// See specs/028-kenaz-vm-integration/spec.md §protocol version.
}
