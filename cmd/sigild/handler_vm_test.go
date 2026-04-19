package main

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
	"github.com/sigil-tech/sigil/internal/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeLauncherProfile writes a minimal LauncherProfile JSON to a temp dir and
// sets XDG_CONFIG_HOME so that launcherprofile.Read() picks it up during tests.
// Returns a cleanup function; callers must invoke it with defer or t.Cleanup.
func writeLauncherProfile(t *testing.T, diskImagePath string) {
	t.Helper()
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)

	dir := filepath.Join(cfgDir, "sigil-launcher")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	profile := map[string]any{
		"memorySize":        uint64(4294967296),
		"cpuCount":          2,
		"workspacePath":     "/home/testuser/workspace",
		"diskImagePath":     diskImagePath,
		"kernelPath":        "/images/vmlinuz",
		"initrdPath":        "/images/initrd",
		"sshPort":           uint16(2222),
		"kernelCommandLine": "console=hvc0",
		"editor":            "vscode",
		"containerEngine":   "docker",
		"shell":             "zsh",
		"notificationLevel": 2,
	}
	raw, err := json.Marshal(profile)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "settings.json"), raw, 0o600))
}

// openTestStoreForVM opens an in-memory store. The store migration creates the
// sessions table so VM handlers have a valid schema to operate against.
func openTestStoreForVM(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(":memory:")
	require.NoError(t, err, "open in-memory store")
	t.Cleanup(func() { s.Close() })
	return s
}

// shortTempDirVM returns a short temp directory under /tmp to keep Unix socket
// paths within the 104-byte sun_path limit imposed by the kernel.
func shortTempDirVM(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "sigil-vm-test-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// startVMTestServer launches a socket.Server with only the VM handlers
// registered. It returns the server and its socket path.
func startVMTestServer(t *testing.T, st *store.Store) (*socket.Server, string) {
	t.Helper()
	dir := shortTempDirVM(t)
	sockPath := filepath.Join(dir, "vm.sock")
	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	srv := socket.New(sockPath, log)
	cfg := daemonConfig{fileCfg: config.Defaults()}
	registerVMHandlers(srv, st, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		srv.Stop()
	})
	require.NoError(t, srv.Start(ctx))
	return srv, sockPath
}

// sendVM dials the socket, sends one request, reads one response, and closes.
func sendVM(t *testing.T, sockPath string, method string, payload any) socket.Response {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)

	conn, err := net.Dial("unix", sockPath)
	require.NoError(t, err)
	defer conn.Close()

	req := socket.Request{Method: method, Payload: json.RawMessage(raw)}
	require.NoError(t, json.NewEncoder(conn).Encode(req))

	var resp socket.Response
	require.NoError(t, json.NewDecoder(bufio.NewReader(conn)).Decode(&resp))
	return resp
}

// TestVMListHandler_Stub verifies that VMList returns OK and a valid JSON
// array. The in-memory store migration seeds one host-default sentinel row, so
// the result is non-empty; the test asserts the response shape, not a specific
// count.
func TestVMListHandler_Stub(t *testing.T) {
	st := openTestStoreForVM(t)
	_, sockPath := startVMTestServer(t, st)

	resp := sendVM(t, sockPath, "VMList", map[string]any{"limit": 10})
	require.True(t, resp.OK, "expected OK, got error: %s", resp.Error)

	var sessions []vm.Session
	require.NoError(t, json.Unmarshal(resp.Payload, &sessions))
	// Response must be a valid (possibly empty) slice — never null or an object.
	assert.NotNil(t, sessions)
}

// TestVMStartHandler_Stub verifies that VMStart inserts a new session and
// returns it with StateBooting and a non-empty ID — no hypervisor required.
func TestVMStartHandler_Stub(t *testing.T) {
	writeLauncherProfile(t, "/images/base.qcow2")
	st := openTestStoreForVM(t)
	_, sockPath := startVMTestServer(t, st)

	resp := sendVM(t, sockPath, "VMStart", map[string]any{
		"disk_image_path": "/images/base.qcow2",
		"overlay_path":    "/tmp/overlay.qcow2",
	})
	require.True(t, resp.OK, "expected OK, got error: %s", resp.Error)

	var sess vm.Session
	require.NoError(t, json.Unmarshal(resp.Payload, &sess))
	assert.NotEmpty(t, sess.ID)
	assert.Equal(t, vm.StateBooting, sess.Status)
}

// TestVMStopHandler_Stub verifies that VMStop transitions a running session to
// StateStopping. No hypervisor is involved.
func TestVMStopHandler_Stub(t *testing.T) {
	writeLauncherProfile(t, "/images/base.qcow2")
	st := openTestStoreForVM(t)
	_, sockPath := startVMTestServer(t, st)

	startResp := sendVM(t, sockPath, "VMStart", map[string]any{
		"disk_image_path": "/images/base.qcow2",
	})
	require.True(t, startResp.OK)

	var sess vm.Session
	require.NoError(t, json.Unmarshal(startResp.Payload, &sess))

	stopResp := sendVM(t, sockPath, "VMStop", map[string]any{"session_id": sess.ID})
	require.True(t, stopResp.OK, "expected OK, got error: %s", stopResp.Error)

	statusResp := sendVM(t, sockPath, "VMStatus", map[string]any{"session_id": sess.ID})
	require.True(t, statusResp.OK)

	var got vm.Session
	require.NoError(t, json.Unmarshal(statusResp.Payload, &got))
	assert.Equal(t, vm.StateStopping, got.Status)
}

