package analyzer

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/wambozi/aether/internal/notifier"
	"github.com/wambozi/aether/internal/store"
)

// editTestWindow is the maximum elapsed time between a file edit and a
// subsequent test/build command for the pair to be counted toward the
// EditThenTest pattern.
const editTestWindow = 5 * time.Minute

// editTestThreshold is the minimum ratio of (edit→test pairs) / (total edits
// in a directory) required before a suggestion is emitted.
const editTestThreshold = 0.60

// buildFailStreakMin is the number of consecutive build/test failures required
// before a suggestion is emitted.
const buildFailStreakMin = 3

// contextSwitchHourlyLimit is the number of working-directory changes per hour
// above which a context-switching suggestion is emitted.
const contextSwitchHourlyLimit = 6

// Detector runs pure-Go heuristic pattern checks over the local event store
// and returns actionable suggestions.  It never calls the network.
type Detector struct {
	store *store.Store
	log   *slog.Logger
}

// NewDetector creates a Detector backed by the given store.
func NewDetector(s *store.Store, log *slog.Logger) *Detector {
	return &Detector{store: s, log: log}
}

// Detect runs all five pattern checks over the given time window and returns
// any suggestions that meet their confidence thresholds.  A partial failure in
// one check is logged and skipped; the remaining checks still run.
func (d *Detector) Detect(ctx context.Context, window time.Duration) ([]notifier.Suggestion, error) {
	since := time.Now().Add(-window)

	type checkFn func(context.Context, time.Time) ([]notifier.Suggestion, error)
	checks := []struct {
		name string
		fn   checkFn
	}{
		{"edit_then_test", d.checkEditThenTest},
		{"frequent_files", d.checkFrequentFiles},
		{"build_failure_streak", d.checkBuildFailureStreak},
		{"context_switch_frequency", d.checkContextSwitchFrequency},
		{"time_of_day", d.checkTimeOfDay},
	}

	var out []notifier.Suggestion
	for _, c := range checks {
		suggestions, err := c.fn(ctx, since)
		if err != nil {
			// Non-fatal: log and continue so one broken check doesn't silence
			// the rest.
			d.log.Warn("patterns: check failed", "check", c.name, "err", err)
			continue
		}
		out = append(out, suggestions...)
	}
	return out, nil
}

// checkEditThenTest detects directories where the user frequently edits a file
// and then runs a test or build command within editTestWindow.  A suggestion is
// emitted for each directory where that ratio exceeds editTestThreshold.
func (d *Detector) checkEditThenTest(ctx context.Context, since time.Time) ([]notifier.Suggestion, error) {
	fileEvents, err := d.store.QueryRecentFileEvents(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("patterns: edit_then_test: fetch file events: %w", err)
	}
	termEvents, err := d.store.QueryTerminalEvents(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("patterns: edit_then_test: fetch terminal events: %w", err)
	}
	if len(fileEvents) == 0 || len(termEvents) == 0 {
		return nil, nil
	}

	// editCount and followedCount are keyed by directory.
	editCount := make(map[string]int)
	followedCount := make(map[string]int)

	for _, fe := range fileEvents {
		dir := dirFromPayload(fe.Payload)
		if dir == "" {
			continue
		}
		editCount[dir]++

		// Scan forward through terminal events that fall within the window
		// after this file edit.
		deadline := fe.Timestamp.Add(editTestWindow)
		for _, te := range termEvents {
			if te.Timestamp.Before(fe.Timestamp) {
				continue
			}
			if te.Timestamp.After(deadline) {
				break
			}
			if isTestOrBuildCmd(cmdFromPayload(te.Payload)) {
				followedCount[dir]++
				break // count at most one test run per edit event
			}
		}
	}

	var out []notifier.Suggestion
	for dir, total := range editCount {
		if total == 0 {
			continue
		}
		ratio := float64(followedCount[dir]) / float64(total)
		if ratio < editTestThreshold {
			continue
		}
		out = append(out, notifier.Suggestion{
			Category:   "pattern",
			Confidence: ratio,
			Title:      "Edit-then-test pattern detected",
			Body: fmt.Sprintf(
				"You run tests after %.0f%% of edits in %s — consider a file-watch test runner.",
				ratio*100, dir,
			),
		})
	}
	return out, nil
}

