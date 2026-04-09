# 023 — Knowledge Worker Signal Enrichment: Implementation Plan

**Spec:** `specs/023-knowledge-worker-signals/spec.md`
**Branch:** `feat/023-knowledge-worker-signals`

---

## Pre-Implementation Gates

### DAG Gate

All new code lives in `internal/collector/sources/` (new source files) and `internal/event/` (new Kind constants). No new packages. No new edges in the dependency graph.

```
event (leaf) ← unchanged, gains new Kind constants only
  ↑
config ← gains SourcesConfig struct
  ↑
collector/sources ← new source files, same Source interface
  ↑
cmd/sigild ← registers new sources in addPlatformSources()
```

### Interface Gate

No new interfaces. Every new source implements the existing `collector.Source` interface:

```go
type Source interface {
    Name() string
    Events(ctx context.Context) (<-chan event.Event, error)
}
```

The only structural addition is a `SourcesConfig` in the config package to hold per-source enable/disable flags and options.

### Privacy Gate

- **No data leaves the machine.** All new sources write to the local SQLite store.
- **Metadata only.** Typing captures rate (not keys), pointer captures distance (not coordinates), network stores SSID hash (not name), downloads store extension (not filename).
- **Input sources opt-in.** Typing and pointer default to `enabled = false`.
- **Kill switch works.** `sigilctl purge` deletes all event kinds.
- **Retention applies.** `raw_event_days` covers all new event kinds.

### Simplicity Gate

Each source is a single file per platform (e.g., `idle_darwin.go`, `idle_linux.go`, `idle_windows.go`) following the exact same pattern as existing sources. No frameworks, no abstractions beyond the `Source` interface. Each source polls or listens, emits events, done.

---

## Technical Design

### Event Kinds

Add to `internal/event/event.go`:

```go
KindIdle         Kind = "idle"
KindTyping       Kind = "typing"
KindPointer      Kind = "pointer"
KindDesktop      Kind = "desktop"
KindDisplay      Kind = "display"
KindAudio        Kind = "audio"
KindPower        Kind = "power"
KindNetwork      Kind = "network"
KindFocusMode    Kind = "focus_mode"
KindAppLifecycle Kind = "app_lifecycle"
KindScreenshot   Kind = "screenshot"
KindDownload     Kind = "download"
KindCalendar     Kind = "calendar"
KindBrowser      Kind = "browser"
```

### Config Changes

Add to `internal/config/config.go`:

```go
type Config struct {
    // ... existing fields ...
    Sources SourcesConfig `toml:"sources" json:"sources"`
}

type SourcesConfig struct {
    Idle         SourceToggle       `toml:"idle" json:"idle"`
    Typing       SourceToggle       `toml:"typing" json:"typing"`
    Pointer      SourceToggle       `toml:"pointer" json:"pointer"`
    Desktop      SourceToggle       `toml:"desktop" json:"desktop"`
    Display      SourceToggle       `toml:"display" json:"display"`
    Audio        SourceToggle       `toml:"audio" json:"audio"`
    Power        SourceToggle       `toml:"power" json:"power"`
    Network      NetworkSourceConfig `toml:"network" json:"network"`
    FocusMode    SourceToggle       `toml:"focus_mode" json:"focus_mode"`
    AppLifecycle SourceToggle       `toml:"app_lifecycle" json:"app_lifecycle"`
    Screenshot   SourceToggle       `toml:"screenshot" json:"screenshot"`
    Download     DownloadSourceConfig `toml:"download" json:"download"`
    Calendar     CalendarSourceConfig `toml:"calendar" json:"calendar"`
    Browser      BrowserSourceConfig  `toml:"browser" json:"browser"`
}

type SourceToggle struct {
    Enabled *bool `toml:"enabled" json:"enabled"` // nil = use default
}

type NetworkSourceConfig struct {
    Enabled  *bool `toml:"enabled" json:"enabled"`
    HashSSID bool  `toml:"hash_ssid" json:"hash_ssid"`
}

type DownloadSourceConfig struct {
    Enabled  *bool  `toml:"enabled" json:"enabled"`
    WatchDir string `toml:"watch_dir" json:"watch_dir"`
}

type CalendarSourceConfig struct {
    Enabled   *bool    `toml:"enabled" json:"enabled"`
    Calendars []string `toml:"calendars" json:"calendars"`
}

type BrowserSourceConfig struct {
    Enabled        *bool    `toml:"enabled" json:"enabled"`
    BlockedDomains []string `toml:"blocked_domains" json:"blocked_domains"`
    PollInterval   string   `toml:"poll_interval" json:"poll_interval"`
}
```

