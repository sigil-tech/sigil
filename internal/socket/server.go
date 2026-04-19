// Package socket implements the Unix domain socket server that the Sigil Shell
// (Phase 2) and sigilctl will talk to.  The protocol is newline-delimited
// JSON: one Request per line, one Response per line.
package socket

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

// Request is the message a client sends to the daemon.
type Request struct {
	Method  string          `json:"method"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Response is what the daemon sends back.
type Response struct {
	OK      bool            `json:"ok"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// pushEvent is a server-to-client push message sent on a subscription channel.
type pushEvent struct {
	Event   string          `json:"event"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// HandlerFunc processes a single request and returns a response.
type HandlerFunc func(ctx context.Context, req Request) Response

// allowedTopics is the explicit allowlist of topics that clients may subscribe to.
// Subscription requests for any topic not in this set are rejected. This ensures
// only intended event streams are exposed to external subscribers.
var allowedTopics = map[string]bool{
	"suggestions":         true, // legacy suggestion push topic
	"actuations":          true, // legacy actuator push topic
	"vm-status":           true, // VM lifecycle state changes
	"observer-event-rate": true, // observer event rate updates
	"analyzer-cycle":      true, // analyzer cycle completion
	"observer-events":     true, // kenazproto-serialized host events (spec 027)
	"vm-events":           true, // kenazproto-serialized VM-origin events (spec 028 Phase 6)
}

// isAllowedTopic reports whether topic is in the allowlist.
func isAllowedTopic(topic string) bool {
	return allowedTopics[topic]
}

// RegisterTopic allows the daemon to register additional topics at startup.
// Must be called before any subscribers connect. This enables plugins or
// optional subsystems to register their own topics without modifying the
// allowlist in code.
func RegisterTopic(topic string) {
	allowedTopics[topic] = true
}

// TopicConfig holds daemon-registered configuration for a push topic.
// Registered via RegisterTopicConfig before subscribers connect.
type TopicConfig struct {
	// BufSize is the per-subscriber channel buffer depth.
	// If zero, defaults to 32.
	BufSize int
	// SubscriberFactory, if non-nil, is called when a client subscribes to
	// this topic.  It receives the raw subscribe payload and returns
	// per-subscriber callbacks:
	//   filter     — if non-nil, called with each json.RawMessage before
	//                delivery; return false to skip the event for this sub.
	//   closeAfter — if non-nil, called after delivery; return true to close
	//                the subscriber's channel immediately (e.g. vm.session_terminal).
	SubscriberFactory func(payload json.RawMessage) (
		filter func(json.RawMessage) bool,
		closeAfter func(json.RawMessage) bool,
	)
}

// subscriber holds a single connection's push channel and its per-instance
// callbacks.
type subscriber struct {
	ch         chan json.RawMessage
	filter     func(json.RawMessage) bool
	closeAfter func(json.RawMessage) bool
}

// dropCounter tracks per-topic drops for the topic_drops_total metric.
// Incremented when a subscriber's channel is full (backpressure).
// Read via TopicDrops.
var dropCounter sync.Map // map[string]*atomic.Int64

// TopicDrops returns the cumulative drop count for topic since process start.
// Used by the FR-022 topic_drops_total metric.
func TopicDrops(topic string) int64 {
	v, ok := dropCounter.Load(topic)
	if !ok {
		return 0
	}
	return v.(*atomic.Int64).Load()
}

// addTopicDrop increments the drop counter for topic by 1.
func addTopicDrop(topic string) {
	v, _ := dropCounter.LoadOrStore(topic, new(atomic.Int64))
	v.(*atomic.Int64).Add(1)
}

// Server listens on a Unix socket and dispatches requests to registered
// handlers.  Multiple concurrent clients are supported.
type Server struct {
	socketPath string
	handlers   map[string]HandlerFunc
	log        *slog.Logger

	mu       sync.Mutex
	listener net.Listener

	// subMu guards topicSubs, which maps topic names to the set of per-
	// subscriber entries for active subscription connections.
	subMu     sync.RWMutex
	topicSubs map[string][]*subscriber

	// topicCfgMu guards topicCfg.
	topicCfgMu sync.RWMutex
	topicCfg   map[string]TopicConfig
}

// New creates a Server.  socketPath is the file-system path for the socket
// (e.g. "/run/user/1000/sigild.sock").
func New(socketPath string, log *slog.Logger) *Server {
	return &Server{
		socketPath: socketPath,
		handlers:   make(map[string]HandlerFunc),
		log:        log,
		topicSubs:  make(map[string][]*subscriber),
		topicCfg:   make(map[string]TopicConfig),
	}
}

// RegisterTopicConfig registers daemon-side configuration for a push topic.
// Must be called before any subscribers connect.  Calling RegisterTopic is
// sufficient for topics that need no special options; RegisterTopicConfig
// additionally registers the topic in the allowlist.
func (s *Server) RegisterTopicConfig(topic string, cfg TopicConfig) {
	RegisterTopic(topic)
	s.topicCfgMu.Lock()
	s.topicCfg[topic] = cfg
	s.topicCfgMu.Unlock()
}

// Handle registers a handler for the given method name.
// Panics if method is empty or already registered.
func (s *Server) Handle(method string, fn HandlerFunc) {
	if _, exists := s.handlers[method]; exists {
		panic(fmt.Sprintf("socket: handler for %q already registered", method))
	}
	s.handlers[method] = fn
}

// Notify fans out payload to all subscribers of topic.  Each send is
// non-blocking: if a subscriber's channel is full the message is dropped for
// that subscriber (drop counted in topic_drops_total).  The filter predicate
// is applied before delivery; the closeAfter predicate is applied after;
// if it returns true the subscriber's channel is closed.  Safe to call from
// any goroutine.
func (s *Server) Notify(topic string, payload json.RawMessage) {
	s.subMu.RLock()
	subs := s.topicSubs[topic]
	s.subMu.RUnlock()

	for _, sub := range subs {
		// Per-subscriber filter: skip this event for this subscriber.
		if sub.filter != nil && !sub.filter(payload) {
			continue
		}

		select {
		case sub.ch <- payload:
		default:
			// Subscriber is not keeping up; drop the message rather than block.
			addTopicDrop(topic)
		}

		// Close-after predicate: if this event signals session terminal, close
		// the subscriber channel so the push loop exits cleanly.
		if sub.closeAfter != nil && sub.closeAfter(payload) {
			s.removeAndClose(topic, sub)
		}
	}
}

// removeAndClose removes sub from topic's subscriber list and closes its
// channel.  Safe to call from Notify (which holds only RLock).
func (s *Server) removeAndClose(topic string, target *subscriber) {
	s.subMu.Lock()
	subs := s.topicSubs[topic]
	filtered := subs[:0]
	for _, sub := range subs {
		if sub != target {
			filtered = append(filtered, sub)
		}
	}
	s.topicSubs[topic] = filtered
	s.subMu.Unlock()
	close(target.ch)
}

// SubscriberCount returns the number of active push subscribers for topic.
func (s *Server) SubscriberCount(topic string) int {
	s.subMu.RLock()
	n := len(s.topicSubs[topic])
	s.subMu.RUnlock()
	return n
}

// Start begins listening.  It removes any stale socket file before binding.
// Returns immediately; the accept loop runs in the background until ctx is
// cancelled.
func (s *Server) Start(ctx context.Context) error {
	// Ensure the socket directory exists (important on Windows where
	// the socket lives under %LOCALAPPDATA%\sigil\).
	if err := os.MkdirAll(filepath.Dir(s.socketPath), 0o700); err != nil {
		return fmt.Errorf("socket: mkdir %s: %w", filepath.Dir(s.socketPath), err)
	}

	// Remove stale socket from a previous daemon run.
	_ = os.Remove(s.socketPath)

	ln, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("socket: listen %s: %w", s.socketPath, err)
	}

	// Enforce 0600 permissions on the socket file so only the owning user
	// can connect. Without this, the umask-dependent default permissions
	// may allow other local users to connect.
	if err := os.Chmod(s.socketPath, 0o600); err != nil {
		ln.Close()
		return fmt.Errorf("socket: chmod %s: %w", s.socketPath, err)
	}

	s.mu.Lock()
	s.listener = ln
	s.mu.Unlock()

	go s.acceptLoop(ctx, ln)
	return nil
}

// ServeListener starts accepting connections from ln and dispatching them
// using the same logic as Start.  Unlike Start, it does not create its own
// listener — the caller is responsible for binding and any pre-accept
// wrapping (e.g. TLS, auth).  The accept loop runs in a background goroutine;
// ServeListener returns immediately.
func (s *Server) ServeListener(ctx context.Context, ln net.Listener) error {
	go s.acceptLoop(ctx, ln)
	return nil
}

// Stop closes the listener, causing the accept loop to exit.
func (s *Server) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		s.listener.Close()
	}
}

func (s *Server) acceptLoop(ctx context.Context, ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return // clean shutdown
			default:
				s.log.Error("socket: accept", "err", err)
				return
			}
		}
		go s.handleConn(ctx, conn)
	}
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	enc := json.NewEncoder(conn)

	// Read the first line to determine connection mode.
	if !scanner.Scan() {
		return
	}

	var first Request
	if err := json.Unmarshal(scanner.Bytes(), &first); err != nil {
		_ = enc.Encode(Response{Error: "invalid JSON"})
		return
	}

	// Subscribe mode: the connection becomes a long-lived push channel.
	if first.Method == "subscribe" {
		s.handleSubscribe(ctx, conn, enc, first)
		return
	}

	// Request/response mode: dispatch the first request, then loop.
	s.dispatch(ctx, enc, first)
	for scanner.Scan() {
		var req Request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			_ = enc.Encode(Response{Error: "invalid JSON"})
			continue
		}
		s.dispatch(ctx, enc, req)
	}
}

// dispatch routes a single request to its handler and encodes the response.
func (s *Server) dispatch(ctx context.Context, enc *json.Encoder, req Request) {
	handler, ok := s.handlers[req.Method]
	if !ok {
		_ = enc.Encode(Response{Error: fmt.Sprintf("unknown method: %s", req.Method)})
		return
	}
	resp := handler(ctx, req)
	if err := enc.Encode(resp); err != nil {
		s.log.Warn("socket: write response", "err", err)
	}
}

// handleSubscribe upgrades the connection to a push-only subscription channel.
// It parses the topic from the first request payload, registers a buffered
// channel for that topic, sends the acknowledgement, then loops forwarding
// push events until ctx is cancelled or the channel is closed (either by the
// close-after predicate or by the server shutting down).
func (s *Server) handleSubscribe(ctx context.Context, conn net.Conn, enc *json.Encoder, req Request) {
	var p struct {
		Topic string `json:"topic"`
	}
	if err := json.Unmarshal(req.Payload, &p); err != nil || p.Topic == "" {
		_ = enc.Encode(Response{Error: "subscribe: payload must be {\"topic\":\"<name>\"}"})
		return
	}

	// Enforce topic allowlist: subscribers may only receive events on
	// topics explicitly published by the daemon. Unknown topics are rejected.
	if !isAllowedTopic(p.Topic) {
		_ = enc.Encode(Response{Error: fmt.Sprintf("subscribe: unknown topic %q", p.Topic)})
		return
	}

	// Determine buffer size and per-subscriber callbacks from the registered
	// topic configuration (if any).
	s.topicCfgMu.RLock()
	cfg := s.topicCfg[p.Topic]
	s.topicCfgMu.RUnlock()

	bufSize := cfg.BufSize
	if bufSize <= 0 {
		bufSize = 32
	}

	sub := &subscriber{
		ch: make(chan json.RawMessage, bufSize),
	}
	if cfg.SubscriberFactory != nil {
		sub.filter, sub.closeAfter = cfg.SubscriberFactory(req.Payload)
	}

	s.subMu.Lock()
	s.topicSubs[p.Topic] = append(s.topicSubs[p.Topic], sub)
	s.subMu.Unlock()

	// Deregister this subscriber when the connection exits.
	defer func() {
		s.subMu.Lock()
		subs := s.topicSubs[p.Topic]
		filtered := subs[:0]
		for _, sv := range subs {
			if sv != sub {
				filtered = append(filtered, sv)
			}
		}
		s.topicSubs[p.Topic] = filtered
		s.subMu.Unlock()
	}()

	// Acknowledge the subscription.
	_ = enc.Encode(Response{
		OK:      true,
		Payload: MarshalPayload(map[string]any{"subscribed": true}),
	})

	for {
		select {
		case <-ctx.Done():
			return
		case payload, ok := <-sub.ch:
			if !ok {
				// Channel closed by close-after predicate: session terminal.
				return
			}
			evt := pushEvent{
				Event:   p.Topic,
				Payload: payload,
			}
			if err := enc.Encode(evt); err != nil {
				s.log.Warn("socket: write push event", "topic", p.Topic, "err", err)
				return
			}
		}
	}
}

// --- Built-in payload helpers -----------------------------------------------

// MarshalPayload is a convenience wrapper that marshals v into a RawMessage.
func MarshalPayload(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
