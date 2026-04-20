package payload

import "testing"

func TestPolicyDenyPayloadAllowlist(t *testing.T) {
	assertMarshalledKeys(t, PolicyDenyPayload{
		Rule:     "exec.sandbox.shell",
		Action:   "vm.spawn",
		Reason:   "sandbox policy blocks interactive shells",
		DeniedAt: "2026-04-19T12:00:00Z",
	}, []string{"rule", "action", "reason", "denied_at"})
}