**Defaults:** Idle, display, audio, power, network, focus_mode, app_lifecycle, screenshot, download, desktop, browser all default to **enabled**. Typing and pointer default to **disabled** (require Accessibility on macOS).

### Source File Naming Convention

Each source follows the existing pattern:

```
internal/collector/sources/
  idle_darwin.go          ← macOS implementation
  idle_linux.go           ← Linux implementation
  idle_windows.go         ← Windows implementation
  idle_other.go           ← no-op fallback (if needed)
  idle_test.go            ← shared tests
```

### Socket API

No new socket methods. Events are captured by the collector and stored via the existing `InsertEvent` path. They're queryable via the existing `events` method with `kind` filter.

New health checks in the `health` handler report whether each source is active and healthy.

---

## Implementation Phases

### Phase 1: Event Kinds + Config Schema

**Goal:** Define all new event kinds and config structures. No runtime behavior yet.

**Files:**
- `internal/event/event.go` — add 14 new Kind constants
- `internal/config/config.go` — add `SourcesConfig` and sub-structs

**Verification:**
```bash
go build ./... && go vet ./... && go test ./internal/event/ ./internal/config/ -count=1
```

---

### Phase 2: Idle Detection (P0)

**Goal:** Detect active/idle transitions and screen lock on all platforms.

**Files:**
- `internal/collector/sources/idle_darwin.go` — `CGEventSourceSecondsSinceLastEventType` + `CGSessionCopyCurrentDictionary` via cgo
- `internal/collector/sources/idle_linux.go` — `XScreenSaverQueryInfo` via X11 or polling `/proc/`
- `internal/collector/sources/idle_windows.go` — `GetLastInputInfo` via syscall
- `internal/collector/sources/idle_test.go` — test event emission, threshold, state machine
- `cmd/sigild/sources_darwin.go` — register `IdleSource`
- `cmd/sigild/sources_linux.go` — register `IdleSource`
- `cmd/sigild/sources_windows.go` — register `IdleSource`

**State machine:**
```
Active → (no input for threshold) → Idle → (input received) → Active
Active → (screen locked) → Locked → (screen unlocked) → Active
```

**Verification:**
```bash
go build ./... && go vet ./...
go test ./internal/collector/sources/ -run TestIdle -count=1
# Manual: lock screen, wait, unlock — check sigilctl events --kind idle
```

---

### Phase 3: Git Enrichment (P0)

**Goal:** Commit events include message, branch, and diff stats. Branch events include previous branch.

**Files:**
- `internal/collector/sources/git.go` — enhance `classifyGitEvent` to shell out to `git log` and `git diff --stat` on commit, read `.git/HEAD` on head_change

**Verification:**
```bash
go build ./... && go test ./internal/collector/sources/ -run TestGit -count=1
# Manual: make a commit, check sigilctl events --kind git for enriched payload
```

---

### Phase 4: File Metadata Enrichment (P0)

**Goal:** Every file event includes language, is_test, is_config, size_bytes.

**Files:**
- `internal/collector/sources/filemeta.go` — `EnrichFileEvent(e *event.Event)` function with extension→language map, test/config pattern matchers
- `internal/collector/sources/filemeta_test.go` — table-driven tests for all language mappings and patterns
- `internal/collector/sources/files.go` — call `EnrichFileEvent` before emitting

**Verification:**
```bash
go build ./... && go test ./internal/collector/sources/ -run TestFileMeta -count=1
# Manual: edit a .go file, check sigilctl events --kind file for enriched fields
```

---

### Phase 5: App Lifecycle (P0)

**Goal:** App launch/quit/crash events on all platforms.

**Files:**
- `internal/collector/sources/applifecycle_darwin.go` — `NSWorkspace` notifications via cgo
- `internal/collector/sources/applifecycle_linux.go` — process polling with PID tracking
- `internal/collector/sources/applifecycle_windows.go` — WMI process start/stop
- `internal/collector/sources/applifecycle_test.go`

**Verification:**
```bash
go build ./... && go test ./internal/collector/sources/ -run TestAppLifecycle -count=1
# Manual: launch Slack, quit Slack — check sigilctl events --kind app_lifecycle
```

