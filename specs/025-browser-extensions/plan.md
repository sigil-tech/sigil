# 025 — Browser Extensions: Implementation Plan

**Spec:** `specs/025-browser-extensions/spec.md`
**Branch:** `feat/025-browser-extensions`
**Depends on:** Spec 023 Phase 1 (event kinds), sigild HTTP ingest endpoint (already exists at `:7775`)

---

## Pre-Implementation Gates

### DAG Gate
No Go code changes. Extensions are pure TypeScript/JavaScript living in `extensions/`. They POST events to sigild's existing HTTP ingest endpoint. The daemon already stores `KindBrowser` events from spec 023/024.

### Interface Gate
The only "interface" is the HTTP ingest endpoint. The event payload schema must match what the daemon expects:

```json
{
  "kind": "browser",
  "source": "chrome-extension",
  "payload": { ... }
}
```

The existing plugin ingest endpoint at `127.0.0.1:7775` accepts this format.

### Privacy Gate
- Extensions request only `tabs` + `activeTab` — no content scripts, no web requests, no `<all_urls>`
- Domain extracted from URL hostname — path/query stripped in the extension before transmission
- Domain blocklist configurable in extension popup
- Incognito: `"incognito": "not_allowed"` in manifest — extension never loads in private windows
- Communication only to `127.0.0.1` — extension has no remote fetch permissions

### Simplicity Gate
Shared core library (`browser-shared/`) avoids duplicating logic across three extensions. Each browser-specific extension is a thin wrapper (~50 lines) around the shared core.

---

## Technical Design

### Directory Structure

```
extensions/
  browser-shared/              ← shared TypeScript library
    src/
      collector.ts             ← tab event collection (listen + poll)
      transport.ts             ← HTTP POST to sigild with batching
      privacy.ts               ← domain extraction, blocklist, incognito
      classifier.ts            ← domain → category mapping
      types.ts                 ← event type definitions
    tsconfig.json
    package.json
  chrome/                      ← Chrome/Edge/Brave/Vivaldi/Opera
    manifest.json              ← Manifest V3
    src/
      background.ts            ← service worker entry
      popup.html               ← settings popup
      popup.ts                 ← popup logic
    icons/
      icon-16.png
      icon-48.png
      icon-128.png
    package.json
    tsconfig.json
    vite.config.ts             ← builds background.js + popup.js
  firefox/                     ← Firefox
    manifest.json              ← Manifest V2 (MV3 support incomplete in FF)
    src/
      background.ts
      popup.html
      popup.ts
    icons/
    package.json
    tsconfig.json
    vite.config.ts
  safari/                      ← Safari Web Extension
    SigilSafari/
      manifest.json
      Resources/
        background.js
        popup.html
        popup.js
        icons/
    SigilSafari.xcodeproj/
    README.md
```

### Shared Core API

```typescript
// types.ts
interface BrowserEvent {
  action: "tab_activated" | "tab_created" | "tab_closed" | "tab_updated" | "tab_count" | "page_time";
  browser: string;
  tab_id?: number;
  page_title: string;
  domain: string;
  category: string;
  tab_count?: number;
  active_seconds?: number;
  previous_domain?: string;
}

// collector.ts
class TabCollector {
  constructor(config: CollectorConfig);
  start(): void;           // registers browser event listeners
  stop(): void;            // removes listeners
  onEvent(cb: (e: BrowserEvent) => void): void;
}

// transport.ts
class SigilTransport {
  constructor(endpoint: string, batchIntervalMs: number);
  send(event: BrowserEvent): void;  // buffers, flushes on interval
  isConnected(): boolean;           // last POST succeeded
  getStats(): { sent: number; dropped: number };
}

// privacy.ts
function extractDomain(url: string): string;     // hostname only
function isBlocked(domain: string, blocklist: string[]): boolean;
function redactEvent(event: BrowserEvent): BrowserEvent;

// classifier.ts
function classifyDomain(domain: string): string;  // same map as Go side
```

### Ingest Endpoint Compatibility

The existing plugin ingest at `127.0.0.1:7775` expects:

```json
{
  "kind": "browser",
  "source": "chrome-extension",
  "payload": { ... },
  "ts": 1712345678000
}
```

The extension's `SigilTransport` batches events and POSTs them as a JSON array. The daemon's ingest handler already accepts this format — verify and adapt if needed.

**Potential issue:** The current ingest handler (`cmd/sigild/main.go` lines ~1256) expects shell command fields (`cmd`, `exit_code`, `cwd`). We need to check if it can accept generic event payloads or if it needs a new endpoint.

<function_calls>
Let me check...

The existing ingest handler is terminal-command specific. We need either:
(a) A generic `plugin-ingest` HTTP endpoint (already exists for plugins), or
(b) Extend the socket `ingest` method to accept any event kind

