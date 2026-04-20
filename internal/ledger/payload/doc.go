// Package payload declares the typed payload structs that ledger
// emitters hand to ledger.Emit. Spec 029 FR-032 forbids `any` /
// map[string]interface{} values anywhere in the ledger — every field
// carried into a `payload_json` cell MUST be a named, reviewed struct
// field. This package is the allowlist.
//
// Each sub-file (vm.go, merge.go, training.go, policy.go) defines the
// typed payloads for one subsystem's emitters. Their tests live in
// companion _test.go files and assert two properties:
//
//  1. The struct fields' JSON names form a stable allowlist — adding
//     or renaming a field requires a deliberate test update, so a
//     drift surfaces in code review.
//
//  2. No field name matches a denylist of secret-looking substrings
//     ("password", "token", "secret", "session" unless fingerprint-
//     adjacent, etc.). The cross-cutting check lives in
//     allowlist_test.go, which walks every exported struct in the
//     package via reflect.
//
// DAG position: `payload` sits directly below `ledger` — it imports
// nothing from ledger itself, so cycles are impossible. Every emitter
// imports `payload` and `ledger`; no one else depends on this
// package.
package payload
