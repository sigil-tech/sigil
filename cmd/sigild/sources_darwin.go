//go:build darwin

package main

import (
	"log/slog"

	"github.com/sigil-tech/sigil/internal/collector"
	"github.com/sigil-tech/sigil/internal/collector/sources"
)

// addPlatformSources registers macOS-only collector sources.
// cfg provides per-source enable/disable and poll interval config.
func addPlatformSources(col *collector.Collector, log *slog.Logger) {
	col.Add(&sources.DarwinFocusSource{})
	col.Add(sources.NewAppStateSource(log))
	col.Add(&sources.ClipboardSource{})

	// Spec 023: Knowledge worker signals.
	// Poll intervals are configurable via [sources.*] config sections.
	// When not set, they use the frequency preset (high/medium/low).
	col.Add(sources.NewIdleSource(0, 0))
	col.Add(sources.NewPowerSource())
	col.Add(sources.NewScreenshotSource())
	col.Add(sources.NewDownloadSource(""))

	// Spec 024: Browser context enrichment.
	bs := sources.NewBrowserSource(0, nil)
	bs.ReadActiveTab = sources.ReadActiveTabDarwin
	col.Add(bs)
}
