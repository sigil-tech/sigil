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
}
