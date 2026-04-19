package socket

// Tests for the topic-server extensions introduced in spec 028 Phase 6:
//   - Per-subscriber CloseAfter predicate (Task 6.2)
//   - Per-subscriber filter predicate (Task 6.2)
//   - 256-slot buffer + drop counter (Task 6.4)

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startTopicTestServer creates a server with no pre-registered test topics.
// Callers register their own topics via RegisterTopicConfig.
func startTopicTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := shortTempDir(t)
	sockPath := dir + "/ts.sock"
	log := newTestLogger()
	srv := New(sockPath, log)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		srv.Stop()
	})
	require.NoError(t, srv.Start(ctx))
	return srv, sockPath
}

// subscribeAndAck dials the socket, sends a subscribe request, and reads the
// acknowledgement.  Returns the connection and a bufio.Scanner positioned
// after the ack line.  The caller is responsible for closing the connection.
func subscribeAndAck(t *testing.T, sockPath string, topic string, extra map[string]any) (net.Conn, *bufio.Scanner) {
	t.Helper()
	conn, err := net.Dial("unix", sockPath)
	require.NoError(t, err)

	payload := map[string]any{"topic": topic}
	for k, v := range extra {
		payload[k] = v
	}
	raw, err := json.Marshal(payload)
	require.NoError(t, err)

	req := Request{Method: "subscribe", Payload: json.RawMessage(raw)}
	require.NoError(t, json.NewEncoder(conn).Encode(req))

	scanner := bufio.NewScanner(conn)
	require.True(t, scanner.Scan(), "expected ack line")

	var ack Response
	require.NoError(t, json.Unmarshal(scanner.Bytes(), &ack))
	require.True(t, ack.OK, "subscribe ack not OK: %s", ack.Error)

	return conn, scanner
}

// readPushEvent reads one push event from scanner and returns the parsed
// pushEvent struct.
func readPushEvent(t *testing.T, scanner *bufio.Scanner) pushEvent {
	t.Helper()
	require.True(t, scanner.Scan(), "expected push event; err=%v", scanner.Err())
	var evt pushEvent
	require.NoError(t, json.Unmarshal(scanner.Bytes(), &evt))
	return evt
}

// TestTopicServerCloseAfterPredicate verifies that when the closeAfter
// predicate returns true for an event, the subscriber's channel is closed
// and the push loop exits cleanly (spec 028 Phase 6 Task 6.2).
func TestTopicServerCloseAfterPredicate(t *testing.T) {
	srv, sockPath := startTopicTestServer(t)

	const topic = "close-after-test"

	// Register with a closeAfter predicate that fires on kind=="terminal".
	srv.RegisterTopicConfig(topic, TopicConfig{
		BufSize: 32,
		SubscriberFactory: func(_ json.RawMessage) (
			filter func(json.RawMessage) bool,
			closeAfter func(json.RawMessage) bool,
		) {
			closeAfter = func(raw json.RawMessage) bool {
				var p struct {
					Kind string `json:"kind"`
				}
				_ = json.Unmarshal(raw, &p)
				return p.Kind == "terminal"
			}
			return nil, closeAfter
		},
	})

	conn, scanner := subscribeAndAck(t, sockPath, topic, nil)
	defer conn.Close()

	// Notify a normal event — must be delivered.
	normalPayload := MarshalPayload(map[string]string{"kind": "file", "msg": "event1"})
	srv.Notify(topic, normalPayload)
	evt := readPushEvent(t, scanner)
	assert.Equal(t, topic, evt.Event)

	// Notify the terminal event — delivered, then channel closed.
	termPayload := MarshalPayload(map[string]string{"kind": "terminal", "msg": "done"})
	srv.Notify(topic, termPayload)
	evt = readPushEvent(t, scanner)
	assert.Equal(t, topic, evt.Event)

	// Channel should now be closed; scanner.Scan() returns false (EOF).
	// Allow brief time for the close to propagate.
	conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	closed := !scanner.Scan()
	assert.True(t, closed, "expected EOF after close-after event")
}

