//go:build windows

package sources

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/sigil-tech/sigil/internal/event"
)

// ScreenshotSource detects screenshots on Windows by watching the user's
// Screenshots folder (%USERPROFILE%\Pictures\Screenshots). Windows stores
// screenshots taken with Win+PrintScreen in this location.
type ScreenshotSource struct{}

// NewScreenshotSource creates a ScreenshotSource.
func NewScreenshotSource() *ScreenshotSource { return &ScreenshotSource{} }

func (s *ScreenshotSource) Name() string { return "screenshot" }

func (s *ScreenshotSource) Events(ctx context.Context) (<-chan event.Event, error) {
	home, _ := os.UserHomeDir()
	screenshotDir := filepath.Join(home, "Pictures", "Screenshots")

	// Ensure the directory exists; if not, create it so the watcher
	// doesn't fail. Win+PrintScreen creates it on first use, but the
	// user may not have taken a screenshot yet.
	if err := os.MkdirAll(screenshotDir, 0o755); err != nil {
		// If we can't create it, return a no-op source rather than failing.
		ch := make(chan event.Event)
		go func() { <-ctx.Done(); close(ch) }()
		return ch, nil
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := w.Add(screenshotDir); err != nil {
		w.Close()
		return nil, err
	}

	ch := make(chan event.Event, 8)

	go func() {
		defer w.Close()
		defer close(ch)

		for {
			select {
			case <-ctx.Done():
				return
			case fe, ok := <-w.Events:
				if !ok {
					return
				}
				if fe.Op&fsnotify.Create == 0 {
					continue
				}
				base := filepath.Base(fe.Name)
				ext := strings.ToLower(filepath.Ext(base))
				// Windows screenshots are typically PNG files named
				// "Screenshot (N).png" or "Screenshot YYYY-MM-DD ..."
				if ext != ".png" && ext != ".jpg" && ext != ".jpeg" {
					continue
				}
				if !strings.HasPrefix(base, "Screenshot") {
					continue
				}

				select {
				case ch <- event.Event{
					Kind:   event.KindScreenshot,
					Source: s.Name(),
					Payload: map[string]any{
						"action": "captured",
						"type":   "full_screen",
					},
					Timestamp: time.Now(),
				}:
				case <-ctx.Done():
					return
				}
			case _, ok := <-w.Errors:
				if !ok {
					return
				}
			}
		}
	}()

	return ch, nil
}
