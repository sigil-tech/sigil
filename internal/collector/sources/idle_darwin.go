//go:build darwin

package sources

/*
#cgo CFLAGS: -x objective-c -mmacosx-version-min=10.14
#cgo LDFLAGS: -framework CoreGraphics -framework Foundation

#include <CoreGraphics/CoreGraphics.h>

double secondsSinceLastInput() {
    return CGEventSourceSecondsSinceLastEventType(
        kCGEventSourceStateCombinedSessionState,
        kCGAnyInputEventType
    );
}
*/
import "C"

import (
	"context"
	"time"

	"github.com/sigil-tech/sigil/internal/event"
)

// IdleSource detects active/idle transitions and screen lock on macOS.
type IdleSource struct {
	Threshold    time.Duration // idle threshold (default: 5m)
	PollInterval time.Duration // how often to check (default: 5s)
}

func NewIdleSource(threshold, pollInterval time.Duration) *IdleSource {
	if threshold <= 0 {
		threshold = 5 * time.Minute
	}
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}
	return &IdleSource{Threshold: threshold, PollInterval: pollInterval}
}

func (s *IdleSource) Name() string { return "idle" }

func (s *IdleSource) Events(ctx context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event, 16)

	go func() {
		defer close(ch)

		ticker := time.NewTicker(s.PollInterval)
		defer ticker.Stop()

		idle := false
		var idleStart time.Time

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				idleSec := float64(C.secondsSinceLastInput())
				threshSec := s.Threshold.Seconds()

				if !idle && idleSec >= threshSec {
					// Transition to idle.
					idle = true
					idleStart = time.Now()
					emit(ch, ctx, event.Event{
						Kind:   event.KindIdle,
						Source: s.Name(),
						Payload: map[string]any{
							"state":        "idle_start",
							"idle_seconds": 0,
						},
						Timestamp: time.Now(),
					})
				} else if idle && idleSec < threshSec {
					// Transition to active.
					duration := time.Since(idleStart).Seconds()
					idle = false
					emit(ch, ctx, event.Event{
						Kind:   event.KindIdle,
						Source: s.Name(),
						Payload: map[string]any{
							"state":        "idle_end",
							"idle_seconds": int(duration),
						},
						Timestamp: time.Now(),
					})
				}
			}
		}
	}()

	return ch, nil
}

// emit is defined in emit.go (shared across all platforms).
