# 024 — Cross-Platform Browser Signals

**Status:** Draft
**Author:** Alec Feeman
**Date:** 2026-04-08

---

## Problem

Browser activity is the single largest blind spot in sigild's data collection. Knowledge workers spend 60%+ of their day in a browser — researching, reading documentation, reviewing PRs, responding to email, managing tickets. Sigil currently captures that a browser is focused but nothing about what the user is doing in it.

## Goals

1. **Capture browser context on all platforms** (macOS, Windows, Linux) and for all major browsers without requiring the user to install anything.

2. **Extract page title and domain** from the active browser tab using OS-level APIs — window title parsing (all platforms), supplemented by AppleScript (macOS Chrome/Safari) for richer data.

3. **Classify browser activity** into categories: research, communication, documentation, social, entertainment, development tools — enabling ML models to understand how time is split.

4. **Respect privacy**: capture domain and page title only. Never capture full URLs, page content, form inputs, or browsing history. Provide a domain blocklist for excluding sensitive sites.

## Non-Goals

- Building a browser extension (that's a future enhancement for deeper signals like tab count)
- Capturing browsing history or full URLs
- Reading page content or DOM
- Tracking incognito/private browsing windows
- Real-time content analysis

---

## Design

### Tier 1: Window Title Parsing (All Platforms, All Browsers)

Every browser puts the page title in the window title. This works for every browser on every OS with no special permissions (except Accessibility on macOS for window titles).

**Window title formats by browser:**

| Browser | Window title format |
|---------|-------------------|
| Chrome | `{page title} - Google Chrome` |
| Firefox | `{page title} — Mozilla Firefox` |
| Safari | `{page title}` (no suffix) |
| Edge | `{page title} - Microsoft Edge` |
| Brave | `{page title} - Brave` |
| Arc | `{page title}` |
| Vivaldi | `{page title} - Vivaldi` |
| Opera | `{page title} - Opera` |

**Implementation:** Parse the window title when the focused app is a known browser. Strip the browser suffix to get the page title. Extract the domain from the page title when possible (many pages include it: "Pull request #76 · sigil-tech/sigil · GitHub").

**Platform APIs for reading window titles:**

| Platform | API | Permission needed |
|----------|-----|------------------|
| macOS | `CGWindowListCopyWindowInfo` or AppleScript `System Events` | Accessibility |
| Windows | `GetForegroundWindow` + `GetWindowText` (Win32) | None |
| Linux (X11) | `XGetInputFocus` + `XFetchName` / `_NET_WM_NAME` | None |
| Linux (Wayland) | Compositor-specific (Hyprland IPC, Sway IPC, GNOME Shell D-Bus) | None |

### Tier 2: AppleScript Direct Query (macOS only, Chrome + Safari)

When the frontmost app is Chrome or Safari on macOS, query the browser directly via AppleScript for the active tab's URL (to extract domain) and title (more reliable than window title parsing).

```applescript
-- Chrome
tell application "Google Chrome" to get {title, URL} of active tab of front window

-- Safari
tell application "Safari" to get {name, URL} of current tab of front window
```

This gives us the actual URL (from which we extract the domain only — never store the full URL) and a clean page title without needing to parse the window title.

**Fallback:** If AppleScript fails (browser not running, permission denied), fall back to Tier 1 window title parsing.

### Browser Detection

**Known browser process names:**

```
macOS:     Google Chrome, Safari, Firefox, Brave Browser, Microsoft Edge,
           Arc, Vivaldi, Opera, Chromium
Windows:   chrome.exe, firefox.exe, msedge.exe, brave.exe, vivaldi.exe,
           opera.exe, iexplore.exe
Linux:     google-chrome, firefox, chromium, brave-browser, microsoft-edge,
           vivaldi, opera
```

The focus source already captures `window_class` (app name). When `window_class` matches a known browser, the event is enriched with browser-specific fields.

### Event Schema

**Enhanced `hyprland` (focus) event when browser is frontmost:**

```json
{
  "action": "focus",
  "window_class": "Google Chrome",
  "window_title": "Pull request #76 · sigil-tech/sigil · GitHub",
  "browser": {
    "page_title": "Pull request #76 · sigil-tech/sigil",
    "domain": "github.com",
    "category": "development"
  }
}
```

**Standalone `browser` event on tab switch within same browser:**

```json
{
  "kind": "browser",
  "source": "browser",
  "payload": {
    "action": "tab_switch",
    "browser_name": "Google Chrome",
    "page_title": "React Hooks Documentation",
    "domain": "react.dev",
    "category": "documentation",
    "previous_domain": "github.com"
  }
}
```

The `browser` event kind fires when the user switches tabs within the same browser (the focused app doesn't change, so no `hyprland` event fires, but the page context changed).

### Domain Extraction

From URL (Tier 2 — macOS Chrome/Safari):
```
https://mail.google.com/mail/u/0/#inbox → mail.google.com
```

From page title (Tier 1 — all platforms, best-effort):
```
"Pull request #76 · sigil-tech/sigil · GitHub" → github.com (known site pattern)
"React Hooks – React" → react.dev (known site pattern)
"Inbox - Gmail" → mail.google.com (known site pattern)
```

A mapping of ~100 common site title patterns to domains covers the most valuable cases. When no pattern matches, `domain` is empty and only `page_title` is populated.

### Activity Classification

Classify domains into categories for ML features:

| Category | Example domains |
|----------|----------------|
| `development` | github.com, gitlab.com, bitbucket.org, stackoverflow.com |
| `documentation` | docs.*, developer.*, man7.org, pkg.go.dev, react.dev |
| `communication` | mail.google.com, outlook.live.com, slack.com, teams.microsoft.com |
| `project_management` | linear.app, jira.atlassian.com, notion.so, asana.com |
| `research` | scholar.google.com, arxiv.org, wikipedia.org |
| `social` | twitter.com, linkedin.com, reddit.com, news.ycombinator.com |
| `entertainment` | youtube.com, netflix.com, twitch.tv |
| `other` | anything not matched |

Classification is best-effort and happens at event time. Users can override via config.

### Privacy Controls

**Domain blocklist** — configurable list of domains that are never recorded:

```toml
[sources.browser]
enabled = true
blocked_domains = ["mail.google.com", "banking.example.com"]
poll_interval = "2s"
```

When the active tab's domain matches a blocked entry, the event is emitted with `domain: "[blocked]"` and `page_title: "[blocked]"` — the transition is still recorded (for context-switch detection) but the content is redacted.

**Incognito/Private detection:** On macOS, Chrome's incognito windows have a different window title pattern ("- Google Chrome (Incognito)"). Private windows are never recorded — events are dropped entirely.

---

## Requirements

### Functional

1. When a known browser is the focused application, the focus event MUST include `browser.page_title` and `browser.domain` (when extractable).
2. When the user switches tabs within a browser, a `browser` event MUST be emitted with the new tab's title and domain.
3. Tab-switch detection MUST poll at most every 2 seconds (configurable via `[sources.browser] poll_interval`).
4. Domain extraction from URLs (Tier 2) MUST strip the path and query — only scheme + host are used, and only host is stored.
5. Domain extraction from page titles (Tier 1) MUST use a pattern table of at least 50 common sites.
6. Browser activity MUST be classified into one of: development, documentation, communication, project_management, research, social, entertainment, other.
7. Blocked domains MUST be redacted in the event payload — domain and page_title replaced with `[blocked]`.
8. Incognito/private browsing windows MUST be detected and excluded entirely — no event emitted.
9. The browser source MUST work on macOS, Windows, and Linux.
10. On macOS without Accessibility permission, the source MUST degrade gracefully: browser is detected via `window_class` but `page_title` and `domain` are empty.

### Cross-Platform Implementation

11. macOS: Use `CGWindowListCopyWindowInfo` for window titles (cgo, requires Accessibility). Fall back to AppleScript for Chrome/Safari URL extraction.
12. Windows: Use `GetForegroundWindow` + `GetWindowText` via syscall. No special permissions needed.
13. Linux/X11: Use `XGetInputFocus` + `_NET_WM_NAME` via X11 protocol. No special permissions needed.
14. Linux/Wayland: Use compositor IPC (existing Hyprland source pattern). Support Hyprland, Sway, GNOME Shell.

### Performance

15. Browser polling MUST NOT increase daemon RSS by more than 2MB.
16. Window title reads MUST timeout after 200ms (matching existing focus source timeout).
17. Domain pattern matching MUST be O(1) via map lookup, not O(n) regex scan.
18. AppleScript calls MUST timeout after 500ms and fall back to window title parsing on failure.

### Privacy

19. Full URLs MUST never be stored in the event payload — domain only.
20. Page content MUST never be accessed or stored.
21. Blocked domains MUST be configurable via `[sources.browser] blocked_domains`.
22. Incognito/private windows MUST be excluded.
23. The browser source MUST be disableable via `[sources.browser] enabled` (default: true).
24. `sigilctl export` and fleet reporting MUST NOT include browser domains — only category aggregates.

---

## Success Criteria

25. On macOS with Accessibility enabled and Chrome frontmost, `sigilctl events --kind browser` shows page title and domain for the active tab.
26. On macOS with Safari frontmost, same result via AppleScript Tier 2.
27. On macOS with Firefox frontmost, page title extracted from window title (Tier 1), domain via pattern matching.
28. On Windows with Chrome frontmost, page title extracted from Win32 window title.
29. On Linux/X11 with Firefox frontmost, page title extracted from _NET_WM_NAME.
30. Tab switches within a browser produce `browser` events with the new tab's context.
31. A blocked domain produces an event with `[blocked]` for title and domain.
32. An incognito Chrome window produces no browser events.
33. Browser poll adds < 2MB RSS and < 1% CPU on the reference hardware (2017 MacBook Pro).

---

## Implementation Priority

| Phase | What | Platform | Effort |
|-------|------|----------|--------|
| 1 | Window title parsing + browser detection | All (enhance existing focus source) | 2 days |
| 2 | Domain pattern table (50+ sites) | All | 0.5 days |
| 3 | AppleScript enrichment for Chrome/Safari | macOS | 1 day |
| 4 | Activity classification | All | 0.5 days |
| 5 | Privacy controls (blocklist, incognito detection) | All | 1 day |
| 6 | Tab-switch detection (poll within focused browser) | All | 1 day |
| **Total** | | | **6 days** |

Phase 1 alone (window title parsing) delivers the majority of value — we immediately know "user is reading React docs" or "user is in Gmail" on every platform.
