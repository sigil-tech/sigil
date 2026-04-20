# Direct Dependencies

Per constitution §VIII ("Minimal Dependencies"), every direct Go module
added to `go.mod` needs a justification: what it provides, why a stdlib
or vendored alternative isn't acceptable, and who reviewed the decision.
This file is the audit trail. Indirect deps are not individually
justified — they ride on a direct dep that is.

Update this file whenever a new direct dependency is added or a
justification changes materially (major version bump, new feature being
relied on). CI does not yet enforce `go.mod <-> DEPENDENCIES.md` drift,
but spec-reviewer + security-privacy will flag missing entries.

## Runtime

### `filippo.io/age` — v1.3.1

- **Purpose**: Age-format symmetric encryption for the ledger signing
  key when the age-file fallback backend is in use
  (`internal/ledger/keystore/agefile.go`). Scrypt-KDF passphrase
  recipient wraps the ed25519 private key so plaintext never lands on
  disk (spec 029 §3.3).
- **Stdlib alternative**: None. Go's `crypto/aes` + a hand-rolled KDF
  would be a ~500 LOC crypto primitive reimplementation. Shelling out
  to an `age` CLI would add an install-time dependency + fork overhead.
- **Why this module**: `filippo.io/age` is the canonical pure-Go
  implementation of the age format, authored by Filippo Valsorda
  (Go cryptographer, ex-Google security team). Reviewed crypto code,
  minimal transitive deps (`filippo.io/hpke` + `golang.org/x/crypto`).
  No CGo.
- **Spec authority**: spec 029 §3.3 (plan), Task 2.4.
- **Review**: architect + security-privacy agents (spec 029 Phase 2 gate).

### `github.com/godbus/dbus/v5` — v5.2.2

- **Purpose**: DBus client for the freedesktop Secret Service backend
  (`internal/ledger/keystore/secretservice_linux.go`) — allows sigild
  to store its ledger signing key in gnome-keyring / kwallet5 /
  equivalent on Linux desktops. Also already in use transitively by
  the Wails tray and notify code (`cmd/sigil-app/{tray,notify}_linux.go`).
- **Stdlib alternative**: None. DBus is a wire protocol; reimplementing
  it is prohibitively complex.
- **Why this module**: Canonical pure-Go DBus client. `libsecret`
  (the C binding alternative) requires CGo, which the constitution
  §VIII rules out. Single additional direct dep — we were already
  pulling it in transitively.
- **Spec authority**: spec 029 §3.2 (plan), Task 2.3.
- **Review**: architect + security-privacy agents (spec 029 Phase 2 gate).

## Pre-existing (kept here for completeness, not newly justified)

The following entries predate the DEPENDENCIES.md convention. Each is
in scope for a one-time backfill review but is not a blocker for
spec 029; listing them here ensures future additions know the bar.

- `github.com/fsnotify/fsnotify` — file event source in `internal/collector/`.
- `github.com/google/uuid` — event IDs.
- `github.com/pelletier/go-toml/v2` — config loader.
- `github.com/stretchr/testify` — assertion helpers in tests.
- `github.com/wailsapp/wails/v2` — desktop app shell (`cmd/sigil-app`).
- `go.uber.org/goleak` — goroutine-leak detection in tests.
- `modernc.org/sqlite` — pure-Go SQLite, no CGo per constitution §VIII.
