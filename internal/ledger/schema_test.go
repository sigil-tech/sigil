package ledger

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// openMemoryDB returns a fresh in-memory SQLite database with WAL-ish
// pragmas appropriate for the ledger tests. `:memory:` is per-connection
// so we set MaxOpenConns=1 to keep every query on the same schema.
func openMemoryDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestMigrationCreateLedger covers Task 3.1: running Migrate against a
// fresh database creates the `ledger` table in STRICT mode with the
// expected columns, indexes, and append-only triggers.
func TestMigrationCreateLedger(t *testing.T) {
	ctx := context.Background()
	db := openMemoryDB(t)

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	t.Run("ledger table exists and is STRICT", func(t *testing.T) {
		var name, ddl string
		err := db.QueryRow(
			`SELECT name, sql FROM sqlite_schema WHERE type='table' AND name='ledger'`,
		).Scan(&name, &ddl)
		if err != nil {
			t.Fatalf("lookup ledger table: %v", err)
		}
		if !strings.Contains(ddl, "STRICT") {
			t.Fatalf("ledger table not STRICT:\n%s", ddl)
		}
	})

	t.Run("ledger table has all expected columns", func(t *testing.T) {
		want := map[string]bool{
			"id": false, "ts": false, "type": false, "subject": false,
			"payload_json": false, "prev_hash": false, "hash": false,
			"signature": false, "signing_key_fp": false,
		}
		rows, err := db.Query(`PRAGMA table_info(ledger)`)
		if err != nil {
			t.Fatalf("table_info: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var (
				cid     int
				colName string
				colType string
				notNull int
				dfltVal sql.NullString
				pk      int
			)
			if err := rows.Scan(&cid, &colName, &colType, &notNull, &dfltVal, &pk); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if _, ok := want[colName]; ok {
				want[colName] = true
			}
		}
		for col, seen := range want {
			if !seen {
				t.Errorf("ledger column %q missing", col)
			}
		}
	})

	t.Run("ts and type indexes exist", func(t *testing.T) {
		for _, idx := range []string{"idx_ledger_ts", "idx_ledger_type"} {
			var name string
			err := db.QueryRow(
				`SELECT name FROM sqlite_schema WHERE type='index' AND name=?`,
				idx,
			).Scan(&name)
			if err != nil {
				t.Errorf("index %q missing: %v", idx, err)
			}
		}
	})

	t.Run("unique constraint on hash", func(t *testing.T) {
		if err := insertRaw(ctx, db, 1, "a", "aa"); err != nil {
			t.Fatalf("first insert: %v", err)
		}
		// Re-inserting the same hash must fail (UNIQUE on hash).
		if err := insertRaw(ctx, db, 2, "b", "aa"); err == nil {
			t.Fatalf("expected UNIQUE violation on duplicate hash")
		}
	})

	t.Run("append-only triggers reject UPDATE and DELETE", func(t *testing.T) {
		// Row 1 was inserted above. UPDATE must raise.
		if _, err := db.ExecContext(ctx, `UPDATE ledger SET subject='tamper' WHERE id=1`); err == nil {
			t.Fatalf("expected UPDATE to be rejected by trigger")
		}
		if _, err := db.ExecContext(ctx, `DELETE FROM ledger WHERE id=1`); err == nil {
			t.Fatalf("expected DELETE to be rejected by trigger")
		}
	})

	t.Run("Migrate is idempotent", func(t *testing.T) {
		if err := Migrate(ctx, db); err != nil {
			t.Fatalf("second Migrate: %v", err)
		}
	})
}

