//go:build linux

package sources

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sigil-tech/sigil/internal/event"
)

// AppLifecycleSource polls /proc to detect GUI application launches and exits
// on Linux. Only processes with DISPLAY or WAYLAND_DISPLAY in their environment
// are considered GUI processes.
type AppLifecycleSource struct{}

func NewAppLifecycleSource() *AppLifecycleSource { return &AppLifecycleSource{} }

func (s *AppLifecycleSource) Name() string { return "app_lifecycle" }

type trackedProc struct {
	name    string
	startAt time.Time
}

func (s *AppLifecycleSource) Events(ctx context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event, 32)

	go func() {
		defer close(ch)

		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		known := make(map[int]trackedProc)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				current := scanGUIProcs()

				// Detect new processes.
				for pid, name := range current {
					if _, exists := known[pid]; !exists {
						known[pid] = trackedProc{name: name, startAt: time.Now()}
						emit(ch, ctx, event.Event{
							Kind:   event.KindAppLifecycle,
							Source: s.Name(),
							Payload: map[string]any{
								"action": "launched",
								"app":    name,
								"pid":    pid,
							},
							Timestamp: time.Now(),
						})
					}
				}

				// Detect exited processes.
				for pid, proc := range known {
					if _, exists := current[pid]; !exists {
						duration := time.Since(proc.startAt).Seconds()
						delete(known, pid)
						emit(ch, ctx, event.Event{
							Kind:   event.KindAppLifecycle,
							Source: s.Name(),
							Payload: map[string]any{
								"action":           "exited",
								"app":              proc.name,
								"pid":              pid,
								"duration_seconds": int(duration),
							},
							Timestamp: time.Now(),
						})
					}
				}
			}
		}
	}()

	return ch, nil
}

// scanGUIProcs scans /proc for processes with a display environment variable
// (DISPLAY or WAYLAND_DISPLAY), indicating a GUI process.
func scanGUIProcs() map[int]string {
	result := make(map[int]string)

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return result
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}

		if !hasDisplayEnv(pid) {
			continue
		}

		name := procName(pid)
		if name == "" {
			continue
		}

		result[pid] = name
	}
	return result
}

// hasDisplayEnv checks if a process has DISPLAY or WAYLAND_DISPLAY set.
func hasDisplayEnv(pid int) bool {
	envPath := fmt.Sprintf("/proc/%d/environ", pid)
	data, err := os.ReadFile(envPath)
	if err != nil {
		return false
	}
	// environ is null-byte separated.
	envStr := string(data)
	for _, entry := range strings.Split(envStr, "\x00") {
		if strings.HasPrefix(entry, "DISPLAY=") || strings.HasPrefix(entry, "WAYLAND_DISPLAY=") {
			return true
		}
	}
	return false
}

// procName returns the process name from /proc/PID/comm.
func procName(pid int) string {
	commPath := filepath.Join("/proc", strconv.Itoa(pid), "comm")
	data, err := os.ReadFile(commPath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
