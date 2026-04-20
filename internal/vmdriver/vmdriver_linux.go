//go:build linux

package vmdriver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/sigil-tech/sigil/internal/vm"
)

// ringBufferSize is the maximum number of bytes kept from QEMU stderr.
const ringBufferSize = 1 << 20 // 1 MiB

// defaultMemoryMB is used when StartSpec.MemoryMB is 0.
const defaultMemoryMB = 4096

// defaultCPUCount is used when StartSpec.CPUCount is 0.
const defaultCPUCount uint8 = 4

// qmpConnectTimeout is how long Start waits for the QMP socket after forking.
const qmpConnectTimeout = 10 * time.Second

// subscribeInterval is the polling rate for 1 Hz stat updates.
const subscribeInterval = time.Second

// linuxSession holds per-session runtime state for a running QEMU process.
type linuxSession struct {
	spec        vm.StartSpec
	cmd         *exec.Cmd
	qmpSocket   string
	overlayPath string
	subCtx      context.Context    // cancelled when subscription should stop
	subCancel   context.CancelFunc // cancels subCtx
	stderrBuf   *ringBuffer
	waitDone    chan struct{} // closed when cmd.Wait returns
}

// linuxDriver implements vm.Driver for Linux via direct qemu-system subprocess.
type linuxDriver struct {
	mu       sync.Mutex
	sessions map[vm.SessionID]*linuxSession
	qemuBin  string
	useKVM   bool
	closed   bool
}

// New probes the host for KVM availability and locates the appropriate
// qemu-system binary. Returns an error wrapping vm.ErrHypervisorUnavailable
// if QEMU is not found.
func New() (vm.Driver, error) {
	arch := runtime.GOARCH
	var binary string
	switch arch {
	case "amd64":
		binary = "qemu-system-x86_64"
	case "arm64":
		binary = "qemu-system-aarch64"
	default:
		binary = "qemu-system-" + arch
	}

	qemuPath, err := exec.LookPath(binary)
	if err != nil {
		return nil, &vm.VMError{
			Code:    vm.ErrHypervisorUnavailable,
			Message: fmt.Sprintf("%s not found in PATH: %v", binary, err),
		}
	}

	useKVM := false
	if f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0); err == nil {
		f.Close()
		useKVM = true
	} else {
		slog.Warn("vmdriver: /dev/kvm not accessible; falling back to TCG software emulation — expect 5–20x slower VM boot and execution",
			"err", err)
	}

	return &linuxDriver{
		sessions: make(map[vm.SessionID]*linuxSession),
		qemuBin:  qemuPath,
		useKVM:   useKVM,
	}, nil
}