// TestMigrationCreateLedgerKeys covers Task 3.2: the ledger_keys table
// exists with a PRIMARY KEY on fingerprint, a UNIQUE constraint on
// public_key, a no-delete trigger, and a single-update-path trigger
// that only permits retired_at to transition NULL → non-NULL.
func TestMigrationCreateLedgerKeys(t *testing.T) {
	ctx := context.Background()
	db := openMemoryDB(t)

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	t.Run("ledger_keys table exists and is STRICT", func(t *testing.T) {
		var ddl string
		err := db.QueryRow(
			`SELECT sql FROM sqlite_schema WHERE type='table' AND name='ledger_keys'`,
		).Scan(&ddl)
		if err != nil {
			t.Fatalf("lookup ledger_keys: %v", err)
		}
		if !strings.Contains(ddl, "STRICT") {
			t.Fatalf("ledger_keys table not STRICT:\n%s", ddl)
		}
	})

	t.Run("insert active key then retire via UPDATE retired_at", func(t *testing.T) {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO ledger_keys (fingerprint, public_key, generated_at, retired_at) VALUES (?,?,?,NULL)`,
			"fp1", "pk1", "2026-04-19T00:00:00Z",
		); err != nil {
			t.Fatalf("insert active key: %v", err)
		}
		if _, err := db.ExecContext(ctx,
			`UPDATE ledger_keys SET retired_at=? WHERE fingerprint=?`,
			"2026-04-19T01:00:00Z", "fp1",
		); err != nil {
			t.Fatalf("retire key: %v", err)
		}
	})

	t.Run("UPDATE of any other column is rejected", func(t *testing.T) {
		// Insert a second active key to exercise the "other column" path
		// without reusing fp1 which is already retired (and would hit the
		// "already retired" abort first).
		if _, err := db.ExecContext(ctx,
			`INSERT INTO ledger_keys (fingerprint, public_key, generated_at, retired_at) VALUES (?,?,?,NULL)`,
			"fp2", "pk2", "2026-04-19T02:00:00Z",
		); err != nil {
			t.Fatalf("insert fp2: %v", err)
		}
		if _, err := db.ExecContext(ctx,
			`UPDATE ledger_keys SET public_key='different' WHERE fingerprint=?`, "fp2",
		); err == nil {
			t.Fatalf("expected UPDATE of public_key to be rejected")
		}
		if _, err := db.ExecContext(ctx,
			`UPDATE ledger_keys SET generated_at='rewritten' WHERE fingerprint=?`, "fp2",
		); err == nil {
			t.Fatalf("expected UPDATE of generated_at to be rejected")
		}
	})

	t.Run("re-retiring an already-retired key is rejected", func(t *testing.T) {
		// fp1 has retired_at set above. Any UPDATE on it must fail.
		if _, err := db.ExecContext(ctx,
			`UPDATE ledger_keys SET retired_at='2026-04-19T05:00:00Z' WHERE fingerprint=?`, "fp1",
		); err == nil {
			t.Fatalf("expected re-retire of fp1 to be rejected")
		}
	})

	t.Run("UPDATE that clears retired_at (NULL) is rejected", func(t *testing.T) {
		// fp2 is still active. Setting retired_at to NULL is the only
		// "update" that matches all column-equality checks but must still
		// be rejected — otherwise a no-op UPDATE would bypass the gate.
		if _, err := db.ExecContext(ctx,
			`UPDATE ledger_keys SET retired_at=NULL WHERE fingerprint=?`, "fp2",
		); err == nil {
			t.Fatalf("expected UPDATE that keeps retired_at NULL to be rejected")
		}
	})

	t.Run("DELETE is rejected", func(t *testing.T) {
		if _, err := db.ExecContext(ctx,
			`DELETE FROM ledger_keys WHERE fingerprint=?`, "fp2",
		); err == nil {
			t.Fatalf("expected DELETE to be rejected by trigger")
		}
	})
}

// insertRaw is a test-only helper that bypasses the Emitter to write a
// hand-authored row. Used by the schema tests to exercise triggers and
// by the Reader round-trip test to populate fixtures without having the
// full sign/verify stack wired in.
func insertRaw(ctx context.Context, db *sql.DB, id int64, subject, hash string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO ledger
		   (id, ts, type, subject, payload_json, prev_hash, hash, signature, signing_key_fp)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id,
		"2026-04-19T00:00:00Z",
		"vm.spawn",
		subject,
		"{}",
		strings.Repeat("0", 64),
		hash,
		strings.Repeat("0", 128),
		strings.Repeat("0", 32),
	)
	return err
}
