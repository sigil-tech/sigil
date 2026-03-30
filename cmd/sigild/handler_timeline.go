package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/wambozi/sigil/internal/event"
	"github.com/wambozi/sigil/internal/socket"
	"github.com/wambozi/sigil/internal/store"
)

// registerTimelineHandlers adds the timeline socket method.
func registerTimelineHandlers(srv *socket.Server, db *store.Store) {
	srv.Handle("timeline", func(ctx context.Context, req socket.Request) socket.Response {
		var p struct {
			Date   string   `json:"date"`
			Types  []string `json:"types"`
			Offset int      `json:"offset"`
			Limit  int      `json:"limit"`
		}
		if req.Payload != nil {
			_ = json.Unmarshal(req.Payload, &p)
		}
		if p.Date == "" {
			p.Date = time.Now().Format("2006-01-02")
		}
		if p.Limit <= 0 {
			p.Limit = 50
		}

		date, err := time.Parse("2006-01-02", p.Date)
		if err != nil {
			return socket.Response{Error: fmt.Sprintf("invalid date: %v", err)}
		}

		start := date
		end := date.AddDate(0, 0, 1)

		// Use the paginated filter API.
		filter := store.EventFilter{
			After:  start.UnixMilli(),
			Before: end.UnixMilli(),
			Limit:  p.Limit,
			Offset: p.Offset,
		}

		// If types filter specifies a single kind, use it.
		if len(p.Types) == 1 {
			filter.Kind = event.Kind(p.Types[0])
		}

		events, total, err := db.QueryEventsPaginated(ctx, filter)
		if err != nil {
			return socket.Response{Error: fmt.Sprintf("query events: %v", err)}
		}

		// Filter by multiple types client-side if needed (EventFilter only supports one kind).
		if len(p.Types) > 1 {
			typeSet := make(map[string]bool)
			for _, t := range p.Types {
				typeSet[t] = true
			}
			var filtered []event.Event
			for _, e := range events {
				if typeSet[string(e.Kind)] {
					filtered = append(filtered, e)
				}
			}
			events = filtered
		}

		// Build response items.
		items := make([]timelineItem, 0, len(events))
		for _, e := range events {
			items = append(items, timelineItem{
				Timestamp: e.Timestamp.Format(time.RFC3339),
				Kind:      string(e.Kind),
				Summary:   summarizeEvent(e),
				Detail:    e.Payload,
			})
		}

		payload, _ := json.Marshal(map[string]any{
			"events": items,
			"total":  total,
		})
		return socket.Response{OK: true, Payload: payload}
	})
}

type timelineItem struct {
	Timestamp string         `json:"timestamp"`
	Kind      string         `json:"kind"`
	Summary   string         `json:"summary"`
	Detail    map[string]any `json:"detail,omitempty"`
}

// summarizeEvent is defined in main.go and reused here.
