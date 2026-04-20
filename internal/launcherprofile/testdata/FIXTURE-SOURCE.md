# launcher_profile_round_trip.json — Fixture Source

## Status: SYNTHETIC (CI job scaffolded, production pin pending)

`launcher_profile_round_trip.json` is currently a **manually synthesised** fixture.
It was created by reading the Swift source of truth at
`sigil-launcher-macos/SigilLauncher/Models/LauncherProfile.swift` and populating
every field with a deterministic, non-empty value.

## Phase 0b status

Phase 0b of spec 028 (`sigil-launcher-macos` CI produces a LauncherProfile artefact
from a live Swift build) is **scaffolded but not yet running in production**:

- The CI workflow lives at
  `sigil-launcher-macos/.github/workflows/launcher-profile-artefact.yml`.
- The Swift test helper that produces the artefact lives at
  `sigil-launcher-macos/Tests/LauncherProfileArtefactTests.swift`.
- Until a workflow run successfully publishes the artefact and the URL is
  pinned below, this synthetic fixture remains authoritative. Keep it
  hand-maintained whenever `LauncherProfile.swift` gains or loses a field.

## Replacement path (final switchover)

When a `sigil-launcher-macos` workflow has produced and published the real
artefact:

1. Record the commit SHA + download URL below.
2. Update `testdata.go`'s `go:generate` comment to fetch from that URL and
   verify a SHA-256 checksum sidecar.
3. Regenerate by running `go generate ./internal/launcherprofile/`; commit
   the updated bytes.
4. Delete this entire notice — the fixture is no longer synthetic.

### Artefact pin (fill in when available)

- `sigil-launcher-macos` commit SHA: _pending_
- Artefact URL: _pending_
- SHA-256 of pinned artefact: _pending_

## Field ordering

Fields are ordered **alphabetically by JSON key** to enable byte-exact round-trip
comparisons. When adding fields, insert them in alphabetical position.

## Field values (rationale)

| JSON key            | Value used                                          | Notes                    |
|---------------------|-----------------------------------------------------|--------------------------|
| `containerEngine`   | `"docker"`                                          | Swift default             |
| `cpuCount`          | `2`                                                 | Swift defaultProfile      |
| `diskImagePath`     | `"/home/testuser/.sigil/images/sigil-vm.img"`       | Deterministic test path  |
| `editor`            | `"vscode"`                                          | Swift default             |
| `initrdPath`        | `"/home/testuser/.sigil/images/initrd"`             | Deterministic test path  |
| `kernelCommandLine` | `"console=hvc0 root=/dev/vda rw"`                   | Swift defaultProfile      |
| `kernelPath`        | `"/home/testuser/.sigil/images/vmlinuz"`            | Deterministic test path  |
| `memorySize`        | `4294967296` (4 GiB)                                | Swift defaultProfile      |
| `modelId`           | `"llama-3.1-8b-instruct"`                           | All optionals populated  |
| `modelPath`         | `"/home/testuser/.sigil/models/llama-3.1-8b-instruct.gguf"` | All optionals populated |
| `notificationLevel` | `2` (ambient)                                       | Swift default             |
| `shell`             | `"zsh"`                                             | Swift default             |
| `sshPort`           | `2222`                                              | Swift defaultProfile      |
| `workspacePath`     | `"/home/testuser/workspace"`                        | Deterministic test path  |

## Tracking issue

Phase 0b tracking: see spec 028 tasks.md §Phase 0b tasks.
