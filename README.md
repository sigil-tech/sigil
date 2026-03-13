# Sigil

[![Build](https://github.com/wambozi/sigil/actions/workflows/release.yml/badge.svg)](https://github.com/wambozi/sigil/actions/workflows/release.yml)
[![Tests](https://github.com/wambozi/sigil/actions/workflows/ci.yml/badge.svg)](https://github.com/wambozi/sigil/actions/workflows/ci.yml)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

**A self-tuning intelligence layer for professional software engineers.**

Sigil runs as a lightweight background daemon that observes your workflow —
file edits, terminal commands, git activity, process signals, and window
focus — builds a local model of your patterns entirely on-device, and surfaces
actionable insights as desktop notifications the moment you need them.

No cloud required. No data leaves your machine unless you opt in.

> Sigil is part of [Sigil OS](https://sigilos.io), an AI-native Linux
> operating system for software engineers. The daemon works standalone on any
> Linux machine — you don't need Sigil OS to use it.

## Install

```bash
# One-line install
curl -fsSL https://raw.githubusercontent.com/wambozi/sigil/main/scripts/install.sh | bash

# Or build from source
git clone https://github.com/wambozi/sigil.git && cd sigil
make build    # produces ./sigild and ./sigilctl
```

## Quick Start

```bash
# Initialize — sets up config, shell hooks, and systemd service
sigild init

# Check daemon status
sigilctl status

# Watch live events as you work
sigilctl tail

# See what patterns the daemon has detected
sigilctl patterns

# View suggestions with confidence scores
sigilctl suggestions
```

## How It Works

Sigil's daemon (`sigild`) runs a continuous observe → analyze → suggest loop:

```
Sources → Collector → Store (SQLite WAL)
                  ↓
              Analyzer (timer) → Detector (15 heuristic checks)
                  ↓               ↓ optional cloud enrichment
              Notifier → notify-send / osascript
                  ↑
              Socket server ← sigilctl / Sigil Shell
                  ↑
              Actuator registry (reversible actions)
```

**6 event sources:** file system (fsnotify), process poll (/proc), git
activity, terminal commands (shell hook), Hyprland window focus (IPC), AI
interactions.

**15 pattern detectors** (pure Go heuristics — no LLM required): edit-then-test
correlation, frequent files, build failure streaks, context-switch frequency,
time-of-day productivity, stuck detection, dependency churn, idle gaps, and
more.

**5 notification levels:** Silent → Digest → Ambient (default) →
Conversational → Autonomous. The system earns trust through demonstrated
utility — suggestions only surface after the daemon has observed a pattern
multiple times.

**Local inference:** managed `llama-server` (llama.cpp) with 4 routing modes
(`local`, `localfirst`, `remotefirst`, `remote`). 80%+ of queries handled
on-device. Cloud frontier models (Anthropic, OpenAI) available as opt-in
fallback.

**Reversible actions:** the actuator system can auto-split panes on build,
pre-warm containers, and adjust keybindings — all with an undo window.

**Fleet (enterprise, optional):** anonymized hourly metrics aggregation for
team-level insights. Opt-in per engineer. See [PRIVACY.md](PRIVACY.md).

## sigilctl Command Reference

| Command | Description |
|---------|-------------|
| `status` | Daemon health, version, current RSS |
| `events [-n N] [-offline]` | List the N most recent events (default 20) |
| `tail` | Stream live events every 2s |
| `files` | Top files by edit count (last 24h) |
| `commands` | Command frequency table (last 24h) |
| `patterns` | Detected patterns with confidence scores |
| `suggestions` | Suggestion history with lifecycle status |
| `summary` | Trigger an immediate analysis cycle |
| `level [N]` | Show or set notification level (0–4) |
| `feedback <id> accept\|dismiss` | Respond to a suggestion |
| `config` | Print resolved daemon configuration |
| `purge` | Delete all local data (confirmation required) |
| `export` | Export all data as newline-delimited JSON |
| `ai-query <prompt>` | Send a query through the inference engine |
| `fleet-preview` | Preview what fleet reporting would send |
| `fleet-opt-in` | Enable fleet reporting |
| `fleet-opt-out` | Disable fleet reporting |
| `model list` | List cached inference models |
| `model pull [name]` | Download a model |

## Privacy

All data is stored locally in `~/.local/share/sigild/data.db`. Nothing leaves
your machine unless you explicitly configure a cloud inference endpoint and
opt in.

See [PRIVACY.md](PRIVACY.md) for the full data inventory, retention policy,
and opt-out instructions.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Apache 2.0 — see [LICENSE](LICENSE).
