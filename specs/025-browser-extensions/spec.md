# 025 — Browser Extensions for Rich Signal Collection

**Status:** Draft
**Author:** Alec Feeman
**Date:** 2026-04-08

---

## Problem

Spec 024 captures browser context via OS-level window title parsing. This gives us page title and domain for the active tab — enough to know "user is on GitHub" but not enough to understand browsing behavior in depth.

The window title approach has limitations:
- **No tab count** — can't detect context sprawl (40 open tabs)
- **No inactive tab awareness** — only sees the active tab, not what's open
- **No navigation timing** — can't measure time-on-page or reading duration
- **No domain without URL** — Tier 1 relies on pattern matching page titles to domains, which is lossy
- **Best-effort domain extraction** — Firefox and some sites don't include the domain in the title
- **No tab lifecycle** — can't see tab open/close/reload patterns

Browser extensions solve all of these. They run inside the browser with full access to the tab API. They complement the daemon's baseline signals with data that only the browser itself can provide.

## Goals

1. **Build extensions for Chrome, Firefox, and Safari** that push rich tab events to sigild's local ingest endpoint.
2. **Extensions live in `extensions/chrome/`, `extensions/firefox/`, `extensions/safari/`** alongside the existing `extensions/vscode/` and `extensions/jetbrains/`.
3. **Signals complement, never duplicate** the daemon's baseline browser detection. The daemon handles focus-level tracking; the extension adds tab-level depth.
4. **Manifest V3** for Chrome/Edge/Brave (all Chromium-based browsers share one extension). **WebExtension API** for Firefox. **Safari Web Extension** for Safari.

## Non-Goals

- Reading page content, DOM, or form inputs
- Injecting scripts into web pages
- Tracking browsing history beyond the current session
- Requiring the extension for basic browser detection (spec 024 handles that)
- Supporting Internet Explorer

---

## Design

### Extension Architecture

```
Browser Extension (runs in browser)
    │
    │  chrome.tabs.onActivated
    │  chrome.tabs.onUpdated
    │  chrome.tabs.onCreated
    │  chrome.tabs.onRemoved
    │  chrome.windows.onFocusChanged
    │
    ├──► Collect: tab ID, title, domain (extracted from URL), timestamp
    ├──► Filter: apply domain blocklist, skip incognito
    ├──► Batch: buffer events for 2 seconds to avoid flooding
    │
    └──► POST http://127.0.0.1:7775/ingest
              │
              ▼
         sigild plugin ingest endpoint
              │
              ▼
         Store as kind="browser" events
```

### Shared Core (`extensions/browser-shared/`)

A shared JavaScript/TypeScript module used by all three extensions:

```
extensions/
  browser-shared/        ← shared code
    src/
      collector.ts       ← tab event collection logic
      transport.ts       ← HTTP POST to sigild
      privacy.ts         ← domain blocklist, incognito filter
      classifier.ts      ← domain → category mapping
      types.ts           ← event type definitions
    package.json
  chrome/                ← Chrome/Edge/Brave (Manifest V3)
    manifest.json
    src/
      background.ts      ← service worker, imports browser-shared
      popup.html         ← settings UI (blocklist, connection status)
      popup.ts
    package.json
  firefox/               ← Firefox (WebExtension)
    manifest.json
    src/
      background.ts
      popup.html
      popup.ts
    package.json
  safari/                ← Safari Web Extension
    SigilSafari/
      manifest.json
      background.js
    SigilSafari.xcodeproj/
```

### Events Captured

| Event | Trigger | Payload |
|-------|---------|---------|
| `tab_activated` | User switches to a different tab | title, domain, tab_id |
| `tab_created` | New tab opened | title, domain, tab_id |
| `tab_closed` | Tab closed | tab_id, domain, time_open_seconds |
| `tab_updated` | Page navigation within a tab | title, domain, previous_domain |
| `tab_count` | Periodic (every 60s) | total_tabs, windows_count |
| `page_time` | Tab deactivated or closed | domain, title, active_seconds |

### Event Schema

```json
{
  "kind": "browser",
  "source": "chrome-extension",
  "payload": {
    "action": "tab_activated",
    "browser": "chrome",
    "tab_id": 42,
    "page_title": "Pull request #76 · sigil-tech/sigil",
    "domain": "github.com",
    "category": "development",
    "tab_count": 23,
    "active_seconds": 145,
    "previous_domain": "stackoverflow.com"
  }
}
```

### Privacy Controls

#### Domain Blocklist

Users configure which domains to exclude in the extension popup:

```
Blocked domains:
  mail.google.com
  banking.example.com
  + Add domain...
```

Blocked domains produce events with `domain: "[blocked]"` and `page_title: "[blocked]"` — the tab switch is recorded (for context-switch metrics) but content is redacted.

#### Incognito / Private Browsing

Extensions do NOT request incognito access. Chrome's Manifest V3 `"incognito": "not_allowed"` ensures the extension never runs in incognito windows. Firefox and Safari have equivalent settings.

#### Data Transmission

- Events are sent ONLY to `127.0.0.1:7775` (localhost) — never to any remote server
- If sigild is not running, events are dropped silently (no buffering to disk)
- The extension has NO remote fetch permissions — it cannot phone home

### Connection Status

The extension popup shows:

```
Sigil Browser Extension

Status: ● Connected to sigild
Events sent: 142 today
Blocked domains: 2 configured

[Manage Blocklist]
```

When sigild is not running:

```
Sigil Browser Extension

Status: ○ sigild not detected
Events will be captured when the daemon starts.
```

### Chrome Extension (Manifest V3)

