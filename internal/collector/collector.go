// Package collector orchestrates all observation sources and fans their
// event streams into the store.  Each source runs in its own goroutine.
// The collector itself is lifecycle-managed via Start/Stop.
package collector

import (
	"context"
	"log/slog"
	"sync"

	"github.com/sigil-tech/sigil/internal/event"
	"github.com/sigil-tech/sigil/internal/kenazproto"
)

// EventInserter is the subset of store operations the collector needs.
type EventInserter interface {
	InsertEvent(ctx context.Context, e event.Event) error
}

// Source is implemented by every event-producing subsystem.
type Source interface {
	// Name returns a stable identifier used in event.Source.
	Name() string

	// Events starts the source and returns a channel of observations.
	// The channel is closed when ctx is cancelled or the source terminates.
	Events(ctx context.Context) (<-chan event.Event, error)
}

// Collector fans in events from all registered sources and writes them to the
// store.  It exposes Subscribe() for in-process consumers that want a copy of
// every stored event.
type Collector struct {
	store   EventInserter
	sources []Source
	log     *slog.Logger
	rateObs RateObserver

	// Broadcast is kept for backward compatibility.  New consumers should use
	// Subscribe() instead, which gives each consumer its own buffered channel.
	Broadcast chan event.Event

	mu          sync.Mutex
	subscribers []chan event.Event

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// Option is a functional option for New.
type Option func(*Collector)

// WithRateObserver injects a RateObserver into the Collector.  The observer is
// called on the drain hot-path; it MUST be non-blocking.  When nil is passed
// the default no-op observer is kept.
func WithRateObserver(obs RateObserver) Option {
	return func(c *Collector) {
		if obs != nil {
			c.rateObs = obs
		}
	}
}

// New creates a Collector.  sources may be extended before calling Start.
func New(s EventInserter, log *slog.Logger, opts ...Option) *Collector {
	c := &Collector{
		store:     s,
		log:       log,
		rateObs:   noopRateObserver{},
		Broadcast: make(chan event.Event, 256),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Add registers an additional source.  Must be called before Start.
func (c *Collector) Add(src Source) {
	c.sources = append(c.sources, src)
}

// Subscribe returns a new buffered channel that receives a copy of every
// stored event.  Each subscriber gets its own channel so a slow consumer
// does not block others.  Must be called before Start.
func (c *Collector) Subscribe() <-chan event.Event {
	ch := make(chan event.Event, 256)
	c.mu.Lock()
	c.subscribers = append(c.subscribers, ch)
	c.mu.Unlock()
	return ch
}

// Start launches all sources and begins fanning their events into the store.
// It returns immediately; collection runs in the background until Stop is
// called or the parent context is cancelled.
func (c *Collector) Start(ctx context.Context) error {
	ctx, c.cancel = context.WithCancel(ctx)

	for _, src := range c.sources {
		ch, err := src.Events(ctx)
		if err != nil {
			c.cancel()
			return err
		}

		c.wg.Add(1)
		go c.drain(ctx, src.Name(), ch)
	}

	return nil
}

// Stop signals all sources to terminate and waits for them to finish.
func (c *Collector) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
	c.wg.Wait()
	close(c.Broadcast)
	c.mu.Lock()
	for _, ch := range c.subscribers {
		close(ch)
	}
	c.mu.Unlock()
}

// drain reads events from a single source channel and writes them to the store.
func (c *Collector) drain(ctx context.Context, name string, ch <-chan event.Event) {
	defer c.wg.Done()

	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return
			}
			if err := c.store.InsertEvent(ctx, e); err != nil {
				c.log.Error("store event", "source", name, "err", err)
				continue
			}
			// Notify the rate observer for mapped event kinds.  Unmapped kinds
			// (e.g. KindIdle, KindPointer) are silently skipped per FR-010d.
			if sourceID, ok := kenazproto.SourceIDForKind(e.Kind); ok {
				c.rateObs.Observe(sourceID)
			}
			// Non-blocking broadcast to legacy channel.
			select {
			case c.Broadcast <- e:
			default:
			}
			// Non-blocking fan-out to all subscribers.
			c.mu.Lock()
			for _, sub := range c.subscribers {
				select {
				case sub <- e:
				default:
				}
			}
			c.mu.Unlock()

		case <-ctx.Done():
			return
		}
	}
}
