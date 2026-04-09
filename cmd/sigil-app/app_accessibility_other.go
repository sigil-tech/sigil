//go:build !darwin

package main

// CheckAccessibility is a no-op on non-macOS platforms.
func (a *App) CheckAccessibility() bool { return true }

// PromptAccessibility is a no-op on non-macOS platforms.
func (a *App) PromptAccessibility() {}
