//go:build darwin

package vmdriver

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/sigil-tech/sigil/internal/vm"
)

// Protocol constants — keep in sync with sigil-launcher-macos/Sources/sigild-vz.
// See ADR-028a §2, §3 for the wire contract.
const (
	// supportedVZProtocol is the only JSON-line protocol version vmdriver_darwin
	// understands. Any mismatch is treated as a fatal launch error so a partial
	// update (new sigild + old sigild-vz, or vice versa) fails loudly.
	supportedVZProtocol = 1

	// handshakeTimeout bounds how long Start will wait for sigild-vz to emit
	// its startup `{"vz_version":..,"protocol":..}` line.
	handshakeTimeout = 5 * time.Second

	// commandTimeout is the default request/response deadline for id-bearing
	// commands (start/stop/status/health/close). Subscribe has no timeout; its
	// ack is the only thing bounded by this value.
	commandTimeout = 30 * time.Second

	// closeGrace is how long Close waits for sigild-vz to exit cleanly after
	// receiving the `close` command before escalating to SIGTERM.
	closeGrace = 10 * time.Second

	// termGrace is how long Close waits after SIGTERM before escalating to
	// SIGKILL.
	termGrace = 2 * time.Second

	// darwinRingBufferSize bounds captured stderr. Matches the Linux driver.
	darwinRingBufferSize = 1 << 20 // 1 MiB
)

// envVZBinary overrides the discovered sigild-vz binary path. The test suite
// points this at a fake helper (see fake_sigild_vz_test.go) so the protocol
// path is exercised without a real VZ process.
const envVZBinary = "SIGILD_VZ_BINARY"

// envVZTestArch forces the architecture-detection code path during tests.
// Valid values: "intel", "arm64". Any other value falls back to the live
// sysctl probe. See ADR-028d.
const envVZTestArch = "SIGILD_VZ_TEST_ARCH"

// darwinArch captures the host architecture detection result. arm64 is the
// only supported target; intel triggers a graceful `ERR_IMAGE_MISSING`.
type darwinArch string

const (
	archARM64 darwinArch = "arm64"
	archIntel darwinArch = "intel"
)

// darwinSession owns a single sigild-vz subprocess and its I/O goroutines.
// All mutable fields are guarded by the explicit locks documented per field.
type darwinSession struct {
	id     vm.SessionID
	spec   vm.StartSpec
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr *ringBuffer

	// writeMu serialises stdin writes. The JSON-line protocol requires each
	// request to reach sigild-vz as a single `\n`-terminated line; without
	// this lock, two goroutines could interleave mid-message.
	writeMu sync.Mutex

	// pendingMu guards pending; the reader goroutine delivers responses by
	// matching the request id.
	pendingMu sync.Mutex
	pending   map[string]chan vzResponse

	// statsMu guards statsCh. nil means no active Subscribe. When the reader
	// goroutine observes a `hypervisor_exit` event or EOF on stdout, it closes
	// statsCh (FR-009 lifecycle contract).
	statsMu sync.Mutex
	statsCh chan vm.StatSnapshot

	// waitDone is closed when the cmd.Wait goroutine returns (process fully
	// exited + reaped). Subscribe, Close, and Status all observe this to
	// detect abnormal exits.
	waitDone chan struct{}

	// readerDone is closed when the stdout reader goroutine exits (EOF or
	// parse fatal). Used by Close to confirm ordered shutdown.
	readerDone chan struct{}
}

// vzRequest is the Go → Swift wire shape for id-bearing commands.
type vzRequest struct {
	ID     string `json:"id"`
	Cmd    string `json:"cmd"`
	Params any    `json:"params,omitempty"`
}

