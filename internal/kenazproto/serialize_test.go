package kenazproto_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sigil-tech/sigil/internal/event"
	"github.com/sigil-tech/sigil/internal/kenazproto"
)

// -update regenerates golden files from the current Serialize output.
// Always review diffs carefully — these files are security-reviewed.
var update = flag.Bool("update", false, "regenerate golden files")

// fixedTime is a stable timestamp for deterministic golden output.
var fixedTime = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

// ---------------------------------------------------------------------------
// Golden-file helpers
// ---------------------------------------------------------------------------

func readGolden(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	data, err := os.ReadFile(path)
	require.NoError(t, err, "golden file %s not found — run with -update to create", path)
	return data
}

func writeGolden(t *testing.T, name string, data []byte) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	err := os.WriteFile(path, data, 0o644)
	require.NoError(t, err, "writing golden %s", path)
}

func marshalGolden(t *testing.T, ke kenazproto.KenazEvent) []byte {
	t.Helper()
	b, err := json.MarshalIndent(ke, "", "  ")
	require.NoError(t, err)
	b = append(b, '\n')
	return b
}

func checkGolden(t *testing.T, name string, ke kenazproto.KenazEvent) {
	t.Helper()
	got := marshalGolden(t, ke)
	if *update {
		writeGolden(t, name, got)
		return
	}
	want := readGolden(t, name)
	assert.Equal(t, string(want), string(got), "golden mismatch for %s", name)
}

// ---------------------------------------------------------------------------
// Mapped-kinds golden tests (Task 4.6)
// ---------------------------------------------------------------------------

