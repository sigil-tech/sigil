//go:build linux

package sources

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sigil-tech/sigil/internal/event"
)

const (
	batStatusPath  = "/sys/class/power_supply/BAT0/status"
	batCapacityPath = "/sys/class/power_supply/BAT0/capacity"
)

// PowerSource polls Linux battery state via sysfs.
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
				onAC, pct := readLinuxPowerState()
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

// readLinuxPowerState reads battery status and capacity from sysfs.
// Returns (onAC, percent). If files are unreadable (e.g. desktop without
// a battery), defaults to (true, 100).
func readLinuxPowerState() (onAC bool, percent int) {
	statusBytes, err := os.ReadFile(batStatusPath)
	if err != nil {
		return true, 100
	}
	status := strings.TrimSpace(string(statusBytes))

	// "Charging" or "Full" means on AC; "Discharging" means battery.
	onAC = status == "Charging" || status == "Full" || status == "Not charging"

	capBytes, err := os.ReadFile(batCapacityPath)
	if err != nil {
		return onAC, 100
	}
	pct, err := strconv.Atoi(strings.TrimSpace(string(capBytes)))
	if err != nil {
		return onAC, 100
	}
	return onAC, pct
}
