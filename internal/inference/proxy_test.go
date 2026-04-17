package inference

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

// mockBackend is a simple in-process Backend for proxy fallback tests.
type mockBackend struct {
	completeResult string
	pingErr        error
}

func (m *mockBackend) Complete(_ context.Context, _, _ string) (*CompletionResult, error) {
	return &CompletionResult{Content: m.completeResult, Routing: "mock"}, nil
}

func (m *mockBackend) Ping(_ context.Context) error {
	return m.pingErr
}

// serveOnce reads one framed request from conn and writes a framed response.
func serveOnce(t *testing.T, conn net.Conn, resp proxyResponse) {
	t.Helper()

	var hdr [4]byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		t.Errorf("serveOnce read header: %v", err)
		return
	}
	length := binary.BigEndian.Uint32(hdr[:])
	data := make([]byte, length)
	if _, err := io.ReadFull(conn, data); err != nil {
		t.Errorf("serveOnce read body: %v", err)
		return
	}

	respData, err := json.Marshal(resp)
	if err != nil {
		t.Errorf("serveOnce marshal: %v", err)
		return
	}
	var respHdr [4]byte
	binary.BigEndian.PutUint32(respHdr[:], uint32(len(respData)))
	conn.Write(respHdr[:]) //nolint:errcheck
	conn.Write(respData)   //nolint:errcheck
}

func TestInferenceProxyComplete(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	proxy := NewInferenceProxy(client, nil, 50)

	go serveOnce(t, server, proxyResponse{OK: true, Result: "Hello from host"})

	result, err := proxy.Complete(context.Background(), "system", "user")
	require.NoError(t, err)
	require.Equal(t, "Hello from host", result.Content)
	require.Equal(t, "proxy", result.Routing)
}

func TestInferenceProxyPing(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	proxy := NewInferenceProxy(client, nil, 50)

	go serveOnce(t, server, proxyResponse{OK: true})

	require.NoError(t, proxy.Ping(context.Background()))
}

func TestInferenceProxyFallback_nilConn(t *testing.T) {
	fallback := &mockBackend{completeResult: "fallback result"}
	proxy := NewInferenceProxy(nil, fallback, 50)

	result, err := proxy.Complete(context.Background(), "sys", "user")
	require.NoError(t, err)
	require.Equal(t, "fallback result", result.Content)
}

func TestInferenceProxyFallback_connError(t *testing.T) {
	// Closing client immediately forces a write error, triggering fallback.
	client, server := net.Pipe()
	server.Close()

	fallback := &mockBackend{completeResult: "from fallback"}
	proxy := NewInferenceProxy(client, fallback, 50)

	result, err := proxy.Complete(context.Background(), "sys", "user")
	require.NoError(t, err)
	require.Equal(t, "from fallback", result.Content)

	client.Close()
}

func TestInferenceProxyNoConnectionNoFallback(t *testing.T) {
	proxy := NewInferenceProxy(nil, nil, 50)

	_, err := proxy.Complete(context.Background(), "sys", "user")
	require.Error(t, err)
}

func TestInferenceProxyHostError(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	proxy := NewInferenceProxy(client, nil, 50)

	go serveOnce(t, server, proxyResponse{OK: false, Error: "model not loaded"})

	_, err := proxy.Complete(context.Background(), "sys", "user")
	require.ErrorContains(t, err, "model not loaded")
}

func TestInferenceProxySetConn(t *testing.T) {
	// Start with a broken connection, then swap in a working one.
	dead, _ := net.Pipe()
	dead.Close()

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	fallback := &mockBackend{completeResult: "fallback"}
	proxy := NewInferenceProxy(dead, fallback, 50)

	proxy.SetConn(client)

	go serveOnce(t, server, proxyResponse{OK: true, Result: "after reconnect"})

	result, err := proxy.Complete(context.Background(), "sys", "user")
	require.NoError(t, err)
	require.Equal(t, "after reconnect", result.Content)
}

func TestInferenceProxyDefaultMaxQueue(t *testing.T) {
	p := NewInferenceProxy(nil, nil, 0)
	require.Equal(t, 50, p.maxQueue)
}
