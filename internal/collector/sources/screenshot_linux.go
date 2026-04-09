//go:build linux

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

// ScreenshotSource detects screenshots on Linux by watching common
// screenshot directories for new image files.
type ScreenshotSource struct{}

func NewScreenshotSource() *ScreenshotSource { return &ScreenshotSource{} }

func (s *ScreenshotSource) Name() string { return "screenshot" }

func (s *ScreenshotSource) Events(ctx context.Context) (<-chan event.Event, error) {
	home, _ := os.UserHomeDir()

	dirs := []string{
		filepath.Join(home, "Pictures", "Screenshots"),
		filepath.Join(home, "Pictures"),
		"/tmp",
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	watched := 0
	for _, d := range dirs {
		if _, err := os.Stat(d); err == nil {
			if err := w.Add(d); err == nil {
				watched++
			}
		}
	}

	if watched == 0 {
		w.Close()
		// No directories to watch; return a no-op channel.
		ch := make(chan event.Event)
		go func() { <-ctx.Done(); close(ch) }()
		return ch, nil
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
				if !isScreenshotFile(fe.Name) {
					continue
				}

				emit(ch, ctx, event.Event{
					Kind:   event.KindScreenshot,
					Source: s.Name(),
					Payload: map[string]any{
						"action": "captured",
						"type":   "unknown",
					},
					Timestamp: time.Now(),
				})
			case _, ok := <-w.Errors:
				if !ok {
					return
				}
			}
		}
	}()

	return ch, nil
}

// isScreenshotFile checks if a filename looks like a screenshot based on
// common naming conventions used by GNOME Screenshot, Flameshot, Spectacle,
// scrot, and similar tools.
func isScreenshotFile(path string) bool {
	base := strings.ToLower(filepath.Base(path))

	// Must be an image file.
	ext := filepath.Ext(base)
	switch ext {
	case ".png", ".jpg", ".jpeg", ".webp", ".bmp":
	default:
		return false
	}

	// Common screenshot name patterns.
	patterns := []string{
		"screenshot",
		"screen shot",
		"scrot",
		"flameshot",
		"spectacle",
		"capture",
	}
	for _, p := range patterns {
		if strings.Contains(base, p) {
			return true
		}
	}
	return false
}
