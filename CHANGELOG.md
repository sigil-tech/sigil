# Changelog

## [Unreleased]

### Added — spec 028: Kenaz VM Sandbox integration

- `VMStart` / `VMStop` / `VMStatus` / `VMList` / `VMMerge` socket handlers
  (spec 017 FR-003 full implementation).
- `vm-events` push topic with server-side `vm_id` filtering and
  close-after-sentinel predicate on `vm.session_terminal`.
- Linux hypervisor driver — direct `qemu-system-<arch>` subprocess with
  QMP lifecycle control (ADR-028b). QMP socket at `0600` permissions,
  unlinked on teardown.
- macOS hypervisor driver — extern Swift `sigild-vz` subprocess via
  stdin/stdout JSON-line pipe (ADR-028a). **PENDING Phase 4b Swift
  extraction** (`sigil-launcher-macos/Sources/SigilVZ/`).
- `launcherprofile` Go mirror package — reads `LauncherProfile` from
  `~/.sigil/launcher/settings.json` (macOS) or
  `$XDG_CONFIG_HOME/sigil-launcher/settings.json` (Linux); FR-013a
  round-trip property test in `internal/launcherprofile/`.
- `sessions.ledger_events_total` column (migration v5) — aggregate count
  of rows committed to `training_corpus` per session; written by
  `merge.Merge` in the transactional commit (Amendment B, spec 017;
  Amendment C, spec 019).
- `sessions.policy_status` column (migration v6) — static per-session
  policy verdict from `evaluatePolicyStatus` at VMStart time; one of
  `ok | pending | denied | not_applicable` (Amendment D, spec 017;
  ADR-028c).
- `vmstats` package — per-session stat cache and fan-in goroutine;
  feeds live metrics to the FR-022 counters.
- FR-022 metrics: `vm_sessions_active` gauge, `vm_merge_duration_seconds`
  histogram, `vm_events_per_sec` gauge, `topic_drops_total` counter.
- FR-019 privacy property test (4 pattern classes: raw paths, cmd args,
  env vars, clipboard content) — wired as CI gate.
- Protocol version bumped 2 → 3 (`kenazproto` package).
- `sigildSandboxAPI` Go backend wired in `kenaz-app`; `vmhost` package
  bridges socket client to Wails frontend.
- Integration test scaffolding: `test/integration/spec028_test.go`
  (`//go:build integration`), US1–US4 + SC-001–SC-014 stubs with
  `t.Skip` guards pending KVM/VZ CI runners.
- CI: `.github/workflows/spec-028-integration.yml` — matrix
  `{macos-14, ubuntu-24.04}`; jobs: `go-test-race`, `vitest-kenaz-frontend`,
  `sc-004-gate`, `integration-linux` (conditional KVM_AVAILABLE),
  `integration-macos` (scaffold only — Phase 4b BLOCKED).
- `Makefile`: `fetch-sigil-os-image` target — `SIGIL_OS_IMAGE_URL`-driven
  SHA-verified download of sigil-os QCOW2 for integration testing.
- Error codes `ERR_PROFILE_MISSING` and `ERR_HYPERVISOR_UNAVAILABLE` added
  to spec 017 Error Behavior Reference (Amendment A).

### Pending

- **Phase 4b**: Swift extraction of `VZVirtualMachine` wrapper into
  `sigild-vz` binary (`sigil-launcher-macos/Sources/SigilVZ/`) +
  `internal/vmdriver/vmdriver_darwin.go`. Blocks macOS VM boot.
- **Phase 10b**: macOS app bundle assembly + codesign + notarisation.
  Requires Apple Developer ID.
- **Phase 9 integration matrix**: requires macOS 14 Apple Silicon CI runner
  (`VZ_AVAILABLE`) and Ubuntu 24.04 KVM CI runner
  (`KVM_AVAILABLE` + sigil-os guest image). Scaffold is in place; execution
  blocked on runner availability.

---

## [0.1.0-beta] - 2026-03-22

### Added
- Core daemon (`sigild`) with background workflow observation
- CLI client (`sigilctl`) with 10+ commands
- 5 event sources: file, process, git, terminal, Hyprland
- SQLite store with WAL mode and 90-day retention
- 15 heuristic pattern detectors in the analyzer
- Hybrid inference engine (local llama.cpp + cloud Anthropic/OpenAI)
- 4 inference routing modes: local, localfirst, remotefirst, remote
- 5-level notification system (Silent -> Autonomous)
- Reversible action registry (actuator)
- Fleet telemetry aggregation (opt-in, anonymized)
- TCP+TLS network listener for remote shell connections
- Unix socket JSON API with 30+ methods
- VS Code extension for IDE suggestion toasts
- Plugin system with 5 plugins (Claude, GitHub, JetBrains, Jira, VS Code)
- Task tracker with phase detection
- ML prediction sidecar integration
- Shell hooks for Zsh and Bash

### Security
- All data stored locally, never sent without user review
- TLS with SPKI fingerprint pinning for network connections
- Credential management via `sigilctl credential` commands
