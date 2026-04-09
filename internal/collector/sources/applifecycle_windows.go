//go:build windows

package sources

import (
	"context"
	"syscall"
	"time"
	"unsafe"

	"github.com/sigil-tech/sigil/internal/event"
)

var (
	alcKernel32              = syscall.NewLazyDLL("kernel32.dll")
	createToolhelp32Snapshot = alcKernel32.NewProc("CreateToolhelp32Snapshot")
	process32FirstW          = alcKernel32.NewProc("Process32FirstW")
	process32NextW           = alcKernel32.NewProc("Process32NextW")
	alcCloseHandle           = alcKernel32.NewProc("CloseHandle")

	alcUser32        = syscall.NewLazyDLL("user32.dll")
	enumWindows      = alcUser32.NewProc("EnumWindows")
	isWindowVisible  = alcUser32.NewProc("IsWindowVisible")
	alcGetWindowTPI  = alcUser32.NewProc("GetWindowThreadProcessId")
)

const (
	th32csSnapProcess = 0x00000002
	maxPath           = 260
	invalidHandleVal  = ^uintptr(0) // INVALID_HANDLE_VALUE
)

// processEntry32W mirrors the PROCESSENTRY32W Windows structure.
type processEntry32W struct {
	dwSize              uint32
	cntUsage            uint32
	th32ProcessID       uint32
	th32DefaultHeapID   uintptr
	th32ModuleID        uint32
	cntThreads          uint32
	th32ParentProcessID uint32
	pcPriClassBase      int32
	dwFlags             uint32
	szExeFile           [maxPath]uint16
}

// AppLifecycleSource detects GUI application launches and exits on Windows
// by polling the process list via CreateToolhelp32Snapshot. Only processes
// with at least one visible window are considered GUI applications.
type AppLifecycleSource struct{}

// NewAppLifecycleSource creates an AppLifecycleSource.
func NewAppLifecycleSource() *AppLifecycleSource { return &AppLifecycleSource{} }

func (s *AppLifecycleSource) Name() string { return "app_lifecycle" }

type alcTrackedProc struct {
	name    string
	startAt time.Time
}

func (s *AppLifecycleSource) Events(ctx context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event, 32)

	go func() {
		defer close(ch)

		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		known := make(map[uint32]alcTrackedProc)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				current := scanGUIProcesses()

				// Detect new processes.
				for pid, name := range current {
					if _, exists := known[pid]; !exists {
						known[pid] = alcTrackedProc{name: name, startAt: time.Now()}
						select {
						case ch <- event.Event{
							Kind:   event.KindAppLifecycle,
							Source: s.Name(),
							Payload: map[string]any{
								"action": "launched",
								"app":    name,
								"pid":    int(pid),
							},
							Timestamp: time.Now(),
						}:
						case <-ctx.Done():
							return
						}
					}
				}

				// Detect exited processes.
				for pid, proc := range known {
					if _, exists := current[pid]; !exists {
						duration := time.Since(proc.startAt).Seconds()
						delete(known, pid)
						select {
						case ch <- event.Event{
							Kind:   event.KindAppLifecycle,
							Source: s.Name(),
							Payload: map[string]any{
								"action":           "exited",
								"app":              proc.name,
								"pid":              int(pid),
								"duration_seconds": int(duration),
							},
							Timestamp: time.Now(),
						}:
						case <-ctx.Done():
							return
						}
					}
				}
			}
		}
	}()

	return ch, nil
}

// scanGUIProcesses enumerates all processes and returns those that have at
// least one visible window, filtering to likely GUI applications.
func scanGUIProcesses() map[uint32]string {
	result := make(map[uint32]string)

	// Get the set of PIDs that own at least one visible window.
	guiPIDs := visibleWindowPIDs()

	// Snapshot all processes.
	handle, _, _ := createToolhelp32Snapshot.Call(th32csSnapProcess, 0)
	if handle == invalidHandleVal {
		return result
	}
	defer alcCloseHandle.Call(handle)

	var entry processEntry32W
	entry.dwSize = uint32(unsafe.Sizeof(entry))

	ret, _, _ := process32FirstW.Call(handle, uintptr(unsafe.Pointer(&entry)))
	if ret == 0 {
		return result
	}

	for {
		pid := entry.th32ProcessID
		if _, isGUI := guiPIDs[pid]; isGUI {
			name := syscall.UTF16ToString(entry.szExeFile[:])
			if name != "" {
				result[pid] = name
			}
		}

		entry.dwSize = uint32(unsafe.Sizeof(entry))
		ret, _, _ = process32NextW.Call(handle, uintptr(unsafe.Pointer(&entry)))
		if ret == 0 {
			break
		}
	}

	return result
}

// visibleWindowPIDs enumerates all top-level windows and returns the set of
// process IDs that own at least one visible window.
func visibleWindowPIDs() map[uint32]struct{} {
	pids := make(map[uint32]struct{})

	// EnumWindows callback receives each top-level window handle.
	callback := syscall.NewCallback(func(hwnd uintptr, lParam uintptr) uintptr {
		visible, _, _ := isWindowVisible.Call(hwnd)
		if visible == 0 {
			return 1 // continue enumeration
		}

		var pid uint32
		alcGetWindowTPI.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		if pid != 0 {
			pids[pid] = struct{}{}
		}
		return 1 // continue enumeration
	})

	enumWindows.Call(callback, 0)
	return pids
}
