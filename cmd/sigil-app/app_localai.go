package main

import (
	"fmt"
	"os/exec"
)

// StartLocalModel attempts to start the local AI server (ollama or llama-server).
// Returns nil on success — the health check will verify it's responding.
func (a *App) StartLocalModel() error {
	// Try ollama first (most common on macOS).
	if path, err := exec.LookPath("ollama"); err == nil {
		a.log.Info("starting ollama serve", "path", path)
		cmd := exec.Command(path, "serve")
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("start ollama: %w", err)
		}
		// Detach — don't wait.
		go cmd.Wait()
		return nil
	}

	// Fall back to llama-server.
	if path, err := exec.LookPath("llama-server"); err == nil {
		a.log.Info("starting llama-server", "path", path)
		cmd := exec.Command(path)
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("start llama-server: %w", err)
		}
		go cmd.Wait()
		return nil
	}

	return fmt.Errorf("no local AI server found (install ollama or llama-server)")
}
