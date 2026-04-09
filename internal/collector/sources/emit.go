package sources

import (
	"context"

	"github.com/sigil-tech/sigil/internal/event"
)

// emit sends an event to the channel without blocking.
func emit(ch chan<- event.Event, ctx context.Context, e event.Event) {
	select {
	case ch <- e:
	case <-ctx.Done():
	}
}
