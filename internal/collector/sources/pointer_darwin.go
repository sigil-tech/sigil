//go:build darwin

package sources

/*
#cgo CFLAGS: -x objective-c -mmacosx-version-min=10.14
#cgo LDFLAGS: -framework CoreGraphics -framework Foundation -framework ApplicationServices

#include <CoreGraphics/CoreGraphics.h>
#include <ApplicationServices/ApplicationServices.h>
#include <math.h>

static volatile int64_t clickCount = 0;
static volatile int64_t scrollDist = 0;
static volatile int64_t moveDist = 0;
static volatile double lastX = -1;
static volatile double lastY = -1;

CGEventRef mouseCallback(CGEventTapProxy proxy, CGEventType type, CGEventRef event, void *refcon) {
    switch (type) {
    case kCGEventLeftMouseDown:
    case kCGEventRightMouseDown:
    case kCGEventOtherMouseDown:
        __sync_add_and_fetch(&clickCount, 1);
        break;
    case kCGEventScrollWheel: {
        int64_t dy = CGEventGetIntegerValueField(event, kCGScrollWheelEventDeltaAxis1);
        __sync_add_and_fetch(&scrollDist, dy > 0 ? dy : -dy);
        break;
    }
    case kCGEventMouseMoved: {
        CGPoint pt = CGEventGetLocation(event);
        double lx = lastX, ly = lastY;
        if (lx >= 0 && ly >= 0) {
            double dx = pt.x - lx;
            double dy = pt.y - ly;
            int64_t dist = (int64_t)sqrt(dx*dx + dy*dy);
            __sync_add_and_fetch(&moveDist, dist);
        }
        lastX = pt.x;
        lastY = pt.y;
        break;
    }
    default:
        break;
    }
    if (type == kCGEventTapDisabledByTimeout || type == kCGEventTapDisabledByUserInput) {
        CGEventTapEnable((CFMachPortRef)refcon, true);
    }
    return event;
}

int startMouseTap() {
    CGEventMask mask = CGEventMaskBit(kCGEventLeftMouseDown)
                     | CGEventMaskBit(kCGEventRightMouseDown)
                     | CGEventMaskBit(kCGEventOtherMouseDown)
                     | CGEventMaskBit(kCGEventScrollWheel)
                     | CGEventMaskBit(kCGEventMouseMoved);
    CFMachPortRef tap = CGEventTapCreate(
        kCGSessionEventTap,
        kCGHeadInsertEventTap,
        kCGEventTapOptionListenOnly,
        mask,
        mouseCallback,
        NULL
    );
    if (!tap) return 0;
    CGEventTapEnable(tap, true);
    CFRunLoopSourceRef src = CFMachPortCreateRunLoopSource(kCFAllocatorDefault, tap, 0);
    CFRunLoopAddSource(CFRunLoopGetMain(), src, kCFRunLoopCommonModes);
    CFRelease(src);
    return 1;
}

// readAndResetMouse returns clicks, scroll, movement and resets all counters.
void readAndResetMouse(int64_t *clicks, int64_t *scroll, int64_t *move) {
    *clicks = __sync_lock_test_and_set(&clickCount, 0);
    *scroll = __sync_lock_test_and_set(&scrollDist, 0);
    *move   = __sync_lock_test_and_set(&moveDist, 0);
}
*/
import "C"

import (
	"context"
	"sync"
	"time"

	"github.com/sigil-tech/sigil/internal/event"
)

// PointerSource tracks mouse/trackpad activity on macOS via CGEventTap.
// Captures click count, scroll distance, and movement distance — never
// coordinates or click targets.
type PointerSource struct {
	Window time.Duration
}

func NewPointerSource(window time.Duration) *PointerSource {
	if window <= 0 {
		window = 30 * time.Second
	}
	return &PointerSource{Window: window}
}

func (s *PointerSource) Name() string { return "pointer" }

var mouseTapOnce sync.Once
var mouseTapOK bool

func (s *PointerSource) Events(ctx context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event, 16)

	go func() {
		defer close(ch)

		mouseTapOnce.Do(func() {
			mouseTapOK = C.startMouseTap() == 1
		})

		if !mouseTapOK {
			emit(ch, ctx, event.Event{
				Kind:   event.KindPointer,
				Source: s.Name(),
				Payload: map[string]any{
					"error": "Accessibility permission required for pointer tracking",
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
				var clicks, scroll, move C.int64_t
				C.readAndResetMouse(&clicks, &scroll, &move)

				if clicks == 0 && scroll == 0 && move == 0 {
					continue
				}

				app := currentFrontApp()

				emit(ch, ctx, event.Event{
					Kind:   event.KindPointer,
					Source: s.Name(),
					Payload: map[string]any{
						"window_seconds":  int(s.Window.Seconds()),
						"clicks":          int(clicks),
						"scroll_distance": int(scroll),
						"movement_pixels": int(move),
						"active_app":      app,
					},
					Timestamp: time.Now(),
				})
			}
		}
	}()

	return ch, nil
}
