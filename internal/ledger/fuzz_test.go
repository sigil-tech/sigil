package ledger

import (
	"context"
	"testing"
)

// FuzzTamperDetection is Task 4.4 — spec 029 SC-002: every
// single-column tamper in a live ledger MUST be detected by the
// Verifier. The fuzz engine drives three inputs:
//
//   - a row id in [1..N]
//   - a column enum [0..5] picking one of (ts, type, subject,
//     payload_json, prev_hash, signature) to mutate
//   - a replacement-byte index + replacement byte, producing a
//     deterministic single-byte perturbation of the chosen column
//
// Each iteration:
//
//  1. Builds a fresh in-memory ledger with N=5 rows.
//  2. Disables the append-only trigger for a single UPDATE.
//  3. Mutates the selected column on the selected row.
//  4. Runs Verify and asserts OK=false with a BreakAtID ≤ targetID
//     (tamper on row K can surface at row K or any later row that
//     chains off row K's hash — the Verifier may flag the prev_hash
//     mismatch on K+1 before it re-hashes K).
//
// Run with `go test -fuzz=FuzzTamperDetection -fuzztime=60s
// ./internal/ledger/`.
func FuzzTamperDetection(f *testing.F) {
	// Seed corpus — one row per column, spread across the ledger.
	f.Add(1, 0, 0, byte(0xff)) // ts on row 1
	f.Add(2, 1, 0, byte(0x5a)) // type on row 2
	f.Add(3, 2, 0, byte(0x42)) // subject
	f.Add(4, 3, 1, byte(0x00)) // payload_json
	f.Add(5, 4, 0, byte(0x00)) // prev_hash
	f.Add(5, 5, 0, byte(0xde)) // signature

	const N = 5

	f.Fuzz(func(t *testing.T, rowID int, colIdx int, byteIdx int, newByte byte) {
		rowID = ((rowID%N)+N)%N + 1 // coerce into [1..N]
		col := colIdx % 6
		if col < 0 {
			col += 6
		}

		ctx := context.Background()
		db, _, reg, em := newTestEmitter(t, nil)
		var rows []Entry
		for i := 1; i <= N; i++ {
			e, err := em.Emit(ctx, Event{
				Type:    EventVMSpawn,
				Subject: "fuzz",
				Payload: map[string]any{"i": i},
			})
			if err != nil {
				t.Fatalf("Emit %d: %v", i, err)
			}
			rows = append(rows, e)
		}

		// Drop the no-update trigger for this one mutation; we never
		// reinstate it — the DB is test-scoped and will be torn down.
		if _, err := db.ExecContext(ctx, `DROP TRIGGER IF EXISTS ledger_no_update`); err != nil {
			t.Fatalf("drop trigger: %v", err)
		}

		target := rows[rowID-1]
		col, mutated, ok := tamperColumn(target, col, byteIdx, newByte)
		if !ok {
			t.Skip() // mutation was a no-op (same byte) — no signal to test.
		}

		query := updateForColumn(col)
		if query == "" {
			t.Skip()
		}
		if _, err := db.ExecContext(ctx, query, mutated, rowID); err != nil {
			t.Fatalf("UPDATE: %v", err)
		}

		v := NewVerifier(db, reg)
		r, err := v.Verify(ctx, VerifyScope{Full: true})
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if r.OK {
			t.Fatalf("tamper on row %d column %d went undetected: %+v", rowID, col, r)
		}
		// Break may land on the tampered row or later rows whose
		// prev_hash references it; the earliest break point is what
		// we care about.
		if r.BreakAtID < int64(rowID) {
			t.Fatalf("BreakAtID=%d before tamper row %d: %+v", r.BreakAtID, rowID, r)
		}
	})
}

// tamperColumn returns the mutated column value for the chosen column.
// byteIdx selects which byte within the (possibly hex-encoded) column
// to flip; if the chosen byte already matches the newByte, returns
// ok=false so the fuzz harness can skip the no-op case.
func tamperColumn(e Entry, col int, byteIdx int, newByte byte) (int, string, bool) {
	var current string
	switch col {
	case 0:
		current = e.Timestamp
	case 1:
		current = e.Type
	case 2:
		current = e.Subject
	case 3:
		current = e.PayloadJSON
	case 4:
		current = e.PrevHash
	case 5:
		current = e.Signature
	default:
		return col, "", false
	}
	if len(current) == 0 {
		return col, "", false
	}
	buf := []byte(current)
	byteIdx = ((byteIdx % len(buf)) + len(buf)) % len(buf)

	// For hex-encoded columns (prev_hash, signature), keep the output
	// in the hex charset so Verify's decode step doesn't error out
	// BEFORE the hash / signature check — we want to exercise the
	// detection path, not the parse path.
	var replacement byte
	if col == 4 || col == 5 {
		replacement = "0123456789abcdef"[int(newByte)&0x0f]
	} else {
		// Restrict to printable ASCII for the non-hex columns so the
		// resulting bytes are still legal TEXT in SQLite. The STRICT
		// table enforces TEXT; non-UTF-8 bytes would be rejected by
		// the driver before Verify ever runs.
		replacement = 0x20 + (newByte % 0x5e)
	}
	// Skip the no-op case — a "replacement" that equals the original
	// byte mutates nothing, so there is no tamper for Verify to detect.
	if buf[byteIdx] == replacement {
		return col, "", false
	}
	buf[byteIdx] = replacement
	return col, string(buf), true
}

// updateForColumn returns the single-column UPDATE statement for the
// chosen column. Kept as a switch rather than a map so the SQL text
// is lexically visible in grep-driven code review.
func updateForColumn(col int) string {
	switch col {
	case 0:
		return `UPDATE ledger SET ts = ? WHERE id = ?`
	case 1:
		return `UPDATE ledger SET type = ? WHERE id = ?`
	case 2:
		return `UPDATE ledger SET subject = ? WHERE id = ?`
	case 3:
		return `UPDATE ledger SET payload_json = ? WHERE id = ?`
	case 4:
		return `UPDATE ledger SET prev_hash = ? WHERE id = ?`
	case 5:
		return `UPDATE ledger SET signature = ? WHERE id = ?`
	}
	return ""
}
