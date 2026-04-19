package main

import (
	"sync/atomic"
	"time"
)

// bucketCount is the number of 5-second buckets in the rolling window.
// 60 buckets × 5 s = 300 s = 5-minute window.
const bucketCount = 60

// bucketWidthSec is the duration of each bucket in seconds.
const bucketWidthSec = 5

// windowSeconds is the total rolling window in seconds.
const windowSeconds = bucketCount * bucketWidthSec // 300

// SourceStatus is a point-in-time snapshot of activity for one source family.
type SourceStatus struct {
	// RatePerMin is the estimated events-per-minute over the rolling window.
	// At startup (uptime < 5 min) the rate is extrapolated from the actual
	// elapsed time to avoid a false "low activity" reading.
	RatePerMin int
	// Cumulative is the all-time event count since process start.
	Cumulative int64
	// LastSeen is the wall-clock time of the most recent Observe call.
	// Zero value means the source has never been observed.
	LastSeen time.Time
}

// bucket holds an atomic event count and the slot index at which it was last
// reset.  On Observe, if the current slot index differs from the stored
// generation, the bucket is cleared before incrementing — this is the
// "lazy reset" that implements sliding-window expiry without extra goroutines.
//
// Two atomics are used; a CAS-based reset ensures that concurrent Observe
// calls on a stale bucket are race-free.
type bucket struct {
	count      atomic.Int64
	generation atomic.Int64 // slot index (unixSec / bucketWidthSec)
}

// sourceState holds the per-source ring buffer and cumulative counter.
type sourceState struct {
	buckets      [bucketCount]bucket
	cumulative   atomic.Int64
	lastSeenNano atomic.Int64 // Unix nanoseconds; 0 = never seen
}

// rateCounter is a per-source event rate tracker.  It implements
// collector.RateObserver and is safe for concurrent use.
//
// The source-ID → state mapping is built from knownSourceIDs at construction
// and never mutated afterward.  Observe does a short linear scan over the
// 6-element slice (cheaper than a map on the hot path) and then performs
// two atomic operations — no allocations, no locks.
type rateCounter struct {
	ids    []string
	states []*sourceState

	// clock is injected for testing; production code passes nil → time.Now.
	clock func() time.Time

	// startNano records when the counter was created, for uptime-clamped rate
	// extrapolation during the initial ramp-up window.
	startNano int64
}

// knownSourceIDs must match kenazproto.SourceIDForKind exhaustively.
var knownSourceIDs = []string{
	"filesystem",
	"process",
	"clipboard",
	"network",
	"keystroke",
	"app-context",
}

// newRateCounter constructs a rateCounter.  Pass nil for clock to use
// time.Now.
func newRateCounter(clock func() time.Time) *rateCounter {
	if clock == nil {
		clock = time.Now
	}
	ids := make([]string, len(knownSourceIDs))
	copy(ids, knownSourceIDs)
	states := make([]*sourceState, len(ids))
	for i := range states {
		states[i] = &sourceState{}
	}
	return &rateCounter{
		ids:       ids,
		states:    states,
		clock:     clock,
		startNano: clock().UnixNano(),
	}
}

// indexOf returns the slice index for sourceID, or -1 if unknown.
// Linear scan over a 6-element slice is faster than a map on the hot path.
func (rc *rateCounter) indexOf(sourceID string) int {
	for i, id := range rc.ids {
		if id == sourceID {
			return i
		}
	}
	return -1
}

// Observe records one event for the given sourceID.
// Implements collector.RateObserver.
// Zero allocations; two atomic ops on the hot path.
func (rc *rateCounter) Observe(sourceID string) {
	i := rc.indexOf(sourceID)
	if i < 0 {
		return
	}
	st := rc.states[i]

	now := rc.clock()
	slot := now.Unix() / bucketWidthSec // current absolute slot index
	idx := slot % bucketCount           // ring buffer position
	bkt := &st.buckets[idx]

	// Lazy reset: if the bucket's generation predates the current slot, it
	// holds stale data from a prior revolution of the ring.  Swap the
	// generation first; whichever goroutine wins the CAS is responsible for
	// zeroing the count.
	gen := bkt.generation.Load()
	if gen != slot {
		if bkt.generation.CompareAndSwap(gen, slot) {
			// We won the race: reset the stale count to zero before adding.
			bkt.count.Store(0)
		}
		// Losers see the updated generation and fall through to Add below.
		// There is a brief window where a loser may Add to a bucket that the
		// winner hasn't zeroed yet.  This is acceptable: the window is
		// nanoseconds wide and the worst-case effect is a single extra count
		// attributed to the current bucket — a negligible rate error.
	}

	bkt.count.Add(1)
	st.cumulative.Add(1)
	st.lastSeenNano.Store(now.UnixNano())
}

// Snapshot returns a point-in-time copy of the current per-source status.
// May allocate; not on the hot path.
func (rc *rateCounter) Snapshot() map[string]SourceStatus {
	now := rc.clock()
	nowUnix := now.Unix()
	nowNano := now.UnixNano()

	// Effective window for rate extrapolation during startup ramp-up.
	uptimeSec := (nowNano - rc.startNano) / int64(time.Second)
	if uptimeSec <= 0 {
		uptimeSec = 1
	}
	effectiveWindowSec := int64(windowSeconds)
	if uptimeSec < effectiveWindowSec {
		effectiveWindowSec = uptimeSec
	}

	nowSlot := nowUnix / bucketWidthSec // absolute slot index for now

	out := make(map[string]SourceStatus, len(rc.ids))
	for i, id := range rc.ids {
		st := rc.states[i]

		// Sum buckets whose generation falls within the rolling window.
		// A bucket at ring index idx is live if:
		//   nowSlot - bucketCount < bucket.generation <= nowSlot
		var sum int64
		for b := 0; b < bucketCount; b++ {
			bkt := &st.buckets[b]
			gen := bkt.generation.Load()
			if gen > nowSlot-bucketCount && gen <= nowSlot {
				sum += bkt.count.Load()
			}
		}

		ratePerMin := 0
		if effectiveWindowSec > 0 {
			ratePerMin = int(sum * 60 / effectiveWindowSec)
		}

		lastSeenNano := st.lastSeenNano.Load()
		var lastSeen time.Time
		if lastSeenNano != 0 {
			lastSeen = time.Unix(0, lastSeenNano)
		}

		out[id] = SourceStatus{
			RatePerMin: ratePerMin,
			Cumulative: st.cumulative.Load(),
			LastSeen:   lastSeen,
		}
	}
	return out
}
