package payload

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestVMPayloadAllowlist asserts that both VM payload shapes marshal
// to JSON with exactly the field names the spec 029 / FR-032 review
// agreed on. A change here (rename, add, remove) requires a matching
// spec / test update — the gate is deliberate.
func TestVMPayloadAllowlist(t *testing.T) {
	t.Run("VMSpawnPayload field set", func(t *testing.T) {
		want := []string{
			"session_id", "image_path", "policy_id", "egress_tier",
			"vsock_cid", "filter_version", "started_at",
		}
		assertMarshalledKeys(t, VMSpawnPayload{
			SessionID: "s1", ImagePath: "/p", PolicyID: "sandboxed-default",
			EgressTier: "deny", VsockCID: 42, FilterVersion: "abc", StartedAt: "2026-04-19T00:00:00Z",
		}, want)
	})

	t.Run("VMTeardownPayload field set", func(t *testing.T) {
		want := []string{
			"session_id", "outcome", "duration_seconds",
			"ledger_events_total", "policy_status", "ended_at",
		}
		assertMarshalledKeys(t, VMTeardownPayload{
			SessionID: "s1", Outcome: "stopped", DurationSeconds: 60,
			LedgerEventsTotal: 10, PolicyStatus: "ok", EndedAt: "2026-04-19T00:01:00Z",
		}, want)
	})
}

// assertMarshalledKeys marshals v to JSON, unmarshals into a
// map[string]any, and asserts the key set matches want exactly (no
// extras, no missing). Used by each payload struct's test to
// double-check the struct tag wiring.
func assertMarshalledKeys(t *testing.T, v any, want []string) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	gotSet := make(map[string]bool, len(decoded))
	for k := range decoded {
		gotSet[k] = true
	}
	wantSet := make(map[string]bool, len(want))
	for _, k := range want {
		wantSet[k] = true
	}
	for k := range gotSet {
		if !wantSet[k] {
			t.Errorf("unexpected field %q in JSON output (raw=%s)", k, raw)
		}
	}
	for k := range wantSet {
		if !gotSet[k] {
			t.Errorf("missing field %q in JSON output (raw=%s)", k, raw)
		}
	}
	// Defense-in-depth: scan the TOP-LEVEL JSON keys against the
	// denylist to catch a struct-tag drift that fooled the reflect-
	// based TestPayloadAllowlist. We only check gotSet (the keys), not
	// the full body — map values and nested arrays are data, not
	// field names.
	for k := range gotSet {
		for _, bad := range denylist {
			if strings.Contains(strings.ToLower(k), bad) {
				t.Errorf("denylisted substring %q found in JSON key %q", bad, k)
			}
		}
	}
}