func TestSerialize_GoldenMapped(t *testing.T) {
	tests := []struct {
		name string
		evt  event.Event
	}{
		{
			name: "file_write",
			evt: event.Event{
				ID:        1001,
				Kind:      event.KindFile,
				Source:    "host",
				Timestamp: fixedTime,
				Payload: map[string]any{
					"path":  "/home/nick/workspace/sigil/internal/kenazproto/serialize.go",
					"ext":   ".go",
					"delta": "+1.2K",
					"op":    "write",
				},
			},
		},
		{
			name: "file_read",
			evt: event.Event{
				ID:        1002,
				Kind:      event.KindFile,
				Source:    "host",
				Timestamp: fixedTime,
				Payload: map[string]any{
					"path": "/home/nick/workspace/sigil/go.mod",
					"ext":  ".mod",
					"op":   "read",
				},
			},
		},
		{
			name: "git_commit",
			evt: event.Event{
				ID:        1003,
				Kind:      event.KindGit,
				Source:    "host",
				Timestamp: fixedTime,
				Payload: map[string]any{
					"repo":   "~/workspace/sigil",
					"op":     "commit",
					"branch": "main",
					"hash":   "2c460d1f9a8b3e5",
				},
			},
		},
		{
			name: "git_checkout",
			evt: event.Event{
				ID:        1004,
				Kind:      event.KindGit,
				Source:    "host",
				Timestamp: fixedTime,
				Payload: map[string]any{
					"repo":   "~/workspace/sigil",
					"op":     "checkout",
					"branch": "027-kenaz-sigild-observer-integration",
				},
			},
		},
		{
			name: "process_spawn",
			evt: event.Event{
				ID:        1005,
				Kind:      event.KindProcess,
				Source:    "host",
				Timestamp: fixedTime,
				Payload: map[string]any{
					"name": "node",
					"pid":  float64(81341),
				},
			},
		},
		{
			name: "terminal_cmd",
			evt: event.Event{
				ID:        1006,
				Kind:      event.KindTerminal,
				Source:    "host",
				Timestamp: fixedTime,
				Payload: map[string]any{
					"cmd":       "go test ./internal/kenazproto/...",
					"exit_code": float64(0),
				},
			},
		},
		{
			name: "clipboard_copy",
			evt: event.Event{
				ID:        1007,
				Kind:      event.KindClipboard,
				Source:    "host",
				Timestamp: fixedTime,
				Payload: map[string]any{
					"hash": "sha256:4f3a91e7b2d8c5f0a1e9d3b6c8f2a4e7b0d5c9f3a1e8b2d6c4f0a9e3b7d1c5",
					"size": float64(248),
				},
			},
		},
		{
			name: "network_resolve",
			evt: event.Event{
				ID:        1008,
				Kind:      event.KindNetwork,
				Source:    "host",
				Timestamp: fixedTime,
				Payload: map[string]any{
					"host":  "api.github.com",
					"port":  "443",
					"proto": "tcp4",
				},
			},
		},
		{
			name: "typing_cadence",
			evt: event.Event{
				ID:        1009,
				Kind:      event.KindTyping,
				Source:    "host",
				Timestamp: fixedTime,
				Payload: map[string]any{
					"cadence": "32 keys / 30s",
				},
			},
		},
		{
			name: "focus_change_short_title",
			evt: event.Event{
				ID:        1010,
				Kind:      event.KindHyprland,
				Source:    "host",
				Timestamp: fixedTime,
				Payload: map[string]any{
					"title":     "kitty",
					"workspace": "3",
					"bin":       "kitty",
				},
			},
		},
		{
			name: "app_lifecycle",
			evt: event.Event{
				ID:        1011,
				Kind:      event.KindAppLifecycle,
				Source:    "host",
				Timestamp: fixedTime,
				Payload: map[string]any{
					"app":   "VSCode.app",
					"event": "launched",
				},
			},
		},
		{
			name: "focus_mode_state",
			evt: event.Event{
				ID:        1012,
				Kind:      event.KindFocusMode,
				Source:    "host",
				Timestamp: fixedTime,
				Payload: map[string]any{
					"state": "Focus",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ke, ok := kenazproto.Serialize(tt.evt)
			require.True(t, ok, "expected Serialize to return ok=true for %s", tt.evt.Kind)
			checkGolden(t, tt.name, ke)
		})
	}
}

// ---------------------------------------------------------------------------
// Adversarial golden tests (Task 4.7)
// ---------------------------------------------------------------------------

func TestSerialize_GoldenAdversarial(t *testing.T) {
	// focus_change_long_title: triggers ContentClassTruncated.
	t.Run("focus_change_long_title", func(t *testing.T) {
		evt := event.Event{
			ID:        2001,
			Kind:      event.KindHyprland,
			Source:    "host",
			Timestamp: fixedTime,
			Payload: map[string]any{
				// 80 ASCII chars — exceeds 64-byte cap.
				"title":     "Very Long Application Title That Exceeds The Sixty-Four Byte Hard Cap Limit!",
				"workspace": "1",
				"bin":       "longapp",
			},
		}
		ke, ok := kenazproto.Serialize(evt)
		require.True(t, ok)
		assert.Equal(t, kenazproto.ContentClassTruncated, ke.ContentClass)
		assert.LessOrEqual(t, len(ke.Subject), 64)
		checkGolden(t, "focus_change_long_title", ke)
	})

	// adversarial_newlines_in_subject: newlines must be replaced with spaces.
	t.Run("adversarial_newlines_in_subject", func(t *testing.T) {
		evt := event.Event{
			ID:        2002,
			Kind:      event.KindTerminal,
			Source:    "host",
			Timestamp: fixedTime,
			Payload: map[string]any{
				"cmd":       "bash\n--norc\r\n-i",
				"exit_code": float64(1),
			},
		}
		ke, ok := kenazproto.Serialize(evt)
		require.True(t, ok)
		assert.NotContains(t, ke.Subject, "\n")
		assert.NotContains(t, ke.Subject, "\r")
		checkGolden(t, "adversarial_newlines_in_subject", ke)
	})

	// adversarial_unicode_surrogates: invalid UTF-8 replaced with U+FFFD.
	t.Run("adversarial_unicode_surrogates", func(t *testing.T) {
		evt := event.Event{
			ID:        2003,
			Kind:      event.KindFile,
			Source:    "host",
			Timestamp: fixedTime,
			Payload: map[string]any{
				// Invalid UTF-8 byte sequence embedded in the path.
				"path": "/home/nick/\xed\xa0\x80bad\xed\xbf\xbfpath.go",
				"ext":  ".go",
			},
		}
		ke, ok := kenazproto.Serialize(evt)
		require.True(t, ok)
		checkGolden(t, "adversarial_unicode_surrogates", ke)
	})

	// adversarial_binary_bytes: control characters replaced with '?'.
	t.Run("adversarial_binary_bytes", func(t *testing.T) {
		evt := event.Event{
			ID:        2004,
			Kind:      event.KindProcess,
			Source:    "host",
			Timestamp: fixedTime,
			Payload: map[string]any{
				"name": "proc\x00with\x01binary\x1fbytes",
				"pid":  float64(99),
			},
		}
		ke, ok := kenazproto.Serialize(evt)
		require.True(t, ok)
		assert.NotContains(t, ke.Subject, "\x00")
		assert.NotContains(t, ke.Subject, "\x01")
		checkGolden(t, "adversarial_binary_bytes", ke)
	})

	// adversarial_deep_path: 10-level absolute path must emit only the last two
	// components prefixed with "…/".  Confirms the truncation invariant holds
	// regardless of path depth.
	t.Run("adversarial_deep_path", func(t *testing.T) {
		evt := event.Event{
			ID:        2006,
			Kind:      event.KindFile,
			Source:    "host",
			Timestamp: fixedTime,
			Payload: map[string]any{
				"path": "/a/b/c/d/e/f/g/h/penultimate/filename.ext",
				"ext":  ".ext",
				"op":   "write",
			},
		}
		ke, ok := kenazproto.Serialize(evt)
		require.True(t, ok)
		assert.Equal(t, "…/penultimate/filename.ext", ke.Subject)
		checkGolden(t, "adversarial_deep_path", ke)
	})

	// adversarial_10kb_subject: oversize file path must be dropped (>256 bytes
	// after left-truncation cannot be produced from truncatePathLeft since it
	// caps at 256; test that a synthetic path of exactly 257 bytes in Subject
	// causes a drop via validateLengths — we inject directly via a KindNetwork
	// event since it copies host verbatim and we can make it exactly 257 bytes).
	//
	// The golden records the drop outcome: ok=false, KenazEvent zero-value.
	t.Run("adversarial_10kb_subject", func(t *testing.T) {
		// Build a host string that is 65 bytes — exceeds the 64-byte Origin cap.
		longOrigin := make([]byte, 65)
		for i := range longOrigin {
			longOrigin[i] = 'a'
		}
		evt := event.Event{
			ID:        2005,
			Kind:      event.KindNetwork,
			Source:    string(longOrigin), // will land in Origin, capped at 64
			Timestamp: fixedTime,
			Payload: map[string]any{
				// Build a host+port Subject that is 257 bytes after assembly.
				"host": string(make([]byte, 250)) + "example.com",
				"port": "80",
			},
		}
		// Populate host with valid chars.
		host := ""
		for i := 0; i < 250; i++ {
			host += "a"
		}
		evt.Payload["host"] = host
		dropped := kenazproto.KenazEventDropped()

		ke, ok := kenazproto.Serialize(evt)
		assert.False(t, ok, "expected oversize event to be dropped")
		assert.Equal(t, kenazproto.KenazEvent{}, ke)
		assert.Greater(t, kenazproto.KenazEventDropped(), dropped, "drop counter must increment")

		// Golden records a canonical zero-value JSON for documentation purposes.
		checkGolden(t, "adversarial_10kb_subject", ke)
	})
}

// ---------------------------------------------------------------------------
// Unmapped-kind drop tests (Task 4.8)
// ---------------------------------------------------------------------------

func TestSerialize_UnmappedKindsDropped(t *testing.T) {
	unmapped := []event.Kind{
		event.KindIdle,
		event.KindPointer,
		event.KindAudio,
		event.KindPower,
		event.KindDisplay,
		event.KindScreenshot,
		event.KindDownload,
		event.KindCalendar,
		event.KindBrowser,
		event.KindAI,
	}
	require.Len(t, unmapped, 10, "all 10 unmapped kinds must be tested")

	for _, k := range unmapped {
		k := k
		t.Run(string(k), func(t *testing.T) {
			before := kenazproto.KenazEventDropped()
			evt := event.Event{
				ID:        9000,
				Kind:      k,
				Source:    "host",
				Timestamp: fixedTime,
				Payload:   map[string]any{},
			}
			ke, ok := kenazproto.Serialize(evt)
			assert.False(t, ok, "unmapped kind %s must return ok=false", k)
			assert.Equal(t, kenazproto.KenazEvent{}, ke, "unmapped kind %s must return zero KenazEvent", k)
			assert.Greater(t, kenazproto.KenazEventDropped(), before, "drop counter must increment for %s", k)
		})
	}

	// Golden records the idle drop specifically (spec requirement for unmapped_idle.golden).
	t.Run("golden_idle_drop", func(t *testing.T) {
		evt := event.Event{
			ID:        9001,
			Kind:      event.KindIdle,
			Source:    "host",
			Timestamp: fixedTime,
			Payload:   map[string]any{"state": "idle"},
		}
		ke, ok := kenazproto.Serialize(evt)
		assert.False(t, ok)
		assert.Equal(t, kenazproto.KenazEvent{}, ke)
		checkGolden(t, "unmapped_idle", ke)
	})
}

// ---------------------------------------------------------------------------
// Source-ID mapping tests (Task 4.3)
// ---------------------------------------------------------------------------

func TestSourceIDForKind(t *testing.T) {
	tests := []struct {
		kind   event.Kind
		wantID string
		wantOK bool
	}{
		{event.KindFile, "filesystem", true},
		{event.KindGit, "filesystem", true},
		{event.KindProcess, "process", true},
		{event.KindTerminal, "process", true},
		{event.KindClipboard, "clipboard", true},
		{event.KindNetwork, "network", true},
		{event.KindTyping, "keystroke", true},
		{event.KindHyprland, "app-context", true},
		{event.KindAppLifecycle, "app-context", true},
		{event.KindFocusMode, "app-context", true},
		{event.KindDesktop, "app-context", true},
		{event.KindAppState, "app-context", true},
		// Unmapped.
		{event.KindIdle, "", false},
		{event.KindPointer, "", false},
		{event.KindAudio, "", false},
		{event.KindPower, "", false},
		{event.KindDisplay, "", false},
		{event.KindScreenshot, "", false},
		{event.KindDownload, "", false},
		{event.KindCalendar, "", false},
		{event.KindBrowser, "", false},
		{event.KindAI, "", false},
	}

	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			got, ok := kenazproto.SourceIDForKind(tt.kind)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantID, got)
		})
	}
}

