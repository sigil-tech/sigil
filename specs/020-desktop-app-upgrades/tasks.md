# 020 — Desktop App Upgrades: Tasks

**Status:** In Progress
**Author:** Alec Feeman
**Date:** 2026-03-30
**Branch:** `feat/020-desktop-app-upgrades`

---

## Tasks

### Phase 1: Onboarding Wizard + Init Socket Methods

- [x] **Task 1.1**: Implement `check-init` and `init` socket handlers in sigild
  - Extract config-writing logic from `init_subcommand.go` into a shared function
  - `check-init` returns `{initialized: bool, config_path: string}` by checking if `config.toml` exists
  - `init` accepts config payload (watch_dirs, inference_mode, notification_level, plugins), writes `config.toml`, starts daemon services
  - Register both handlers in `cmd/sigild/main.go` handler map
  - Files: `cmd/sigild/handler_init.go`, `cmd/sigild/init_subcommand.go`, `cmd/sigild/main.go`
  - Test: Unit test: `check-init` returns false with no config, true with config. `init` writes valid `config.toml`
  - Depends: none

- [x] **Task 1.2**: Implement `CheckInit()` and `RunInit()` Go bindings [P]
  - `CheckInit()` calls `check-init` socket method, returns `CheckInitResult`
  - `RunInit(config InitConfig)` calls `init` socket method with user's wizard choices
  - Add both to Wails bindings in `app.go`
  - Files: `cmd/sigil-app/app_init.go`, `cmd/sigil-app/app.go`
  - Test: `go test ./cmd/sigil-app/ -run TestApp_CheckInit` and `TestApp_RunInit`
  - Depends: Task 1.1

- [x] **Task 1.3**: Build `Wizard.tsx` multi-step form component [P]
  - 7 steps: Welcome, Watch Dirs, Inference, Plugins, Notifications, Cloud, Confirm
  - Local state collects all values, step navigation with back/next buttons
  - Watch Dirs step: file picker for adding paths, default `~/` with exclusions
  - Inference step: radio buttons for local-only, cloud, hybrid with trade-off explanations
  - Confirm step: summary of choices, "Start Sigil" button calls `RunInit()`
  - Files: `cmd/sigil-app/frontend/src/views/Wizard.tsx`
  - Test: Visual verification: navigate all steps, form state persists between steps
  - Depends: none (frontend-only, can stub backend calls)

- [x] **Task 1.4**: Wire wizard into app startup flow
  - On mount in `App.tsx`, call `CheckInit()`. If not initialized, render `<Wizard />` instead of normal views
  - After wizard completes, transition to normal views without app restart
  - Add "Re-run Setup" option in Settings view that opens wizard
  - Files: `cmd/sigil-app/frontend/src/App.tsx`, `cmd/sigil-app/frontend/src/views/Settings.tsx`
  - Test: Launch with no `config.toml` -> wizard appears. Complete wizard -> normal views. Launch with existing config -> no wizard
  - Depends: Task 1.2, Task 1.3

- [x] **Task 1.5**: Add IDE and plugin auto-detection to wizard [P]
  - Detect installed IDEs (VS Code, JetBrains) and dev tools (git) via filesystem checks
  - Pre-configure relevant plugins based on detected tools
  - Offer to install VS Code/JetBrains extension if IDE detected
  - Files: `cmd/sigil-app/app_init.go`, `cmd/sigil-app/frontend/src/views/Wizard.tsx`
  - Test: On a system with VS Code installed, wizard suggests enabling VS Code plugin
  - Depends: Task 1.2, Task 1.3

**Phase 1 verification:** Launch `sigil-app` with no `config.toml` -- wizard appears. Complete wizard -- `config.toml` written, sigild starts, app transitions to normal views. Launch with existing `config.toml` -- wizard does not appear. Settings > "Re-run Setup" opens wizard.

---

### Phase 2: System Tray (Platform-Native)

