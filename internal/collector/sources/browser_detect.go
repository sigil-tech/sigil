package sources

import "strings"

// knownBrowsers maps app names (as reported by the focus source) to browser
// identifiers. Case-insensitive matching via lowercase keys.
var knownBrowsers = map[string]string{
	"google chrome":   "chrome",
	"chrome":          "chrome",
	"chromium":        "chromium",
	"safari":          "safari",
	"firefox":         "firefox",
	"mozilla firefox": "firefox",
	"brave browser":   "brave",
	"brave":           "brave",
	"microsoft edge":  "edge",
	"msedge":          "edge",
	"arc":             "arc",
	"vivaldi":         "vivaldi",
	"opera":           "opera",
	// Linux process names
	"google-chrome":  "chrome",
	"brave-browser":  "brave",
	"microsoft-edge": "edge",
	// Windows process names
	"chrome.exe":  "chrome",
	"firefox.exe": "firefox",
	"msedge.exe":  "edge",
	"brave.exe":   "brave",
	"vivaldi.exe": "vivaldi",
	"opera.exe":   "opera",
}

// IsBrowser returns the browser identifier if appName is a known browser,
// or empty string if not.
func IsBrowser(appName string) string {
	return knownBrowsers[strings.ToLower(appName)]
}

// browserSuffixes are the suffixes browsers append to window titles.
// Ordered longest-first for correct matching.
var browserSuffixes = []string{
	" — Mozilla Firefox (Private Browsing)",
	" - Google Chrome (Incognito)",
	" - InPrivate - Microsoft Edge",
	" - Brave (Private)",
	" — Mozilla Firefox",
	" - Google Chrome",
	" - Microsoft Edge",
	" - Brave Browser",
	" - Brave",
	" - Vivaldi",
	" - Opera",
	" - Chromium",
}

// incognitoSuffixes are the window title suffixes for private browsing.
var incognitoSuffixes = []string{
	" — Mozilla Firefox (Private Browsing)",
	" - Google Chrome (Incognito)",
	" - InPrivate - Microsoft Edge",
	" - Brave (Private)",
}

// StripBrowserSuffix removes the browser name suffix from a window title,
// returning the page title. If no known suffix matches, returns the title as-is.
func StripBrowserSuffix(title string) string {
	for _, suffix := range browserSuffixes {
		if strings.HasSuffix(title, suffix) {
			return strings.TrimSuffix(title, suffix)
		}
	}
	return title
}

// IsIncognito returns true if the window title indicates private browsing.
func IsIncognito(title string) bool {
	for _, suffix := range incognitoSuffixes {
		if strings.HasSuffix(title, suffix) {
			return true
		}
	}
	return false
}
