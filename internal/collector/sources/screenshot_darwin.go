//go:build darwin

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

// ScreenshotSource detects screenshots on macOS by watching ~/Desktop
// for files matching the Screenshot pattern.
type ScreenshotSource struct{}

func NewScreenshotSource() *ScreenshotSource { return &ScreenshotSource{} }

func (s *ScreenshotSource) Name() string { return "screenshot" }

func (s *ScreenshotSource) Events(ctx context.Context) (<-chan event.Event, error) {
	home, _ := os.UserHomeDir()
	desktop := filepath.Join(home, "Desktop")

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := w.Add(desktop); err != nil {
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
				if !strings.HasPrefix(base, "Screenshot") && !strings.HasPrefix(base, "Screen Shot") {
					continue
				}

				captureType := "full_screen"
				if strings.Contains(base, "at") {
					captureType = "region" // best guess
				}

				emit(ch, ctx, event.Event{
					Kind:   event.KindScreenshot,
					Source: s.Name(),
					Payload: map[string]any{
						"action": "captured",
						"type":   captureType,
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
