# 024 — Cross-Platform Browser Signals: Implementation Plan

**Spec:** `specs/024-browser-signals/spec.md`
**Branch:** `feat/024-browser-signals`
**Depends on:** Spec 023 Phase 1 (event kinds + config schema — adds `KindBrowser` and `BrowserSourceConfig`)

---

## Pre-Implementation Gates

### DAG Gate
All code lives in `internal/collector/sources/`. No new packages. The browser source implements the existing `collector.Source` interface and emits `event.Event` values.

### Interface Gate
No new interfaces. Browser detection, title parsing, domain extraction, and classification are internal functions within the source files.

### Privacy Gate
- Domain only — never full URLs. URL path/query stripped at extraction time.
- Domain blocklist for redaction. Incognito windows excluded.
- `sigilctl export` and fleet reporting exclude raw domains — only category aggregates.

### Simplicity Gate
Three layers, each independent: (1) browser detection via known process names, (2) title/domain extraction via OS APIs, (3) classification via map lookup. No complex state machines.

---

## Technical Design

### New Files

```
internal/collector/sources/
  browser.go              ← shared types, browser registry, Source struct
  browser_detect.go       ← isBrowser() map, browser suffix stripping
  browser_patterns.go     ← page title → domain pattern table (100+ entries)
  browser_classify.go     ← domain → category map
  browser_privacy.go      ← blocklist check, incognito detection
  browser_darwin.go       ← AppleScript Tier 2 + CGWindowList titles
  browser_linux.go        ← X11 _NET_WM_NAME + Wayland IPC
  browser_windows.go      ← GetForegroundWindow + GetWindowText
  browser_test.go         ← unit tests for parsing/classification
  browser_patterns_test.go ← exhaustive pattern table tests
```

### Integration with Existing Focus Source

The browser source does NOT replace `DarwinFocusSource` / `LinuxFocusSource`. Instead:

1. Focus source continues emitting `KindHyprland` events with `window_class` + `window_title`
2. Browser source runs alongside, polling the focused window when `window_class` matches a known browser
3. Browser source emits `KindBrowser` events with enriched `page_title`, `domain`, `category`
4. Both events are stored — focus gives app-level context, browser gives page-level context

### Domain Extraction Pipeline

```
Tier 2 (macOS Chrome/Safari): AppleScript → URL → net/url.Parse → hostname
    ↓ (on failure)
Tier 1 (all platforms): window title → strip browser suffix → pattern match → domain
    ↓ (on no match)
Emit event with domain="" (page_title still populated)
```

### Tab-Switch Detection

Poll the active window title every 2 seconds. When the title changes but the focused app is the same browser → emit a `tab_switch` browser event. This catches in-browser navigation without needing extension-level tab APIs.

---

## Implementation Phases

### Phase 1: Browser Detection + Title Parsing

**Goal:** When a browser is focused, extract the page title from the window title on all platforms.

**Files:**
- `browser.go` — `BrowserSource` struct, `Source` interface impl, poll loop
- `browser_detect.go` — `isBrowser(appName string) bool`, `stripBrowserSuffix(title, appName string) string`
- `browser_darwin.go` — read window title via existing `windowTitle()` func from focus_darwin.go, plus AppleScript for Chrome/Safari
- `browser_linux.go` — read window title via `_NET_WM_NAME` (X11) or compositor IPC
- `browser_windows.go` — `GetWindowText` via syscall

**Tests:**
```go
func TestIsBrowser(t *testing.T) {
    tests := []struct{ app string; want bool }{
        {"Google Chrome", true},
        {"Firefox", true}, // Note: window_class may be "firefox" on Linux
        {"Safari", true},
        {"GoLand", false},
        {"Terminal", false},
    }
}

func TestStripBrowserSuffix(t *testing.T) {
    tests := []struct{ title, app, want string }{
        {"GitHub - Google Chrome", "Google Chrome", "GitHub"},
        {"GitHub — Mozilla Firefox", "Firefox", "GitHub"},
        {"GitHub", "Safari", "GitHub"},  // Safari has no suffix
        {"GitHub - Brave", "Brave Browser", "GitHub"},
    }
}
```

**Verification:**
```bash
go build ./... && go test ./internal/collector/sources/ -run "TestIsBrowser|TestStripBrowserSuffix" -count=1
```

---

### Phase 2: Domain Pattern Table

**Goal:** Map page titles to domains for the 100 most common sites.

**Files:**
- `browser_patterns.go` — `domainFromTitle(title string) string` using a `map[string]string` of title suffixes/patterns to domains

**Pattern examples:**
```go
var titleDomainPatterns = map[string]string{
    " · GitHub":           "github.com",
    " · GitLab":           "gitlab.com",
    " - Stack Overflow":   "stackoverflow.com",
    " - Gmail":            "mail.google.com",
    " | LinkedIn":         "linkedin.com",
    " - YouTube":          "youtube.com",
    " / Twitter":          "twitter.com",
    " — Wikipedia":        "wikipedia.org",
    " | Notion":           "notion.so",
    " - Jira":             "jira.atlassian.com",
    " - Linear":           "linear.app",
    " - Figma":            "figma.com",
    // ... 90+ more
}
```

**Lookup:** Iterate suffixes (longest first) — O(k) where k is pattern count. Pre-sort by length for early match.

