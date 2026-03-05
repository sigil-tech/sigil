// Package store provides the local SQLite persistence layer for aetherd.
// All raw telemetry is stored here and never leaves the machine.
// The store is opened in WAL mode to allow the analyzer to read while
// the collector writes.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/wambozi/aether/internal/event"
	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// Store wraps a SQLite database and exposes typed read/write methods.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) the SQLite database at path and runs the schema
// migrations.  The caller is responsible for calling Close.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}

	// Single writer, multiple readers — optimal for our access pattern.
	db.SetMaxOpenConns(1)

	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}

	return &Store{db: db}, nil
}

// Close releases the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// InsertEvent persists a raw observation event.
func (s *Store) InsertEvent(ctx context.Context, e event.Event) error {
	payload, err := json.Marshal(e.Payload)
	if err != nil {
		return fmt.Errorf("store: marshal payload: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO events (kind, source, payload, ts) VALUES (?, ?, ?, ?)`,
		string(e.Kind),
		e.Source,
		string(payload),
		e.Timestamp.UnixMilli(),
	)
	return err
}

// InsertAIInteraction persists a single AI interaction record.
func (s *Store) InsertAIInteraction(ctx context.Context, ai event.AIInteraction) error {
	accepted := 0
	if ai.Accepted {
		accepted = 1
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO ai_interactions
		 (query_text, query_category, routing, latency_ms, accepted, ts)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		ai.QueryText,
		ai.QueryCategory,
		ai.Routing,
		ai.LatencyMS,
		accepted,
		ai.Timestamp.UnixMilli(),
	)
	return err
}

// QueryEvents returns the most recent n events, optionally filtered by kind.
// Pass an empty string for kind to return all events.
func (s *Store) QueryEvents(ctx context.Context, kind event.Kind, n int) ([]event.Event, error) {
	var (
		rows *sql.Rows
		err  error
	)

	if kind == "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, kind, source, payload, ts FROM events ORDER BY ts DESC LIMIT ?`, n)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, kind, source, payload, ts FROM events WHERE kind = ? ORDER BY ts DESC LIMIT ?`,
			string(kind), n)
	}
	if err != nil {
		return nil, fmt.Errorf("store: query events: %w", err)
	}
	defer rows.Close()

	events := make([]event.Event, 0)
	for rows.Next() {
		var (
			e       event.Event
			payload string
			tsMS    int64
		)
		if err := rows.Scan(&e.ID, (*string)(&e.Kind), &e.Source, &payload, &tsMS); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(payload), &e.Payload); err != nil {
			return nil, err
		}
		e.Timestamp = time.UnixMilli(tsMS)
		events = append(events, e)
	}
	return events, rows.Err()
}

// CountEvents returns the total number of stored events, optionally filtered
// by kind and a start time.  Used by the analyzer for frequency scoring.
func (s *Store) CountEvents(ctx context.Context, kind event.Kind, since time.Time) (int64, error) {
	var count int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events WHERE kind = ? AND ts >= ?`,
		string(kind), since.UnixMilli(),
	).Scan(&count)
	return count, err
}

// InsertPattern writes (or replaces) an analyzer-derived pattern.
func (s *Store) InsertPattern(ctx context.Context, kind string, summary any) error {
	blob, err := json.Marshal(summary)
	if err != nil {
		return fmt.Errorf("store: marshal pattern: %w", err)
	}
	now := time.Now().UnixMilli()

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO patterns (kind, summary, created_at, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(kind) DO UPDATE SET summary = excluded.summary, updated_at = excluded.updated_at`,
		kind, string(blob), now, now,
	)
	return err
}

// --- Suggestions -----------------------------------------------------------

// SuggestionStatus is the lifecycle state of a surfaced suggestion.
type SuggestionStatus string

const (
	StatusPending   SuggestionStatus = "pending"
	StatusShown     SuggestionStatus = "shown"
	StatusAccepted  SuggestionStatus = "accepted"
	StatusDismissed SuggestionStatus = "dismissed"
	StatusIgnored   SuggestionStatus = "ignored"
)

// Suggestion is a single insight produced by the analyzer and tracked through
// its full lifecycle (created → shown → accepted/dismissed/ignored).
type Suggestion struct {
	ID         int64            `json:"id,omitempty"`
	Category   string           `json:"category"`   // "pattern", "reminder", "optimization", "insight"
	Confidence float64          `json:"confidence"` // 0.0-1.0
	Title      string           `json:"title"`
	Body       string           `json:"body"`
	ActionCmd  string           `json:"action_cmd,omitempty"` // optional shell command
	Status     SuggestionStatus `json:"status"`
	CreatedAt  time.Time        `json:"created_at"`
	ShownAt    *time.Time       `json:"shown_at,omitempty"`
	ResolvedAt *time.Time       `json:"resolved_at,omitempty"`
}