// Start creates a QCOW2 overlay, forks QEMU with QMP + vsock + virtio-fs, and
// connects to QMP for the initial capability handshake. Returns the SessionID
// derived from the overlay filename base.
func (d *linuxDriver) Start(ctx context.Context, spec vm.StartSpec) (vm.SessionID, error) {
	if err := d.checkClosed(); err != nil {
		return "", err
	}

	memMB := spec.MemoryMB
	if memMB == 0 {
		memMB = defaultMemoryMB
	}
	cpuCount := spec.CPUCount
	if cpuCount == 0 {
		cpuCount = defaultCPUCount
	}

	// Create QCOW2 overlay backed by the base image.
	if err := createOverlay(spec.ImagePath, spec.OverlayPath); err != nil {
		return "", fmt.Errorf("vmdriver: create overlay: %w", err)
	}

	// Derive SessionID from the overlay base name.
	id := vm.SessionID(filepath.Base(spec.OverlayPath))

	qmpSocket := spec.OverlayPath + ".qmp.sock"

	args := d.buildQEMUArgs(spec, memMB, cpuCount, qmpSocket)

	cmd := exec.Command(d.qemuBin, args...)

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		os.Remove(spec.OverlayPath)
		return "", fmt.Errorf("vmdriver: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		os.Remove(spec.OverlayPath)
		return "", fmt.Errorf("vmdriver: start qemu: %w", err)
	}

	buf := newRingBuffer(ringBufferSize)
	waitDone := make(chan struct{})

	// Stderr drain goroutine — reads until EOF (pipe closes on process exit).
	go func() {
		drainTo(stderrPipe, buf)
	}()

	// Wait goroutine — closes waitDone when the process exits, which signals
	// all Subscribe goroutines to exit cleanly (FR-009 lifecycle contract).
	go func() {
		defer close(waitDone)
		cmd.Wait() //nolint:errcheck
	}()

	// Connect to QMP with timeout.
	qmpCtx, qmpCancel := context.WithTimeout(ctx, qmpConnectTimeout)
	defer qmpCancel()

	qmpClient, err := Dial(qmpCtx, qmpSocket)
	if err != nil {
		// QEMU is running but QMP handshake failed — kill and clean up.
		cmd.Process.Kill()
		<-waitDone
		os.Remove(spec.OverlayPath)
		os.Remove(qmpSocket)
		return "", fmt.Errorf("vmdriver: qmp dial: %w", err)
	}
	// Close the QMP client; per-operation callers open a fresh connection.
	qmpClient.Close()

	subCtx, subCancel := context.WithCancel(context.Background())

	sess := &linuxSession{
		spec:        spec,
		cmd:         cmd,
		qmpSocket:   qmpSocket,
		overlayPath: spec.OverlayPath,
		subCtx:      subCtx,
		subCancel:   subCancel,
		stderrBuf:   buf,
		waitDone:    waitDone,
	}

	d.mu.Lock()
	d.sessions[id] = sess
	d.mu.Unlock()

	// Background goroutine that cancels subscriptions when QEMU exits.
	go func() {
		select {
		case <-waitDone:
			subCancel()
		case <-subCtx.Done():
		}
	}()

	return id, nil
}

// Stop performs graceful shutdown: QMP system_powerdown → wait 10 s → QMP quit
// + SIGTERM → wait 2 s → SIGKILL. Overlay and QMP socket are always removed.
func (d *linuxDriver) Stop(ctx context.Context, id vm.SessionID) error {
	if err := d.checkClosed(); err != nil {
		return err
	}

	d.mu.Lock()
	sess, ok := d.sessions[id]
	if !ok {
		d.mu.Unlock()
		return &vm.VMError{Code: vm.ErrSessionNotFound, Message: string(id)}
	}
	d.mu.Unlock()

	defer func() {
		os.Remove(sess.qmpSocket)
		os.Remove(sess.overlayPath)
		sess.subCancel()
		d.mu.Lock()
		delete(d.sessions, id)
		d.mu.Unlock()
	}()

	d.stopProcessGracefully(ctx, sess)
	return nil
}

// Status returns a Snapshot derived from QMP query-status.
func (d *linuxDriver) Status(ctx context.Context, id vm.SessionID) (vm.Snapshot, error) {
	if err := d.checkClosed(); err != nil {
		return vm.Snapshot{}, err
	}

	d.mu.Lock()
	sess, ok := d.sessions[id]
	d.mu.Unlock()
	if !ok {
		return vm.Snapshot{}, &vm.VMError{Code: vm.ErrSessionNotFound, Message: string(id)}
	}

	state, err := d.queryStatus(ctx, sess)
	if err != nil {
		return vm.Snapshot{}, err
	}

	var pid int
	if sess.cmd.Process != nil {
		pid = sess.cmd.Process.Pid
	}

	return vm.Snapshot{
		State:    state,
		VsockCID: sess.spec.VsockCID,
		PID:      pid,
	}, nil
}

