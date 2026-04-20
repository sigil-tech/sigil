package vmstats

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sigil-tech/sigil/internal/vm"
)

// BenchmarkStatSampler measures Sampler steady-state cost with 3 concurrent
// driver streams, matching the production topology (sigild attaches one
// Sampler per VM session; typical concurrent sessions ≤ 3).
//
// Per spec 028 Task 9.6 the budget is ≤ 2% of one core on a Linux KVM runner
// sustaining 1 Hz per stream. At 1 Hz × 3 streams = 3 ops/sec, that yields a
// per-op budget of:
//
//	(0.02 * 1e9 ns/sec) / 3 ops/sec ≈ 6_666_666 ns/op (~6.67 ms)
//
// The /Push subtest measures the hot path (driver → fan-in goroutine → map
// store); any ns/op well below the 6.67 ms ceiling meets the budget with
// orders of magnitude of headroom.
//
// The /PushAndRead subtest adds concurrent Read + FormatCPU/FormatMem callers
// to simulate a UI polling at 10 Hz; it exists to catch accidental
// RWMutex-contention regressions but is not part of the 2%/core gate.
func BenchmarkStatSampler(b *testing.B) {
	b.Run("Push_3streams", benchmarkSamplerPush)
	b.Run("PushAndRead_3streams", benchmarkSamplerPushAndRead)
}

// benchmarkSamplerPush drives b.N pushes round-robin across three attached
// streams. The fan-in goroutines consume and update the entry map.
func benchmarkSamplerPush(b *testing.B) {
	const streams = 3

	s := NewSampler()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	chans := make([]chan vm.StatSnapshot, streams)
	for i := range chans {
		// Buffered generously so b.N loop iterations don't block waiting for
		// the consumer goroutine to be scheduled.
		chans[i] = make(chan vm.StatSnapshot, 64)
		id := vm.SessionID(fmt.Sprintf("bench-%d", i))
		s.AttachSession(ctx, id, chans[i])
	}

	snap := vm.StatSnapshot{
		Timestamp:         time.Now(),
		CPUPercent:        42.5,
		CPUCores:          4,
		MemoryUsedMB:      2048,
		MemoryAllocatedMB: 4096,
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		chans[i%streams] <- snap
	}

	b.StopTimer()

	for i := range chans {
		close(chans[i])
	}
	// Give fan-in goroutines a chance to observe close and clean up before
	// the next sub-benchmark runs.
	waitForDrain(s, streams)
}

// benchmarkSamplerPushAndRead pushes snapshots from one producer pool and
// reads / formats them from another, measuring combined RWMutex contention.
func benchmarkSamplerPushAndRead(b *testing.B) {
	const streams = 3

	s := NewSampler()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ids := make([]vm.SessionID, streams)
	chans := make([]chan vm.StatSnapshot, streams)
	for i := range chans {
		chans[i] = make(chan vm.StatSnapshot, 64)
		ids[i] = vm.SessionID(fmt.Sprintf("bench-%d", i))
		s.AttachSession(ctx, ids[i], chans[i])
	}

	// Seed each stream so the reader side has data to observe.
	snap := vm.StatSnapshot{
		Timestamp:         time.Now(),
		CPUPercent:        42.5,
		CPUCores:          4,
		MemoryUsedMB:      2048,
		MemoryAllocatedMB: 4096,
	}
	for i := range chans {
		chans[i] <- snap
	}

	// Concurrent reader loop — continues until the benchmark stops sending.
	readerCtx, readerCancel := context.WithCancel(context.Background())
	defer readerCancel()

	var readerWG sync.WaitGroup
	readerWG.Add(1)
	go func() {
		defer readerWG.Done()
		var i int
		for {
			select {
			case <-readerCtx.Done():
				return
			default:
			}
			id := ids[i%streams]
			_, _ = s.Read(id)
			_ = s.FormatCPU(id)
			_ = s.FormatMem(id)
			i++
		}
	}()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		chans[i%streams] <- snap
	}

	b.StopTimer()

	readerCancel()
	readerWG.Wait()

	for i := range chans {
		close(chans[i])
	}
	waitForDrain(s, streams)
}

// waitForDrain polls Sampler's internal session map until all fan-in
// goroutines have observed channel close and cleaned themselves up, bounded
// by a short timeout so a regression does not wedge the suite.
func waitForDrain(s *Sampler, expectedZero int) {
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		s.mu.RLock()
		n := len(s.sessions)
		s.mu.RUnlock()
		if n == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	_ = expectedZero
}
