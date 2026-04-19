// Package vmstats implements the per-session stat cache and fan-in goroutine
// for VM hypervisor metrics. It satisfies vm.SessionSampler, which is the
// interface declared by vm and consumed here.
//
// DAG position: imports vm for types only (vm.SessionID, vm.StatSnapshot).
// Must not import vmdriver, launcherprofile, store, or any package above vm
// in the DAG.
package vmstats

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sigil-tech/sigil/internal/vm"
)

// stalenessThreshold is the maximum age of a StatSnapshot before it is
// considered stale. Per spec 028 FR-011, stale CPU and MEM string fields
// receive a " ~" suffix when read.
const stalenessThreshold = 3 * time.Second

// entry holds the most-recent snapshot for a single session together with the
// timestamp at which the snapshot was recorded by the fan-in goroutine.
type entry struct {
	snap      vm.StatSnapshot
	updatedAt time.Time
}

// sessionState groups the mutable state for one attached session so that
// ownership can be compared by pointer rather than by function value.
type sessionState struct {
	cancel context.CancelFunc
}

// Sampler caches per-session StatSnapshot values received from the Driver
// subscribe channel. One goroutine per session consumes the channel; the
// results are stored in an in-memory map guarded by an RWMutex.
//
// Sampler implements vm.SessionSampler. The zero value is not useful; use
// NewSampler.
type Sampler struct {
	mu      sync.RWMutex
	entries map[vm.SessionID]*entry

	// sessions holds the active sessionState for each attached session goroutine.
	// DetachSession and the goroutine itself both manipulate this map, so it is
	// guarded by mu.
	sessions map[vm.SessionID]*sessionState
}

// NewSampler creates a ready-to-use Sampler.
func NewSampler() *Sampler {
	return &Sampler{
		entries:  make(map[vm.SessionID]*entry),
		sessions: make(map[vm.SessionID]*sessionState),
	}
}

// AttachSession starts consuming StatSnapshot values from ch and caching them
// under id. The goroutine exits when ch is closed (hypervisor exit per
// FR-009) or when ctx is cancelled. On channel close the entry is removed
// from the map so that any concurrent Read sees no data rather than stale
// data.
//
// AttachSession is safe to call concurrently. Calling AttachSession for an
// already-attached id detaches the previous goroutine before starting a new
// one.
func (s *Sampler) AttachSession(ctx context.Context, id vm.SessionID, ch <-chan vm.StatSnapshot) {
	s.mu.Lock()
	// Cancel any previous goroutine for this id.
	if prev, ok := s.sessions[id]; ok {
		prev.cancel()
	}
	sessCtx, cancel := context.WithCancel(ctx)
	st := &sessionState{cancel: cancel}
	s.sessions[id] = st
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			// Only clean up if this goroutine's state is still the current owner
			// (a racing AttachSession may have replaced it).
			if s.sessions[id] == st {
				delete(s.sessions, id)
				delete(s.entries, id)
			}
			s.mu.Unlock()
			cancel() // idempotent — ensures context is cancelled even if detached early
		}()

		for {
			select {
			case snap, ok := <-ch:
				if !ok {
					// Channel closed — hypervisor exited (FR-009 lifecycle
					// contract). Deferred cleanup removes the entry.
					return
				}
				s.mu.Lock()
				s.entries[id] = &entry{snap: snap, updatedAt: time.Now()}
				s.mu.Unlock()
			case <-sessCtx.Done():
				return
			}
		}
	}()
}

// DetachSession stops consuming for id and removes the cached snapshot.
// Safe to call when no session is attached (no-op).
func (s *Sampler) DetachSession(id vm.SessionID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.sessions[id]; ok {
		st.cancel()
		delete(s.sessions, id)
	}
	delete(s.entries, id)
}

// Read returns the most-recent StatSnapshot for id and whether one was found.
// The returned snapshot is the raw driver value; callers that want the
// staleness-annotated string form for display should use ReadFormatted.
func (s *Sampler) Read(id vm.SessionID) (vm.StatSnapshot, bool) {
	s.mu.RLock()
	e := s.entries[id]
	s.mu.RUnlock()
	if e == nil {
		return vm.StatSnapshot{}, false
	}
	return e.snap, true
}

// isStale reports whether the entry for id has not been updated within
// stalenessThreshold of now.
func (s *Sampler) isStale(id vm.SessionID) bool {
	s.mu.RLock()
	e := s.entries[id]
	s.mu.RUnlock()
	if e == nil {
		return true
	}
	return time.Since(e.updatedAt) > stalenessThreshold
}

// FormatCPU returns the CPU usage as a percentage string. If the snapshot for
// id is stale (older than 3 s per FR-011), the string carries a " ~" suffix.
// Returns "" if no snapshot exists.
func (s *Sampler) FormatCPU(id vm.SessionID) string {
	s.mu.RLock()
	e := s.entries[id]
	s.mu.RUnlock()
	if e == nil {
		return ""
	}
	v := formatPercent(e.snap.CPUPercent)
	if time.Since(e.updatedAt) > stalenessThreshold {
		v += " ~"
	}
	return v
}

// FormatMem returns the memory usage as a MiB string. If the snapshot for
// id is stale (older than 3 s per FR-011), the string carries a " ~" suffix.
// Returns "" if no snapshot exists.
func (s *Sampler) FormatMem(id vm.SessionID) string {
	s.mu.RLock()
	e := s.entries[id]
	s.mu.RUnlock()
	if e == nil {
		return ""
	}
	v := formatMB(e.snap.MemoryUsedMB)
	if time.Since(e.updatedAt) > stalenessThreshold {
		v += " ~"
	}
	return v
}

// formatPercent formats a CPU percentage as "N.N%".
func formatPercent(pct float64) string {
	// Use integer rounding for display; fractional CPU is noise at this level.
	return fmt.Sprintf("%.1f%%", pct)
}

// formatMB formats a memory value in MiB as "NMiB".
func formatMB(mb uint64) string {
	return fmt.Sprintf("%dMiB", mb)
}