// TestTopicServerFilterPredicate verifies that the filter predicate suppresses
// events for a given subscriber without affecting other subscribers.
func TestTopicServerFilterPredicate(t *testing.T) {
	srv, sockPath := startTopicTestServer(t)

	const topic = "filter-test"

	// Register with a filter that only lets through events with vm_id=="vm1".
	srv.RegisterTopicConfig(topic, TopicConfig{
		BufSize: 32,
		SubscriberFactory: func(subPayload json.RawMessage) (
			filter func(json.RawMessage) bool,
			closeAfter func(json.RawMessage) bool,
		) {
			var p struct {
				VMID string `json:"vm_id"`
			}
			_ = json.Unmarshal(subPayload, &p)
			wantVMID := p.VMID
			filter = func(raw json.RawMessage) bool {
				var e struct {
					VMID string `json:"vm_id"`
				}
				_ = json.Unmarshal(raw, &e)
				return e.VMID == wantVMID
			}
			return filter, nil
		},
	})

	// Subscriber A subscribes to vm_id=="vm1".
	connA, scannerA := subscribeAndAck(t, sockPath, topic, map[string]any{"vm_id": "vm1"})
	defer connA.Close()

	// Subscriber B subscribes to vm_id=="vm2".
	connB, scannerB := subscribeAndAck(t, sockPath, topic, map[string]any{"vm_id": "vm2"})
	defer connB.Close()

	// Send event for vm1.
	vm1Payload := MarshalPayload(map[string]string{"vm_id": "vm1", "kind": "file"})
	srv.Notify(topic, vm1Payload)

	// A receives it.
	evtA := readPushEvent(t, scannerA)
	assert.Equal(t, topic, evtA.Event)

	// B must NOT receive it — timeout after 100ms.
	connB.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	gotB := scannerB.Scan()
	assert.False(t, gotB, "subscriber B should not receive vm1 event")
}

// TestVMEventsBackpressure verifies that when a subscriber's buffer is full,
// events are dropped and the topic_drops_total counter increments.
// (spec 028 Phase 6 Task 6.4)
func TestVMEventsBackpressure(t *testing.T) {
	srv, sockPath := startTopicTestServer(t)

	const topic = "backpressure-test"
	const bufSize = 2 // very small buffer for test speed

	srv.RegisterTopicConfig(topic, TopicConfig{
		BufSize: bufSize,
		// no factory — events pass through unfiltered
	})

	// Connect but do NOT read events — simulates a slow consumer.
	conn, _ := subscribeAndAck(t, sockPath, topic, nil)
	defer conn.Close()

	// Give the subscriber goroutine time to register.
	time.Sleep(20 * time.Millisecond)

	before := TopicDrops(topic)

	// Send bufSize+3 events — at least 3 must be dropped.
	for i := 0; i < bufSize+3; i++ {
		srv.Notify(topic, MarshalPayload(map[string]int{"seq": i}))
	}

	// Allow brief time for the non-blocking sends to complete.
	time.Sleep(20 * time.Millisecond)

	after := TopicDrops(topic)
	assert.Greater(t, after, before, "expected drops > 0 when buffer is full")
}

// TestTopicServerCloseAfterPredicate_NoFilterNilsafe verifies that a nil
// filter does not panic when events are delivered.
func TestTopicServerCloseAfterPredicate_NoFilterNilsafe(t *testing.T) {
	srv, sockPath := startTopicTestServer(t)

	const topic = "nil-filter-test"
	srv.RegisterTopicConfig(topic, TopicConfig{
		BufSize: 8,
		SubscriberFactory: func(_ json.RawMessage) (
			filter func(json.RawMessage) bool,
			closeAfter func(json.RawMessage) bool,
		) {
			// nil filter = pass all
			return nil, nil
		},
	})

	conn, scanner := subscribeAndAck(t, sockPath, topic, nil)
	defer conn.Close()

	srv.Notify(topic, MarshalPayload("hello"))
	evt := readPushEvent(t, scanner)
	assert.Equal(t, topic, evt.Event)
}

// TestTopicDrops_ZeroForUnknownTopic verifies TopicDrops returns 0 for a topic
// that has never received a drop.
func TestTopicDrops_ZeroForUnknownTopic(t *testing.T) {
	assert.Equal(t, int64(0), TopicDrops("topic.never.seen"))
}
