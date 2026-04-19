//go:build linux

package launcherprofile

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRead_ErrProfileMissing verifies that Read returns ErrProfileMissing when
// the settings file does not exist on Linux.
func TestRead_ErrProfileMissing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "nonexistent-xdg"))

	_, err := Read()
	require.Error(t, err)
	require.ErrorIs(t, err, ErrProfileMissing, "missing file must wrap ErrProfileMissing")
}

// TestRead_ParseError verifies that Read returns a wrapped parse error for
// malformed JSON on Linux.
func TestRead_ParseError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	settingsDir := filepath.Join(dir, "sigil-launcher")
	require.NoError(t, os.MkdirAll(settingsDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(settingsDir, "settings.json"), []byte("{bad json"), 0o600))

	_, err := Read()
	require.Error(t, err)
	require.Contains(t, err.Error(), "launcherprofile: parse")
}

// TestRead_Success verifies that Read returns a correctly populated Profile
// when a valid settings.json is present on Linux.
func TestRead_Success(t *testing.T) {
	raw, err := os.ReadFile(fixtureFile)
	require.NoError(t, err)

	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	settingsDir := filepath.Join(dir, "sigil-launcher")
	require.NoError(t, os.MkdirAll(settingsDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(settingsDir, "settings.json"), raw, 0o600))

	p, err := Read()
	require.NoError(t, err)
	require.Equal(t, uint64(4294967296), p.MemorySize)
	require.Equal(t, "vscode", p.Editor)
}
