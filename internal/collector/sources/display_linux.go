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

// DisplaySource detects monitor connect/disconnect on Linux by polling
// xrandr --listmonitors.
type DisplaySource struct{}

func NewDisplaySource() *DisplaySource { return &DisplaySource{} }

func (s *DisplaySource) Name() string { return "display" }

func (s *DisplaySource) Events(ctx context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event, 8)

	go func() {
		defer close(ch)

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		lastCount := -1

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				count, names := readMonitors()
				if lastCount >= 0 && count != lastCount {
					action := "connected"
					if count < lastCount {
						action = "disconnected"
					}
					emit(ch, ctx, event.Event{
						Kind:   event.KindDisplay,
						Source: s.Name(),
						Payload: map[string]any{
							"action":        action,
							"monitor_count": count,
							"monitors":      names,
						},
						Timestamp: time.Now(),
					})
				}
				lastCount = count
			}
		}
	}()

	return ch, nil
}

// readMonitors runs xrandr --listmonitors and returns the count and names.
func readMonitors() (int, []string) {
	out, err := exec.Command("xrandr", "--listmonitors").Output()
	if err != nil {
		return 0, nil
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 {
		return 0, nil
	}

	// First line is "Monitors: N"
	var count int
	if parts := strings.Fields(lines[0]); len(parts) >= 2 {
		count, _ = strconv.Atoi(parts[1])
	}

	var names []string
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Format: " 0: +*eDP-1 1920/340x1080/190+0+0  eDP-1"
		fields := strings.Fields(line)
		if len(fields) > 0 {
			name := fields[len(fields)-1]
			names = append(names, name)
		}
	}

	return count, names
}
