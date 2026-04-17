// Package corpus manages the training corpus pipeline: host event ingestion,
// annotation via local inference, retention vacuum, and CLI query operations.
//
// DAG position: imports store, inference, config, filter.
// Must NOT import analyzer, notifier, actuator, socket, merge, or collector.
package corpus

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/sigil-tech/sigil/internal/config"
	"github.com/sigil-tech/sigil/internal/filter"
	"github.com/sigil-tech/sigil/internal/inference"
)

// ValidLabels is the closed enum of task labels.
var ValidLabels = map[string]bool{
	"coding":        true,
	"reviewing":     true,
	"debugging":     true,
	"researching":   true,
	"communicating": true,
	"deploying":     true,
	"idle":          true,
}

// ValidPhases is the closed enum of session phases.
var ValidPhases = map[string]bool{
	"deep_work":      true,
	"context_switch": true,
	"ramp_up":        true,
	"wind_down":      true,
	"blocked":        true,
	"flow":           true,
}

// Corpus manages the training corpus pipeline.
type Corpus struct {
	db      *sql.DB
	engine  *inference.Engine
	cfg     *config.Config
	hmacKey []byte
	log     *slog.Logger
}

// New creates a new Corpus instance.
func New(db *sql.DB, engine *inference.Engine, cfg *config.Config, hmacKey []byte, log *slog.Logger) *Corpus {
	return &Corpus{
		db:      db,
		engine:  engine,
		cfg:     cfg,
		hmacKey: hmacKey,
		log:     log,
	}
}

// RunAnnotator runs the corpus annotator on a timer. It blocks until ctx is cancelled.
func (c *Corpus) RunAnnotator(ctx context.Context) {
	interval := c.cfg.Corpus.AnnotationIntervalDuration()
	c.log.Info("corpus annotator starting", "interval", interval)

	// Run once immediately.
	c.annotate(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.annotate(ctx)
		}
	}
}

// RunVacuum runs the retention vacuum on a timer. It blocks until ctx is cancelled.
func (c *Corpus) RunVacuum(ctx context.Context) {
	interval := c.cfg.Corpus.VacuumIntervalDuration()
	c.log.Info("corpus vacuum starting", "interval", interval)

	// Check if we need to run immediately (missed vacuum).
	c.maybeRunVacuum(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.vacuum(ctx)
		}
	}
}

// IngestHostEvents takes events from the latest analyzer cycle and ingests them
// into training_corpus with origin='host'. Events are filtered through the
// denylist and deduplicated by payload_hash + hour window.
func (c *Corpus) IngestHostEvents(ctx context.Context, events []HostEvent) (ingested, filtered int) {
	denylist := c.cfg.Merge.EffectiveDenylist()

	for _, ev := range events {
		// Decode payload for filter inspection.
		var decoded map[string]any
		if err := json.Unmarshal(ev.Payload, &decoded); err != nil {
			c.log.Debug("corpus: skipping malformed payload", "event_type", ev.Kind)
			continue
		}

		// Denylist filter.
		if pattern, hit := filter.WalkPayloadStrings(decoded, denylist); hit {
			c.insertFilteredLog(ctx, "host-default", ev, "denylist:"+pattern, "content_match")
			filtered++
			continue
		}

		// Strip process args.
		if ev.Kind == "process" {
			decoded = filter.StripProcessArgs(decoded)
			cleaned, err := json.Marshal(decoded)
			if err != nil {
				c.log.Warn("corpus: re-marshal process payload failed", "error", err)
				continue
			}
			ev.Payload = cleaned
		}

		// Network destination filter.
		if ev.Kind == "net.connect" {
			dest, _ := decoded["dest"].(string)
			if dest == "" {
				dest, _ = decoded["destination"].(string)
			}
			if filter.IsRFC1918(dest) || filter.IsInternalHostname(dest) {
				c.insertFilteredLog(ctx, "host-default", ev, "private_destination", "row_excluded")
				filtered++
				continue
			}
		}

		// Compute HMAC payload hash.
		hash := filter.PayloadHashHMAC(ev.Payload, c.hmacKey)

		// Dedup check: same event_type + source + payload_hash within the same hour.
		hourWindow := ev.Timestamp / 3600000 // truncate to hour boundary
		var exists int
		err := c.db.QueryRowContext(ctx,
			`SELECT 1 FROM training_corpus
			 WHERE event_type = ? AND source = ? AND payload_hash = ? AND (ts / 3600000) = ?
			 LIMIT 1`,
			ev.Kind, ev.Source, hash, hourWindow,
		).Scan(&exists)
		if err == nil {
			// Duplicate — skip.
			continue
		}

		// Insert into training_corpus.
		_, err = c.db.ExecContext(ctx,
			`INSERT INTO training_corpus
			 (ts, origin, origin_session, event_type, source, payload, payload_size_bytes, filter_version, vm_row_id, payload_hash)
			 VALUES (?, 'host', 'host-default', ?, ?, ?, ?, ?, 0, ?)`,
			ev.Timestamp, ev.Kind, ev.Source, ev.Payload, len(ev.Payload),
			c.cfg.Merge.FilterVersion, hash,
		)
		if err != nil {
			c.log.Error("corpus: insert training_corpus failed", "error", err)
			continue
		}
		ingested++
	}

	if ingested > 0 || filtered > 0 {
		c.log.Info("corpus: host ingestion complete", "ingested", ingested, "filtered", filtered)
	}
	return ingested, filtered
}

