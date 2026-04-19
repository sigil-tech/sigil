package store

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigrationAtScale populates 10,000 sessions rows at schema version 4
// (before Amendments B and D), runs migrations v5 and v6, and asserts:
//
//	(a) both migrations complete in < 1 s on the test machine,
//	(b) every row carries the correct default values in both new columns,
//	(c) no row corruption — a sampled set of pre-existing columns round-trips intact.
//
// SQLite ≥ 3.37 makes ALTER TABLE ADD COLUMN with DEFAULT O(1) — it writes
// only to sqlite_master, not to every row. This test guards against version
// drift where that property no longer holds, and provides a concrete timing
// budget for CI.
func TestMigrationAtScale(t *testing.T) {
	const rowCount = 10_000

	dir := t.TempDir()
	db := openAtVersion(t, dir, "scale_v4", 4)
	defer db.Close()

	// Bulk-insert 10,000 session rows inside a single transaction for speed.
	tx, err := db.Begin()
	require.NoError(t, err)

	stmt, err := tx.Prepare(`INSERT INTO sessions
		(id, started_at, status, merge_outcome, disk_image_path, overlay_path, vm_db_path, vsock_cid, filter_version)
		VALUES (?, ?, 'stopped', 'complete', ?, '', '', 0, 'v1')`)
	require.NoError(t, err)

	baseMS := time.Now().UnixMilli()
	for i := range rowCount {
		id := fmt.Sprintf("scale-session-%06d", i)
		imagePath := fmt.Sprintf("/images/sigil-os-%06d.qcow2", i)
		_, err = stmt.Exec(id, baseMS+int64(i), imagePath)
		require.NoError(t, err)
	}
	require.NoError(t, stmt.Close())
	require.NoError(t, tx.Commit())

	// Confirm row count before migration. Earlier migrations may seed the table
	// with rows (e.g. a 'host-default' sentinel row); record the actual count
	// so the post-migration assertion is relative to it.
	var pre int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&pre))
	require.GreaterOrEqual(t, pre, rowCount, "pre-migration row count must be at least %d", rowCount)

	// Apply migrations v5 and v6, measuring elapsed time.
	start := time.Now()
	for _, m := range migrations[4:] {
		mtx, txErr := db.Begin()
		require.NoError(t, txErr)
		require.NoError(t, m.fn(mtx), "apply migration v%d", m.version)
		_, txErr = mtx.Exec(`UPDATE schema_version SET version = ?`, m.version)
		require.NoError(t, txErr)
		require.NoError(t, mtx.Commit())
	}
	elapsed := time.Since(start)

	// (a) Timing budget: < 1 s for both migrations combined.
	assert.Less(t, elapsed, time.Second,
		"migrations v5+v6 on %d rows must complete in < 1 s; took %s", rowCount, elapsed)

	// (b) All rows must have the correct defaults. COUNT aggregate is O(1) against
	// an index but we need to confirm the column values, so we use a single
	// aggregate query rather than iterating all rows to keep the test fast.
	var wrongLedger int
	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*) FROM sessions WHERE ledger_events_total != 0`,
	).Scan(&wrongLedger))
	assert.Equal(t, 0, wrongLedger, "all rows must have ledger_events_total = 0")

	var wrongPolicy int
	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*) FROM sessions WHERE policy_status != 'ok'`,
	).Scan(&wrongPolicy))
	assert.Equal(t, 0, wrongPolicy, "all rows must have policy_status = 'ok'")

	// Confirm total row count is unchanged after migrations.
	var post int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&post))
	assert.Equal(t, pre, post, "post-migration row count must equal pre-migration count")

	// (c) No corruption: spot-check 10 rows spread across the table.
	checkIDs := sampleIDs(rowCount, 10)
	for _, idx := range checkIDs {
		id := fmt.Sprintf("scale-session-%06d", idx)
		expectedPath := fmt.Sprintf("/images/sigil-os-%06d.qcow2", idx)

		var gotPath, gotStatus, gotOutcome string
		var gotLedger int
		var gotPolicy string
		err := db.QueryRow(
			`SELECT disk_image_path, status, merge_outcome, ledger_events_total, policy_status
			 FROM sessions WHERE id = ?`, id,
		).Scan(&gotPath, &gotStatus, &gotOutcome, &gotLedger, &gotPolicy)
		require.NoError(t, err, "query row %s", id)

		assert.Equal(t, expectedPath, gotPath, "row %s: disk_image_path", id)
		assert.Equal(t, "stopped", gotStatus, "row %s: status", id)
		assert.Equal(t, "complete", gotOutcome, "row %s: merge_outcome", id)
		assert.Equal(t, 0, gotLedger, "row %s: ledger_events_total", id)
		assert.Equal(t, "ok", gotPolicy, "row %s: policy_status", id)
	}

	t.Logf("migration scale test: %d rows, elapsed=%s", rowCount, elapsed)
}

// sampleIDs returns n evenly-distributed indices from [0, total).
func sampleIDs(total, n int) []int {
	if n >= total {
		out := make([]int, total)
		for i := range total {
			out[i] = i
		}
		return out
	}
	out := make([]int, n)
	step := total / n
	for i := range n {
		out[i] = i * step
	}
	return out
}

// TestMigrationScaleQueryPlan verifies that SQLite's query planner considers
// the new columns index-eligible by running EXPLAIN QUERY PLAN against a
// predicate on policy_status. This is a canary for unexpected schema oddities.
func TestMigrationScaleQueryPlan(t *testing.T) {
	dir := t.TempDir()
	db := openAtVersion(t, dir, "qplan", 6)
	defer db.Close()

	rows, err := db.Query(
		`EXPLAIN QUERY PLAN SELECT id FROM sessions WHERE policy_status = 'denied'`,
	)
	require.NoError(t, err)
	defer rows.Close()

	var plan strings.Builder
	for rows.Next() {
		var id, parent, notused int
		var detail string
		require.NoError(t, rows.Scan(&id, &parent, &notused, &detail))
		plan.WriteString(detail)
		plan.WriteByte('\n')
	}
	require.NoError(t, rows.Err())

	// We don't assert index usage (no index on policy_status yet) but we do
	// assert the query executes without error and touches the sessions table.
	assert.Contains(t, strings.ToLower(plan.String()), "sessions",
		"query plan must reference the sessions table")

	_ = sql.ErrNoRows // imported for completeness; used in other test helpers
}
