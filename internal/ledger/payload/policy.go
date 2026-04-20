package payload

// PolicyDenyPayload is written to the ledger each time the centralised
// policy.Denier refuses an action per FR-006. Every deny site across
// the daemon routes through policy.Deny(ctx, rule, action, reason),
// which emits exactly one ledger row per decision — this payload is
// its wire shape.
//
// Fields:
//
//   - Rule       short machine-readable name of the policy rule
//     that fired. Vocabulary is centrally maintained; freeform
//     strings are a drift signal a future CI check will flag.
//   - Action     narrow vocabulary of actions the caller is
//     refusing ("merge.row", "vm.spawn", "sudo", etc.).
//   - Reason     short human-readable justification. Freeform but
//     SHOULD fit in 128 chars so the Audit Viewer can surface it
//     without truncation.
//   - DeniedAt   RFC 3339 UTC timestamp of the deny decision.
type PolicyDenyPayload struct {
	Rule     string `json:"rule"`
	Action   string `json:"action"`
	Reason   string `json:"reason"`
	DeniedAt string `json:"denied_at"`
}