// HostEvent represents a raw event for corpus ingestion.
type HostEvent struct {
	Kind      string
	Source    string
	Payload   []byte
	Timestamp int64 // Unix milliseconds
}

// annotate enriches unannotated training_corpus rows with labels and phases.
func (c *Corpus) annotate(ctx context.Context) {
	batchSize := c.cfg.Corpus.AnnotationBatchSizeOrDefault()

	// Check backlog size.
	var backlog int
	_ = c.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM training_corpus WHERE label IS NULL`,
	).Scan(&backlog)
	if backlog > 10000 {
		c.log.Warn("corpus: annotation backlog exceeds 10000", "backlog", backlog)
	}
	if backlog == 0 {
		return
	}

	rows, err := c.db.QueryContext(ctx,
		`SELECT id, event_type, source, ts FROM training_corpus WHERE label IS NULL ORDER BY id LIMIT ?`,
		batchSize,
	)
	if err != nil {
		c.log.Error("corpus: query unannotated rows", "error", err)
		return
	}
	defer rows.Close()

	type corpusRow struct {
		id        int64
		eventType string
		source    string
		ts        int64
	}
	var batch []corpusRow
	for rows.Next() {
		var r corpusRow
		if err := rows.Scan(&r.id, &r.eventType, &r.source, &r.ts); err != nil {
			c.log.Error("corpus: scan unannotated row", "error", err)
			return
		}
		batch = append(batch, r)
	}
	if err := rows.Err(); err != nil {
		c.log.Error("corpus: iterate unannotated rows", "error", err)
		return
	}

	annotated := 0
	for _, r := range batch {
		if ctx.Err() != nil {
			return
		}

		// Build a minimal prompt for annotation. Only event_type, source, and
		// coarsened timestamp are included — never raw payloads.
		hour := time.UnixMilli(r.ts).UTC().Format("2006-01-02T15")
		prompt := fmt.Sprintf(
			"Classify this developer activity event.\nEvent type: %s\nSource: %s\nTime: %s\n\n"+
				"Respond with exactly two words: a task label and a phase.\n"+
				"Valid labels: coding, reviewing, debugging, researching, communicating, deploying, idle\n"+
				"Valid phases: deep_work, context_switch, ramp_up, wind_down, blocked, flow\n"+
				"Example: coding deep_work",
			r.eventType, r.source, hour,
		)

		result, err := c.engine.Complete(ctx,
			"You are a developer activity classifier. Respond with exactly two words: label phase.",
			prompt,
		)
		if err != nil {
			c.log.Warn("corpus: annotation inference failed", "error", err)
			return // Retry next cycle.
		}

		label, phase, confidence := parseAnnotation(result.Content)
		if !ValidLabels[label] {
			c.log.Warn("corpus: rejected unknown label",
				"corpus_row_id", r.id,
				"event_type", r.eventType,
				"rejected_value", label,
			)
			continue
		}
		if !ValidPhases[phase] {
			c.log.Warn("corpus: rejected unknown phase",
				"corpus_row_id", r.id,
				"event_type", r.eventType,
				"rejected_value", phase,
			)
			continue
		}

		now := time.Now().UnixMilli()
		_, err = c.db.ExecContext(ctx,
			`UPDATE training_corpus SET label = ?, phase = ?, confidence = ?, annotated_at = ? WHERE id = ?`,
			label, phase, confidence, now, r.id,
		)
		if err != nil {
			c.log.Error("corpus: update annotation", "error", err, "id", r.id)
			continue
		}
		annotated++
	}

	c.log.Info("corpus: annotation cycle complete",
		"annotated", annotated,
		"batch_size", len(batch),
	)
}

// parseAnnotation extracts label, phase, and confidence from the LLM response.
func parseAnnotation(response string) (label, phase string, confidence float64) {
	// Expected format: "label phase" (two words)
	parts := splitWords(response)
	if len(parts) >= 2 {
		label = parts[0]
		phase = parts[1]
		confidence = 0.8 // Default confidence for clean parses.
	} else if len(parts) == 1 {
		label = parts[0]
		phase = "deep_work" // Fallback.
		confidence = 0.4
	}
	return label, phase, confidence
}

// splitWords splits a string into lowercase words, ignoring punctuation.
func splitWords(s string) []string {
	var words []string
	word := ""
	for _, c := range s {
		if c == ' ' || c == '\n' || c == '\t' || c == ',' || c == '.' {
			if word != "" {
				words = append(words, word)
				word = ""
			}
		} else {
			word += string(c)
		}
	}
	if word != "" {
		words = append(words, word)
	}
	// Lowercase all.
	for i := range words {
		words[i] = toLower(words[i])
	}
	return words
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

// vacuum deletes corpus rows older than retention_days and enforces max_size_mb.
func (c *Corpus) vacuum(ctx context.Context) {
	retentionDays := c.cfg.Corpus.RetentionDaysOrDefault()
	cutoff := time.Now().AddDate(0, 0, -retentionDays).UnixMilli()

	result, err := c.db.ExecContext(ctx,
		`DELETE FROM training_corpus WHERE ts < ?`, cutoff,
	)
	if err != nil {
		c.log.Error("corpus: vacuum retention delete", "error", err)
		return
	}
	deleted, _ := result.RowsAffected()

	// Enforce max_size_mb by deleting oldest rows until under budget.
	maxBytes := c.cfg.Corpus.MaxSizeBytes()
	var totalSize int64
	_ = c.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(payload_size_bytes), 0) FROM training_corpus`,
	).Scan(&totalSize)

	sizeDeleted := int64(0)
	for totalSize > maxBytes {
		res, err := c.db.ExecContext(ctx,
			`DELETE FROM training_corpus WHERE id IN (SELECT id FROM training_corpus ORDER BY ts ASC LIMIT 100)`,
		)
		if err != nil {
			c.log.Error("corpus: vacuum size delete", "error", err)
			break
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			break
		}
		sizeDeleted += n
		_ = c.db.QueryRowContext(ctx,
			`SELECT COALESCE(SUM(payload_size_bytes), 0) FROM training_corpus`,
		).Scan(&totalSize)
	}

	// Record last vacuum timestamp.
	_, _ = c.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO kv_meta (key, value) VALUES ('last_corpus_vacuum', ?)`,
		fmt.Sprintf("%d", time.Now().UnixMilli()),
	)

	if deleted > 0 || sizeDeleted > 0 {
		c.log.Info("corpus: vacuum complete",
			"retention_deleted", deleted,
			"size_deleted", sizeDeleted,
		)
	}
}

// maybeRunVacuum checks if the vacuum should fire immediately (missed schedule).
func (c *Corpus) maybeRunVacuum(ctx context.Context) {
	var lastVacuumStr string
	err := c.db.QueryRowContext(ctx,
		`SELECT value FROM kv_meta WHERE key = 'last_corpus_vacuum'`,
	).Scan(&lastVacuumStr)
	if err != nil {
		// No record — run vacuum now.
		c.vacuum(ctx)
		return
	}

	var lastVacuumMS int64
	fmt.Sscanf(lastVacuumStr, "%d", &lastVacuumMS)
	if lastVacuumMS == 0 {
		c.vacuum(ctx)
		return
	}

	interval := c.cfg.Corpus.VacuumIntervalDuration()
	if time.Since(time.UnixMilli(lastVacuumMS)) > interval {
		c.vacuum(ctx)
	}
}

// insertFilteredLog records an excluded host event in the filtered_log table.
func (c *Corpus) insertFilteredLog(ctx context.Context, sessionID string, ev HostEvent, filterRule, excludedReason string) {
	hash := filter.PayloadHashHMAC(ev.Payload, c.hmacKey)
	_, err := c.db.ExecContext(ctx,
		`INSERT INTO filtered_log (session_id, ts, event_type, filter_rule, excluded_reason, payload_hash)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		sessionID, ev.Timestamp, ev.Kind, filterRule, excludedReason, hash,
	)
	if err != nil {
		c.log.Error("corpus: insert filtered_log", "error", err)
	}
}
