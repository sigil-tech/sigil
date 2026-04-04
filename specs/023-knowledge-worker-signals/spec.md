# 023 — Knowledge Worker Signal Enrichment

**Status:** Draft
**Author:** Alec Feeman
**Date:** 2026-04-04

---

## Problem

Sigil currently captures signals optimized for software engineers: file edits, git activity, terminal commands, process lifecycle. Knowledge workers outside engineering — product managers, designers, analysts, executives — spend their day in browsers, calendars, documents, and communication tools. Sigil sees none of this.

Even for engineers, critical context is missing. The daemon knows "the user switched to Chrome" but not "the user is reading React docs for the third time this session." It knows "a commit happened" but not the commit message, branch, or diff stats. It detects "the user was idle" only by the absence of events, not by actual idle/active state.

Without richer signals, the ML models can't learn meaningful patterns. Recommendations stay generic ("you context-switch a lot") instead of actionable ("you spend 40 minutes researching before each PR — try writing a design doc first").

## Goals

1. **Daemon-native sources**: Add idle detection, typing velocity, git enrichment, and file metadata enrichment directly in sigild — zero setup, works for everyone.

2. **Browser extension**: Capture page titles and domains (not URLs, not content) from Chrome/Firefox/Safari via a lightweight extension that pushes events to sigild's ingest endpoint.

3. **Calendar integration**: Read-only access to the local calendar (macOS EventKit, Google Calendar API) to capture meeting boundaries and free/busy blocks.

4. **Plugin-based third-party sources**: Slack, Zoom, Google Docs, Notion, and Microsoft Teams via the existing plugin system — opt-in, separate install, same event schema.

5. **Sufficient signal density for ML**: After this spec, sigild should capture enough data for sigil-ml to answer: "When is the user most productive?", "What causes context-switch storms?", "How much time goes to meetings vs deep work?", and "What research patterns precede productive coding sessions?"

## Non-Goals

- Keylogging or screen recording — we capture typing *rate*, not keystrokes.
- Reading document content — we capture document names and metadata, not text.
- Browser URL capture — we capture domain and page title, not full URLs (privacy).
- Real-time collaboration monitoring — we detect "user is in Google Docs" not "user is editing paragraph 3."
- Modifying the analyzer or notifier — this spec is about data collection, not recommendations.

---

## Design

### Part 1: Daemon-Native Sources

These require no user setup beyond running sigild. They use the existing `collector.Source` interface and emit standard `event.Event` values.

#### 1.1 Idle Detection (`sources.IdleSource`)

**Platform:** macOS (CGEventSource), Linux (XScreenSaver), Windows (GetLastInputInfo)

**Events:**
- `idle:start` — no keyboard/mouse input for 5 minutes (configurable)
- `idle:end` — input resumes after an idle period
- `screen:lock` / `screen:unlock` — screen saver or lock screen engaged/disengaged

**Event kind:** `idle`

**Payload:**
```json
{
  "state": "idle_start|idle_end|screen_lock|screen_unlock",
  "idle_seconds": 342,
  "last_input_type": "keyboard|mouse"
}
```

**Requirements:**
1. Idle threshold MUST be configurable via `[daemon] idle_threshold` (default: 5m)
2. Idle events MUST NOT fire during screen lock (lock/unlock are separate events)
3. RSS impact MUST be < 1MB (polling, not hooks)
4. macOS: use `CGEventSourceSecondsSinceLastEventType` (no Accessibility needed)
5. Linux: use `XScreenSaverQueryInfo` via Xlib
6. Windows: use `GetLastInputInfo` via syscall

#### 1.2 Typing Velocity (`sources.TypingSource`)

**Platform:** macOS (CGEventTap), Linux (libinput), Windows (low-level keyboard hook)

**What it captures:** Keystrokes per minute, aggregated in 30-second windows. NOT individual keys — only the count.

**Event kind:** `typing`

