package collector

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/sigil-tech/sigil/internal/config"
)

const stopTimeout = 2 * time.Second

// sourceHandle owns the cancel function and done channel for one running source.
type sourceHandle struct {
	cancel context.CancelFunc
	done   <-chan struct{} // closed when the drain goroutine has exited
}

// Manager wraps a Collector and adds per-source cancel tracking so that
// individual sources can be started and stopped at runtime via Reload.
//
// Callers must call Start before Reload. Stop must be called exactly once after
// use to release all goroutines.
type Manager struct {
	col      *Collector
	log      *slog.Logger
	registry []Source                // all known sources (superset of running)
	running  map[string]sourceHandle // keyed by Source.Name()
	cfg      config.SourcesConfig

	mu sync.Mutex // guards registry, running, and cfg
}

// NewManager constructs a Manager around an existing Collector.
// Sources must be registered via AddSource before calling Start.
func NewManager(c *Collector, log *slog.Logger) *Manager {
	return &Manager{
		col:     c,
		log:     log,
		running: make(map[string]sourceHandle),
	}
}

// AddSource registers a source with the Manager. It must be called before
// Start. All registered sources are candidates for Reload to start or stop
// based on config.SourcesConfig flags.
func (m *Manager) AddSource(src Source) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.registry = append(m.registry, src)
}

// Collector returns the underlying Collector for consumers that need direct
// access (e.g. Subscribe, Broadcast).
func (m *Manager) Collector() *Collector {
	return m.col
}

// Start launches every registered source that is enabled in initCfg and starts
// the Collector's drain infrastructure. It should be called once at daemon
// boot. Sources registered but disabled in initCfg are kept in the registry so
// that Reload can start them later.
func (m *Manager) Start(ctx context.Context, initCfg config.SourcesConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cfg = initCfg

	desired := enabledSourceNames(initCfg)

	// Start the Collector's drain infrastructure. Sources added to the
	// Collector via col.Add before calling mgr.Start are treated as
	// always-on — they are started by the Collector's own Start call and
	// are not subject to Reload. Sources in m.registry (added via
	// mgr.AddSource) are managed exclusively by the Manager.
	if err := m.col.Start(ctx); err != nil {
		return fmt.Errorf("start collector: %w", err)
	}

	// Start only the enabled manager-managed sources.
	for _, src := range m.registry {
		if !desired[src.Name()] {
			continue
		}
		h, err := m.startSource(ctx, src)
		if err != nil {
			m.stopAllLocked()
			m.col.Stop()
			return fmt.Errorf("start source %q: %w", src.Name(), err)
		}
		m.running[src.Name()] = h
	}

	return nil
}

// Stop cancels all running sources, waits for their goroutines to exit, then
// stops the underlying Collector.
func (m *Manager) Stop() {
	m.mu.Lock()
	m.stopAllLocked()
	m.mu.Unlock()

	m.col.Stop()
}

// Reload computes the diff between the current running source set and the
// desired set described by newCfg. It stops removed sources (waiting up to 2 s
// each) and starts added sources.
//
// On any failure Reload attempts best-effort rollback to the pre-call running
// set. If rollback itself fails, both errors are joined and returned. Reload is
// safe for concurrent use.
func (m *Manager) Reload(ctx context.Context, newCfg config.SourcesConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Snapshot the current running handles for rollback.
	prev := make(map[string]sourceHandle, len(m.running))
	for k, v := range m.running {
		prev[k] = v
	}

	toAdd, toRemove := m.diffLocked(newCfg)

	var (
		started []string // names of sources started in this call
		stopped []string // names of sources stopped in this call
		errs    []error
	)

	// Stop removed sources first so that a start failure has fewer goroutines
	// to clean up.
	for _, name := range toRemove {
		h := m.running[name]
		if err := m.stopHandle(name, h); err != nil {
			errs = append(errs, fmt.Errorf("stop source %q: %w", name, err))
		} else {
			delete(m.running, name)
			stopped = append(stopped, name)
		}
	}

	// Start added sources.
	for _, src := range toAdd {
		h, err := m.startSource(ctx, src)
		if err != nil {
			errs = append(errs, fmt.Errorf("start source %q: %w", src.Name(), err))
		} else {
			m.running[src.Name()] = h
			started = append(started, src.Name())
		}
	}

	if len(errs) == 0 {
		m.cfg = newCfg
		return nil
	}

	primary := errors.Join(errs...)

	// Best-effort rollback: undo what was done in this call.
	// Use context.Background() so a cancelled caller context does not prevent
	// cleanup.
	rbCtx := context.Background()
	var rbErrs []error

	// Stop sources that were started in this failed call.
	for _, name := range started {
		if h, ok := m.running[name]; ok {
			if err := m.stopHandle(name, h); err != nil {
				rbErrs = append(rbErrs, fmt.Errorf("rollback stop %q: %w", name, err))
			} else {
				delete(m.running, name)
			}
		}
	}

	// Restart sources that were stopped in this failed call.
	for _, name := range stopped {
		src := m.findSourceLocked(name)
		if src == nil {
			rbErrs = append(rbErrs, fmt.Errorf("rollback start %q: source not in registry", name))
			continue
		}
		newH, err := m.startSource(rbCtx, src)
		if err != nil {
			rbErrs = append(rbErrs, fmt.Errorf("rollback start %q: %w", name, err))
		} else {
			m.running[name] = newH
		}
	}

	if len(rbErrs) > 0 {
		rbErr := errors.Join(rbErrs...)
		m.log.Error("collector.Manager reload rollback failed",
			"primary_err", primary,
			"rollback_err", rbErr,
		)
		return fmt.Errorf("reload failed (rollback also failed): %w", errors.Join(primary, rbErr))
	}

	return primary
}

