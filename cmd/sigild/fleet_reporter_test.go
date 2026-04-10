package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestComputeAdoptionTier(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		events int
		accept float64
		want   int
	}{
		{"inactive", 50, 0.1, 0},
		{"onboarding", 200, 0.2, 1},
		{"active", 600, 0.4, 2},
		{"power_user", 1500, 0.6, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := computeAdoptionTier(tt.events, tt.accept)
			if got != tt.want {
				t.Errorf("computeAdoptionTier(%d, %.1f) = %d, want %d", tt.events, tt.accept, got, tt.want)
			}
		})
	}
}

func TestFleetReportJSON(t *testing.T) {
	t.Parallel()

	rpt := fleetReport{
		NodeID:            "n_test123",
		Timestamp:         time.Now(),
		Platform:          "darwin",
		Version:           "0.1.0",
		TotalEvents:       500,
		EventCounts:       map[string]int{"file": 300, "git": 50},
		BrowserCategories: map[string]float64{"development": 30.0},
		TopApps:           map[string]float64{"GoLand": 45.0},
		FocusScore:        72.5,
		ContextSwitches:   42,
	}

	data, err := json.Marshal(rpt)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded["node_id"] != "n_test123" {
		t.Errorf("node_id = %v, want n_test123", decoded["node_id"])
	}
	if decoded["platform"] != "darwin" {
		t.Errorf("platform = %v, want darwin", decoded["platform"])
	}
	if decoded["focus_score"] != 72.5 {
		t.Errorf("focus_score = %v, want 72.5", decoded["focus_score"])
	}
}

func TestPostReportAuth(t *testing.T) {
	t.Parallel()

	var gotAuth string
	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer srv.Close()

	r := &fleetReporter{
		endpoint: srv.URL,
		token:    "test_token_123",
		nodeID:   "n_test",
		seenRecs: make(map[string]time.Time),
		active:   true,
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	rpt := fleetReport{NodeID: "n_test", Timestamp: time.Now()}
	if err := r.postReport(t.Context(), rpt); err != nil {
		t.Fatalf("postReport: %v", err)
	}

	if gotAuth != "Bearer test_token_123" {
		t.Errorf("auth header = %q, want Bearer test_token_123", gotAuth)
	}
	if gotContentType != "application/json" {
		t.Errorf("content-type = %q, want application/json", gotContentType)
	}
}

func TestPostReportDeactivatesOn403(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	r := &fleetReporter{
		endpoint: srv.URL,
		token:    "expired_token",
		nodeID:   "n_test",
		seenRecs: make(map[string]time.Time),
		active:   true,
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	rpt := fleetReport{NodeID: "n_test", Timestamp: time.Now()}
	err := r.postReport(t.Context(), rpt)
	if err == nil {
		t.Fatal("expected error on 403")
	}

	if r.isActive() {
		t.Error("expected reporter to be deactivated after 403")
	}
}

func TestQueueAndDrain(t *testing.T) {
	t.Parallel()

	r := &fleetReporter{
		seenRecs: make(map[string]time.Time),
		active:   true,
	}

	// Enqueue 3 reports.
	for i := 0; i < 3; i++ {
		r.enqueue(fleetReport{NodeID: "n_test"})
	}

	r.mu.Lock()
	if len(r.queue) != 3 {
		t.Errorf("queue length = %d, want 3", len(r.queue))
	}
	r.mu.Unlock()

	// Verify cap at 24.
	for i := 0; i < 25; i++ {
		r.enqueue(fleetReport{NodeID: "n_test"})
	}
	r.mu.Lock()
	if len(r.queue) > 24 {
		t.Errorf("queue length = %d, want <= 24", len(r.queue))
	}
	r.mu.Unlock()
}

func TestEnsureNodeID(t *testing.T) {
	t.Parallel()

	r := &fleetReporter{
		seenRecs: make(map[string]time.Time),
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	r.ensureNodeID()

	if r.nodeID == "" {
		t.Fatal("nodeID should not be empty")
	}
	if len(r.nodeID) != 18 { // "n_" + 16 hex chars
		t.Errorf("nodeID length = %d, want 18, got %q", len(r.nodeID), r.nodeID)
	}
	if r.nodeID[:2] != "n_" {
		t.Errorf("nodeID should start with n_, got %q", r.nodeID)
	}

	// Should not change on second call.
	first := r.nodeID
	r.ensureNodeID()
	if r.nodeID != first {
		t.Error("nodeID changed on second call")
	}
}

func TestRecommendationDedup(t *testing.T) {
	t.Parallel()

	r := &fleetReporter{
		seenRecs: make(map[string]time.Time),
		active:   true,
	}
	r.mu.Lock()
	r.cachedPolicy = &fleetPolicy{
		Recommendations: []fleetRecommendation{
			{ID: "rec_1", Title: "Test 1"},
			{ID: "rec_2", Title: "Test 2"},
		},
	}
	r.mu.Unlock()

	// First call: both are new.
	fresh := r.newRecommendations()
	if len(fresh) != 2 {
		t.Errorf("first call: got %d recs, want 2", len(fresh))
	}

	// Second call: both are seen.
	fresh = r.newRecommendations()
	if len(fresh) != 0 {
		t.Errorf("second call: got %d recs, want 0", len(fresh))
	}
}
