package vm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVMErrorConstants verifies the error code sentinel strings match the
// spec-017 error table (Amendment A) and that the new ErrHypervisorUnavailable
// constant is present and distinct from the pre-existing three.
func TestVMErrorConstants(t *testing.T) {
	require.Equal(t, "ERR_SESSION_ACTIVE", ErrSessionActive)
	require.Equal(t, "ERR_IMAGE_MISSING", ErrImageMissing)
	require.Equal(t, "ERR_SESSION_NOT_FOUND", ErrSessionNotFound)
	require.Equal(t, "ERR_HYPERVISOR_UNAVAILABLE", ErrHypervisorUnavailable)

	// All four must be distinct.
	codes := []string{ErrSessionActive, ErrImageMissing, ErrSessionNotFound, ErrHypervisorUnavailable}
	seen := make(map[string]struct{}, len(codes))
	for _, c := range codes {
		_, dup := seen[c]
		require.False(t, dup, "duplicate error code: %s", c)
		seen[c] = struct{}{}
	}
}

// TestPolicyStatusConstants verifies the four PolicyStatus constant values
// match the spec-028 data-model §2.6.
func TestPolicyStatusConstants(t *testing.T) {
	require.Equal(t, PolicyStatus("ok"), PolicyStatusOK)
	require.Equal(t, PolicyStatus("pending"), PolicyStatusPending)
	require.Equal(t, PolicyStatus("denied"), PolicyStatusDenied)
	require.Equal(t, PolicyStatus("not_applicable"), PolicyStatusNotApplicable)
}

// TestManagerStartWithSpec_NilDriver verifies that StartWithSpec inserts a
// session and correctly persists the evaluated policy_status when no driver is
// wired.
func TestManagerStartWithSpec_NilDriver(t *testing.T) {
	db := testDB(t)
	mgr := NewManager(db, nil, nil)
	ctx := context.Background()

	spec := StartSpec{
		Name:        "test-session",
		PolicyID:    "sandbox-default",
		EgressTier:  "restricted",
		ImagePath:   "/images/base.qcow2",
		OverlayPath: "/tmp/overlay.qcow2",
	}

	id, err := mgr.StartWithSpec(ctx, spec, nil)
	require.NoError(t, err)
	require.NotEmpty(t, string(id))

	// Verify the session is persisted and the policy_status is ok.
	sess, err := mgr.Status(ctx, string(id))
	require.NoError(t, err)
	assert.Equal(t, StateBooting, sess.Status)
	assert.Equal(t, "ok", sess.PolicyStatus)
}

// TestManagerStartWithSpec_PolicyDenied verifies that a policyID on the
// denylist results in PolicyStatusDenied being persisted.
func TestManagerStartWithSpec_PolicyDenied(t *testing.T) {
	db := testDB(t)
	mgr := NewManager(db, nil, nil)
	ctx := context.Background()

	spec := StartSpec{
		Name:     "denied-session",
		PolicyID: "revoked-policy",
	}
	denyList := []string{"revoked-policy"}

	id, err := mgr.StartWithSpec(ctx, spec, denyList)
	require.NoError(t, err)

	sess, err := mgr.Status(ctx, string(id))
	require.NoError(t, err)
	assert.Equal(t, "denied", sess.PolicyStatus)
}

// TestManagerStartWithSpec_NotApplicable verifies that an empty policyID
// results in PolicyStatusNotApplicable being persisted.
func TestManagerStartWithSpec_NotApplicable(t *testing.T) {
	db := testDB(t)
	mgr := NewManager(db, nil, nil)
	ctx := context.Background()

	spec := StartSpec{
		Name:      "no-policy-session",
		PolicyID:  "",
		ImagePath: "/images/base.qcow2",
	}

	id, err := mgr.StartWithSpec(ctx, spec, nil)
	require.NoError(t, err)

	sess, err := mgr.Status(ctx, string(id))
	require.NoError(t, err)
	assert.Equal(t, "not_applicable", sess.PolicyStatus)
}

// TestManagerStartWithSpec_SingleVMConstraint verifies the single-VM
// constraint is enforced for StartWithSpec, matching the existing behaviour
// of Start.
func TestManagerStartWithSpec_SingleVMConstraint(t *testing.T) {
	db := testDB(t)
	mgr := NewManager(db, nil, nil)
	ctx := context.Background()

	_, err := mgr.StartWithSpec(ctx, StartSpec{Name: "first"}, nil)
	require.NoError(t, err)

	_, err = mgr.StartWithSpec(ctx, StartSpec{Name: "second"}, nil)
	require.Error(t, err)
	var vmErr *VMError
	require.ErrorAs(t, err, &vmErr)
	assert.Equal(t, ErrSessionActive, vmErr.Code)
}

// TestNewManagerWithoutDriver_Alias verifies that NewManagerWithoutDriver
// returns a functional Manager and is consistent with NewManager(db, nil, nil).
func TestNewManagerWithoutDriver_Alias(t *testing.T) {
	db := testDB(t)
	mgr := NewManagerWithoutDriver(db)
	ctx := context.Background()

	// Must be able to start a session — the alias must wire a valid manager.
	sess, err := mgr.Start(ctx, StartRequest{DiskImagePath: "/images/base.qcow2"})
	require.NoError(t, err)
	require.NotEmpty(t, sess.ID)
}
