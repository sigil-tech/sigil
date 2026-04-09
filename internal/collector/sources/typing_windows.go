//go:build windows

package sources

import (
	"context"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/sigil-tech/sigil/internal/event"
)

var (
	typUser32          = syscall.NewLazyDLL("user32.dll")
	setWindowsHookExW  = typUser32.NewProc("SetWindowsHookExW")
	callNextHookEx     = typUser32.NewProc("CallNextHookEx")
	unhookWindowsHookEx = typUser32.NewProc("UnhookWindowsHookEx")
	getMessage         = typUser32.NewProc("GetMessageW")
)

const (
	whKeyboardLL = 13 // WH_KEYBOARD_LL
	wmKeyDown    = 0x0100
	wmSysKeyDown = 0x0104
)

// kbdLLHookStruct mirrors the KBDLLHOOKSTRUCT Windows structure.
// We only read the wParam to detect key-down events. We NEVER record
// the vkCode or scanCode to avoid capturing key identifiers.
type kbdLLHookStruct struct {
	vkCode      uint32
	scanCode    uint32
	flags       uint32
	time        uint32
	dwExtraInfo uintptr
}

// msgStruct mirrors the MSG structure for GetMessage.
type msgStruct struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      struct{ x, y int32 }
}

// TypingSource tracks keystroke rate on Windows using a low-level keyboard
// hook (WH_KEYBOARD_LL). It counts key-down events per 30-second window
// and emits the count. It NEVER captures key identifiers — only the total
// count of keystrokes.
type TypingSource struct {
	Window time.Duration // aggregation window, default 30s
}

// NewTypingSource creates a TypingSource.
func NewTypingSource(window time.Duration) *TypingSource {
	if window <= 0 {
		window = 30 * time.Second
	}
	return &TypingSource{Window: window}
}

func (s *TypingSource) Name() string { return "typing" }

func (s *TypingSource) Events(ctx context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event, 16)

	// Atomic counter for keystrokes; the hook callback increments it
	// and the ticker goroutine reads and resets it.
	var keyCount int64

	go func() {
		defer close(ch)

		// Install the low-level keyboard hook. This must run on a thread
		// with a message pump, so we run GetMessage in a loop.
		hookCallback := syscall.NewCallback(func(nCode int, wParam uintptr, lParam uintptr) uintptr {
			if nCode >= 0 {
				// Only count key-down events.
				if wParam == wmKeyDown || wParam == wmSysKeyDown {
					atomic.AddInt64(&keyCount, 1)
				}
			}
			ret, _, _ := callNextHookEx.Call(0, uintptr(nCode), wParam, lParam)
			return ret
		})

		hook, _, err := setWindowsHookExW.Call(whKeyboardLL, hookCallback, 0, 0)
		if hook == 0 {
			// If we can't install the hook (e.g., insufficient permissions),
			// log the error via the channel mechanism and exit gracefully.
			_ = err // hook installation failed
			return
		}
		defer unhookWindowsHookEx.Call(hook)

		// Start a goroutine to periodically emit keystroke counts.
		go func() {
			ticker := time.NewTicker(s.Window)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					count := atomic.SwapInt64(&keyCount, 0)
					if count == 0 {
						continue
					}
					select {
					case ch <- event.Event{
						Kind:   event.KindTyping,
						Source: s.Name(),
						Payload: map[string]any{
							"keystrokes":  int(count),
							"window_secs": int(s.Window.Seconds()),
						},
						Timestamp: time.Now(),
					}:
					case <-ctx.Done():
						return
					}
				}
			}
		}()

		// Message pump — required for WH_KEYBOARD_LL to receive events.
		// GetMessage blocks until a message is available or the thread is
		// asked to quit. We use a separate goroutine to post WM_QUIT when
		// the context is cancelled.
		go func() {
			<-ctx.Done()
			// Post WM_QUIT to break the GetMessage loop.
			postQuitMessage := typUser32.NewProc("PostThreadMessageW")
			tid := syscall.NewLazyDLL("kernel32.dll").NewProc("GetCurrentThreadId")
			threadID, _, _ := tid.Call()
			postQuitMessage.Call(threadID, 0x0012, 0, 0) // WM_QUIT = 0x0012
		}()

		var msg msgStruct
		for {
			ret, _, _ := getMessage.Call(
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
