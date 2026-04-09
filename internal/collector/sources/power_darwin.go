//go:build darwin

package sources

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/sigil-tech/sigil/internal/event"
)

// PowerSource polls macOS battery state via pmset.
type PowerSource struct{}

func NewPowerSource() *PowerSource { return &PowerSource{} }

func (s *PowerSource) Name() string { return "power" }

func (s *PowerSource) Events(ctx context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event, 8)

	go func() {
		defer close(ch)

		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()

		var lastOnAC *bool

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				onAC, pct := readPowerState()
				if lastOnAC != nil && *lastOnAC != onAC {
					action := "ac_connected"
					payload := map[string]any{"action": action}
					if !onAC {
						action = "ac_disconnected"
						payload["action"] = action
						payload["battery_percent"] = pct
					}
					emit(ch, ctx, event.Event{
						Kind:      event.KindPower,
						Source:    s.Name(),
						Payload:   payload,
						Timestamp: time.Now(),
					})
				}
				lastOnAC = &onAC

				if !onAC && (pct == 20 || pct == 10) {
					emit(ch, ctx, event.Event{
						Kind:   event.KindPower,
						Source: s.Name(),
						Payload: map[string]any{
							"action":          "low_battery",
							"battery_percent": pct,
						},
						Timestamp: time.Now(),
					})
				}
			}
		}
	}()

	return ch, nil
}

func readPowerState() (onAC bool, percent int) {
	out, err := exec.Command("pmset", "-g", "batt").Output()
	if err != nil {
		return true, 100
	}
	s := string(out)
	onAC = strings.Contains(s, "'AC Power'")
	// Parse "XX%" from the output.
	for _, line := range strings.Split(s, "\n") {
		if idx := strings.Index(line, "%"); idx > 0 {
			start := idx - 1
			for start > 0 && line[start-1] >= '0' && line[start-1] <= '9' {
				start--
			}
			if n, err := strconv.Atoi(line[start:idx]); err == nil {
				percent = n
			}
		}
	}
	return
}