The plugin HTTP ingest at `:7775` is the right path — it's already used by `sigil-plugin-github` and `sigil-plugin-jetbrains`.

### Page Time Tracking

```typescript
// In collector.ts
class TabCollector {
  private activatedAt: Map<number, number> = new Map();  // tabId → timestamp

  onTabActivated(tabId: number) {
    // Emit page_time for the previously active tab
    const prev = this.activatedAt.get(this.lastActiveTab);
    if (prev) {
      this.emit({
        action: "page_time",
        active_seconds: Math.round((Date.now() - prev) / 1000),
        ...previousTabInfo
      });
    }
    this.activatedAt.set(tabId, Date.now());
    this.lastActiveTab = tabId;
  }
}
```

### Build System

Each extension uses Vite to bundle TypeScript → JavaScript:

```json
// chrome/package.json
{
  "scripts": {
    "build": "vite build",
    "watch": "vite build --watch",
    "package": "npm run build && cd dist && zip -r ../sigil-chrome.zip ."
  }
}
```

The Makefile gets new targets:

```makefile
## build-extension-chrome: build the Chrome browser extension.
build-extension-chrome:
	@cd extensions/chrome && npm install && npm run build

## build-extension-firefox: build the Firefox browser extension.
build-extension-firefox:
	@cd extensions/firefox && npm install && npm run build

## build-extensions: build all browser extensions.
build-extensions: build-extension-chrome build-extension-firefox
```

Safari requires Xcode and is macOS-only.

---

## Implementation Phases

### Phase 1: Shared Core Library

**Goal:** `extensions/browser-shared/` with all shared logic — collector, transport, privacy, classifier, types.

**Files:**
- `extensions/browser-shared/src/types.ts`
- `extensions/browser-shared/src/collector.ts`
- `extensions/browser-shared/src/transport.ts`
- `extensions/browser-shared/src/privacy.ts`
- `extensions/browser-shared/src/classifier.ts`
- `extensions/browser-shared/package.json`
- `extensions/browser-shared/tsconfig.json`

**Tests:**
- `privacy.test.ts` — domain extraction, blocklist, redaction
- `classifier.test.ts` — domain → category for 30+ domains
- `transport.test.ts` — batching, connection detection

**Verification:**
```bash
cd extensions/browser-shared && npm install && npm test
```

---

### Phase 2: Chrome Extension

**Goal:** Working Chrome extension that captures tab events and sends to sigild.

**Files:**
- `extensions/chrome/manifest.json` — Manifest V3
- `extensions/chrome/src/background.ts` — service worker: create `TabCollector` + `SigilTransport`, wire events
- `extensions/chrome/src/popup.html` — connection status, blocklist UI
- `extensions/chrome/src/popup.ts` — popup logic
- `extensions/chrome/icons/` — Sigil icons at 16/48/128px
- `extensions/chrome/package.json`
- `extensions/chrome/tsconfig.json`
- `extensions/chrome/vite.config.ts`

**Background service worker (~80 lines):**
```typescript
import { TabCollector, SigilTransport, classifyDomain, extractDomain, isBlocked } from "browser-shared";

const transport = new SigilTransport("http://127.0.0.1:7775/ingest", 2000);
const collector = new TabCollector({ pollInterval: 60000 });

collector.onEvent((event) => {
  // Privacy: extract domain, check blocklist
  const domain = extractDomain(event.url);
  if (isBlocked(domain, config.blockedDomains)) {
    event.domain = "[blocked]";
    event.page_title = "[blocked]";
  } else {
    event.domain = domain;
    event.category = classifyDomain(domain);
  }
  transport.send(event);
});

collector.start();
```

**Popup (~100 lines):**
- Shows connection status (green dot / gray dot)
- Shows events sent today (count)
- Domain blocklist management (add/remove)
- Settings stored in `chrome.storage.local`

**Verification:**
```bash
cd extensions/chrome && npm install && npm run build
# Load unpacked extension in Chrome → chrome://extensions
# Navigate to a few sites → check sigilctl events --kind browser
```

---

### Phase 3: Firefox Extension

**Goal:** Firefox extension using the same shared core.

**Files:**
- `extensions/firefox/manifest.json` — Manifest V2 with `browser.*` API
- `extensions/firefox/src/background.ts` — same logic as Chrome, `browser.*` namespace
- `extensions/firefox/src/popup.html`
- `extensions/firefox/src/popup.ts`
- `extensions/firefox/package.json`
- `extensions/firefox/vite.config.ts`

**Key differences from Chrome:**
- `browser.tabs.*` instead of `chrome.tabs.*`
- `browser.storage.local` instead of `chrome.storage.local`
- Background script (persistent) instead of service worker
- Manifest V2 format

**Verification:**
```bash
cd extensions/firefox && npm install && npm run build
# Load temporary add-on in Firefox → about:debugging
# Navigate → check events
```

---

### Phase 4: Safari Extension

