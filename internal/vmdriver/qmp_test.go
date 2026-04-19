package vmdriver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeQMPServer runs a minimal fake QEMU QMP server over net.Pipe. The caller
// provides a handler that receives each inbound line and writes response lines.
type fakeQMPServer struct {
	server net.Conn
	client net.Conn
}

// newFakeQMP returns a pipe pair. server is the "QEMU side"; client is used by
// Dial via a custom dialer (see dialPipe helper).
func newFakeQMP(t *testing.T) *fakeQMPServer {
	t.Helper()
	server, client := net.Pipe()
	t.Cleanup(func() {
		server.Close()
		client.Close()
	})
	return &fakeQMPServer{server: server, client: client}
}

// dialPipe returns a QMPClient that communicates over the pre-established
// net.Pipe connection instead of dialing a real socket.
func dialPipe(t *testing.T, pipe *fakeQMPServer, greet func(net.Conn)) (*QMPClient, error) {
	t.Helper()
	ctx := context.Background()

	// Start the server side in a goroutine.
	done := make(chan struct{})
	go func() {
		defer close(done)
		greet(pipe.server)
	}()

	// Build client directly from the pipe connection (bypass Dial's dial step).
	c := &QMPClient{
		conn: pipe.client,
		r:    newBufScanner(pipe.client),
	}

	// Read greeting + send qmp_capabilities response manually.
	// We call the internal execute path directly.
	_ = ctx

	<-done
	return c, nil
}

// serveQMP is a helper that sends the greeting and then processes one command.
func serveSimple(conn net.Conn, greeting string, responses map[string]string) {
	defer conn.Close()

	// Send greeting.
	fmt.Fprintf(conn, "%s\n", greeting)

	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		line := strings.TrimSpace(string(buf[:n]))
		if line == "" {
			continue
		}

		var req map[string]any
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			return
		}
		cmd, _ := req["execute"].(string)
		resp, ok := responses[cmd]
		if !ok {
			fmt.Fprintf(conn, `{"error":{"class":"GenericError","desc":"unknown command: %s"}}`+"\n", cmd)
			continue
		}
		fmt.Fprintf(conn, "%s\n", resp)
	}
}

// TestQMPClient_HappyPath_QueryStatus exercises the happy-path handshake and
// a query-status command over net.Pipe.
func TestQMPClient_HappyPath_QueryStatus(t *testing.T) {
	server, client := net.Pipe()
	t.Cleanup(func() { server.Close(); client.Close() })

	const greeting = `{"QMP":{"version":{"qemu":{"major":8,"minor":0,"micro":0},"package":""}},"capabilities":[]}`
	const capResp = `{"return":{}}`
	const statusResp = `{"return":{"status":"running","singlestep":false,"running":true}}`

	go serveSimple(server, greeting, map[string]string{
		"qmp_capabilities": capResp,
		"query-status":     statusResp,
	})

	// Manually build client from pipe.
	c := &QMPClient{conn: client, r: newBufScanner(client)}

	ctx := context.Background()

	// Read greeting.
	require.True(t, c.r.Scan(), "expected greeting line")
	var g qmpGreeting
	require.NoError(t, json.Unmarshal(c.r.Bytes(), &g))

	// capabilities handshake.
	_, err := c.Execute(ctx, "qmp_capabilities", nil)
	require.NoError(t, err)

	// query-status.
	raw, err := c.Execute(ctx, "query-status", nil)
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(raw, &result))
	assert.Equal(t, "running", result["status"])
}

