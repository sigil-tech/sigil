//go:build linux

package main

import (
	"log/slog"

	"github.com/sigil-tech/sigil/internal/collector"
	"github.com/sigil-tech/sigil/internal/collector/sources"
	"github.com/sigil-tech/sigil/internal/config"
)

func addPlatformSources(col *collector.Collector, log *slog.Logger, srcs config.SourcesConfig) {
	if src := sources.NewLinuxFocusSource(log); src != nil {
		col.Add(src)
	}

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
	if srcs.Desktop.IsEnabled(true) {
		col.Add(sources.NewDesktopSource())
	}

	// Spec 024: Browser context enrichment.
	if srcs.Browser.IsEnabled(true) {
		col.Add(sources.NewBrowserSource(0, nil))
	}

	// Typing and clipboard sources are opt-in-by-default on Linux but require
	// elevated permissions (input group) and available clipboard tools respectively.
	if srcs.Typing.IsEnabled(true) {
		col.Add(sources.NewTypingSource(log))
	}
	if srcs.Clipboard.IsEnabled(true) {
		col.Add(&sources.ClipboardSource{})
	}
}
