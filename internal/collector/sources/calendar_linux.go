//go:build linux

package sources

import (
	"context"

	"github.com/sigil-tech/sigil/internal/event"
)

// CalendarSource is a no-op stub on Linux. There is no standard
// calendar API on Linux desktop environments (unlike macOS EventKit).
type CalendarSource struct{}

func NewCalendarSource() *CalendarSource { return &CalendarSource{} }

func (s *CalendarSource) Name() string { return "calendar" }

func (s *CalendarSource) Events(_ context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event)
	close(ch)
	return ch, nil
}
