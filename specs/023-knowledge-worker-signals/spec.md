# 023 — Knowledge Worker Signal Enrichment

**Status:** Draft (v2)
**Author:** Alec Feeman
**Date:** 2026-04-08

---

## Problem

Sigil currently captures signals optimized for software engineers: file edits, git activity, terminal commands, process lifecycle. Knowledge workers outside engineering — product managers, designers, analysts, executives — spend their day in browsers, calendars, documents, and communication tools. Sigil sees none of this.

Even for engineers, critical context is missing. The daemon knows "the user switched to Chrome" but not what they're reading. It knows "a commit happened" but not the commit message or diff stats. It can't distinguish a focused deep-work session from someone staring at a locked screen.

Without comprehensive signals, ML models can't learn meaningful patterns. This spec defines every daemon-native signal source we need to capture a complete picture of how a knowledge worker interacts with their system.

## Goals

1. **Capture every meaningful user-system interaction** at the metadata level — input patterns, system state, application lifecycle, environment context.
2. **All daemon-native** — no extensions, no plugins, no separate installs. Works out of the box for every user.
3. **Cross-platform** — macOS, Linux, Windows implementations for every source.
4. **Privacy-preserving** — metadata and rates only, never content. User can disable any source.
5. **ML-ready** — sufficient signal density for models to predict focus state, productivity patterns, context-switch cost, and optimal work schedules.

## Non-Goals

- Keylogging or screen recording
- Reading document content, email bodies, or message text
- Browser URL capture (domain only — handled by specs 024/025)
- Plugin-based third-party integrations (Slack, Zoom, etc. — separate specs)

---

## Design

### Part 1: Input Signals

How the user physically interacts with their machine.

#### 1.1 Idle Detection

**What:** Active/idle transitions and screen lock state.

**Event kind:** `idle`

```json
{"state": "idle_start", "idle_seconds": 0}
{"state": "idle_end", "idle_seconds": 342}
{"state": "screen_lock"}
{"state": "screen_unlock", "locked_seconds": 1800}
```

**Platform APIs:**
| Platform | API | Permission |
|----------|-----|------------|
| macOS | `CGEventSourceSecondsSinceLastEventType` | None |
| Linux | `XScreenSaverQueryInfo` | None |
| Windows | `GetLastInputInfo` | None |

**Requirements:**
1. Idle threshold configurable via `[sources.idle] threshold = "5m"` (default: 5m)
2. Screen lock detected via `CGSessionCopyCurrentDictionary` (macOS), `org.freedesktop.ScreenSaver` D-Bus (Linux), `WTSRegisterSessionNotification` (Windows)
3. Idle events MUST NOT fire during screen lock (separate events)
4. Poll interval: 5 seconds

#### 1.2 Typing Velocity

**What:** Keystrokes per minute aggregated in 30-second windows. Count only — never individual keys.

**Event kind:** `typing`

```json
{"keys_per_minute": 85, "window_seconds": 30, "active_app": "GoLand"}
```

**Platform APIs:**
| Platform | API | Permission |
|----------|-----|------------|
| macOS | `CGEventTapCreate` (kCGEventKeyDown) | Accessibility |
| Linux | `/dev/input/event*` or `libinput` | Input group |
| Windows | `SetWindowsHookEx(WH_KEYBOARD_LL)` | None |

**Requirements:**
5. Capture keystroke COUNT only — never key identifiers, sequences, or modifiers
6. Aggregation window: 30 seconds minimum
7. Opt-in: `[sources.typing] enabled = false` (default: disabled)
8. Include frontmost app name at time of measurement

#### 1.3 Mouse/Trackpad Activity

**What:** Input density metrics — click rate, scroll velocity, cursor movement distance. Detects reading (scroll), searching (rapid clicks), and idle-but-present (cursor wiggle).

**Event kind:** `pointer`

```json
{
  "window_seconds": 30,
  "clicks": 4,
  "scroll_distance": 2400,
  "movement_pixels": 850,
  "active_app": "Google Chrome",
  "gesture": "none"
}
```

**Platform APIs:**
| Platform | API | Permission |
|----------|-----|------------|
| macOS | `CGEventTapCreate` (mouse events) | Accessibility |
| Linux | `/dev/input/event*` | Input group |
| Windows | `SetWindowsHookEx(WH_MOUSE_LL)` | None |

**Requirements:**
9. Aggregate over 30-second windows (same as typing)
10. Track: click count, scroll distance (pixels), cursor movement distance (pixels)
11. macOS: detect trackpad gestures (swipe between desktops = context switch signal)
12. Opt-in: `[sources.pointer] enabled = false` (default: disabled)
13. Never capture click coordinates or targets — only aggregate counts and distances

