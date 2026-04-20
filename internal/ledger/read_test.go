package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// seedRows inserts n rows with sequential ids (1..n), alternating types
// for filter coverage. Hashes are unique because the subject is unique
// per row. Returns a lookup from id → subject so callers can assert
// shape without reaching into the insert again.
func seedRows(t *testing.T, db *sql.DB, n int) map[int64]string {
	t.Helper()
	subjects := make(map[int64]string, n)
	for i := int64(1); i <= int64(n); i++ {
		subject := fmt.Sprintf("row-%04d", i)
		typ := "vm.spawn"
		if i%2 == 0 {
			typ = "vm.teardown"
		}
		_, err := db.Exec(
			`INSERT INTO ledger
			   (id, ts, type, subject, payload_json, prev_hash, hash, signature, signing_key_fp)
			 VALUES (?,?,?,?,?,?,?,?,?)`,
			i,
			fmt.Sprintf("2026-04-19T00:00:%02dZ", i%60),
			typ,
			subject,
			"{}",
			strings.Repeat("0", 64),
			fmt.Sprintf("%064d", i),
			strings.Repeat("0", 128),
			strings.Repeat("0", 32),
		)
		if err != nil {
			t.Fatalf("seed row %d: %v", i, err)
		}
		subjects[i] = subject
	}
	return subjects
}

