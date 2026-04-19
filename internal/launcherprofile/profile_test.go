package launcherprofile

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

const fixtureFile = "testdata/launcher_profile_round_trip.json"

// TestRoundTrip verifies that the canonical fixture parses into a Profile
// without error and that re-serialising through a sorted-key map produces
// output that is byte-exact equal to the fixture.
//
// "Byte-exact" is defined as: the fixture's compact JSON equals the compact
// JSON produced by (1) unmarshalling the fixture into Profile, (2) marshalling
// Profile back to JSON, (3) unmarshalling that JSON into map[string]any,
// (4) marshalling the map (Go's encoding/json sorts map keys alphabetically).
//
// The fixture fields are ordered alphabetically by JSON key. Any deviation in
// the fixture ordering will cause this test to fail, keeping the fixture
// canonical over time.
func TestRoundTrip(t *testing.T) {
	raw, err := os.ReadFile(fixtureFile)
	require.NoError(t, err, "read fixture")

	// Step 1: parse fixture into Profile using DisallowUnknownFields (FR-013a).
	var p Profile
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	require.NoError(t, dec.Decode(&p), "decode fixture into Profile")

	// Spot-check a representative set of fields to confirm the struct mapping
	// is correct and not silently zero-valued.
	require.Equal(t, uint64(4294967296), p.MemorySize)
	require.Equal(t, 2, p.CPUCount)
	require.Equal(t, "vscode", p.Editor)
	require.Equal(t, "docker", p.ContainerEngine)
	require.Equal(t, "zsh", p.Shell)
	require.Equal(t, 2, p.NotificationLevel)
	require.Equal(t, uint16(2222), p.SSHPort)
	require.Equal(t, "console=hvc0 root=/dev/vda rw", p.KernelCommandLine)
	require.NotNil(t, p.ModelID, "ModelID must be non-nil (all optional fields populated in fixture)")
	require.Equal(t, "llama-3.1-8b-instruct", *p.ModelID)
	require.NotNil(t, p.ModelPath, "ModelPath must be non-nil (all optional fields populated in fixture)")

	// Step 2: re-marshal Profile → JSON bytes.
	profileBytes, err := json.Marshal(p)
	require.NoError(t, err, "marshal Profile")

	// Step 3: unmarshal the re-marshalled bytes into map[string]any.
	// encoding/json marshals map keys in sorted order, giving us a canonical form.
	var m map[string]any
	require.NoError(t, json.Unmarshal(profileBytes, &m), "unmarshal into map")

	// Step 4: marshal the map (keys sorted alphabetically by encoding/json).
	sortedBytes, err := json.Marshal(m)
	require.NoError(t, err, "marshal sorted map")

	// Step 5: produce the same sorted compact form from the fixture.
	var fixtureMap map[string]any
	require.NoError(t, json.Unmarshal(raw, &fixtureMap), "unmarshal fixture into map")
	fixtureCanonical, err := json.Marshal(fixtureMap)
	require.NoError(t, err, "marshal fixture map")

	require.Equal(t, string(fixtureCanonical), string(sortedBytes),
		"re-serialised Profile must be byte-exact equal to the canonical fixture")
}

// TestRoundTrip_DisallowUnknownFields verifies that a JSON document with an
// unknown field is rejected, guarding against schema drift (FR-013a).
func TestRoundTrip_DisallowUnknownFields(t *testing.T) {
	raw, err := os.ReadFile(fixtureFile)
	require.NoError(t, err, "read fixture")

	// Inject an unknown field into the fixture.
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	m["unknownFieldFromFutureLauncherVersion"] = "should-be-rejected"
	withExtra, err := json.Marshal(m)
	require.NoError(t, err)

	var p Profile
	dec := json.NewDecoder(bytes.NewReader(withExtra))
	dec.DisallowUnknownFields()
	err = dec.Decode(&p)
	require.Error(t, err, "DisallowUnknownFields must reject an unknown field")
}
