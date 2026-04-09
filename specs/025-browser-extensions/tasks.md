# 025 — Browser Extensions: Tasks

**Status:** Draft
**Branch:** `feat/025-browser-extensions`

---

## Tasks

### Phase 1: Shared Core Library

- [ ] **Task 1.1**: Define event types and interfaces
  - Files: `extensions/browser-shared/src/types.ts`
  - Test: `cd extensions/browser-shared && npm test`
  - Depends: none

- [ ] **Task 1.2**: Implement domain extraction + privacy filter [P]
  - `extractDomain(url)` → hostname only
  - `isBlocked(domain, blocklist)` → boolean
  - `redactEvent(event)` → replace domain/title with `[blocked]`
  - Files: `extensions/browser-shared/src/privacy.ts`, `privacy.test.ts`
  - Test: `cd extensions/browser-shared && npm test -- --grep privacy`
  - Depends: Task 1.1

- [ ] **Task 1.3**: Implement domain classifier [P]
  - `classifyDomain(domain)` → category string
  - Same category map as Go side (spec 024)
  - Files: `extensions/browser-shared/src/classifier.ts`, `classifier.test.ts`
  - Test: `cd extensions/browser-shared && npm test -- --grep classifier`
  - Depends: Task 1.1

- [ ] **Task 1.4**: Implement transport layer [P]
  - `SigilTransport` class: POST to `127.0.0.1:7775`, 2-second batching, connection status, stats
  - Drop events silently when daemon unreachable
  - Files: `extensions/browser-shared/src/transport.ts`, `transport.test.ts`
  - Test: `cd extensions/browser-shared && npm test -- --grep transport`
  - Depends: Task 1.1

- [ ] **Task 1.5**: Implement tab event collector
  - `TabCollector` class: listens to tab API events, tracks page time, emits periodic tab count
  - Abstracts `chrome.tabs.*` / `browser.tabs.*` difference
  - Files: `extensions/browser-shared/src/collector.ts`, `collector.test.ts`
  - Test: `cd extensions/browser-shared && npm test -- --grep collector`
  - Depends: Task 1.1, Task 1.2

- [ ] **Task 1.6**: Package.json + tsconfig + build setup
  - Files: `extensions/browser-shared/package.json`, `tsconfig.json`
  - Test: `cd extensions/browser-shared && npm install && npm run build && npm test`
  - Depends: none

- [ ] **Task 1.7**: Phase 1 verification
  - Test: `cd extensions/browser-shared && npm test` (all tests pass)
  - Depends: Task 1.1–1.6

---

### Phase 2: Chrome Extension

- [ ] **Task 2.1**: Create Manifest V3 manifest.json
  - Permissions: `tabs`, `activeTab`
  - Host permissions: `http://127.0.0.1:7775/*`
  - `"incognito": "not_allowed"`
  - Files: `extensions/chrome/manifest.json`
  - Test: chrome://extensions → load unpacked validates manifest
  - Depends: none

- [ ] **Task 2.2**: Implement service worker background
  - Import shared core, create TabCollector + SigilTransport, wire events
  - Apply privacy filter before sending
  - Files: `extensions/chrome/src/background.ts`
  - Test: load extension → navigate → check sigild events
  - Depends: Task 1.7, Task 2.1

- [ ] **Task 2.3**: Build popup UI (connection status + blocklist) [P]
  - Show: connected/disconnected, events sent today, blocked domains list
  - Manage blocklist via `chrome.storage.local`
  - Files: `extensions/chrome/src/popup.html`, `extensions/chrome/src/popup.ts`
  - Test: click extension icon → popup renders correctly
  - Depends: Task 2.1

- [ ] **Task 2.4**: Vite build config + icons
  - Files: `extensions/chrome/vite.config.ts`, `extensions/chrome/package.json`, `extensions/chrome/tsconfig.json`, `extensions/chrome/icons/`
  - Test: `cd extensions/chrome && npm run build` produces `dist/`
  - Depends: Task 2.2, Task 2.3

