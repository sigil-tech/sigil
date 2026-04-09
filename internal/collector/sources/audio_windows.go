//go:build windows

package sources

import (
	"context"

	"github.com/sigil-tech/sigil/internal/event"
)

// AudioSource is a stub on Windows.
//
// TODO: Implement audio device monitoring. The proper approach is to use the
// Windows Core Audio API (IMMDeviceEnumerator via COM). This requires COM
// initialization (CoInitializeEx), IMMDeviceEnumerator::RegisterEndpointNotificationCallback,
// and implementing the IMMNotificationClient interface — all of which are
// non-trivial in pure Go without cgo.
//
// Possible alternatives:
//   - Use go-ole to interact with COM objects for IMMDeviceEnumerator.
//   - Shell out to PowerShell: Get-AudioDevice (requires AudioDeviceCmdlets module).
//   - Poll the registry for audio device changes under
//     HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\MMDevices\Audio\Render.
//
// Until implemented, this source compiles but emits no events.
type AudioSource struct{}

// NewAudioSource creates an AudioSource.
func NewAudioSource() *AudioSource { return &AudioSource{} }

func (s *AudioSource) Name() string { return "audio" }

func (s *AudioSource) Events(ctx context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event)
	go func() { <-ctx.Done(); close(ch) }()
	return ch, nil
}
