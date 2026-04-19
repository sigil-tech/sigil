# launcher_profile_round_trip.json — Fixture Source

## Status: SYNTHETIC (Phase 0b CI gate pending)

`launcher_profile_round_trip.json` is a **manually synthesised** fixture. It was
created by reading the Swift source of truth at
`sigil-launcher-macos/SigilLauncher/Models/LauncherProfile.swift` and populating
every field with a deterministic, non-empty value.

## Why synthetic?

Phase 0b of spec 028 (`sigil-launcher-macos` CI produces a LauncherProfile artefact
from a live Swift build) is **BLOCKED** on external Swift CI work in the
`sigil-launcher-macos` repository. Until Phase 0b ships:

- This fixture is the authoritative test input for `TestRoundTrip`.
- The fixture is maintained by hand whenever `LauncherProfile.swift` changes.

## Replacement path (Phase 0b)

When Phase 0b completes:

1. The `sigil-launcher-macos` CI job will produce `launcher_profile_round_trip.json`
   by instantiating a `LauncherProfile` with every optional field populated, encoding
   it with sorted keys, and uploading the result as a CI artefact.
2. This file will be replaced by the CI-produced artefact, pinned to a specific
   `sigil-launcher-macos` commit SHA via a `//go:generate` comment in `testdata.go`.
3. The `go:generate` comment will use `gh release download` or equivalent to fetch
   the pinned artefact and overwrite this file.
4. Delete this notice once the CI gate is wired and the synthetic file is gone.

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
