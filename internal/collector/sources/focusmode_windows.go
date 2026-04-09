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
	fmAdvapi32    = syscall.NewLazyDLL("advapi32.dll")
	regOpenKeyExW = fmAdvapi32.NewProc("RegOpenKeyExW")
	regQueryValue = fmAdvapi32.NewProc("RegQueryValueExW")
	regCloseKey   = fmAdvapi32.NewProc("RegCloseKey")
)

const (
	hkeyCurrentUser      = 0x80000001
	keyRead              = 0x20019
	errorSuccess         = 0
	regBinary            = 3
)

// Focus Assist registry path under HKCU. This key stores the Focus Assist
// (formerly Quiet Hours) enforcement state as a binary blob. When Focus
// Assist is active, this key's data contains specific byte patterns.
var focusAssistKeyPath = syscall.StringToUTF16Ptr(
	`Software\Microsoft\Windows\CurrentVersion\CloudStore\Store\DefaultAccount\Current\default$windows.data.notifications.quiethoursenforcement\windows.data.notifications.quiethoursenforcement`,
)

// FocusModeSource detects Windows Focus Assist (Do Not Disturb) state
// changes by polling the registry. It emits events when Focus Assist
// is enabled or disabled.
type FocusModeSource struct{}

// NewFocusModeSource creates a FocusModeSource.
func NewFocusModeSource() *FocusModeSource { return &FocusModeSource{} }

func (s *FocusModeSource) Name() string { return "focus_mode" }

func (s *FocusModeSource) Events(ctx context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event, 8)

	go func() {
		defer close(ch)

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		var lastActive *bool

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				active := isFocusAssistActive()

				if lastActive != nil && *lastActive == active {
					continue
				}

				action := "disabled"
				mode := "off"
				if active {
					action = "enabled"
					mode = "focus_assist"
				}

				select {
				case ch <- event.Event{
					Kind:   event.KindFocusMode,
					Source: s.Name(),
					Payload: map[string]any{
						"action": action,
						"mode":   mode,
					},
					Timestamp: time.Now(),
				}:
				case <-ctx.Done():
					return
				}

				lastActive = &active
			}
		}
	}()

	return ch, nil
}

// isFocusAssistActive reads the Focus Assist registry key and determines
// whether Focus Assist is currently enabled. The registry value is a binary
// blob; when Focus Assist is active, the data at offset 0x10 (16) contains
// a non-zero value indicating the mode (1=priority, 2=alarms only).
func isFocusAssistActive() bool {
	var hKey syscall.Handle
	ret, _, _ := regOpenKeyExW.Call(
		uintptr(hkeyCurrentUser),
		uintptr(unsafe.Pointer(focusAssistKeyPath)),
		0,
		keyRead,
		uintptr(unsafe.Pointer(&hKey)),
	)
	if ret != errorSuccess {
		return false
	}
	defer regCloseKey.Call(uintptr(hKey))

	// Read the "Data" value.
	valueName := syscall.StringToUTF16Ptr("Data")
	var dataType uint32
	var dataSize uint32

	// First call to get the size.
	ret, _, _ = regQueryValue.Call(
		uintptr(hKey),
		uintptr(unsafe.Pointer(valueName)),
		0,
		uintptr(unsafe.Pointer(&dataType)),
		0,
		uintptr(unsafe.Pointer(&dataSize)),
	)
	if ret != errorSuccess || dataSize == 0 {
		return false
	}

	data := make([]byte, dataSize)
	ret, _, _ = regQueryValue.Call(
		uintptr(hKey),
		uintptr(unsafe.Pointer(valueName)),
		0,
		uintptr(unsafe.Pointer(&dataType)),
		uintptr(unsafe.Pointer(&data[0])),
		uintptr(unsafe.Pointer(&dataSize)),
	)
	if ret != errorSuccess {
		return false
	}

	// The Focus Assist state is encoded at offset 16 in the binary blob.
	// A non-zero byte at this position indicates Focus Assist is active.
	if len(data) > 16 {
		return data[16] != 0
	}
	return false
}
