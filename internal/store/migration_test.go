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

// columnExists reports whether a column with the given name exists on the given
// table in db.
func columnExists(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dfltValue sql.NullString
		var pk int
		require.NoError(t, rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk))
		if name == column {
			return true
		}
	}
	require.NoError(t, rows.Err())
	return false
}

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
	assert.Equal(t, 6, schemaVersion(t, s.db))

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

	assert.Equal(t, 6, schemaVersion(t, s.db))

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

	assert.Equal(t, 6, schemaVersion(t, s2.db))
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

// openAtVersion opens a fresh SQLite file at dir/name.db, runs only the
// migrations whose version is <= stopAfter, and returns the raw *sql.DB.
// The caller is responsible for closing it.
func openAtVersion(t *testing.T, dir, name string, stopAfter int) *sql.DB {
	t.Helper()
	path := filepath.Join(dir, name+".db")

	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)

	_, err = db.Exec("PRAGMA journal_mode = WAL")
	require.NoError(t, err)
	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`)
	require.NoError(t, err)

	var current int
	for _, m := range migrations {
		if m.version > stopAfter {
			break
		}
		tx, err := db.Begin()
		require.NoError(t, err, "begin migration v%d", m.version)
		require.NoError(t, m.fn(tx), "apply migration v%d", m.version)
		if current == 0 {
			_, err = tx.Exec(`INSERT INTO schema_version (version) VALUES (?)`, m.version)
		} else {
			_, err = tx.Exec(`UPDATE schema_version SET version = ?`, m.version)
		}
		require.NoError(t, err, "record migration v%d", m.version)
		require.NoError(t, tx.Commit(), "commit migration v%d", m.version)
		current = m.version
	}
	return db
}

// TestMigrateAddLedgerEventsTotal verifies that migration v5 adds the
// ledger_events_total column to sessions with DEFAULT 0 applied to
// pre-existing rows.
func TestMigrateAddLedgerEventsTotal(t *testing.T) {
	dir := t.TempDir()
	db := openAtVersion(t, dir, "v4", 4)
	defer db.Close()

	// Insert a session row before the migration.
	_, err := db.Exec(`INSERT INTO sessions (id, started_at, status, merge_outcome, disk_image_path, overlay_path, vm_db_path, vsock_cid, filter_version)
		VALUES ('pre-existing-1', 1000, 'stopped', 'complete', '/img', '', '', 0, '')`)
	require.NoError(t, err)

	// Apply migration v5 only.
	tx, err := db.Begin()
	require.NoError(t, err)
	require.NoError(t, migrations[4].fn(tx)) // index 4 = version 5
	_, err = tx.Exec(`UPDATE schema_version SET version = 5`)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	// Column must now exist.
	assert.True(t, columnExists(t, db, "sessions", "ledger_events_total"),
		"ledger_events_total column must exist after migration v5")

	// Pre-existing row must have DEFAULT 0.
	var val int
	require.NoError(t, db.QueryRow(
		`SELECT ledger_events_total FROM sessions WHERE id = 'pre-existing-1'`,
	).Scan(&val))
	assert.Equal(t, 0, val, "pre-existing row must have ledger_events_total = 0")
}

// TestMigrateAddPolicyStatus verifies that migration v6 adds the policy_status
// column to sessions with DEFAULT 'ok' applied to pre-existing rows.
func TestMigrateAddPolicyStatus(t *testing.T) {
	dir := t.TempDir()
	db := openAtVersion(t, dir, "v5", 5)
	defer db.Close()

	// Insert a session row before the migration.
	_, err := db.Exec(`INSERT INTO sessions (id, started_at, status, merge_outcome, disk_image_path, overlay_path, vm_db_path, vsock_cid, filter_version, ledger_events_total)
		VALUES ('pre-existing-2', 2000, 'stopped', 'complete', '/img', '', '', 0, '', 7)`)
	require.NoError(t, err)

	// Apply migration v6 only.
	tx, err := db.Begin()
	require.NoError(t, err)
	require.NoError(t, migrations[5].fn(tx)) // index 5 = version 6
	_, err = tx.Exec(`UPDATE schema_version SET version = 6`)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	// Column must now exist.
	assert.True(t, columnExists(t, db, "sessions", "policy_status"),
		"policy_status column must exist after migration v6")

	// Pre-existing row must have DEFAULT 'ok'.
	var val string
	require.NoError(t, db.QueryRow(
		`SELECT policy_status FROM sessions WHERE id = 'pre-existing-2'`,
	).Scan(&val))
	assert.Equal(t, "ok", val, "pre-existing row must have policy_status = 'ok'")
}

// TestMigrationsApplyToV2DB opens a database at v4 (before the new columns),
// inserts a session row, applies all remaining migrations, and asserts that the
// pre-existing row has the correct default values for both new columns.
func TestMigrationsApplyToV2DB(t *testing.T) {
	dir := t.TempDir()
	db := openAtVersion(t, dir, "v4fixture", 4)
	defer db.Close()

	// Insert a pre-existing session row.
	_, err := db.Exec(`INSERT INTO sessions (id, started_at, status, merge_outcome, disk_image_path, overlay_path, vm_db_path, vsock_cid, filter_version)
		VALUES ('fixture-session', 3000, 'stopped', 'complete', '/base.qcow2', '', '', 0, 'v1')`)
	require.NoError(t, err)

	// Apply migrations v5 and v6.
	for _, m := range migrations[4:] {
		tx, txErr := db.Begin()
		require.NoError(t, txErr)
		require.NoError(t, m.fn(tx), "apply migration v%d", m.version)
		_, txErr = tx.Exec(`UPDATE schema_version SET version = ?`, m.version)
		require.NoError(t, txErr)
		require.NoError(t, tx.Commit())
	}

	// Both new columns must exist.
	assert.True(t, columnExists(t, db, "sessions", "ledger_events_total"))
	assert.True(t, columnExists(t, db, "sessions", "policy_status"))

	// Pre-existing row must carry correct defaults.
	var ledger int
	var policyStatus string
	require.NoError(t, db.QueryRow(
		`SELECT ledger_events_total, policy_status FROM sessions WHERE id = 'fixture-session'`,
	).Scan(&ledger, &policyStatus))
	assert.Equal(t, 0, ledger, "pre-existing row: ledger_events_total must be 0")
	assert.Equal(t, "ok", policyStatus, "pre-existing row: policy_status must be 'ok'")

	// Existing data must be unmodified.
	var diskPath string
	require.NoError(t, db.QueryRow(
		`SELECT disk_image_path FROM sessions WHERE id = 'fixture-session'`,
	).Scan(&diskPath))
	assert.Equal(t, "/base.qcow2", diskPath, "existing column data must survive migration")
}