---

### Phase 6: System State Sources (P1)

**Goal:** Display, audio, power, network, focus mode, desktop switches.

Each is a small, self-contained source. Implement all six in one phase since they're structurally identical (event-driven listeners, no polling).

**Files (per source, × 3 platforms):**
- `display_darwin.go`, `display_linux.go`, `display_windows.go`
- `audio_darwin.go`, `audio_linux.go`, `audio_windows.go`
- `power_darwin.go`, `power_linux.go`, `power_windows.go`
- `network_darwin.go`, `network_linux.go`, `network_windows.go`
- `focusmode_darwin.go`, `focusmode_windows.go` (no Linux)
- `desktop_darwin.go`, `desktop_linux.go`, `desktop_windows.go`

**macOS implementation notes:**
- Display: `CGDisplayRegisterReconfigurationCallback` (cgo)
- Audio: CoreAudio `AudioObjectAddPropertyListener` (cgo)
- Power: `IOPSNotificationCreateRunLoopSource` (cgo)
- Network: `NWPathMonitor` (cgo) or shell out to `networksetup`
- Focus mode: `defaults read com.apple.controlcenter` + poll
- Desktop: `NSWorkspace.activeSpaceDidChangeNotification` (cgo)

**Linux implementation notes:**
- Display: `xrandr --listmonitors` poll or D-Bus
- Audio: PulseAudio/PipeWire D-Bus or `pactl subscribe`
- Power: `/sys/class/power_supply/BAT0/status` poll
- Network: NetworkManager D-Bus
- Desktop: `_NET_CURRENT_DESKTOP` X11 property

**Windows implementation notes:**
- Display: `WM_DISPLAYCHANGE` message
- Audio: `IMMNotificationClient` COM interface
- Power: `SYSTEM_POWER_STATUS` poll
- Network: `NotifyIpInterfaceChange` callback
- Focus mode: Focus Assist WMI
- Desktop: `IVirtualDesktopManager`

**Verification:**
```bash
go build ./... && go vet ./...
go test ./internal/collector/sources/ -run "TestDisplay|TestAudio|TestPower|TestNetwork|TestFocusMode|TestDesktop" -count=1
```

---

### Phase 7: Screenshot + Download Detection (P2)

**Goal:** Detect screenshots and new downloads.

**Files:**
- `internal/collector/sources/screenshot_darwin.go` — watch `~/Desktop/Screenshot*` via fsnotify
- `internal/collector/sources/screenshot_linux.go` — watch common screenshot dirs
- `internal/collector/sources/screenshot_windows.go` — watch Screenshots folder
- `internal/collector/sources/download.go` — watch `~/Downloads/` via fsnotify (cross-platform)

**Verification:**
```bash
go build ./... && go test ./internal/collector/sources/ -run "TestScreenshot|TestDownload" -count=1
# Manual: take a screenshot, download a file — check events
```

---

### Phase 8: Input Sources — Typing + Pointer (P2)

**Goal:** Keystroke rate and pointer metrics, opt-in only.

**Files:**
- `internal/collector/sources/typing_darwin.go` — `CGEventTapCreate` for key events (cgo, Accessibility)
- `internal/collector/sources/typing_linux.go` — `/dev/input/event*` or libinput
- `internal/collector/sources/typing_windows.go` — `SetWindowsHookEx(WH_KEYBOARD_LL)`
- `internal/collector/sources/pointer_darwin.go` — same CGEventTap for mouse
- `internal/collector/sources/pointer_linux.go`
- `internal/collector/sources/pointer_windows.go`

**Critical privacy invariant:** These sources MUST be reviewed for privacy compliance before merge. Only aggregate counts and distances — never individual events.

**Verification:**
```bash
go build ./... && go test ./internal/collector/sources/ -run "TestTyping|TestPointer" -count=1
# Manual: enable in config, type for 30s — check sigilctl events --kind typing
```

---

### Phase 9: Calendar Integration (P1)

**Goal:** Meeting boundaries via macOS EventKit.

**Files:**
- `internal/collector/sources/calendar_darwin.go` — EventKit via cgo (requires Calendar permission)
- `internal/collector/sources/calendar_other.go` — no-op on non-macOS

**Verification:**
```bash
go build ./... && go test ./internal/collector/sources/ -run TestCalendar -count=1
# Manual: verify against known calendar entries
```

