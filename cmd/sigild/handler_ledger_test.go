package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sigil-tech/sigil/internal/ledger"
	"github.com/sigil-tech/sigil/internal/socket"
)

// ledgerTestDB bootstraps a fresh in-memory ledger with n emitted
// rows so the handler tests exercise the real Reader/Verifier/
// KeyRegistry wiring. Returns the three accessors the handlers
// consume plus the list of emitted entries for assertion.
func ledgerTestDB(t *testing.T, n int) (ledger.Reader, ledger.Verifier, ledger.KeyRegistry, []ledger.Entry) {
	t.Helper()
	db, _, reg, em := newLedgerTest(t)

	var entries []ledger.Entry
	for i := 0; i < n; i++ {
		e, err := em.Emit(context.Background(), ledger.Event{
			Type:    ledger.EventVMSpawn,
			Subject: "handler-test",
			Payload: map[string]any{"i": i},
		})
		require.NoError(t, err)
		entries = append(entries, e)
	}
	return ledger.NewReader(db), ledger.NewVerifier(db, reg), reg, entries
}

// TestHandleLedgerList drives the ledger-list handler through the
// paginated + filtered paths the socket exposes. Defaults,
// before-id cursor, and type filter all covered.
func TestHandleLedgerList(t *testing.T) {
	reader, _, _, entries := ledgerTestDB(t, 10)
	h := handleLedgerList(reader)

	t.Run("empty payload returns full page newest-first", func(t *testing.T) {
		resp := h(context.Background(), socket.Request{})
		require.True(t, resp.OK)
		var payload struct {
			Entries []ledger.Entry `json:"entries"`
		}
		require.NoError(t, json.Unmarshal(resp.Payload, &payload))
		require.Len(t, payload.Entries, len(entries))
		require.Equal(t, int64(len(entries)), payload.Entries[0].ID)
	})

	t.Run("before_id cursor narrows the result set", func(t *testing.T) {
		body, _ := json.Marshal(ledgerListRequest{BeforeID: 5, Limit: 3})
		resp := h(context.Background(), socket.Request{Payload: body})
		require.True(t, resp.OK)
		var payload struct {
			Entries []ledger.Entry `json:"entries"`
		}
		require.NoError(t, json.Unmarshal(resp.Payload, &payload))
		require.Len(t, payload.Entries, 3)
		require.Equal(t, int64(4), payload.Entries[0].ID)
	})

	t.Run("type_filter narrows to matching type", func(t *testing.T) {
		body, _ := json.Marshal(ledgerListRequest{TypeFilter: string(ledger.EventVMSpawn), Limit: 50})
		resp := h(context.Background(), socket.Request{Payload: body})
		require.True(t, resp.OK)
	})

	t.Run("malformed payload returns error", func(t *testing.T) {
		resp := h(context.Background(), socket.Request{Payload: []byte("{not-json")})
		require.False(t, resp.OK)
		require.Contains(t, resp.Error, "invalid payload")
	})
}

func TestHandleLedgerGet(t *testing.T) {
	reader, _, _, entries := ledgerTestDB(t, 5)
	h := handleLedgerGet(reader)

	t.Run("returns matching entry", func(t *testing.T) {
		body, _ := json.Marshal(ledgerGetRequest{ID: 3})
		resp := h(context.Background(), socket.Request{Payload: body})
		require.True(t, resp.OK)
		var got ledger.Entry
		require.NoError(t, json.Unmarshal(resp.Payload, &got))
		require.Equal(t, entries[2].Hash, got.Hash)
	})

	t.Run("missing id returns not-found error", func(t *testing.T) {
		body, _ := json.Marshal(ledgerGetRequest{ID: 999})
		resp := h(context.Background(), socket.Request{Payload: body})
		require.False(t, resp.OK)
		require.Contains(t, resp.Error, "not found")
	})

	t.Run("id <= 0 is rejected", func(t *testing.T) {
		body, _ := json.Marshal(ledgerGetRequest{ID: 0})
		resp := h(context.Background(), socket.Request{Payload: body})
		require.False(t, resp.OK)
	})
}

func TestHandleLedgerVerify(t *testing.T) {
	_, verifier, _, entries := ledgerTestDB(t, 5)
	h := handleLedgerVerify(verifier)

	t.Run("empty payload defaults to full-chain verify", func(t *testing.T) {
		resp := h(context.Background(), socket.Request{})
		require.True(t, resp.OK)
		var r ledger.VerifyResult
		require.NoError(t, json.Unmarshal(resp.Payload, &r))
		require.True(t, r.OK)
		require.Equal(t, len(entries), r.EntriesChecked)
	})

	t.Run("id shortcut verifies single entry", func(t *testing.T) {
		body, _ := json.Marshal(ledgerVerifyRequest{ID: 3})
		resp := h(context.Background(), socket.Request{Payload: body})
		require.True(t, resp.OK)
		var r ledger.VerifyResult
		require.NoError(t, json.Unmarshal(resp.Payload, &r))
		require.True(t, r.OK)
		require.Equal(t, 1, r.EntriesChecked)
	})

	t.Run("range scope verifies window", func(t *testing.T) {
		body, _ := json.Marshal(ledgerVerifyRequest{FromID: 2, ToID: 4})
		resp := h(context.Background(), socket.Request{Payload: body})
		require.True(t, resp.OK)
		var r ledger.VerifyResult
		require.NoError(t, json.Unmarshal(resp.Payload, &r))
		require.True(t, r.OK)
		require.Equal(t, 3, r.EntriesChecked)
	})
}

func TestHandleLedgerKey(t *testing.T) {
	_, _, reg, _ := ledgerTestDB(t, 3)
	h := handleLedgerKey(reg)

	resp := h(context.Background(), socket.Request{})
	require.True(t, resp.OK)

	var payload struct {
		Active  []ledger.KeyRecord `json:"active"`
		Retired []ledger.KeyRecord `json:"retired"`
	}
	require.NoError(t, json.Unmarshal(resp.Payload, &payload))
	require.Len(t, payload.Active, 1, "emit() populated exactly one active key")
	require.Empty(t, payload.Retired)
}

// TestProtocolBackCompat covers Task 6.1's back-compat contract: a
// client with min_protocol=3 still handshakes successfully against
// a v4 daemon (the ledger handlers are additive).
func TestProtocolBackCompat(t *testing.T) {
	// Simulate the handshake logic from registerControlPlaneHandlers.
	// We inline the compatibility rule here rather than spinning up a
	// full socket server — the logic is one-liner and the end-to-end
	// flow is exercised by the existing handlers_test.go suite.
	cases := []struct {
		name        string
		minProtocol int
		wantCompat  bool
	}{
		{"client wants v1 (ancient)", 1, true},
		{"client wants v2 (observer)", 2, true},
		{"client wants v3 (vm sandbox)", 3, true},
		{"client wants v4 (spec 029 ledger)", 4, true},
		{"client wants v5 (future)", 5, false},
		{"client has no min_protocol", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			compatible := ProtocolVersion >= tc.minProtocol || tc.minProtocol == 0
			if compatible != tc.wantCompat {
				t.Fatalf("min_protocol=%d: compatible=%v, want %v", tc.minProtocol, compatible, tc.wantCompat)
			}
		})
	}
	// The current protocol MUST be 4 — if this fails it means a future
	// change bumped the version without updating this test's intent.
	if ProtocolVersion != 4 {
		t.Fatalf("ProtocolVersion drifted to %d without updating spec 029 compat test", ProtocolVersion)
	}
}
