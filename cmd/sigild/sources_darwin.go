//go:build darwin

package main

import (
	"log/slog"

	"github.com/sigil-tech/sigil/internal/collector"
	"github.com/sigil-tech/sigil/internal/collector/sources"
	"github.com/sigil-tech/sigil/internal/config"
)

// addPlatformSources registers macOS-only collector sources.
// srcs provides per-source enable/disable and poll interval config.
func addPlatformSources(col *collector.Collector, log *slog.Logger, srcs config.SourcesConfig) {
	col.Add(&sources.DarwinFocusSource{})
	col.Add(sources.NewAppStateSource(log))

	if srcs.Clipboard.IsEnabled(true) {
		col.Add(&sources.ClipboardSource{})
	}

	// Spec 023: Knowledge worker signals.
	// Poll intervals are configurable via [sources.*] config sections.
	// When not set, they use the frequency preset (high/medium/low).
	if srcs.Idle.IsEnabled(true) {
		col.Add(sources.NewIdleSource(0, 0))
	}
	if srcs.Power.IsEnabled(true) {
		col.Add(sources.NewPowerSource())
	}
	if srcs.Screenshot.IsEnabled(true) {
		col.Add(sources.NewScreenshotSource())
	}
	if srcs.Download.IsEnabled(true) {
		col.Add(sources.NewDownloadSource(""))
	}

	// Spec 024: Browser context enrichment.
	if srcs.Browser.IsEnabled(true) {
		bs := sources.NewBrowserSource(0, nil)
		bs.ReadActiveTab = sources.ReadActiveTabDarwin
		col.Add(bs)
	}
}