- [ ] **Task 2.1**: Define cross-platform `Tray` interface and factory
  - Interface: `Show()`, `SetConnected(bool)`, `SetLevel(int)`, `OnOpen(func())`, `OnQuit(func())`, `OnSetLevel(func(int))`, `OnPause(func())`, `Destroy()`
  - `NewTray(iconPath string) (Tray, error)` factory function with build tags per platform
  - Files: `cmd/sigil-app/tray.go`
  - Test: `go vet ./cmd/sigil-app/...` passes on all GOOS targets
  - Depends: none

- [ ] **Task 2.2**: Implement macOS tray via `progrium/macdriver` [P]
  - `NSStatusItem` in macOS menubar with `NSMenu`
  - Icon loaded from embedded `assets/icon.png` (normal) and `assets/icon-dimmed.png` (disconnected)
  - Menu items: Open Sigil, Status label, Notification Level submenu (0-4), Pause/Resume, Quit
  - Runs on main thread (macOS requirement)
  - Files: `cmd/sigil-app/tray_darwin.go`
  - Test: Tray icon visible in macOS menubar, click shows menu, "Open" brings window to front
  - Depends: Task 2.1

- [ ] **Task 2.3**: Implement Linux tray via D-Bus StatusNotifierItem [P]
  - Use `godbus/dbus/v5` for `org.kde.StatusNotifierItem` protocol
  - Fall back to no-op tray with log warning if no StatusNotifierHost available
  - Files: `cmd/sigil-app/tray_linux.go`
  - Test: KDE: tray icon in system tray. GNOME + AppIndicator: tray icon visible. GNOME no AppIndicator: graceful fallback, warning logged
  - Depends: Task 2.1

- [ ] **Task 2.4**: Implement Windows tray via `Shell_NotifyIcon` syscall [P]
  - Load icon from embedded assets
  - Handle `WM_COMMAND` messages for menu interaction
  - Files: `cmd/sigil-app/tray_windows.go`
  - Test: Tray icon in Windows notification area, right-click shows menu
  - Depends: Task 2.1

- [ ] **Task 2.5**: Wire tray into app lifecycle
  - Initialize tray after Wails app starts in `main.go`
  - Wire tray callbacks to `App` methods (Open -> `WindowShow`, Quit -> `runtime.Quit`, SetLevel -> `SetLevel()`, Pause -> toggle)
  - On window close event, hide window instead of quitting (tray persists)
  - Dynamic icon swap based on `connection:changed` Wails event
  - Files: `cmd/sigil-app/main.go`, `cmd/sigil-app/app.go`
  - Test: Close window -> tray icon persists, app still running. Tray icon dims when sigild disconnects. Level submenu changes level (verify via `sigilctl status`). "Quit" terminates completely
  - Depends: Task 2.2 or 2.3 or 2.4 (whichever platform), Task 1.2

**Phase 2 verification:** macOS: tray icon in menubar, click shows menu, "Open" brings window to front. Linux: tray icon in system tray (KDE/GNOME+AppIndicator), graceful fallback without. Windows: tray icon in notification area. Close window: tray persists. Icon dims/restores with daemon connection. Level changes propagate to sigild. "Quit" terminates.

---

### Phase 3: Cloud Account Integration

- [ ] **Task 3.1**: Implement `cloud-auth` and `cloud-status` socket handlers
  - `cloud-auth`: accepts `{api_key: string}`, stores key in config, enables cloud, returns `{ok, tier, email}`
  - `cloud-status`: returns `{connected, tier, email, sync_state, ml_predictions_used, llm_tokens_used, llm_tokens_limit}`
  - Files: `cmd/sigild/handler_cloud_auth.go`, `cmd/sigild/main.go`
  - Test: Unit test: `cloud-auth` writes key to config. `cloud-status` returns correct state
  - Depends: none (spec 019 cloud auth endpoints must be deployed)

- [ ] **Task 3.2**: Implement OAuth flow in `app_cloud.go` [P]
  - `CloudSignIn()`: start local HTTP server on random port, open browser to `https://app.sigilos.io/oauth/desktop?redirect_port=PORT`, wait for callback with `?token=...` (5-min timeout), call sigild `cloud-auth` with token, shut down server
  - `GetCloudStatus()`: call `cloud-status` socket method
  - `CloudSignOut()`: clear cloud config via socket method
  - Add all to Wails bindings
  - Files: `cmd/sigil-app/app_cloud.go`, `cmd/sigil-app/app.go`
  - Test: `go test ./cmd/sigil-app/ -run TestApp_CloudSignIn` with mock HTTP callback
  - Depends: Task 3.1

