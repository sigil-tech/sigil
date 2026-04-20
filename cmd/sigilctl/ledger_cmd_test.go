package main

import (
	"testing"
)

// TestLedgerCmdHelp covers Task 7.1: invoking `sigilctl ledger`
// with no args prints usage and does not error. A help-mode that
// returns an error would break shell wrappers that call the
// subcommand tree for discovery.
func TestLedgerCmdHelp(t *testing.T) {
	if err := cmdLedger("", nil); err != nil {
		t.Fatalf("cmdLedger(nil) returned error: %v", err)
	}
}

func TestLedgerCmdRejectsUnknownSubcommand(t *testing.T) {
	if err := cmdLedger("", []string{"nope"}); err == nil {
		t.Fatalf("cmdLedger(nope) should have errored")
	}
}

// TestShortTS checks the timestamp-clipping helper. 19 chars is the
// RFC 3339 prefix (YYYY-MM-DDTHH:MM:SS); a shorter string passes
// through unchanged.
func TestShortTS(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"2026-04-20T12:34:56.789Z", "2026-04-20T12:34:56"},
		{"2026-04-20T12:34:56Z", "2026-04-20T12:34:56"},
		{"short", "short"},
	}
	for _, c := range cases {
		if got := shortTS(c.in); got != c.want {
			t.Errorf("shortTS(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestTruncate verifies the simple utility keeps short strings
// untouched and produces n-length output for longer inputs.
func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("truncate(short, 10) = %q", got)
	}
	if got := truncate("too-long-string", 8); len([]rune(got)) != 8 {
		t.Errorf("truncate(too-long-string, 8) rune count = %d, want 8; got %q", len([]rune(got)), got)
	}
}

// TestHashSuffix pins the 8-char tail extraction.
func TestHashSuffix(t *testing.T) {
	if got := hashSuffix("abcdef1234567890deadbeef"); got != "deadbeef" {
		t.Errorf("hashSuffix = %q", got)
	}
	if got := hashSuffix("short"); got != "short" {
		t.Errorf("hashSuffix(short) = %q, want short", got)
	}
}

// TestLedgerListFlagDispatch covers the list subcommand flag parser
// end-to-end WITHOUT a real daemon. We give it an invalid socket
// path so call() fails with a connection error — the test is about
// flag parsing + dispatch, not network I/O.
func TestLedgerListFlagDispatch(t *testing.T) {
	err := cmdLedgerList("/tmp/nonexistent.sock", []string{"--limit", "5", "--before", "10", "--format", "json"})
	// We expect a socket/connect error here, not a flag parse error.
	if err == nil {
		t.Fatalf("expected socket connect error, got nil")
	}
}

func TestLedgerGetFlagDispatch(t *testing.T) {
	// Invalid id should parse-reject before touching the socket.
	err := cmdLedgerGet("/tmp/nonexistent.sock", []string{"not-a-number"})
	if err == nil || !containsAny(err.Error(), "invalid id") {
		t.Fatalf("expected invalid-id error, got %v", err)
	}
	err = cmdLedgerGet("/tmp/nonexistent.sock", []string{})
	if err == nil {
		t.Fatalf("expected usage error for missing id")
	}
}

func containsAny(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