// vzResponse is the Swift → Go wire shape for command responses.
type vzResponse struct {
	ID     string          `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *vzError        `json:"error,omitempty"`
}

// vzPush is the Swift → Go wire shape for unsolicited events.
type vzPush struct {
	Event   string          `json:"event"`
	Payload json.RawMessage `json:"payload"`
}

// vzError is the structured error payload carried on `{"ok":false}` responses.
type vzError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// vzHandshake is the first line sigild-vz writes on startup.
type vzHandshake struct {
	VZVersion string `json:"vz_version"`
	Protocol  int    `json:"protocol"`
}

// vzStatPayload is the payload of a "stat" push event.
type vzStatPayload struct {
	Timestamp  string  `json:"ts"`
	CPUPercent float64 `json:"cpu_percent"`
	CPUCores   uint8   `json:"cpu_cores"`
	MemUsedMB  uint64  `json:"mem_used_mb"`
	MemAllocMB uint64  `json:"mem_alloc_mb"`
}

// vzStartParams is the Go-constructed Params for a "start" command.
type vzStartParams struct {
	Name            string   `json:"name"`
	ImagePath       string   `json:"image_path"`
	OverlayPath     string   `json:"overlay_path"`
	MemoryMB        uint64   `json:"memory_mb"`
	CPUCount        uint8    `json:"cpu_count"`
	Editor          string   `json:"editor,omitempty"`
	Shell           string   `json:"shell,omitempty"`
	ContainerEngine string   `json:"container_engine,omitempty"`
	WorkbenchApps   []string `json:"workbench_apps,omitempty"`
}

// vzStartResult is the response.result body of a successful "start" command.
type vzStartResult struct {
	SessionID string `json:"session_id"`
	PID       int    `json:"pid"`
}

// vzStatusResult is the response.result body of a "status" command.
type vzStatusResult struct {
	State     string `json:"state"`
	StartedAt string `json:"started_at,omitempty"`
	EndedAt   string `json:"ended_at,omitempty"`
	PID       int    `json:"pid,omitempty"`
}

// darwinDriver implements vm.Driver on macOS by managing one sigild-vz
// subprocess per session. The driver itself is stateless beyond the session
// map; all lifecycle logic lives in darwinSession.
type darwinDriver struct {
	binaryPath string
	arch       darwinArch

	mu       sync.Mutex
	sessions map[vm.SessionID]*darwinSession
	closed   bool
}

// New returns a macOS vm.Driver backed by the sigild-vz subprocess helper.
// It locates sigild-vz once at construction time and caches the host arch
// detection so per-Start calls are cheap.
//
// Locator precedence (first hit wins):
//  1. $SIGILD_VZ_BINARY (test seam and deployment override).
//  2. Sibling of os.Args[0] (ADR-028a §5 — the app bundle layout).
//
// Returns a vm.VMError with Code=ErrHypervisorUnavailable if the binary is
// not found at either location.
func New() (vm.Driver, error) {
	binPath, err := locateVZBinary()
	if err != nil {
		return nil, &vm.VMError{
			Code:    vm.ErrHypervisorUnavailable,
			Message: fmt.Sprintf("sigild-vz not found: %v", err),
		}
	}

	arch := detectDarwinArch()

	return &darwinDriver{
		binaryPath: binPath,
		arch:       arch,
		sessions:   make(map[vm.SessionID]*darwinSession),
	}, nil
}

// Start spawns sigild-vz, verifies the handshake, and issues the "start"
// command with the merged StartSpec. On Intel hosts it short-circuits with
// ErrImageMissing per ADR-028d (only arm64 sigil-os images are built).
func (d *darwinDriver) Start(ctx context.Context, spec vm.StartSpec) (vm.SessionID, error) {
	if err := d.checkClosed(); err != nil {
		return "", err
	}

	if d.arch == archIntel {
		return "", &vm.VMError{
			Code: vm.ErrImageMissing,
			Message: "Apple Silicon required: sigil-os images are arm64-only. " +
				"Intel Macs are out of scope for the MVP (see ADR-028d).",
		}
	}

	sess, err := d.spawnSession(spec)
	if err != nil {
		return "", err
	}

	params := vzStartParams{
		Name:            spec.Name,
		ImagePath:       spec.ImagePath,
		OverlayPath:     spec.OverlayPath,
		MemoryMB:        spec.MemoryMB,
		CPUCount:        spec.CPUCount,
		Editor:          spec.Editor,
		Shell:           spec.Shell,
		ContainerEngine: spec.ContainerEngine,
		WorkbenchApps:   spec.WorkbenchApps,
	}

	raw, err := sess.call(ctx, "start", params, commandTimeout)
	if err != nil {
		sess.terminate()
		return "", fmt.Errorf("vmdriver darwin: start: %w", err)
	}

	var result vzStartResult
	if err := json.Unmarshal(raw, &result); err != nil {
		sess.terminate()
		return "", fmt.Errorf("vmdriver darwin: parse start result: %w", err)
	}

	id := vm.SessionID(result.SessionID)
	if id == "" {
		// sigild-vz did not supply an id — fall back to the overlay base name
		// to keep the session addressable.
		id = vm.SessionID(filepath.Base(spec.OverlayPath))
	}
	sess.id = id

	d.mu.Lock()
	d.sessions[id] = sess
	d.mu.Unlock()

	return id, nil
}

// Stop issues the "stop" command over the subprocess and, when it returns,
// tears down the subprocess. It does not rely on the subprocess to exit on
// its own — a hung VZ machine must still surrender control to sigild.
func (d *darwinDriver) Stop(ctx context.Context, id vm.SessionID) error {
	if err := d.checkClosed(); err != nil {
		return err
	}

	sess, ok := d.session(id)
	if !ok {
		return &vm.VMError{Code: vm.ErrSessionNotFound, Message: string(id)}
	}

	_, err := sess.call(ctx, "stop", nil, commandTimeout)
	sess.terminate()

	d.mu.Lock()
	delete(d.sessions, id)
	d.mu.Unlock()

	if err != nil {
		return fmt.Errorf("vmdriver darwin: stop: %w", err)
	}
	return nil
}

// Status returns the VZ lifecycle state via a "status" command.
func (d *darwinDriver) Status(ctx context.Context, id vm.SessionID) (vm.Snapshot, error) {
	if err := d.checkClosed(); err != nil {
		return vm.Snapshot{}, err
	}

	sess, ok := d.session(id)
	if !ok {
		return vm.Snapshot{}, &vm.VMError{Code: vm.ErrSessionNotFound, Message: string(id)}
	}

	raw, err := sess.call(ctx, "status", nil, commandTimeout)
	if err != nil {
		return vm.Snapshot{}, fmt.Errorf("vmdriver darwin: status: %w", err)
	}

	var result vzStatusResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return vm.Snapshot{}, fmt.Errorf("vmdriver darwin: parse status result: %w", err)
	}

	snap := vm.Snapshot{
		State: mapVZState(result.State),
		PID:   result.PID,
	}
	if result.StartedAt != "" {
		if t, perr := time.Parse(time.RFC3339, result.StartedAt); perr == nil {
			snap.StartedAt = t.UTC()
		}
	}
	if result.EndedAt != "" {
		if t, perr := time.Parse(time.RFC3339, result.EndedAt); perr == nil {
			snap.EndedAt = t.UTC()
		}
	}
	// VZ owns the CID internally; surface 0 to callers per driver.go comment.
	snap.VsockCID = 0

	return snap, nil
}

// Subscribe asks sigild-vz to begin streaming stat push events and returns
// a channel that carries those events. The channel is closed on one of:
//   - ctx cancellation (caller-initiated teardown),
//   - a "hypervisor_exit" push event,
//   - subprocess EOF (crash or external kill).
//
// Only one active subscription per session is supported; a second call
// returns an error rather than multiplex. Re-subscription after the first
// channel closes is permitted.
func (d *darwinDriver) Subscribe(ctx context.Context, id vm.SessionID) (<-chan vm.StatSnapshot, error) {
	if err := d.checkClosed(); err != nil {
		return nil, err
	}

	sess, ok := d.session(id)
	if !ok {
		return nil, &vm.VMError{Code: vm.ErrSessionNotFound, Message: string(id)}
	}

	sess.statsMu.Lock()
	if sess.statsCh != nil {
		sess.statsMu.Unlock()
		return nil, fmt.Errorf("vmdriver darwin: subscription already active for %s", id)
	}
	ch := make(chan vm.StatSnapshot, 8)
	sess.statsCh = ch
	sess.statsMu.Unlock()

	if _, err := sess.call(ctx, "subscribe-stats", nil, commandTimeout); err != nil {
		sess.statsMu.Lock()
		sess.statsCh = nil
		close(ch)
		sess.statsMu.Unlock()
		return nil, fmt.Errorf("vmdriver darwin: subscribe-stats: %w", err)
	}

	// Bridge ctx cancellation to channel close. The reader goroutine also
	// closes statsCh on hypervisor_exit / EOF; whichever fires first wins
	// via statsMu below.
	go func() {
		select {
		case <-ctx.Done():
			sess.closeStatsCh()
		case <-sess.waitDone:
			// reader goroutine will close statsCh on EOF; nothing to do.
		}
	}()

	return ch, nil
}

// Health sends a cheap "health" probe. It does not inspect VZ state beyond
// confirming sigild-vz is responsive within the short command timeout.
func (d *darwinDriver) Health(ctx context.Context, id vm.SessionID) error {
	if err := d.checkClosed(); err != nil {
		return err
	}

	sess, ok := d.session(id)
	if !ok {
		return &vm.VMError{Code: vm.ErrSessionNotFound, Message: string(id)}
	}

	if _, err := sess.call(ctx, "health", nil, 5*time.Second); err != nil {
		return fmt.Errorf("vmdriver darwin: health: %w", err)
	}
	return nil
}

// Close terminates every tracked subprocess in the canonical close →
// SIGTERM → SIGKILL ladder. Safe to call once; subsequent calls return nil.
func (d *darwinDriver) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	sessions := make([]*darwinSession, 0, len(d.sessions))
	for _, sess := range d.sessions {
		sessions = append(sessions, sess)
	}
	d.sessions = make(map[vm.SessionID]*darwinSession)
	d.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), closeGrace+termGrace+5*time.Second)
	defer cancel()

	for _, sess := range sessions {
		// Best-effort graceful close command; ignore errors (process may be
		// already dead or unresponsive — the terminate fallback handles it).
		_, _ = sess.call(ctx, "close", nil, closeGrace)
		sess.terminate()
	}
	return nil
}

// ─── internal helpers ────────────────────────────────────────────────────────

func (d *darwinDriver) checkClosed() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return errors.New("vmdriver darwin: driver is closed")
	}
	return nil
}

func (d *darwinDriver) session(id vm.SessionID) (*darwinSession, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	s, ok := d.sessions[id]
	return s, ok
}

// spawnSession forks sigild-vz, consumes the startup handshake, and wires
// up the stdout reader + wait goroutines. The returned session is ready to
// receive `call`s but has no "start" command issued yet.
func (d *darwinDriver) spawnSession(spec vm.StartSpec) (*darwinSession, error) {
	cmd := exec.Command(d.binaryPath)
	cmd.Env = os.Environ()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("vmdriver darwin: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("vmdriver darwin: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("vmdriver darwin: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("vmdriver darwin: launch sigild-vz: %w", err)
	}

	ring := newRingBuffer(darwinRingBufferSize)
	go drainTo(stderr, ring)

	sess := &darwinSession{
		spec:       spec,
		cmd:        cmd,
		stdin:      stdin,
		stderr:     ring,
		pending:    make(map[string]chan vzResponse),
		waitDone:   make(chan struct{}),
		readerDone: make(chan struct{}),
	}

	reader := bufio.NewReader(stdout)

	// Read the handshake before spawning the long-running reader goroutine
	// so a bad handshake fails fast without any backlog to drain.
	if err := readHandshake(reader, handshakeTimeout, cmd); err != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		return nil, err
	}

	// Wait goroutine: reaps the subprocess and signals teardown.
	go func() {
		_ = cmd.Wait()
		close(sess.waitDone)
		sess.closeStatsCh()
	}()

	// Reader goroutine: dispatches response lines and push events.
	go sess.readerLoop(reader)

	return sess, nil
}

// readHandshake reads the first line from reader within timeout and verifies
// it matches the supported protocol version. Any parse failure or mismatch
// is a fatal launch error.
func readHandshake(r *bufio.Reader, timeout time.Duration, cmd *exec.Cmd) error {
	type lineResult struct {
		line []byte
		err  error
	}
	ch := make(chan lineResult, 1)
	go func() {
		l, err := r.ReadBytes('\n')
		ch <- lineResult{line: l, err: err}
	}()

	select {
	case res := <-ch:
		if res.err != nil && len(res.line) == 0 {
			return fmt.Errorf("vmdriver darwin: handshake read: %w", res.err)
		}
		var hs vzHandshake
		if err := json.Unmarshal(res.line, &hs); err != nil {
			return fmt.Errorf("vmdriver darwin: handshake parse: %w (line=%q)", err, res.line)
		}
		if hs.Protocol != supportedVZProtocol {
			return fmt.Errorf("vmdriver darwin: protocol version mismatch: sigild-vz=%d, sigild=%d (check that both binaries shipped together)",
				hs.Protocol, supportedVZProtocol)
		}
		slog.Debug("vmdriver darwin: handshake ok",
			"vz_version", hs.VZVersion, "protocol", hs.Protocol)
		return nil
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		return fmt.Errorf("vmdriver darwin: handshake timeout after %s", timeout)
	}
}

// call writes a single request line, waits for the matching response on
// pending[id], and returns the raw result. Returns an error if the response
// carries ok:false, the subprocess exits, or ctx/timeout fires first.
func (s *darwinSession) call(ctx context.Context, cmd string, params any, timeout time.Duration) (json.RawMessage, error) {
	id := newRequestID()
	reqCh := make(chan vzResponse, 1)

	s.pendingMu.Lock()
	s.pending[id] = reqCh
	s.pendingMu.Unlock()

	defer func() {
		s.pendingMu.Lock()
		delete(s.pending, id)
		s.pendingMu.Unlock()
	}()

	req := vzRequest{ID: id, Cmd: cmd, Params: params}
	line, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	line = append(line, '\n')

	s.writeMu.Lock()
	_, werr := s.stdin.Write(line)
	s.writeMu.Unlock()
	if werr != nil {
		return nil, fmt.Errorf("write request: %w", werr)
	}

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case resp := <-reqCh:
		if !resp.OK {
			if resp.Error != nil {
				return nil, &vm.VMError{Code: resp.Error.Code, Message: resp.Error.Message}
			}
			return nil, errors.New("vmdriver darwin: response ok=false with no error body")
		}
		return resp.Result, nil
	case <-callCtx.Done():
		return nil, fmt.Errorf("command %q: %w", cmd, callCtx.Err())
	case <-s.waitDone:
		tail := s.stderr.String()
		if len(tail) > 512 {
			tail = tail[len(tail)-512:]
		}
		return nil, fmt.Errorf("vmdriver darwin: sigild-vz exited before responding to %q (stderr tail: %s)", cmd, tail)
	}
}

// readerLoop is the single consumer of sigild-vz's stdout. It decodes each
// JSON line into either a response (id set) or a push event (event set) and
// dispatches accordingly. Exits on EOF or unrecoverable parse error.
func (s *darwinSession) readerLoop(r *bufio.Reader) {
	defer close(s.readerDone)
	defer s.closeStatsCh()

	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			s.dispatchLine(line)
		}
		if err != nil {
			if err != io.EOF {
				slog.Debug("vmdriver darwin: stdout read error", "err", err)
			}
			return
		}
	}
}

// dispatchLine classifies a single JSON line and hands it to the appropriate
// handler. Unknown lines are logged at DEBUG and dropped rather than fatal,
// so a sigild-vz version that adds new event kinds does not wedge the driver.
func (s *darwinSession) dispatchLine(line []byte) {
	// Peek at the first object keys without double-parsing the entire body.
	var probe struct {
		ID    string `json:"id"`
		Event string `json:"event"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		slog.Debug("vmdriver darwin: malformed stdout line", "err", err, "line", string(line))
		return
	}

	switch {
	case probe.ID != "":
		var resp vzResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			slog.Debug("vmdriver darwin: malformed response", "err", err, "line", string(line))
			return
		}
		s.pendingMu.Lock()
		ch, ok := s.pending[resp.ID]
		s.pendingMu.Unlock()
		if !ok {
			slog.Debug("vmdriver darwin: orphan response", "id", resp.ID)
			return
		}
		select {
		case ch <- resp:
		default:
			// The caller may have already timed out; drop silently.
		}

	case probe.Event == "stat":
		var push vzPush
		if err := json.Unmarshal(line, &push); err != nil {
			return
		}
		var payload vzStatPayload
		if err := json.Unmarshal(push.Payload, &payload); err != nil {
			return
		}
		s.emitStat(payload)

	case probe.Event == "hypervisor_exit":
		s.closeStatsCh()

	default:
		slog.Debug("vmdriver darwin: unknown stdout line", "line", string(line))
	}
}

