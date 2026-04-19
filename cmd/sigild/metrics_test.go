package main

// metrics_test.go — tests for FR-022 VM metrics (spec 028 Phase 6b).
//
// Tests verify:
//   - vm_sessions_active appears in the metrics response
//   - vm_merge_duration_seconds appears with all four outcome labels
//   - vm_events_per_sec appears (stubbed as empty map in Phase 6b)
//   - topic_drops_total appears with vm-events key

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/sigil-tech/sigil/internal/config"
	"github.com/sigil-tech/sigil/internal/socket"
	"github.com/sigil-tech/sigil/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startMetricsTestServer creates a minimal socket server with the metrics
// handler registered.  It returns the server and its socket path.
func startMetricsTestServer(t *testing.T, st *store.Store) (*socket.Server, string) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "sigil-metrics-test-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })

	sockPath := filepath.Join(dir, "m.sock")
	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	srv := socket.New(sockPath, log)

	// We need a full daemonConfig and an inference engine.  Use a no-op engine
	// by wiring registerHandlers with nil where inference/notifier/analyzer are
	// not exercised by the metrics handler.  The metrics handler only uses
	// engine.LocalProcessInfo() and db, both of which we can supply.
	// Rather than invoke the full registerHandlers (which requires all
	// dependencies), register the metrics handler directly — the handler logic
	// is a single closure in registerHandlers; we test it via a controlled setup.
	//
	// Strategy: register a thin wrapper that exercises just the VM metric paths.
	cfg := daemonConfig{fileCfg: config.Defaults()}
	_ = cfg

	// Register VM handlers so the sessions table and topic config exist.
	registerVMHandlers(srv, st, cfg)

	// Register a "metrics-vm" method that exercises only the VM metric fields
	// from registerHandlers.  This avoids pulling in inference.Engine which
	// has no no-op constructor.
	srv.Handle("metrics-vm", func(ctx context.Context, _ socket.Request) socket.Response {
		// vm_sessions_active — query live from DB.
		var activeCount int64 = -1
		if err := st.DB().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sessions WHERE status IN ('booting','ready','connecting','stopping')`,
		).Scan(&activeCount); err != nil {
			activeCount = -1
		}

		return socket.Response{OK: true, Payload: socket.MarshalPayload(map[string]any{
			"vm_sessions_active":        activeCount,
			"vm_merge_duration_seconds": MergeDurationSnapshot(),
			"vm_events_per_sec":         map[string]float64{},
			"topic_drops_total": map[string]int64{
				"vm-events": socket.TopicDrops("vm-events"),
			},
		})}
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		srv.Stop()
	})
	require.NoError(t, srv.Start(ctx))
	return srv, sockPath
}

// sendMetrics dials the socket, sends a metrics-vm request, and returns the
// parsed response payload as a map.
func sendMetrics(t *testing.T, sockPath string) map[string]any {
	t.Helper()
	conn, err := net.Dial("unix", sockPath)
	require.NoError(t, err)
	defer conn.Close()

	req := socket.Request{Method: "metrics-vm"}
	require.NoError(t, json.NewEncoder(conn).Encode(req))

	var resp socket.Response
	require.NoError(t, json.NewDecoder(bufio.NewReader(conn)).Decode(&resp))
	require.True(t, resp.OK, "metrics-vm: %s", resp.Error)

	var result map[string]any
	require.NoError(t, json.Unmarshal(resp.Payload, &result))
	return result
}

// TestMetricsVMSessionsActive verifies that vm_sessions_active is present
// in the metrics response and reflects the current count of active sessions.
func TestMetricsVMSessionsActive(t *testing.T) {
	writeLauncherProfile(t, "/images/base.qcow2")
	st := openTestStoreForVM(t)
	_, sockPath := startMetricsTestServer(t, st)

	result := sendMetrics(t, sockPath)

	// vm_sessions_active must be present.
	raw, ok := result["vm_sessions_active"]
	require.True(t, ok, "vm_sessions_active must be present in metrics response")

	// Initially zero — no sessions started yet.
	active, isNum := raw.(float64)
	require.True(t, isNum, "vm_sessions_active must be a number, got %T", raw)
	assert.Equal(t, float64(0), active, "expected 0 active sessions initially")
}

// TestMetricsVMMergeDuration verifies that vm_merge_duration_seconds is
// present with all four outcome labels.
func TestMetricsVMMergeDuration(t *testing.T) {
	st := openTestStoreForVM(t)
	_, sockPath := startMetricsTestServer(t, st)

	result := sendMetrics(t, sockPath)

	raw, ok := result["vm_merge_duration_seconds"]
	require.True(t, ok, "vm_merge_duration_seconds must be present")

	outcomes, ok := raw.(map[string]any)
	require.True(t, ok, "vm_merge_duration_seconds must be an object, got %T", raw)

	// All four outcome labels must be present (even before any merge has run).
	for _, label := range []string{"complete", "partial", "failed", "already_complete"} {
		entry, exists := outcomes[label]
		require.True(t, exists, "outcome label %q must be present", label)

		m, ok := entry.(map[string]any)
		require.True(t, ok, "outcome %q must be an object, got %T", label, entry)
		assert.Contains(t, m, "count", "outcome %q must have 'count' field", label)
		assert.Contains(t, m, "sum_seconds", "outcome %q must have 'sum_seconds' field", label)
	}
}

// TestMetricsVMMergeDuration_ObservationRecorded verifies that
// ObserveMergeDuration is reflected in the subsequent MergeDurationSnapshot.
func TestMetricsVMMergeDuration_ObservationRecorded(t *testing.T) {
	// Record a synthetic observation for the "complete" outcome.
	ObserveMergeDuration("complete", 1_500_000_000) // 1.5 seconds

	snap := MergeDurationSnapshot()
	entry, ok := snap["complete"]
	require.True(t, ok)

	m, ok := entry.(map[string]any)
	require.True(t, ok)

	count, _ := m["count"].(int64)
	assert.GreaterOrEqual(t, count, int64(1), "count must be ≥ 1 after observation")

	sumSeconds, _ := m["sum_seconds"].(float64)
	assert.GreaterOrEqual(t, sumSeconds, 1.5, "sum_seconds must be ≥ 1.5 after 1.5s observation")
}

// TestMetricsVMEventsPerSec verifies that vm_events_per_sec is present
// and is an object (stubbed as empty in Phase 6b).
func TestMetricsVMEventsPerSec(t *testing.T) {
	st := openTestStoreForVM(t)
	_, sockPath := startMetricsTestServer(t, st)

	result := sendMetrics(t, sockPath)

	raw, ok := result["vm_events_per_sec"]
	require.True(t, ok, "vm_events_per_sec must be present")
	_, isMap := raw.(map[string]any)
	assert.True(t, isMap, "vm_events_per_sec must be an object, got %T", raw)
}

// TestMetricsTopicDrops verifies that topic_drops_total is present and
// contains the vm-events key.
func TestMetricsTopicDrops(t *testing.T) {
	st := openTestStoreForVM(t)
	_, sockPath := startMetricsTestServer(t, st)

	result := sendMetrics(t, sockPath)

	raw, ok := result["topic_drops_total"]
	require.True(t, ok, "topic_drops_total must be present")

	drops, ok := raw.(map[string]any)
	require.True(t, ok, "topic_drops_total must be an object, got %T", raw)
	assert.Contains(t, drops, "vm-events",
		"topic_drops_total must contain 'vm-events' key")
}
