package finetuner

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sigil-tech/sigil/internal/config"
	"github.com/sigil-tech/sigil/internal/store"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	s, err := store.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s.DB()
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestStatusEmpty(t *testing.T) {
	db := openTestDB(t)
	ft := New(db, nil, &config.Config{}, testLogger())

	ctx := context.Background()
	status, err := ft.Status(ctx)
	require.NoError(t, err)
	assert.Equal(t, "", status.ActiveAdapterID)
	assert.Equal(t, 0, status.CompletedRuns)
}

func TestHistoryEmpty(t *testing.T) {
	db := openTestDB(t)
	ft := New(db, nil, &config.Config{}, testLogger())

	ctx := context.Background()
	history, err := ft.History(ctx, 10)
	require.NoError(t, err)
	assert.Empty(t, history)
}

func TestRunNowDisabled(t *testing.T) {
	db := openTestDB(t)
	ft := New(db, nil, &config.Config{}, testLogger()) // backend nil = disabled

	ctx := context.Background()
	_, err := ft.RunNow(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "disabled")
}

func TestRollbackDisabled(t *testing.T) {
	db := openTestDB(t)
	ft := New(db, nil, &config.Config{}, testLogger())

	ctx := context.Background()
	err := ft.Rollback(ctx, "some-id")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "disabled")
}

func TestConcurrentFinetuneRejected(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	// Insert a "running" job.
	_, err := db.ExecContext(ctx,
		`INSERT INTO finetune_runs (id, started_at, status, mode, base_model_ver, corpus_row_count)
		 VALUES ('test-run', 1000, 'running', 'local', 'v1', 100)`,
	)
	require.NoError(t, err)

	// Create finetuner with a mock backend.
	ft := New(db, &mockBackend{}, &config.Config{}, testLogger())

	_, err = ft.RunNow(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ERR_FINETUNE_RUNNING")
}

func TestSanitizeErrorMsg(t *testing.T) {
	msg := string(make([]byte, 300))
	result := sanitizeErrorMsg(msg)
	assert.Len(t, result, 256)
}

func TestVerifyAdapterHash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.gguf")
	data := []byte("test adapter data")
	require.NoError(t, os.WriteFile(path, data, 0600))

	// Compute expected hash.
	expected := PayloadHashSHA256(data)

	err := verifyAdapterHash(path, expected)
	assert.NoError(t, err)

	err = verifyAdapterHash(path, "wrong-hash")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "hash mismatch")
}

// mockBackend implements FinetuneBackend for testing.
type mockBackend struct{}

func (m *mockBackend) Train(ctx context.Context, batch TrainBatch) (AdapterResult, error) {
	return AdapterResult{}, nil
}

func (m *mockBackend) LoadAdapter(ctx context.Context, adapterPath string) error {
	return nil
}
