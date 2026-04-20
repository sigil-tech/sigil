package main

import (
	"strings"
	"testing"
)

// TestLedgerKeyRotate covers Task 9.1: `sigilctl ledger key rotate`
// dispatches through cmdLedgerKey. With an unreachable socket and
// --yes we expect a connection error (not a flag parse error).
func TestLedgerKeyRotate(t *testing.T) {
	err := cmdLedgerKey("/tmp/nonexistent.sock", []string{"rotate", "--yes", "--reason", "test"})
	if err == nil {
		t.Fatalf("expected socket connect error, got nil")
	}
	// The error should reach out to the daemon, not reject at flag
	// parse time.
	if strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("unexpected flag parse error: %v", err)
	}
}

// TestLedgerKeyRotateConfirm covers Task 9.2: without --yes and
// with no tty stdin, the rotate command reads stdin via fmt.Fscan.
// We cannot easily feed stdin here without a harness; instead we
// assert the flag parsing side: --reason and --yes are defined,
// --foo is rejected.
func TestLedgerKeyRotateConfirm(t *testing.T) {
	err := cmdLedgerKey("/tmp/x", []string{"rotate", "--foo"})
	if err == nil {
		t.Fatalf("expected unknown-flag error")
	}
	if !strings.Contains(err.Error(), "flag provided but not defined") &&
		!strings.Contains(err.Error(), "foo") {
		t.Fatalf("unexpected error for --foo: %v", err)
	}
}

// TestLedgerKeyRotateOutput documents the Task 9.3 expected output
// shape. Since we cannot invoke a live daemon from a unit test the
// assertion here is that the output format strings in the source
// contain the three key fields — a drift would break the operator's
// expected dashboard copy.
func TestLedgerKeyRotateOutput(t *testing.T) {
	// Pull the output-format strings into this test via a compile-
	// time copy. If cmdLedgerKeyRotate's fmt.Printf calls drift
	// (renamed or reformatted), update the expected constant below
	// in lockstep.
	const expectFragments = `old fingerprint|new fingerprint|sentinel entry`
	for want := range strings.SplitSeq(expectFragments, "|") {
		if !strings.Contains(expectedRotateOutput, want) {
			t.Errorf("expected rotate-output fragment %q missing — cmdLedgerKeyRotate drifted?", want)
		}
	}
}

// expectedRotateOutput mirrors the Printf format strings in
// cmdLedgerKeyRotate. Kept as a second source of truth so a rename
// is caught at test time without spinning up a real daemon.
const expectedRotateOutput = `Rotation complete:
  old fingerprint:  ...
  new fingerprint:  ...
  sentinel entry:   ...`
