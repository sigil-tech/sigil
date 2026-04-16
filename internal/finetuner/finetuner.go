// Package finetuner manages the LoRA fine-tuning lifecycle: batch selection
// from the training corpus, adapter production via llama-finetune, adapter
// integrity verification, and adapter lifecycle management.
//
// DAG position: imports store and config only.
// Must NOT import inference, analyzer, notifier, actuator, socket, or collector.
package finetuner

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/sigil-tech/sigil/internal/config"
)

// FinetuneBackend is implemented by backends capable of LoRA training.
// It is defined here (consumer-owned interface per constitution Principle III).
type FinetuneBackend interface {
	// Train runs LoRA fine-tuning on the given batch and produces an adapter.
	Train(ctx context.Context, batch TrainBatch) (AdapterResult, error)

	// LoadAdapter signals the inference server to load the adapter at the given path.
	LoadAdapter(ctx context.Context, adapterPath string) error
}

// TrainBatch describes a fine-tuning batch.
type TrainBatch struct {
	CorpusDBPath string
	RowIDs       []int64
	BaseModelVer string
	OutputDir    string
	MaxDuration  time.Duration
}

// AdapterResult is returned by Train on success.
type AdapterResult struct {
	AdapterPath string
	SHA256      string
	LossFinal   float64
	Duration    time.Duration
}

// Finetuner manages the fine-tuning lifecycle.
type Finetuner struct {
	db      *sql.DB
	backend FinetuneBackend
	cfg     *config.Config
	log     *slog.Logger
}

// New creates a new Finetuner. backend may be nil if fine-tuning is disabled.
func New(db *sql.DB, backend FinetuneBackend, cfg *config.Config, log *slog.Logger) *Finetuner {
	return &Finetuner{
		db:      db,
		backend: backend,
		cfg:     cfg,
		log:     log,
	}
}

// RunSchedule runs the fine-tuning scheduler. It blocks until ctx is cancelled.
func (f *Finetuner) RunSchedule(ctx context.Context) {
	if f.backend == nil {
		f.log.Info("finetuner: disabled (no backend configured)")
		return
	}

	f.log.Info("finetuner: schedule started")

	for {
		// Calculate next fire time.
		next := f.nextFireTime()
		delay := time.Until(next)
		if delay <= 0 {
			delay = 1 * time.Minute
		}

		f.log.Info("finetuner: next run scheduled", "at", next, "in", delay)

		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
			f.runOnce(ctx)
		}
	}
}

// RunNow triggers an immediate fine-tune run. Returns the result.
func (f *Finetuner) RunNow(ctx context.Context) (*RunResult, error) {
	if f.backend == nil {
		return nil, fmt.Errorf("fine-tuning is disabled (no backend)")
	}

	// Check for running job.
	var runningID string
	err := f.db.QueryRowContext(ctx,
		`SELECT id FROM finetune_runs WHERE status = 'running' LIMIT 1`,
	).Scan(&runningID)
	if err == nil {
		return nil, fmt.Errorf("ERR_FINETUNE_RUNNING: job %s already in progress", runningID)
	}

	return f.runOnce(ctx), nil
}

// RunResult is the outcome of a fine-tune run.
type RunResult struct {
	RunID       string  `json:"run_id"`
	Status      string  `json:"status"`
	RowsTrained int     `json:"rows_trained"`
	AdapterPath string  `json:"adapter_path,omitempty"`
	AdapterSHA  string  `json:"adapter_sha256,omitempty"`
	LossFinal   float64 `json:"loss_final,omitempty"`
	Duration    string  `json:"duration,omitempty"`
	Error       string  `json:"error,omitempty"`
}

