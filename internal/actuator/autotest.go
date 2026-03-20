package actuator

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sigil-tech/sigil/internal/event"
)

// AutoTestActuator watches file events and automatically runs the project's
// test command after a debounce period.  Only active at notification level 4
// (Autonomous/Autopilot).
type AutoTestActuator struct {
	log       *slog.Logger
	levelFn   func() int // returns current notification level
	notifyFn  func(Action)
	debounce  time.Duration
	mu        sync.Mutex
	timer     *time.Timer
	lastRunAt time.Time
	minRunGap time.Duration
}

// NewAutoTestActuator creates an AutoTestActuator.
func NewAutoTestActuator(log *slog.Logger, levelFn func() int, notifyFn func(Action)) *AutoTestActuator {
	return &AutoTestActuator{
		log:       log,
		levelFn:   levelFn,
		notifyFn:  notifyFn,
		debounce:  5 * time.Second,
		minRunGap: 30 * time.Second,
	}
}

// RunEventLoop reads file events and triggers debounced test runs at level 4.
func (a *AutoTestActuator) RunEventLoop(events <-chan event.Event) {
	for ev := range events {
		if ev.Kind != event.KindFile {
			continue
		}
		if a.levelFn() < 4 {
			continue
		}
		path, _ := ev.Payload["path"].(string)
		if path == "" {
			continue
		}
		repo := findRepoRoot(path)
		if repo == "" {
			continue
		}
		a.scheduleTestRun(repo)
	}
}

func (a *AutoTestActuator) scheduleTestRun(repo string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.timer != nil {
		a.timer.Stop()
	}
	a.timer = time.AfterFunc(a.debounce, func() {
		a.runTests(repo)
	})
}

func (a *AutoTestActuator) runTests(repo string) {
	a.mu.Lock()
	if time.Since(a.lastRunAt) < a.minRunGap {
		a.mu.Unlock()
		return
	}
	a.lastRunAt = time.Now()
	a.mu.Unlock()

	testCmd := detectTestCommand(repo)
	if testCmd == "" {
		return
	}

	a.log.Info("autopilot: running tests", "repo", filepath.Base(repo), "cmd", testCmd)

	action := Action{
		ID:          "auto-test-" + filepath.Base(repo),
		Description: "Autopilot: running " + testCmd,
		ExecuteCmd:  testCmd,
		ExpiresAt:   time.Now().Add(5 * time.Minute),
	}
	if a.notifyFn != nil {
		a.notifyFn(action)
	}

	parts := strings.Fields(testCmd)
	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		a.log.Warn("autopilot: tests failed", "repo", filepath.Base(repo), "err", err,
			"output_tail", truncateTail(string(out), 200))
	} else {
		a.log.Info("autopilot: tests passed", "repo", filepath.Base(repo))
	}
}

// detectTestCommand probes the repo root for common build files.
func detectTestCommand(repo string) string {
	has := func(name string) bool {
		_, err := os.Stat(filepath.Join(repo, name))
		return err == nil
	}
	switch {
	case has("go.mod"):
		return "go test ./..."
	case has("Cargo.toml"):
		return "cargo test"
	case has("package.json"):
		return "npm test"
	case has("pyproject.toml"), has("setup.py"):
		return "pytest"
	case has("Makefile"):
		return "make test"
	default:
		return ""
	}
}

// findRepoRoot walks up from path looking for .git.
func findRepoRoot(path string) string {
	dir := filepath.Dir(path)
	for {
		if info, err := os.Stat(filepath.Join(dir, ".git")); err == nil && info.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func truncateTail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}
