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
	idleUser32         = syscall.NewLazyDLL("user32.dll")
	getLastInputInfo   = idleUser32.NewProc("GetLastInputInfo")
	idleKernel32       = syscall.NewLazyDLL("kernel32.dll")
	getTickCount       = idleKernel32.NewProc("GetTickCount")
	lockWorkStation    = idleUser32.NewProc("LockWorkStation")
	getSystemMetricsW  = idleUser32.NewProc("GetSystemMetrics")
)

// LASTINPUTINFO is the Windows structure for GetLastInputInfo.
type lastInputInfo struct {
	cbSize uint32
	dwTime uint32
}

const smRemoteSession = 0x1000 // SM_REMOTESESSION

// IdleSource detects active/idle transitions and screen lock on Windows.
// It uses GetLastInputInfo (user32.dll) for idle time detection and polls
// the session state for lock detection.
type IdleSource struct {
	Threshold time.Duration
}

// NewIdleSource creates an IdleSource with the given idle threshold.
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
		locked := false
		var idleStart time.Time

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				idleMs := idleMillisWindows()
				threshMs := s.Threshold.Milliseconds()

				if !idle && idleMs >= threshMs {
					idle = true
					idleStart = time.Now()
					select {
					case ch <- event.Event{
						Kind:   event.KindIdle,
						Source: s.Name(),
						Payload: map[string]any{
							"state":        "idle_start",
							"idle_seconds": 0,
						},
						Timestamp: time.Now(),
					}:
					case <-ctx.Done():
						return
					}
				} else if idle && idleMs < threshMs {
					duration := time.Since(idleStart).Seconds()
					idle = false
					select {
					case ch <- event.Event{
						Kind:   event.KindIdle,
						Source: s.Name(),
						Payload: map[string]any{
							"state":        "idle_end",
							"idle_seconds": int(duration),
						},
						Timestamp: time.Now(),
					}:
					case <-ctx.Done():
						return
					}
				}

				// Detect screen lock via session state.
				// On Windows, we approximate lock detection by checking if idle
				// time is very large (> 2x threshold) which often indicates lock,
				// or by checking for a remote session flip.
				nowLocked := isSessionLocked()
				if nowLocked != locked {
					locked = nowLocked
					state := "unlocked"
					if locked {
						state = "locked"
					}
					select {
					case ch <- event.Event{
						Kind:   event.KindIdle,
						Source: s.Name(),
						Payload: map[string]any{
							"state": state,
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

// idleMillisWindows returns the number of milliseconds since the last user
// input event using GetLastInputInfo.
func idleMillisWindows() int64 {
	info := lastInputInfo{cbSize: uint32(unsafe.Sizeof(lastInputInfo{}))}
	ret, _, _ := getLastInputInfo.Call(uintptr(unsafe.Pointer(&info)))
	if ret == 0 {
		return 0
	}

	tickCount, _, _ := getTickCount.Call()
	elapsed := uint32(tickCount) - info.dwTime
	return int64(elapsed)
}

// isSessionLocked provides a heuristic for screen lock detection on Windows.
// A robust approach would use WTSRegisterSessionNotification, but that requires
// a window handle and message pump. Instead, we use a combination of heuristics:
// if idle time exceeds 10 minutes, we assume the session is likely locked.
// TODO: Use WTSQuerySessionInformation for proper lock detection.
func isSessionLocked() bool {
	// Check if idle time is very long (>10 min), suggesting lock.
	idleMs := idleMillisWindows()
	return idleMs > 600000
}
