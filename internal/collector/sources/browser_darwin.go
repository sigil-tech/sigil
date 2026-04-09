//go:build darwin

package sources

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// ReadActiveTabDarwin queries Chrome or Safari via AppleScript for the
// active tab's title and URL. Returns empty strings for unsupported browsers.
func ReadActiveTabDarwin(ctx context.Context, appName string) (title, rawURL string, err error) {
	switch {
	case strings.Contains(strings.ToLower(appName), "chrome"):
		return queryChromeDarwin(ctx)
	case strings.EqualFold(appName, "Safari"):
		return querySafariDarwin(ctx)
	default:
		return "", "", nil
	}
}

func queryChromeDarwin(ctx context.Context) (string, string, error) {
	script := `tell application "Google Chrome" to get {title, URL} of active tab of front window`
	out, err := exec.CommandContext(ctx, "osascript", "-e", script).Output()
	if err != nil {
		return "", "", fmt.Errorf("chrome applescript: %w", err)
	}
	// Output format: "Page Title, https://example.com/path"
	parts := strings.SplitN(strings.TrimSpace(string(out)), ", ", 2)
	if len(parts) < 2 {
		return strings.TrimSpace(string(out)), "", nil
	}
	return parts[0], parts[1], nil
}

func querySafariDarwin(ctx context.Context) (string, string, error) {
	script := `tell application "Safari" to get {name, URL} of current tab of front window`
	out, err := exec.CommandContext(ctx, "osascript", "-e", script).Output()
	if err != nil {
		return "", "", fmt.Errorf("safari applescript: %w", err)
	}
	parts := strings.SplitN(strings.TrimSpace(string(out)), ", ", 2)
	if len(parts) < 2 {
		return strings.TrimSpace(string(out)), "", nil
	}
	return parts[0], parts[1], nil
}
