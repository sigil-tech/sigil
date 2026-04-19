package main

// metrics.go holds the in-process metric accumulators for FR-022 VM metrics.
//
// These are not Prometheus-compatible histograms — they are simple in-memory
// accumulators whose values are serialised to the JSON map returned by the
// "metrics" socket method.  Each counter/gauge is safe for concurrent use.
//
// Four metrics (spec 028 Phase 6b):
//
//	vm_sessions_active{}                       — gauge; queried live from DB
//	vm_merge_duration_seconds{outcome}         — histogram per merge outcome
//	vm_events_per_sec{vm_id}                   — gauge; stubbed (TODO Phase 7)
//	topic_drops_total{topic}                   — counter; read from socket pkg

import (
	"sync"
	"sync/atomic"
)

// mergeDurationHistogram is a per-outcome duration accumulator.
// Each outcome stores the count of observations and the sum of durations in
// seconds.  These are sufficient for computing mean duration; a full histogram
// bucket array is out of scope for the current metrics surface.
type mergeDurationHistogram struct {
	mu      sync.Mutex
	entries map[string]*mergeDurationEntry
}

type mergeDurationEntry struct {
	count atomic.Int64
	// sumNano stores the cumulative nanoseconds as an int64 to allow atomic
	// reads without holding the mutex on the fast read path.
	sumNano atomic.Int64
}

// globalMergeDuration is the process-singleton merge duration accumulator.
// It is initialised with one entry per known merge outcome so that the metrics
// handler can emit zeroed entries even before any merge has run.
var globalMergeDuration = func() *mergeDurationHistogram {
	h := &mergeDurationHistogram{
		entries: make(map[string]*mergeDurationEntry),
	}
	for _, outcome := range []string{"complete", "partial", "failed", "already_complete"} {
		h.entries[outcome] = &mergeDurationEntry{}
	}
	return h
}()

// ObserveMergeDuration records a single merge duration observation.
// outcome must be one of the four known values; unknown outcomes are silently
// ignored to avoid unbounded key growth in long-running daemons.
func ObserveMergeDuration(outcome string, durationNano int64) {
	globalMergeDuration.mu.Lock()
	entry, ok := globalMergeDuration.entries[outcome]
	globalMergeDuration.mu.Unlock()
	if !ok {
		return
	}
	entry.count.Add(1)
	entry.sumNano.Add(durationNano)
}

// MergeDurationSnapshot returns a snapshot of merge duration metrics as a
// map[outcome]map[string]any with fields "count" and "sum_seconds".
func MergeDurationSnapshot() map[string]any {
	globalMergeDuration.mu.Lock()
	outcomes := make([]string, 0, len(globalMergeDuration.entries))
	for k := range globalMergeDuration.entries {
		outcomes = append(outcomes, k)
	}
	globalMergeDuration.mu.Unlock()

	out := make(map[string]any, len(outcomes))
	for _, outcome := range outcomes {
		globalMergeDuration.mu.Lock()
		entry := globalMergeDuration.entries[outcome]
		globalMergeDuration.mu.Unlock()

		count := entry.count.Load()
		sumNano := entry.sumNano.Load()
		out[outcome] = map[string]any{
			"count":       count,
			"sum_seconds": float64(sumNano) / 1e9,
		}
	}
	return out
}
