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
	evKey = 0x01

	// Size of a Linux input_event struct on 64-bit: timeval(16) + type(2) + code(2) + value(4) = 24 bytes.
	inputEventSize = 24

	typingWindowDuration = 30 * time.Second
)

// TypingSource reads /dev/input/event* devices for EV_KEY events and counts
// keystrokes per 30-second window. It NEVER captures key identifiers --
// only the count is recorded. Requires the user to be in the 'input' group
// or running as root. This source is opt-in only.
type TypingSource struct {
	log *slog.Logger
}

func NewTypingSource(log *slog.Logger) *TypingSource {
	return &TypingSource{log: log}
}

func (s *TypingSource) Name() string { return "typing" }

func (s *TypingSource) Events(ctx context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event, 8)

	devices := findKeyboardDevices()
	if len(devices) == 0 {
		s.log.Warn("typing: no accessible /dev/input/event* devices found; keystroke counting disabled")
		go func() { <-ctx.Done(); close(ch) }()
		return ch, nil
	}

	var keyCount atomic.Int64

	// Start a reader goroutine for each keyboard device.
	for _, dev := range devices {
		go s.readDevice(ctx, dev, &keyCount)
	}

	// Aggregation goroutine: emit count every 30 seconds.
	go func() {
		defer close(ch)

		ticker := time.NewTicker(typingWindowDuration)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				count := keyCount.Swap(0)
				if count == 0 {
					continue
				}
				emit(ch, ctx, event.Event{
					Kind:   event.KindTyping,
					Source: s.Name(),
					Payload: map[string]any{
						"action":     "window",
						"key_count":  count,
						"window_sec": int(typingWindowDuration.Seconds()),
					},
					Timestamp: time.Now(),
				})
			}
		}
	}()

	return ch, nil
}

// readDevice reads raw input events from a single /dev/input/event* device
// and increments the counter for each key press (value == 1).
func (s *TypingSource) readDevice(ctx context.Context, path string, counter *atomic.Int64) {
	f, err := os.Open(path)
	if err != nil {
		s.log.Debug("typing: cannot open device", "path", path, "err", err)
		return
	}
	defer f.Close()

	buf := make([]byte, inputEventSize)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		n, err := f.Read(buf)
		if err != nil || n < inputEventSize {
			return
		}

		// Parse input_event: skip timeval (16 bytes), then type (2 bytes), code (2 bytes), value (4 bytes).
		evType := binary.LittleEndian.Uint16(buf[16:18])
		// We intentionally do NOT read buf[18:20] (key code) to avoid capturing key identifiers.
		value := binary.LittleEndian.Uint32(buf[20:24])

		// EV_KEY with value 1 = key press (not repeat or release).
		if evType == evKey && value == 1 {
			counter.Add(1)
		}
	}
}

// findKeyboardDevices scans /dev/input/ for event devices that are readable
// and appear to be keyboards (contain "kbd" or "keyboard" in their sysfs name).
func findKeyboardDevices() []string {
	var devices []string

	matches, err := filepath.Glob("/dev/input/event*")
	if err != nil {
		return nil
	}

	for _, dev := range matches {
		// Check readability.
		f, err := os.Open(dev)
		if err != nil {
			continue
		}
		f.Close()

		// Try to identify as keyboard via sysfs.
		// /sys/class/input/eventN/device/name
		base := filepath.Base(dev)
		nameFile := filepath.Join("/sys/class/input", base, "device", "name")
		nameBytes, err := os.ReadFile(nameFile)
		if err != nil {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(string(nameBytes)))
		if strings.Contains(name, "keyboard") || strings.Contains(name, "kbd") {
			devices = append(devices, dev)
		}
	}

	return devices
}
