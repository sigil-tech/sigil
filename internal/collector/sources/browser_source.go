package sources

import (
	"context"
	"net/url"
	"time"

	"github.com/sigil-tech/sigil/internal/event"
)

// BrowserSource polls the frontmost browser's active tab for title/domain
// changes and emits browser events. It complements the focus source — the
// focus source detects app-level switches; this detects tab-level changes.
type BrowserSource struct {
	PollInterval   time.Duration
	BlockedDomains []string

	// Platform-specific title reader, set by the platform init code.
	// Returns (pageTitle, rawURL, error). rawURL may be empty on Tier 1.
	ReadActiveTab func(ctx context.Context, appName string) (title, rawURL string, err error)
}

// NewBrowserSource creates a BrowserSource with the given config.
func NewBrowserSource(pollInterval time.Duration, blocked []string) *BrowserSource {
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}
	return &BrowserSource{
		PollInterval:   pollInterval,
		BlockedDomains: blocked,
	}
}

func (s *BrowserSource) Name() string { return "browser" }

func (s *BrowserSource) Events(ctx context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event, 32)

	go func() {
		defer close(ch)

		ticker := time.NewTicker(s.PollInterval)
		defer ticker.Stop()

		var lastApp, lastTitle, lastDomain string

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				app := frontApp()
				browserID := IsBrowser(app)
				if browserID == "" {
					lastApp = app
					continue
				}

				var title, rawURL string
				if s.ReadActiveTab != nil {
					pollCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
					title, rawURL, _ = s.ReadActiveTab(pollCtx, app)
					cancel()
				}

				// Fall back to window title parsing if no direct query.
				if title == "" {
					title = windowTitle(app)
				}

				if title == "" {
					continue
				}

				// Check incognito — drop entirely.
				if IsIncognito(title) {
					continue
				}

				pageTitle := StripBrowserSuffix(title)

				// Extract domain.
				var domain string
				if rawURL != "" {
					if u, err := url.Parse(rawURL); err == nil {
						domain = u.Hostname()
					}
				}
				if domain == "" {
					domain = DomainFromTitle(pageTitle)
				}

				// Check blocklist.
				blocked := false
				for _, bd := range s.BlockedDomains {
					if domain == bd {
						blocked = true
						break
					}
				}

				if blocked {
					pageTitle = "[blocked]"
					domain = "[blocked]"
				}

				category := ""
				if domain != "" && domain != "[blocked]" {
					category = ClassifyDomain(domain)
				}

				// Determine if this is a new event.
				isNewApp := app != lastApp
				isNewTab := pageTitle != lastTitle || domain != lastDomain

				if !isNewApp && !isNewTab {
					continue
				}

				action := "tab_switch"
				if isNewApp {
					action = "focus"
				}

				e := event.Event{
					Kind:   event.KindBrowser,
					Source: s.Name(),
					Payload: map[string]any{
						"action":          action,
						"browser_name":    browserID,
						"page_title":      pageTitle,
						"domain":          domain,
						"category":        category,
						"previous_domain": lastDomain,
					},
					Timestamp: time.Now(),
				}

				emit(ch, ctx, e)

				lastApp = app
				lastTitle = pageTitle
				lastDomain = domain
			}
		}
	}()

	return ch, nil
}