---

### Phase 10: Browser Signal Baseline (P0 — spec 024)

**Goal:** Extract page title and domain when a browser is focused.

**Files:**
- `internal/collector/sources/browser_darwin.go` — AppleScript for Chrome/Safari, window title for others
- `internal/collector/sources/browser_linux.go` — window title parsing
- `internal/collector/sources/browser_windows.go` — `GetWindowText` parsing
- `internal/collector/sources/browser_patterns.go` — domain extraction from page title patterns (50+ sites)
- `internal/collector/sources/browser_classify.go` — domain → category map
- `internal/collector/sources/browser_test.go`

**Verification:**
```bash
go build ./... && go test ./internal/collector/sources/ -run TestBrowser -count=1
# Manual: open Chrome, check sigilctl events --kind browser for domain + title
```

---

### Phase 11: Source Registration + Health

**Goal:** Register all sources with config-based enable/disable. Report source health.

**Files:**
- `cmd/sigild/sources_darwin.go` — register all new sources (check config before adding)
- `cmd/sigild/sources_linux.go` — same
- `cmd/sigild/sources_windows.go` — same
- `cmd/sigild/handler_health.go` — report active source count and per-source status

**Verification:**
```bash
make check
sigilctl status  # should list active sources
echo '{"method":"health"}' | nc -U /tmp/sigild.sock  # should show source health
```

---

## Testing Strategy

### Unit Tests (per source)

Table-driven tests with `t.Run`. Each source gets:

```go
func TestIdleSource(t *testing.T) {
    t.Run("emits_idle_start_after_threshold", ...)
    t.Run("emits_idle_end_on_input", ...)
    t.Run("screen_lock_suppresses_idle", ...)
    t.Run("respects_config_threshold", ...)
    t.Run("disabled_emits_nothing", ...)
}
```

### File Metadata Tests

Comprehensive table for language mapping:

```go
func TestEnrichFileEvent(t *testing.T) {
    tests := []struct{
        path     string
        wantLang string
        wantTest bool
        wantCfg  bool
    }{
        {"main.go", "go", false, false},
        {"store_test.go", "go", true, false},
        {"config.toml", "toml", false, true},
        {"Dockerfile", "docker", false, true},
        ...
    }
}
```

### Git Enrichment Tests

Mock `exec.Command` to verify git command parsing:

```go
func TestGitCommitEnrichment(t *testing.T) {
    t.Run("parses_log_output", ...)
    t.Run("parses_diff_stat", ...)
    t.Run("truncates_long_message", ...)
    t.Run("timeout_falls_back_to_basic", ...)
}
```

### Integration Tests

Real SQLite via `openMemory(t)`. Verify events round-trip through store:

```go
func TestIdleEventsStoredCorrectly(t *testing.T) {
    db := openMemory(t)
    // Insert idle events, query back, verify payload
}
```

### Mock Boundaries

No new mocks needed. Sources implement `collector.Source` which is already mockable. The `exec.Command` calls in git enrichment use a `RunCmd` function type (already exists for actuators) for testability.

---

## Summary

| Phase | What | Sources | Effort |
|-------|------|---------|--------|
| 1 | Event kinds + config | — | 0.5 days |
| 2 | Idle detection | 1 (×3 platforms) | 2 days |
| 3 | Git enrichment | 1 (enhance existing) | 1 day |
| 4 | File metadata | 1 (cross-platform) | 0.5 days |
| 5 | App lifecycle | 1 (×3 platforms) | 1 day |
| 6 | System state (6 sources) | 6 (×3 platforms) | 3 days |
| 7 | Screenshot + download | 2 (×3 platforms) | 1 day |
| 8 | Typing + pointer | 2 (×3 platforms) | 2 days |
| 9 | Calendar | 1 (macOS only for now) | 1.5 days |
| 10 | Browser baseline | 1 (×3 platforms) | 2 days |
| 11 | Registration + health | — | 0.5 days |
| **Total** | | **16 sources** | **~15 days** |

### Critical Path

Phase 1 → Phase 2 (idle needs new Kind constants)
Phase 1 → Phase 3 (git enrichment needs config for timeout)
Phase 1 → Phase 10 (browser needs BrowserSourceConfig)
Phases 2-5 are the P0 block — ship in 5 days for immediate ML value.
Phases 6-9 can run in parallel after Phase 1.
