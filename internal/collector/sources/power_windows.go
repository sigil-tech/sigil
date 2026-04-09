//go:build windows

package sources

import (
	"context"
	"syscall"
	"time"
	"unsafe"

	"github.com/sigil-tech/sigil/internal/event"
)

var (
	powerKernel32        = syscall.NewLazyDLL("kernel32.dll")
	getSystemPowerStatus = powerKernel32.NewProc("GetSystemPowerStatus")
)

// systemPowerStatus mirrors the SYSTEM_POWER_STATUS structure from Windows API.
type systemPowerStatus struct {
	ACLineStatus        byte
	BatteryFlag         byte
	BatteryLifePercent  byte
	SystemStatusFlag    byte
	BatteryLifeTime     uint32
	BatteryFullLifeTime uint32
}

const (
	acOnline  = 1
	acOffline = 0
)

// PowerSource polls battery state via GetSystemPowerStatus on Windows.
type PowerSource struct{}

// NewPowerSource creates a PowerSource.
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
				status, ok := readWindowsPowerStatus()
				if !ok {
					continue
				}

				onAC := status.ACLineStatus == acOnline
				pct := int(status.BatteryLifePercent)
				// BatteryLifePercent is 255 when unknown.
				if pct > 100 {
					pct = 100
				}

				if lastOnAC != nil && *lastOnAC != onAC {
					action := "ac_connected"
					payload := map[string]any{"action": action}
					if !onAC {
						action = "ac_disconnected"
						payload["action"] = action
						payload["battery_percent"] = pct
					}
					select {
					case ch <- event.Event{
						Kind:      event.KindPower,
						Source:    s.Name(),
						Payload:   payload,
						Timestamp: time.Now(),
					}:
					case <-ctx.Done():
						return
					}
				}
				lastOnAC = &onAC

				if !onAC && (pct == 20 || pct == 10 || pct == 5) {
					select {
					case ch <- event.Event{
						Kind:   event.KindPower,
						Source: s.Name(),
						Payload: map[string]any{
							"action":          "low_battery",
							"battery_percent": pct,
						},
						Timestamp: time.Now(),
					}:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()

	return ch, nil
}

// readWindowsPowerStatus calls GetSystemPowerStatus and returns the result.
func readWindowsPowerStatus() (systemPowerStatus, bool) {
	var status systemPowerStatus
	ret, _, _ := getSystemPowerStatus.Call(uintptr(unsafe.Pointer(&status)))
	if ret == 0 {
		return status, false
	}
	return status, true
}