// Subscribe returns a channel receiving StatSnapshot at ~1 Hz via qom-get
// polling. The channel is closed when ctx is cancelled or the QEMU process
// exits (FR-009 lifecycle contract).
func (d *linuxDriver) Subscribe(ctx context.Context, id vm.SessionID) (<-chan vm.StatSnapshot, error) {
	if err := d.checkClosed(); err != nil {
		return nil, err
	}

	d.mu.Lock()
	sess, ok := d.sessions[id]
	d.mu.Unlock()
	if !ok {
		return nil, &vm.VMError{Code: vm.ErrSessionNotFound, Message: string(id)}
	}

	ch := make(chan vm.StatSnapshot, 4)

	go func() {
		defer close(ch)
		ticker := time.NewTicker(subscribeInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-sess.subCtx.Done():
				// Process exited or Stop was called.
				return
			case <-sess.waitDone:
				// Process exited — close channel per FR-009.
				return
			case <-ticker.C:
				snap := d.pollStats(ctx, sess)
				snap.Timestamp = time.Now()
				select {
				case ch <- snap:
				case <-ctx.Done():
					return
				case <-sess.subCtx.Done():
					return
				case <-sess.waitDone:
					return
				}
			}
		}
	}()

	return ch, nil
}

// Health pings QMP with query-status and returns nil if the VM responds and is
// not in a terminal state.
func (d *linuxDriver) Health(ctx context.Context, id vm.SessionID) error {
	if err := d.checkClosed(); err != nil {
		return err
	}

	d.mu.Lock()
	sess, ok := d.sessions[id]
	d.mu.Unlock()
	if !ok {
		return &vm.VMError{Code: vm.ErrSessionNotFound, Message: string(id)}
	}

	state, err := d.queryStatus(ctx, sess)
	if err != nil {
		return fmt.Errorf("vmdriver: health check: %w", err)
	}
	if state == vm.StateFailed || state == vm.StateStopped {
		return fmt.Errorf("vmdriver: health check: VM is in terminal state %s", state)
	}
	return nil
}

// Close terminates all tracked QEMU processes and waits for goroutines to exit.
func (d *linuxDriver) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	sessions := make(map[vm.SessionID]*linuxSession, len(d.sessions))
	for k, v := range d.sessions {
		sessions[k] = v
	}
	d.sessions = make(map[vm.SessionID]*linuxSession)
	d.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	for id, sess := range sessions {
		// Cancel subscriptions first so goroutines unblock.
		sess.subCancel()

		d.stopProcessGracefully(ctx, sess)

		os.Remove(sess.qmpSocket)
		os.Remove(sess.overlayPath)

		slog.Debug("vmdriver: Close: session terminated", "id", id)
	}

	return nil
}

// ─── internal helpers ─────────────────────────────────────────────────────────

func (d *linuxDriver) checkClosed() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return fmt.Errorf("vmdriver: driver is closed")
	}
	return nil
}

func (d *linuxDriver) buildQEMUArgs(spec vm.StartSpec, memMB uint64, cpuCount uint8, qmpSocket string) []string {
	args := []string{
		"-nographic",
		"-serial", "none",
		"-monitor", "none",
	}

	if d.useKVM {
		args = append(args, "-machine", "q35,accel=kvm", "-cpu", "host")
	} else {
		args = append(args, "-machine", "q35,accel=tcg", "-cpu", "qemu64")
	}

	args = append(args,
		"-m", strconv.FormatUint(memMB, 10),
		"-smp", strconv.Itoa(int(cpuCount)),
		"-drive", "file="+spec.OverlayPath+",format=qcow2,if=virtio",
		"-netdev", "user,id=net0",
		"-device", "virtio-net-pci,netdev=net0",
		"-device", fmt.Sprintf("vhost-vsock-pci,guest-cid=%d", spec.VsockCID),
		"-qmp", "unix:"+qmpSocket+",server,nowait",
	)

	// virtio-fs shared directory. Only add if a shared directory is configured.
	if spec.ImagePath != "" {
		sharedDir := filepath.Dir(spec.ImagePath)
		args = append(args,
			"-virtfs", "local,path="+sharedDir+",mount_tag=sigil,security_model=none",
		)
	}

	return args
}

