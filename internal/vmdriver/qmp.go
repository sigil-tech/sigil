// Package vmdriver — QMP client.
// This file implements a minimal subset of the QEMU Machine Protocol (QMP),
// which is line-delimited JSON over a Unix socket. Only the command subset
// required by vmdriver_linux.go is implemented. No external dependencies are
// introduced; stdlib only.
package vmdriver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"time"
)

// qmpDialTimeout is how long Dial waits for the QMP socket to appear and
// accept a connection after QEMU has been forked.
const qmpDialTimeout = 5 * time.Second

// qmpReadTimeout is the per-read deadline applied to individual JSON responses.
const qmpReadTimeout = 10 * time.Second

// qmpGreeting is the first JSON object QEMU sends after the socket is
// accepted. We read and discard it (the version info is not used).
type qmpGreeting struct {
	QMP struct {
		Version json.RawMessage `json:"version"`
	} `json:"QMP"`
}

// qmpResponse is the envelope for a command response. Exactly one of Return
// and Error will be populated for a command response.  Event fields indicate
// an asynchronous event (which we log and skip).
type qmpResponse struct {
	Return json.RawMessage `json:"return"`
	Error  *struct {
		Class string `json:"class"`
		Desc  string `json:"desc"`
	} `json:"error"`
	Event string `json:"event"` // non-empty for async events
}

// qmpCommand is the wire shape for an outgoing QMP request.
type qmpCommand struct {
	Execute   string         `json:"execute"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// QMPClient is a minimal QMP client over a Unix socket.
// Obtain via Dial. Close when done.
type QMPClient struct {
	conn net.Conn
	r    *bufio.Scanner
}

// Dial connects to the QMP socket at socketPath, reads the greeting banner,
// and sends the qmp_capabilities handshake, leaving the connection in
// command mode. Returns a ready-to-use *QMPClient.
func Dial(ctx context.Context, socketPath string) (*QMPClient, error) {
	var conn net.Conn
	var lastErr error

	// Retry with backoff: QEMU takes a short time after fork to create the
	// socket.
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(qmpDialTimeout)
	}

	backoff := 50 * time.Millisecond
	for time.Now().Before(deadline) {
		conn, lastErr = net.DialTimeout("unix", socketPath, qmpDialTimeout)
		if lastErr == nil {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 500*time.Millisecond {
			backoff *= 2
		}
	}
	if conn == nil {
		return nil, fmt.Errorf("qmp: dial %s: %w", socketPath, lastErr)
	}

	c := &QMPClient{
		conn: conn,
		r:    bufio.NewScanner(conn),
	}

	// Read the greeting banner. QEMU sends a single JSON line on connect.
	if err := conn.SetReadDeadline(time.Now().Add(qmpReadTimeout)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("qmp: set read deadline: %w", err)
	}
	if !c.r.Scan() {
		err := c.r.Err()
		conn.Close()
		if err != nil {
			return nil, fmt.Errorf("qmp: read greeting: %w", err)
		}
		return nil, fmt.Errorf("qmp: connection closed before greeting")
	}
	var greeting qmpGreeting
	if err := json.Unmarshal(c.r.Bytes(), &greeting); err != nil {
		conn.Close()
		return nil, fmt.Errorf("qmp: parse greeting: %w", err)
	}

	// Negotiate capabilities. The response to qmp_capabilities is
	// {"return": {}} which we ignore.
	if _, err := c.Execute(ctx, "qmp_capabilities", nil); err != nil {
		conn.Close()
		return nil, fmt.Errorf("qmp: capabilities handshake: %w", err)
	}

	return c, nil
}

// Execute sends a QMP command and waits for the non-event response. Async
// events received before the response are logged at Debug and skipped.
// Returns the raw "return" field value (may be an empty JSON object).
func (c *QMPClient) Execute(ctx context.Context, cmd string, args map[string]any) (json.RawMessage, error) {
	req := qmpCommand{Execute: cmd, Arguments: args}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("qmp: marshal command %s: %w", cmd, err)
	}
	data = append(data, '\n')

	if err := c.conn.SetWriteDeadline(time.Now().Add(qmpReadTimeout)); err != nil {
		return nil, fmt.Errorf("qmp: set write deadline: %w", err)
	}
	if _, err := c.conn.Write(data); err != nil {
		return nil, fmt.Errorf("qmp: write command %s: %w", cmd, err)
	}

	// Read until we get a non-event response.
	for {
		if err := c.conn.SetReadDeadline(time.Now().Add(qmpReadTimeout)); err != nil {
			return nil, fmt.Errorf("qmp: set read deadline: %w", err)
		}

		// Check for context cancellation before blocking.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if !c.r.Scan() {
			if err := c.r.Err(); err != nil {
				return nil, fmt.Errorf("qmp: read response to %s: %w", cmd, err)
			}
			return nil, fmt.Errorf("qmp: connection closed while reading response to %s", cmd)
		}

		var resp qmpResponse
		if err := json.Unmarshal(c.r.Bytes(), &resp); err != nil {
			return nil, fmt.Errorf("qmp: parse response to %s: %w", cmd, err)
		}

		// Async event — log at Debug and skip.
		if resp.Event != "" {
			slog.Debug("qmp: async event", "event", resp.Event)
			continue
		}

		if resp.Error != nil {
			return nil, fmt.Errorf("qmp: command %s error: %s: %s",
				cmd, resp.Error.Class, resp.Error.Desc)
		}

		return resp.Return, nil
	}
}

// Close closes the underlying connection.
func (c *QMPClient) Close() error {
	return c.conn.Close()
}