#### 1.4 Desktop/Space Switches

**What:** Virtual desktop (Space) switches on macOS, workspace switches on Linux.

**Event kind:** `desktop`

```json
{"action": "switch", "desktop_index": 2, "desktop_count": 4}
```

**Platform APIs:**
| Platform | API | Permission |
|----------|-----|------------|
| macOS | `NSWorkspace.activeSpaceDidChangeNotification` | None |
| Linux/X11 | `_NET_CURRENT_DESKTOP` property change | None |
| Linux/Wayland | Compositor IPC (Hyprland, Sway) | None |
| Windows | `IVirtualDesktopManager` | None |

**Requirements:**
14. Emit on every desktop/space switch
15. Include index and total count
16. macOS: no special permissions needed (NSWorkspace notification)

---

### Part 2: System State Signals

The environment the user is working in.

#### 2.1 Display Configuration

**What:** External monitor connect/disconnect, display count changes. Proxy for "at desk" vs "mobile."

**Event kind:** `display`

```json
{"action": "connected", "display_count": 2, "primary_resolution": "2560x1440"}
{"action": "disconnected", "display_count": 1, "primary_resolution": "1440x900"}
```

**Platform APIs:**
| Platform | API | Permission |
|----------|-----|------------|
| macOS | `CGDisplayRegisterReconfigurationCallback` | None |
| Linux | `xrandr --listmonitors` or D-Bus | None |
| Windows | `WM_DISPLAYCHANGE` | None |

**Requirements:**
17. Emit on display connect/disconnect only (not continuous polling)
18. Include display count and primary resolution
19. Never capture display content or screenshots

#### 2.2 Audio State

**What:** Headphones connected/disconnected, microphone active. Proxy for focus mode (headphones) and calls (mic active).

**Event kind:** `audio`

```json
{"action": "headphones_connected", "output_device": "AirPods Pro"}
{"action": "headphones_disconnected"}
{"action": "mic_active", "app": "zoom.us"}
{"action": "mic_inactive"}
```

**Platform APIs:**
| Platform | API | Permission |
|----------|-----|------------|
| macOS | `AVAudioSession` / CoreAudio notifications | None |
| Linux | PulseAudio/PipeWire events | None |
| Windows | `IMMNotificationClient` | None |

**Requirements:**
20. Detect audio output device changes (headphones on/off)
21. Detect microphone activation (call in progress signal)
22. Include device name for output changes
23. Include process name using the microphone (if available)

#### 2.3 Power State

**What:** Plugged in vs battery, charge level. Proxy for mobile vs desk, and urgency context.

**Event kind:** `power`

```json
{"action": "ac_connected"}
{"action": "ac_disconnected", "battery_percent": 72}
{"action": "low_battery", "battery_percent": 15}
```

**Platform APIs:**
| Platform | API | Permission |
|----------|-----|------------|
| macOS | `IOPSNotificationCreateRunLoopSource` | None |
| Linux | `/sys/class/power_supply/` or UPower D-Bus | None |
| Windows | `SYSTEM_POWER_STATUS` | None |

