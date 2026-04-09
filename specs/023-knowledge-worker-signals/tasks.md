# 023 — Knowledge Worker Signal Enrichment: Tasks

**Status:** Draft
**Author:** Alec Feeman
**Date:** 2026-04-08
**Branch:** `feat/023-knowledge-worker-signals`

---

## Tasks

### Phase 1: Event Kinds + Config Schema

- [ ] **Task 1.1**: Add new event kind constants to `internal/event/event.go`
  - Add: `KindIdle`, `KindTyping`, `KindPointer`, `KindDesktop`, `KindDisplay`, `KindAudio`, `KindPower`, `KindNetwork`, `KindFocusMode`, `KindAppLifecycle`, `KindScreenshot`, `KindDownload`, `KindCalendar`, `KindBrowser`
  - Files: `internal/event/event.go`
  - Test: `go test ./internal/event/ -count=1`
  - Depends: none

- [ ] **Task 1.2**: Add `SourcesConfig` struct to `internal/config/config.go` [P]
  - Add `Sources SourcesConfig` to `Config` struct with TOML + JSON tags
  - Add `SourceToggle`, `NetworkSourceConfig`, `DownloadSourceConfig`, `CalendarSourceConfig`, `BrowserSourceConfig` sub-structs
  - Set defaults: idle/display/audio/power/network/focus_mode/app_lifecycle/screenshot/download/desktop/browser = enabled, typing/pointer = disabled, calendar = disabled
  - Files: `internal/config/config.go`
  - Test: `go test ./internal/config/ -count=1`
  - Depends: none

- [ ] **Task 1.3**: Phase 1 verification
  - Test: `go build ./... && go vet ./... && go test ./internal/event/ ./internal/config/ -count=1`
  - Depends: Task 1.1, Task 1.2

---

### Phase 2: Idle Detection (P0)

- [ ] **Task 2.1**: Implement macOS idle source
  - `CGEventSourceSecondsSinceLastEventType` for idle detection (cgo)
  - `CGSessionCopyCurrentDictionary` for screen lock detection (cgo)
  - State machine: Active → Idle → Active, Active → Locked → Active
  - Configurable threshold from `[sources.idle] threshold`
  - Files: `internal/collector/sources/idle_darwin.go`
  - Test: `go test ./internal/collector/sources/ -run TestIdle -count=1`
  - Depends: Task 1.1

- [ ] **Task 2.2**: Implement Linux idle source [P]
  - `XScreenSaverQueryInfo` for idle time (X11) or `/proc/` fallback
  - `org.freedesktop.ScreenSaver` D-Bus for screen lock
  - Files: `internal/collector/sources/idle_linux.go`
  - Test: `go test ./internal/collector/sources/ -run TestIdle -count=1` (on Linux)
  - Depends: Task 1.1

- [ ] **Task 2.3**: Implement Windows idle source [P]
  - `GetLastInputInfo` via syscall for idle time
  - `WTSRegisterSessionNotification` for screen lock
  - Files: `internal/collector/sources/idle_windows.go`
  - Test: `go test ./internal/collector/sources/ -run TestIdle -count=1` (on Windows)
  - Depends: Task 1.1

- [ ] **Task 2.4**: Write idle source tests
  - Table-driven: threshold respected, idle_start/idle_end timing, screen_lock suppresses idle, config disable
  - Files: `internal/collector/sources/idle_test.go`
  - Test: `go test ./internal/collector/sources/ -run TestIdle -v -count=1`
  - Depends: Task 2.1

- [ ] **Task 2.5**: Register idle source in `addPlatformSources`
  - Files: `cmd/sigild/sources_darwin.go`, `cmd/sigild/sources_linux.go`, `cmd/sigild/sources_windows.go`
  - Test: `go build ./cmd/sigild/`
  - Depends: Task 2.1, Task 2.2, Task 2.3

- [ ] **Task 2.6**: Phase 2 verification
  - Test: `make check`
  - Depends: Task 2.5

---

### Phase 3: Git Enrichment (P0)

- [ ] **Task 3.1**: Enrich commit events with message, branch, diff stats
  - On `COMMIT_EDITMSG` write: exec `git log -1 --format='%H|%s'` and `git diff --stat HEAD~1..HEAD`
  - Parse output into `message`, `hash`, `files_changed`, `insertions`, `deletions`
  - 2-second timeout on git commands
  - Truncate message to 200 chars
  - Files: `internal/collector/sources/git.go`
  - Test: `go test ./internal/collector/sources/ -run TestGitCommitEnrich -count=1`
  - Depends: Task 1.1

