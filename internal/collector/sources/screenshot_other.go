//go:build !darwin && !linux && !windows

package sources

import (
	"context"

	"github.com/sigil-tech/sigil/internal/event"
)

type ScreenshotSource struct{}

func NewScreenshotSource() *ScreenshotSource { return &ScreenshotSource{} }
func (s *ScreenshotSource) Name() string     { return "screenshot" }
func (s *ScreenshotSource) Events(ctx context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event)
	go func() { <-ctx.Done(); close(ch) }()
	return ch, nil
}
