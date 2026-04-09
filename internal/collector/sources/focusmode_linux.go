//go:build linux

package sources

import (
	"context"

	"github.com/sigil-tech/sigil/internal/event"
)

// FocusModeSource is a no-op stub on Linux. There is no standard
// Do-Not-Disturb / Focus Mode API on Linux desktop environments.
type FocusModeSource struct{}

func NewFocusModeSource() *FocusModeSource { return &FocusModeSource{} }

func (s *FocusModeSource) Name() string { return "focus_mode" }

func (s *FocusModeSource) Events(_ context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event)
	// No events will ever be sent; the channel stays open until ctx is cancelled
	// by the caller closing it indirectly (the collector drains until closed).
	// We close immediately since there is nothing to do.
	close(ch)
	return ch, nil
}
