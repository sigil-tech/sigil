//go:build linux

package sources

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/sigil-tech/sigil/internal/event"
)

// LinuxFocusSource tracks the active window on Linux Wayland compositors.
type LinuxFocusSource struct {
	log      *slog.Logger
	interval time.Duration
	backend  func(ctx context.Context) (class string, title string, err error)
}

// NewLinuxFocusSource detects the active Wayland compositor and returns a
// LinuxFocusSource, or nil if a compositor-specific backend is not available
// (e.g. when Hyprland is detected — it has its own source).
func NewLinuxFocusSource(log *slog.Logger) *LinuxFocusSource {
	// Skip if Hyprland is running — it has its own dedicated source.
	if os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") != "" {
		return nil
	}

	backend := detectBackend(log)
	if backend == nil {
		return nil
	}
	return &LinuxFocusSource{log: log, interval: 2 * time.Second, backend: backend}
}

// Name returns the source name.
func (s *LinuxFocusSource) Name() string { return "linux-focus" }

// Events returns a channel of focus change events.
func (s *LinuxFocusSource) Events(ctx context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event, 16)

	go func() {
		defer close(ch)
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		var lastClass, lastTitle string

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				class, title, err := s.backend(ctx)
				if err != nil || (class == "" && title == "") {
					continue
				}
				if class == lastClass && title == lastTitle {
					continue
				}
				lastClass = class
				lastTitle = title

				ch <- event.Event{
					Kind:      event.KindHyprland,
					Source:    "linux-focus",
					Timestamp: time.Now(),
					Payload: map[string]any{
						"window_class": class,
						"window_title": title,
					},
				}
			}
		}
	}()

	return ch, nil
}

// frontApp returns the name of the foreground application.
// Uses the detected backend; stub returns empty.
func frontApp() string { return "" }

// windowTitle returns the title of the active window for the given app.
// Uses the detected backend; stub returns empty.
func windowTitle(_ string) string { return "" }

// detectBackend returns a polling function for the active Wayland compositor.
func detectBackend(log *slog.Logger) func(context.Context) (string, string, error) {
	session := os.Getenv("XDG_SESSION_TYPE")
	desktop := os.Getenv("XDG_CURRENT_DESKTOP")
	swaysock := os.Getenv("SWAYSOCK")

	// Sway: available if SWAYSOCK is set or desktop is sway.
	if swaysock != "" || strings.EqualFold(desktop, "sway") {
		return swayBackend
	}

	if session == "wayland" {
		switch strings.ToUpper(desktop) {
		case "GNOME":
			return gnomeBackend
		case "KDE":
			return kdeBackend
		}
	}

	return nil
}

// swayBackend queries Sway IPC for focused window.
func swayBackend(_ context.Context) (string, string, error) {
	return "", "", nil // stub — requires sway IPC implementation
}

// gnomeBackend queries GNOME for focused window.
func gnomeBackend(_ context.Context) (string, string, error) {
	return "", "", nil // stub — requires D-Bus implementation
}

// kdeBackend queries KDE for focused window.
func kdeBackend(_ context.Context) (string, string, error) {
	return "", "", nil // stub — requires D-Bus implementation
}

// searchFocused walks a sway/i3 tree to find the focused node.
func searchFocused(data []byte) (class, title string) {
	var node map[string]any
	if err := json.Unmarshal(data, &node); err != nil {
		return "", ""
	}
	return searchFocusedNode(node)
}

func searchFocusedNode(node map[string]any) (string, string) {
	focused, _ := node["focused"].(bool)
	if focused {
		class, _ := node["app_id"].(string)
		title, _ := node["name"].(string)
		return class, title
	}

	// Search regular nodes.
	if nodes, ok := node["nodes"].([]any); ok {
		for _, n := range nodes {
			if child, ok := n.(map[string]any); ok {
				if c, t := searchFocusedNode(child); c != "" || t != "" {
					return c, t
				}
			}
		}
	}

	// Search floating nodes.
	if floats, ok := node["floating_nodes"].([]any); ok {
		for _, n := range floats {
			if child, ok := n.(map[string]any); ok {
				if c, t := searchFocusedNode(child); c != "" || t != "" {
					return c, t
				}
			}
		}
	}

	return "", ""
}