// startSource starts a single source under a child context derived from ctx.
// It calls source.Events, then spawns a drain goroutine. The returned handle's
// done channel is closed when the goroutine exits.
func (m *Manager) startSource(ctx context.Context, src Source) (sourceHandle, error) {
	srcCtx, cancel := context.WithCancel(ctx)

	ch, err := src.Events(srcCtx)
	if err != nil {
		cancel()
		return sourceHandle{}, err
	}

	done := make(chan struct{})
	m.col.wg.Add(1)
	go func() {
		defer close(done)
		m.col.drain(srcCtx, src.Name(), ch)
	}()

	return sourceHandle{cancel: cancel, done: done}, nil
}

// stopHandle cancels a source and waits up to stopTimeout for its drain
// goroutine to exit. Returns an error if the goroutine does not exit in time.
func (m *Manager) stopHandle(name string, h sourceHandle) error {
	h.cancel()
	select {
	case <-h.done:
		return nil
	case <-time.After(stopTimeout):
		return fmt.Errorf("source %q drain goroutine did not exit within %s", name, stopTimeout)
	}
}

// stopAllLocked cancels and waits for every running source. Must be called with
// m.mu held.
func (m *Manager) stopAllLocked() {
	for name, h := range m.running {
		if err := m.stopHandle(name, h); err != nil {
			m.log.Error("source stop timeout", "source", name, "err", err)
		}
		delete(m.running, name)
	}
}

// diffLocked returns the sources to add and the names to remove given newCfg.
// Must be called with m.mu held.
func (m *Manager) diffLocked(newCfg config.SourcesConfig) (toAdd []Source, toRemove []string) {
	desired := enabledSourceNames(newCfg)

	// Running but not desired → remove.
	for name := range m.running {
		if !desired[name] {
			toRemove = append(toRemove, name)
		}
	}

	// Desired but not running → add (if in registry).
	for name, want := range desired {
		if !want {
			continue
		}
		if _, ok := m.running[name]; ok {
			continue // already running
		}
		if src := m.findSourceLocked(name); src != nil {
			toAdd = append(toAdd, src)
		}
	}

	return toAdd, toRemove
}

// findSourceLocked looks up a Source by name in the registry.
// Must be called with m.mu held.
func (m *Manager) findSourceLocked(name string) Source {
	for _, src := range m.registry {
		if src.Name() == name {
			return src
		}
	}
	return nil
}

// enabledSourceNames returns the set of source names that should be running
// given the config. Only the four Kenaz-managed sources are tracked here;
// sources with no config entry (e.g. terminal, hyprland) are always started
// outside of Manager's diff logic.
func enabledSourceNames(cfg config.SourcesConfig) map[string]bool {
	return map[string]bool{
		"files":     cfg.Files.IsEnabled(true),
		"git":       cfg.Git.IsEnabled(true),
		"clipboard": cfg.Clipboard.IsEnabled(true),
		"process":   cfg.Process.IsEnabled(true),
	}
}
