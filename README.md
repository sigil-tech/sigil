# Sigil OS

[![Build](https://github.com/wambozi/sigil/actions/workflows/release.yml/badge.svg)](https://github.com/wambozi/sigil/actions/workflows/release.yml)
[![Tests](https://github.com/wambozi/sigil/actions/workflows/ci.yml/badge.svg)](https://github.com/wambozi/sigil/actions/workflows/ci.yml)

Sigil is a self-tuning intelligence layer for professional software engineers.
It runs as a lightweight background daemon that observes your workflow — file
edits, terminal commands, git activity, process signals, and window focus — builds
a local model of your patterns entirely on-device, and surfaces actionable
insights as desktop notifications the moment you need them. No cloud required;
no data leaves your machine unless you opt in.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/wambozi/sigil/main/scripts/install.sh | bash
```

## sigilctl command reference

| Command | Description |
|---------|-------------|
| `status` | Show daemon health, version, and current RSS |
| `events [-n N] [-offline]` | List the N most recent events (default 20) |
| `tail` | Poll and stream live events every 2s |
| `files` | Top files by edit count in the last 24h |
| `commands` | Command frequency table for the last 24h |
| `patterns` | Detected patterns with confidence scores |
| `suggestions` | Suggestion history with lifecycle status |
| `summary` | Trigger an immediate analysis cycle |
| `level` | Show current notification level |
| `level N` | Set notification level (0=silent … 4=autonomous) |
| `feedback <id> accept\|dismiss` | Respond to a suggestion by ID |
| `config` | Print resolved daemon configuration |
| `purge` | Delete all local data (confirmation required) |
| `export` | Export all data as newline-delimited JSON |

## Architecture

```
Sources → Collector → Store (SQLite WAL)
                  ↓
              Analyzer (timer) → Detector (15 heuristic checks)
                  ↓               ↓ optional cloud enrichment
              Notifier → notify-send / osascript
                  ↑
              Socket server ← sigilctl / shell
                  ↑
              Actuator registry (reversible actions)
```

**Event sources (6):** file system (fsnotify), process poll (/proc), git, terminal commands (shell hook), Hyprland window focus (IPC), AI interactions.

**Two-tier analysis:** 15 fast local heuristic checks run every cycle; optional cloud pass for deeper reasoning.

**Five notification levels:** 0 Silent → 1 Digest → 2 Ambient (default) → 3 Conversational → 4 Autonomous.

**Fleet (optional):** anonymized hourly metrics aggregation for team-level insights. Opt-out disables all telemetry.

## Privacy

All data is stored locally in `~/.local/share/sigild/data.db`. Nothing leaves
your machine unless you configure a remote Cactus endpoint.

See [PRIVACY.md](PRIVACY.md) for the full data inventory and opt-out instructions.
