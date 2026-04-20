package payload

import (
	"reflect"
	"strings"
	"testing"
)

// denylist is the set of substrings that must never appear in a
// ledger payload field name. FR-032 / FR-033 / spec 029 SC-007 all
// enforce that raw secret material never lands in the audit ledger;
// this test is the drift gate that catches a regression where a
// future emitter accidentally drops a token or password field into
// an exported struct here.
//
// The list is deliberately conservative — the cost of a false
// positive is one extra test update; the cost of a false negative is
// a privacy breach. Entries are lowercase, matched against lowercased
// field names via strings.Contains.
var denylist = []string{
	"password",
	"passwd",
	"secret",
	"token",
	"apikey",
	"api_key",
	"private",
	"privatekey",
	"privkey",
	"credential",
	"cred",
	"cookie",
	"bearer",
	"authorization",
	"session_cookie",
}

// allowedSessionSuffixes lists field-name suffixes that are allowed
// to contain otherwise-denylisted substrings because they refer to
// non-sensitive attributes. Example: `session_id` contains "session",
// which is fine; `session_cookie` is not.
var allowedSessionSuffixes = []string{
	"_id",       // session_id, task_id, vm_session_id
	"_count",    // session_count
	"_duration", // session_duration
	"_start",    // session_start
	"_end",      // session_end
}

// TestPayloadAllowlist walks every exported struct declared in this
// package via reflect and asserts that no JSON-tagged field name
// contains a denylist substring (modulo the allow-suffix exceptions).
// Because reflect cannot enumerate package-level types without help,
// we register each struct in testAllPayloadStructs; a new struct
// that a developer forgets to register will compile fine but skip
// this gate — the _test.go next to each file (vm_test.go,
// merge_test.go, etc.) is the second line of defense, so a drop is
// caught there even if this list misses it.
func TestPayloadAllowlist(t *testing.T) {
	for _, zero := range testAllPayloadStructs() {
		typ := reflect.TypeOf(zero)
		t.Run(typ.Name(), func(t *testing.T) {
			for i := 0; i < typ.NumField(); i++ {
				f := typ.Field(i)
				jsonTag := strings.SplitN(f.Tag.Get("json"), ",", 2)[0]
				if jsonTag == "" {
					t.Fatalf("field %s.%s has no json tag — all payload fields MUST carry one", typ.Name(), f.Name)
				}
				lower := strings.ToLower(jsonTag)
				for _, bad := range denylist {
					if !strings.Contains(lower, bad) {
						continue
					}
					if isAllowedException(lower, bad) {
						continue
					}
					t.Errorf("field %s.%s (json=%q) contains denylisted substring %q — payload fields must never carry secret material (FR-032)", typ.Name(), f.Name, jsonTag, bad)
				}
			}
		})
	}
}

// isAllowedException returns true iff a matched denylist substring
// is part of a known non-sensitive pattern. Currently only the
// "session" substring has exceptions (session_id, session_start,
// etc.); every other denylist entry is an absolute block.
func isAllowedException(_, matched string) bool {
	if matched != "session_cookie" && matched == "cookie" {
		return false
	}
	// The "session_" false-positive family is handled by the denylist
	// itself — we do not flag bare "session" (not in denylist), and
	// "session_cookie" IS in denylist, so any other "session_*" field
	// is implicitly allowed by not matching any entry. No further
	// exception logic is needed here; reserved for future patterns.
	_ = allowedSessionSuffixes
	return false
}

// testAllPayloadStructs is the registry of every exported payload
// struct in this package. Each new struct MUST be added here so the
// allowlist test exercises it. Returns zero values, not pointers —
// reflect.TypeOf handles either shape but values compose more
// naturally in the iteration.
//
// Keep entries sorted by file name, then by struct name within a file,
// so diffs remain legible as the package grows across Phase 5 sub-
// phases.
func testAllPayloadStructs() []any {
	return []any{
		// vm.go
		VMSpawnPayload{},
		VMTeardownPayload{},
		// merge.go
		MergeFilterPayload{},
		ModelMergePayload{},
		PolicyDenyVMBatchPayload{},
		// training.go
		TrainingTunePayload{},
	}
}
