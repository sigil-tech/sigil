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

// DownloadSource watches the Downloads folder for new files.
type DownloadSource struct {
	WatchDir string
}

func NewDownloadSource(watchDir string) *DownloadSource {
	if watchDir == "" {
		home, _ := os.UserHomeDir()
		watchDir = filepath.Join(home, "Downloads")
	} else if strings.HasPrefix(watchDir, "~/") {
		home, _ := os.UserHomeDir()
		watchDir = filepath.Join(home, watchDir[2:])
	}
	return &DownloadSource{WatchDir: watchDir}
}

func (s *DownloadSource) Name() string { return "download" }

func (s *DownloadSource) Events(ctx context.Context) (<-chan event.Event, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := w.Add(s.WatchDir); err != nil {
		w.Close()
		return nil, err
	}

	ch := make(chan event.Event, 16)

	go func() {
		defer w.Close()
		defer close(ch)

		// Debounce: track recently seen files.
		seen := make(map[string]time.Time)

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
				// Debounce: skip if seen in the last 5 seconds.
				if t, ok := seen[fe.Name]; ok && time.Since(t) < 5*time.Second {
					continue
				}
				seen[fe.Name] = time.Now()

				ext := strings.ToLower(filepath.Ext(fe.Name))
				var size int64
				if info, err := os.Stat(fe.Name); err == nil {
					size = info.Size()
				}

				emit(ch, ctx, event.Event{
					Kind:   event.KindDownload,
					Source: s.Name(),
					Payload: map[string]any{
						"action":     "completed",
						"extension":  ext,
						"size_bytes": size,
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
