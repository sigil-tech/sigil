//go:build windows

package main

import (
	"log/slog"

	"github.com/sigil-tech/sigil/internal/collector"
	"github.com/sigil-tech/sigil/internal/collector/sources"
	"github.com/sigil-tech/sigil/internal/config"
)

func addPlatformSources(col *collector.Collector, log *slog.Logger, srcs config.SourcesConfig) {
	col.Add(sources.NewWindowsFocusSource(log))
	if srcs.Clipboard.IsEnabled(true) {
		col.Add(sources.NewWindowsClipboardSource(log))
	}
	col.Add(sources.NewWindowsAppStateSource(log))

	// Spec 023: Knowledge worker signals.
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
	if srcs.AppLifecycle.IsEnabled(true) {
		col.Add(sources.NewAppLifecycleSource())
	}
	if srcs.Display.IsEnabled(true) {
		col.Add(sources.NewDisplaySource())
	}
	if srcs.Audio.IsEnabled(true) {
		col.Add(sources.NewAudioSource())
	}
	if srcs.Network.IsEnabled(true) {
		col.Add(sources.NewNetworkSource())
	}

	// Spec 024: Browser context enrichment.
	if srcs.Browser.IsEnabled(true) {
		col.Add(sources.NewBrowserSource(0, nil))
	}
}