// InsertSuggestion persists a new suggestion and returns its assigned ID.
func (s *Store) InsertSuggestion(ctx context.Context, sg Suggestion) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO suggestions (category, confidence, title, body, action_cmd, status, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sg.Category, sg.Confidence, sg.Title, sg.Body,
		sg.ActionCmd, string(StatusPending), sg.CreatedAt.UnixMilli(),
	)
	if err != nil {
		return 0, fmt.Errorf("store: insert suggestion: %w", err)
	}
	return res.LastInsertId()
}

// UpdateSuggestionStatus advances a suggestion's status and records the
// shown_at / resolved_at timestamps where appropriate.
func (s *Store) UpdateSuggestionStatus(ctx context.Context, id int64, status SuggestionStatus) error {
	now := time.Now().UnixMilli()
	var err error
	switch status {
	case StatusShown:
		_, err = s.db.ExecContext(ctx,
			`UPDATE suggestions SET status = ?, shown_at = ? WHERE id = ?`,
			string(status), now, id)
	case StatusAccepted, StatusDismissed, StatusIgnored:
		_, err = s.db.ExecContext(ctx,
			`UPDATE suggestions SET status = ?, resolved_at = ? WHERE id = ?`,
			string(status), now, id)
	default:
		_, err = s.db.ExecContext(ctx,
			`UPDATE suggestions SET status = ? WHERE id = ?`,
			string(status), id)
	}
	return err
}

// QuerySuggestions returns the most recent n suggestions, optionally filtered
// by status.  Pass an empty string to return all statuses.
func (s *Store) QuerySuggestions(ctx context.Context, status SuggestionStatus, n int) ([]Suggestion, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if status == "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, category, confidence, title, body, action_cmd, status, created_at, shown_at, resolved_at
			 FROM suggestions ORDER BY created_at DESC LIMIT ?`, n)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, category, confidence, title, body, action_cmd, status, created_at, shown_at, resolved_at
			 FROM suggestions WHERE status = ? ORDER BY created_at DESC LIMIT ?`,
			string(status), n)
	}
	if err != nil {
		return nil, fmt.Errorf("store: query suggestions: %w", err)
	}
	defer rows.Close()

	out := make([]Suggestion, 0)
	for rows.Next() {
		var (
			sg            Suggestion
			actionCmd     sql.NullString
			createdAtMS   int64
			shownAtMS     sql.NullInt64
			resolvedAtMS  sql.NullInt64
		)
		if err := rows.Scan(
			&sg.ID, &sg.Category, &sg.Confidence, &sg.Title, &sg.Body,
			&actionCmd, (*string)(&sg.Status),
			&createdAtMS, &shownAtMS, &resolvedAtMS,
		); err != nil {
			return nil, err
		}
		sg.ActionCmd = actionCmd.String
		sg.CreatedAt = time.UnixMilli(createdAtMS)
		if shownAtMS.Valid {
			t := time.UnixMilli(shownAtMS.Int64)
			sg.ShownAt = &t
		}
		if resolvedAtMS.Valid {
			t := time.UnixMilli(resolvedAtMS.Int64)
			sg.ResolvedAt = &t
		}
		out = append(out, sg)
	}
	return out, rows.Err()
}

// --- Feedback --------------------------------------------------------------

// InsertFeedback records the outcome of a surfaced suggestion.
func (s *Store) InsertFeedback(ctx context.Context, suggestionID int64, outcome string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO feedback (suggestion_id, outcome, ts) VALUES (?, ?, ?)`,
		suggestionID, outcome, time.Now().UnixMilli(),
	)
	return err
}

// migrate creates all tables and indexes if they do not already exist.
// Idempotent — safe to call on every startup.
func migrate(db *sql.DB) error {
	schema := `
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS events (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    kind    TEXT    NOT NULL,
    source  TEXT    NOT NULL,
    payload TEXT    NOT NULL,
    ts      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_events_kind ON events (kind);
CREATE INDEX IF NOT EXISTS idx_events_ts   ON events (ts);

CREATE TABLE IF NOT EXISTS ai_interactions (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    query_text     TEXT,
    query_category TEXT,
    routing        TEXT    NOT NULL,
    latency_ms     INTEGER NOT NULL,
    accepted       INTEGER NOT NULL DEFAULT 0,
    ts             INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_ai_ts ON ai_interactions (ts);

CREATE TABLE IF NOT EXISTS patterns (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    kind       TEXT    NOT NULL UNIQUE,
    summary    TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS suggestions (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    category    TEXT    NOT NULL,
    confidence  REAL    NOT NULL,
    title       TEXT    NOT NULL,
    body        TEXT    NOT NULL,
    action_cmd  TEXT,
    status      TEXT    NOT NULL DEFAULT 'pending',
    created_at  INTEGER NOT NULL,
    shown_at    INTEGER,
    resolved_at INTEGER
);
CREATE INDEX IF NOT EXISTS idx_suggestions_status ON suggestions (status);

CREATE TABLE IF NOT EXISTS feedback (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    suggestion_id INTEGER NOT NULL REFERENCES suggestions(id),
    outcome       TEXT    NOT NULL,
    ts            INTEGER NOT NULL
);`

	_, err := db.Exec(schema)
	return err
}
