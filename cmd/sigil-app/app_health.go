package main

import (
	"encoding/json"
	"fmt"
)

// ServiceHealth describes the health of a backend service.
type ServiceHealth struct {
	Name    string         `json:"name"`
	Status  string         `json:"status"`
	Message string         `json:"message"`
	Actions []HealthAction `json:"actions,omitempty"`
}

// HealthAction is a one-click fix the user can apply.
type HealthAction struct {
	Label  string `json:"label"`
	Action string `json:"action"`
}

// HealthResult holds the health of all backend services.
type HealthResult struct {
	Services []ServiceHealth `json:"services"`
}

// GetHealth returns the health status of all backend services.
func (a *App) GetHealth() (HealthResult, error) {
	resp, err := a.call("health", nil)
	if err != nil {
		return HealthResult{}, err
	}
	if !resp.OK {
		return HealthResult{}, fmt.Errorf("daemon error: %s", resp.Error)
	}
	var result HealthResult
	if err := json.Unmarshal(resp.Payload, &result); err != nil {
		return HealthResult{}, fmt.Errorf("unmarshal health: %w", err)
	}
	return result, nil
}
