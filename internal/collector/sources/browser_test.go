package sources

import "testing"

func TestIsBrowser(t *testing.T) {
	t.Parallel()

	tests := []struct {
		app  string
		want string
	}{
		{"Google Chrome", "chrome"},
		{"google chrome", "chrome"},
		{"Firefox", "firefox"},
		{"Mozilla Firefox", "firefox"},
		{"Safari", "safari"},
		{"Brave Browser", "brave"},
		{"Microsoft Edge", "edge"},
		{"Arc", "arc"},
		{"Vivaldi", "vivaldi"},
		{"Opera", "opera"},
		{"chrome.exe", "chrome"},
		{"firefox.exe", "firefox"},
		{"GoLand", ""},
		{"Terminal", ""},
		{"Finder", ""},
	}

	for _, tt := range tests {
		t.Run(tt.app, func(t *testing.T) {
			t.Parallel()
			got := IsBrowser(tt.app)
			if got != tt.want {
				t.Errorf("IsBrowser(%q) = %q, want %q", tt.app, got, tt.want)
			}
		})
	}
}

func TestStripBrowserSuffix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		title string
		want  string
	}{
		{"Pull request #76 · sigil-tech/sigil · GitHub - Google Chrome", "Pull request #76 · sigil-tech/sigil · GitHub"},
		{"React Docs — Mozilla Firefox", "React Docs"},
		{"Inbox - Gmail - Google Chrome", "Inbox - Gmail"},
		{"GitHub - Brave", "GitHub"},
		{"My Page - Microsoft Edge", "My Page"},
		{"untitled - Vivaldi", "untitled"},
		{"Some Page", "Some Page"},     // no suffix
		{"Safari page", "Safari page"}, // Safari has no suffix
	}

	for _, tt := range tests {
		t.Run(tt.title[:minInt(30, len(tt.title))], func(t *testing.T) {
			t.Parallel()
			got := StripBrowserSuffix(tt.title)
			if got != tt.want {
				t.Errorf("StripBrowserSuffix(%q) = %q, want %q", tt.title, got, tt.want)
			}
		})
	}
}

func TestIsIncognito(t *testing.T) {
	t.Parallel()

	tests := []struct {
		title string
		want  bool
	}{
		{"New Tab - Google Chrome (Incognito)", true},
		{"Page — Mozilla Firefox (Private Browsing)", true},
		{"Page - InPrivate - Microsoft Edge", true},
		{"Page - Brave (Private)", true},
		{"GitHub - Google Chrome", false},
		{"Docs — Mozilla Firefox", false},
	}

	for _, tt := range tests {
		t.Run(tt.title[:minInt(30, len(tt.title))], func(t *testing.T) {
			t.Parallel()
			got := IsIncognito(tt.title)
			if got != tt.want {
				t.Errorf("IsIncognito(%q) = %v, want %v", tt.title, got, tt.want)
			}
		})
	}
}

func TestDomainFromTitle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		title      string
		wantDomain string
	}{
		{"Pull request #76 · sigil-tech/sigil · GitHub", "github.com"},
		{"Inbox - Gmail", "mail.google.com"},
		{"React Hooks – docs.rs", "docs.rs"},
		{"issue #42 — pkg.go.dev", "pkg.go.dev"},
		{"Weekly Sync - Google Meet", "meet.google.com"},
		{"Budget Q4 - Google Sheets", "docs.google.com"},
		{"Deployment logs | Grafana", "grafana.com"},
		{"user/repo · GitLab", "gitlab.com"},
		{"How to fix X - Stack Overflow", "stackoverflow.com"},
		{"React.js — Wikipedia", "wikipedia.org"},
		{"Random Page Title", ""},
	}

	for _, tt := range tests {
		t.Run(tt.title[:minInt(30, len(tt.title))], func(t *testing.T) {
			t.Parallel()
			got := DomainFromTitle(tt.title)
			if got != tt.wantDomain {
				t.Errorf("DomainFromTitle(%q) = %q, want %q", tt.title, got, tt.wantDomain)
			}
		})
	}
}

func TestClassifyDomain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		domain       string
		wantCategory string
	}{
		{"github.com", "development"},
		{"stackoverflow.com", "development"},
		{"developer.mozilla.org", "documentation"},
		{"react.dev", "documentation"},
		{"docs.example.com", "documentation"},
		{"mail.google.com", "communication"},
		{"slack.com", "communication"},
		{"notion.so", "project_management"},
		{"linear.app", "project_management"},
		{"wikipedia.org", "research"},
		{"twitter.com", "social"},
		{"youtube.com", "entertainment"},
		{"meet.google.com", "meeting"},
		{"random-site.com", "other"},
	}

	for _, tt := range tests {
		t.Run(tt.domain, func(t *testing.T) {
			t.Parallel()
			got := ClassifyDomain(tt.domain)
			if got != tt.wantCategory {
				t.Errorf("ClassifyDomain(%q) = %q, want %q", tt.domain, got, tt.wantCategory)
			}
		})
	}
}
