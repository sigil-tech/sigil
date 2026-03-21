package inference

import (
	"io"
	"log/slog"
)

// testLogger returns a logger that discards all output. Used across all
// test files in this package.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// containsPathComponent reports whether any slash-delimited segment of path
// equals component.
func containsPathComponent(path, component string) bool {
	for {
		i := len(path)
		for i > 0 && path[i-1] != '/' {
			i--
		}
		seg := path[i:]
		if seg == component {
			return true
		}
		if i == 0 {
			return false
		}
		path = path[:i-1]
	}
}
