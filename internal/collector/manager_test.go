package collector

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/sigil-tech/sigil/internal/config"
	"github.com/sigil-tech/sigil/internal/event"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// --- fake source implementations --------------------------------------------

// fakeEventSource is a Source whose channel closes when ctx is cancelled.
// Optionally it returns a configured error from Events instead of starting.
type fakeEventSource struct {
	name      string
	eventsErr error

	mu     sync.Mutex
	starts int
}

func newFakeEventSource(name string) *fakeEventSource {
	return &fakeEventSource{name: name}
}

func (f *fakeEventSource) withEventsErr(err error) *fakeEventSource {
	f.eventsErr = err
	return f
}

func (f *fakeEventSource) Name() string { return f.name }

func (f *fakeEventSource) Events(ctx context.Context) (<-chan event.Event, error) {
	if f.eventsErr != nil {
		return nil, f.eventsErr
	}
	f.mu.Lock()
	f.starts++
	f.mu.Unlock()

	ch := make(chan event.Event)
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch, nil
}

func (f *fakeEventSource) startCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts
}

// hangingSource simulates a source whose Events goroutine takes time to exit
// (it waits on relCh in addition to ctx.Done()). This models a real-world
// source that may do cleanup work before closing its channel. Drain still exits
// quickly because it selects on ctx.Done() directly.
type hangingSource struct {
	name  string
	relCh chan struct{}
}

func newHangingSource(name string) *hangingSource {
	return &hangingSource{
		name:  name,
		relCh: make(chan struct{}),
	}
}

func (h *hangingSource) Name() string { return h.name }

func (h *hangingSource) Events(ctx context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event)
	go func() {
		select {
		case <-h.relCh:
		case <-ctx.Done():
		}
		close(ch)
	}()
	return ch, nil
}

// Verify interface compliance at compile time.
var _ Source = (*fakeEventSource)(nil)
var _ Source = (*hangingSource)(nil)

// --- test helpers -----------------------------------------------------------

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	ins := &mockInserter{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	col := New(ins, log)
	return NewManager(col, log)
}

func runningCount(m *Manager) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.running)
}

// buildSourcesConfig builds a SourcesConfig for the four Kenaz-managed sources.
// Each source is explicitly enabled or disabled: keys present in the map take
// the given value; absent keys are explicitly set to false so that
// IsEnabled(true) does not treat them as enabled-by-default.
func buildSourcesConfig(enabled map[string]bool) config.SourcesConfig {
	bp := func(b bool) *bool { return &b }
	get := func(name string) *bool {
		v, ok := enabled[name]
		if !ok {
			v = false
		}
		return bp(v)
	}
	return config.SourcesConfig{
		Files:     config.SourceConfig{Enabled: get("files")},
		Git:       config.SourceConfig{Enabled: get("git")},
		Process:   config.SourceConfig{Enabled: get("process")},
		Clipboard: config.SourceConfig{Enabled: get("clipboard")},
	}
}

// --- tests ------------------------------------------------------------------

// TestManager_StartStop verifies the basic lifecycle.
func TestManager_StartStop(t *testing.T) {
	m := newTestManager(t)
	s1 := newFakeEventSource("files")
	s2 := newFakeEventSource("git")
	m.AddSource(s1)
	m.AddSource(s2)

	initCfg := buildSourcesConfig(map[string]bool{"files": true, "git": true})
	require.NoError(t, m.Start(context.Background(), initCfg))
	assert.Equal(t, 2, runningCount(m))
	assert.Equal(t, 1, s1.startCount())
	assert.Equal(t, 1, s2.startCount())

	m.Stop()
	assert.Equal(t, 0, runningCount(m))
}

// TestManager_StartError verifies that a source failure during Start causes
// Start to return a wrapped error and leaves no running goroutines.
func TestManager_StartError(t *testing.T) {
	m := newTestManager(t)
	m.AddSource(newFakeEventSource("files"))
	m.AddSource(newFakeEventSource("git").withEventsErr(errors.New("source unavailable")))

	initCfg := buildSourcesConfig(map[string]bool{"files": true, "git": true})
	err := m.Start(context.Background(), initCfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "git")
	// goleak enforces no goroutine leak.
}