- [ ] **Task 3.3**: Add cloud UI to StatusBar and Settings [P]
  - `StatusBar.tsx`: tier badge (Free=gray, Pro=blue, Team=purple), sync indicator dot (green/yellow/gray/red)
  - `Settings.tsx`: Cloud section with sign-in/sign-out button, account details (email, tier), sync toggle, usage bars (ML predictions, LLM tokens)
  - Files: `cmd/sigil-app/frontend/src/components/StatusBar.tsx`, `cmd/sigil-app/frontend/src/views/Settings.tsx`, `cmd/sigil-app/frontend/src/components/CloudStatus.tsx`
  - Test: Visual verification: tier badge renders, sync indicator reflects state, sign-in/sign-out flow works
  - Depends: Task 3.2

- [ ] **Task 3.4**: Build `UpgradePrompt.tsx` component
  - Rendered inline when Free user accesses Pro/Team feature
  - Links to `https://app.sigilos.io/billing`
  - Clear description of what the feature requires and tier needed
  - Files: `cmd/sigil-app/frontend/src/components/UpgradePrompt.tsx`, integration points in Settings and Analytics views
  - Test: Free-tier user navigating to cloud ML settings sees upgrade prompt with billing link
  - Depends: Task 3.3

**Phase 3 verification:** Click "Sign In" -- browser opens to Sigil Cloud auth page. Complete OAuth -- tier badge appears in status bar. `cloud-status` reflects correct tier and sync state. Free user sees upgrade prompt on Pro features. Sign out clears cloud config.

---

### Phase 4: Suggestion Analytics

- [x] **Task 4.1**: Implement `analytics` and `export-suggestions` socket handlers
  - `analytics`: accepts `{days: int}` (default 30), returns `{daily_counts, category_breakdown, hourly_distribution, streak_days}` via SQL aggregations against `suggestions` table
  - `export-suggestions`: accepts `{format: "json"|"csv", from: string, to: string}`, returns `{data: string}`
  - Ensure `suggestions` table is indexed on `created_at` and `category`
  - Files: `cmd/sigild/handler_analytics.go`, `cmd/sigild/handler_export.go`, `cmd/sigild/main.go`
  - Test: Unit test: `analytics` returns correct aggregations against test data. `export-suggestions` produces valid CSV/JSON
  - Depends: none

- [x] **Task 4.2**: Implement `GetAnalytics()` and `ExportSuggestions()` Go bindings [P]
  - `GetAnalytics(days int)` calls `analytics` socket method
  - `ExportSuggestions(format, from, to string)` calls `export-suggestions`, triggers file save dialog
  - Add both to Wails bindings
  - Files: `cmd/sigil-app/app_analytics.go`, `cmd/sigil-app/app.go`
  - Test: `go test ./cmd/sigil-app/ -run TestApp_GetAnalytics`
  - Depends: Task 4.1

- [x] **Task 4.3**: Build chart components [P]
  - `AcceptanceChart.tsx`: line chart of acceptance rate (Canvas 2D or uPlot, daily granularity)
  - `DailyChart.tsx`: bar chart of daily suggestion counts
  - `CategoryChart.tsx`: horizontal bars for top categories
  - `HeatmapChart.tsx`: 24-cell row showing hourly distribution
  - `charts.ts`: shared chart utilities (axis formatting, color palette, responsive sizing)
  - Chart dependency must be < 50KB gzipped (uPlot or Canvas 2D direct)
  - Files: `cmd/sigil-app/frontend/src/components/AcceptanceChart.tsx`, `cmd/sigil-app/frontend/src/components/DailyChart.tsx`, `cmd/sigil-app/frontend/src/components/CategoryChart.tsx`, `cmd/sigil-app/frontend/src/components/HeatmapChart.tsx`, `cmd/sigil-app/frontend/src/lib/charts.ts`
  - Test: Each chart renders correctly with mock data, responsive to container size
  - Depends: none (frontend-only)

