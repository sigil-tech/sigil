//go:build darwin

package vmdriver_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"
)

// TestMain implements the re-exec test-helper trick: when the test binary is
// invoked with SIGIL_TEST_FAKE_VZ_MODE set, it runs the fake sigild-vz
// implementation and exits without touching the Go testing runtime. The
// darwin vmdriver tests point SIGILD_VZ_BINARY at os.Args[0] so spawning
// sigild-vz is really spawning this same test binary in fake mode.
//
// Normal test runs (env unset) fall through to the standard goleak-verified
// test main.
func TestMain(m *testing.M) {
	if mode := os.Getenv("SIGIL_TEST_FAKE_VZ_MODE"); mode != "" {
		runFakeSigildVZ(mode)
		return
	}
	goleak.VerifyTestMain(m)
}

// runFakeSigildVZ is the fake's main loop. It MUST NOT call t.*; it runs
// as a subprocess and communicates exclusively over stdin/stdout. Stderr is
// usable for debug output (captured into the driver's stderr ring buffer).
//
// Modes (selected via SIGIL_TEST_FAKE_VZ_MODE):
//
//	happy         — correct handshake; echoes ok:true to every command
//	bad-protocol  — handshake carries protocol:999 (sigild must reject)
//	no-handshake  — emits nothing, hangs reading stdin forever
//	error-start   — handshake ok, responds ok:false to "start"
//	push-stats    — handshake ok, on subscribe-stats pushes N stat events
//	                then a hypervisor_exit event and exits
//	crash         — handshake ok, then exits immediately
//	slow-response — handshake ok, but waits 10s before replying (for timeouts)
//
// Optional env knobs:
//
//	SIGIL_TEST_FAKE_VZ_STAT_COUNT — integer; number of stats to emit in push-stats mode
//	SIGIL_TEST_FAKE_VZ_VERSION    — version string to include in handshake
func runFakeSigildVZ(mode string) {
	version := os.Getenv("SIGIL_TEST_FAKE_VZ_VERSION")
	if version == "" {
		version = "0.0.0-fake"
	}

	// Handshake
	switch mode {
	case "no-handshake":
		// Sleep forever, simulating a hung sigild-vz.
		<-context.Background().Done()
		return
	case "bad-protocol":
		writeLine(map[string]any{"vz_version": version, "protocol": 999})
	default:
		writeLine(map[string]any{"vz_version": version, "protocol": 1})
	}

	if mode == "crash" {
		os.Exit(1)
	}

	reader := bufio.NewReader(os.Stdin)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var req struct {
			ID     string          `json:"id"`
			Cmd    string          `json:"cmd"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			fmt.Fprintf(os.Stderr, "fake sigild-vz: bad request: %v\n", err)
			continue
		}

		handleFakeCommand(mode, req.ID, req.Cmd, req.Params)

		if req.Cmd == "close" {
			return
		}
	}
}

// handleFakeCommand dispatches a single JSON-line command in the fake. The
// logic here mirrors the wire shape defined in ADR-028a §3 so the driver
// sees a realistic response for every mode.
func handleFakeCommand(mode, id, cmd string, _ json.RawMessage) {
	respond := func(ok bool, result any, errCode, errMsg string) {
		r := map[string]any{"id": id, "ok": ok}
		if ok {
			r["result"] = result
		} else {
			r["error"] = map[string]string{"code": errCode, "message": errMsg}
		}
		writeLine(r)
	}

	if mode == "slow-response" {
		time.Sleep(10 * time.Second)
	}

	switch cmd {
	case "start":
		if mode == "error-start" {
			respond(false, nil, "ERR_IMAGE_MISSING", "fake: disk image not found")
			return
		}
		respond(true, map[string]any{
			"session_id": "fake-session-" + id,
			"pid":        4242,
		}, "", "")

	case "stop":
		respond(true, map[string]any{}, "", "")

	case "status":
		respond(true, map[string]any{
			"state":      "running",
			"started_at": time.Now().UTC().Format(time.RFC3339),
			"pid":        4242,
		}, "", "")

	case "health":
		respond(true, map[string]any{}, "", "")

	case "subscribe-stats":
		respond(true, map[string]any{}, "", "")
		if mode == "push-stats" {
			count := 3
			if s := os.Getenv("SIGIL_TEST_FAKE_VZ_STAT_COUNT"); s != "" {
				if n, err := strconv.Atoi(s); err == nil && n > 0 {
					count = n
				}
			}
			go func() {
				for i := 0; i < count; i++ {
					writeLine(map[string]any{
						"event": "stat",
						"payload": map[string]any{
							"ts":           time.Now().UTC().Format(time.RFC3339),
							"cpu_percent":  15.0 + float64(i),
							"cpu_cores":    4,
							"mem_used_mb":  1024 + uint64(i)*128,
							"mem_alloc_mb": 4096,
						},
					})
					time.Sleep(10 * time.Millisecond)
				}
				writeLine(map[string]any{
					"event":   "hypervisor_exit",
					"payload": map[string]any{"code": 0},
				})
				// Give the driver a moment to observe the exit event, then
				// exit the fake so waitDone fires.
				time.Sleep(20 * time.Millisecond)
				os.Exit(0)
			}()
		}

	case "close":
		respond(true, map[string]any{}, "", "")

	default:
		respond(false, nil, "ERR_UNKNOWN_COMMAND", "fake: unknown command "+cmd)
	}
}

// writeLine marshals v to JSON and writes it with a trailing newline on
// stdout. Output is flushed implicitly — os.Stdout in a subprocess is not
// buffered by Go's runtime.
func writeLine(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fake sigild-vz: marshal: %v\n", err)
		return
	}
	data = append(data, '\n')
	if _, err := os.Stdout.Write(data); err != nil {
		os.Exit(2)
	}
}

// fakeEnv returns the environment slice that spawns this test binary as a
// fake sigild-vz in the given mode. Callers pass it via t.Setenv before
// constructing the driver under test.
func fakeEnv(t *testing.T, mode string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	t.Setenv("SIGILD_VZ_BINARY", exe)
	t.Setenv("SIGIL_TEST_FAKE_VZ_MODE", mode)
}
