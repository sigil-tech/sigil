# 024 — Cross-Platform Browser Signals: Tasks

**Status:** Draft
**Branch:** `feat/024-browser-signals`

---

## Tasks

### Phase 1: Browser Detection + Title Parsing

- [ ] **Task 1.1**: Implement `BrowserSource` struct and poll loop
  - Files: `internal/collector/sources/browser.go`
  - Test: `go test ./internal/collector/sources/ -run TestBrowserSource -count=1`
  - Depends: spec 023 Task 1.1 (KindBrowser constant)

- [ ] **Task 1.2**: Implement browser detection map + title suffix stripping [P]
  - `isBrowser(appName)`, `stripBrowserSuffix(title, appName)`
  - Cover: Chrome, Firefox, Safari, Edge, Brave, Arc, Vivaldi, Opera
  - Files: `internal/collector/sources/browser_detect.go`
  - Test: `go test ./internal/collector/sources/ -run "TestIsBrowser|TestStripSuffix" -count=1`
  - Depends: none

- [ ] **Task 1.3**: Platform window title readers [P]
  - macOS: reuse existing `windowTitle()` from focus_darwin.go
  - Linux: `_NET_WM_NAME` via X11 or compositor IPC
  - Windows: `GetWindowText` via syscall
  - Files: `internal/collector/sources/browser_darwin.go`, `browser_linux.go`, `browser_windows.go`
  - Test: `go build ./...`
  - Depends: none

- [ ] **Task 1.4**: Phase 1 verification
  - Test: `make check`
  - Depends: Task 1.1, 1.2, 1.3

### Phase 2: Domain Pattern Table

- [ ] **Task 2.1**: Build domain extraction from page title patterns
  - 100+ site patterns (GitHub, Gmail, Stack Overflow, LinkedIn, etc.)
  - O(1) map lookup by suffix
  - Files: `internal/collector/sources/browser_patterns.go`, `browser_patterns_test.go`
  - Test: `go test ./internal/collector/sources/ -run TestDomainFromTitle -count=1`
  - Depends: none

### Phase 3: AppleScript Enrichment (macOS)

- [ ] **Task 3.1**: Query Chrome/Safari active tab via AppleScript
  - `queryChrome(ctx)` and `querySafari(ctx)` returning title + domain
  - 500ms timeout, fallback to Tier 1 on failure
  - Domain extracted via `url.Parse().Hostname()`
  - Files: `internal/collector/sources/browser_darwin.go`
  - Test: `go test ./internal/collector/sources/ -run TestBrowserAppleScript -count=1`
  - Depends: Task 1.3

### Phase 4: Activity Classification

- [ ] **Task 4.1**: Domain → category classifier
  - Categories: development, documentation, communication, project_management, research, social, entertainment, other
  - Prefix map for subdomains + exact map for base domains
  - Files: `internal/collector/sources/browser_classify.go`, `browser_classify_test.go`
  - Test: `go test ./internal/collector/sources/ -run TestClassifyDomain -count=1`
  - Depends: none

### Phase 5: Privacy Controls

- [ ] **Task 5.1**: Domain blocklist + incognito detection
  - `isBlocked(domain, blocklist)`, `isIncognito(title, appName)`
  - Redact blocked domains: domain and title → `[blocked]`
  - Detect Chrome/Firefox/Edge/Brave incognito title patterns
  - Files: `internal/collector/sources/browser_privacy.go`, `browser_privacy_test.go`
  - Test: `go test ./internal/collector/sources/ -run "TestIsBlocked|TestIsIncognito" -count=1`
  - Depends: none

### Phase 6: Tab-Switch Detection

- [ ] **Task 6.1**: Poll-based tab switch detection
  - Track `lastTitle`, emit `tab_switch` when title changes within same browser
  - 2-second poll interval (configurable)
  - Files: `internal/collector/sources/browser.go` (enhance poll loop)
  - Test: `go test ./internal/collector/sources/ -run TestBrowserTabSwitch -count=1`
  - Depends: Task 1.1

### Phase 7: Source Registration

- [ ] **Task 7.1**: Register BrowserSource in all platform source files
  - Gate on `[sources.browser] enabled`
  - Files: `cmd/sigild/sources_darwin.go`, `sources_linux.go`, `sources_windows.go`
  - Test: `make check`
  - Depends: all previous

---

| Phase | Tasks | Effort |
|-------|-------|--------|
| 1 | 4 | 2 days |
| 2 | 1 | 0.5 days |
| 3 | 1 | 1 day |
| 4 | 1 | 0.5 days |
| 5 | 1 | 1 day |
| 6 | 1 | 1 day |
| 7 | 1 | 0.5 days |
| **Total** | **10** | **6.5 days** |