- [x] **Task 4.4**: Build `Analytics.tsx` view and wire navigation
  - Compose all chart components with real data from `GetAnalytics()`
  - Date range selector (7, 30, 90 days, custom) that re-fetches data
  - Streak indicator display
  - CSV/JSON export buttons calling `ExportSuggestions()`
  - Add "Analytics" tab to navigation in `App.tsx` (position 5, before Settings)
  - Files: `cmd/sigil-app/frontend/src/views/Analytics.tsx`, `cmd/sigil-app/frontend/src/App.tsx`
  - Test: Analytics view loads with real data. All charts render. Date range updates charts. Export downloads valid file
  - Depends: Task 4.2, Task 4.3

**Phase 4 verification:** Analytics view loads with real data from sigild. Acceptance rate chart shows correct trend. Daily count chart matches `sigilctl suggestions` counts. Category breakdown matches actual distribution. Date range selector updates all charts. CSV and JSON export produce valid files.

---

### Phase 5: Improved Ask Sigil

- [x] **Task 5.1**: Modify `Ask()` binding to support context
  - Update `Ask()` to accept an optional context struct (current task, branch, recent files)
  - Include context in the `ai-query` payload sent to sigild
  - Files: `cmd/sigil-app/app.go`
  - Test: `go test ./cmd/sigil-app/ -run TestApp_AskWithContext` -- context fields included in socket payload
  - Depends: none

- [x] **Task 5.2**: Build `ChatMessage.tsx` and `CodeBlock.tsx` components [P]
  - `ChatMessage.tsx`: user messages right-aligned with accent background, assistant messages left-aligned with markdown parsing
  - `CodeBlock.tsx`: detect language from fence marker, syntax highlighting via Prism.js (~6KB) or manual regex tokenizer, "Copy" button using `navigator.clipboard.writeText()`
  - Files: `cmd/sigil-app/frontend/src/components/ChatMessage.tsx`, `cmd/sigil-app/frontend/src/components/CodeBlock.tsx`
  - Test: Code blocks render with syntax highlighting for Go, Python, TypeScript. Copy button copies code to clipboard
  - Depends: none (frontend-only)

- [x] **Task 5.3**: Build `ContextPanel.tsx` component [P]
  - Displays: current task name, Git branch, last 5 edited file paths
  - Collapsible (expanded on first message, collapsed after)
  - Data fetched from `GetCurrentTask()` and `GetStatus()`, refreshed on window focus
  - Files: `cmd/sigil-app/frontend/src/components/ContextPanel.tsx`
  - Test: Panel shows task and branch. Collapse/expand toggles work. Data refreshes on focus
  - Depends: none (frontend-only)

- [x] **Task 5.4**: Rewrite `AskSigil.tsx` as multi-turn chat interface
  - Replace single-query form with chat UI using `messages[]` state
  - On submit: append user message, call `App.Ask(query, context)`, append assistant response
  - Scroll to bottom on new message, loading indicator during query
  - Integrate `ChatMessage`, `CodeBlock`, and `ContextPanel` components
  - Clear conversation button resets the view
  - Preserve conversation until user clears or closes app
  - Files: `cmd/sigil-app/frontend/src/views/AskSigil.tsx`
  - Test: Send query -> response visible. Send follow-up -> full history visible. Code blocks highlighted. Context panel shows task/branch. Clear resets view
  - Depends: Task 5.1, Task 5.2, Task 5.3

**Phase 5 verification:** Send query, receive response, both visible in chat. Send follow-up, full history visible. Code blocks render with syntax highlighting. Copy button copies code. Context panel shows current task and branch. Clear conversation resets view.

---

### Phase 6: Activity Timeline

- [x] **Task 6.1**: Implement `timeline` socket handler
  - Accepts `{date: string, types: []string, offset: int, limit: int}`
  - Returns `{events: [{timestamp, kind, summary, detail}], total: int}`
  - Queries `events` table with optional kind filter, ordered by timestamp descending
  - Summary generated from event payload (e.g., "Edited internal/analyzer/detector.go", "Committed: fix threshold")
  - Files: `cmd/sigild/handler_timeline.go`, `cmd/sigild/main.go`
  - Test: Unit test: returns correct events for a date, filter by type works, pagination works
  - Depends: none

