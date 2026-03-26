//go:build !windows

package main

import (
	"log/slog"

	"github.com/wambozi/sigil/internal/collector"
)

func addPlatformSources(_ *collector.Collector, _ *slog.Logger) {
	// No platform-specific sources on non-Windows systems (yet).
	// macOS and Linux sources (Hyprland, etc.) are registered directly in main.go.
}
