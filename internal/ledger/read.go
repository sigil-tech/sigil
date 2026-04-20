package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ErrEntryNotFound is returned by Reader.Get when no row matches the
// supplied id. Callers distinguish via errors.Is.
var ErrEntryNotFound = errors.New("ledger: entry not found")

// Entry is the decoded form of one ledger row. All hex-encoded fields
// are kept as lowercase strings in Go to match the on-disk storage
// format and the socket wire contract. Callers that need raw bytes
// decode at the boundary.
type Entry struct {
	ID           int64
	Timestamp    string // RFC 3339 nanosecond UTC
	Type         string // EventType enum
	Subject      string
	PayloadJSON  string // JCS-canonical JSON
	PrevHash     string // 64 hex chars
	Hash         string // 64 hex chars
	Signature    string // 128 hex chars
	SigningKeyFP string // 32 hex chars
}

// ListFilter specifies the subset of the ledger to return from List.
// The zero value returns the most recent DefaultListLimit entries in
// descending id order.
//
// Pagination is expressed as `BeforeID`: rows with id strictly less
// than BeforeID are returned. An empty BeforeID means "start from the
// tip". This is the cursor shape FR-018 and spec 029 plan §6 specify;
// it is stable under append (a new entry at the tip does not shift
// existing cursors) and O(1) per page fetch against the ts index.
//
// TypeFilter, if non-empty, filters to rows where `type = TypeFilter`.
// Limit is clamped to [1, MaxListLimit]; a zero Limit uses
// DefaultListLimit.
type ListFilter struct {
	BeforeID   int64
	TypeFilter string
	Limit      int
}

const (
	// DefaultListLimit is the page size used when ListFilter.Limit is
	// the zero value. Matches the Audit Viewer's visible-row budget.
	DefaultListLimit = 100

	// MaxListLimit caps a single page's row count to protect the
	// socket + frontend path from pathologically large responses.
	MaxListLimit = 500
)

// Reader is the read-side interface the socket layer and sigilctl use
// to browse the ledger. Implementations are safe for concurrent use.
type Reader interface {
	// Get returns the single entry with the given id, or ErrEntryNotFound
	// if it does not exist.
	Get(ctx context.Context, id int64) (Entry, error)

	// List returns a page of entries in descending id order, newest
	// first. See ListFilter for cursor / filter semantics.
	List(ctx context.Context, filter ListFilter) ([]Entry, error)

	// Count returns the total number of rows currently in the ledger.
	// Useful for the Audit Viewer's "showing N of M" chrome and for
	// health checks during tests.
	Count(ctx context.Context) (int64, error)

	// IterateAll returns every entry in ascending id order via a
	// callback. The callback may return a non-nil error to abort
	// iteration early; the error propagates back unchanged. Unlike
	// List, IterateAll streams — it is what the Verifier uses for
	// full-chain walks (Phase 4). Rows are yielded under a forward
	// cursor so the caller sees a consistent snapshot even if new
	// rows are appended during iteration; new rows past the initial
	// tip are ignored.
	IterateAll(ctx context.Context, fn func(Entry) error) error
}

// readerImpl is the database-backed Reader. It holds a raw *sql.DB
// rather than a richer Store abstraction because the ledger package
// sits below the merge / corpus layers that build on top of Store —
// a thin handle here avoids a ripple of interface widening.
type readerImpl struct {
	db *sql.DB
}

// NewReader wires a Reader to an open *sql.DB. The database MUST
// already have been migrated via Migrate; NewReader does not run
// schema creation.
func NewReader(db *sql.DB) Reader {
	return &readerImpl{db: db}
}

// selectColumns is the SELECT clause shared by every read path. Kept
// as a constant so adding a column later (e.g., a payload-size hint)
// happens exactly once rather than three times.
const selectColumns = `id, ts, type, subject, payload_json, prev_hash, hash, signature, signing_key_fp`

func scanEntry(scanner interface {
	Scan(dest ...any) error
}) (Entry, error) {
	var e Entry
	err := scanner.Scan(
		&e.ID, &e.Timestamp, &e.Type, &e.Subject, &e.PayloadJSON,
		&e.PrevHash, &e.Hash, &e.Signature, &e.SigningKeyFP,
	)
	return e, err
}

func (r *readerImpl) Get(ctx context.Context, id int64) (Entry, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+selectColumns+` FROM ledger WHERE id = ?`, id,
	)
	e, err := scanEntry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, fmt.Errorf("ledger.Get id=%d: %w", id, ErrEntryNotFound)
	}
	if err != nil {
		return Entry{}, fmt.Errorf("ledger.Get id=%d: %w", id, err)
	}
	return e, nil
}

func (r *readerImpl) List(ctx context.Context, filter ListFilter) ([]Entry, error) {
	limit := filter.Limit
	switch {
	case limit <= 0:
		limit = DefaultListLimit
	case limit > MaxListLimit:
		limit = MaxListLimit
	}

	// Compose the WHERE clause incrementally. We avoid reflection and
	// fancy query builders — the shape is small enough that explicit
	// branching is clearer than a DSL.
	query := `SELECT ` + selectColumns + ` FROM ledger`
	var (
		clauses []string
		args    []any
	)
	if filter.BeforeID > 0 {
		clauses = append(clauses, `id < ?`)
		args = append(args, filter.BeforeID)
	}
	if filter.TypeFilter != "" {
		clauses = append(clauses, `type = ?`)
		args = append(args, filter.TypeFilter)
	}
	if len(clauses) > 0 {
		query += ` WHERE ` + joinAnd(clauses)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("ledger.List: %w", err)
	}
	defer rows.Close()

	out := make([]Entry, 0, limit)
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("ledger.List scan: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ledger.List rows: %w", err)
	}
	return out, nil
}

func (r *readerImpl) Count(ctx context.Context) (int64, error) {
	var n int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ledger`).Scan(&n); err != nil {
		return 0, fmt.Errorf("ledger.Count: %w", err)
	}
	return n, nil
}

func (r *readerImpl) IterateAll(ctx context.Context, fn func(Entry) error) error {
	// Snapshot the tip up-front so late appends do not extend the walk.
	// Verifier correctness depends on iterating a fixed set of rows —
	// without this pin, a concurrent emit could push an unverifiable
	// (because not-yet-signed at snapshot) row into the walk.
	var tip int64
	if err := r.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(id), 0) FROM ledger`,
	).Scan(&tip); err != nil {
		return fmt.Errorf("ledger.IterateAll tip: %w", err)
	}
	if tip == 0 {
		return nil
	}

	rows, err := r.db.QueryContext(ctx,
		`SELECT `+selectColumns+` FROM ledger WHERE id <= ? ORDER BY id ASC`, tip,
	)
	if err != nil {
		return fmt.Errorf("ledger.IterateAll: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		e, err := scanEntry(rows)
		if err != nil {
			return fmt.Errorf("ledger.IterateAll scan: %w", err)
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	return rows.Err()
}

// joinAnd concatenates clauses with " AND " without bringing in the
// strings package just for one use. Keeps the compiled binary leaner
// in a hot-path-adjacent file.
func joinAnd(clauses []string) string {
	return strings.Join(clauses, " AND ")
}