- [x] **Task 6.2**: Implement `GetTimeline()` Go binding [P]
  - `GetTimeline(date string, types []string, offset, limit int)` calls `timeline` socket method
  - Add to Wails bindings
  - Files: `cmd/sigil-app/app_timeline.go`, `cmd/sigil-app/app.go`
  - Test: `go test ./cmd/sigil-app/ -run TestApp_GetTimeline`
  - Depends: Task 6.1

- [x] **Task 6.3**: Build `TimelineEvent.tsx` component [P]
  - Icon per event type (file: document, git: branch, process: terminal, task: flag, suggestion: lightbulb)
  - Timestamp (HH:MM format), summary text
  - Expandable: click shows full detail JSON in formatted panel
  - Task session dividers: labeled separator when task changes
  - Files: `cmd/sigil-app/frontend/src/components/TimelineEvent.tsx`
  - Test: Component renders with mock data, expand/collapse works, icons match event types
  - Depends: none (frontend-only)

- [x] **Task 6.4**: Build `Timeline.tsx` view and integrate into Summary
  - Fetches events for today via `GetTimeline(date, types, offset, limit)`
  - Renders chronological list with `<TimelineEvent />` components
  - Filter bar: checkboxes for event types (file, git, process, task, suggestion)
  - Load more button at bottom (pagination)
  - Add as sub-view of Summary (or accessible from Summary)
  - Files: `cmd/sigil-app/frontend/src/views/Timeline.tsx`, `cmd/sigil-app/frontend/src/views/DaySummary.tsx`
  - Test: Timeline shows today's events in order. Filter checkboxes work. Click event expands. Pagination loads more
  - Depends: Task 6.2, Task 6.3

**Phase 6 verification:** Timeline shows today's events in chronological order. Filter checkboxes filter by event type. Click event expands to show details. Pagination loads more events. Task session boundaries render as dividers.

---

### Phase 7: Notification Upgrades (Granular + Native)

- [ ] **Task 7.1**: Implement `dnd-schedule` and `mute-category` socket handlers
  - `dnd-schedule` GET: returns `{enabled, start, end, days}`. SET: accepts same, writes to `[notifications.dnd]` in config
  - `mute-category` GET: returns `{muted: []string}`. SET: accepts `{muted: []string}`, writes to `[notifications]` in config
  - Integrate checks into notification pipeline: DND time check before delivery, muted category check before delivery
  - Files: `cmd/sigild/handler_dnd.go`, `cmd/sigild/handler_mute.go`, `cmd/sigild/main.go`
  - Test: Unit test: set DND schedule -> notifications suppressed during DND hours. Mute category -> that category suppressed
  - Depends: none

- [ ] **Task 7.2**: Build `DndSchedule.tsx` and `CategoryMute.tsx` components [P]
  - `DndSchedule.tsx`: time pickers for start/end, day-of-week toggles, enable/disable switch
  - `CategoryMute.tsx`: list of all known categories with toggle switches
  - Files: `cmd/sigil-app/frontend/src/components/DndSchedule.tsx`, `cmd/sigil-app/frontend/src/components/CategoryMute.tsx`
  - Test: UI renders, toggles update state, save persists via socket methods
  - Depends: none (frontend-only)

- [ ] **Task 7.3**: Integrate notification settings into Settings view [P]
  - Add "Do Not Disturb" and "Category Filters" subsections under Notifications in Settings
  - Wire to `dnd-schedule` and `mute-category` socket methods
  - Files: `cmd/sigil-app/frontend/src/views/Settings.tsx`
  - Test: Set DND schedule from UI -> verify via `sigilctl`. Mute category -> verify suppression
  - Depends: Task 7.1, Task 7.2

