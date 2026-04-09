//go:build linux

package sources

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/sigil-tech/sigil/internal/event"
)

// AudioSource detects audio output device changes and microphone activity
// on Linux by polling PulseAudio/PipeWire via pactl.
type AudioSource struct{}

func NewAudioSource() *AudioSource { return &AudioSource{} }

func (s *AudioSource) Name() string { return "audio" }

func (s *AudioSource) Events(ctx context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event, 8)

	go func() {
		defer close(ch)

		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		var lastSink string
		var lastMicActive bool

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sink := readDefaultSink()
				micActive := isMicActive()

				if lastSink != "" && sink != lastSink {
					emit(ch, ctx, event.Event{
						Kind:   event.KindAudio,
						Source: s.Name(),
						Payload: map[string]any{
							"action": "output_changed",
							"device": sink,
						},
						Timestamp: time.Now(),
					})
				}
				lastSink = sink

				if micActive != lastMicActive {
					action := "mic_activated"
					if !micActive {
						action = "mic_deactivated"
					}
					emit(ch, ctx, event.Event{
						Kind:   event.KindAudio,
						Source: s.Name(),
						Payload: map[string]any{
							"action": action,
						},
						Timestamp: time.Now(),
					})
				}
				lastMicActive = micActive
			}
		}
	}()

	return ch, nil
}

// readDefaultSink returns the name of the current default audio sink
// using pactl list sinks short.
func readDefaultSink() string {
	out, err := exec.Command("pactl", "list", "sinks", "short").Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			// Return the first sink name (typically the default/active one).
			return fields[1]
		}
	}
	return ""
}

// isMicActive checks if any source output (microphone stream) is active.
func isMicActive() bool {
	out, err := exec.Command("pactl", "list", "source-outputs", "short").Output()
	if err != nil {
		return false
	}
	// Any line in the output means at least one application is using the mic.
	return strings.TrimSpace(string(out)) != ""
}
