package main

import (
	"encoding/json"
	"fmt"
)

// FleetStatusResult holds the fleet connection status.
type FleetStatusResult struct {
	Active    bool   `json:"active"`
	NodeID    string `json:"node_id"`
	Endpoint  string `json:"endpoint"`
	LastSent  string `json:"last_sent"`
	QueueSize int    `json:"queue_size"`
	Interval  string `json:"interval"`
	OrgName   string `json:"org_name,omitempty"`
	TeamName  string `json:"team_name,omitempty"`
	Role      string `json:"role,omitempty"`
}

// GetFleetStatus returns the fleet reporter status.
func (a *App) GetFleetStatus() (FleetStatusResult, error) {
	resp, err := a.call("fleet-status", nil)
	if err != nil {
		return FleetStatusResult{}, err
	}
	if !resp.OK {
		return FleetStatusResult{}, fmt.Errorf("daemon error: %s", resp.Error)
	}
	var result FleetStatusResult
	if err := json.Unmarshal(resp.Payload, &result); err != nil {
		return FleetStatusResult{}, fmt.Errorf("unmarshal fleet status: %w", err)
	}
	return result, nil
}

// GetFleetPreview returns a preview of the fleet report data.
func (a *App) GetFleetPreview() (map[string]any, error) {
	resp, err := a.call("fleet-preview", nil)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("daemon error: %s", resp.Error)
	}
	var result map[string]any
	if err := json.Unmarshal(resp.Payload, &result); err != nil {
		return nil, fmt.Errorf("unmarshal fleet preview: %w", err)
	}
	return result, nil
}