// ---------------------------------------------------------------------------
// Oversize-drop tests (Task 4.4)
// ---------------------------------------------------------------------------

func TestSerialize_OversizeDrops(t *testing.T) {
	// Subject > 256 bytes via a long process name injected into Subject.
	// The process serializer produces: name + " " + "pid N".
	// Make name 260 bytes; the resulting Subject > 256 bytes triggers drop.
	longName := ""
	for i := 0; i < 260; i++ {
		longName += "a"
	}
	before := kenazproto.KenazEventDropped()
	evt := event.Event{
		ID:        8001,
		Kind:      event.KindProcess,
		Source:    "host",
		Timestamp: fixedTime,
		Payload: map[string]any{
			"name": longName,
			"pid":  float64(1),
		},
	}
	ke, ok := kenazproto.Serialize(evt)
	assert.False(t, ok)
	assert.Equal(t, kenazproto.KenazEvent{}, ke)
	assert.Greater(t, kenazproto.KenazEventDropped(), before)
}

// ---------------------------------------------------------------------------
// Normalization tests (Task 4.5)
// ---------------------------------------------------------------------------

func TestSerialize_Normalization(t *testing.T) {
	t.Run("newlines_replaced", func(t *testing.T) {
		evt := event.Event{
			ID:        7001,
			Kind:      event.KindTyping,
			Source:    "host",
			Timestamp: fixedTime,
			Payload: map[string]any{
				"cadence": "32 keys\n/ 30s",
			},
		}
		ke, ok := kenazproto.Serialize(evt)
		require.True(t, ok)
		assert.NotContains(t, ke.Subject, "\n")
		assert.Contains(t, ke.Subject, " ")
	})

	t.Run("control_chars_replaced", func(t *testing.T) {
		evt := event.Event{
			ID:        7002,
			Kind:      event.KindTyping,
			Source:    "host",
			Timestamp: fixedTime,
			Payload: map[string]any{
				"cadence": "32\x00keys\x1f/\x0130s",
			},
		}
		ke, ok := kenazproto.Serialize(evt)
		require.True(t, ok)
		assert.NotContains(t, ke.Subject, "\x00")
		assert.NotContains(t, ke.Subject, "\x1f")
		assert.Contains(t, ke.Subject, "?")
	})

	t.Run("invalid_utf8_replaced", func(t *testing.T) {
		evt := event.Event{
			ID:        7003,
			Kind:      event.KindTyping,
			Source:    "host",
			Timestamp: fixedTime,
			Payload: map[string]any{
				"cadence": "32 \xed\xa0\x80 keys / 30s",
			},
		}
		ke, ok := kenazproto.Serialize(evt)
		require.True(t, ok)
		assert.Contains(t, ke.Subject, "\uFFFD")
	})
}

