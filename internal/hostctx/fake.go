package hostctx

import (
	"context"

	"github.com/sigil-tech/sigil/internal/store"
)

// FakeHostContextReader is a configurable test double for HostContextReader.
type FakeHostContextReader struct {
	Patterns []store.PatternSummary
	Task     *store.TaskRecord
	Err      error
}

// RecentPatterns returns the configured patterns or error.
func (f *FakeHostContextReader) RecentPatterns(_ context.Context, limit int) ([]store.PatternSummary, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	if limit <= 0 || limit > len(f.Patterns) {
		return f.Patterns, nil
	}
	return f.Patterns[:limit], nil
}

// ActiveSession returns the configured task or error.
func (f *FakeHostContextReader) ActiveSession(_ context.Context) (*store.TaskRecord, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	return f.Task, nil
}

// compile-time check
var _ HostContextReader = (*FakeHostContextReader)(nil)