// emitStat forwards a decoded stat payload on the subscribe channel. Drops
// the update if no subscription is active or the channel would block (the
// next tick supersedes a dropped one).
func (s *darwinSession) emitStat(p vzStatPayload) {
	s.statsMu.Lock()
	ch := s.statsCh
	s.statsMu.Unlock()
	if ch == nil {
		return
	}
	ts, err := time.Parse(time.RFC3339, p.Timestamp)
	if err != nil {
		ts = time.Now()
	}
	snap := vm.StatSnapshot{
		Timestamp:         ts.UTC(),
		CPUPercent:        p.CPUPercent,
		CPUCores:          p.CPUCores,
		MemoryUsedMB:      p.MemUsedMB,
		MemoryAllocatedMB: p.MemAllocMB,
	}
	select {
	case ch <- snap:
	default:
	}
}

// closeStatsCh closes the active subscribe channel (if any) exactly once.
// Called from multiple paths (ctx cancel, hypervisor_exit event, reader
// goroutine exit, Close); statsMu makes the close idempotent.
func (s *darwinSession) closeStatsCh() {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	if s.statsCh != nil {
		close(s.statsCh)
		s.statsCh = nil
	}
}

// terminate runs the close → SIGTERM → SIGKILL ladder against the subprocess.
// Idempotent: calling after the process has already exited is a no-op.
func (s *darwinSession) terminate() {
	if s.cmd.Process == nil {
		return
	}

	// Close stdin to signal sigild-vz to shut down cleanly.
	_ = s.stdin.Close()

	select {
	case <-s.waitDone:
		return
	case <-time.After(closeGrace):
	}

	_ = s.cmd.Process.Signal(syscall.SIGTERM)

	select {
	case <-s.waitDone:
		return
	case <-time.After(termGrace):
	}

	_ = s.cmd.Process.Signal(syscall.SIGKILL)
	<-s.waitDone
}