**Requirements:**
24. Emit on AC connect/disconnect
25. Emit at 20% and 10% battery thresholds
26. Poll interval: 60 seconds (battery level doesn't change fast)

#### 2.4 Network State

**What:** Connection type changes. Proxy for work (ethernet/VPN) vs home (WiFi) vs travel (tethering).

**Event kind:** `network`

```json
{"action": "connected", "type": "wifi", "ssid_hash": "a3f8..."}
{"action": "vpn_connected", "vpn_name": "Corporate VPN"}
{"action": "disconnected"}
```

**Platform APIs:**
| Platform | API | Permission |
|----------|-----|------------|
| macOS | `NWPathMonitor` or SystemConfiguration | None |
| Linux | NetworkManager D-Bus | None |
| Windows | `NotifyIpInterfaceChange` | None |

**Requirements:**
27. Detect WiFi/ethernet/cellular transitions
28. Detect VPN connect/disconnect
29. Store SSID as a hash (privacy — can distinguish "home" vs "office" without storing the name)
30. VPN name is stored (not sensitive — it's a corporate tool name)

#### 2.5 Focus Mode / Do Not Disturb

**What:** OS-level Focus/DND state. Proxy for intentional deep work periods.

**Event kind:** `focus_mode`

```json
{"action": "enabled", "mode": "Do Not Disturb"}
{"action": "disabled"}
```

**Platform APIs:**
| Platform | API | Permission |
|----------|-----|------------|
| macOS | `DNDNotificationCenter` / defaults read | None |
| Linux | N/A (no OS-level DND) | N/A |
| Windows | Focus Assist via WMI | None |

**Requirements:**
31. Detect Focus mode enable/disable
32. Include mode name if available (macOS: "Work", "Personal", "Do Not Disturb")
33. Linux: skip (no standard OS-level DND mechanism)

---

### Part 3: Application Lifecycle

#### 3.1 App Launch/Quit

**What:** Full application lifecycle, not just focus changes. Know when apps start and stop.

**Event kind:** `app_lifecycle`

```json
{"action": "launch", "app": "Slack", "pid": 12345}
{"action": "quit", "app": "Slack", "pid": 12345, "duration_seconds": 28800}
{"action": "crash", "app": "Xcode", "pid": 54321}
```

**Platform APIs:**
| Platform | API | Permission |
|----------|-----|------------|
| macOS | `NSWorkspace.didLaunchApplicationNotification` / `didTerminateApplicationNotification` | None |
| Linux | Process polling (existing ProcessSource) | None |
| Windows | WMI `Win32_ProcessStartTrace` | None |

**Requirements:**
34. Emit on app launch and quit
35. Track duration (quit timestamp - launch timestamp)
36. Detect crashes (abnormal termination) separately from normal quit
37. Filter to GUI apps only — skip background daemons and system processes

#### 3.2 Screenshot Capture

**What:** Screenshot taken. Proxy for documentation, bug reporting, sharing.

**Event kind:** `screenshot`

```json
{"action": "captured", "type": "region"}
```

**Platform APIs:**
| Platform | API | Permission |
|----------|-----|------------|
| macOS | Watch `~/Desktop/Screenshot*` or `NSMetadataQuery` for screenshot files | None |
| Linux | Watch screenshot directory | None |
| Windows | Watch clipboard for image + Screenshot folder | None |

**Requirements:**
38. Detect when a screenshot is captured
39. Distinguish: full screen, region, window (if detectable)
40. Never capture or store the screenshot content

#### 3.3 Download Activity

**What:** Files appearing in the Downloads folder. Proxy for resource gathering.

**Event kind:** `download`

```json
{"action": "completed", "extension": ".pdf", "size_bytes": 2400000}
```

**Platform APIs:** Filesystem watcher on the Downloads directory (same as existing FileSource).

**Requirements:**
41. Watch the user's Downloads directory for new files
42. Capture file extension and size — NOT filename (privacy)
43. Debounce: one event per file, not per write chunk

---

### Part 4: Enrichment of Existing Sources

#### 4.1 Git Enrichment

Enhance existing `sources.GitSource` events.

On commit:
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

On branch change:
```json
{
  "git_kind": "head_change",
  "branch": "main",
  "previous_branch": "feat/desktop-polish"
}
```

**Requirements:**
44. On commit: read `git log -1` for message/hash, `git diff --stat HEAD~1` for stats
45. On HEAD change: read `.git/HEAD` for branch, diff against previous
46. Git commands timeout after 2 seconds
47. Commit message truncated to 200 characters

#### 4.2 File Metadata Enrichment

Enhance existing `sources.FileSource` events.

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
48. Add extension, language, is_test, is_config, size_bytes to every file event
49. Language mapping for 20+ languages
50. `is_test` / `is_config` from filename patterns
51. `size_bytes` via `os.Stat` — best-effort, skip on error

---

### Part 5: Calendar Integration

#### 5.1 macOS EventKit (daemon-native)

**Event kind:** `calendar`

```json
{
  "action": "meeting_start",
  "title": "Sprint Planning",
  "duration_minutes": 30,
  "attendee_count": 8,
  "is_recurring": true
}
```

**Requirements:**
52. Use EventKit framework (requires Calendar permission)
53. Poll every 5 minutes for upcoming events
54. Emit meeting_start/meeting_end/free_block events
55. Capture title, duration, attendee count, recurrence — NOT attendee names
56. Configurable calendar filter: `[sources.calendar] calendars = ["Work"]`

---

### Part 6: New Event Kinds

Add to `internal/event/event.go`:

```go
KindIdle        Kind = "idle"          // active/idle/lock transitions
KindTyping      Kind = "typing"        // keystroke rate (not keys)
KindPointer     Kind = "pointer"       // mouse/trackpad aggregate metrics
KindDesktop     Kind = "desktop"       // virtual desktop switches
KindDisplay     Kind = "display"       // monitor connect/disconnect
KindAudio       Kind = "audio"         // headphones/mic state
KindPower       Kind = "power"         // AC/battery transitions
KindNetwork     Kind = "network"       // connection type changes
KindFocusMode   Kind = "focus_mode"    // OS DND/Focus state
KindAppLifecycle Kind = "app_lifecycle" // app launch/quit/crash
KindScreenshot  Kind = "screenshot"    // screenshot captured
KindDownload    Kind = "download"      // file downloaded
KindCalendar    Kind = "calendar"      // meeting boundaries
KindBrowser     Kind = "browser"       // browser context (specs 024/025)
```

---

## Privacy Model

| Signal | What's captured | What's NOT captured |
|--------|----------------|-------------------|
| Typing | Keystrokes per minute (count) | Individual keys, sequences, content |
| Pointer | Click count, scroll distance, movement | Click targets, coordinates |
| Audio | Device name, mic active state | Audio content, call audio |
| Network | Connection type, SSID hash | SSID name, IP addresses, traffic |
| Download | File extension, size | Filename, content |
| Calendar | Title, duration, attendee count | Attendee names, meeting notes |
| Screenshot | Timestamp, capture type | Image content |
| Git | Commit message (truncated), stats | Full diff, file contents |

All sources:
- Default to **enabled** for non-intrusive signals (idle, display, power, network, app lifecycle)
- Default to **disabled** for input monitoring (typing, pointer)
- Respect `sigilctl purge` kill switch
- Respect `raw_event_days` retention policy
- Data never leaves the machine unless user opts into cloud

---

## Configuration

```toml
[sources.idle]
enabled = true          # default: true
threshold = "5m"        # time before idle_start fires

[sources.typing]
enabled = false         # default: false (requires Accessibility on macOS)

[sources.pointer]
enabled = false         # default: false (requires Accessibility on macOS)

[sources.desktop]
enabled = true          # default: true

[sources.display]
enabled = true          # default: true

[sources.audio]
enabled = true          # default: true

[sources.power]
enabled = true          # default: true

[sources.network]
enabled = true          # default: true
hash_ssid = true        # default: true (store hash instead of name)

[sources.focus_mode]
enabled = true          # default: true

[sources.app_lifecycle]
enabled = true          # default: true
gui_only = true         # default: true (skip background processes)

[sources.screenshot]
enabled = true          # default: true

[sources.download]
enabled = true          # default: true
watch_dir = "~/Downloads"

[sources.calendar]
enabled = false         # default: false (requires Calendar permission)
calendars = ["Work"]    # which calendars to read

[sources.browser]
enabled = true          # default: true
blocked_domains = []
poll_interval = "2s"
```

---

## Success Criteria

57. Idle detection fires within 1 second of input resume
58. Display connect/disconnect events match actual monitor changes
59. Power events match AC plug/unplug
60. Audio events match headphone connect/disconnect
61. App lifecycle captures launch/quit for GUI apps
62. Git enrichment populates commit message and branch for every commit event
63. File enrichment populates language and is_test for every file event
64. Total RSS increase from all Part 1-4 sources < 8MB
65. All sources configurable via `[sources.*]` TOML sections
66. `sigilctl purge` removes all new event kinds
67. ML focus-score prediction accuracy > 60% on 7-day training data with enriched signals

---

## Implementation Priority

| Priority | Source | What it unlocks for ML | Effort |
|----------|--------|----------------------|--------|
| P0 | Idle detection | Accurate time-on-task, break detection | 2 days |
| P0 | Git enrichment | Commit context, branch awareness | 1 day |
| P0 | File metadata | Language distribution, test frequency | 0.5 days |
| P0 | App lifecycle | App usage duration, daily patterns | 1 day |
| P1 | Display config | Location proxy (home/office/travel) | 0.5 days |
| P1 | Audio state | Focus proxy (headphones), call detection | 1 day |
| P1 | Power state | Location/mobility proxy | 0.5 days |
| P1 | Network state | Location/context proxy (VPN = work) | 0.5 days |
| P1 | Desktop switches | Context-switch frequency | 0.5 days |
| P1 | Focus mode | Intentional deep-work detection | 0.5 days |
| P1 | Calendar (EventKit) | Meeting load, free-block prediction | 2 days |
| P2 | Typing velocity | Flow state detection | 2 days |
| P2 | Pointer activity | Reading vs searching behavior | 1 day |
| P2 | Screenshot | Documentation/reporting activity | 0.5 days |
| P2 | Download activity | Resource gathering patterns | 0.5 days |
| **Total** | | | **~14 days** |

P0 (idle + git + file + app lifecycle) ships in 4-5 days and provides the foundation for all ML models.
