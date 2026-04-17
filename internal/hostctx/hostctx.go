// Package hostctx defines the interface through which the VM Observer
// reads host-side workflow context. The production implementation sends
// requests over the vsock control channel (port 7700); the fake is used
// in all tests.
package hostctx

import (
	"context"

	"github.com/sigil-tech/sigil/internal/store"
)

// HostContextReader is implemented by the VM Observer to access host-side
// workflow context. All methods return empty/nil results (not errors) when
// the host is unreachable — degraded mode is the default, not exceptional.
type HostContextReader interface {
	// RecentPatterns returns the most recent limit analyzer-derived patterns
	// from the host store. Returns an empty slice (not an error) when the
	// host is unreachable — degraded mode.
	RecentPatterns(ctx context.Context, limit int) ([]store.PatternSummary, error)

	// ActiveSession returns the host's current task record if one exists,
	// or nil if the host is idle or unreachable.
	ActiveSession(ctx context.Context) (*store.TaskRecord, error)
}