- [ ] **Task 3.2**: Enrich HEAD change events with branch name
  - On `HEAD` change: read `.git/HEAD` to get current branch, track previous
  - Files: `internal/collector/sources/git.go`
  - Test: `go test ./internal/collector/sources/ -run TestGitBranchEnrich -count=1`
  - Depends: Task 3.1

- [ ] **Task 3.3**: Write git enrichment tests
  - Table-driven: commit parsing, diff stat parsing, timeout fallback, branch tracking, message truncation
  - Mock `exec.Command` for deterministic output
  - Files: `internal/collector/sources/git_enrich_test.go`
  - Test: `go test ./internal/collector/sources/ -run TestGit -v -count=1`
  - Depends: Task 3.2

- [ ] **Task 3.4**: Phase 3 verification
  - Test: `make check`
  - Depends: Task 3.3

---

### Phase 4: File Metadata Enrichment (P0)

- [ ] **Task 4.1**: Implement file metadata enrichment function
  - `EnrichFileEvent(e *event.Event)` adds `extension`, `language`, `is_test`, `is_config`, `size_bytes`
  - Language map: 20+ languages (go, python, typescript, javascript, rust, java, c, cpp, ruby, swift, kotlin, sql, yaml, toml, json, markdown, html, css, shell, docker, terraform, proto)
  - Test patterns: `*_test.go`, `*.test.ts`, `*.spec.js`, `test_*.py`, `*_test.rs`, etc.
  - Config patterns: `*.toml`, `*.yaml`, `*.yml`, `*.json`, `*.env`, `Makefile`, `Dockerfile`, `*.tf`, etc.
  - Files: `internal/collector/sources/filemeta.go`
  - Test: `go test ./internal/collector/sources/ -run TestFileMeta -count=1`
  - Depends: Task 1.1

- [ ] **Task 4.2**: Wire enrichment into FileSource event emission
  - Call `EnrichFileEvent` before sending event to channel
  - Files: `internal/collector/sources/files.go`
  - Test: `go test ./internal/collector/sources/ -run TestFileSource -count=1`
  - Depends: Task 4.1

- [ ] **Task 4.3**: Write exhaustive file metadata tests
  - Table-driven: every language mapping, every test pattern, every config pattern, missing files, large files
  - Files: `internal/collector/sources/filemeta_test.go`
  - Test: `go test ./internal/collector/sources/ -run TestFileMeta -v -count=1`
  - Depends: Task 4.1

- [ ] **Task 4.4**: Phase 4 verification
  - Test: `make check`
  - Depends: Task 4.3

---

### Phase 5: App Lifecycle (P0)

- [ ] **Task 5.1**: Implement macOS app lifecycle source
  - `NSWorkspace.didLaunchApplicationNotification` / `didTerminateApplicationNotification` via cgo
  - Filter to GUI apps (skip background daemons)
  - Track launch time → compute duration on quit
  - Detect crashes (abnormal exit)
  - Files: `internal/collector/sources/applifecycle_darwin.go`
  - Test: `go test ./internal/collector/sources/ -run TestAppLifecycle -count=1`
  - Depends: Task 1.1

- [ ] **Task 5.2**: Implement Linux app lifecycle source [P]
  - Process polling with PID tracking (enhance existing ProcessSource)
  - Files: `internal/collector/sources/applifecycle_linux.go`
  - Test: `go test ./internal/collector/sources/ -run TestAppLifecycle -count=1`
  - Depends: Task 1.1

- [ ] **Task 5.3**: Implement Windows app lifecycle source [P]
  - WMI `Win32_ProcessStartTrace` or process polling
  - Files: `internal/collector/sources/applifecycle_windows.go`
  - Test: `go test ./internal/collector/sources/ -run TestAppLifecycle -count=1`
  - Depends: Task 1.1

- [ ] **Task 5.4**: Register app lifecycle source + tests
  - Files: `internal/collector/sources/applifecycle_test.go`, `cmd/sigild/sources_*.go`
  - Test: `go build ./cmd/sigild/ && go test ./internal/collector/sources/ -run TestAppLifecycle -v -count=1`
  - Depends: Task 5.1, Task 5.2, Task 5.3

- [ ] **Task 5.5**: Phase 5 verification
  - Test: `make check`
  - Depends: Task 5.4

---

### Phase 6: System State Sources (P1)

