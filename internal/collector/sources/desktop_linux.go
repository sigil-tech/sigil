//go:build linux

package sources

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/sigil-tech/sigil/internal/event"
)

// DesktopSource detects virtual desktop/workspace switches on Linux
// by polling xdotool for the current desktop number.
type DesktopSource struct{}

func NewDesktopSource() *DesktopSource { return &DesktopSource{} }

func (s *DesktopSource) Name() string { return "desktop" }

func (s *DesktopSource) Events(ctx context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event, 8)

	go func() {
		defer close(ch)

		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		lastDesktop := -1

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				current, total := readDesktopState()
				if current < 0 {
					continue
				}

				if lastDesktop >= 0 && current != lastDesktop {
					emit(ch, ctx, event.Event{
						Kind:   event.KindDesktop,
						Source: s.Name(),
						Payload: map[string]any{
							"action":         "switched",
							"desktop":        current,
							"total_desktops": total,
							"previous":       lastDesktop,
						},
						Timestamp: time.Now(),
					})
				}
				lastDesktop = current
			}
		}
	}()

	return ch, nil
}

// readDesktopState returns the current desktop index and total desktop count.
// Returns (-1, 0) if xdotool is unavailable.
func readDesktopState() (current, total int) {
	currentOut, err := exec.Command("xdotool", "get-desktop").Output()
	if err != nil {
		return -1, 0
	}
	current, err = strconv.Atoi(strings.TrimSpace(string(currentOut)))
	if err != nil {
		return -1, 0
	}

	totalOut, err := exec.Command("xdotool", "get-num-desktops").Output()
	if err != nil {
		return current, 0
	}
	total, _ = strconv.Atoi(strings.TrimSpace(string(totalOut)))

	return current, total
}