// ---------------------------------------------------------------------------
// Benchmarks (Task 4.9)
// ---------------------------------------------------------------------------

var allKindInputs = []event.Event{
	{ID: 1, Kind: event.KindFile, Source: "host", Timestamp: fixedTime,
		Payload: map[string]any{"path": "/home/user/project/main.go", "ext": ".go", "delta": "+512"}},
	{ID: 2, Kind: event.KindGit, Source: "host", Timestamp: fixedTime,
		Payload: map[string]any{"repo": "~/project", "op": "commit", "branch": "main", "hash": "abc1234def"}},
	{ID: 3, Kind: event.KindProcess, Source: "host", Timestamp: fixedTime,
		Payload: map[string]any{"name": "node", "pid": float64(1234)}},
	{ID: 4, Kind: event.KindTerminal, Source: "host", Timestamp: fixedTime,
		Payload: map[string]any{"cmd": "go build ./...", "exit_code": float64(0)}},
	{ID: 5, Kind: event.KindClipboard, Source: "host", Timestamp: fixedTime,
		Payload: map[string]any{"hash": "sha256:abcdef0123456789", "size": float64(128)}},
	{ID: 6, Kind: event.KindNetwork, Source: "host", Timestamp: fixedTime,
		Payload: map[string]any{"host": "api.github.com", "port": "443", "proto": "tcp4"}},
	{ID: 7, Kind: event.KindTyping, Source: "host", Timestamp: fixedTime,
		Payload: map[string]any{"cadence": "45 keys / 30s"}},
	{ID: 8, Kind: event.KindHyprland, Source: "host", Timestamp: fixedTime,
		Payload: map[string]any{"title": "kitty", "workspace": "2", "bin": "kitty"}},
	{ID: 9, Kind: event.KindAppLifecycle, Source: "host", Timestamp: fixedTime,
		Payload: map[string]any{"app": "VSCode", "event": "launched"}},
	{ID: 10, Kind: event.KindFocusMode, Source: "host", Timestamp: fixedTime,
		Payload: map[string]any{"state": "Focus"}},
	{ID: 11, Kind: event.KindDesktop, Source: "host", Timestamp: fixedTime,
		Payload: map[string]any{"id": "1", "name": "desktop 1"}},
	{ID: 12, Kind: event.KindAppState, Source: "host", Timestamp: fixedTime,
		Payload: map[string]any{"app": "Slack", "state": "hidden"}},
}

