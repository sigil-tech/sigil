package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// schemaVersion reads the current schema_version from db, returning 0 if the
// table exists but is empty, or an error if the table is absent or the query
// fails.
func schemaVersion(t *testing.T, db *sql.DB) int {
	t.Helper()
	var v int
	err := db.QueryRow(`SELECT version FROM schema_version LIMIT 1`).Scan(&v)
	if err == sql.ErrNoRows {
		return 0
	}
	require.NoError(t, err)
	return v
}

// tableExists reports whether a table with the given name exists in db.
func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name,
	).Scan(&n)
	require.NoError(t, err)
	return n > 0
}

// TestMigrateFromEmpty verifies that opening a brand-new database creates all
// expected tables and records schema_version = 3.
func TestMigrateFromEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.db")

	s, err := Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	// Version must be at the latest migration.
	assert.Equal(t, 4, schemaVersion(t, s.db))

	// V1 tables.
	for _, tbl := range []string{
		"events", "ai_interactions", "patterns", "suggestions",
		"feedback", "action_log", "tasks", "ml_events", "ml_predictions",
		"plugin_events", "sync_cursors",
	} {
		assert.True(t, tableExists(t, s.db, tbl), "expected table %s", tbl)
	}

	// V2 tables.
	for _, tbl := range []string{
		"sessions", "training_corpus", "merge_log", "filtered_log",
	} {
		assert.True(t, tableExists(t, s.db, tbl), "expected table %s", tbl)
	}
}

// TestMigrateFromV0 verifies that an existing database whose tables were
// created without a schema_version row is correctly upgraded to version 2
// and gains the new VM lifecycle tables.
func TestMigrateFromV0(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v0.db")

	// Bootstrap a "pre-versioning" database: create the original tables by
	// hand without inserting a schema_version row.
	raw, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = raw.Exec(`PRAGMA journal_mode = WAL`)
	require.NoError(t, err)
	_, err = raw.Exec(`CREATE TABLE IF NOT EXISTS events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		kind TEXT NOT NULL, source TEXT NOT NULL,
		payload TEXT NOT NULL, ts INTEGER NOT NULL)`)
	require.NoError(t, err)
	_, err = raw.Exec(`CREATE TABLE IF NOT EXISTS patterns (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		kind TEXT NOT NULL UNIQUE, summary TEXT NOT NULL,
		created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`)
	require.NoError(t, err)
	require.NoError(t, raw.Close())

	// Now open via the store package — this should detect version 0 and
	// apply all migrations.
	s, err := Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	assert.Equal(t, 4, schemaVersion(t, s.db))

	// V2 tables must now exist.
	for _, tbl := range []string{
		"sessions", "training_corpus", "merge_log", "filtered_log",
	} {
		assert.True(t, tableExists(t, s.db, tbl), "expected V2 table %s after upgrade", tbl)
	}

	// Original tables must still exist and be intact.
	assert.True(t, tableExists(t, s.db, "events"), "events table must survive migration")
	assert.True(t, tableExists(t, s.db, "patterns"), "patterns table must survive migration")
}

// TestMigrateIdempotent verifies that calling Open (and therefore migrate) a
// second time on a fully-migrated database is a no-op and produces no error.
func TestMigrateIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "idempotent.db")

	s1, err := Open(path)
	require.NoError(t, err)
	require.NoError(t, s1.Close())

	// Second open must succeed.
	s2, err := Open(path)
	require.NoError(t, err)
	defer s2.Close()

	assert.Equal(t, 4, schemaVersion(t, s2.db))
}

// TestOpenReadOnly verifies that OpenReadOnly returns a connection on which
// write operations fail.
func TestOpenReadOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "readonly.db")

	// Seed the database via the normal write path.
	s, err := Open(path)
	require.NoError(t, err)
	require.NoError(t, s.Close())

	db, err := OpenReadOnly(path)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	// Reads must work.
	var n int
	err = db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&n)
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	// Writes must be rejected.
	_, err = db.Exec(`INSERT INTO events (kind, source, payload, ts) VALUES ('file','test','{}',1)`)
	assert.Error(t, err, "write on a read-only connection must fail")
}

// TestQueryPatternSummaries verifies ordering, clamping, and correct field
// mapping for QueryPatternSummaries.
func TestQueryPatternSummaries(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	now := time.Now().Truncate(time.Millisecond)

	// Insert three patterns with distinct updated_at values.
	type prow struct {
		kind      string
		summary   string
		updatedAt time.Time
	}
	rows := []prow{
		{"build_failures", `{"count":3}`, now.Add(-2 * time.Minute)},
		{"test_slowness", `{"p99_ms":4200}`, now.Add(-1 * time.Minute)},
		{"context_switch", `{"rate":0.8}`, now},
	}
	for _, r := range rows {
		tsMS := r.updatedAt.UnixMilli()
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO patterns (kind, summary, created_at, updated_at) VALUES (?, ?, ?, ?)`,
			r.kind, r.summary, tsMS, tsMS,
		)
		require.NoError(t, err)
	}

	// Limit=0 should be clamped to 50 (returns all 3).
	summaries, err := s.QueryPatternSummaries(ctx, 0)
	require.NoError(t, err)
	require.Len(t, summaries, 3)

	// Most-recently-updated comes first.
	assert.Equal(t, "context_switch", summaries[0].Kind)
	assert.Equal(t, "test_slowness", summaries[1].Kind)
	assert.Equal(t, "build_failures", summaries[2].Kind)

	// Timestamps round-trip correctly.
	assert.Equal(t, now.UnixMilli(), summaries[0].UpdatedAt.UnixMilli())

	// Limit=1 returns exactly one.
	top1, err := s.QueryPatternSummaries(ctx, 1)
	require.NoError(t, err)
	require.Len(t, top1, 1)
	assert.Equal(t, "context_switch", top1[0].Kind)

	// Limit > 50 is clamped to 50.
	all, err := s.QueryPatternSummaries(ctx, 999)
	require.NoError(t, err)
	assert.Len(t, all, 3) // only 3 rows exist, so result is still 3
}