- [ ] **Task 2.5**: Phase 2 verification
  - Load unpacked in Chrome, Edge, Brave → tab events appear in `sigilctl events --kind browser`
  - Depends: Task 2.4

---

### Phase 3: Firefox Extension

- [ ] **Task 3.1**: Create Firefox manifest.json (Manifest V2)
  - `browser.*` API namespace, background script (not service worker)
  - Files: `extensions/firefox/manifest.json`
  - Depends: none

- [ ] **Task 3.2**: Implement Firefox background script + popup
  - Adapt Chrome background.ts for `browser.*` namespace
  - Files: `extensions/firefox/src/background.ts`, `extensions/firefox/src/popup.html`, `extensions/firefox/src/popup.ts`
  - Test: about:debugging → load temporary add-on → test
  - Depends: Task 1.7, Task 3.1

- [ ] **Task 3.3**: Build config + package
  - Files: `extensions/firefox/vite.config.ts`, `extensions/firefox/package.json`
  - Test: `cd extensions/firefox && npm run build`
  - Depends: Task 3.2

- [ ] **Task 3.4**: Phase 3 verification
  - Test: load in Firefox → tab events appear
  - Depends: Task 3.3

---

### Phase 4: Safari Extension

- [ ] **Task 4.1**: Create Safari Web Extension Xcode project
  - Wrapper app + extension target
  - Files: `extensions/safari/SigilSafari.xcodeproj/`, `extensions/safari/SigilSafari/`
  - Test: Xcode → build succeeds
  - Depends: none

- [ ] **Task 4.2**: Port shared core to Safari extension resources
  - Compile browser-shared to single background.js
  - Files: `extensions/safari/SigilSafari/Resources/background.js`, `popup.html`, `popup.js`, `manifest.json`
  - Test: enable in Safari preferences → tab events appear
  - Depends: Task 1.7, Task 4.1

- [ ] **Task 4.3**: Phase 4 verification
  - Test: Safari → tab switch → events in sigild
  - Depends: Task 4.2

---

### Phase 5: Daemon Ingest Compatibility

- [ ] **Task 5.1**: Verify/adapt plugin HTTP ingest for browser events
  - Ensure `127.0.0.1:7775/ingest` accepts `kind: "browser"` events (not just terminal)
  - Files: `cmd/sigild/main.go` (plugin ingest handler)
  - Test: `curl -X POST http://127.0.0.1:7775/ingest -d '{"kind":"browser","source":"test","payload":{"action":"tab_activated","domain":"github.com"}}'` → event stored
  - Depends: none

---

### Phase 6: Build Targets + Distribution

- [ ] **Task 6.1**: Add Makefile targets for extension builds
  - `make build-extension-chrome`, `make build-extension-firefox`, `make build-extensions`
  - Files: `Makefile`
  - Test: `make build-extensions`
  - Depends: Task 2.4, Task 3.3

- [ ] **Task 6.2**: Packaging scripts for store submission [P]
  - Chrome: zip for Chrome Web Store
  - Firefox: zip for AMO
  - Files: `extensions/chrome/scripts/package.sh`, `extensions/firefox/scripts/package.sh`
  - Test: scripts produce valid zip files
  - Depends: Task 6.1

- [ ] **Task 6.3**: Final verification
  - All three extensions build, load, capture events, respect blocklist, exclude incognito
  - Test: manual test matrix (Chrome, Firefox, Safari × all event types)
  - Depends: all previous

---

## Summary

| Phase | Tasks | Effort |
|-------|-------|--------|
| 1 | 7 | 2 days |
| 2 | 5 | 2 days |
| 3 | 4 | 1 day |
| 4 | 3 | 2 days |
| 5 | 1 | 0.5 days |
| 6 | 3 | 1 day |
| **Total** | **23** | **8.5 days** |

### Parallelization
Phases 2, 3, 4 can run in parallel after Phase 1.
Phase 5 can start immediately (no dependency on extensions).
With parallelization: **~5 days elapsed time**.
