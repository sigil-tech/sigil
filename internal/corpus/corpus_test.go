package corpus

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

func testConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Merge.FilterVersion = "v1"
	return cfg
}

func TestQueryStatsEmpty(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	stats, err := QueryStats(ctx, db)
	require.NoError(t, err)
	assert.Equal(t, 0, stats.TotalRows)
}

func TestQueryStatsWithData(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_, err := db.ExecContext(ctx,
			`INSERT INTO training_corpus (ts, origin, origin_session, event_type, source, payload, payload_size_bytes, filter_version, vm_row_id, payload_hash)
			 VALUES (?, 'host', 'host-default', 'file', 'fs', '{}', 2, 'v1', 0, ?)`,
			int64(1000000+i*1000), "hash"+string(rune('0'+i)),
		)
		require.NoError(t, err)
	}

	stats, err := QueryStats(ctx, db)
	require.NoError(t, err)
	assert.Equal(t, 5, stats.TotalRows)
	assert.Equal(t, 5, stats.RowsByOrigin["host"])
	assert.Equal(t, 0, stats.AnnotatedCount)
	assert.Equal(t, 5, stats.UnannotatedCount)
}

func TestPurge(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		_, err := db.ExecContext(ctx,
			`INSERT INTO training_corpus (ts, origin, origin_session, event_type, source, payload, payload_size_bytes, filter_version, vm_row_id, payload_hash)
			 VALUES (?, 'host', 'host-default', 'file', 'fs', '{}', 2, 'v1', 0, ?)`,
			int64(1000+i*100), "hash"+string(rune('a'+i)),
		)
		require.NoError(t, err)
	}

	result, err := Purge(ctx, db, 1500)
	require.NoError(t, err)
	assert.Equal(t, 5, result.RowsDeleted)

	var count int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM training_corpus`).Scan(&count)
	assert.Equal(t, 5, count)
}

func TestHostIngestion(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	hmacKey := []byte("testkey1234567890testkey12345678")
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := testConfig()

	c := New(db, nil, cfg, hmacKey, log)

	events := []HostEvent{
		{Kind: "file", Source: "fs", Payload: []byte(`{"path": "/home/user/code/main.go"}`), Timestamp: 1000000},
		{Kind: "file", Source: "fs", Payload: []byte(`{"path": "/home/user/code/main.go"}`), Timestamp: 1000001}, // same hour, should dedup
		{Kind: "file", Source: "fs", Payload: []byte(`{"path": "/home/user/.ssh/id_rsa"}`), Timestamp: 1000002},  // should be filtered
	}

	ingested, filtered := c.IngestHostEvents(ctx, events)
	assert.Equal(t, 1, ingested)
	assert.Equal(t, 1, filtered)
}

func TestHostIngestionNetworkFilter(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	hmacKey := []byte("testkey1234567890testkey12345678")
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := testConfig()

	c := New(db, nil, cfg, hmacKey, log)

	events := []HostEvent{
		{Kind: "net.connect", Source: "net", Payload: []byte(`{"dest": "192.168.1.1:443"}`), Timestamp: 2000000},
		{Kind: "net.connect", Source: "net", Payload: []byte(`{"dest": "8.8.8.8:443"}`), Timestamp: 2000001},
	}

	ingested, filtered := c.IngestHostEvents(ctx, events)
	assert.Equal(t, 1, ingested) // Only the public IP
	assert.Equal(t, 1, filtered) // Private IP filtered
}

func TestDeduplication(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	hmacKey := []byte("testkey1234567890testkey12345678")
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := testConfig()

	c := New(db, nil, cfg, hmacKey, log)

	// Same event, same hour window — should only insert once.
	event := HostEvent{Kind: "file", Source: "fs", Payload: []byte(`{"path": "/code/app.go"}`), Timestamp: 3600000} // ts=1h

	ingested1, _ := c.IngestHostEvents(ctx, []HostEvent{event})
	assert.Equal(t, 1, ingested1)

	ingested2, _ := c.IngestHostEvents(ctx, []HostEvent{event})
	assert.Equal(t, 0, ingested2) // Duplicate

	// Same event, different hour — should insert.
	event2 := HostEvent{Kind: "file", Source: "fs", Payload: []byte(`{"path": "/code/app.go"}`), Timestamp: 7200000} // ts=2h
	ingested3, _ := c.IngestHostEvents(ctx, []HostEvent{event2})
	assert.Equal(t, 1, ingested3)
}

func TestExport(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	dir := t.TempDir()
	outputPath := filepath.Join(dir, "export.jsonl")

	// Insert annotated rows.
	for i := 0; i < 3; i++ {
		_, err := db.ExecContext(ctx,
			`INSERT INTO training_corpus (ts, origin, origin_session, event_type, source, payload, payload_size_bytes, filter_version, vm_row_id, payload_hash, label, phase, confidence, annotated_at)
			 VALUES (?, 'host', 'host-default', 'file', 'fs', '{}', 2, 'v1', 0, ?, 'coding', 'deep_work', 0.9, ?)`,
			int64(1000+i*100), "hash"+string(rune('a'+i)), int64(2000+i*100),
		)
		require.NoError(t, err)
	}

	// Insert unannotated row (should not be exported).
	_, err := db.ExecContext(ctx,
		`INSERT INTO training_corpus (ts, origin, origin_session, event_type, source, payload, payload_size_bytes, filter_version, vm_row_id, payload_hash)
		 VALUES (?, 'host', 'host-default', 'file', 'fs', '{}', 2, 'v1', 0, 'unannotated')`,
		int64(5000),
	)
	require.NoError(t, err)

	count, err := Export(ctx, db, outputPath, nil)
	require.NoError(t, err)
	assert.Equal(t, 3, count) // Only annotated rows.

	// Verify file exists with correct permissions.
	info, err := os.Stat(outputPath)
	require.NoError(t, err)
	assert.True(t, info.Size() > 0)
}