// newRequestID returns a 16-byte hex id (sufficient collision resistance for
// in-flight requests on a single session; the id space is per-session).
func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand should not fail on darwin; if it does, fall back to a
		// monotonic counter via time to avoid blocking the critical path.
		return fmt.Sprintf("t%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// locateVZBinary resolves the sigild-vz path from environment or sibling
// discovery. Returns a descriptive error if neither succeeds.
func locateVZBinary() (string, error) {
	if override := os.Getenv(envVZBinary); override != "" {
		if _, err := os.Stat(override); err != nil {
			return "", fmt.Errorf("%s=%s: %w", envVZBinary, override, err)
		}
		return override, nil
	}

	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate sigild executable: %w", err)
	}
	candidate := filepath.Join(filepath.Dir(exe), "sigild-vz")
	if _, err := os.Stat(candidate); err != nil {
		return "", fmt.Errorf("sigild-vz not at %s (override with %s): %w", candidate, envVZBinary, err)
	}
	return candidate, nil
}

// detectDarwinArch returns arm64 or intel, honouring the SIGILD_VZ_TEST_ARCH
// override for CI/test purposes. Runtime detection relies on
// runtime.GOARCH which is baked in at compile time — accurate for the normal
// single-arch build, and overridable for fat-binary or cross-arch scenarios.
func detectDarwinArch() darwinArch {
	if override := os.Getenv(envVZTestArch); override != "" {
		switch override {
		case "intel":
			return archIntel
		case "arm64":
			return archARM64
		}
	}
	if runtime.GOARCH == "arm64" {
		return archARM64
	}
	return archIntel
}

// mapVZState translates the Swift VZVirtualMachine state vocabulary into
// the canonical vm.LifecycleState. States not known to sigild map to
// StateFailed so an unexpected VZ state cannot be mistaken for Ready.
func mapVZState(s string) vm.LifecycleState {
	switch s {
	case "running":
		return vm.StateReady
	case "starting", "resuming":
		return vm.StateBooting
	case "pausing", "paused":
		return vm.StateBooting
	case "stopping":
		return vm.StateStopping
	case "stopped":
		return vm.StateStopped
	case "error":
		return vm.StateFailed
	default:
		return vm.StateFailed
	}
}
