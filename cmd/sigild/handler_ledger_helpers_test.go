package main

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"sync"
	"testing"

	"github.com/sigil-tech/sigil/internal/ledger"
	"github.com/sigil-tech/sigil/internal/ledger/keystore"

	_ "modernc.org/sqlite"
)

// handlerTestKeystore is an in-process KeyStorage used by the
// handler tests. The production backends live in
// internal/ledger/keystore; tests only need an in-memory stand-in.
type handlerTestKeystore struct {
	mu   sync.Mutex
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

func (m *handlerTestKeystore) Load(context.Context) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.priv == nil {
		return nil, nil, keystore.ErrKeyNotFound
	}
	return m.priv, m.pub, nil
}

func (m *handlerTestKeystore) Store(_ context.Context, priv ed25519.PrivateKey, pub ed25519.PublicKey) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.priv, m.pub = priv, pub
	return nil
}

func (m *handlerTestKeystore) Backend() string { return "memory" }

// newLedgerTest spins up a fresh in-memory SQLite + migrated ledger +
// Emitter for handler tests. Returns the db, the keystore (rarely
// needed), the KeyRegistry (for TestHandleLedgerKey), and the
// Emitter (for the seeding loop in ledgerTestDB).
func newLedgerTest(t *testing.T) (*sql.DB, keystore.KeyStorage, ledger.KeyRegistry, ledger.Emitter) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	if err := ledger.Migrate(context.Background(), db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	ks := &handlerTestKeystore{}
	reg := ledger.NewKeyRegistry(db)
	em := ledger.NewEmitter(db, ks, reg)
	return db, ks, reg, em
}
