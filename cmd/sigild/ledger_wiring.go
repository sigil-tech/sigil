package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/sigil-tech/sigil/internal/ledger"
	"github.com/sigil-tech/sigil/internal/ledger/keystore"
	"github.com/sigil-tech/sigil/internal/socket"
)

// ledgerBundle aggregates the four audit-ledger components the daemon
// needs: the key storage backend, the public-key registry, the
// chain reader, and the chain verifier. The Emitter is the single
// append path; subsystems that need to emit (vm, merge, finetuner,
// policy) wrap it in per-subsystem adapters.
//
// Wiring split: the handler layer only needs Reader / Verifier /
// Registry; the subsystem adapters need the Emitter. Keeping the
// bundle self-contained avoids passing half-a-dozen ledger handles
// through the daemon's setup chain.
type ledgerBundle struct {
	Emitter  ledger.Emitter
	Reader   ledger.Reader
	Verifier ledger.Verifier
	Registry ledger.KeyRegistry
	Keystore keystore.KeyStorage
}

// setupLedger runs the daemon-side ledger migration, picks a
// keystore backend via Choose (spec 029 §3 autodetect), and
// constructs the four collaborators. Returns an error if any step
// fails — the daemon MUST refuse to start if the ledger cannot be
// brought up, because every privileged action emits through it.
func setupLedger(ctx context.Context, db *sql.DB, log *slog.Logger) (*ledgerBundle, error) {
	if err := ledger.Migrate(ctx, db); err != nil {
		return nil, fmt.Errorf("sigild: ledger migrate: %w", err)
	}
	ks, err := keystore.Choose(ctx, keystore.Config{}, log)
	if err != nil {
		return nil, fmt.Errorf("sigild: keystore choose: %w", err)
	}
	reg := ledger.NewKeyRegistry(db)
	em := ledger.NewEmitter(db, ks, reg)
	return &ledgerBundle{
		Emitter:  em,
		Reader:   ledger.NewReader(db),
		Verifier: ledger.NewVerifier(db, reg),
		Registry: reg,
		Keystore: ks,
	}, nil
}

// registerLedgerWiring glues the bundle's Reader / Verifier / Registry
// into the socket server's ledger-* handlers. Subsystem Emitter
// wiring (via WithLedger on vm.Manager, MergeWithLedger, etc.) is
// done separately by each subsystem's setup site.
func registerLedgerWiring(srv *socket.Server, bundle *ledgerBundle) {
	registerLedgerHandlers(srv, bundle.Reader, bundle.Verifier, bundle.Registry)
}
