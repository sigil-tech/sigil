package merge

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sigil-tech/sigil/internal/config"

	_ "modernc.org/sqlite"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// testEvent is a convenience type for building VM rows.
type testEvent struct {
	kind    string
	source  string
	payload map[string]any
	ts      int64
	vmID    string
}

// createVMDB creates a temporary SQLite file populated with events and returns
// its absolute path.  The file lives in t.TempDir() and is removed automatically.
func createVMDB(t *testing.T, events []testEvent) string {
	t.Helper()
	path := t.TempDir() + "/vm.db"

	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE events (
		id      INTEGER PRIMARY KEY AUTOINCREMENT,
		kind    TEXT    NOT NULL,
		source  TEXT    NOT NULL,
		payload TEXT    NOT NULL,
		ts      INTEGER NOT NULL,
		vm_id   TEXT    NOT NULL DEFAULT ''
	)`)
	require.NoError(t, err)

	for _, ev := range events {
		raw, err := json.Marshal(ev.payload)
		require.NoError(t, err)
		ts := ev.ts
		if ts == 0 {
			ts = time.Now().UnixMilli()
		}
		_, err = db.Exec(`INSERT INTO events (kind, source, payload, ts, vm_id) VALUES (?,?,?,?,?)`,
			ev.kind, ev.source, string(raw), ts, ev.vmID)
		require.NoError(t, err)
	}
	return path
}

// createHostDB creates an in-memory SQLite database with the v2 merge schema.
func createHostDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	stmts := []string{
		`CREATE TABLE sessions (
			id               TEXT    PRIMARY KEY,
			started_at       INTEGER NOT NULL,
			ended_at         INTEGER,
			status           TEXT    NOT NULL,
			merge_outcome    TEXT    NOT NULL DEFAULT 'pending',
			disk_image_path  TEXT    NOT NULL,
			overlay_path     TEXT    NOT NULL DEFAULT '',
			vm_db_path       TEXT    NOT NULL DEFAULT '',
			vsock_cid        INTEGER NOT NULL DEFAULT 0,
			filter_version   TEXT    NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE training_corpus (
			id                 INTEGER PRIMARY KEY AUTOINCREMENT,
			ts                 INTEGER NOT NULL,
			origin             TEXT    NOT NULL,
			origin_session     TEXT    NOT NULL REFERENCES sessions(id),
			event_type         TEXT    NOT NULL,
			source             TEXT    NOT NULL,
			payload            BLOB,
			payload_size_bytes INTEGER NOT NULL DEFAULT 0,
			filter_version     TEXT    NOT NULL,
			vm_row_id          INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE UNIQUE INDEX ux_training_corpus_origin
			ON training_corpus(origin_session, vm_row_id)
			WHERE origin = 'vm_merge'`,
		`CREATE TABLE merge_log (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id     TEXT    NOT NULL UNIQUE REFERENCES sessions(id),
			vm_db_path     TEXT    NOT NULL,
			started_at     INTEGER NOT NULL,
			completed_at   INTEGER,
			status         TEXT    NOT NULL,
			rows_merged    INTEGER NOT NULL DEFAULT 0,
			rows_filtered  INTEGER NOT NULL DEFAULT 0,
			checkpoint     INTEGER NOT NULL DEFAULT 0,
			filter_version TEXT    NOT NULL,
			error_msg      TEXT
		)`,
		`CREATE TABLE filtered_log (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id      TEXT    NOT NULL REFERENCES sessions(id),
			ts              INTEGER NOT NULL,
			event_type      TEXT    NOT NULL,
			filter_rule     TEXT    NOT NULL,
			excluded_reason TEXT    NOT NULL,
			payload_hash    TEXT    NOT NULL
		)`,
	}
	for _, s := range stmts {
		snippet := s
		if len(snippet) > 60 {
			snippet = snippet[:60]
		}
		_, err := db.Exec(s)
		require.NoError(t, err, "setup: %s", snippet)
	}
	return db
}

// seedSession inserts a minimal sessions row so foreign-key constraints pass.
func seedSession(t *testing.T, db *sql.DB, sessionID string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO sessions (id, started_at, status, disk_image_path) VALUES (?,?,?,?)`,
		sessionID, time.Now().UnixMilli(), "ended", "/tmp/fake.qcow2",
	)
	require.NoError(t, err)
}

// defaultCfg returns a *config.Config with all-default merge settings.
func defaultCfg() *config.Config {
	return &config.Config{}
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestMergeCleanSession(t *testing.T) {
	hostDB := createHostDB(t)
	sessionID := "sess-clean-001"
	seedSession(t, hostDB, sessionID)

	events := make([]testEvent, 100)
	for i := range events {
		events[i] = testEvent{
			kind:    "file",
			source:  "watcher",
			payload: map[string]any{"path": fmt.Sprintf("/home/user/file%d.go", i), "op": "write"},
		}
	}
	vmPath := createVMDB(t, events)

	result, err := Merge(context.Background(), hostDB, vmPath, sessionID, defaultCfg())
	require.NoError(t, err)
	assert.Equal(t, MergeStatusComplete, result.Status)
	assert.Equal(t, 100, result.RowsMerged)
	assert.Equal(t, 0, result.RowsFiltered)

	var count int
	require.NoError(t, hostDB.QueryRow(
		`SELECT COUNT(*) FROM training_corpus WHERE origin_session = ?`, sessionID,
	).Scan(&count))
	assert.Equal(t, 100, count)

	var logStatus string
	require.NoError(t, hostDB.QueryRow(
		`SELECT status FROM merge_log WHERE session_id = ?`, sessionID,
	).Scan(&logStatus))
	assert.Equal(t, "complete", logStatus)
}

func TestMergeIdempotency(t *testing.T) {
	hostDB := createHostDB(t)
	sessionID := "sess-idem-001"
	seedSession(t, hostDB, sessionID)

	events := []testEvent{
		{kind: "file", source: "watcher", payload: map[string]any{"path": "/tmp/a.go"}},
		{kind: "file", source: "watcher", payload: map[string]any{"path": "/tmp/b.go"}},
	}
	vmPath := createVMDB(t, events)

	first, err := Merge(context.Background(), hostDB, vmPath, sessionID, defaultCfg())
	require.NoError(t, err)
	assert.Equal(t, MergeStatusComplete, first.Status)
	assert.Equal(t, 2, first.RowsMerged)

	second, err := Merge(context.Background(), hostDB, vmPath, sessionID, defaultCfg())
	require.NoError(t, err)
	assert.Equal(t, MergeStatusAlreadyComplete, second.Status)
	assert.Equal(t, 0, second.RowsMerged)

	var count int
	require.NoError(t, hostDB.QueryRow(
		`SELECT COUNT(*) FROM training_corpus WHERE origin_session = ?`, sessionID,
	).Scan(&count))
	assert.Equal(t, 2, count, "no duplicate rows after second merge")
}

func TestMergeResume(t *testing.T) {
	hostDB := createHostDB(t)
	sessionID := "sess-resume-001"
	seedSession(t, hostDB, sessionID)

	events := make([]testEvent, 10)
	for i := range events {
		events[i] = testEvent{
			kind:    "file",
			source:  "watcher",
			payload: map[string]any{"path": fmt.Sprintf("/tmp/f%d.go", i)},
		}
	}
	vmPath := createVMDB(t, events)

	// Simulate a crash after 5 rows: an in_progress row with checkpoint=5.
	now := time.Now().UnixMilli()
	_, err := hostDB.Exec(
		`INSERT INTO merge_log (session_id, vm_db_path, started_at, status, filter_version, checkpoint, rows_merged)
		 VALUES (?,?,?,'in_progress','v1',5,5)`,
		sessionID, vmPath, now,
	)
	require.NoError(t, err)
	// Seed the rows that were committed before the crash.
	for i := 1; i <= 5; i++ {
		_, err := hostDB.Exec(
			`INSERT INTO training_corpus
			 (ts, origin, origin_session, event_type, source, payload, payload_size_bytes, filter_version, vm_row_id)
			 VALUES (?,?,?,?,?,?,?,?,?)`,
			now, "vm_merge", sessionID, "file", "watcher", `{"path":"/tmp/pre.go"}`, 20, "v1", i,
		)
		require.NoError(t, err)
	}

	result, err := Merge(context.Background(), hostDB, vmPath, sessionID, defaultCfg())
	require.NoError(t, err)
	assert.Equal(t, MergeStatusComplete, result.Status)
	assert.Equal(t, 5, result.RowsMerged, "only the remaining 5 rows should be merged in this call")

	var total int
	require.NoError(t, hostDB.QueryRow(
		`SELECT COUNT(*) FROM training_corpus WHERE origin_session = ?`, sessionID,
	).Scan(&total))
	assert.Equal(t, 10, total)
}

func TestFilterDenylist(t *testing.T) {
	patterns := config.DefaultDenylist()
	cases := []struct {
		name    string
		payload map[string]any
	}{
		{"pem file", map[string]any{"path": "/etc/tls/server.pem"}},
		{"key file", map[string]any{"path": "/home/user/.ssh/id.key"}},
		{"env file", map[string]any{"path": "/app/.env"}},
		{"id_rsa", map[string]any{"path": "id_rsa"}},
		{"secret field", map[string]any{"my_secret": "abc123"}},
		{"password field", map[string]any{"password": "hunter2"}},
		{"token field", map[string]any{"auth_token": "tok_abc"}},
		{"p12 cert", map[string]any{"file": "client.p12"}},
		{"pfx cert", map[string]any{"file": "client.pfx"}},
		{"credential", map[string]any{"type": "credential_value"}},
		{"bearer", map[string]any{"header": "Authorization: Bearer xyz"}},
		{"gpg key", map[string]any{"export": "keyring.gpg"}},
		{"asc armored", map[string]any{"file": "key.asc"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, hit := walkPayloadStrings(tc.payload, patterns)
			assert.True(t, hit, "expected denylist hit for %v", tc.payload)
		})
	}
}

func TestFilterRecursivePayload(t *testing.T) {
	payload := map[string]any{
		"level1": map[string]any{
			"level2": map[string]any{
				"level3": map[string]any{
					"api_secret": "s3cr3t_value",
				},
			},
		},
	}
	_, hit := walkPayloadStrings(payload, config.DefaultDenylist())
	assert.True(t, hit, "expected denylist hit at depth 3")
}

func TestFilterPayloadTooLarge(t *testing.T) {
	hostDB := createHostDB(t)
	sessionID := "sess-large-001"
	seedSession(t, hostDB, sessionID)

	// Build a payload that exceeds the 64 KB default limit.
	large := strings.Repeat("x", 65537)
	events := []testEvent{
		{kind: "file", source: "watcher", payload: map[string]any{"data": large}},
		{kind: "file", source: "watcher", payload: map[string]any{"path": "/small.go"}},
	}
	vmPath := createVMDB(t, events)

	result, err := Merge(context.Background(), hostDB, vmPath, sessionID, defaultCfg())
	require.NoError(t, err)
	assert.Equal(t, MergeStatusComplete, result.Status)
	assert.Equal(t, 1, result.RowsMerged, "only the small row should be merged")
	assert.Equal(t, 1, result.RowsFiltered)

	var rule string
	require.NoError(t, hostDB.QueryRow(
		`SELECT filter_rule FROM filtered_log WHERE session_id = ?`, sessionID,
	).Scan(&rule))
	assert.Equal(t, "payload_too_large", rule)

	// The raw payload must not be stored — only a hash.
	var hash string
	require.NoError(t, hostDB.QueryRow(
		`SELECT payload_hash FROM filtered_log WHERE session_id = ?`, sessionID,
	).Scan(&hash))
	assert.Len(t, hash, 64, "SHA-256 hex digest must be 64 chars")
}

func TestFilterProcessArgStrip(t *testing.T) {
	hostDB := createHostDB(t)
	sessionID := "sess-args-001"
	seedSession(t, hostDB, sessionID)

	events := []testEvent{
		{
			kind:   "process",
			source: "proc",
			payload: map[string]any{
				"name":    "go",
				"args":    []any{"build", "-o", "/tmp/out", "./..."},
				"cmdline": "go build -o /tmp/out ./...",
				"pid":     12345,
			},
		},
	}
	vmPath := createVMDB(t, events)

	result, err := Merge(context.Background(), hostDB, vmPath, sessionID, defaultCfg())
	require.NoError(t, err)
	assert.Equal(t, MergeStatusComplete, result.Status)
	assert.Equal(t, 1, result.RowsMerged)

	var rawPayload []byte
	require.NoError(t, hostDB.QueryRow(
		`SELECT payload FROM training_corpus WHERE origin_session = ?`, sessionID,
	).Scan(&rawPayload))

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(rawPayload, &decoded))
	assert.NotContains(t, decoded, "args", "args must be stripped from process events")
	assert.NotContains(t, decoded, "cmdline", "cmdline must be stripped from process events")
	assert.Equal(t, float64(12345), decoded["pid"], "non-arg fields must be preserved")
}

func TestFilterRFC1918Net(t *testing.T) {
	hostDB := createHostDB(t)
	sessionID := "sess-rfc1918-001"
	seedSession(t, hostDB, sessionID)

	events := []testEvent{
		{kind: "net.connect", source: "net", payload: map[string]any{"dest": "192.168.1.1:443"}},
		{kind: "net.connect", source: "net", payload: map[string]any{"dest": "10.0.0.5:80"}},
		{kind: "net.connect", source: "net", payload: map[string]any{"dest": "172.16.4.10:8080"}},
		{kind: "net.connect", source: "net", payload: map[string]any{"dest": "8.8.8.8:443"}}, // public — allow
	}
	vmPath := createVMDB(t, events)

	result, err := Merge(context.Background(), hostDB, vmPath, sessionID, defaultCfg())
	require.NoError(t, err)
	assert.Equal(t, MergeStatusComplete, result.Status)
	assert.Equal(t, 1, result.RowsMerged, "only the public IP should be merged")
	assert.Equal(t, 3, result.RowsFiltered)

	var count int
	require.NoError(t, hostDB.QueryRow(
		`SELECT COUNT(*) FROM filtered_log WHERE session_id = ? AND filter_rule = 'private_destination'`, sessionID,
	).Scan(&count))
	assert.Equal(t, 3, count)
}

func TestFilterInternalHostname(t *testing.T) {
	hostDB := createHostDB(t)
	sessionID := "sess-internal-001"
	seedSession(t, hostDB, sessionID)

	events := []testEvent{
		{kind: "net.connect", source: "net", payload: map[string]any{"dest": "service.internal"}},
		{kind: "net.connect", source: "net", payload: map[string]any{"dest": "db.corp"}},
		{kind: "net.connect", source: "net", payload: map[string]any{"dest": "printer.local"}},
		{kind: "net.connect", source: "net", payload: map[string]any{"dest": "router"}},          // single-label
		{kind: "net.connect", source: "net", payload: map[string]any{"dest": "example.com:443"}}, // public — allow
	}
	vmPath := createVMDB(t, events)

	result, err := Merge(context.Background(), hostDB, vmPath, sessionID, defaultCfg())
	require.NoError(t, err)
	assert.Equal(t, MergeStatusComplete, result.Status)
	assert.Equal(t, 1, result.RowsMerged, "only example.com should be merged")
	assert.Equal(t, 4, result.RowsFiltered)
}

func TestSizeBudgetExceeded(t *testing.T) {
	hostDB := createHostDB(t)
	sessionID := "sess-budget-001"
	seedSession(t, hostDB, sessionID)

	// Budget: 1 MB.  Rows: 30 × ~40 KB ≈ 1.2 MB → partial after ~25 rows.
	// MaxRowPayloadBytes must be ≥ row size (40 KB < 64 KB default, so default is fine).
	cfg := &config.Config{
		Merge: config.MergeConfig{
			SessionBudgetMB:    1,
			MaxRowPayloadBytes: 65536,
		},
	}
	bigVal := strings.Repeat("a", 40_000)
	// 30 rows × ~40 KB ≈ 1.2 MB total, exceeding the 1 MB budget.
	events := make([]testEvent, 30)
	for i := range events {
		events[i] = testEvent{
			kind:    "file",
			source:  "watcher",
			payload: map[string]any{"data": bigVal, "path": fmt.Sprintf("/tmp/f%d.go", i)},
		}
	}
	vmPath := createVMDB(t, events)

	result, err := Merge(context.Background(), hostDB, vmPath, sessionID, cfg)
	require.NoError(t, err)
	assert.Equal(t, MergeStatusPartial, result.Status, "budget should be exhausted before all 30 rows")
	assert.Less(t, result.RowsMerged, 30)
	assert.Greater(t, result.RowsMerged, 0)

	var checkpoint int64
	require.NoError(t, hostDB.QueryRow(
		`SELECT checkpoint FROM merge_log WHERE session_id = ?`, sessionID,
	).Scan(&checkpoint))
	assert.Greater(t, checkpoint, int64(0), "checkpoint must be non-zero for resume")
}

func TestDBSizeValidation(t *testing.T) {
	hostDB := createHostDB(t)
	sessionID := "sess-empty-001"
	seedSession(t, hostDB, sessionID)

	// Create an empty (zero-byte) file.
	emptyPath := t.TempDir() + "/empty.db"
	f, err := os.Create(emptyPath)
	require.NoError(t, err)
	f.Close()

	result, mergeErr := Merge(context.Background(), hostDB, emptyPath, sessionID, defaultCfg())
	assert.Error(t, mergeErr)
	assert.Equal(t, MergeStatusFailed, result.Status)
	assert.NotEmpty(t, result.Error)

	var logStatus string
	require.NoError(t, hostDB.QueryRow(
		`SELECT status FROM merge_log WHERE session_id = ?`, sessionID,
	).Scan(&logStatus))
	assert.Equal(t, "failed", logStatus)
}
