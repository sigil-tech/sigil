//go:build darwin

package main

import (
	"github.com/sigil-tech/sigil/internal/collector"
	"github.com/sigil-tech/sigil/internal/collector/sources"
)

// addPlatformSources registers macOS-specific collector sources.
func addPlatformSources(col *collector.Collector) {
	col.Add(&sources.DarwinFocusSource{})
}
