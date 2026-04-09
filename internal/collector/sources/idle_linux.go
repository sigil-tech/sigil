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

// IdleSource detects active/idle transitions and screen lock on Linux.
// It uses xprintidle for idle time detection and dbus-send for screen lock
// state via org.freedesktop.ScreenSaver.
type IdleSource struct {
	Threshold    time.Duration
	PollInterval time.Duration
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
		locked := false
		var idleStart time.Time

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				idleMs := readIdleMillis()
				threshMs := int64(s.Threshold.Milliseconds())

				if !idle && idleMs >= threshMs {
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
				} else if idle && idleMs < threshMs {
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

				// Check screen lock via D-Bus.
				nowLocked := isScreenLocked()
				if nowLocked != locked {
					locked = nowLocked
					state := "unlocked"
					if locked {
						state = "locked"
					}
					emit(ch, ctx, event.Event{
						Kind:   event.KindIdle,
						Source: s.Name(),
						Payload: map[string]any{
							"state": state,
						},
						Timestamp: time.Now(),
					})
				}
			}
		}
	}()

	return ch, nil
}

// readIdleMillis returns user idle time in milliseconds via xprintidle.
// Falls back to reading /proc/interrupts if xprintidle is unavailable.
func readIdleMillis() int64 {
	out, err := exec.Command("xprintidle").Output()
	if err != nil {
		return readIdleFromProcInterrupts()
	}
	ms, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0
	}
	return ms
}

// readIdleFromProcInterrupts is a rough fallback: it cannot give real idle
// time, so it returns 0 (always active) when /proc/interrupts is unreadable.
func readIdleFromProcInterrupts() int64 {
	// /proc/interrupts only tells us total interrupt counts, not idle time.
	// Without a baseline to compare against, we cannot compute idle duration.
	// Return 0 so the source degrades gracefully to never firing idle events.
	return 0
}

// isScreenLocked checks the screen lock state via org.freedesktop.ScreenSaver D-Bus.
func isScreenLocked() bool {
	out, err := exec.Command("dbus-send",
		"--session",
		"--dest=org.freedesktop.ScreenSaver",
		"--type=method_call",
		"--print-reply",
		"/org/freedesktop/ScreenSaver",
		"org.freedesktop.ScreenSaver.GetActive",
	).Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "boolean true")
}
