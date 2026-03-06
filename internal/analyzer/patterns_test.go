package analyzer

import (
	"context"
	"testing"
	"time"

	"github.com/wambozi/aether/internal/event"
	"github.com/wambozi/aether/internal/notifier"
)

// insertFile inserts a file event with the given path and timestamp.
func insertFile(t *testing.T, ctx context.Context, db interface {
	InsertEvent(context.Context, event.Event) error
}, path string, ts time.Time) {
	t.Helper()
	if err := db.InsertEvent(ctx, event.Event{
		Kind:      event.KindFile,
		Source:    "test",
		Payload:   map[string]any{"path": path},
		Timestamp: ts,
	}); err != nil {
		t.Fatalf("insertFile %s: %v", path, err)
	}
}

// insertTerminal inserts a terminal event with the given command, exit code,
// working directory, and timestamp.
func insertTerminal(t *testing.T, ctx context.Context, db interface {
	InsertEvent(context.Context, event.Event) error
}, cmd string, exitCode int, cwd string, ts time.Time) {
	t.Helper()
	if err := db.InsertEvent(ctx, event.Event{
		Kind:   event.KindTerminal,
		Source: "test",
		Payload: map[string]any{
			"cmd":       cmd,
			"exit_code": float64(exitCode), // JSON numbers decode as float64
			"cwd":       cwd,
		},
		Timestamp: ts,
	}); err != nil {
		t.Fatalf("insertTerminal %q: %v", cmd, err)
	}
}

// hasSuggestionWithTitle returns true if any suggestion in ss has the given
// title, and reports the full suggestion list on failure.
func hasSuggestionWithTitle(t *testing.T, ss []notifier.Suggestion, title string) bool {
	t.Helper()
	for _, s := range ss {
		if s.Title == title {
			return true
		}
	}
	return false
}

// --- EditThenTest -----------------------------------------------------------

func TestDetector_EditThenTest_suggestionReturned(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()
	now := time.Now()

	// Insert 5 file edits in /home/user/project, each followed within
	// 2 minutes by a "go test" — ratio = 100 %, well above 60 % threshold.
	for i := range 5 {
		base := now.Add(-time.Duration(i+1) * 10 * time.Minute)
		insertFile(t, ctx, db, "/home/user/project/main.go", base)
		insertTerminal(t, ctx, db, "go test ./...", 0, "/home/user/project", base.Add(2*time.Minute))
	}

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.Detect(ctx, 2*time.Hour)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if !hasSuggestionWithTitle(t, suggestions, "Edit-then-test pattern detected") {
		t.Errorf("expected EditThenTest suggestion; got %+v", suggestions)
	}
}

func TestDetector_EditThenTest_belowThreshold_noSuggestion(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()
	now := time.Now()

	// 5 file edits, only 1 followed by a test (20 % ratio — below 60 %).
	for i := range 5 {
		base := now.Add(-time.Duration(i+1) * 10 * time.Minute)
		insertFile(t, ctx, db, "/home/user/project/main.go", base)
	}
	insertTerminal(t, ctx, db, "go test ./...", 0, "/home/user/project",
		now.Add(-9*time.Minute+30*time.Second))

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.Detect(ctx, 2*time.Hour)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if hasSuggestionWithTitle(t, suggestions, "Edit-then-test pattern detected") {
		t.Error("expected no EditThenTest suggestion below threshold")
	}
}

// --- BuildFailureStreak -----------------------------------------------------

func TestDetector_BuildFailureStreak_threeFailures_suggestionReturned(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()
	now := time.Now()

	// Three consecutive "go test" failures.
	for i := range 3 {
		insertTerminal(t, ctx, db, "go test ./...", 1, "/home/user/project",
			now.Add(-time.Duration(3-i)*5*time.Minute))
	}

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.Detect(ctx, time.Hour)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if !hasSuggestionWithTitle(t, suggestions, "3 consecutive build/test failures") {
		t.Errorf("expected build failure streak suggestion; got %+v", suggestions)
	}
}

