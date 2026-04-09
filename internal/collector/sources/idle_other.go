//go:build !darwin && !linux && !windows

package sources

import (
	"context"
	"time"

	"github.com/sigil-tech/sigil/internal/event"
)

// IdleSource is a stub on non-macOS platforms.
// TODO: Implement via XScreenSaverQueryInfo (Linux) and GetLastInputInfo (Windows).
type IdleSource struct {
	Threshold    time.Duration
	PollInterval time.Duration
}

func NewIdleSource(threshold, pollInterval time.Duration) *IdleSource {
	return &IdleSource{Threshold: threshold, PollInterval: pollInterval}
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
