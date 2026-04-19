package vm

// evaluatePolicyStatus derives the static per-session policy verdict from the
// inputs available at VMStart time. It is a pure function with no I/O and no
// dependency on sigild runtime state beyond its arguments. See ADR-028c §Decision.
//
// Evaluation order:
//  1. not_applicable — policyID is empty (free-form session, no policy governance).
//  2. denied — policyID is in the denyList snapshot, OR egressTier is
//     incompatible with policyTier (vault-only egress when policy requires allowlist).
//  3. pending — reserved for post-MVP approval workflows; unreachable in MVP.
//  4. ok — all other cases.
//
// The denyList is the merge filter's configured snapshot at VMStart time; the
// same version is recorded in sessions.filter_version. A subsequently updated
// denylist does not retroactively change an existing session's PolicyStatus.
func evaluatePolicyStatus(policyID, egressTier, policyTier string, denyList []string) PolicyStatus {
	if policyID == "" {
		return PolicyStatusNotApplicable
	}

	for _, denied := range denyList {
		if denied == policyID {
			return PolicyStatusDenied
		}
	}

	// Egress-tier incompatibility: a policy that declares "allowlist" tier
	// requires egress to be restricted to an allowlist. If the session requests
	// "none" egress (unrestricted outbound), the policy's requirements are
	// violated.
	if policyTier == "allowlist" && egressTier == "none" {
		return PolicyStatusDenied
	}

	return PolicyStatusOK
}
