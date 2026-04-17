package collector

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sigil-tech/sigil/internal/event"
	"github.com/stretchr/testify/require"
)

var errSourceFailed = errors.New("source unavailable")

// staticSource emits a fixed, pre-built set of events and then closes.
// It is distinct from mockSource (defined in collector_test.go) to avoid
// redeclaration within the package.
type staticSource struct {
	name   string
	events []event.Event
}

func (s *staticSource) Name() string { return s.name }

func (s *staticSource) Events(_ context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event, len(s.events))
	for _, e := range s.events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

// drainAll reads all events from ch until it is closed or ctx is cancelled,
// returning whatever was received.
func drainAll(ctx context.Context, ch <-chan event.Event) []event.Event {
	var out []event.Event
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, e)
		case <-ctx.Done():
			return out
		}
	}
}

func makeStaticEvents(n int, kind event.Kind, source string) []event.Event {
	events := make([]event.Event, n)
	for i := range events {
		events[i] = event.Event{Kind: kind, Source: source, Timestamp: time.Now()}
	}
	return events
}

func TestRateLimitSourceName(t *testing.T) {
	src := &staticSource{name: "mySource"}
	limited := NewRateLimitSource(src, 100)
	require.Equal(t, "mySource", limited.Name())
}

func TestRateLimitSourcePassesUnderLimit(t *testing.T) {
	const n = 10
	src := &staticSource{
		name:   "test",
		events: makeStaticEvents(n, event.KindFile, "test"),
	}
	limited := NewRateLimitSource(src, 1000)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ch, err := limited.Events(ctx)
	require.NoError(t, err)

	received := drainAll(ctx, ch)
	require.Equal(t, n, len(received), "all events under limit must pass through")
}

func TestRateLimitSourceDropsOverLimit(t *testing.T) {
	const total = 200
	const limit = 50

	src := &staticSource{
		name:   "burst",
		events: makeStaticEvents(total, event.KindFile, "burst"),
	}
	limited := NewRateLimitSource(src, limit)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ch, err := limited.Events(ctx)
	require.NoError(t, err)

	received := drainAll(ctx, ch)

	// Exactly limit events pass; the ticker fires only after one minute so no
	// rate_limit summary event will appear within the 2-second test window.
	require.Equal(t, limit, len(received),
		"expected exactly %d events (limit), got %d", limit, len(received))
}

func TestRateLimitSourceDefaultLimit(t *testing.T) {
	src := &staticSource{name: "s"}
	rl := NewRateLimitSource(src, 0)
	require.Equal(t, 1000, rl.limit)

	rl2 := NewRateLimitSource(src, -5)
	require.Equal(t, 1000, rl2.limit)
}

func TestRateLimitSourcePropagatesSourceError(t *testing.T) {
	// errSource always returns an error from Events.
	errSrc := &mockSource{name: "err", err: errSourceFailed}
	limited := NewRateLimitSource(errSrc, 100)

	ctx := context.Background()
	_, err := limited.Events(ctx)
	require.ErrorIs(t, err, errSourceFailed)
}

func TestRateLimitSourceContextCancellation(t *testing.T) {
	// Use an unbuffered, never-closing channel so the goroutine must exit via
	// ctx.Done().
	inner := &mockSource{name: "proc", ch: make(chan event.Event)}
	limited := NewRateLimitSource(inner, 100)

	ctx, cancel := context.WithCancel(context.Background())

	ch, err := limited.Events(ctx)
	require.NoError(t, err)

	cancel()

	// After cancellation the output channel must be closed promptly.
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("channel still open after context cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("output channel not closed after context cancellation")
	}
}
