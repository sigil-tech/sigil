// Package policy centralises deny-decision reporting across the
// sigild daemon. Per spec 029 FR-006 every deny (actuator refusal,
// merge rejection, VM spawn refusal, egress block) MUST emit a
// `policy.deny` ledger row. The Denier interface is the single
// append path; every subsystem that previously handled deny
// semantics in-line now routes through Deny(ctx, rule, action,
// reason) — that makes the ledger side effect impossible to
// forget and the deny vocabulary enforceable via a CI grep gate.
//
// DAG position: imports `ledger` only (indirectly through the
// narrow LedgerEmitter interface declared in this package, so the
// policy package itself compiles without an import). Consumers
// (actuator, merge, vm) each get the Denier via dependency
// injection from cmd/sigild, which wires the real ledger.Emitter.
//
// The package is intentionally tiny — one interface, one concrete
// type, one helper. Anything more elaborate belongs in a future
// spec.
package policy