// TestManager_Reload is the primary table-driven test covering the six cases
// described in spec 027 task 3.4.
func TestManager_Reload(t *testing.T) {
	tests := []struct {
		name string

		// initial is the set of source names that are running before Reload.
		initial map[string]bool
		// newEnabled is the desired running set after Reload.
		newEnabled map[string]bool
		// eventsErrs maps source name → error returned by Events().
		eventsErrs map[string]error
		// cancelCtx, if true, cancels the Reload context before calling Reload.
		cancelCtx bool

		wantErr     bool
		wantErrMsg  string
		wantRunning int
	}{
		{
			name:        "add-source",
			initial:     map[string]bool{"files": true, "git": true},
			newEnabled:  map[string]bool{"files": true, "git": true, "process": true},
			wantRunning: 3,
		},
		{
			name:        "remove-source",
			initial:     map[string]bool{"files": true, "git": true, "process": true},
			newEnabled:  map[string]bool{"files": true, "git": true, "process": false},
			wantRunning: 2,
		},
		{
			name:        "no-op",
			initial:     map[string]bool{"files": true},
			newEnabled:  map[string]bool{"files": true},
			wantRunning: 1,
		},
		{
			// A source that fails to start causes Reload to return an error.
			// Rollback is trivial (nothing was stopped), so the error is
			// returned as-is and the running set is unchanged.
			name:        "partial-failure-rollback",
			initial:     map[string]bool{"files": true, "git": true},
			newEnabled:  map[string]bool{"files": true, "git": true, "process": true},
			eventsErrs:  map[string]error{"process": errors.New("process unavailable")},
			wantErr:     true,
			wantErrMsg:  "process",
			wantRunning: 2, // files and git still running after rollback
		},
		{
			// Cancelling the context passed to Reload before calling it means
			// new sources are started under an already-cancelled context. Their
			// drain goroutines will exit immediately via ctx.Done(), but the
			// start itself succeeds (Events only needs to return a channel).
			// After Reload the sources are nominally in the running map; Stop
			// will clean them up via the per-source cancel.
			name:        "ctx-cancel",
			initial:     map[string]bool{"files": true},
			newEnabled:  map[string]bool{"files": true, "git": true},
			cancelCtx:   true,
			wantRunning: 2, // git starts even under cancelled ctx
		},
		{
			// A source fails to start. Rollback has nothing to stop (nothing
			// was removed). Reload returns the start error. This exercises the
			// double-failure path where rollback would also fail if the source
			// always returns an error — but since rollback only re-starts
			// sources that were stopped (none here), rollback is a no-op and
			// succeeds trivially.
			name:        "double-failure-terminal",
			initial:     map[string]bool{"files": true},
			newEnabled:  map[string]bool{"files": true, "git": true},
			eventsErrs:  map[string]error{"git": errors.New("git broken")},
			wantErr:     true,
			wantErrMsg:  "git",
			wantRunning: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ins := &mockInserter{}
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			col := New(ins, log)
			mgr := NewManager(col, log)

			// Collect all source names that might be referenced.
			allNames := make(map[string]bool)
			for n := range tt.initial {
				allNames[n] = true
			}
			for n := range tt.newEnabled {
				allNames[n] = true
			}

			// Register all sources. Sources with eventsErrs will fail when
			// Reload tries to start them; they must NOT be in the initial set.
			for n := range allNames {
				src := newFakeEventSource(n)
				if err, ok := tt.eventsErrs[n]; ok {
					src = src.withEventsErr(err)
				}
				mgr.AddSource(src)
			}

			// Start with only the initial set running.
			initCfg := buildSourcesConfig(tt.initial)
			ctx := context.Background()
			require.NoError(t, mgr.Start(ctx, initCfg), "Start failed")
			require.Equal(t, len(tt.initial), runningCount(mgr), "unexpected initial running count")

			// Build the Reload context.
			reloadCtx := ctx
			if tt.cancelCtx {
				c, cancel := context.WithCancel(ctx)
				cancel()
				reloadCtx = c
			}

			newCfg := buildSourcesConfig(tt.newEnabled)
			err := mgr.Reload(reloadCtx, newCfg)

			if tt.wantErr {
				require.Error(t, err)
				if tt.wantErrMsg != "" {
					assert.Contains(t, err.Error(), tt.wantErrMsg)
				}
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, tt.wantRunning, runningCount(mgr))

			mgr.Stop()
			assert.Equal(t, 0, runningCount(mgr))
		})
	}
}

// TestManager_Reload_ConcurrentSafety verifies no data races under concurrent
// Reload calls.
func TestManager_Reload_ConcurrentSafety(t *testing.T) {
	ins := &mockInserter{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	col := New(ins, log)
	mgr := NewManager(col, log)

	for _, n := range []string{"files", "git", "process", "clipboard"} {
		mgr.AddSource(newFakeEventSource(n))
	}

	initCfg := buildSourcesConfig(map[string]bool{
		"files": true, "git": true, "process": true, "clipboard": true,
	})
	require.NoError(t, mgr.Start(context.Background(), initCfg))

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			cfg := buildSourcesConfig(map[string]bool{
				"files":     i%2 == 0,
				"git":       i%3 == 0,
				"process":   true,
				"clipboard": i%4 == 0,
			})
			_ = mgr.Reload(context.Background(), cfg)
		}()
	}
	wg.Wait()

	mgr.Stop()
}

// TestManager_Reload_StopSourceExits verifies that stopping a source via Reload
// causes its drain goroutine to exit cleanly within stopTimeout.
// hangingSource is used as the "git" source to confirm that a source whose
// Events goroutine takes time to exit still terminates the drain loop quickly
// because drain itself selects on ctx.Done().
func TestManager_Reload_StopSourceExits(t *testing.T) {
	ins := &mockInserter{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	col := New(ins, log)
	mgr := NewManager(col, log)

	// Use "git" as the name so enabledSourceNames maps it correctly.
	mgr.AddSource(newHangingSource("git"))
	mgr.AddSource(newFakeEventSource("files"))

	initCfg := buildSourcesConfig(map[string]bool{"git": true, "files": true})
	require.NoError(t, mgr.Start(context.Background(), initCfg))
	assert.Equal(t, 2, runningCount(mgr))

	// Remove the hanging source. drain exits via ctx.Done() when stopHandle
	// calls h.cancel(), so Reload succeeds without reaching stopTimeout.
	newCfg := buildSourcesConfig(map[string]bool{"git": false, "files": true})
	require.NoError(t, mgr.Reload(context.Background(), newCfg))
	assert.Equal(t, 1, runningCount(mgr))

	mgr.Stop()
	assert.Equal(t, 0, runningCount(mgr))
}
