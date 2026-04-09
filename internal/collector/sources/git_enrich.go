package sources

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sigil-tech/sigil/internal/event"
)

// enrichGitEvent adds semantic metadata to commit and head_change events
// by shelling out to git. This is best-effort — if any git command fails
// or times out, the event is returned with whatever fields it already has.
func enrichGitEvent(e *event.Event) {
	gitKind, _ := e.Payload["git_kind"].(string)
	repoRoot, _ := e.Payload["repo_root"].(string)
	if repoRoot == "" {
		return
	}

	switch gitKind {
	case "commit":
		enrichCommit(e, repoRoot)
	case "head_change":
		enrichHeadChange(e, repoRoot)
	}
}

// enrichCommit reads the latest commit message, hash, and diff stats.
func enrichCommit(e *event.Event, repoRoot string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// git log -1 --format='%H|%s'
	out, err := gitCmd(ctx, repoRoot, "log", "-1", "--format=%H|%s")
	if err == nil {
		parts := strings.SplitN(strings.TrimSpace(out), "|", 2)
		if len(parts) == 2 {
			e.Payload["hash"] = parts[0][:minInt(12, len(parts[0]))]
			msg := parts[1]
			if len(msg) > 200 {
				msg = msg[:200]
			}
			e.Payload["message"] = msg
		}
	}

	// git diff --stat HEAD~1..HEAD
	out, err = gitCmd(ctx, repoRoot, "diff", "--stat", "--stat-width=1000", "HEAD~1..HEAD")
	if err == nil {
		parseGitDiffStat(e, out)
	}

	// Current branch
	branch := readBranch(repoRoot)
	if branch != "" {
		e.Payload["branch"] = branch
	}
}

// enrichHeadChange reads the current branch name from .git/HEAD.
func enrichHeadChange(e *event.Event, repoRoot string) {
	branch := readBranch(repoRoot)
	if branch != "" {
		e.Payload["branch"] = branch
	}
}

// readBranch reads .git/HEAD and extracts the branch name.
func readBranch(repoRoot string) string {
	data, err := os.ReadFile(filepath.Join(repoRoot, ".git", "HEAD"))
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(data))
	// "ref: refs/heads/main" → "main"
	if strings.HasPrefix(line, "ref: refs/heads/") {
		return line[len("ref: refs/heads/"):]
	}
	// Detached HEAD — return short hash.
	if len(line) >= 12 {
		return line[:12]
	}
	return line
}

// parseGitDiffStat extracts files_changed, insertions, deletions from
// the summary line of `git diff --stat`, e.g.:
// " 3 files changed, 42 insertions(+), 7 deletions(-)"
func parseGitDiffStat(e *event.Event, output string) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 {
		return
	}
	summary := lines[len(lines)-1]

	for _, seg := range strings.Split(summary, ",") {
		seg = strings.TrimSpace(seg)
		parts := strings.Fields(seg)
		if len(parts) < 2 {
			continue
		}
		n, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		switch {
		case strings.Contains(seg, "file"):
			e.Payload["files_changed"] = n
		case strings.Contains(seg, "insertion"):
			e.Payload["insertions"] = n
		case strings.Contains(seg, "deletion"):
			e.Payload["deletions"] = n
		}
	}
}

// gitCmd runs a git command in the given repo directory with a timeout.
func gitCmd(ctx context.Context, repoRoot string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
