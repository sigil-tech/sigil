package collector

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/sigil-tech/sigil/internal/event"
)

// mockInserter records every event passed to InsertEvent.
// If err is non-nil, every call returns that error instead of storing.
type mockInserter struct {
	mu     sync.Mutex
	events []event.Event
	err    error
}

func (m *mockInserter) InsertEvent(_ context.Context, e event.Event) error {
	if m.err != nil {
		return m.err
	}
	m.mu.Lock()
	m.events = append(m.events, e)
	m.mu.Unlock()
	return nil
}

// stored returns a snapshot of all recorded events.
func (m *mockInserter) stored() []event.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]event.Event, len(m.events))
	copy(out, m.events)
	return out
}

// mockSource is a Source whose event channel is supplied by the test.
type mockSource struct {
	name string
	ch   chan event.Event
	err  error
}

func (s *mockSource) Name() string { return s.name }

func (s *mockSource) Events(_ context.Context) (<-chan event.Event, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.ch, nil
}

// discardLogger returns a no-op structured logger suitable for tests.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// makeEvent builds a minimal event for use in tests.
func makeEvent(kind event.Kind, source string) event.Event {
	return event.Event{
		Kind:      kind,
		Source:    source,
		Payload:   map[string]any{"test": true},
		Timestamp: time.Now(),
	}
}

// TestNew verifies that New returns a Collector with a non-nil, 256-buffered
// Broadcast channel.
func TestNew(t *testing.T) {
	ins := &mockInserter{}
	c := New(ins, discardLogger())

	if c == nil {
		t.Fatal("New returned nil")
	}
	if c.Broadcast == nil {
		t.Fatal("Broadcast channel is nil")
	}

	// Channel must be buffered to 256 — fill it without blocking to confirm.
	for i := 0; i < 256; i++ {
		select {
		case c.Broadcast <- makeEvent(event.KindFile, "test"):
		default:
			t.Fatalf("Broadcast channel blocked at index %d; want capacity 256", i)
		}
	}
	if len(c.Broadcast) != 256 {
		t.Fatalf("len(Broadcast) = %d, want 256", len(c.Broadcast))
	}
}

// TestAdd verifies that Add appends sources in order, accessible on c.sources.
func TestAdd(t *testing.T) {
	ins := &mockInserter{}
	c := New(ins, discardLogger())

	src1 := &mockSource{name: "s1", ch: make(chan event.Event)}
	src2 := &mockSource{name: "s2", ch: make(chan event.Event)}
	src3 := &mockSource{name: "s3", ch: make(chan event.Event)}

	c.Add(src1)
	c.Add(src2)
	c.Add(src3)

	if len(c.sources) != 3 {
		t.Fatalf("len(sources) = %d, want 3", len(c.sources))
	}
	for i, want := range []string{"s1", "s2", "s3"} {
		if got := c.sources[i].Name(); got != want {
			t.Errorf("sources[%d].Name() = %q, want %q", i, got, want)
		}
	}
}

// TestStartAndDrain verifies the core happy path: events sent through a source
// channel are persisted to the store and broadcast on the Broadcast channel.
func TestStartAndDrain(t *testing.T) {
	ins := &mockInserter{}
	c := New(ins, discardLogger())

	ch := make(chan event.Event, 4)
	src := &mockSource{name: "files", ch: ch}
	c.Add(src)

	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	want := []event.Event{
		makeEvent(event.KindFile, "files"),
		makeEvent(event.KindGit, "files"),
	}
	for _, e := range want {
		ch <- e
	}
	close(ch)

	// Collect broadcast events, bounding the wait to avoid a hanging test.
	got := make([]event.Event, 0, len(want))
	timeout := time.After(2 * time.Second)
	for len(got) < len(want) {
		select {
		case e, ok := <-c.Broadcast:
			if !ok {
				t.Fatal("Broadcast closed before receiving all events")
			}
			got = append(got, e)
		case <-timeout:
			t.Fatalf("timed out: received %d broadcast events, want %d", len(got), len(want))
		}
	}

	c.Stop()

	if len(got) != len(want) {
		t.Errorf("broadcast count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Kind != want[i].Kind || got[i].Source != want[i].Source {
			t.Errorf("broadcast[%d] = {Kind:%s Source:%s}, want {Kind:%s Source:%s}",
				i, got[i].Kind, got[i].Source, want[i].Kind, want[i].Source)
		}
	}

	stored := ins.stored()
	if len(stored) != len(want) {
		t.Errorf("stored count = %d, want %d", len(stored), len(want))
	}
}

