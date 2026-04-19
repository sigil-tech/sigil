//go:build linux

package launcherprofile

import (
	"os"
	"path/filepath"
)

// settingsPath returns the Linux path to the LauncherProfile settings file.
//
// Follows the XDG Base Directory Specification:
//   - $XDG_CONFIG_HOME/sigil-launcher/settings.json  (when XDG_CONFIG_HOME is set)
//   - ~/.config/sigil-launcher/settings.json          (fallback)
func settingsPath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "sigil-launcher", "settings.json"), nil
}
