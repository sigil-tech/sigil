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
	Threshold time.Duration // default: 5 minutes
}

func NewIdleSource(threshold time.Duration) *IdleSource {
	if threshold <= 0 {
		threshold = 5 * time.Minute
	}
	return &IdleSource{Threshold: threshold}
}

func (s *IdleSource) Name() string { return "idle" }

func (s *IdleSource) Events(ctx context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event, 16)

	go func() {
		defer close(ch)

		ticker := time.NewTicker(5 * time.Second)
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