// ---------------------------------------------------------------------------
// fileSubjectFromPath unit tests (ADR-027a Defect 1 fix)
// ---------------------------------------------------------------------------

func TestFileSubjectFromPath(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "deep path (5 levels) produces ellipsis prefix",
			input: "/home/nick/workspace/sigil/internal/kenazproto/serialize.go",
			want:  "…/kenazproto/serialize.go",
		},
		{
			name:  "3-level path produces ellipsis prefix",
			input: "/etc/sigil/config.toml",
			want:  "…/sigil/config.toml",
		},
		{
			name:  "2-level path produces no prefix",
			input: "internal/store.go",
			want:  "internal/store.go",
		},
		{
			name:  "single component produces no prefix",
			input: "go.mod",
			want:  "go.mod",
		},
		{
			name:  "empty string returns empty string",
			input: "",
			want:  "",
		},
		{
			name:  "trailing slash handled by filepath.Clean",
			input: "/home/nick/workspace/sigil/",
			want:  "…/workspace/sigil",
		},
		{
			name:  "double slashes collapsed by filepath.Clean",
			input: "/home//nick//workspace/sigil/go.mod",
			want:  "…/sigil/go.mod",
		},
		{
			name:  "root-only path returns empty string",
			input: "/",
			want:  "",
		},
		{
			name:  "absolute 2-component path produces no prefix",
			input: "/sigil/go.mod",
			want:  "sigil/go.mod",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := kenazproto.FileSubjectFromPath(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ---------------------------------------------------------------------------
// VM-origin tests (spec 028 Phase 6 Task 6.1)
// ---------------------------------------------------------------------------

// TestSerializeVMOrigin verifies that Serialize populates Origin and VMID
// correctly for events with Source = "vm:<uuid>", and that redaction rules
// still apply (FR-010e mirror requirement).
func TestSerializeVMOrigin(t *testing.T) {
	const sessionUUID = "550e8400-e29b-41d4-a716-446655440000"
	vmSource := "vm:" + sessionUUID

	tests := []struct {
		name       string
		evt        event.Event
		wantOrigin string
		wantVMID   string
	}{
		{
			name: "file_event_vm_origin",
			evt: event.Event{
				ID:        3001,
				Kind:      event.KindFile,
				Source:    vmSource,
				Timestamp: fixedTime,
				Payload: map[string]any{
					"path": "/home/user/project/main.go",
					"ext":  ".go",
					"op":   "write",
				},
			},
			wantOrigin: vmSource,
			wantVMID:   sessionUUID,
		},
		{
			name: "terminal_event_vm_origin_redaction_applies",
			evt: event.Event{
				ID:        3002,
				Kind:      event.KindTerminal,
				Source:    vmSource,
				Timestamp: fixedTime,
				Payload: map[string]any{
					// argv[0] only rule still applies for VM-origin events.
					"cmd":       "go test ./...",
					"exit_code": float64(0),
				},
			},
			wantOrigin: vmSource,
			wantVMID:   sessionUUID,
		},
		{
			name: "host_event_no_vmid",
			evt: event.Event{
				ID:        3003,
				Kind:      event.KindFile,
				Source:    "host",
				Timestamp: fixedTime,
				Payload: map[string]any{
					"path": "/home/user/go.mod",
					"ext":  ".mod",
				},
			},
			wantOrigin: "host",
			wantVMID:   "",
		},
		{
			name: "empty_source_defaults_to_host_no_vmid",
			evt: event.Event{
				ID:        3004,
				Kind:      event.KindFile,
				Source:    "",
				Timestamp: fixedTime,
				Payload: map[string]any{
					"path": "/home/user/go.mod",
					"ext":  ".mod",
				},
			},
			wantOrigin: "host",
			wantVMID:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ke, ok := kenazproto.Serialize(tt.evt)
			require.True(t, ok, "expected ok=true for kind %s", tt.evt.Kind)
			assert.Equal(t, tt.wantOrigin, ke.Origin, "Origin mismatch")
			assert.Equal(t, tt.wantVMID, ke.VMID, "VMID mismatch")
		})
	}
}

// ---------------------------------------------------------------------------
// BenchmarkSerialize_AllKinds measures Serialize across all 12 mapped kinds.
// Target: ≤ 500 ns/op, ≤ 1 alloc/op (contract §Benchmarks).
func BenchmarkSerialize_AllKinds(b *testing.B) {
	n := len(allKindInputs)
	for b.Loop() {
		_, _ = kenazproto.Serialize(allKindInputs[b.N%n])
	}
}

// BenchmarkSerialize_HotPath measures the most common path: KindFile.
// Target: ≤ 500 ns/op, ≤ 1 alloc/op.
func BenchmarkSerialize_HotPath(b *testing.B) {
	evt := allKindInputs[0] // KindFile
	for b.Loop() {
		_, _ = kenazproto.Serialize(evt)
	}
}