func createOverlay(base, overlay string) error {
	cmd := exec.Command("qemu-img", "create",
		"-b", base,
		"-F", "qcow2",
		"-f", "qcow2",
		overlay,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("qemu-img create: %w — %s", err, stderr.String())
	}
	return nil
}

// qmpExecute dials a fresh QMP connection, sends a single command, and closes.
func (d *linuxDriver) qmpExecute(ctx context.Context, sess *linuxSession, cmd string, args map[string]any) (json.RawMessage, error) {
	qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	c, err := Dial(qctx, sess.qmpSocket)
	if err != nil {
		return nil, fmt.Errorf("qmp dial: %w", err)
	}
	defer c.Close()

	return c.Execute(qctx, cmd, args)
}

func (d *linuxDriver) queryStatus(ctx context.Context, sess *linuxSession) (vm.LifecycleState, error) {
	raw, err := d.qmpExecute(ctx, sess, "query-status", nil)
	if err != nil {
		return vm.StateFailed, err
	}

	var result struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return vm.StateFailed, fmt.Errorf("vmdriver: parse query-status: %w", err)
	}

	return mapQEMUStatus(result.Status), nil
}

// mapQEMUStatus maps QEMU RunState strings to vm.LifecycleState.
func mapQEMUStatus(s string) vm.LifecycleState {
	switch s {
	case "running":
		return vm.StateReady
	case "paused", "prelaunch", "finish-migrate", "restore-vm", "save-vm", "suspended":
		return vm.StateBooting
	case "shutdown", "guest-panicked", "internal-error", "io-error":
		return vm.StateFailed
	default:
		return vm.StateFailed
	}
}

// pollStats collects a StatSnapshot via qom-get balloon. QEMU returns int64;
// we clamp negatives to 0 and cast to uint64 per plan §Technical Design note.
func (d *linuxDriver) pollStats(ctx context.Context, sess *linuxSession) vm.StatSnapshot {
	snap := vm.StatSnapshot{
		CPUCores: sess.spec.CPUCount,
	}
	if snap.CPUCores == 0 {
		snap.CPUCores = defaultCPUCount
	}

	raw, err := d.qmpExecute(ctx, sess, "qom-get", map[string]any{
		"path":     "/machine/peripheral-anon/device[0]",
		"property": "guest-stats",
	})
	if err != nil {
		slog.Debug("vmdriver: qom-get balloon failed (fallback to 0)", "err", err)
		return snap
	}

	var stats struct {
		Stats struct {
			ActualBalloon int64 `json:"actual-balloon"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(raw, &stats); err != nil {
		slog.Debug("vmdriver: parse balloon stats failed (fallback to 0)", "err", err)
		return snap
	}

	if stats.Stats.ActualBalloon < 0 {
		snap.MemoryUsedMB = 0
	} else {
		snap.MemoryUsedMB = uint64(stats.Stats.ActualBalloon) / (1024 * 1024)
	}

	return snap
}

// stopProcessGracefully implements the three-step shutdown sequence:
// system_powerdown (10s) → quit + SIGTERM (2s) → SIGKILL.
func (d *linuxDriver) stopProcessGracefully(ctx context.Context, sess *linuxSession) {
	// Step 1: ACPI power-off.
	if _, err := d.qmpExecute(ctx, sess, "system_powerdown", nil); err != nil {
		slog.Warn("vmdriver: system_powerdown failed", "err", err)
	}

	select {
	case <-sess.waitDone:
		return
	case <-time.After(10 * time.Second):
	}

	// Step 2: QMP quit + SIGTERM.
	_, _ = d.qmpExecute(ctx, sess, "quit", nil)
	if sess.cmd.Process != nil {
		sess.cmd.Process.Signal(os.Interrupt)
	}

	select {
	case <-sess.waitDone:
		return
	case <-time.After(2 * time.Second):
	}

	// Step 3: SIGKILL.
	if sess.cmd.Process != nil {
		sess.cmd.Process.Kill()
	}
	<-sess.waitDone
}

// ringBuffer and drainTo live in ringbuffer.go (no build tag).
