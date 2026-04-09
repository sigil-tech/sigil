//go:build !darwin

package sources

import (
	"context"

	"github.com/sigil-tech/sigil/internal/event"
)

// PowerSource is a stub on non-macOS platforms.
type PowerSource struct{}

func NewPowerSource() *PowerSource { return &PowerSource{} }
func (s *PowerSource) Name() string { return "power" }
func (s *PowerSource) Events(ctx context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event)
	go func() { <-ctx.Done(); close(ch) }()
	return ch, nil
}