- [ ] **Task 6.1**: Implement display source (macOS/Linux/Windows)
  - macOS: `CGDisplayRegisterReconfigurationCallback` (cgo)
  - Linux: `xrandr --listmonitors` poll or D-Bus
  - Windows: `WM_DISPLAYCHANGE`
  - Files: `internal/collector/sources/display_darwin.go`, `display_linux.go`, `display_windows.go`
  - Test: `go test ./internal/collector/sources/ -run TestDisplay -count=1`
  - Depends: Task 1.1

- [ ] **Task 6.2**: Implement audio source (macOS/Linux/Windows) [P]
  - macOS: CoreAudio `AudioObjectAddPropertyListener` (cgo)
  - Linux: PulseAudio/PipeWire D-Bus or `pactl subscribe`
  - Windows: `IMMNotificationClient` COM
  - Files: `internal/collector/sources/audio_darwin.go`, `audio_linux.go`, `audio_windows.go`
  - Test: `go test ./internal/collector/sources/ -run TestAudio -count=1`
  - Depends: Task 1.1

- [ ] **Task 6.3**: Implement power source (macOS/Linux/Windows) [P]
  - macOS: `IOPSNotificationCreateRunLoopSource` (cgo)
  - Linux: `/sys/class/power_supply/` poll or UPower D-Bus
  - Windows: `SYSTEM_POWER_STATUS` poll
  - Files: `internal/collector/sources/power_darwin.go`, `power_linux.go`, `power_windows.go`
  - Test: `go test ./internal/collector/sources/ -run TestPower -count=1`
  - Depends: Task 1.1

- [ ] **Task 6.4**: Implement network source (macOS/Linux/Windows) [P]
  - macOS: `NWPathMonitor` (cgo) or `networksetup`
  - Linux: NetworkManager D-Bus
  - Windows: `NotifyIpInterfaceChange`
  - SSID stored as SHA256 hash (privacy)
  - Files: `internal/collector/sources/network_darwin.go`, `network_linux.go`, `network_windows.go`
  - Test: `go test ./internal/collector/sources/ -run TestNetwork -count=1`
  - Depends: Task 1.1

- [ ] **Task 6.5**: Implement focus mode source (macOS/Windows) [P]
  - macOS: `defaults read com.apple.controlcenter` poll
  - Windows: Focus Assist WMI
  - Linux: no-op (no standard OS DND)
  - Files: `internal/collector/sources/focusmode_darwin.go`, `focusmode_windows.go`, `focusmode_other.go`
  - Test: `go test ./internal/collector/sources/ -run TestFocusMode -count=1`
  - Depends: Task 1.1

- [ ] **Task 6.6**: Implement desktop switch source (macOS/Linux/Windows) [P]
  - macOS: `NSWorkspace.activeSpaceDidChangeNotification` (cgo)
  - Linux: `_NET_CURRENT_DESKTOP` X11 property or compositor IPC
  - Windows: `IVirtualDesktopManager`
  - Files: `internal/collector/sources/desktop_darwin.go`, `desktop_linux.go`, `desktop_windows.go`
  - Test: `go test ./internal/collector/sources/ -run TestDesktop -count=1`
  - Depends: Task 1.1

- [ ] **Task 6.7**: Register all system state sources + tests
  - Files: `cmd/sigild/sources_*.go`, `internal/collector/sources/*_test.go`
  - Test: `go build ./cmd/sigild/`
  - Depends: Task 6.1–6.6

- [ ] **Task 6.8**: Phase 6 verification
  - Test: `make check`
  - Depends: Task 6.7

---

### Phase 7: Screenshot + Download Detection (P2)

- [ ] **Task 7.1**: Implement screenshot detection source
  - macOS: watch `~/Desktop/Screenshot*` via fsnotify
  - Linux: watch common screenshot directories
  - Windows: watch `%USERPROFILE%\Pictures\Screenshots`
  - Files: `internal/collector/sources/screenshot_darwin.go`, `screenshot_linux.go`, `screenshot_windows.go`
  - Test: `go test ./internal/collector/sources/ -run TestScreenshot -count=1`
  - Depends: Task 1.1

- [ ] **Task 7.2**: Implement download detection source [P]
  - Watch `~/Downloads/` via fsnotify (cross-platform)
  - Capture extension and size — NOT filename
  - Debounce: one event per file
  - Files: `internal/collector/sources/download.go`
  - Test: `go test ./internal/collector/sources/ -run TestDownload -count=1`
  - Depends: Task 1.1

- [ ] **Task 7.3**: Register screenshot + download sources
  - Files: `cmd/sigild/sources_*.go`
  - Test: `go build ./cmd/sigild/`
  - Depends: Task 7.1, Task 7.2