// checkFrequentFiles surfaces files that appear in today's top-5 most-edited
// list but were absent from yesterday's top-5 — indicating an unusual focus
// shift.
//
// "Today" is the last 24 hours; "yesterday" is the 24-hour window before that.
// Both sets are derived from a single query (last 48h) partitioned in Go so
// the store only needs a single-sided time bound.
func (d *Detector) checkFrequentFiles(ctx context.Context, since time.Time) ([]notifier.Suggestion, error) {
	now := time.Now()
	todayStart := now.Add(-24 * time.Hour)
	yesterdayStart := todayStart.Add(-24 * time.Hour)

	// Fetch all file events for the last 48 hours and partition them into
	// today and yesterday buckets in Go — avoids needing an upper-bound on
	// the store query.
	allEvents, err := d.store.QueryRecentFileEvents(ctx, yesterdayStart)
	if err != nil {
		return nil, fmt.Errorf("patterns: frequent_files: fetch events: %w", err)
	}
	if len(allEvents) == 0 {
		return nil, nil
	}

	todayCounts := make(map[string]int64)
	yesterdayCounts := make(map[string]int64)
	for _, e := range allEvents {
		path, _ := e.Payload["path"].(string)
		if path == "" {
			continue
		}
		if !e.Timestamp.Before(todayStart) {
			todayCounts[path]++
		} else {
			yesterdayCounts[path]++
		}
	}

	todayTop := topN(todayCounts, 5)
	yesterdayTop := topN(yesterdayCounts, 5)

	if len(todayTop) == 0 {
		return nil, nil
	}

	yesterdaySet := make(map[string]struct{}, len(yesterdayTop))
	for _, f := range yesterdayTop {
		yesterdaySet[f.Path] = struct{}{}
	}

	var out []notifier.Suggestion
	for _, f := range todayTop {
		if _, seen := yesterdaySet[f.Path]; seen {
			continue
		}
		out = append(out, notifier.Suggestion{
			Category:   "pattern",
			Confidence: notifier.ConfidenceWeak,
			Title:      "Unusual file focus",
			Body: fmt.Sprintf(
				"You're spending more time in %s than usual (%d edits today, not in yesterday's top 5).",
				filepath.Base(f.Path), f.Count,
			),
		})
	}
	return out, nil
}