// TestVMStopHandler_NotFound verifies that stopping a non-existent session
// returns an error response rather than panicking or returning OK.
func TestVMStopHandler_NotFound(t *testing.T) {
	st := openTestStoreForVM(t)
	_, sockPath := startVMTestServer(t, st)

	resp := sendVM(t, sockPath, "VMStop", map[string]any{"session_id": "nonexistent"})
	assert.False(t, resp.OK)
	assert.NotEmpty(t, resp.Error)
}

// TestVMStatusHandler_Stub verifies that VMStatus with no session_id returns
// a null payload when no active session exists, and that it returns a session
// once one is started.
func TestVMStatusHandler_Stub(t *testing.T) {
	writeLauncherProfile(t, "/images/base.qcow2")
	st := openTestStoreForVM(t)
	_, sockPath := startVMTestServer(t, st)

	// No active session — payload should be null.
	noActiveResp := sendVM(t, sockPath, "VMStatus", map[string]any{})
	require.True(t, noActiveResp.OK, "expected OK: %s", noActiveResp.Error)
	assert.Equal(t, "null", string(noActiveResp.Payload))

	// Start one — now VMStatus should return it.
	startResp := sendVM(t, sockPath, "VMStart", map[string]any{
		"disk_image_path": "/images/base.qcow2",
	})
	require.True(t, startResp.OK)

	activeResp := sendVM(t, sockPath, "VMStatus", map[string]any{})
	require.True(t, activeResp.OK)

	var got vm.Session
	require.NoError(t, json.Unmarshal(activeResp.Payload, &got))
	assert.Equal(t, vm.StateBooting, got.Status)
}

// TestVMStartHandler_ProfileMissing verifies that VMStart returns
// ERR_PROFILE_MISSING when the LauncherProfile JSON is absent from disk.
func TestVMStartHandler_ProfileMissing(t *testing.T) {
	// Point XDG_CONFIG_HOME at an empty directory so no settings.json exists.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	st := openTestStoreForVM(t)
	_, sockPath := startVMTestServer(t, st)

	resp := sendVM(t, sockPath, "VMStart", map[string]any{
		"name": "test-vm",
	})
	assert.False(t, resp.OK)
	assert.Contains(t, resp.Error, vm.ErrProfileMissing)
}

// TestVMStartHandler_Real verifies that VMStart reads the LauncherProfile,
// merges fields into a StartSpec, inserts a session, and returns it.
func TestVMStartHandler_Real(t *testing.T) {
	writeLauncherProfile(t, "/images/sigil-vm.img")
	st := openTestStoreForVM(t)
	_, sockPath := startVMTestServer(t, st)

	resp := sendVM(t, sockPath, "VMStart", map[string]any{
		"name":        "my-workspace",
		"policy_id":   "",
		"egress_tier": "",
	})
	require.True(t, resp.OK, "expected OK, got error: %s", resp.Error)

	var sess vm.Session
	require.NoError(t, json.Unmarshal(resp.Payload, &sess))
	assert.NotEmpty(t, sess.ID)
	assert.Equal(t, vm.StateBooting, sess.Status)
	// Policy status must be not_applicable when no policy_id is given.
	assert.Equal(t, string(vm.PolicyStatusNotApplicable), sess.PolicyStatus)
}

// TestVMListHandler_RealGolden pins the FR-020 invariant: ledger_events_total
// is an integer scalar in the VMList response — no per-event breakdown under
// that field. The golden file at testdata/vm_list_response.json documents the
// authoritative response shape.
func TestVMListHandler_RealGolden(t *testing.T) {
	writeLauncherProfile(t, "/images/base.qcow2")
	st := openTestStoreForVM(t)
	_, sockPath := startVMTestServer(t, st)

	// Seed a session.
	startResp := sendVM(t, sockPath, "VMStart", map[string]any{
		"disk_image_path": "/images/base.qcow2",
	})
	require.True(t, startResp.OK, "start: %s", startResp.Error)

	listResp := sendVM(t, sockPath, "VMList", map[string]any{"limit": 10})
	require.True(t, listResp.OK, "list: %s", listResp.Error)

	var sessions []map[string]any
	require.NoError(t, json.Unmarshal(listResp.Payload, &sessions))
	require.NotEmpty(t, sessions, "expected at least one session in list response")

	for _, sess := range sessions {
		// FR-020: ledger_events_total MUST be a number (JSON number → float64 in
		// interface{}) not an array or object. Any nested structure is a protocol
		// violation that would expose raw event records to the Kenaz client.
		val, ok := sess["ledger_events_total"]
		require.True(t, ok, "ledger_events_total field must be present")
		_, isNumber := val.(float64)
		assert.True(t, isNumber,
			"FR-020: ledger_events_total must be an integer scalar, got %T (%v)",
			val, val)
	}
}

// TestVMMergeHandler_Stub verifies that VMMerge rejects a session not in
// stopping/stopped state. No hypervisor or real SQLite merge is needed to
// exercise the precondition guard.
func TestVMMergeHandler_Stub(t *testing.T) {
	writeLauncherProfile(t, "/images/base.qcow2")
	st := openTestStoreForVM(t)
	_, sockPath := startVMTestServer(t, st)

	startResp := sendVM(t, sockPath, "VMStart", map[string]any{
		"disk_image_path": "/images/base.qcow2",
	})
	require.True(t, startResp.OK)

	var sess vm.Session
	require.NoError(t, json.Unmarshal(startResp.Payload, &sess))

	// Attempt merge while the session is still booting — must be rejected.
	mergeResp := sendVM(t, sockPath, "VMMerge", map[string]any{"session_id": sess.ID})
	assert.False(t, mergeResp.OK, "merge of a booting session should be rejected")
	assert.Contains(t, mergeResp.Error, "merge_precondition_failed")
}