**Tests:**
```go
func TestDomainFromTitle(t *testing.T) {
    tests := []struct{ title, wantDomain string }{
        {"Pull request #76 · sigil-tech/sigil · GitHub", "github.com"},
        {"Inbox - Gmail", "mail.google.com"},
        {"Some Random Page", ""},  // no match
    }
}
```

**Verification:**
```bash
go test ./internal/collector/sources/ -run TestDomainFromTitle -count=1
```

---

### Phase 3: AppleScript Enrichment (macOS)

**Goal:** When Chrome or Safari is focused on macOS, get the actual URL and extract the domain.

**Files:**
- `browser_darwin.go` — `queryChrome(ctx) (title, domain, error)`, `querySafari(ctx) (title, domain, error)` using `osascript`

**Timeout:** 500ms per AppleScript call. On failure, fall back to Tier 1 title parsing.

**URL → domain extraction:**
```go
func domainFromURL(rawURL string) string {
    u, err := url.Parse(rawURL)
    if err != nil {
        return ""
    }
    return u.Hostname()
}
```

**Verification:**
```bash
go build ./... && go test ./internal/collector/sources/ -run TestBrowserDarwin -count=1
# Manual: open Chrome, check sigilctl events --kind browser
```

---

### Phase 4: Activity Classification

**Goal:** Map domains to activity categories.

**Files:**
- `browser_classify.go` — `classifyDomain(domain string) string`

**Implementation:** Prefix-match map for subdomains, exact-match map for base domains.

```go
// Prefix matches (subdomains)
var domainPrefixCategories = map[string]string{
    "docs.":      "documentation",
    "developer.": "documentation",
    "api.":       "documentation",
    "meet.":      "meeting",
    "mail.":      "communication",
}

// Exact matches
var domainCategories = map[string]string{
    "github.com":          "development",
    "stackoverflow.com":   "development",
    "slack.com":           "communication",
    "notion.so":           "project_management",
    "youtube.com":         "entertainment",
    // ...
}
```

**Tests:** Table-driven with 30+ cases covering every category.

**Verification:**
```bash
go test ./internal/collector/sources/ -run TestClassifyDomain -count=1
```

---

### Phase 5: Privacy Controls

**Goal:** Domain blocklist and incognito detection.

**Files:**
- `browser_privacy.go` — `isBlocked(domain string, blocklist []string) bool`, `isIncognito(title, appName string) bool`

**Incognito patterns:**
```go
var incognitoPatterns = []string{
    " - Google Chrome (Incognito)",
    " — Mozilla Firefox (Private Browsing)",
    " - InPrivate - Microsoft Edge",
    " - Brave (Private)",
}
```

**Tests:**
```go
func TestIsBlocked(t *testing.T) {
    blocklist := []string{"mail.google.com", "banking.example.com"}
    tests := []struct{ domain string; want bool }{
        {"mail.google.com", true},
        {"github.com", false},
    }
}

func TestIsIncognito(t *testing.T) {
    tests := []struct{ title, app string; want bool }{
        {"New Tab - Google Chrome (Incognito)", "Google Chrome", true},
        {"GitHub - Google Chrome", "Google Chrome", false},
    }
}
```

**Verification:**
```bash
go test ./internal/collector/sources/ -run "TestIsBlocked|TestIsIncognito" -count=1
```

---

### Phase 6: Tab-Switch Detection

**Goal:** Detect when the user changes tabs within the same browser by polling the window title.

**Files:**
- `browser.go` — enhance the poll loop: track `lastTitle`, emit `tab_switch` event when title changes but `window_class` stays the same browser

**State:**
```go
type BrowserSource struct {
    cfg         config.BrowserSourceConfig
    interval    time.Duration
    lastApp     string
    lastTitle   string
    lastDomain  string
}
```

**Verification:**
```bash
go build ./... && go test ./internal/collector/sources/ -run TestBrowserTabSwitch -count=1
# Manual: switch tabs in Chrome, check sigilctl events --kind browser
```

---

### Phase 7: Source Registration

**Goal:** Register `BrowserSource` in `addPlatformSources` on all platforms, gated by config.

**Files:**
- `cmd/sigild/sources_darwin.go` — add `BrowserSource`
- `cmd/sigild/sources_linux.go` — add `BrowserSource`
- `cmd/sigild/sources_windows.go` — add `BrowserSource`

**Verification:**
```bash
make check
# Manual: verify browser events appear in sigilctl events
```

---

## Testing Strategy

- All parsing/matching functions: table-driven tests with `t.Run`, `t.Parallel()`
- AppleScript calls: test with mock `exec.Command` using `RunCmd` pattern
- Integration: real events through collector → store → query back
- 100+ test cases for the domain pattern table
- Platform-specific tests guarded by build tags

---

## Summary

| Phase | What | Effort |
|-------|------|--------|
| 1 | Browser detection + title parsing | 2 days |
| 2 | Domain pattern table (100+ sites) | 0.5 days |
| 3 | AppleScript enrichment (macOS) | 1 day |
| 4 | Activity classification | 0.5 days |
| 5 | Privacy controls | 1 day |
| 6 | Tab-switch detection | 1 day |
| 7 | Source registration | 0.5 days |
| **Total** | | **6.5 days** |