**Goal:** Safari Web Extension wrapped in an Xcode project.

**Files:**
- `extensions/safari/SigilSafari/manifest.json`
- `extensions/safari/SigilSafari/Resources/background.js` — compiled from shared core
- `extensions/safari/SigilSafari/Resources/popup.html`
- `extensions/safari/SigilSafari.xcodeproj/`

**Key differences:**
- Safari Web Extensions require a native app container (Xcode project)
- Uses `browser.*` API namespace (same as Firefox)
- Distribution via Mac App Store or direct Xcode build

**Verification:**
```bash
cd extensions/safari && open SigilSafari.xcodeproj
# Build + run in Xcode → enable in Safari preferences → test
```

---

### Phase 5: Daemon Ingest Compatibility

**Goal:** Verify and adapt the daemon's HTTP ingest endpoint to accept browser events.

**Files:**
- `cmd/sigild/main.go` — check/modify plugin ingest handler to accept generic event kinds (not just terminal commands)

**Current ingest handler** expects `cmd`, `exit_code`, `cwd` fields (terminal-specific). Need to either:
1. Add a generic `plugin-event` endpoint, or
2. Modify the existing plugin ingest HTTP handler to accept `kind` + `payload` directly

**Verification:**
```bash
curl -X POST http://127.0.0.1:7775/ingest \
  -H "Content-Type: application/json" \
  -d '{"kind":"browser","source":"chrome-extension","payload":{"action":"tab_activated","domain":"github.com","page_title":"sigil"}}'
# Check sigilctl events --kind browser
```

---

### Phase 6: Build Targets + Distribution Prep

**Goal:** Makefile targets and packaging scripts.

**Files:**
- `Makefile` — `build-extensions`, `build-extension-chrome`, `build-extension-firefox`
- `extensions/chrome/scripts/package.sh` — zip for Chrome Web Store
- `extensions/firefox/scripts/package.sh` — zip for AMO
- `.github/workflows/extensions.yml` — CI to build and test extensions

**Chrome Web Store submission requirements:**
- Zip file under 20MB
- Privacy policy URL
- Detailed description of permissions used
- Screenshots (1280x800)

**Verification:**
```bash
make build-extensions
ls extensions/chrome/dist/sigil-chrome.zip
ls extensions/firefox/dist/sigil-firefox.zip
```

---

## Testing Strategy

### Shared Core Tests

```typescript
// privacy.test.ts
describe("extractDomain", () => {
  it("extracts hostname from https URL", () => { ... });
  it("strips path and query", () => { ... });
  it("handles URLs without protocol", () => { ... });
  it("returns empty for invalid URLs", () => { ... });
});

describe("isBlocked", () => {
  it("blocks exact domain match", () => { ... });
  it("blocks subdomain match", () => { ... });
  it("allows non-blocked domains", () => { ... });
});

// classifier.test.ts
describe("classifyDomain", () => {
  it("classifies github.com as development", () => { ... });
  it("classifies docs.google.com as documentation", () => { ... });
  it("classifies unknown domains as other", () => { ... });
  // 30+ cases
});

// transport.test.ts
describe("SigilTransport", () => {
  it("batches events for 2 seconds", () => { ... });
  it("drops events when daemon is unreachable", () => { ... });
  it("reports connection status", () => { ... });
});
```

### Extension Integration Tests

Manual test matrix:

| Test | Chrome | Firefox | Safari |
|------|--------|---------|--------|
| Tab switch → event | [ ] | [ ] | [ ] |
| New tab → event | [ ] | [ ] | [ ] |
| Close tab → page_time | [ ] | [ ] | [ ] |
| Tab count → periodic event | [ ] | [ ] | [ ] |
| Blocked domain → redacted | [ ] | [ ] | [ ] |
| Incognito → no events | [ ] | [ ] | [ ] |
| sigild not running → no errors | [ ] | [ ] | [ ] |
| Works in Edge/Brave/Vivaldi | [ ] | N/A | N/A |

---

## Summary

| Phase | What | Effort |
|-------|------|--------|
| 1 | Shared core library | 2 days |
| 2 | Chrome extension | 2 days |
| 3 | Firefox extension | 1 day |
| 4 | Safari extension | 2 days |
| 5 | Daemon ingest compatibility | 0.5 days |
| 6 | Build targets + distribution | 1 day |
| **Total** | | **8.5 days** |

### Critical Path

```
Phase 1 (shared core) ─── 2 days
    │
    ├──► Phase 2 (Chrome) ── 2 days ─┐
    ├──► Phase 3 (Firefox) ── 1 day  ├── can run in parallel
    └──► Phase 4 (Safari) ── 2 days ─┘
                                      │
Phase 5 (daemon ingest) ── 0.5 days ──┤ (can start with Phase 1)
                                      │
Phase 6 (build + dist) ── 1 day ──────┘

With parallelization: ~5 days elapsed time.
```
