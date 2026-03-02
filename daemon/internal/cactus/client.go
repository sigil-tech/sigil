// Package cactus provides a client for the Cactus Compute inference engine.
// Cactus exposes an OpenAI-compatible HTTP API on localhost, so this client
// works equally well against Ollama or any other OpenAI-compatible backend —
// just point BaseURL at a different address.
package cactus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// RoutingMode controls how the Cactus Hybrid Router handles each query.
// These map directly to Cactus's four routing modes.
type RoutingMode string

const (
	RouteLocal       RoutingMode = "local"       // strictly on-device; never calls the network
	RouteLocalFirst  RoutingMode = "localfirst"  // try local, fall back to cloud
	RouteRemoteFirst RoutingMode = "remotefirst" // prefer cloud, use local if API fails
	RouteRemote      RoutingMode = "remote"      // strictly cloud; always calls the network
)

// Client talks to Cactus via its OpenAI-compatible /v1/chat/completions endpoint.
type Client struct {
	BaseURL    string
	Model      string
	Routing    RoutingMode
	httpClient *http.Client
}

// New returns a Client pointed at the given Cactus endpoint.
// BaseURL should be e.g. "http://127.0.0.1:8080" (no trailing slash).
func New(baseURL, model string, routing RoutingMode) *Client {
	return &Client{
		BaseURL: baseURL,
		Model:   model,
		Routing: routing,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// --- OpenAI-compatible request / response types ----------------------------

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model     string        `json:"model"`
	Messages  []chatMessage `json:"messages"`
	MaxTokens int           `json:"max_tokens,omitempty"`
}

type chatChoice struct {
	Message chatMessage `json:"message"`
}

type chatResponse struct {
	Choices []chatChoice `json:"choices"`

	// Cactus-specific extension: routing metadata.
	// These fields may be absent when talking to a non-Cactus backend.
	CactusRouting struct {
		Decision string `json:"decision"` // "local" | "cloud"
		LatencyMS int64  `json:"latency_ms"`
	} `json:"cactus_routing,omitempty"`
}

// CompletionResult is the parsed response from a chat completion call.
type CompletionResult struct {
	Content   string
	Routing   string // "local" | "cloud" — may be empty for non-Cactus backends
	LatencyMS int64
}

// Complete sends a single-turn prompt to Cactus and returns the assistant reply.
// Use system to provide a system prompt; pass an empty string to omit it.
func (c *Client) Complete(ctx context.Context, system, user string) (*CompletionResult, error) {
	msgs := make([]chatMessage, 0, 2)
	if system != "" {
		msgs = append(msgs, chatMessage{Role: "system", Content: system})
	}
	msgs = append(msgs, chatMessage{Role: "user", Content: user})

	body, err := json.Marshal(chatRequest{
		Model:    c.Model,
		Messages: msgs,
	})
	if err != nil {
		return nil, fmt.Errorf("cactus: marshal request: %w", err)
	}

	url := c.BaseURL + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("cactus: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Cactus routing hint header — ignored by non-Cactus backends.
	if c.Routing != "" {
		req.Header.Set("X-Cactus-Routing", string(c.Routing))
	}

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cactus: request: %w", err)
	}
	defer resp.Body.Close()
	elapsed := time.Since(start).Milliseconds()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("cactus: HTTP %d: %s", resp.StatusCode, raw)
	}

	var cr chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return nil, fmt.Errorf("cactus: decode response: %w", err)
	}
	if len(cr.Choices) == 0 {
		return nil, fmt.Errorf("cactus: empty choices in response")
	}

	result := &CompletionResult{
		Content:   cr.Choices[0].Message.Content,
		Routing:   cr.CactusRouting.Decision,
		LatencyMS: elapsed,
	}
	if result.Routing == "" {
		// Non-Cactus backend: assume cloud routing.
		result.Routing = "cloud"
	}

	return result, nil
}

// Ping checks reachability by hitting the models endpoint.  Returns nil if
// the backend is up and serving.
func (c *Client) Ping(ctx context.Context) error {
	url := c.BaseURL + "/v1/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cactus: ping: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("cactus: ping returned HTTP %d", resp.StatusCode)
	}
	return nil
}
