package keystore

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"io"
	"log/slog"
	"testing"
)

// TestPassphraseMemory asserts the memory-hygiene helpers in secrets.go:
// wipe zeroes its input, and constantTimeEqual returns correctly across
// equal / unequal / length-mismatch cases. The underlying protections
// (runtime.KeepAlive, subtle.ConstantTimeCompare) are stdlib — this test
// confirms the contracts that call sites depend on rather than the
// runtime implementation details.
func TestPassphraseMemory(t *testing.T) {
	t.Run("wipe zeros every byte", func(t *testing.T) {
		buf := []byte("correct horse battery staple")
		wipe(buf)
		for i, b := range buf {
			if b != 0 {
				t.Fatalf("buf[%d] = %d, want 0", i, b)
			}
		}
	})

	t.Run("wipe on nil / empty is a no-op", func(t *testing.T) {
		// Must not panic.
		wipe(nil)
		wipe([]byte{})
	})

	t.Run("constantTimeEqual true when bytes match", func(t *testing.T) {
		a := []byte("abc123")
		b := []byte("abc123")
		if !constantTimeEqual(a, b) {
			t.Fatalf("expected equal, got unequal")
		}
	})

	t.Run("constantTimeEqual false when bytes differ", func(t *testing.T) {
		a := []byte("abc123")
		b := []byte("abc124")
		if constantTimeEqual(a, b) {
			t.Fatalf("expected unequal, got equal")
		}
	})

	t.Run("constantTimeEqual false when lengths differ", func(t *testing.T) {
		a := []byte("abc")
		b := []byte("abc123")
		if constantTimeEqual(a, b) {
			t.Fatalf("expected unequal across length mismatch, got equal")
		}
	})
}

// TestPassphraseMemory_AgeFileWipesBuffers asserts that the age-file
// backend wipes passphrase buffers after use. We pass an injected
// passphrase function that hands out a retained slice, then inspect the
// slice after Store returns — the deferred wipe must have zeroed it.
//
// This does not prove the daemon-shipped passphrase source (env var or
// systemd credential) is wiped after use, because those return fresh
// slices each call. But it does catch a regression where a future
// refactor forgets the `defer wipe(pass)` at any call site.
func TestPassphraseMemory_AgeFileWipesBuffers(t *testing.T) {
	dir := t.TempDir()
	secret := []byte("hunter2 hunter2 hunter2")
	retained := append([]byte(nil), secret...)

	a := &ageFileKeyStorage{
		path:   dir + "/ledger.age",
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		passphrase: func() ([]byte, error) {
			return retained, nil
		},
	}

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if err := a.Store(context.Background(), priv, pub); err != nil {
		t.Fatalf("Store: %v", err)
	}

	// After Store, the `defer wipe(pass)` should have zeroed the slice
	// we handed in. A zeroed slice is NOT equal to the original secret.
	if bytes.Equal(retained, secret) {
		t.Fatalf("passphrase buffer still contains original secret — wipe did not run")
	}
	for i, b := range retained {
		if b != 0 {
			t.Fatalf("passphrase buffer not fully zeroed at index %d: got %d", i, b)
		}
	}
}