- [ ] **Task 7.4**: Implement focus-based native notification routing
  - When `sigil-app` window loses focus -> enable native notifications for new suggestions
  - When window gains focus -> disable native notifications
  - Use existing platform-native notification code from spec 016 (`notify_darwin.go`, `notify_linux.go`, `notify_windows.go`)
  - Accept/Dismiss actions on notifications call `AcceptSuggestion()` / `DismissSuggestion()` bindings
  - Click handler brings window to front and navigates to the suggestion (emit Wails event `navigate-to-suggestion` with ID)
  - Files: `cmd/sigil-app/app.go`, `cmd/sigil-app/notify_darwin.go`, `cmd/sigil-app/notify_linux.go`, `cmd/sigil-app/notify_windows.go`
  - Test: Background app -> native notification appears. Focus app -> native notification stops. Accept on notification -> suggestion marked accepted. Click notification -> app opens to that suggestion
  - Depends: Phase 2 (tray for background detection)

- [ ] **Task 7.5**: Frontend handling for notification navigation
  - Listen for `navigate-to-suggestion` Wails event in `App.tsx`
  - Navigate to `SuggestionDetail` for the specified suggestion ID
  - Suppress in-app notification when native notification is delivered (avoid double)
  - Files: `cmd/sigil-app/frontend/src/App.tsx`, `cmd/sigil-app/frontend/src/views/SuggestionList.tsx`
  - Test: Click native notification -> app window opens and shows the correct suggestion detail
  - Depends: Task 7.4

**Phase 7 verification:** Mute a category -- suggestions of that category produce no notification. Unmute -- notifications resume. Set DND schedule -- no notifications during DND hours. Background app -- native notification appears. Focus app -- native notification stops. Accept on notification -- suggestion marked accepted. Click notification -- app opens to that suggestion.

---

### Phase 8: Auto-Update

- [ ] **Task 8.1**: Implement update checker in `app_update.go`
  - `CheckForUpdate()`: query GitHub Releases API, compare tag against compiled-in version via `golang.org/x/mod/semver`, return `UpdateInfo{Version, Changelog, URL, Checksum}` or nil if up-to-date
  - `DownloadUpdate(url, checksum string)`: download binary to temp dir, verify SHA256. Reject non-HTTPS URLs
  - `ApplyUpdate()`: atomic rename (current -> backup, staged -> current), return success for caller to prompt restart
  - Check on startup (respecting `update_mode` config), then on configured interval
  - Files: `cmd/sigil-app/app_update.go`, `cmd/sigil-app/app.go`
  - Test: `go test ./cmd/sigil-app/ -run TestApp_CheckForUpdate` with mock HTTP response. Verify HTTPS-only enforcement. Verify checksum validation
  - Depends: none (spec 018 GitHub Releases artifacts must exist)

- [ ] **Task 8.2**: Build `UpdateBanner.tsx` component [P]
  - Rendered at top of main window when update available
  - Shows version, first 3 lines of changelog, "Update Now" button, "Dismiss" (snooze for 24h)
  - Progress indicator during download
  - Post-apply: "Restart to complete update" prompt
  - Files: `cmd/sigil-app/frontend/src/components/UpdateBanner.tsx`
  - Test: Banner renders with version and changelog. Dismiss hides for 24h. Update Now triggers download flow
  - Depends: none (frontend-only)

- [ ] **Task 8.3**: Add update settings to Settings view [P]
  - "Updates" section with mode selector (Auto / Notify / Disabled)
  - Interval input (default 24h)
  - Current version display (compiled-in version string)
  - "Check Now" button that calls `CheckForUpdate()` and shows result
  - Files: `cmd/sigil-app/frontend/src/views/Settings.tsx`
  - Test: Mode changes persist to config. Check Now finds/doesn't find update correctly
  - Depends: Task 8.1

- [ ] **Task 8.4**: Wire update checker into app startup
  - On startup, check config for `update_mode`. If not "disabled", call `CheckForUpdate()`
  - If update found, emit Wails event `update:available` with `UpdateInfo`
  - Frontend listens and renders `UpdateBanner`
  - Schedule periodic re-checks based on `update_interval` config
  - Files: `cmd/sigil-app/main.go`, `cmd/sigil-app/frontend/src/App.tsx`
  - Test: App detects newer version on startup -> banner shows. "Disabled" mode -> no check. "Update Now" downloads, verifies, replaces binary. Restart loads new version
  - Depends: Task 8.1, Task 8.2, Task 8.3

