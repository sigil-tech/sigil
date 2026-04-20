package policy

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// LedgerEmitter is the narrow interface the policy package needs from
// the audit ledger. Declared here so the package does not import
// `ledger` directly — every consumer that already has a ledger
// emitter can fulfil this interface with an adapter in cmd/sigild.
type LedgerEmitter interface {
	EmitPolicyDeny(ctx context.Context, rule string, payload any) error
}

// Denier is the single entry point for deny-decision reporting.
// Every deny site across the daemon (actuator, merge, vm, future
// subsystems) holds a Denier and calls Deny on the deny path. The
// Denier handles the ledger emission; the caller does not need to
// know about the ledger.
//
// Deny MUST NOT return nil on a failed ledger emission — callers
// that observe an error MUST treat the deny decision as undone and
// either retry or surface the failure. In practice most call sites
// log at WARN and proceed (the local deny decision is already made)
// but a hard-gate caller (e.g., sandbox exec refusal) may want to
// fail-closed if the ledger is unavailable.
type Denier interface {
	Deny(ctx context.Context, rule string, action string, reason string) error
}

// denierImpl forwards deny decisions to the ledger. Construction is
// via New; a nil LedgerEmitter is an error at construction time so a
// caller cannot accidentally wire in a no-op deny path.
type denierImpl struct {
	emitter LedgerEmitter
	logger  *slog.Logger
	now     func() time.Time
}

// New returns a Denier that emits every deny to the supplied ledger
// emitter. Returns an error (rather than silently producing a
// no-op) when em is nil — policy decisions are security-relevant
// and the fail-open path is unacceptable.
func New(em LedgerEmitter, logger *slog.Logger) (Denier, error) {
	if em == nil {
		return nil, fmt.Errorf("policy.New: ledger emitter is required (nil provided)")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &denierImpl{
		emitter: em,
		logger:  logger,
		now:     func() time.Time { return time.Now().UTC() },
	}, nil
}

// Deny records a deny decision. Arguments:
//
//   - rule    short machine-readable name of the policy rule that
//     fired ("net.egress.public", "exec.sandbox.shell"). MUST be in
//     the centrally-maintained vocabulary; freeform strings are a
//     drift signal.
//   - action  the action the caller is refusing ("merge.row",
//     "vm.spawn", "sudo"). Narrow vocabulary.
//   - reason  short human-readable justification. Allowed to be
//     freeform but SHOULD be short enough to surface in the Audit
//     Viewer's row-level view without truncation (≤128 chars is
//     the soft target).
//
// The call logs at WARN if the ledger emission fails. Callers
// observe the error and decide whether to fail-closed (e.g., a
// sandbox exec refusal that cannot proceed without an audit trail)
// or fail-open (a best-effort warn on an incidental deny).
func (d *denierImpl) Deny(ctx context.Context, rule string, action string, reason string) error {
	if rule == "" {
		return fmt.Errorf("policy.Deny: rule is required")
	}
	if action == "" {
		return fmt.Errorf("policy.Deny: action is required")
	}
	payload := map[string]any{
		"rule":      rule,
		"action":    action,
		"reason":    reason,
		"denied_at": d.now().Format(time.RFC3339Nano),
	}
	if err := d.emitter.EmitPolicyDeny(ctx, rule, payload); err != nil {
		d.logger.Warn("policy.deny: ledger emission failed",
			"rule", rule, "action", action, "err", err)
		return fmt.Errorf("policy.Deny: emit: %w", err)
	}
	return nil
}

// SetClock swaps the time source. Kept unexported-adjacent —
// exported for tests only. Production callers MUST NOT override.
func SetClock(d Denier, now func() time.Time) {
	impl, ok := d.(*denierImpl)
	if !ok {
		return
	}
	impl.now = now
}