// topN returns the n file paths with the highest counts from the given map,
// sorted by count descending.
func topN(counts map[string]int64, n int) []store.FileEditCount {
	out := make([]store.FileEditCount, 0, len(counts))
	for path, count := range counts {
		out = append(out, store.FileEditCount{Path: path, Count: count})
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Count > out[j-1].Count; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// checkBuildFailureStreak detects three or more consecutive build or test
// command failures and suggests reviewing the error output.
func (d *Detector) checkBuildFailureStreak(ctx context.Context, since time.Time) ([]notifier.Suggestion, error) {
	termEvents, err := d.store.QueryTerminalEvents(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("patterns: build_failure_streak: %w", err)
	}

	streak := 0
	maxStreak := 0
	for _, te := range termEvents {
		cmd := cmdFromPayload(te.Payload)
		if !isTestOrBuildCmd(cmd) {
			continue
		}
		exitCode := exitCodeFromPayload(te.Payload)
		if exitCode != 0 {
			streak++
			if streak > maxStreak {
				maxStreak = streak
			}
		} else {
			streak = 0
		}
	}

	if maxStreak < buildFailStreakMin {
		return nil, nil
	}
	return []notifier.Suggestion{{
		Category:   "pattern",
		Confidence: notifier.ConfidenceModerate,
		Title:      fmt.Sprintf("%d consecutive build/test failures", maxStreak),
		Body:       "You've had multiple failures in a row — want a summary of the errors?",
	}}, nil
}

// checkContextSwitchFrequency counts working-directory changes per hour and
// emits a suggestion when the rate exceeds contextSwitchHourlyLimit.
func (d *Detector) checkContextSwitchFrequency(ctx context.Context, since time.Time) ([]notifier.Suggestion, error) {
	termEvents, err := d.store.QueryTerminalEvents(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("patterns: context_switch_frequency: %w", err)
	}
	if len(termEvents) == 0 {
		return nil, nil
	}

	// Bucket events into one-hour slots keyed by the hour boundary (Unix
	// seconds truncated to the hour).
	type hourKey int64
	hourOf := func(t time.Time) hourKey {
		return hourKey(t.Unix() / 3600)
	}

	// switchesPerHour counts directory transitions within each hour bucket.
	switchesPerHour := make(map[hourKey]int)
	prevCwd := ""
	prevHour := hourKey(0)

	for i, te := range termEvents {
		cwd := cwdFromPayload(te.Payload)
		h := hourOf(te.Timestamp)

		if i == 0 {
			prevCwd = cwd
			prevHour = h
			continue
		}
		if cwd != prevCwd && cwd != "" {
			switchesPerHour[prevHour]++
		}
		prevCwd = cwd
		prevHour = h
	}

	maxSwitches := 0
	for _, n := range switchesPerHour {
		if n > maxSwitches {
			maxSwitches = n
		}
	}

	if maxSwitches <= contextSwitchHourlyLimit {
		return nil, nil
	}
	return []notifier.Suggestion{{
		Category:   "pattern",
		Confidence: notifier.ConfidenceWeak,
		Title:      "High context-switching",
		Body: fmt.Sprintf(
			"High context-switching today — %d directory changes in a single hour.",
			maxSwitches,
		),
	}}, nil
}

// checkTimeOfDay identifies the hour of day with the most file edits over the
// window and surfaces it as a productive-hours insight for the daily digest.
func (d *Detector) checkTimeOfDay(ctx context.Context, since time.Time) ([]notifier.Suggestion, error) {
	fileEvents, err := d.store.QueryRecentFileEvents(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("patterns: time_of_day: %w", err)
	}
	if len(fileEvents) == 0 {
		return nil, nil
	}

	// editsByHour counts file edits per hour-of-day (0–23).
	editsByHour := make(map[int]int, 24)
	for _, fe := range fileEvents {
		editsByHour[fe.Timestamp.Hour()]++
	}

	peakHour := 0
	peakCount := 0
	for h, n := range editsByHour {
		if n > peakCount {
			peakCount = n
			peakHour = h
		}
	}

	// Only surface if there is a meaningful concentration of activity.
	if peakCount < 5 {
		return nil, nil
	}
	return []notifier.Suggestion{{
		Category:   "insight",
		Confidence: notifier.ConfidenceWeak,
		Title:      "Productive hour identified",
		Body: fmt.Sprintf(
			"Your most active coding hour is %02d:00–%02d:00 (%d file edits).",
			peakHour, peakHour+1, peakCount,
		),
	}}, nil
}

// --- Payload helpers -------------------------------------------------------
//
// Terminal events carry a JSON payload with keys "cmd", "exit_code", "cwd".
// File events carry a payload with key "path".
// These helpers centralise payload extraction so the pattern checks stay readable.

func dirFromPayload(payload map[string]any) string {
	path, _ := payload["path"].(string)
	if path == "" {
		return ""
	}
	return filepath.Dir(path)
}

func cmdFromPayload(payload map[string]any) string {
	cmd, _ := payload["cmd"].(string)
	return cmd
}

func exitCodeFromPayload(payload map[string]any) int {
	switch v := payload["exit_code"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	}
	return 0
}

func cwdFromPayload(payload map[string]any) string {
	cwd, _ := payload["cwd"].(string)
	return cwd
}

// isTestOrBuildCmd reports whether cmd looks like a test or build invocation.
// The list is intentionally conservative — false negatives are safer than
// false positives for streak detection.
func isTestOrBuildCmd(cmd string) bool {
	if cmd == "" {
		return false
	}
	prefixes := []string{
		"go test", "go build", "go vet",
		"make", "cargo test", "cargo build",
		"npm test", "npm run test", "npm run build",
		"pytest", "python -m pytest",
		"./gradlew", "mvn test", "mvn build",
	}
	lower := strings.ToLower(strings.TrimSpace(cmd))
	for _, p := range prefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}