**Phase 8 verification:** App detects newer version on startup. Banner shows with version and changelog. "Update Now" downloads, verifies SHA256, replaces binary. Restart loads new version. "Disabled" mode: no update check. Update over HTTP is rejected (HTTPS only).

---

### Phase 9: Keyboard Shortcuts & Accessibility

- [ ] **Task 9.1**: Implement `shortcuts.ts` keyboard shortcut manager
  - Register global keyboard shortcuts via `document.addEventListener("keydown", ...)`
  - `Meta+1` through `Meta+6`: navigate to Suggestions, Summary, Ask, Plugins, Analytics, Settings
  - `Meta+,`: open Settings. `Meta+K`: focus Ask input. `Meta+F`: focus search
  - Use `Control` instead of `Meta` on Linux/Windows (detect via `navigator.platform`)
  - Files: `cmd/sigil-app/frontend/src/lib/shortcuts.ts`
  - Test: `Cmd+1` through `Cmd+6` switch views. `Cmd+,` opens Settings. `Cmd+K` focuses Ask input
  - Depends: Phases 4-6 (all views must exist for navigation targets)

- [ ] **Task 9.2**: Accessibility pass -- ARIA attributes [P]
  - Add `role` attributes to custom interactive components across all views
  - Add `aria-label` to icon-only buttons (Accept, Dismiss, Copy, Expand, filter toggles)
  - Add `aria-live="polite"` to suggestion list and chat message area for screen reader announcements
  - Files: all view and component files in `cmd/sigil-app/frontend/src/`
  - Test: VoiceOver (macOS) reads all buttons, inputs, and status text correctly
  - Depends: Phases 4-6 (all views must exist)

- [ ] **Task 9.3**: Accessibility pass -- focus management [P]
  - Ensure all focusable elements have visible focus indicators (outline, not just color change)
  - Focus indicators visible in both light and dark themes
  - Tab order follows visual layout (top-left to bottom-right, sidebar before content)
  - Escape key navigates back from detail views
  - Files: `cmd/sigil-app/frontend/src/style.css`, all view files
  - Test: Tab through all interactive elements in each view. Focus indicators visible in light and dark themes. Tab order is logical
  - Depends: Phases 4-6 (all views must exist)

- [ ] **Task 9.4**: Integration test for keyboard navigation
  - Verify full keyboard workflow: launch -> `Cmd+1` to suggestions -> Tab to first card -> Enter to detail -> Tab to Accept -> Enter -> Escape back to list
  - Verify `Cmd+K` focuses Ask input from any view
  - Verify `Cmd+F` triggers search in applicable views
  - Files: none (testing only)
  - Test: Manual verification of complete keyboard-only workflow on each platform
  - Depends: Task 9.1, Task 9.2, Task 9.3

**Phase 9 verification:** `Cmd+1` through `Cmd+6` switch views. `Cmd+,` opens Settings. `Cmd+K` focuses Ask input. Tab through all interactive elements in each view. VoiceOver reads all buttons, inputs, and status text. Focus indicators visible in both light and dark themes.

---

### Phase 10: Performance Hardening

- [ ] **Task 10.1**: Build `VirtualList.tsx` generic virtual scroll component
  - Props: `items: T[]`, `rowHeight: number`, `renderItem: (item: T) => JSX.Element`, `overscan: number`
  - Renders only visible items + overscan buffer using `position: absolute` with calculated `top`
  - DOM must never contain more than 100 suggestion elements (spec requirement 85)
  - Files: `cmd/sigil-app/frontend/src/components/VirtualList.tsx`
  - Test: Render 500 items -> DOM contains only ~visible + overscan elements. Scroll is smooth
  - Depends: none (frontend-only)

