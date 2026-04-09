//go:build windows

package sources

import (
	"context"
	"syscall"
	"time"

	"github.com/sigil-tech/sigil/internal/event"
)

var (
	dispUser32       = syscall.NewLazyDLL("user32.dll")
	getSystemMetrics = dispUser32.NewProc("GetSystemMetrics")
)

const smCMonitors = 80 // SM_CMONITORS — number of display monitors

// DisplaySource detects monitor connect/disconnect events on Windows by
// polling GetSystemMetrics(SM_CMONITORS). It emits an event whenever the
// monitor count changes.
type DisplaySource struct{}

// NewDisplaySource creates a DisplaySource.
func NewDisplaySource() *DisplaySource { return &DisplaySource{} }

func (s *DisplaySource) Name() string { return "display" }

func (s *DisplaySource) Events(ctx context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event, 8)

	go func() {
		defer close(ch)

		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		lastCount := monitorCount()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				count := monitorCount()
				if count == lastCount {
					continue
				}

				action := "connected"
				if count < lastCount {
					action = "disconnected"
				}

				select {
				case ch <- event.Event{
					Kind:   event.KindDisplay,
					Source: s.Name(),
					Payload: map[string]any{
						"action":        action,
						"monitor_count": count,
						"previous":      lastCount,
					},
					Timestamp: time.Now(),
				}:
				case <-ctx.Done():
					return
				}

				lastCount = count
			}
		}
	}()

	return ch, nil
}

// monitorCount returns the number of display monitors attached to the system.
func monitorCount() int {
	ret, _, _ := getSystemMetrics.Call(smCMonitors)
	return int(ret)
}