- [ ] **Task 7.4**: Phase 7 verification
  - Test: `make check`
  - Depends: Task 7.3

---

### Phase 8: Typing + Pointer (P2)

- [ ] **Task 8.1**: Implement typing velocity source (macOS)
  - `CGEventTapCreate` for kCGEventKeyDown (cgo, Accessibility)
  - 30-second aggregation window, count only — never key identifiers
  - Files: `internal/collector/sources/typing_darwin.go`
  - Test: `go test ./internal/collector/sources/ -run TestTyping -count=1`
  - Depends: Task 1.1

- [ ] **Task 8.2**: Implement typing velocity source (Linux/Windows) [P]
  - Linux: `/dev/input/event*` or libinput
  - Windows: `SetWindowsHookEx(WH_KEYBOARD_LL)`
  - Files: `internal/collector/sources/typing_linux.go`, `typing_windows.go`
  - Test: `go test ./internal/collector/sources/ -run TestTyping -count=1`
  - Depends: Task 1.1

- [ ] **Task 8.3**: Implement pointer activity source (macOS) [P]
  - `CGEventTapCreate` for mouse events (cgo, Accessibility)
  - 30-second window: click count, scroll distance, movement distance
  - Files: `internal/collector/sources/pointer_darwin.go`
  - Test: `go test ./internal/collector/sources/ -run TestPointer -count=1`
  - Depends: Task 1.1

- [ ] **Task 8.4**: Implement pointer activity source (Linux/Windows) [P]
  - Files: `internal/collector/sources/pointer_linux.go`, `pointer_windows.go`
  - Test: `go test ./internal/collector/sources/ -run TestPointer -count=1`
  - Depends: Task 1.1

- [ ] **Task 8.5**: Register typing + pointer sources (config-gated)
  - Only register if `[sources.typing] enabled = true` / `[sources.pointer] enabled = true`
  - Files: `cmd/sigild/sources_*.go`
  - Test: `go build ./cmd/sigild/`
  - Depends: Task 8.1–8.4

- [ ] **Task 8.6**: Phase 8 verification
  - Test: `make check`
  - Depends: Task 8.5

---

### Phase 9: Calendar Integration (P1)

- [ ] **Task 9.1**: Implement macOS EventKit calendar source
  - Poll EventKit every 5 minutes for upcoming events (cgo, Calendar permission)
  - Emit: meeting_start, meeting_end, free_block
  - Capture: title, duration, attendee count, recurrence — NOT attendee names
  - Configurable calendar filter
  - Files: `internal/collector/sources/calendar_darwin.go`
  - Test: `go test ./internal/collector/sources/ -run TestCalendar -count=1`
  - Depends: Task 1.1

- [ ] **Task 9.2**: Implement no-op calendar source for non-macOS [P]
  - Files: `internal/collector/sources/calendar_other.go`
  - Test: `go build ./...`
  - Depends: Task 1.1

- [ ] **Task 9.3**: Register calendar source
  - Files: `cmd/sigild/sources_darwin.go`
  - Test: `go build ./cmd/sigild/`
  - Depends: Task 9.1, Task 9.2

- [ ] **Task 9.4**: Phase 9 verification
  - Test: `make check`
  - Depends: Task 9.3

---

### Phase 10: Source Registration + Health (Final)

- [ ] **Task 10.1**: Update health handler to report active source count
  - Show which sources are enabled, which are running, which failed
  - Files: `cmd/sigild/handler_health.go`
  - Test: `go test ./cmd/sigild/ -run TestHealth -count=1`
  - Depends: All previous phases

- [ ] **Task 10.2**: Final verification
  - Test: `make check && make coverage`
  - Depends: Task 10.1

---

## Summary

| Phase | Tasks | Parallelizable | Effort |
|-------|-------|----------------|--------|
| 1 | 3 | 2 | 0.5 days |
| 2 | 6 | 3 | 2 days |
| 3 | 4 | 0 | 1 day |
| 4 | 4 | 1 | 0.5 days |
| 5 | 5 | 3 | 1 day |
| 6 | 8 | 6 | 3 days |
| 7 | 4 | 1 | 1 day |
| 8 | 6 | 4 | 2 days |
| 9 | 4 | 1 | 1.5 days |
| 10 | 2 | 0 | 0.5 days |
| **Total** | **46** | **21** | **~13 days** |

### Critical Path
Phase 1 → Phases 2-5 (P0 block, 5 days) → Phase 10
Phases 6-9 can run in parallel with each other after Phase 1.
