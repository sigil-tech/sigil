//go:build !darwin

package main

import "github.com/sigil-tech/sigil/internal/collector"

// addPlatformSources is a no-op on Linux and other platforms.
// HyprlandSource (registered unconditionally) handles Linux window focus.
func addPlatformSources(_ *collector.Collector) {}