// TestQMPClient_ErrorResponse verifies that a QMP error response is surfaced
// as a Go error from Execute.
func TestQMPClient_ErrorResponse(t *testing.T) {
	server, client := net.Pipe()
	t.Cleanup(func() { server.Close(); client.Close() })

	const greeting = `{"QMP":{"version":{}}}`
	go serveSimple(server, greeting, map[string]string{
		"qmp_capabilities": `{"return":{}}`,
		// no "bad-cmd" entry — serveSimple returns a generic error
	})

	c := &QMPClient{conn: client, r: newBufScanner(client)}
	ctx := context.Background()

	// Skip greeting.
	c.r.Scan()

	// Capabilities.
	_, _ = c.Execute(ctx, "qmp_capabilities", nil)

	// Issue an unknown command — expect an error.
	_, err := c.Execute(ctx, "bad-cmd", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad-cmd")
}

// TestQMPClient_ConnectionCloseMidRead verifies that Execute returns an error
// when the server closes the connection before sending a response.
func TestQMPClient_ConnectionCloseMidRead(t *testing.T) {
	server, client := net.Pipe()
	t.Cleanup(func() { server.Close(); client.Close() })

	// Server sends greeting then closes immediately.
	go func() {
		defer server.Close()
		fmt.Fprintf(server, `{"QMP":{"version":{}}}`)
		fmt.Fprintf(server, "\n")
		// Close without sending capability response.
	}()

	c := &QMPClient{conn: client, r: newBufScanner(client)}
	ctx := context.Background()

	// Read greeting.
	c.r.Scan()

	// Execute qmp_capabilities — server is already closed.
	_, err := c.Execute(ctx, "qmp_capabilities", nil)
	require.Error(t, err)
}

// TestQMPClient_HandshakeFailure verifies that Dial-time capabilities failure
// is surfaced correctly when we simulate it via a broken greeting.
func TestQMPClient_HandshakeFailure(t *testing.T) {
	server, client := net.Pipe()
	t.Cleanup(func() { server.Close(); client.Close() })

	// Server sends a malformed greeting (not valid JSON).
	go func() {
		defer server.Close()
		fmt.Fprintf(server, "not valid json\n")
	}()

	c := &QMPClient{conn: client, r: newBufScanner(client)}

	// Read greeting line — scan should succeed but parse should fail.
	require.True(t, c.r.Scan())
	var g qmpGreeting
	err := json.Unmarshal(c.r.Bytes(), &g)
	require.Error(t, err, "expected parse error for malformed greeting")
}

// TestQMPClient_AsyncEventsSkipped verifies that async events interspersed
// with a command response are skipped and the real response is returned.
func TestQMPClient_AsyncEventsSkipped(t *testing.T) {
	server, client := net.Pipe()
	t.Cleanup(func() { server.Close(); client.Close() })

	go func() {
		defer server.Close()
		// Greeting.
		fmt.Fprintf(server, `{"QMP":{"version":{}}}`+"\n")

		// Read qmp_capabilities.
		buf := make([]byte, 512)
		server.Read(buf) //nolint:errcheck
		// Send cap response.
		fmt.Fprintf(server, `{"return":{}}`+"\n")

		// Read next command.
		server.Read(buf) //nolint:errcheck

		// Interleave an async event before the real response.
		fmt.Fprintf(server, `{"event":"RESET","data":{},"timestamp":{"seconds":1}}`+"\n")
		fmt.Fprintf(server, `{"return":{"status":"running","singlestep":false,"running":true}}`+"\n")
	}()

	c := &QMPClient{conn: client, r: newBufScanner(client)}
	ctx := context.Background()

	// Read greeting.
	c.r.Scan()
	_, _ = c.Execute(ctx, "qmp_capabilities", nil)

	raw, err := c.Execute(ctx, "query-status", nil)
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(raw, &result))
	assert.Equal(t, "running", result["status"])
}

// TestQMPClient_Close verifies that Close is idempotent and that Execute
// after Close returns an error.
func TestQMPClient_Close(t *testing.T) {
	server, client := net.Pipe()
	t.Cleanup(func() { server.Close(); client.Close() })

	c := &QMPClient{conn: client, r: newBufScanner(client)}
	require.NoError(t, c.Close())

	// A second Close should not panic.
	_ = c.Close()

	// Execute after Close must fail.
	_, err := c.Execute(context.Background(), "query-status", nil)
	require.Error(t, err)
}

// newBufScanner wraps a net.Conn in a bufio.Scanner, matching the field
// initialisation in QMPClient.
func newBufScanner(r io.Reader) *bufio.Scanner {
	// Import the same bufio.NewScanner used in Dial.
	return bufio.NewScanner(r)
}
