//go:build windows

package main

import (
	"log/slog"

	"github.com/sigil-tech/sigil/internal/collector"
	"github.com/sigil-tech/sigil/internal/collector/sources"
)

func addPlatformSources(col *collector.Collector, log *slog.Logger) {
	col.Add(sources.NewWindowsFocusSource(log))
	col.Add(sources.NewWindowsClipboardSource(log))
	col.Add(sources.NewWindowsAppStateSource(log))

	// Spec 023: Knowledge worker signals.
	col.Add(sources.NewIdleSource(0, 0))
	col.Add(sources.NewPowerSource())
	col.Add(sources.NewScreenshotSource())
	col.Add(sources.NewDownloadSource(""))
	col.Add(sources.NewAppLifecycleSource())
	col.Add(sources.NewDisplaySource())
	col.Add(sources.NewAudioSource())
	col.Add(sources.NewNetworkSource(true))

	// Spec 024: Browser context enrichment.
	col.Add(sources.NewBrowserSource(0, nil))
}
