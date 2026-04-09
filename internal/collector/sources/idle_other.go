//go:build !darwin

package sources

import (
	"context"
	"time"

	"github.com/sigil-tech/sigil/internal/event"
)

// IdleSource is a stub on non-macOS platforms.
// TODO: Implement via XScreenSaverQueryInfo (Linux) and GetLastInputInfo (Windows).
type IdleSource struct {
	Threshold time.Duration
}

func NewIdleSource(threshold time.Duration) *IdleSource {
	return &IdleSource{Threshold: threshold}
}

func (s *IdleSource) Name() string { return "idle" }

func (s *IdleSource) Events(ctx context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event)
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch, nil
}
