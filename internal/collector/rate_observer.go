package collector

// RateObserver is an optional side-channel for observing per-source event
// rate. The collector calls Observe(sourceID) for every event that passes
// through drain, where sourceID is resolved by the caller (typically via
// kenazproto.SourceIDForKind). Implementations MUST be non-blocking on the
// hot path — the collector's drain loop must not be gated by slow observers.
type RateObserver interface {
	Observe(sourceID string)
}

// noopRateObserver is the default when no observer is injected.
type noopRateObserver struct{}

func (noopRateObserver) Observe(string) {}
