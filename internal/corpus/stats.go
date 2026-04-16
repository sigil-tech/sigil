package corpus

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/sigil-tech/sigil/internal/filter"
)

// Stats holds aggregated corpus statistics.
type Stats struct {
	TotalRows        int            `json:"total_rows"`
	RowsByOrigin     map[string]int `json:"rows_by_origin"`
	LabelDist        map[string]int `json:"label_distribution"`
	AnnotatedCount   int            `json:"annotated_count"`
	UnannotatedCount int            `json:"unannotated_count"`
	OldestTS         int64          `json:"oldest_ts"`
	NewestTS         int64          `json:"newest_ts"`
}

// QueryStats returns aggregated corpus statistics.
func QueryStats(ctx context.Context, db *sql.DB) (*Stats, error) {
	s := &Stats{
		RowsByOrigin: make(map[string]int),
		LabelDist:    make(map[string]int),
	}

	// Total rows.
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM training_corpus`).Scan(&s.TotalRows); err != nil {
		return nil, fmt.Errorf("corpus stats: count: %w", err)
	}
	if s.TotalRows == 0 {
		return s, nil
	}

	// Rows by origin.
	rows, err := db.QueryContext(ctx, `SELECT origin, COUNT(*) FROM training_corpus GROUP BY origin`)
	if err != nil {
		return nil, fmt.Errorf("corpus stats: origin: %w", err)
	}
	for rows.Next() {
		var origin string
		var count int
		if err := rows.Scan(&origin, &count); err != nil {
			rows.Close()
			return nil, err
		}
		s.RowsByOrigin[origin] = count
	}
	rows.Close()

	// Label distribution.
	rows, err = db.QueryContext(ctx,
		`SELECT COALESCE(label, '(unlabeled)'), COUNT(*) FROM training_corpus GROUP BY label`)
	if err != nil {
		return nil, fmt.Errorf("corpus stats: labels: %w", err)
	}
	for rows.Next() {
		var label string
		var count int
		if err := rows.Scan(&label, &count); err != nil {
			rows.Close()
			return nil, err
		}
		s.LabelDist[label] = count
	}
	rows.Close()

	// Annotated/unannotated counts.
	_ = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM training_corpus WHERE label IS NOT NULL`).Scan(&s.AnnotatedCount)
	s.UnannotatedCount = s.TotalRows - s.AnnotatedCount

	// Date range.
	_ = db.QueryRowContext(ctx,
		`SELECT MIN(ts), MAX(ts) FROM training_corpus`).Scan(&s.OldestTS, &s.NewestTS)

	return s, nil
}

// PurgeResult holds the result of a purge operation.
type PurgeResult struct {
	RowsDeleted int `json:"rows_deleted"`
}

// Purge deletes corpus rows matching the given criteria.
func Purge(ctx context.Context, db *sql.DB, beforeTS int64) (*PurgeResult, error) {
	result, err := db.ExecContext(ctx,
		`DELETE FROM training_corpus WHERE ts < ?`, beforeTS,
	)
	if err != nil {
		return nil, fmt.Errorf("corpus purge: %w", err)
	}
	n, _ := result.RowsAffected()

	// Write audit entry.
	_, _ = db.ExecContext(ctx,
		`INSERT INTO action_log (action_id, description, created_at, expires_at)
		 VALUES (?, ?, ?, ?)`,
		fmt.Sprintf("corpus-purge-%d", time.Now().UnixMilli()),
		fmt.Sprintf("Purged %d corpus rows before %s", n, time.UnixMilli(beforeTS).Format("2006-01-02")),
		time.Now().UnixMilli(),
		time.Now().Add(365*24*time.Hour).UnixMilli(),
	)

	return &PurgeResult{RowsDeleted: int(n)}, nil
}

// ExportRow represents a single exported corpus row.
type ExportRow struct {
	EventType   string   `json:"event_type"`
	Source      string   `json:"source"`
	Label       *string  `json:"label,omitempty"`
	Phase       *string  `json:"phase,omitempty"`
	Confidence  *float64 `json:"confidence,omitempty"`
	Timestamp   int64    `json:"ts"`
	PayloadHash string   `json:"payload_hash"`
	Origin      string   `json:"origin"`
}

// Export writes annotated corpus rows as JSONL to the given file path.
func Export(ctx context.Context, db *sql.DB, outputPath string, hmacKey []byte) (int, error) {
	_ = hmacKey
	_ = filter.PayloadHashHMAC // ensure import is used

	f, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return 0, fmt.Errorf("corpus export: create file: %w", err)
	}
	defer func() {
		f.Close()
		// If we had an error, remove the partial file.
	}()

	rows, err := db.QueryContext(ctx,
		`SELECT event_type, source, label, phase, confidence, ts, payload_hash, origin
		 FROM training_corpus
		 WHERE label IS NOT NULL
		 ORDER BY ts ASC`,
	)
	if err != nil {
		os.Remove(outputPath)
		return 0, fmt.Errorf("corpus export: query: %w", err)
	}
	defer rows.Close()

	enc := json.NewEncoder(f)
	count := 0
	for rows.Next() {
		var r ExportRow
		if err := rows.Scan(&r.EventType, &r.Source, &r.Label, &r.Phase, &r.Confidence, &r.Timestamp, &r.PayloadHash, &r.Origin); err != nil {
			os.Remove(outputPath)
			return 0, fmt.Errorf("corpus export: scan: %w", err)
		}
		if err := enc.Encode(r); err != nil {
			os.Remove(outputPath)
			return 0, fmt.Errorf("corpus export: encode: %w", err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		os.Remove(outputPath)
		return 0, fmt.Errorf("corpus export: iterate: %w", err)
	}

	return count, nil
}