**Payload:**
```json
{
  "keys_per_minute": 85,
  "window_seconds": 30,
  "active_app": "GoLand"
}
```

**Requirements:**
7. MUST capture keystroke COUNT only — never individual keys, sequences, or key identifiers
8. Aggregation window MUST be at least 30 seconds (no per-keystroke events)
9. macOS: requires Accessibility permission (shared with window title capture)
10. Events MUST include the frontmost app name at time of measurement
11. Source MUST be opt-in via `[sources.typing] enabled = true` (default: false)
12. When disabled, the source MUST not install any event hooks

#### 1.3 Git Enrichment (enhance existing `sources.GitSource`)

**What's missing:** The git source detects commits, branch changes, and staging via fsnotify on `.git/` files, but captures no semantic content.

**Enriched payloads:**

On commit (`git_kind: "commit"`):
```json
{
  "git_kind": "commit",
  "repo_root": "/Users/alec/sigil",
  "message": "fix: resolve race in subscription handler",
  "branch": "feat/desktop-polish",
  "files_changed": 3,
  "insertions": 42,
  "deletions": 7,
  "hash": "abc123f"
}
```

On branch change (`git_kind: "head_change"`):
```json
{
  "git_kind": "head_change",
  "repo_root": "/Users/alec/sigil",
  "branch": "main",
  "previous_branch": "feat/desktop-polish"
}
```

**Requirements:**
13. On COMMIT_EDITMSG write, read `git log -1 --format='%H|%s'` and `git diff --stat HEAD~1..HEAD` to populate message, hash, files_changed, insertions, deletions
14. On HEAD change, read `.git/HEAD` to determine current branch, diff against previous to populate previous_branch
15. Git commands MUST have a 2-second timeout to avoid blocking on network operations
16. Commit message truncated to 200 characters (privacy: no full commit bodies)

#### 1.4 File Metadata Enrichment (enhance existing `sources.FileSource`)

**What's missing:** File events have path and operation but no metadata about the file itself.

**Enriched payload:**
```json
{
  "op": "WRITE",
  "path": "/Users/alec/sigil/internal/store/store.go",
  "extension": ".go",
  "language": "go",
  "is_test": false,
  "is_config": false,
  "size_bytes": 14832
}
```

**Requirements:**
17. Add `extension`, `language` (derived from extension), `is_test`, `is_config` fields to every file event
18. Language mapping covers at minimum: go, python, typescript, javascript, rust, java, c, cpp, ruby, swift, kotlin, sql, yaml, toml, json, markdown, html, css
19. `is_test` true when filename matches `*_test.go`, `*.test.ts`, `*.spec.js`, `test_*.py`, etc.
20. `is_config` true when filename matches `*.toml`, `*.yaml`, `*.yml`, `*.json`, `*.env`, `Makefile`, `Dockerfile`, etc.
21. `size_bytes` read via `os.Stat` — MUST NOT block; skip on error
22. MUST NOT add latency to the file event pipeline; enrichment is best-effort

---

### Part 2: Browser Extension

A lightweight browser extension that sends page focus events to sigild. The user installs it from the Chrome Web Store / Firefox Add-ons / Safari Extensions.

#### 2.1 What the Extension Captures

