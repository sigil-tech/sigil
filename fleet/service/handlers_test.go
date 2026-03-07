package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleHealthz(t *testing.T) {
	h := &handlers{cloudCostPerQuery: 0.01}
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()

	h.handleHealthz(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["status"] != "ok" {
		t.Errorf("expected status ok, got %q", resp["status"])
	}
}

func TestHandleIngestReportUnauthorized(t *testing.T) {
	h := &handlers{apiKey: "secret123", cloudCostPerQuery: 0.01}
	body := `{"node_id":"test-node","timestamp":"2026-01-01T00:00:00Z"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/reports", strings.NewReader(body))
	w := httptest.NewRecorder()

	h.handleIngestReport(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without API key, got %d", w.Code)
	}
}

func TestHandleIngestReportBadBody(t *testing.T) {
	h := &handlers{cloudCostPerQuery: 0.01}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/reports", strings.NewReader("not json"))
	w := httptest.NewRecorder()

	h.handleIngestReport(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad body, got %d", w.Code)
	}
}

func TestHandleIngestReportMissingNodeID(t *testing.T) {
	h := &handlers{cloudCostPerQuery: 0.01}
	body := `{"timestamp":"2026-01-01T00:00:00Z"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/reports", strings.NewReader(body))
	w := httptest.NewRecorder()

	h.handleIngestReport(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing node_id, got %d", w.Code)
	}
}
