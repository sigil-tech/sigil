package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// detectEnvironment probes the local filesystem for installed IDEs and dev tools.
func detectEnvironment() DetectedEnvironment {
	var env DetectedEnvironment

	// Detect IDEs.
	if hasVSCode() {
		env.IDEs = append(env.IDEs, "vscode")
		env.Plugins = append(env.Plugins, "vscode")
	}
	if hasJetBrains() {
		env.IDEs = append(env.IDEs, "jetbrains")
		env.Plugins = append(env.Plugins, "jetbrains")
	}

	// Detect dev tools.
	for _, tool := range []string{"git", "docker", "node", "python3", "go", "cargo"} {
		if _, err := exec.LookPath(tool); err == nil {
			env.Tools = append(env.Tools, tool)
		}
	}

	return env
}

// hasVSCode checks if VS Code is installed.
func hasVSCode() bool {
	if _, err := exec.LookPath("code"); err == nil {
		return true
	}

	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		if _, err := os.Stat("/Applications/Visual Studio Code.app"); err == nil {
			return true
		}
	case "linux":
		if _, err := os.Stat(filepath.Join(home, ".vscode")); err == nil {
			return true
		}
	case "windows":
		localApp := os.Getenv("LOCALAPPDATA")
		if localApp != "" {
			if _, err := os.Stat(filepath.Join(localApp, "Programs", "Microsoft VS Code")); err == nil {
				return true
			}
		}
	}
	return false
}

// hasJetBrains checks if any JetBrains IDE is installed.
func hasJetBrains() bool {
	home, _ := os.UserHomeDir()

	switch runtime.GOOS {
	case "darwin":
		for _, app := range []string{
			"IntelliJ IDEA.app", "IntelliJ IDEA CE.app",
			"GoLand.app", "PyCharm.app", "PyCharm CE.app",
			"WebStorm.app", "CLion.app", "Rider.app",
		} {
			if _, err := os.Stat(filepath.Join("/Applications", app)); err == nil {
				return true
			}
		}
	case "linux":
		for _, dir := range []string{".local/share/JetBrains", ".config/JetBrains"} {
			if _, err := os.Stat(filepath.Join(home, dir)); err == nil {
				return true
			}
		}
	case "windows":
		localApp := os.Getenv("LOCALAPPDATA")
		if localApp != "" {
			if _, err := os.Stat(filepath.Join(localApp, "JetBrains")); err == nil {
				return true
			}
		}
	}
	return false
}
