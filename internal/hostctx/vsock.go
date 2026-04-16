package hostctx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/sigil-tech/sigil/internal/store"
)

// VsockReader implements HostContextReader by sending requests over a
// vsock control channel connection. For Phase 1, the connection is injected
// as a net.Conn (allowing net.Pipe() in tests and real vsock in production).
type VsockReader struct {
	mu   sync.Mutex
	conn net.Conn
}

// NewVsockReader creates a VsockReader using the given connection.
func NewVsockReader(conn net.Conn) *VsockReader {
	return &VsockReader{conn: conn}
}

// request is the wire format for a host context query.
type request struct {
	Method string `json:"method"`
	Limit  int    `json:"limit,omitempty"`
}

// response is the wire format for a host context reply.
type response struct {
	OK       bool                   `json:"ok"`
	Error    string                 `json:"error,omitempty"`
	Patterns []store.PatternSummary `json:"patterns,omitempty"`
	Task     *store.TaskRecord      `json:"task,omitempty"`
}

// RecentPatterns sends a ctrl.context.patterns request over the control channel.
// Returns empty slice on any error (degraded mode).
func (v *VsockReader) RecentPatterns(ctx context.Context, limit int) ([]store.PatternSummary, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.conn == nil {
		return nil, nil // degraded mode
	}

	req := request{Method: "ctrl.context.patterns", Limit: limit}
	resp, err := v.roundTrip(req)
	if err != nil {
		return nil, nil // degraded mode — return empty, not error
	}
	if !resp.OK {
		return nil, nil
	}
	return resp.Patterns, nil
}

// ActiveSession sends a ctrl.context.session request over the control channel.
// Returns nil on any error (degraded mode).
func (v *VsockReader) ActiveSession(ctx context.Context) (*store.TaskRecord, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.conn == nil {
		return nil, nil // degraded mode
	}

	req := request{Method: "ctrl.context.session"}
	resp, err := v.roundTrip(req)
	if err != nil {
		return nil, nil // degraded mode
	}
	if !resp.OK {
		return nil, nil
	}
	return resp.Task, nil
}

// roundTrip sends a JSON request and reads a JSON response.
// Wire format: 4-byte big-endian length prefix + JSON payload.
func (v *VsockReader) roundTrip(req request) (*response, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("hostctx: marshal request: %w", err)
	}

	// Write length prefix (4 bytes big-endian) + payload.
	length := uint32(len(data))
	header := []byte{byte(length >> 24), byte(length >> 16), byte(length >> 8), byte(length)}
	if _, err := v.conn.Write(header); err != nil {
		return nil, fmt.Errorf("hostctx: write header: %w", err)
	}
	if _, err := v.conn.Write(data); err != nil {
		return nil, fmt.Errorf("hostctx: write payload: %w", err)
	}

	// Read response: 4-byte length prefix + JSON payload.
	var respHeader [4]byte
	if _, err := io.ReadFull(v.conn, respHeader[:]); err != nil {
		return nil, fmt.Errorf("hostctx: read response header: %w", err)
	}
	respLen := uint32(respHeader[0])<<24 | uint32(respHeader[1])<<16 | uint32(respHeader[2])<<8 | uint32(respHeader[3])
	if respLen > 1<<20 { // 1 MiB max per ADR-002
		return nil, fmt.Errorf("hostctx: response too large: %d bytes", respLen)
	}

	respData := make([]byte, respLen)
	if _, err := io.ReadFull(v.conn, respData); err != nil {
		return nil, fmt.Errorf("hostctx: read response payload: %w", err)
	}

	var resp response
	if err := json.Unmarshal(respData, &resp); err != nil {
		return nil, fmt.Errorf("hostctx: unmarshal response: %w", err)
	}
	return &resp, nil
}

// compile-time check
var _ HostContextReader = (*VsockReader)(nil)
