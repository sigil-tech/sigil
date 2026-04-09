package sources

import (
	"testing"

	"github.com/sigil-tech/sigil/internal/event"
)

func TestParseGitDiffStat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		output     string
		wantFiles  int
		wantInsert int
		wantDelete int
	}{
		{
			name:       "typical_stat",
			output:     " 3 files changed, 42 insertions(+), 7 deletions(-)\n",
			wantFiles:  3,
			wantInsert: 42,
			wantDelete: 7,
		},
		{
			name:       "insertions_only",
			output:     " 1 file changed, 10 insertions(+)\n",
			wantFiles:  1,
			wantInsert: 10,
		},
		{
			name:       "deletions_only",
			output:     " 2 files changed, 5 deletions(-)\n",
			wantFiles:  2,
			wantDelete: 5,
		},
		{
			name:   "empty",
			output: "",
		},
		{
			name:       "with_file_list",
			output:     " foo.go | 10 ++++\n bar.go | 3 ---\n 2 files changed, 10 insertions(+), 3 deletions(-)\n",
			wantFiles:  2,
			wantInsert: 10,
			wantDelete: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := &event.Event{Payload: map[string]any{}}
			parseGitDiffStat(e, tt.output)

			if got, _ := e.Payload["files_changed"].(int); got != tt.wantFiles {
				t.Errorf("files_changed = %d, want %d", got, tt.wantFiles)
			}
			if got, _ := e.Payload["insertions"].(int); got != tt.wantInsert {
				t.Errorf("insertions = %d, want %d", got, tt.wantInsert)
			}
			if got, _ := e.Payload["deletions"].(int); got != tt.wantDelete {
				t.Errorf("deletions = %d, want %d", got, tt.wantDelete)
			}
		})
	}
}

func TestReadBranch(t *testing.T) {
	t.Parallel()

	// Use the actual repo we're in.
	branch := readBranch("../../..")
	if branch == "" {
		t.Skip("not in a git repo")
	}
	// Should be a non-empty string.
	t.Logf("detected branch: %s", branch)
}

func TestMinInt(t *testing.T) {
	t.Parallel()
	if minInt(3, 5) != 3 {
		t.Error("expected 3")
	}
	if minInt(10, 2) != 2 {
		t.Error("expected 2")
	}
}
