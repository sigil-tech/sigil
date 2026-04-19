package vm

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEvaluatePolicyStatus(t *testing.T) {
	tests := []struct {
		name       string
		policyID   string
		egressTier string
		policyTier string
		denyList   []string
		want       PolicyStatus
	}{
		{
			name:     "empty policyID is not_applicable",
			policyID: "",
			want:     PolicyStatusNotApplicable,
		},
		{
			name:       "policyID in denylist is denied",
			policyID:   "analyst-read-only",
			egressTier: "restricted",
			policyTier: "",
			denyList:   []string{"analyst-read-only", "pii-handler"},
			want:       PolicyStatusDenied,
		},
		{
			name:       "second entry in denylist is denied",
			policyID:   "pii-handler",
			egressTier: "restricted",
			policyTier: "",
			denyList:   []string{"analyst-read-only", "pii-handler"},
			want:       PolicyStatusDenied,
		},
		{
			name:       "allowlist policyTier with none egressTier is denied",
			policyID:   "underwriter-tier-2",
			egressTier: "none",
			policyTier: "allowlist",
			denyList:   nil,
			want:       PolicyStatusDenied,
		},
		{
			name:       "allowlist policyTier with restricted egressTier is ok",
			policyID:   "underwriter-tier-2",
			egressTier: "restricted",
			policyTier: "allowlist",
			denyList:   nil,
			want:       PolicyStatusOK,
		},
		{
			name:       "allowlist policyTier with allowlist egressTier is ok",
			policyID:   "underwriter-tier-2",
			egressTier: "allowlist",
			policyTier: "allowlist",
			denyList:   nil,
			want:       PolicyStatusOK,
		},
		{
			name:       "valid policyID not in denylist is ok",
			policyID:   "sandbox-default",
			egressTier: "restricted",
			policyTier: "",
			denyList:   []string{"analyst-read-only"},
			want:       PolicyStatusOK,
		},
		{
			name:       "valid policyID with empty denylist is ok",
			policyID:   "sandbox-default",
			egressTier: "vault-only",
			policyTier: "",
			denyList:   nil,
			want:       PolicyStatusOK,
		},
		{
			name:       "non-allowlist policyTier with none egressTier is ok",
			policyID:   "sandbox-default",
			egressTier: "none",
			policyTier: "restricted",
			denyList:   nil,
			want:       PolicyStatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := evaluatePolicyStatus(tt.policyID, tt.egressTier, tt.policyTier, tt.denyList)
			require.Equal(t, tt.want, got)
		})
	}
}
