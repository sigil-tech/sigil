package inference

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
)

// InferenceProxy implements Backend by proxying Complete and Ping calls
// to the host inference engine over a network connection (vsock port 7701).
// On connection failure, it falls back to the configured fallback Backend.
type InferenceProxy struct {
	mu       sync.Mutex
	conn     net.Conn
	fallback Backend
	maxQueue int
}

// NewInferenceProxy creates a proxy with the given connection and fallback.
// maxQueue is the maximum number of concurrent pending requests before
// drop-oldest kicks in. If maxQueue <= 0, it defaults to 50.
func NewInferenceProxy(conn net.Conn, fallback Backend, maxQueue int) *InferenceProxy {
	if maxQueue <= 0 {
		maxQueue = 50
	}
	return &InferenceProxy{
		conn:     conn,
		fallback: fallback,
		maxQueue: maxQueue,
	}
}

type proxyRequest struct {
	Method string `json:"method"`
	System string `json:"system,omitempty"`
	User   string `json:"user,omitempty"`
}

type proxyResponse struct {
	OK     bool   `json:"ok"`
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Complete proxies the completion request to the host. Falls back on error.
func (p *InferenceProxy) Complete(ctx context.Context, system, user string) (*CompletionResult, error) {
	p.mu.Lock()
	conn := p.conn
	p.mu.Unlock()

	if conn == nil {
		if p.fallback != nil {
			return p.fallback.Complete(ctx, system, user)
		}
		return nil, fmt.Errorf("inference proxy: no connection and no fallback")
	}

	req := proxyRequest{Method: "inf.complete", System: system, User: user}
	resp, err := roundTrip(conn, req)
	if err != nil {
		if p.fallback != nil {
			return p.fallback.Complete(ctx, system, user)
		}
		return nil, fmt.Errorf("inference proxy: %w", err)
	}
	if !resp.OK {
		if p.fallback != nil {
			return p.fallback.Complete(ctx, system, user)
		}
		return nil, fmt.Errorf("inference proxy: %s", resp.Error)
	}
	return &CompletionResult{Content: resp.Result, Routing: "proxy"}, nil
}

// Ping checks if the host inference engine is reachable.
func (p *InferenceProxy) Ping(ctx context.Context) error {
	p.mu.Lock()
	conn := p.conn
	p.mu.Unlock()

	if conn == nil {
		if p.fallback != nil {
			return p.fallback.Ping(ctx)
		}
		return fmt.Errorf("inference proxy: no connection")
	}

	req := proxyRequest{Method: "inf.ping"}
	resp, err := roundTrip(conn, req)
	if err != nil {
		if p.fallback != nil {
			return p.fallback.Ping(ctx)
		}
		return fmt.Errorf("inference proxy: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("inference proxy: %s", resp.Error)
	}
	return nil
}

// roundTrip sends a JSON request and reads a JSON response.
// Wire format: 4-byte big-endian length prefix + JSON payload.
func roundTrip(conn net.Conn, req proxyRequest) (*proxyResponse, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(data)))
	if _, err := conn.Write(header[:]); err != nil {
		return nil, err
	}
	if _, err := conn.Write(data); err != nil {
		return nil, err
	}

	var respHeader [4]byte
	if _, err := io.ReadFull(conn, respHeader[:]); err != nil {
		return nil, err
	}
	respLen := binary.BigEndian.Uint32(respHeader[:])
	if respLen > 64<<20 { // 64 MiB guard per ADR-002
		return nil, fmt.Errorf("response too large: %d bytes", respLen)
	}

	respData := make([]byte, respLen)
	if _, err := io.ReadFull(conn, respData); err != nil {
		return nil, err
	}

	var resp proxyResponse
	if err := json.Unmarshal(respData, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// SetConn replaces the underlying connection (e.g., on reconnect).
func (p *InferenceProxy) SetConn(conn net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.conn = conn
}

// compile-time assertion: InferenceProxy satisfies Backend.
var _ Backend = (*InferenceProxy)(nil)
