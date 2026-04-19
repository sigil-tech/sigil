package vmstats

import (
	"context"
	"testing"
	"time"

	"github.com/sigil-tech/sigil/internal/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// makeSnap returns a StatSnapshot with the given CPU% and memory values.
func makeSnap(cpuPct float64, memMB uint64) vm.StatSnapshot {
	return vm.StatSnapshot{
		Timestamp:    time.Now(),
		CPUPercent:   cpuPct,
		CPUCores:     4,
		MemoryUsedMB: memMB,
	}
}

// TestSamplerAttachSession verifies that AttachSession starts consuming from
// the channel and that Read returns the most-recent snapshot.
func TestSamplerAttachSession(t *testing.T) {
	defer goleak.VerifyNone(t)

	s := NewSampler()
	id := vm.SessionID("sess-001")
	ch := make(chan vm.StatSnapshot, 4)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.AttachSession(ctx, id, ch)

	ch <- makeSnap(42.5, 512)
	time.Sleep(10 * time.Millisecond) // let goroutine drain

	snap, ok := s.Read(id)
	require.True(t, ok)
	assert.InDelta(t, 42.5, snap.CPUPercent, 0.01)
	assert.Equal(t, uint64(512), snap.MemoryUsedMB)

	// Cancel ctx to clean up goroutine before goleak check.
	cancel()
	time.Sleep(20 * time.Millisecond)
}

// TestSamplerGoroutineLifecycle exercises both exit paths:
//  1. Channel close (hypervisor exit — FR-009).
//  2. Context cancellation.
func TestSamplerGoroutineLifecycle(t *testing.T) {
	defer goleak.VerifyNone(t)

	// Path 1: channel close.
	t.Run("channel close", func(t *testing.T) {
		s := NewSampler()
		id := vm.SessionID("sess-ch-close")
		ch := make(chan vm.StatSnapshot, 1)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		s.AttachSession(ctx, id, ch)
		ch <- makeSnap(10.0, 128)
		time.Sleep(10 * time.Millisecond)

		// Closing the channel simulates hypervisor exit.
		close(ch)
		time.Sleep(20 * time.Millisecond)

		// Entry should be absent after channel close.
		_, ok := s.Read(id)
		assert.False(t, ok, "entry should be removed after channel close")
	})

	// Path 2: context cancellation.
	t.Run("ctx cancel", func(t *testing.T) {
		s := NewSampler()
		id := vm.SessionID("sess-ctx-cancel")
		ch := make(chan vm.StatSnapshot, 1)

		ctx, cancel := context.WithCancel(context.Background())
		s.AttachSession(ctx, id, ch)
		ch <- makeSnap(5.0, 64)
		time.Sleep(10 * time.Millisecond)

		cancel()
		time.Sleep(20 * time.Millisecond)

		// Entry should be absent after ctx cancellation.
		_, ok := s.Read(id)
		assert.False(t, ok, "entry should be removed after ctx cancellation")
	})
}

// TestSamplerRead verifies that Read returns (zero, false) for an unknown id
// and (snap, true) for a known id.
func TestSamplerRead(t *testing.T) {
	defer goleak.VerifyNone(t)

	s := NewSampler()

	// Unknown session.
	_, ok := s.Read("nonexistent")
	assert.False(t, ok)

	id := vm.SessionID("sess-read-001")
	ch := make(chan vm.StatSnapshot, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.AttachSession(ctx, id, ch)
	ch <- makeSnap(77.0, 1024)
	time.Sleep(15 * time.Millisecond)

	snap, ok := s.Read(id)
	require.True(t, ok)
	assert.InDelta(t, 77.0, snap.CPUPercent, 0.01)

	cancel()
	time.Sleep(20 * time.Millisecond)
}

// TestSamplerStaleness verifies the FR-011 staleness rule: FormatCPU and
// FormatMem append " ~" when the snapshot is older than 3 s.
func TestSamplerStaleness(t *testing.T) {
	// Inject an old entry directly to avoid a 3-second wall-clock wait.
	s := NewSampler()
	id := vm.SessionID("sess-stale")

	// Write a fresh entry — no stale marker.
	s.mu.Lock()
	s.entries[id] = &entry{
		snap:      makeSnap(50.0, 256),
		updatedAt: time.Now(),
	}
	s.mu.Unlock()

	cpu := s.FormatCPU(id)
	mem := s.FormatMem(id)
	assert.NotContains(t, cpu, "~", "fresh entry should not carry ~ suffix")
	assert.NotContains(t, mem, "~", "fresh entry should not carry ~ suffix")

	// Age the entry beyond the staleness threshold.
	s.mu.Lock()
	s.entries[id].updatedAt = time.Now().Add(-(stalenessThreshold + time.Second))
	s.mu.Unlock()

	staleCPU := s.FormatCPU(id)
	staleMem := s.FormatMem(id)
	assert.Contains(t, staleCPU, " ~", "stale CPU must carry ~ suffix per FR-011")
	assert.Contains(t, staleMem, " ~", "stale MEM must carry ~ suffix per FR-011")
}

// TestSamplerDetachSession verifies that DetachSession removes the entry and
// stops the goroutine.
func TestSamplerDetachSession(t *testing.T) {
	defer goleak.VerifyNone(t)

	s := NewSampler()
	id := vm.SessionID("sess-detach")
	ch := make(chan vm.StatSnapshot, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.AttachSession(ctx, id, ch)
	ch <- makeSnap(20.0, 200)
	time.Sleep(10 * time.Millisecond)

	s.DetachSession(id)
	time.Sleep(20 * time.Millisecond)

	_, ok := s.Read(id)
	assert.False(t, ok, "entry should be absent after DetachSession")
}

// TestSamplerAttachReplaces verifies that a second AttachSession for the same
// id replaces the first goroutine without leaking it.
func TestSamplerAttachReplaces(t *testing.T) {
	defer goleak.VerifyNone(t)

	s := NewSampler()
	id := vm.SessionID("sess-replace")

	ch1 := make(chan vm.StatSnapshot, 1)
	ch2 := make(chan vm.StatSnapshot, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.AttachSession(ctx, id, ch1)
	time.Sleep(5 * time.Millisecond)

	// Replace with a second channel.
	s.AttachSession(ctx, id, ch2)
	ch2 <- makeSnap(99.0, 4096)
	time.Sleep(15 * time.Millisecond)

	snap, ok := s.Read(id)
	require.True(t, ok)
	assert.InDelta(t, 99.0, snap.CPUPercent, 0.01)

	cancel()
	time.Sleep(20 * time.Millisecond)
}
