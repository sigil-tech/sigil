//go:build darwin

package main

import (
	"log/slog"

	"github.com/sigil-tech/sigil/internal/collector"
	"github.com/sigil-tech/sigil/internal/collector/sources"
)

// addPlatformSources registers macOS-only collector sources.
func addPlatformSources(col *collector.Collector, log *slog.Logger) {
	col.Add(&sources.DarwinFocusSource{})
	col.Add(sources.NewAppStateSource(log))
	col.Add(&sources.ClipboardSource{})

	// Spec 023: Knowledge worker signals.
	col.Add(sources.NewIdleSource(0))        // default 5m threshold
	col.Add(sources.NewPowerSource())         // battery/AC
	col.Add(sources.NewScreenshotSource())    // screenshot detection
	col.Add(sources.NewDownloadSource(""))    // ~/Downloads

	// Spec 024: Browser context enrichment.
	bs := sources.NewBrowserSource(0, nil)
	bs.ReadActiveTab = sources.ReadActiveTabDarwin
	col.Add(bs)
}