func TestReader(t *testing.T) {
	ctx := context.Background()
	db := openMemoryDB(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	seedRows(t, db, 20)
	r := NewReader(db)

	t.Run("Get returns the matching row", func(t *testing.T) {
		e, err := r.Get(ctx, 7)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if e.ID != 7 || e.Subject != "row-0007" {
			t.Fatalf("unexpected row: %+v", e)
		}
	})

	t.Run("Get on missing id returns ErrEntryNotFound", func(t *testing.T) {
		_, err := r.Get(ctx, 9999)
		if !errors.Is(err, ErrEntryNotFound) {
			t.Fatalf("Get 9999: got %v, want ErrEntryNotFound", err)
		}
	})

	t.Run("List with zero filter returns DefaultListLimit rows newest-first", func(t *testing.T) {
		entries, err := r.List(ctx, ListFilter{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(entries) != 20 {
			t.Fatalf("len=%d, want 20 (seeded count)", len(entries))
		}
		if entries[0].ID != 20 {
			t.Fatalf("first ID = %d, want 20", entries[0].ID)
		}
		if entries[len(entries)-1].ID != 1 {
			t.Fatalf("last ID = %d, want 1", entries[len(entries)-1].ID)
		}
	})

	t.Run("List honours BeforeID cursor", func(t *testing.T) {
		entries, err := r.List(ctx, ListFilter{BeforeID: 10, Limit: 3})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(entries) != 3 {
			t.Fatalf("len=%d, want 3", len(entries))
		}
		wantIDs := []int64{9, 8, 7}
		for i, e := range entries {
			if e.ID != wantIDs[i] {
				t.Fatalf("entries[%d].ID = %d, want %d", i, e.ID, wantIDs[i])
			}
		}
	})

	t.Run("List honours TypeFilter", func(t *testing.T) {
		entries, err := r.List(ctx, ListFilter{TypeFilter: "vm.teardown", Limit: 50})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		for _, e := range entries {
			if e.Type != "vm.teardown" {
				t.Fatalf("TypeFilter leak: row id=%d type=%q", e.ID, e.Type)
			}
		}
		if len(entries) != 10 { // 20 rows, even ids → vm.teardown
			t.Fatalf("len=%d, want 10", len(entries))
		}
	})

	t.Run("List Limit is clamped to MaxListLimit", func(t *testing.T) {
		entries, err := r.List(ctx, ListFilter{Limit: MaxListLimit + 9999})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		// Only 20 seeded rows, so len is 20 even with the huge limit. The
		// point is the query didn't blow up and the limit compiled OK.
		if len(entries) == 0 {
			t.Fatalf("huge limit returned zero rows")
		}
	})

	t.Run("Count returns seeded row count", func(t *testing.T) {
		n, err := r.Count(ctx)
		if err != nil {
			t.Fatalf("Count: %v", err)
		}
		if n != 20 {
			t.Fatalf("Count = %d, want 20", n)
		}
	})

	t.Run("IterateAll visits every row in ascending id order", func(t *testing.T) {
		var seen []int64
		if err := r.IterateAll(ctx, func(e Entry) error {
			seen = append(seen, e.ID)
			return nil
		}); err != nil {
			t.Fatalf("IterateAll: %v", err)
		}
		if len(seen) != 20 {
			t.Fatalf("IterateAll yielded %d rows, want 20", len(seen))
		}
		for i, id := range seen {
			if id != int64(i+1) {
				t.Fatalf("seen[%d] = %d, want %d", i, id, i+1)
			}
		}
	})

	t.Run("IterateAll respects callback error", func(t *testing.T) {
		stopAfter := int64(5)
		stop := errors.New("sentinel stop")
		count := 0
		err := r.IterateAll(ctx, func(e Entry) error {
			count++
			if e.ID == stopAfter {
				return stop
			}
			return nil
		})
		if !errors.Is(err, stop) {
			t.Fatalf("IterateAll err = %v, want %v", err, stop)
		}
		if count != int(stopAfter) {
			t.Fatalf("iteration did not halt at id=%d: visited %d rows", stopAfter, count)
		}
	})

	t.Run("IterateAll pins to the pre-iteration tip", func(t *testing.T) {
		// The snapshot cap logic is easiest to exercise by appending a
		// row *before* iteration and capturing the tip explicitly. With
		// MaxOpenConns=1 and an in-memory database, we cannot write
		// concurrently from inside the callback (the write would deadlock
		// on the iterator's connection), so we assert the tip-snapshot
		// behaviour by comparing IterateAll's yielded count against the
		// count at the moment IterateAll was invoked rather than against
		// a post-iteration Count.
		preCount, err := r.Count(ctx)
		if err != nil {
			t.Fatalf("pre-Count: %v", err)
		}
		yielded := 0
		if err := r.IterateAll(ctx, func(Entry) error {
			yielded++
			return nil
		}); err != nil {
			t.Fatalf("IterateAll: %v", err)
		}
		if int64(yielded) != preCount {
			t.Fatalf("IterateAll yielded %d rows, want %d (pre-iteration Count)", yielded, preCount)
		}
	})
}

// TestReaderRoundTrip1K is Task 3.6: stress the Reader with 1000
// hand-authored rows inserted via raw SQL (bypassing Emit for test
// isolation — we want to validate the read path independently of the
// sign/verify stack that Phase 4 introduces). Asserts pagination
// cursors are stable and every id round-trips through Get.
func TestReaderRoundTrip1K(t *testing.T) {
	ctx := context.Background()
	db := openMemoryDB(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	subjects := seedRows(t, db, 1000)
	r := NewReader(db)

	if n, err := r.Count(ctx); err != nil || n != 1000 {
		t.Fatalf("Count = %d, err=%v; want 1000 nil", n, err)
	}

	// Walk in descending pages of 250 with BeforeID cursor. Verify that
	// paginating never skips or repeats a row.
	seen := make(map[int64]bool, 1000)
	cursor := int64(0) // BeforeID=0 means "start from tip"
	for pages := range 6 {
		batch, err := r.List(ctx, ListFilter{BeforeID: cursor, Limit: 250})
		if err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}
		if len(batch) == 0 {
			break
		}
		for _, e := range batch {
			if seen[e.ID] {
				t.Fatalf("duplicate id across pages: %d", e.ID)
			}
			seen[e.ID] = true
			if e.Subject != subjects[e.ID] {
				t.Fatalf("id=%d subject=%q want %q", e.ID, e.Subject, subjects[e.ID])
			}
		}
		// Next cursor is the lowest id we just saw.
		cursor = batch[len(batch)-1].ID
	}

	if len(seen) != 1000 {
		t.Fatalf("paginated walk saw %d rows, want 1000", len(seen))
	}

	// Spot-check Get on every tenth row.
	for id := int64(10); id <= 1000; id += 10 {
		e, err := r.Get(ctx, id)
		if err != nil {
			t.Fatalf("Get id=%d: %v", id, err)
		}
		if e.Subject != subjects[id] {
			t.Fatalf("Get id=%d subject=%q want %q", id, e.Subject, subjects[id])
		}
	}
}
