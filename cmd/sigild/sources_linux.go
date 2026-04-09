//go:build linux

package main

import (
	"log/slog"

	"github.com/sigil-tech/sigil/internal/collector"
	"github.com/sigil-tech/sigil/internal/collector/sources"
)

func addPlatformSources(col *collector.Collector, log *slog.Logger) {
	if src := sources.NewLinuxFocusSource(log); src != nil {
		col.Add(src)
	}

	// Spec 023: Knowledge worker signals.
	col.Add(sources.NewIdleSource(0))
	col.Add(sources.NewPowerSource())
	col.Add(sources.NewScreenshotSource())
	col.Add(sources.NewDownloadSource(""))
	col.Add(sources.NewAppLifecycleSource())
	col.Add(sources.NewDisplaySource())
	col.Add(sources.NewAudioSource())
	col.Add(sources.NewNetworkSource(true))
	col.Add(sources.NewDesktopSource())

	// Spec 024: Browser context enrichment.
	col.Add(sources.NewBrowserSource(0, nil))
}
