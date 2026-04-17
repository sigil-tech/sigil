package hostctx

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"

	"github.com/sigil-tech/sigil/internal/store"
)

func TestFakeHostContextReader(t *testing.T) {
	ctx := context.Background()

	t.Run("returns configured patterns", func(t *testing.T) {
		fake := &FakeHostContextReader{
			Patterns: []store.PatternSummary{
				{Kind: "edit_frequency", Summary: `{"files":10}`, UpdatedAt: time.Now()},
				{Kind: "test_pattern", Summary: `{"pass":5}`, UpdatedAt: time.Now()},
			},
		}

		got, err := fake.RecentPatterns(ctx, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("expected 2 patterns, got %d", len(got))
		}
	})

	t.Run("returns configured task", func(t *testing.T) {
		fake := &FakeHostContextReader{
			Task: &store.TaskRecord{
				ID:       "task-1",
				RepoRoot: "/home/user/project",
				Phase:    "coding",
			},
		}

		task, err := fake.ActiveSession(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if task == nil || task.ID != "task-1" {
			t.Fatalf("expected task-1, got %v", task)
		}
	})

	t.Run("limit is respected", func(t *testing.T) {
		fake := &FakeHostContextReader{
			Patterns: []store.PatternSummary{
				{Kind: "a"}, {Kind: "b"}, {Kind: "c"},
			},
		}
		got, _ := fake.RecentPatterns(ctx, 2)
		if len(got) != 2 {
			t.Fatalf("expected 2, got %d", len(got))
		}
	})

	t.Run("degraded mode returns nil", func(t *testing.T) {
		fake := &FakeHostContextReader{}
		got, err := fake.RecentPatterns(ctx, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("expected empty, got %d", len(got))
		}
		task, err := fake.ActiveSession(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if task != nil {
			t.Fatal("expected nil task")
		}
	})
}

func TestVsockReader(t *testing.T) {
	ctx := context.Background()

	t.Run("nil conn returns empty (degraded)", func(t *testing.T) {
		r := &VsockReader{}
		patterns, err := r.RecentPatterns(ctx, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(patterns) != 0 {
			t.Fatalf("expected empty, got %d", len(patterns))
		}
	})

	t.Run("round trip with net.Pipe", func(t *testing.T) {
		client, server := net.Pipe()
		defer client.Close()
		defer server.Close()

		r := NewVsockReader(client)

		expectedPatterns := []store.PatternSummary{
			{Kind: "edit_freq", Summary: `{"count":42}`, UpdatedAt: time.Now().Truncate(time.Millisecond)},
		}

		// Server goroutine: read request, send response.
		go func() {
			// Read 4-byte header.
			var header [4]byte
			io.ReadFull(server, header[:]) //nolint:errcheck
			length := uint32(header[0])<<24 | uint32(header[1])<<16 | uint32(header[2])<<8 | uint32(header[3])
			data := make([]byte, length)
			io.ReadFull(server, data) //nolint:errcheck

			// Send response.
			resp := response{OK: true, Patterns: expectedPatterns}
			respData, _ := json.Marshal(resp)
			respLen := uint32(len(respData))
			respHeader := []byte{byte(respLen >> 24), byte(respLen >> 16), byte(respLen >> 8), byte(respLen)}
			server.Write(respHeader) //nolint:errcheck
			server.Write(respData)   //nolint:errcheck
		}()

		patterns, err := r.RecentPatterns(ctx, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(patterns) != 1 {
			t.Fatalf("expected 1 pattern, got %d", len(patterns))
		}
		if patterns[0].Kind != "edit_freq" {
			t.Errorf("expected edit_freq, got %s", patterns[0].Kind)
		}
	})
}
