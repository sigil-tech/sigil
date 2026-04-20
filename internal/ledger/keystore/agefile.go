package keystore

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"filippo.io/age"
)

// Environment variables consulted by the age-file backend for passphrase
// sourcing. Checked in this order; the first non-empty source wins.
const (
	envCredentialsDir   = "CREDENTIALS_DIRECTORY"
	envLedgerPassphrase = "SIGILD_LEDGER_KEY_PASSPHRASE"

	// credentialsFileName is the filename inside $CREDENTIALS_DIRECTORY
	// where systemd-credentials drops the passphrase. Matches the convention
	// sigild adopts in its unit file.
	credentialsFileName = "ledger-key-passphrase"

	// agePEMBlockType is the PEM block type used when marshalling an
	// ed25519 private key inside the age-encrypted file. A straight raw
	// ed25519 byte slice would be ambiguous between a private and public
	// key; PEM framing disambiguates and gives future-us a versioning
	// surface.
	agePEMBlockType = "SIGILD ED25519 PRIVATE KEY"
)

// DefaultAgeFilePath returns the standard on-disk location for the
// age-encrypted key file, respecting XDG_STATE_HOME when set.
func DefaultAgeFilePath() string {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "sigild", "keys", "ledger.ed25519.age")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".local", "state", "sigild", "keys", "ledger.ed25519.age")
}

// ageFileKeyStorage is the age-encrypted-file KeyStorage implementation.
// The private key is stored as a PEM block inside a scrypt-passphrase-
// encrypted age ciphertext; the passphrase comes from the environment
// (systemd credentials preferred; operator-set env var fallback).
type ageFileKeyStorage struct {
	path       string
	mu         sync.Mutex
	logger     *slog.Logger
	passphrase func() ([]byte, error)
}

func newAgeFile(_ context.Context, cfg Config, logger *slog.Logger) (KeyStorage, error) {
	path := cfg.AgeFilePath
	if path == "" {
		path = DefaultAgeFilePath()
	}
	return &ageFileKeyStorage{
		path:       path,
		logger:     logger,
		passphrase: readPassphraseFromEnv,
	}, nil
}

func (a *ageFileKeyStorage) Backend() string { return "age-file" }

// Load reads and decrypts the on-disk file, returning ErrKeyNotFound when
// the file does not yet exist (first-boot path). Corruption and passphrase
// errors surface as non-recoverable typed errors so the daemon fails loud
// rather than silently generating a new key.
func (a *ageFileKeyStorage) Load(_ context.Context) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	f, err := os.Open(a.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, ErrKeyNotFound
		}
		return nil, nil, fmt.Errorf("keystore agefile: open %s: %w", a.path, err)
	}
	defer f.Close()

	pass, err := a.passphrase()
	if err != nil {
		return nil, nil, err
	}
	defer wipe(pass)

	ident, err := age.NewScryptIdentity(string(pass))
	if err != nil {
		return nil, nil, fmt.Errorf("keystore agefile: build scrypt identity: %w", err)
	}
	reader, err := age.Decrypt(f, ident)
	if err != nil {
		return nil, nil, fmt.Errorf("keystore agefile: decrypt %s: %w", a.path, err)
	}
	plain, err := io.ReadAll(reader)
	if err != nil {
		return nil, nil, fmt.Errorf("keystore agefile: read decrypted body: %w", err)
	}
	defer wipe(plain)

	block, _ := pem.Decode(plain)
	if block == nil || block.Type != agePEMBlockType {
		return nil, nil, fmt.Errorf("%w: missing or wrong PEM block type", ErrCorruptKey)
	}
	if len(block.Bytes) != ed25519.PrivateKeySize {
		return nil, nil, fmt.Errorf("%w: private key has %d bytes, expected %d", ErrCorruptKey, len(block.Bytes), ed25519.PrivateKeySize)
	}
	priv := ed25519.PrivateKey(append([]byte(nil), block.Bytes...))
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return nil, nil, fmt.Errorf("%w: derived public key has wrong type", ErrCorruptKey)
	}
	return priv, pub, nil
}

// Store encrypts the private key with the configured passphrase and writes
// to a sibling temp file, then renames over the target. The rename is
// atomic within the same filesystem so a mid-write crash cannot leave
// partial ciphertext at the target path.
func (a *ageFileKeyStorage) Store(_ context.Context, priv ed25519.PrivateKey, _ ed25519.PublicKey) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(priv) != ed25519.PrivateKeySize {
		return fmt.Errorf("keystore agefile: private key has %d bytes, expected %d", len(priv), ed25519.PrivateKeySize)
	}

	pass, err := a.passphrase()
	if err != nil {
		return err
	}
	defer wipe(pass)

	if err := os.MkdirAll(filepath.Dir(a.path), 0o700); err != nil {
		return fmt.Errorf("keystore agefile: mkdir %s: %w", filepath.Dir(a.path), err)
	}

	recipient, err := age.NewScryptRecipient(string(pass))
	if err != nil {
		return fmt.Errorf("keystore agefile: build scrypt recipient: %w", err)
	}

	// Encode the private key as PEM. age-encrypted ciphertext is the outer
	// wrapper, so the PEM is only visible after decryption.
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  agePEMBlockType,
		Bytes: priv,
	})
	defer wipe(pemBytes)

	var ciphertext bytes.Buffer
	w, err := age.Encrypt(&ciphertext, recipient)
	if err != nil {
		return fmt.Errorf("keystore agefile: start encrypt: %w", err)
	}
	if _, err := w.Write(pemBytes); err != nil {
		return fmt.Errorf("keystore agefile: encrypt write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("keystore agefile: encrypt close: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(a.path), ".ledger.ed25519.age.tmp-*")
	if err != nil {
		return fmt.Errorf("keystore agefile: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		// If the rename succeeded, Remove will no-op.
		_ = os.Remove(tmpPath)
	}()

	if err := os.Chmod(tmpPath, 0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("keystore agefile: chmod %s: %w", tmpPath, err)
	}
	if _, err := tmp.Write(ciphertext.Bytes()); err != nil {
		tmp.Close()
		return fmt.Errorf("keystore agefile: write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("keystore agefile: fsync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("keystore agefile: close temp: %w", err)
	}
	if err := os.Rename(tmpPath, a.path); err != nil {
		return fmt.Errorf("keystore agefile: rename onto %s: %w", a.path, err)
	}

	return nil
}

// readPassphraseFromEnv implements the passphrase-sourcing rule in FR-013.
// Order: $CREDENTIALS_DIRECTORY/ledger-key-passphrase first (the
// systemd-credentials convention), then $SIGILD_LEDGER_KEY_PASSPHRASE.
// Trailing newlines are stripped because credential managers commonly
// append one.
func readPassphraseFromEnv() ([]byte, error) {
	if dir := os.Getenv(envCredentialsDir); dir != "" {
		data, err := os.ReadFile(filepath.Join(dir, credentialsFileName))
		switch {
		case err == nil:
			return bytes.TrimRight(data, "\r\n"), nil
		case errors.Is(err, os.ErrNotExist):
			// fall through to env var fallback
		default:
			return nil, fmt.Errorf("keystore agefile: read %s: %w", filepath.Join(dir, credentialsFileName), err)
		}
	}
	if pass := os.Getenv(envLedgerPassphrase); pass != "" {
		return []byte(pass), nil
	}
	return nil, ErrPassphraseUnavailable
}
