//go:build windows

package sources

import (
	"context"

	"github.com/sigil-tech/sigil/internal/event"
)

// DesktopSource is a stub on Windows.
//
// TODO: Implement virtual desktop tracking. The proper approach is to use
// the IVirtualDesktopManager COM interface, which requires:
//   - COM initialization (CoInitializeEx)
//   - CoCreateInstance with CLSID_VirtualDesktopManager
//   - IVirtualDesktopManager::GetWindowDesktopId to determine which desktop
//     the foreground window belongs to
//   - Polling or hooking for desktop switch notifications
//
// Possible alternatives:
//   - Use go-ole to interact with COM objects.
//   - Use undocumented Windows APIs from user32.dll
//     (GetCurrentDesktop / SwitchDesktop patterns).
//   - Monitor the registry key changes under
//     HKCU\SOFTWARE\Microsoft\Windows\CurrentVersion\Explorer\VirtualDesktops.
//
// Until implemented, this source compiles but emits no events.
type DesktopSource struct{}

// NewDesktopSource creates a DesktopSource.
func NewDesktopSource() *DesktopSource { return &DesktopSource{} }

func (s *DesktopSource) Name() string { return "desktop" }

func (s *DesktopSource) Events(ctx context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event)
	go func() { <-ctx.Done(); close(ch) }()
	return ch, nil
}
