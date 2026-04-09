//go:build darwin

package sources

/*
#cgo CFLAGS: -x objective-c -mmacosx-version-min=10.14
#cgo LDFLAGS: -framework CoreGraphics -framework Foundation -framework ApplicationServices

#include <CoreGraphics/CoreGraphics.h>
#include <ApplicationServices/ApplicationServices.h>

// Shared counter incremented by the event tap callback.
// Accessed atomically from Go via the exported functions below.
static volatile int64_t keystrokeCount = 0;

CGEventRef keyCallback(CGEventTapProxy proxy, CGEventType type, CGEventRef event, void *refcon) {
    if (type == kCGEventKeyDown) {
        __sync_add_and_fetch(&keystrokeCount, 1);
    }
    // Re-enable the tap if it gets disabled (system does this under load).
    if (type == kCGEventTapDisabledByTimeout || type == kCGEventTapDisabledByUserInput) {
        CGEventTapEnable((CFMachPortRef)refcon, true);
    }
    return event; // pass the event through unmodified
}

// startKeyTap installs a passive event tap for key-down events.
// Returns 0 on failure (e.g. no Accessibility permission).
int startKeyTap() {
    CGEventMask mask = CGEventMaskBit(kCGEventKeyDown);
    CFMachPortRef tap = CGEventTapCreate(
        kCGSessionEventTap,
        kCGHeadInsertEventTap,
        kCGEventTapOptionListenOnly, // passive — does not block or modify events
        mask,
        keyCallback,
        NULL
    );
    if (!tap) {
        return 0; // no Accessibility permission
    }
    // Pass the tap ref to the callback so it can re-enable itself.
    CGEventTapEnable(tap, true);

    CFRunLoopSourceRef src = CFMachPortCreateRunLoopSource(kCFAllocatorDefault, tap, 0);
    CFRunLoopAddSource(CFRunLoopGetMain(), src, kCFRunLoopCommonModes);
    CFRelease(src);
    // tap is intentionally not released — it must stay alive for the process lifetime.
    return 1;
}

int64_t readAndResetKeystrokes() {
    return __sync_lock_test_and_set(&keystrokeCount, 0);
}
*/
import "C"

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/sigil-tech/sigil/internal/event"
)

// TypingSource measures keystroke rate on macOS via a passive CGEventTap.
// It captures keystroke COUNT only — never individual key identifiers.
// Requires Accessibility permission.
type TypingSource struct {
	Window time.Duration // aggregation window (default: 30s)
}

func NewTypingSource(window time.Duration) *TypingSource {
	if window <= 0 {
		window = 30 * time.Second
	}
	return &TypingSource{Window: window}
}

func (s *TypingSource) Name() string { return "typing" }

var typingTapOnce sync.Once
var typingTapOK bool

func (s *TypingSource) Events(ctx context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event, 16)

	go func() {
		defer close(ch)

		// Install the event tap once (must be on the main thread's run loop,
		// but CGEventTapCreate works from any thread — the run loop source
		// is added to CFRunLoopGetMain which Wails already runs).
		typingTapOnce.Do(func() {
			typingTapOK = C.startKeyTap() == 1
		})

		if !typingTapOK {
			// No Accessibility permission. Emit one error event and exit.
			emit(ch, ctx, event.Event{
				Kind:   event.KindTyping,
				Source: s.Name(),
				Payload: map[string]any{
					"error": "Accessibility permission required for typing velocity",
				},
				Timestamp: time.Now(),
			})
			return
		}

		ticker := time.NewTicker(s.Window)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				count := int64(C.readAndResetKeystrokes())
				if count == 0 {
					continue // no keystrokes — don't emit noise
				}

				windowSec := s.Window.Seconds()
				kpm := float64(count) / windowSec * 60.0

				// Best-effort: get frontmost app.
				app := currentFrontApp()

				emit(ch, ctx, event.Event{
					Kind:   event.KindTyping,
					Source: s.Name(),
					Payload: map[string]any{
						"keys_per_minute": int(kpm),
						"window_seconds":  int(windowSec),
						"active_app":      app,
					},
					Timestamp: time.Now(),
				})
			}
		}
	}()

	return ch, nil
}

// currentFrontApp returns the frontmost app name. Uses the same lsappinfo
// approach as DarwinFocusSource.
func currentFrontApp() string {
	asnOut, err := exec.Command("lsappinfo", "front").Output()
	if err != nil {
		return ""
	}
	asn := strings.TrimSpace(string(asnOut))
	if asn == "" {
		return ""
	}
	infoOut, err := exec.Command("lsappinfo", "info", "-only", "name", asn).Output()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(infoOut))
	if !strings.Contains(line, "=") {
		return ""
	}
	parts := strings.SplitN(line, "=", 2)
	if len(parts) != 2 {
		return ""
	}
	return strings.Trim(parts[1], "\"")
}
