//go:build linux

package sources

import (
	"context"
	"encoding/binary"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sigil-tech/sigil/internal/event"
)

const (
	// Linux input event types.
	evRel = 0x02
	// evKey is already defined in typing_linux.go (same build tag, same package).

	pointerInputEventSize = 24
	pointerWindowDuration = 30 * time.Second
)

// PointerSource reads /dev/input/event* for EV_REL (mouse movement) and
// EV_KEY (button clicks) events. It aggregates 30-second windows of movement
// event count and click count. Requires the 'input' group or root.
type PointerSource struct {
	log *slog.Logger
}

func NewPointerSource(log *slog.Logger) *PointerSource {
	return &PointerSource{log: log}
}

func (s *PointerSource) Name() string { return "pointer" }

func (s *PointerSource) Events(ctx context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event, 8)

	devices := findPointerDevices()
	if len(devices) == 0 {
		s.log.Warn("pointer: no accessible /dev/input/event* mouse devices found; pointer tracking disabled")
		go func() { <-ctx.Done(); close(ch) }()
		return ch, nil
	}

	var moveCount atomic.Int64
	var clickCount atomic.Int64

	for _, dev := range devices {
		go s.readPointerDevice(ctx, dev, &moveCount, &clickCount)
	}

	go func() {
		defer close(ch)

		ticker := time.NewTicker(pointerWindowDuration)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				moves := moveCount.Swap(0)
				clicks := clickCount.Swap(0)
				if moves == 0 && clicks == 0 {
					continue
				}
				emit(ch, ctx, event.Event{
					Kind:   event.KindPointer,
					Source: s.Name(),
					Payload: map[string]any{
						"action":      "window",
						"move_events": moves,
						"clicks":      clicks,
						"window_sec":  int(pointerWindowDuration.Seconds()),
					},
					Timestamp: time.Now(),
				})
			}
		}
	}()

	return ch, nil
}

// readPointerDevice reads raw input events from a mouse/trackpad device.
func (s *PointerSource) readPointerDevice(ctx context.Context, path string, moveCount, clickCount *atomic.Int64) {
	f, err := os.Open(path)
	if err != nil {
		s.log.Debug("pointer: cannot open device", "path", path, "err", err)
		return
	}
	defer f.Close()

	buf := make([]byte, pointerInputEventSize)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		n, err := f.Read(buf)
		if err != nil || n < pointerInputEventSize {
			return
		}

		evType := binary.LittleEndian.Uint16(buf[16:18])
		value := binary.LittleEndian.Uint32(buf[20:24])

		switch evType {
		case evRel:
			moveCount.Add(1)
		case evKey:
			// Mouse button press (value 1). Button codes: BTN_LEFT=0x110, BTN_RIGHT=0x111, BTN_MIDDLE=0x112.
			code := binary.LittleEndian.Uint16(buf[18:20])
			if value == 1 && code >= 0x110 && code <= 0x117 {
				clickCount.Add(1)
			}
		}
	}
}

// findPointerDevices scans /dev/input/ for mouse/trackpad devices.
func findPointerDevices() []string {
	var devices []string

	matches, err := filepath.Glob("/dev/input/event*")
	if err != nil {
		return nil
	}

	for _, dev := range matches {
		f, err := os.Open(dev)
		if err != nil {
			continue
		}
		f.Close()

		base := filepath.Base(dev)
		nameFile := filepath.Join("/sys/class/input", base, "device", "name")
		nameBytes, err := os.ReadFile(nameFile)
		if err != nil {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(string(nameBytes)))
		if strings.Contains(name, "mouse") || strings.Contains(name, "trackpad") ||
			strings.Contains(name, "touchpad") || strings.Contains(name, "pointer") {
			devices = append(devices, dev)
		}
	}

	return devices
}