- [ ] **Task 10.2**: Apply virtual scrolling and pagination to existing views [P]
  - `SuggestionList.tsx`: wrap suggestion cards in `<VirtualList />`, implement `GetSuggestions(offset, limit)` pagination (50 items per page, load on scroll)
  - `Timeline.tsx`: wrap timeline events in `<VirtualList />`
  - Plugin log viewer: wrap log lines in `<VirtualList />`
  - Files: `cmd/sigil-app/frontend/src/views/SuggestionList.tsx`, `cmd/sigil-app/frontend/src/views/Timeline.tsx`, `cmd/sigil-app/frontend/src/components/PluginLogViewer.tsx`, `cmd/sigil-app/app.go`
  - Test: Scroll 500 suggestions smoothly (no jank). DOM element count stays bounded. Pagination fetches more on scroll
  - Depends: Task 10.1, Phases 4-6

- [ ] **Task 10.3**: Implement lazy loading for views [P]
  - Split into dynamic imports: `Analytics`, `Timeline`, `Wizard`, `Plugins`
  - Only `Suggestions` and `Summary` eagerly loaded (most common views)
  - Files: `cmd/sigil-app/frontend/src/App.tsx`
  - Test: Navigate to Analytics -> component loads on demand (verify via network tab). Bundle chunks visible in `vite build` output
  - Depends: Phases 4-6

- [ ] **Task 10.4**: Add response caching to Go backend [P]
  - 2-second TTL cache for `status`, `suggestions`, `day-summary`, `task` responses in `app.go`
  - Invalidate on push events for the relevant topic
  - Files: `cmd/sigil-app/app.go`
  - Test: Rapid view switching does not produce redundant socket calls (verify via daemon debug log)
  - Depends: none

- [ ] **Task 10.5**: Performance measurement and validation
  - Measure RSS with 500 suggestions: must be < 50MB (Activity Monitor / `ps`)
  - Measure startup time to first interactive view: must be < 2 seconds
  - Measure scroll performance at 500+ items (DevTools FPS meter)
  - Measure frontend bundle size: must be < 200KB gzipped (`vite build` output)
  - Verify no tight polling loops in socket traffic (daemon debug log)
  - Files: none (testing only)
  - Test: All targets met: RSS < 50MB, startup < 2s, scroll smooth, bundle < 200KB gz, no tight polls
  - Depends: Task 10.2, Task 10.3, Task 10.4

**Phase 10 verification:** RSS < 50MB with 500 suggestions in list. App interactive within 2s of launch. Scroll 500 suggestions smoothly. Analytics loads on demand. Frontend bundle < 200KB gzipped. No tight polling loops in socket traffic.

---

## Summary

| Phase | Feature | Tasks | Parallelizable | Effort | Key Dependencies |
|-------|---------|-------|----------------|--------|-----------------|
| 1 | Onboarding Wizard | 5 | 3 | 5-7 days | None |
| 2 | System Tray | 5 | 3 | 5-7 days | None (parallel with Phase 1) |
| 3 | Cloud Integration | 4 | 2 | 4-5 days | Spec 019 cloud auth endpoints |
| 4 | Suggestion Analytics | 4 | 2 | 4-5 days | None |
| 5 | Improved Ask Sigil | 4 | 2 | 3-4 days | None |
| 6 | Activity Timeline | 4 | 2 | 3-4 days | None |
| 7 | Notification Upgrades | 5 | 2 | 4-5 days | Phase 2 (tray) |
| 8 | Auto-Update | 4 | 2 | 3-4 days | Spec 018 (GitHub Releases) |
| 9 | Keyboard Shortcuts | 4 | 2 | 2-3 days | Phases 4-6 (all views) |
| 10 | Performance Hardening | 5 | 3 | 2-3 days | Phases 4-6 (all views) |
| **Total** | | **44** | **23** | **36-47 days** | |

### Parallelization Notes

- **Phases 1 + 2** can run in parallel (no shared code)
- **Phases 4 + 5 + 6** can run in parallel (independent views, no shared state)
- **Phase 3** can start once spec 019 cloud auth endpoints are deployed
- **Phase 7** requires Phase 2 (tray for background/focus detection)
- **Phase 8** can run in parallel with Phases 4-6
- **Phases 9 + 10** are finishing passes that run after all views are built (Phases 4-6)

### Critical Path

Phase 1/2 (parallel) -> Phases 4/5/6 (parallel) -> Phase 7 -> Phases 9/10 (parallel)

Phase 3 and Phase 8 are off the critical path and can slot in whenever their external dependencies are met.
