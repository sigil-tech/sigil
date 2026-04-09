//go:build windows

package sources

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/sigil-tech/sigil/internal/event"
)

var (
	ptrUser32            = syscall.NewLazyDLL("user32.dll")
	ptrSetWindowsHookEx  = ptrUser32.NewProc("SetWindowsHookExW")
	ptrCallNextHookEx    = ptrUser32.NewProc("CallNextHookEx")
	ptrUnhookWindowsHook = ptrUser32.NewProc("UnhookWindowsHookEx")
	ptrGetMessage        = ptrUser32.NewProc("GetMessageW")
)

const (
	whMouseLL    = 14     // WH_MOUSE_LL
	wmLButtonDn  = 0x0201 // WM_LBUTTONDOWN
	wmRButtonDn  = 0x0204 // WM_RBUTTONDOWN
	wmMButtonDn  = 0x0207 // WM_MBUTTONDOWN
	wmMouseWheel = 0x020A // WM_MOUSEWHEEL
	wmMouseMove  = 0x0200 // WM_MOUSEMOVE
)

// msllHookStruct mirrors the MSLLHOOKSTRUCT Windows structure.
type msllHookStruct struct {
	ptX         int32
	ptY         int32
	mouseData   uint32
	flags       uint32
	time        uint32
	dwExtraInfo uintptr
}

// PointerSource tracks mouse/trackpad activity on Windows using a low-level
// mouse hook (WH_MOUSE_LL). It aggregates click count, scroll distance, and
// movement distance per window interval, then emits a summary event.
type PointerSource struct {
	Window time.Duration // aggregation window, default 30s
}

// NewPointerSource creates a PointerSource.
func NewPointerSource(window time.Duration) *PointerSource {
	if window <= 0 {
		window = 30 * time.Second
	}
	return &PointerSource{Window: window}
}

func (s *PointerSource) Name() string { return "pointer" }

// pointerMetrics tracks aggregate mouse metrics. Access is protected by
// atomic operations for counters and a mutex for position tracking.
type pointerMetrics struct {
	clicks   int64
	scrolls  int64
	moveDist int64 // accumulated pixel distance, stored as int (truncated)

	mu    sync.Mutex
	lastX int32
	lastY int32
	hasPt bool // true once the first mouse position is seen
}

func (s *PointerSource) Events(ctx context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event, 16)
	metrics := &pointerMetrics{}

	go func() {
		defer close(ch)

		hookCallback := syscall.NewCallback(func(nCode int, wParam uintptr, lParam uintptr) uintptr {
			if nCode >= 0 && lParam != 0 {
				info := (*msllHookStruct)(unsafe.Pointer(lParam))
				switch wParam {
				case wmLButtonDn, wmRButtonDn, wmMButtonDn:
					atomic.AddInt64(&metrics.clicks, 1)
				case wmMouseWheel:
					// mouseData high word is the wheel delta (typically 120 per notch).
					delta := int16(info.mouseData >> 16)
					if delta < 0 {
						delta = -delta
					}
					atomic.AddInt64(&metrics.scrolls, int64(delta)/120)
				case wmMouseMove:
					metrics.mu.Lock()
					if metrics.hasPt {
						dx := float64(info.ptX - metrics.lastX)
						dy := float64(info.ptY - metrics.lastY)
						dist := math.Sqrt(dx*dx + dy*dy)
						atomic.AddInt64(&metrics.moveDist, int64(dist))
					}
					metrics.lastX = info.ptX
					metrics.lastY = info.ptY
					metrics.hasPt = true
					metrics.mu.Unlock()
				}
			}
			ret, _, _ := ptrCallNextHookEx.Call(0, uintptr(nCode), wParam, lParam)
			return ret
		})

		hook, _, err := ptrSetWindowsHookEx.Call(whMouseLL, hookCallback, 0, 0)
		if hook == 0 {
			_ = err
			return
		}
		defer ptrUnhookWindowsHook.Call(hook)

		// Ticker goroutine to emit periodic summaries.
		go func() {
			ticker := time.NewTicker(s.Window)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					clicks := atomic.SwapInt64(&metrics.clicks, 0)
					scrolls := atomic.SwapInt64(&metrics.scrolls, 0)
					moveDist := atomic.SwapInt64(&metrics.moveDist, 0)

					if clicks == 0 && scrolls == 0 && moveDist == 0 {
						continue
					}

					select {
					case ch <- event.Event{
						Kind:   event.KindPointer,
						Source: s.Name(),
						Payload: map[string]any{
							"clicks":        int(clicks),
							"scroll_notches": int(scrolls),
							"move_pixels":   int(moveDist),
							"window_secs":   int(s.Window.Seconds()),
						},
						Timestamp: time.Now(),
					}:
					case <-ctx.Done():
						return
					}
				}
			}
		}()

		// Break the message pump when context is cancelled.
		go func() {
			<-ctx.Done()
			postQuitMessage := ptrUser32.NewProc("PostThreadMessageW")
			tid := syscall.NewLazyDLL("kernel32.dll").NewProc("GetCurrentThreadId")
			threadID, _, _ := tid.Call()
			postQuitMessage.Call(threadID, 0x0012, 0, 0) // WM_QUIT
		}()

		// Message pump — required for WH_MOUSE_LL to receive events.
		var msg msgStruct
		for {
			ret, _, _ := ptrGetMessage.Call(
				uintptr(unsafe.Pointer(&msg)),
				0, 0, 0,
			)
			if ret == 0 || ctx.Err() != nil {
				return
			}
		}
	}()

	return ch, nil
}
