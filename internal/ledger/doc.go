// Package ledger implements the Sigil audit ledger — an append-only,
// hash-chained, ed25519-signed record of every privileged action the daemon
// performs. It is the load-bearing compliance surface that security officers,
// leadership, legal, and external auditors consult when something needs
// explaining (see spec 029, `specs/029-kenaz-audit-ledger/spec.md`).
//
// The ledger lives in the same SQLite store as the daemon's other state but
// is protected by three independent invariants that stack:
//
//   - Append-only. The `ledger` and `ledger_keys` tables are never targeted by
//     UPDATE or DELETE anywhere in the codebase except the dedicated
//     purge helper (spec 029 FR-002 / FR-013b). CI enforces this with a grep
//     gate; SQLite triggers enforce it at the storage layer as defense in
//     depth.
//
//   - Hash-chained. Every row's `hash` is the SHA-256 of the canonical
//     encoding of the row's immutable fields concatenated with the previous
//     row's `hash`. Tampering with any historical row invalidates every row
//     after it (spec 029 FR-011).
//
//   - Signed. Every row's `hash` is signed with the host's ed25519 private
//     key. An attacker who rewrites the chain from genesis still cannot
//     produce valid signatures without the private key (spec 029 FR-012).
//     Public keys are tracked append-only in `ledger_keys` so rotations do
//     not break historical verification (spec 029 FR-013a/b).
//
// The seven privileged-action event types in scope for the MVP are
// `vm.spawn`, `vm.teardown`, `policy.deny`, `policy.deny.vm_batch`,
// `merge.filter`, `model.merge`, and `training.tune`. Two ledger-internal
// sentinel types (`key.rotate`, `purge.invoked`) appear in the same `type`
// column but are emitted only by the rotation and purge subsystems
// respectively. Ordinary observation events (file change, terminal command,
// etc.) never enter the ledger — those live in the `events` table with
// different retention and privacy semantics.
//
// DAG position:
//
//	event → config → store → ledger → filter → merge → ... → socket → cmd/*
//
// `ledger` may import `event`, `config`, `store`, and the Go stdlib plus the
// vendored JCS (RFC 8785) canonicalizer and the two external dependencies
// justified in spec 029 plan §3 (`filippo.io/age`, `github.com/godbus/dbus/v5`).
// It is imported by every downstream emitter and by the socket layer. Any
// attempt to import `ledger` from `event`, `config`, or `store` is a
// build-blocking defect (circular dependency).
//
// The package exposes four primary interfaces:
//
//   - Emitter — single append path used by every privileged-action emitter
//     (`vm.spawn`, etc.) per FR-003.
//
//   - Reader — paginated reads for the socket list / get handlers and the
//     sigilctl CLI.
//
//   - Verifier — single-entry / range / full-chain chain-walking with
//     session cache per FR-015 / FR-016.
//
//   - Rotator — atomic key rotation per FR-013a (generate new keypair, emit
//     `key.rotate` sentinel signed by the outgoing key, mark old key retired).
//
// See the contracts directory under specs/029-kenaz-audit-ledger/contracts/
// for the exact wire shapes, hash input format, and Audit Viewer API
// contract.
package ledger