// TestDrain_storeError verifies that a store error causes the event to be
// dropped from the broadcast channel, but drain continues without panicking.
func TestDrain_storeError(t *testing.T) {
	ins := &mockInserter{err: errors.New("disk full")}
	c := New(ins, discardLogger())

	ch := make(chan event.Event, 4)
	src := &mockSource{name: "files", ch: ch}
	c.Add(src)

	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	ch <- makeEvent(event.KindFile, "files")
	close(ch)

	c.Stop()

	if n := len(ins.stored()); n != 0 {
		t.Errorf("stored %d events on error path, want 0", n)
	}
	// Failed events must not reach Broadcast.
	if n := len(c.Broadcast); n != 0 {
		t.Errorf("Broadcast has %d events after store error, want 0", n)
	}
}

// TestStop verifies that after Stop returns the Broadcast channel is closed:
// a receive yields the zero value with ok == false.
func TestStop(t *testing.T) {
	ins := &mockInserter{}
	c := New(ins, discardLogger())

	ch := make(chan event.Event)
	src := &mockSource{name: "proc", ch: ch}
	c.Add(src)

	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Close the source channel before Stop so drain exits cleanly.
	close(ch)
	c.Stop()

	select {
	case _, ok := <-c.Broadcast:
		if ok {
			t.Error("Broadcast channel still open after Stop; want closed")
		}
	default:
		t.Fatal("Broadcast channel not closed after Stop; receive would block")
	}
}

// TestStart_sourceError verifies that Start propagates an error from
// Source.Events to the caller.
func TestStart_sourceError(t *testing.T) {
	ins := &mockInserter{}
	c := New(ins, discardLogger())

	boom := errors.New("source unavailable")
	c.Add(&mockSource{name: "bad", err: boom})

	err := c.Start(context.Background())
	if err == nil {
		t.Fatal("Start returned nil, want error")
	}
	if !errors.Is(err, boom) {
		t.Errorf("Start error = %v, want to wrap %v", err, boom)
	}
}

// TestStart_sourceError_partialSources verifies that when a later source fails,
// Start still returns an error even though earlier sources succeeded.
func TestStart_sourceError_partialSources(t *testing.T) {
	ins := &mockInserter{}
	c := New(ins, discardLogger())

	c.Add(&mockSource{name: "good", ch: make(chan event.Event)})
	c.Add(&mockSource{name: "bad", err: errors.New("broken")})

	if err := c.Start(context.Background()); err == nil {
		t.Fatal("Start returned nil, want error from second source")
	}
}

// signalingInserter wraps mockInserter and closes a channel after the first
// successful InsertEvent call so tests can synchronise without polling.
type signalingInserter struct {
	mockInserter
	once    sync.Once
	stored1 chan struct{}
}

func newSignalingInserter() *signalingInserter {
	return &signalingInserter{stored1: make(chan struct{})}
}

func (s *signalingInserter) InsertEvent(ctx context.Context, e event.Event) error {
	err := s.mockInserter.InsertEvent(ctx, e)
	if err == nil {
		s.once.Do(func() { close(s.stored1) })
	}
	return err
}

// TestBroadcast_nonBlocking verifies that when the Broadcast channel is at
// capacity, drain does not block — it drops the event and moves on.
func TestBroadcast_nonBlocking(t *testing.T) {
	ins := newSignalingInserter()
	c := New(ins, discardLogger())

	// Pre-fill Broadcast to its full 256-event capacity.
	filler := makeEvent(event.KindFile, "filler")
	for i := 0; i < 256; i++ {
		c.Broadcast <- filler
	}

	ch := make(chan event.Event, 4)
	src := &mockSource{name: "files", ch: ch}
	c.Add(src)

	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	overflow := makeEvent(event.KindGit, "files")
	ch <- overflow

	// Wait until drain has stored the event before triggering Stop.
	// This eliminates the race between ctx.Done() and channel drain.
	select {
	case <-ins.stored1:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event to be stored")
	}

	close(ch)

	// Stop must return promptly. If drain blocks on Broadcast this will hang.
	done := make(chan struct{})
	go func() {
		c.Stop()
		close(done)
	}()

	select {
	case <-done:
		// passed — drain did not block
	case <-time.After(2 * time.Second):
		t.Fatal("Stop blocked; drain is not using a non-blocking broadcast send")
	}

	// The event was stored (store call succeeded) despite the broadcast drop.
	stored := ins.stored()
	if len(stored) != 1 {
		t.Fatalf("stored %d events, want 1", len(stored))
	}
	if stored[0].Kind != overflow.Kind {
		t.Errorf("stored event kind = %s, want %s", stored[0].Kind, overflow.Kind)
	}
}

// spyRateObserver records every (sourceID, count) pair passed to Observe.
type spyRateObserver struct {
	mu     sync.Mutex
	counts map[string]int
}

