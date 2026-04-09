//go:build windows

package sources

import (
	"context"

	"github.com/sigil-tech/sigil/internal/event"
)

// CalendarSource is a no-op stub on Windows.
//
// TODO: Windows has no standard calendar API accessible without user
// authentication. Possible approaches for future implementation:
//   - Microsoft Graph API (requires OAuth2 and Azure AD app registration)
//     to read Outlook/Exchange calendar events.
//   - COM automation of Outlook via go-ole, if Outlook is installed.
//   - ICS file watching if the user exports their calendar.
//   - Integration with Google Calendar API for Google Workspace users.
//
// Until a suitable approach is chosen, this source compiles but emits no events.
type CalendarSource struct{}

// NewCalendarSource creates a CalendarSource.
func NewCalendarSource() *CalendarSource { return &CalendarSource{} }

func (s *CalendarSource) Name() string { return "calendar" }

func (s *CalendarSource) Events(ctx context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event)
	go func() { <-ctx.Done(); close(ch) }()
	return ch, nil
}
