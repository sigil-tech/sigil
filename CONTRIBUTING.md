# Contributing to Sigil

Thanks for your interest in contributing to Sigil.

## Before You Start

1. **Read the constitution.** The project's non-negotiable principles live in
   [`.specify/memory/constitution.md`](.specify/memory/constitution.md). Every
   contribution must conform to them — privacy-first, interface-driven, DAG
   discipline, minimal dependencies.

2. **Open an issue first.** For anything beyond a typo fix, open an issue
   describing what you want to change and why. This saves everyone time if
   the change conflicts with the project's direction.

3. **One logical change per PR.** Don't bundle unrelated fixes. Each PR should
   be reviewable in isolation.

## Development Setup

```bash
git clone https://github.com/sigil-tech/sigil.git
cd sigil
make build      # build sigild + sigilctl
make check      # fmt + vet + test — must pass before submitting
make coverage   # verify coverage gate (currently 50%)
```

Requires Go 1.24+. No CGo — builds anywhere Go runs.

## Code Standards

- **Effective Go** is the style authority. Not a suggestion — the standard.
- Interfaces at package boundaries. Consumers depend on interfaces, not
  concrete types.
- Table-driven tests with `t.Run`. Mocks via `mockery`, never hand-written.
- Errors wrapped with context: `fmt.Errorf("operation: %w", err)`.
- No dead code. No duplication. No over-engineering.
- `make check` must pass. No exceptions.

See the full Go code standards in the
[constitution](.specify/memory/constitution.md#go-code-standards).

## Package DAG

New packages must fit the dependency graph without creating cycles:

```
event → config → store → inference → collector → notifier → analyzer → actuator → fleet → sync → socket → cmd/sigild
```

`event` is the leaf — zero internal imports. Violating the DAG is a
build-blocking defect.

## Commit Messages

```
feat: short description (closes #N)
fix: short description
refactor: short description
test: short description
docs: short description
```

## VM Sandbox dependencies

### Linux (QEMU driver — ADR-028b)

The VM Sandbox feature on Linux requires `qemu-system-x86_64` and `qemu-img`
on `$PATH`. Install via your distro's package manager:

```bash
# Ubuntu / Debian
sudo apt install qemu-system-x86 qemu-utils

# Arch Linux
sudo pacman -S qemu-base

# Fedora
sudo dnf install qemu-kvm qemu-img
```

KVM acceleration (`/dev/kvm`) is strongly recommended for acceptable
performance. Load the kernel module if needed:

```bash
sudo modprobe kvm_intel   # Intel
sudo modprobe kvm_amd     # AMD
```

Integration tests behind `//go:build integration` require `KVM_AVAILABLE=1`
in the environment and a `sigil-os` guest image pointed to by
`SIGIL_OS_IMAGE`.

### macOS (sigild-vz subprocess — ADR-028a — PENDING Phase 4b)

On macOS, the VM Sandbox requires the `sigild-vz` helper binary. This binary
is built from the `sigil-launcher-macos` repository as a Swift Package target
(`SigilVZ`). Until Phase 4b ships:

- The macOS driver is not implemented; sigild returns `ERR_HYPERVISOR_UNAVAILABLE`
  on Apple Silicon.
- When Phase 4b lands, `make fetch-vz-binary` will download the pinned release
  from the `sigil-launcher-macos` GitHub releases and verify its SHA-256
  checksum.
- `sigild-vz` must be co-located with the `sigild` binary (same directory) or
  pointed to by the `SIGILD_VZ_BINARY` environment variable.

## Daemon Metrics (`sigilctl metrics`)

`sigilctl metrics` calls the `metrics` Unix socket method and prints the full
JSON payload.  The following fields are emitted as of spec 028 Phase 6b:

| Field | Type | Description |
|---|---|---|
| `vm_sessions_active` | `int64` | Count of sessions in states `booting`, `ready`, `connecting`, or `stopping`. Queried live from the `sessions` table on every call. |
| `vm_merge_duration_seconds` | object | Per-outcome histogram.  Keys: `complete`, `partial`, `failed`, `already_complete`.  Each value is `{"count": N, "sum_seconds": F}`.  `sum_seconds / count` gives mean merge latency.  All four keys are always present even before any merge has run (zeroed). |
| `vm_events_per_sec` | object | Per-VM-ID event throughput.  Keys are VM UUIDs; values are `float64` events/second.  Stubbed as an empty object in Phase 6b — populated in Phase 7. |
| `topic_drops_total` | object | Cumulative count of events dropped due to full subscriber buffers, by topic.  Keys: `vm-events`, `observer-events`.  Increments when a 256-slot per-subscriber channel is full and the server cannot deliver without blocking. |

Example output:

```json
{
  "vm_sessions_active": 1,
  "vm_merge_duration_seconds": {
    "complete":          {"count": 3, "sum_seconds": 4.812},
    "partial":           {"count": 1, "sum_seconds": 2.104},
    "failed":            {"count": 0, "sum_seconds": 0},
    "already_complete":  {"count": 0, "sum_seconds": 0}
  },
  "vm_events_per_sec": {},
  "topic_drops_total": {
    "vm-events":       0,
    "observer-events": 0
  }
}
```

These counters are in-process only — they reset when `sigild` restarts.
No external metrics scraper or Prometheus endpoint is involved.

## License

By contributing, you agree that your contributions will be licensed under the
Apache License 2.0.