func newSpyRateObserver() *spyRateObserver {
	return &spyRateObserver{counts: make(map[string]int)}
}

func (s *spyRateObserver) Observe(sourceID string) {
	s.mu.Lock()
	s.counts[sourceID]++
	s.mu.Unlock()
}

func (s *spyRateObserver) total() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int, len(s.counts))
	for k, v := range s.counts {
		out[k] = v
	}
	return out
}

// countingInserter records all events and closes storedN after exactly n
// successful InsertEvent calls.
type countingInserter struct {
	mockInserter
	target  int
	once    sync.Once
	storedN chan struct{}
}

func newCountingInserter(n int) *countingInserter {
	return &countingInserter{target: n, storedN: make(chan struct{})}
}

func (c *countingInserter) InsertEvent(ctx context.Context, e event.Event) error {
	err := c.mockInserter.InsertEvent(ctx, e)
	if err == nil {
		c.mu.Lock()
		n := len(c.events)
		c.mu.Unlock()
		if n >= c.target {
			c.once.Do(func() { close(c.storedN) })
		}
	}
	return err
}

// TestCollector_RateObserverInjection verifies that:
//   - Observe is called once per successfully stored event for mapped kinds.
//   - Observe is NOT called for unmapped kinds (KindIdle, KindPointer, KindAI).
func TestCollector_RateObserverInjection(t *testing.T) {
	// Mapped kinds: KindFile → "filesystem", KindProcess → "process"
	mapped := []event.Event{
		makeEvent(event.KindFile, "src"),
		makeEvent(event.KindFile, "src"),
		makeEvent(event.KindProcess, "src"),
	}
	// Unmapped kinds: KindIdle, KindPointer, KindAI — must not trigger Observe.
	unmapped := []event.Event{
		makeEvent(event.KindIdle, "src"),
		makeEvent(event.KindPointer, "src"),
		makeEvent(event.KindAI, "src"),
	}
	all := append(mapped, unmapped...)

	// Wait for all 6 events to be stored before stopping, so drain processes
	// everything before ctx cancellation races with the channel drain.
	ins := newCountingInserter(len(all))
	spy := newSpyRateObserver()
	c := New(ins, discardLogger(), WithRateObserver(spy))

	ch := make(chan event.Event, len(all))
	c.Add(&mockSource{name: "src", ch: ch})

	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	for _, e := range all {
		ch <- e
	}

	// Wait for all events to be stored before stopping.
	select {
	case <-ins.storedN:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for all events to be stored")
	}

	close(ch)
	c.Stop()

	counts := spy.total()

	// Verify mapped events were observed.
	if got := counts["filesystem"]; got != 2 {
		t.Errorf("filesystem observe count = %d, want 2", got)
	}
	if got := counts["process"]; got != 1 {
		t.Errorf("process observe count = %d, want 1", got)
	}

	// Verify unmapped kinds produced zero observations.
	for _, unmappedID := range []string{"idle", "pointer", "ai"} {
		if n, ok := counts[unmappedID]; ok && n > 0 {
			t.Errorf("unexpected Observe call for sourceID %q (count=%d)", unmappedID, n)
		}
	}

	// Total observations must equal number of mapped events.
	total := 0
	for _, n := range counts {
		total += n
	}
	if total != len(mapped) {
		t.Errorf("total Observe calls = %d, want %d (mapped events only)", total, len(mapped))
	}
}

// TestCollector_RateObserverInjection_NilDefault verifies that New defaults to
// the noop observer when no WithRateObserver option is passed, and that drain
// does not panic on unmapped kinds.
func TestCollector_RateObserverInjection_NilDefault(t *testing.T) {
	ins := newCountingInserter(2)  // wait for both events to be stored
	c := New(ins, discardLogger()) // no option — must not panic

	ch := make(chan event.Event, 2)
	c.Add(&mockSource{name: "src", ch: ch})

	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	ch <- makeEvent(event.KindFile, "src")
	ch <- makeEvent(event.KindIdle, "src")

	select {
	case <-ins.storedN:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for events to be stored")
	}

	close(ch)
	c.Stop()
}

// TestDrain_contextCancellation verifies that drain exits cleanly when the
// parent context is cancelled, even when the source channel is never closed.
func TestDrain_contextCancellation(t *testing.T) {
	ins := &mockInserter{}
	c := New(ins, discardLogger())

	// Unbuffered, never-closed channel: drain must exit via ctx.Done().
	ch := make(chan event.Event)
	src := &mockSource{name: "proc", ch: ch}
	c.Add(src)

	ctx, cancel := context.WithCancel(context.Background())
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	cancel()

	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// drain exited as expected
	case <-time.After(2 * time.Second):
		t.Fatal("drain goroutine did not exit after context cancellation")
	}
}
