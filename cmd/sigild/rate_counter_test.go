package main

import (
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// advanceClock returns a clock whose time can be moved forward by the test.
func advanceClock(initial time.Time) (clock func() time.Time, advance func(time.Duration)) {
	var mu sync.Mutex
	cur := initial
	return func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			return cur
		}, func(d time.Duration) {
			mu.Lock()
			defer mu.Unlock()
			cur = cur.Add(d)
		}
}

// ---------------------------------------------------------------------------
// Window arithmetic tests
// ---------------------------------------------------------------------------

// TestRateCounter_WindowArithmetic emits N events within a single 5-second
// bucket and verifies that Snapshot reports the correct per-minute rate.
func TestRateCounter_WindowArithmetic(t *testing.T) {
	tests := []struct {
		name         string
		events       int
		wantRateExpr func(events int) int // expected RatePerMin
	}{
		{
			name:         "zero events",
			events:       0,
			wantRateExpr: func(n int) int { return 0 },
		},
		{
			name:         "60 events in first second -> 60/min at t=60s uptime",
			events:       60,
			wantRateExpr: func(n int) int { return 60 },
		},
		{
			name:         "120 events in first second -> 120/min at t=60s uptime",
			events:       120,
			wantRateExpr: func(n int) int { return 120 },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Start time aligned to a bucket boundary for predictable arithmetic.
			start := time.Unix(0, 0)
			clock, advance := advanceClock(start)
			rc := newRateCounter(clock)

			for i := 0; i < tt.events; i++ {
				rc.Observe("filesystem")
			}

			// Advance exactly 60 seconds so uptime == effectiveWindowSec == 60.
			advance(60 * time.Second)

			snap := rc.Snapshot()
			got := snap["filesystem"].RatePerMin
			want := tt.wantRateExpr(tt.events)
			if got != want {
				t.Errorf("RatePerMin = %d, want %d (events=%d)", got, want, tt.events)
			}
		})
	}
}

// TestRateCounter_ClampsToUptime verifies that at t=10s uptime the rate is
// extrapolated from the 10-second observed window (× 6 to get per-minute).
func TestRateCounter_ClampsToUptime(t *testing.T) {
	start := time.Unix(0, 0)
	clock, advance := advanceClock(start)
	rc := newRateCounter(clock)

	const count = 10
	for i := 0; i < count; i++ {
		rc.Observe("process")
	}

	// Advance to t=10s: effectiveWindowSec = 10, rate = count * 60 / 10 = 60.
	advance(10 * time.Second)

	snap := rc.Snapshot()
	got := snap["process"].RatePerMin
	want := count * 60 / 10 // 60

	if got != want {
		t.Errorf("RatePerMin at t=10s = %d, want %d", got, want)
	}
}

// TestRateCounter_DecaysOverTime verifies that events emitted more than
// 5 minutes ago do not contribute to the rate.
func TestRateCounter_DecaysOverTime(t *testing.T) {
	start := time.Unix(0, 0)
	clock, advance := advanceClock(start)
	rc := newRateCounter(clock)

	// Emit 100 events at t=0.
	for i := 0; i < 100; i++ {
		rc.Observe("clipboard")
	}

	// Advance beyond the 5-minute rolling window.
	advance(301 * time.Second)

	// Emit 5 fresh events in the new current bucket.
	for i := 0; i < 5; i++ {
		rc.Observe("clipboard")
	}

	snap := rc.Snapshot()

	// The 100 old events must not appear in the rate.
	// effectiveWindowSec = min(301, 300) = 300.
	// Only the 5 fresh events are in-window.
	wantRate := 5 * 60 / 300 // 1
	if got := snap["clipboard"].RatePerMin; got != wantRate {
		t.Errorf("RatePerMin after decay = %d, want %d", got, wantRate)
	}

	// Cumulative must include all 105 events.
	if got := snap["clipboard"].Cumulative; got != 105 {
		t.Errorf("Cumulative = %d, want 105", got)
	}
}

// TestRateCounter_Snapshot verifies multi-source concurrent Observe calls
// and that Snapshot returns correct per-source counts.
func TestRateCounter_Snapshot(t *testing.T) {
	const goroutines = 8
	const eventsPerGoroutine = 100

	start := time.Unix(0, 0)
	clock, advance := advanceClock(start)
	rc := newRateCounter(clock)

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < eventsPerGoroutine; j++ {
				rc.Observe("filesystem")
				rc.Observe("process")
			}
		}()
	}
	wg.Wait()

	advance(60 * time.Second)
	snap := rc.Snapshot()

	wantCumulative := int64(goroutines * eventsPerGoroutine)
	if got := snap["filesystem"].Cumulative; got != wantCumulative {
		t.Errorf("filesystem Cumulative = %d, want %d", got, wantCumulative)
	}
	if got := snap["process"].Cumulative; got != wantCumulative {
		t.Errorf("process Cumulative = %d, want %d", got, wantCumulative)
	}

	// Sources that received no events must have zero cumulative and zero rate.
	for _, id := range []string{"clipboard", "network", "keystroke", "app-context"} {
		if s := snap[id]; s.Cumulative != 0 || s.RatePerMin != 0 {
			t.Errorf("%s: want zero status, got %+v", id, s)
		}
	}
}

// TestRateCounter_LastSeen verifies that LastSeen reflects the most recent
// Observe call time and is zero for sources that have never been observed.
func TestRateCounter_LastSeen(t *testing.T) {
	start := time.Unix(1_000_000, 0)
	clock, advance := advanceClock(start)
	rc := newRateCounter(clock)

	if s := rc.Snapshot()["filesystem"].LastSeen; !s.IsZero() {
		t.Errorf("LastSeen before any Observe = %v, want zero", s)
	}

	rc.Observe("filesystem")
	observeTime := clock()

	advance(30 * time.Second)
	snap := rc.Snapshot()

	if got := snap["filesystem"].LastSeen; !got.Equal(observeTime) {
		t.Errorf("LastSeen = %v, want %v", got, observeTime)
	}
}

// ---------------------------------------------------------------------------
// Benchmarks
// ---------------------------------------------------------------------------

// BenchmarkRateCounter_Observe measures the hot-path cost of a single Observe
// call.  Target: ≤ 50 ns/op, 0 allocs/op.
func BenchmarkRateCounter_Observe(b *testing.B) {
	rc := newRateCounter(nil)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		rc.Observe("filesystem")
	}
}

// BenchmarkRateCounter_Observe_Contention measures throughput under
// concurrent access by 4 goroutines writing the same source, verifying that
// atomic-only synchronization avoids cache-line contention that would show
// as a dramatic per-goroutine throughput reduction.
func BenchmarkRateCounter_Observe_Contention(b *testing.B) {
	rc := newRateCounter(nil)
	const parallelism = 4

	b.SetParallelism(parallelism)
	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			rc.Observe("filesystem")
		}
	})
}