func TestDetector_BuildFailureStreak_twoFailuresThenSuccess_noSuggestion(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()
	now := time.Now()

	// Two failures, then a success resets the streak.
	insertTerminal(t, ctx, db, "go test ./...", 1, "/proj", now.Add(-15*time.Minute))
	insertTerminal(t, ctx, db, "go test ./...", 1, "/proj", now.Add(-10*time.Minute))
	insertTerminal(t, ctx, db, "go test ./...", 0, "/proj", now.Add(-5*time.Minute))

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.Detect(ctx, time.Hour)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if hasSuggestionWithTitle(t, suggestions, "3 consecutive build/test failures") {
		t.Error("expected no streak suggestion after streak was broken by success")
	}
}

// --- Empty store ------------------------------------------------------------

func TestDetector_EmptyStore_noSuggestionsNoError(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.Detect(ctx, time.Hour)
	if err != nil {
		t.Fatalf("Detect on empty store: %v", err)
	}
	if len(suggestions) != 0 {
		t.Errorf("expected no suggestions from empty store; got %+v", suggestions)
	}
}

// --- ContextSwitchFrequency -------------------------------------------------

func TestDetector_ContextSwitchFrequency_aboveLimit_suggestionReturned(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()
	now := time.Now()

	// Generate 8 distinct directory changes within the same hour.
	dirs := []string{"/a", "/b", "/c", "/d", "/e", "/f", "/g", "/h", "/i"}
	for i, dir := range dirs {
		insertTerminal(t, ctx, db, "ls", 0, dir,
			now.Add(-time.Duration(len(dirs)-i)*5*time.Minute))
	}

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.Detect(ctx, 2*time.Hour)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if !hasSuggestionWithTitle(t, suggestions, "High context-switching") {
		t.Errorf("expected context-switch suggestion; got %+v", suggestions)
	}
}

// --- FrequentFiles ----------------------------------------------------------

func TestDetector_FrequentFiles_newFileInTopFive_suggestionReturned(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()
	now := time.Now()

	// Yesterday: top-5 are a.go through e.go.
	for _, name := range []string{"a.go", "b.go", "c.go", "d.go", "e.go"} {
		for i := range 3 {
			insertFile(t, ctx, db,
				"/proj/"+name,
				now.Add(-36*time.Hour+time.Duration(i)*time.Minute))
		}
	}

	// Today: handler.go rockets to the top with 10 edits (wasn't there yesterday).
	for i := range 10 {
		insertFile(t, ctx, db, "/proj/handler.go",
			now.Add(-time.Duration(i+1)*time.Minute))
	}

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.Detect(ctx, 48*time.Hour)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if !hasSuggestionWithTitle(t, suggestions, "Unusual file focus") {
		t.Errorf("expected unusual file focus suggestion; got %+v", suggestions)
	}
}

// --- TimeOfDay --------------------------------------------------------------

func TestDetector_TimeOfDay_peakHour_suggestionReturned(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()

	// Create a concentrated cluster of 10 file edits, all at the same hour
	// today, far enough in the past to remain within a 24-hour window.
	base := time.Now().Truncate(time.Hour).Add(-2 * time.Hour) // two hours ago, on the hour
	for i := range 10 {
		insertFile(t, ctx, db, "/proj/main.go",
			base.Add(time.Duration(i)*time.Minute))
	}

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.Detect(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if !hasSuggestionWithTitle(t, suggestions, "Productive hour identified") {
		t.Errorf("expected time-of-day suggestion; got %+v", suggestions)
	}
}

// --- isTestOrBuildCmd -------------------------------------------------------

func TestIsTestOrBuildCmd(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"go test ./...", true},
		{"go build .", true},
		{"go vet ./...", true},
		{"make all", true},
		{"cargo test", true},
		{"cargo build --release", true},
		{"npm test", true},
		{"npm run test", true},
		{"npm run build", true},
		{"pytest -v", true},
		{"python -m pytest tests/", true},
		{"./gradlew test", true},
		{"mvn test", true},
		{"git commit -m 'fix'", false},
		{"ls -la", false},
		{"echo hello", false},
		{"", false},
	}
	for _, tt := range tests {
		got := isTestOrBuildCmd(tt.cmd)
		if got != tt.want {
			t.Errorf("isTestOrBuildCmd(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}