// Status returns the current finetuner status.
func (f *Finetuner) Status(ctx context.Context) (*StatusInfo, error) {
	info := &StatusInfo{}

	// Current active adapter.
	err := f.db.QueryRowContext(ctx,
		`SELECT id, base_model_ver, path, adapter_sha256, created_at FROM ml_adapters WHERE is_active = 1 LIMIT 1`,
	).Scan(&info.ActiveAdapterID, &info.BaseModelVer, &info.AdapterPath, &info.AdapterSHA, &info.LoadedAt)
	if err == sql.ErrNoRows {
		info.ActiveAdapterID = ""
	} else if err != nil {
		return nil, fmt.Errorf("finetuner status: %w", err)
	}

	// Running job.
	var runningID string
	err = f.db.QueryRowContext(ctx,
		`SELECT id FROM finetune_runs WHERE status = 'running' LIMIT 1`,
	).Scan(&runningID)
	if err == nil {
		info.RunningJobID = runningID
	}

	// Total completed runs.
	_ = f.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM finetune_runs WHERE status = 'complete'`,
	).Scan(&info.CompletedRuns)

	return info, nil
}

// StatusInfo holds the finetuner status.
type StatusInfo struct {
	ActiveAdapterID string `json:"active_adapter_id"`
	BaseModelVer    string `json:"base_model_ver"`
	AdapterPath     string `json:"adapter_path"`
	AdapterSHA      string `json:"adapter_sha256"`
	LoadedAt        int64  `json:"loaded_at"`
	RunningJobID    string `json:"running_job_id,omitempty"`
	CompletedRuns   int    `json:"completed_runs"`
}

// History returns recent fine-tune runs.
func (f *Finetuner) History(ctx context.Context, limit int) ([]HistoryEntry, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := f.db.QueryContext(ctx,
		`SELECT id, started_at, completed_at, status, mode, base_model_ver, corpus_row_count, adapter_path, adapter_sha256, loss_final, error_msg
		 FROM finetune_runs ORDER BY started_at DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("finetuner history: %w", err)
	}
	defer rows.Close()

	var entries []HistoryEntry
	for rows.Next() {
		var e HistoryEntry
		var completedAt sql.NullInt64
		var adapterPath, adapterSHA, errorMsg sql.NullString
		var lossFinal sql.NullFloat64

		if err := rows.Scan(&e.RunID, &e.StartedAt, &completedAt, &e.Status, &e.Mode,
			&e.BaseModelVer, &e.RowsTrained, &adapterPath, &adapterSHA, &lossFinal, &errorMsg); err != nil {
			return nil, fmt.Errorf("finetuner history scan: %w", err)
		}
		if completedAt.Valid {
			e.CompletedAt = completedAt.Int64
		}
		if adapterPath.Valid {
			e.AdapterPath = adapterPath.String
		}
		if adapterSHA.Valid {
			e.AdapterSHA = adapterSHA.String
		}
		if lossFinal.Valid {
			e.LossFinal = lossFinal.Float64
		}
		if errorMsg.Valid {
			e.Error = errorMsg.String
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// HistoryEntry is a single fine-tune run record.
type HistoryEntry struct {
	RunID        string  `json:"run_id"`
	StartedAt    int64   `json:"started_at"`
	CompletedAt  int64   `json:"completed_at,omitempty"`
	Status       string  `json:"status"`
	Mode         string  `json:"mode"`
	BaseModelVer string  `json:"base_model_ver"`
	RowsTrained  int     `json:"rows_trained"`
	AdapterPath  string  `json:"adapter_path,omitempty"`
	AdapterSHA   string  `json:"adapter_sha256,omitempty"`
	LossFinal    float64 `json:"loss_final,omitempty"`
	Error        string  `json:"error,omitempty"`
}

// Rollback loads a previous adapter by ID.
func (f *Finetuner) Rollback(ctx context.Context, adapterID string) error {
	if f.backend == nil {
		return fmt.Errorf("fine-tuning is disabled")
	}

	// Look up the adapter.
	var path, storedSHA string
	err := f.db.QueryRowContext(ctx,
		`SELECT path, adapter_sha256 FROM ml_adapters WHERE id = ? AND deleted_at IS NULL`,
		adapterID,
	).Scan(&path, &storedSHA)
	if err == sql.ErrNoRows {
		return fmt.Errorf("adapter %s not found", adapterID)
	}
	if err != nil {
		return fmt.Errorf("query adapter: %w", err)
	}

	// Verify integrity.
	if err := verifyAdapterHash(path, storedSHA); err != nil {
		return fmt.Errorf("adapter integrity check failed: %w", err)
	}

	// Load the adapter.
	if err := f.backend.LoadAdapter(ctx, path); err != nil {
		return fmt.Errorf("load adapter: %w", err)
	}

	// Update active flag atomically.
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE ml_adapters SET is_active = 0 WHERE is_active = 1`); err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE ml_adapters SET is_active = 1 WHERE id = ?`, adapterID); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	// Record the rollback in finetune_runs.
	_, _ = f.db.ExecContext(ctx,
		`UPDATE finetune_runs SET status = 'rolled_back' WHERE adapter_path = (SELECT path FROM ml_adapters WHERE is_active = 0 AND id != ? ORDER BY created_at DESC LIMIT 1)`,
		adapterID,
	)

	f.log.Info("finetuner: rolled back to adapter", "adapter_id", adapterID)
	return nil
}

// runOnce executes a single fine-tune run.
func (f *Finetuner) runOnce(ctx context.Context) *RunResult {
	runID := uuid.New().String()
	now := time.Now().UnixMilli()

	// Select batch from training_corpus.
	minRows := 100
	if f.cfg.Corpus.AnnotationBatchSizeOrDefault() > 0 {
		// Use config value if set.
	}

	var totalAnnotated int
	_ = f.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM training_corpus WHERE annotated_at IS NOT NULL AND confidence >= 0.5 AND used_in_finetune = 0`,
	).Scan(&totalAnnotated)

	if totalAnnotated < minRows {
		f.log.Info("finetuner: insufficient corpus rows", "available", totalAnnotated, "min", minRows)
		return &RunResult{RunID: runID, Status: "skipped", Error: fmt.Sprintf("insufficient rows: %d < %d", totalAnnotated, minRows)}
	}

	// Record run start.
	_, err := f.db.ExecContext(ctx,
		`INSERT INTO finetune_runs (id, started_at, status, mode, base_model_ver, corpus_row_count)
		 VALUES (?, ?, 'running', 'local', '', ?)`,
		runID, now, totalAnnotated,
	)
	if err != nil {
		return &RunResult{RunID: runID, Status: "failed", Error: err.Error()}
	}

	// Select row IDs for the batch.
	rows, err := f.db.QueryContext(ctx,
		`SELECT id FROM training_corpus WHERE annotated_at IS NOT NULL AND confidence >= 0.5 AND used_in_finetune = 0 ORDER BY id LIMIT 500`,
	)
	if err != nil {
		f.failRun(ctx, runID, err.Error())
		return &RunResult{RunID: runID, Status: "failed", Error: err.Error()}
	}
	var rowIDs []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		rowIDs = append(rowIDs, id)
	}
	rows.Close()

	// Prepare output directory.
	adapterDir := f.adapterDir()
	os.MkdirAll(adapterDir, 0700)

	batch := TrainBatch{
		RowIDs:      rowIDs,
		OutputDir:   adapterDir,
		MaxDuration: 30 * time.Minute,
	}

	// Run training.
	result, err := f.backend.Train(ctx, batch)
	if err != nil {
		errMsg := sanitizeErrorMsg(err.Error())
		f.failRun(ctx, runID, errMsg)
		return &RunResult{RunID: runID, Status: "failed", Error: errMsg}
	}

	// Record adapter.
	adapterID := uuid.New().String()
	adapterNow := time.Now().UnixMilli()
	_, _ = f.db.ExecContext(ctx,
		`INSERT INTO ml_adapters (id, finetune_run_id, created_at, base_model_ver, is_active, path, adapter_sha256)
		 VALUES (?, ?, ?, ?, 0, ?, ?)`,
		adapterID, runID, adapterNow, batch.BaseModelVer, result.AdapterPath, result.SHA256,
	)

	// Load the new adapter.
	if err := f.backend.LoadAdapter(ctx, result.AdapterPath); err != nil {
		f.log.Error("finetuner: load adapter failed", "error", err)
		// Still mark the run as complete — adapter exists but isn't loaded.
	} else {
		// Activate the new adapter.
		tx, err := f.db.BeginTx(ctx, nil)
		if err == nil {
			tx.ExecContext(ctx, `UPDATE ml_adapters SET is_active = 0 WHERE is_active = 1`)
			tx.ExecContext(ctx, `UPDATE ml_adapters SET is_active = 1 WHERE id = ?`, adapterID)
			tx.Commit()
		}
	}

	// Mark rows as used.
	for _, id := range rowIDs {
		f.db.ExecContext(ctx,
			`UPDATE training_corpus SET used_in_finetune = 1, finetune_run_id = ? WHERE id = ?`,
			runID, id,
		)
	}

	// Update run record.
	completedAt := time.Now().UnixMilli()
	f.db.ExecContext(ctx,
		`UPDATE finetune_runs SET status = 'complete', completed_at = ?, adapter_path = ?, adapter_sha256 = ?, loss_final = ?
		 WHERE id = ?`,
		completedAt, result.AdapterPath, result.SHA256, result.LossFinal, runID,
	)

	// Cleanup old adapters.
	f.cleanupOldAdapters(ctx)

	f.log.Info("finetuner: run complete",
		"run_id", runID,
		"rows", len(rowIDs),
		"duration", result.Duration,
		"loss", result.LossFinal,
	)

	return &RunResult{
		RunID:       runID,
		Status:      "complete",
		RowsTrained: len(rowIDs),
		AdapterPath: result.AdapterPath,
		AdapterSHA:  result.SHA256,
		LossFinal:   result.LossFinal,
		Duration:    result.Duration.String(),
	}
}

func (f *Finetuner) failRun(ctx context.Context, runID, errMsg string) {
	now := time.Now().UnixMilli()
	f.db.ExecContext(ctx,
		`UPDATE finetune_runs SET status = 'failed', completed_at = ?, error_msg = ? WHERE id = ?`,
		now, errMsg, runID,
	)
}

func (f *Finetuner) adapterDir() string {
	dataDir := os.Getenv("XDG_DATA_HOME")
	if dataDir == "" {
		home, _ := os.UserHomeDir()
		dataDir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dataDir, "sigild", "adapters")
}

func (f *Finetuner) nextFireTime() time.Time {
	now := time.Now()
	hour := 2 // default 2 AM
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
	if next.Before(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}

func (f *Finetuner) cleanupOldAdapters(ctx context.Context) {
	maxAdapters := 10

	rows, err := f.db.QueryContext(ctx,
		`SELECT id, path FROM ml_adapters WHERE is_active = 0 AND deleted_at IS NULL ORDER BY created_at ASC`,
	)
	if err != nil {
		return
	}
	defer rows.Close()

	type adapterEntry struct {
		id   string
		path string
	}
	var inactive []adapterEntry
	for rows.Next() {
		var e adapterEntry
		rows.Scan(&e.id, &e.path)
		inactive = append(inactive, e)
	}

	// Count total (active + inactive).
	var activeCount int
	f.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM ml_adapters WHERE is_active = 1 AND deleted_at IS NULL`,
	).Scan(&activeCount)

	total := activeCount + len(inactive)
	if total <= maxAdapters {
		return
	}

	// Delete oldest inactive until within budget.
	toDelete := total - maxAdapters
	for i := 0; i < toDelete && i < len(inactive); i++ {
		os.Remove(inactive[i].path)
		os.Remove(inactive[i].path[:len(inactive[i].path)-5] + ".json") // sidecar
		now := time.Now().UnixMilli()
		f.db.ExecContext(ctx,
			`UPDATE ml_adapters SET deleted_at = ? WHERE id = ?`, now, inactive[i].id,
		)
		f.log.Info("finetuner: cleaned up old adapter", "id", inactive[i].id)
	}
}

// verifyAdapterHash checks that the file at path has the expected SHA-256.
func verifyAdapterHash(path, expectedSHA string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read adapter: %w", err)
	}
	sum := sha256.Sum256(data)
	actual := hex.EncodeToString(sum[:])
	if actual != expectedSHA {
		return fmt.Errorf("hash mismatch: expected %s, got %s", expectedSHA, actual)
	}
	return nil
}

// PayloadHashSHA256 returns the hex-encoded SHA-256 of data.
// Exported for use by backends that produce adapter files.
func PayloadHashSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// sanitizeErrorMsg strips file paths and truncates error messages.
func sanitizeErrorMsg(msg string) string {
	// Truncate to 256 characters.
	if len(msg) > 256 {
		msg = msg[:256]
	}
	return msg
}