```json
{
  "manifest_version": 3,
  "name": "Sigil",
  "version": "0.1.0",
  "description": "Workflow intelligence — captures browsing context for Sigil",
  "permissions": ["tabs", "activeTab"],
  "host_permissions": ["http://127.0.0.1:7775/*"],
  "background": {
    "service_worker": "background.js"
  },
  "action": {
    "default_popup": "popup.html",
    "default_icon": "icon-16.png"
  },
  "incognito": "not_allowed",
  "icons": {
    "16": "icon-16.png",
    "48": "icon-48.png",
    "128": "icon-128.png"
  }
}
```

**Permissions explained:**
- `tabs` — read tab title and URL (required for the core function)
- `activeTab` — access to the current tab when the user clicks the extension
- `host_permissions: 127.0.0.1:7775` — POST events to the local daemon only

**No other permissions.** No `<all_urls>`, no content scripts, no web requests, no storage sync.

### Firefox Extension

Same logic as Chrome but using the `browser.*` API namespace. Manifest V2 format (Firefox supports both but V2 is more stable for background scripts).

### Safari Extension

Safari Web Extensions use the same WebExtension API as Chrome/Firefox but require an Xcode project wrapper. Distributed via the Mac App Store or direct download.

---

## Requirements

### Functional

1. Chrome extension MUST capture tab_activated, tab_created, tab_closed, tab_updated, tab_count, and page_time events.
2. Firefox extension MUST capture the same events using the `browser.tabs` API.
3. Safari extension MUST capture the same events using the `browser.tabs` API.
4. All extensions MUST send events to `http://127.0.0.1:7775/ingest` via HTTP POST.
5. Events MUST be batched with a 2-second flush interval to avoid flooding sigild.
6. `page_time` events MUST accurately measure active time on a tab (time between activation and deactivation).
7. `tab_count` events MUST fire every 60 seconds when the browser is open.
8. Domain MUST be extracted from the URL's hostname — never store path, query, or fragment.
9. Domain blocklist MUST be configurable in the extension popup.
10. Incognito/private windows MUST be excluded — extension must not request incognito access.
11. When sigild is not running, events MUST be dropped silently (no queueing, no error UI).

### Cross-Browser

12. Chrome extension MUST use Manifest V3 with a service worker background.
13. The Chrome extension MUST also work in Edge, Brave, Vivaldi, and Opera (all Chromium-based).
14. Firefox extension MUST use the WebExtension API (Manifest V2 or V3 as appropriate).
15. Safari extension MUST be wrapped in an Xcode project for distribution.
16. Shared logic (`browser-shared/`) MUST be used by all three extensions to avoid duplication.

### Privacy

17. Extensions MUST request only `tabs` and `activeTab` permissions — nothing else.
18. Extensions MUST NOT request `<all_urls>`, content script injection, or web request interception.
19. Full URLs MUST never be transmitted — only the hostname is extracted and sent.
20. Extension source code MUST be open source in the `extensions/` directory.
21. Extension MUST NOT communicate with any server other than `127.0.0.1`.

### Performance

22. Extension memory usage MUST be < 10MB in Chrome.
23. Extension MUST NOT degrade browser performance (no content scripts, no page injection).
24. Event batching MUST limit HTTP requests to at most 1 per 2 seconds.

### Distribution

25. Chrome extension MUST be publishable to the Chrome Web Store.
26. Firefox extension MUST be publishable to Firefox Add-ons (addons.mozilla.org).
27. Safari extension MUST be buildable as a Safari Web Extension via Xcode.
28. All extensions MUST include a link to the source code in the description.

---

## Relationship to Spec 024

Spec 024 (daemon-native browser signals) and this spec (browser extensions) are complementary:

| Capability | Spec 024 (daemon) | Spec 025 (extension) |
|-----------|-------------------|---------------------|
| Detect browser is focused | Yes (window class) | No (extension doesn't know about other apps) |
| Page title of active tab | Yes (window title parsing) | Yes (more reliable) |
| Domain of active tab | Best-effort (pattern matching) | Yes (from URL) |
| Tab count | No | Yes |
| Inactive tabs | No | Yes |
| Tab open/close lifecycle | No | Yes |
| Time-on-page | No | Yes |
| Navigation within tab | No | Yes |
| Works without install | Yes | No (requires extension install) |
| Cross-platform | Yes | Yes |

When both are active, the extension's events take priority (more accurate). The daemon's baseline detection still fires for browsers without the extension installed, or when the extension is disabled.

---

## Success Criteria

29. Chrome extension installed → `sigilctl events --kind browser --source chrome-extension` shows tab switch events with domain and title.
30. Firefox extension installed → same, with `--source firefox-extension`.
31. User opens 30 tabs → `tab_count` event shows `tab_count: 30`.
32. User spends 5 minutes reading a page → `page_time` event shows `active_seconds: ~300`.
33. User adds `mail.google.com` to blocklist → Gmail tab switches produce `[blocked]` domain.
34. Incognito window opened → zero events from that window.
35. sigild not running → extension shows "not detected" status, no errors in console.
36. Chrome extension also works in Edge and Brave without modification.

---

## Implementation Priority

| Phase | What | Effort |
|-------|------|--------|
| 1 | `browser-shared/` core module (collector, transport, privacy, classifier) | 2 days |
| 2 | Chrome extension (Manifest V3, service worker, popup) | 2 days |
| 3 | Firefox extension (WebExtension) | 1 day (reuses shared) |
| 4 | Safari extension (Xcode wrapper) | 2 days |
| 5 | Chrome Web Store + Firefox Add-ons submission | 1 day |
| **Total** | | **8 days** |