- Page title (what's shown in the tab bar)
- Domain (e.g. `github.com`, `docs.google.com`, `stackoverflow.com`)
- Tab count (total open tabs in all windows)
- Active tab transitions (user switches tabs)

**What it does NOT capture:**
- Full URLs (privacy — domain only)
- Page content
- Form inputs
- Network requests
- Browsing history beyond the current session

#### 2.2 Event Schema

**Event kind:** `browser`

```json
{
  "action": "focus|blur|tab_count",
  "domain": "github.com",
  "page_title": "sigil-tech/sigil: Pull request #76",
  "tab_count": 23,
  "browser": "chrome"
}
```

**Requirements:**
23. Extension communicates with sigild via HTTP POST to `http://127.0.0.1:7775/ingest` (the existing plugin ingest endpoint)
24. Extension MUST send domain only, never full URL paths or query parameters
25. Tab count events fire at most once per minute
26. Focus events fire on tab switch, not on every page load
27. Extension MUST work offline (if sigild is not running, events are dropped silently)
28. Extension MUST have a visible indicator showing it's connected to Sigil
29. Domain allowlist/blocklist configurable in extension settings (e.g., exclude `mail.google.com`)
30. Extension source code MUST be open source and auditable

---

### Part 3: Calendar Integration

Read-only calendar access to understand meeting boundaries.

#### 3.1 macOS EventKit (daemon-native)

**Event kind:** `calendar`

```json
{
  "action": "meeting_start|meeting_end|free_block",
  "title": "Sprint Planning",
  "duration_minutes": 30,
  "attendee_count": 8,
  "is_recurring": true,
  "calendar_name": "Work"
}
```

**Requirements:**
31. Use macOS EventKit framework (requires Calendar permission prompt)
32. Poll calendar every 5 minutes for upcoming events
33. Emit `meeting_start` when a calendar event begins (±2 minute tolerance)
34. Emit `meeting_end` when a calendar event ends
35. Emit `free_block` when there's a gap of 30+ minutes between meetings
36. Capture title, duration, attendee count, and recurrence — NOT attendee names or email addresses
37. Filter to calendars the user selects (configurable via `[sources.calendar] calendars = ["Work"]`)

#### 3.2 Google Calendar API (plugin)

Same event schema as EventKit. Implemented as a Sigil plugin (`sigil-plugin-gcal`) that authenticates via OAuth and polls the Google Calendar API.

**Requirements:**
38. Plugin uses the existing plugin protocol (HTTP ingest to sigild)
39. OAuth tokens stored locally, never transmitted to Sigil's servers
40. Polling interval configurable (default: 5 minutes)

---

### Part 4: Plugin-Based Third-Party Sources

Each of these is a separate plugin binary, following the existing plugin pattern (`sigil-plugin-*`). They push events to sigild's ingest endpoint.

#### 4.1 Communication Plugins

**Slack (`sigil-plugin-slack`)**

Event kind: `communication`

```json
{
  "platform": "slack",
  "action": "message_sent|message_received|channel_switch|dnd_on|dnd_off",
  "channel_type": "dm|channel|thread",
  "message_count": 1,
  "is_response": true,
  "response_time_seconds": 45
}
```

**Requirements:**
41. Capture message send/receive counts — NOT message content
42. Track DND status transitions
43. Track channel switches (context switching signal)
44. `is_response` + `response_time_seconds` measures reactivity

**Microsoft Teams (`sigil-plugin-teams`)**

Same schema as Slack with `"platform": "teams"`.

#### 4.2 Video Call Plugins

**Zoom (`sigil-plugin-zoom`)**

Event kind: `meeting`

```json
{
  "platform": "zoom",
  "action": "join|leave|mute|unmute|screen_share_start|screen_share_end",
  "meeting_title": "Weekly Sync",
  "duration_minutes": 45,
  "participant_count": 12
}
```

**Requirements:**
45. Detect Zoom process start/stop for basic join/leave
46. Zoom SDK or API for mute/unmute, screen share, participant count (Pro feature)
47. Meeting title from Zoom API — NOT from screen scraping
48. Duration computed from join to leave timestamps

**Google Meet / FaceTime / WebEx**

Same event schema. Meet can be detected via browser extension (domain `meet.google.com`). FaceTime via process detection.

#### 4.3 Document Plugins

**Google Docs (`sigil-plugin-gdocs`)**

Event kind: `document`

```json
{
  "platform": "google_docs",
  "action": "open|edit|close",
  "document_title": "Q4 Product Roadmap",
  "document_type": "document|spreadsheet|presentation",
  "time_in_doc_seconds": 600
}
```

**Requirements:**
49. Use Google Drive API to detect recently modified documents
50. Capture document title and type — NOT content
51. `time_in_doc_seconds` estimated from browser focus events on `docs.google.com` domain

**Notion (`sigil-plugin-notion`)**

Same event schema with `"platform": "notion"`.

**Requirements:**
52. Use Notion API for page access/modification times
53. Capture page title only — NOT page content or blocks

---

### Part 5: New Event Kinds

Add to `internal/event/event.go`:

```go
KindIdle          Kind = "idle"          // idle/active transitions
KindTyping        Kind = "typing"        // typing velocity measurements
KindBrowser       Kind = "browser"       // browser tab focus/domain
KindCalendar      Kind = "calendar"      // meeting start/end/free blocks
KindCommunication Kind = "communication" // Slack/Teams message patterns
KindMeeting       Kind = "meeting"       // video call lifecycle
KindDocument      Kind = "document"      // document editing lifecycle
```

---

## Privacy Considerations

All new sources follow the existing privacy model:

1. **Everything stays local.** No new data leaves the machine unless the user opts into cloud inference or fleet reporting.
2. **Metadata, not content.** Browser captures domain + title, not URLs or page content. Slack captures message counts, not message text. Documents capture titles, not contents.
3. **Opt-in by default.** Typing velocity requires explicit `enabled = true`. Browser extension is a separate install. Calendar requires permission prompt. Plugins are separate installs.
4. **Kill switch works.** `sigilctl purge` and the Insights view purge ALL event kinds including the new ones.
5. **Retention applies.** The existing `raw_event_days` retention policy applies to all new event kinds equally.
6. **Domain blocklist.** Browser extension allows users to exclude domains they don't want tracked.

---

## Success Criteria

57. After implementing Part 1, sigild captures idle transitions, typing velocity, git commit messages/branches/diff stats, and file language/test/config classification — verified by `sigilctl events` showing populated payloads.
58. After implementing Part 2, the browser extension pushes domain + title events to sigild at tab-switch granularity — verified by `sigilctl events --kind browser` showing recent tab switches.
59. After implementing Part 3, sigild emits meeting_start/meeting_end events aligned with the user's actual calendar — verified against known meeting times.
60. After implementing Part 4, at least one communication plugin (Slack) and one video call plugin (Zoom) push events to sigild — verified by event counts in the store.
61. The ML pipeline (sigil-ml) can train a model that predicts "focus score" (0-100) for each hour based on the enriched signals — verified by prediction accuracy > 60% on held-out data.
62. Total RSS increase from Part 1 sources MUST be < 5MB.
63. Idle detection latency MUST be < 1 second (time between actual input resume and `idle:end` event).
64. File enrichment MUST NOT increase file event processing time by more than 1ms per event.
65. Browser extension MUST consume < 10MB memory in Chrome.

---

## Implementation Priority

| Priority | Part | Source | Native/Plugin | Effort |
|----------|------|--------|--------------|--------|
| P0 | 1.1 | Idle detection | Native | 2 days |
| P0 | 1.3 | Git enrichment | Native | 1 day |
| P0 | 1.4 | File metadata | Native | 0.5 days |
| P1 | 2 | Browser extension | Extension | 3-5 days |
| P1 | 3.1 | Calendar (EventKit) | Native | 2 days |
| P1 | 1.2 | Typing velocity | Native | 2 days |
| P2 | 4.1 | Slack plugin | Plugin | 2-3 days |
| P2 | 4.2 | Zoom plugin | Plugin | 2-3 days |
| P2 | 3.2 | Calendar (Google) | Plugin | 2-3 days |
| P3 | 4.3 | Google Docs plugin | Plugin | 2-3 days |
| P3 | 4.3 | Notion plugin | Plugin | 2-3 days |
| P3 | 4.1 | Teams plugin | Plugin | 2-3 days |

**Total: ~25-35 days across all priorities.**

P0 (idle + git + file enrichment) can ship in 3-4 days and immediately improves ML signal density by ~3x.
