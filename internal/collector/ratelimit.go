package collector

import (
	"context"
	"sync"
	"time"

	"github.com/sigil-tech/sigil/internal/event"
)

// RateLimitSource wraps a Source and enforces a maximum event rate.
// Events within the per-minute limit pass through unchanged. Excess events
// are dropped and a single event with Kind "rate_limit" is emitted per
// window summarising the drop count.
type RateLimitSource struct {
	inner Source
	limit int // max events per minute
}

// NewRateLimitSource wraps inner with a per-minute event rate limit.
// If limit <= 0 it defaults to 1000.
func NewRateLimitSource(inner Source, limit int) *RateLimitSource {
	if limit <= 0 {
		limit = 1000
	}
	return &RateLimitSource{inner: inner, limit: limit}
}

// Name returns the name of the wrapped source.
func (r *RateLimitSource) Name() string {
	return r.inner.Name()
}

// Events returns a channel that emits at most r.limit events per minute from
// the wrapped source. Excess events are dropped, and a single rate_limit event
// is emitted per window summarising the drop count. The returned channel is
// closed when ctx is cancelled or the inner source terminates.
func (r *RateLimitSource) Events(ctx context.Context) (<-chan event.Event, error) {
	in, err := r.inner.Events(ctx)
	if err != nil {
		return nil, err
	}

	out := make(chan event.Event, 64)

	go func() {
		defer close(out)

		var (
			mu          sync.Mutex
			count       int
			dropCount   int
			windowStart = time.Now()
		)

		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()

		// Window resetter: fires every minute to emit a drop summary and
		// reset counters.
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case t := <-ticker.C:
					mu.Lock()
					if dropCount > 0 {
						drop := event.Event{
							Kind:      event.Kind("rate_limit"),
							Source:    r.inner.Name(),
							Timestamp: t,
							Payload: map[string]any{
								"source":       r.inner.Name(),
								"window_start": windowStart.UnixMilli(),
								"drop_count":   dropCount,
							},
						}
						select {
						case out <- drop:
						default:
						}
					}
					count = 0
					dropCount = 0
					windowStart = t
					mu.Unlock()
				}
			}
		}()

		for {
			select {
			case <-ctx.Done():
				return
			case e, ok := <-in:
				if !ok {
					return
				}
				mu.Lock()
				if count < r.limit {
					count++
					mu.Unlock()
					select {
					case out <- e:
					case <-ctx.Done():
						return
					}
				} else {
					dropCount++
					mu.Unlock()
				}
			}
		}
	}()

	return out, nil
}

// compile-time assertion: RateLimitSource satisfies Source.
var _ Source = (*RateLimitSource)(nil)
