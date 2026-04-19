//go:build darwin

package launcherprofile

import (
	"os"
	"path/filepath"
)

// settingsPath returns the macOS path to the LauncherProfile settings file.
//
// Matches LauncherProfile.settingsURL in
// sigil-launcher-macos/SigilLauncher/Models/LauncherProfile.swift:
//
//	FileManager.default.homeDirectoryForCurrentUser
//	    .appendingPathComponent(".sigil/launcher/settings.json")
func settingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".sigil", "launcher", "settings.json"), nil
}
