package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/sigil-tech/sigil/internal/ledger"
	"github.com/sigil-tech/sigil/internal/socket"
)

// registerLedgerHandlers wires the four spec 029 socket methods. Per
// FR-025 the daemon bumps ProtocolVersion by one for the additive
// surface — pre-4 clients keep working because the new methods are
// additions, never modifications to existing method shapes.
//
// Scope note: the handler implementations live in this file (rather
// than internal/socket/handlers.go as the tasks.md file names
// suggested) because the socket package explicitly has "no internal
// imports" (sigil/CLAUDE.md DAG rule). Keeping ledger-aware handlers
// in cmd/sigild/ preserves that invariant while grouping by daemon
// subsystem — the same pattern the other handler_*.go files follow.
func registerLedgerHandlers(srv *socket.Server, reader ledger.Reader, verifier ledger.Verifier, registry ledger.KeyRegistry) {
	srv.Handle("ledger-list", handleLedgerList(reader))
	srv.Handle("ledger-get", handleLedgerGet(reader))
	srv.Handle("ledger-verify", handleLedgerVerify(verifier))
	srv.Handle("ledger-key", handleLedgerKey(registry))
}

// registerLedgerRotateHandler wires ledger-key-rotate to a real
// Rotator. Kept separate from registerLedgerHandlers because the
// Rotator depends on the Emitter + KeyStorage (both already wired in
// the daemon startup bundle), so the tests in
// handler_ledger_test.go can exercise the read-side handlers without
// constructing a full Rotator.
func registerLedgerRotateHandler(srv *socket.Server, rotator ledger.Rotator) {
	srv.Handle("ledger-key-rotate", handleLedgerKeyRotate(rotator))
}

type ledgerKeyRotateRequest struct {
	Reason string `json:"reason"`
}

func handleLedgerKeyRotate(rotator ledger.Rotator) socket.HandlerFunc {
	return func(ctx context.Context, req socket.Request) socket.Response {
		var p ledgerKeyRotateRequest
		if req.Payload != nil {
			if err := json.Unmarshal(req.Payload, &p); err != nil {
				return socket.Response{Error: fmt.Sprintf("ledger-key-rotate: invalid payload: %v", err)}
			}
		}
		if p.Reason == "" {
			p.Reason = "operator-initiated rotation"
		}
		result, err := rotator.Rotate(ctx, p.Reason)
		if err != nil {
			return socket.Response{Error: fmt.Sprintf("ledger-key-rotate: %v", err)}
		}
		return socket.Response{OK: true, Payload: socket.MarshalPayload(result)}
	}
}

// ledgerListRequest matches the spec 029 wire contract for ledger-list.
// BeforeID is the paginate-before cursor (exclusive upper bound);
// AfterID is reserved for a future spec (see contracts/ledger-wire.md
// Phase 6 populate); TypeFilter narrows to one event type; Limit is
// clamped to ledger.MaxListLimit.
type ledgerListRequest struct {
	BeforeID   int64  `json:"before_id,omitempty"`
	AfterID    int64  `json:"after_id,omitempty"`
	TypeFilter string `json:"type_filter,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

func handleLedgerList(reader ledger.Reader) socket.HandlerFunc {
	return func(ctx context.Context, req socket.Request) socket.Response {
		var p ledgerListRequest
		if req.Payload != nil {
			if err := json.Unmarshal(req.Payload, &p); err != nil {
				return socket.Response{Error: fmt.Sprintf("ledger-list: invalid payload: %v", err)}
			}
		}
		entries, err := reader.List(ctx, ledger.ListFilter{
			BeforeID:   p.BeforeID,
			TypeFilter: p.TypeFilter,
			Limit:      p.Limit,
		})
		if err != nil {
			return socket.Response{Error: fmt.Sprintf("ledger-list: %v", err)}
		}
		return socket.Response{OK: true, Payload: socket.MarshalPayload(map[string]any{
			"entries": entries,
		})}
	}
}

type ledgerGetRequest struct {
	ID int64 `json:"id"`
}

func handleLedgerGet(reader ledger.Reader) socket.HandlerFunc {
	return func(ctx context.Context, req socket.Request) socket.Response {
		var p ledgerGetRequest
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return socket.Response{Error: fmt.Sprintf("ledger-get: invalid payload: %v", err)}
		}
		if p.ID <= 0 {
			return socket.Response{Error: "ledger-get: id must be > 0"}
		}
		entry, err := reader.Get(ctx, p.ID)
		if err != nil {
			if errors.Is(err, ledger.ErrEntryNotFound) {
				return socket.Response{Error: fmt.Sprintf("ledger-get: id=%d not found", p.ID)}
			}
			return socket.Response{Error: fmt.Sprintf("ledger-get: %v", err)}
		}
		return socket.Response{OK: true, Payload: socket.MarshalPayload(entry)}
	}
}

type ledgerVerifyRequest struct {
	// Scope. At most one of {Full, (FromID/ToID pair), ID} should be
	// set. An empty request defaults to Full=true.
	Full   bool  `json:"full,omitempty"`
	FromID int64 `json:"from_id,omitempty"`
	ToID   int64 `json:"to_id,omitempty"`
	ID     int64 `json:"id,omitempty"` // single-entry shortcut: id==ID == from==to
}

func handleLedgerVerify(verifier ledger.Verifier) socket.HandlerFunc {
	return func(ctx context.Context, req socket.Request) socket.Response {
		var p ledgerVerifyRequest
		if req.Payload != nil {
			if err := json.Unmarshal(req.Payload, &p); err != nil {
				return socket.Response{Error: fmt.Sprintf("ledger-verify: invalid payload: %v", err)}
			}
		}
		scope := ledger.VerifyScope{
			Full:   p.Full,
			FromID: p.FromID,
			ToID:   p.ToID,
		}
		// Single-entry shortcut: id == from == to.
		if p.ID > 0 {
			scope.FromID = p.ID
			scope.ToID = p.ID
			scope.Full = false
		}
		// Default to full chain when the caller supplied no scope.
		if !scope.Full && scope.FromID == 0 && scope.ToID == 0 {
			scope.Full = true
		}
		result, err := verifier.Verify(ctx, scope)
		if err != nil {
			return socket.Response{Error: fmt.Sprintf("ledger-verify: %v", err)}
		}
		return socket.Response{OK: true, Payload: socket.MarshalPayload(result)}
	}
}

func handleLedgerKey(registry ledger.KeyRegistry) socket.HandlerFunc {
	return func(ctx context.Context, _ socket.Request) socket.Response {
		all, err := registry.ListAll(ctx)
		if err != nil {
			return socket.Response{Error: fmt.Sprintf("ledger-key: %v", err)}
		}
		var active, retired []ledger.KeyRecord
		for _, k := range all {
			if k.Active() {
				active = append(active, k)
			} else {
				retired = append(retired, k)
			}
		}
		return socket.Response{OK: true, Payload: socket.MarshalPayload(map[string]any{
			"active":  active,
			"retired": retired,
		})}
	}
}
