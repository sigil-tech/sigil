package main

import (
	"encoding/json"
	"fmt"
)

// TimelineResult holds the response from the timeline socket method.
type TimelineResult struct {
	Events []TimelineEvent `json:"events"`
	Total  int             `json:"total"`
}

// TimelineEvent is a single event in the timeline.
type TimelineEvent struct {
	Timestamp string         `json:"timestamp"`
	Kind      string         `json:"kind"`
	Summary   string         `json:"summary"`
	Detail    map[string]any `json:"detail,omitempty"`
}

// GetTimeline returns events for a specific date with optional type filtering.
func (a *App) GetTimeline(date string, types []string, offset, limit int) (TimelineResult, error) {
	resp, err := a.call("timeline", map[string]any{
		"date":   date,
		"types":  types,
		"offset": offset,
		"limit":  limit,
	})
	if err != nil {
		return TimelineResult{}, err
	}
	if !resp.OK {
		return TimelineResult{}, fmt.Errorf("daemon error: %s", resp.Error)
	}
	var result TimelineResult
	if err := json.Unmarshal(resp.Payload, &result); err != nil {
		return TimelineResult{}, fmt.Errorf("unmarshal timeline: %w", err)
	}
	return result, nil
}
