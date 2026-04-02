package main

import (
	"context"
	"time"

	fleet "github.com/sigil-tech/sigil-fleet"
	"github.com/sigil-tech/sigil/internal/event"
	"github.com/sigil-tech/sigil/internal/store"
)

// fleetStore wraps a store.Store to satisfy fleet.EventReader,
// converting between internal types and fleet-owned types.
type fleetStore struct {
	db *store.Store
}

func newFleetStore(db *store.Store) *fleetStore {
	return &fleetStore{db: db}
}

func (f *fleetStore) QueryAIInteractions(ctx context.Context, since time.Time) ([]fleet.AIInteraction, error) {
	ais, err := f.db.QueryAIInteractions(ctx, since)
	if err != nil {
		return nil, err
	}
	out := make([]fleet.AIInteraction, len(ais))
	for i, ai := range ais {
		out[i] = fleet.AIInteraction{
			QueryCategory: ai.QueryCategory,
			Routing:       ai.Routing,
		}
	}
	return out, nil
}

func (f *fleetStore) QuerySuggestionAcceptanceRate(ctx context.Context, since time.Time) (float64, error) {
	return f.db.QuerySuggestionAcceptanceRate(ctx, since)
}

func (f *fleetStore) CountEvents(ctx context.Context, kind fleet.EventKind, since time.Time) (int64, error) {
	return f.db.CountEvents(ctx, event.Kind(kind), since)
}

func (f *fleetStore) QueryTerminalEvents(ctx context.Context, since time.Time) ([]fleet.TerminalEvent, error) {
	events, err := f.db.QueryTerminalEvents(ctx, since)
	if err != nil {
		return nil, err
	}
	out := make([]fleet.TerminalEvent, len(events))
	for i, e := range events {
		out[i] = fleet.TerminalEvent{Payload: e.Payload}
	}
	return out, nil
}

func (f *fleetStore) QueryTaskMetrics(ctx context.Context, since time.Time) (fleet.TaskMetrics, error) {
	m, err := f.db.QueryTaskMetrics(ctx, since)
	if err != nil {
		return fleet.TaskMetrics{}, err
	}
	return fleet.TaskMetrics{
		TasksCompleted:    m.TasksCompleted,
		TasksStarted:      m.TasksStarted,
		AvgDurationMin:    m.AvgDurationMin,
		StuckRate:         m.StuckRate,
		PhaseDistribution: m.PhaseDistribution,
	}, nil
}

func (f *fleetStore) QueryTasksByDate(ctx context.Context, date time.Time) ([]fleet.TaskRecord, error) {
	tasks, err := f.db.QueryTasksByDate(ctx, date)
	if err != nil {
		return nil, err
	}
	out := make([]fleet.TaskRecord, len(tasks))
	for i, t := range tasks {
		out[i] = fleet.TaskRecord{
			Files:       t.Files,
			StartedAt:   t.StartedAt,
			CompletedAt: t.CompletedAt,
		}
	}
	return out, nil
}

func (f *fleetStore) QueryMLStats(ctx context.Context, since time.Time) (fleet.MLStats, error) {
	m, err := f.db.QueryMLStats(ctx, since)
	if err != nil {
		return fleet.MLStats{}, err
	}
	return fleet.MLStats{
		Predictions:  m.Predictions,
		RetrainCount: m.RetrainCount,
	}, nil
}
