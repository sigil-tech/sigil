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
}
