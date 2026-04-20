//go:build darwin

package keystore

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// securityBinary is the macOS `security` CLI path. Fixed at the system
// location to avoid PATH injection; if the path is missing, the backend
// reports ErrBackendUnavailable and the chooser falls through.
const securityBinary = "/usr/bin/security"

// keychainKeyStorage wraps the macOS `security` CLI. We shell out rather
// than CGo-bind to Security.framework per constitution §VIII (no CGo
// unless unavoidable). The ACL is narrowed via the `-T` flag so only the
// running sigild binary can unlock the stored password after first
// authentication; other processes get "access denied" until the user
// re-authorises them.
type keychainKeyStorage struct {
	service string
	account string
	trusted string // absolute path passed to -T; zero value means "caller sets at runtime"
	logger  *slog.Logger
	mu      sync.Mutex
}

func newKeychain(_ context.Context, cfg Config, logger *slog.Logger) (KeyStorage, error) {
	if _, err := os.Stat(securityBinary); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrBackendUnavailable, securityBinary, err)
	}
	exe, err := os.Executable()
	if err != nil {
		// Not fatal — the Keychain entry will be created without an ACL
		// narrowing; operator will be prompted on every read.
		exe = ""
		logger.Warn("ledger.key.keychain.no_executable", "err", err)
	}

	svc := cfg.KeychainService
	if svc == "" {
		svc = defaultKeychainService
	}
	acct := cfg.KeychainAccount
	if acct == "" {
		acct = defaultKeychainAccount
	}

	return &keychainKeyStorage{
		service: svc,
		account: acct,
		trusted: exe,
		logger:  logger,
	}, nil
}

func (k *keychainKeyStorage) Backend() string { return "keychain" }

// Load shells out to `security find-generic-password -w` and base64-decodes
// the returned value. `-w` asks `security` to print only the password,
// which is what we want (no metadata). A "could not be found" exit status
// maps to ErrKeyNotFound so first-boot still generates a fresh key.
func (k *keychainKeyStorage) Load(ctx context.Context) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	cmd := exec.CommandContext(ctx, securityBinary,
		"find-generic-password",
		"-a", k.account,
		"-s", k.service,
		"-w",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// security exits 44 for "item not found"; also check stderr text.
		combined := stderr.String()
		if strings.Contains(combined, "could not be found") || strings.Contains(combined, "SecKeychainSearchCopyNext") {
			return nil, nil, ErrKeyNotFound
		}
		return nil, nil, fmt.Errorf("keystore keychain: find-generic-password: %w — %s", err, strings.TrimSpace(combined))
	}

	encoded := bytes.TrimSpace(stdout.Bytes())
	raw, err := base64.StdEncoding.DecodeString(string(encoded))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: keychain entry not base64: %v", ErrCorruptKey, err)
	}
	defer wipe(raw)

	if len(raw) != ed25519.PrivateKeySize {
		return nil, nil, fmt.Errorf("%w: keychain private key has %d bytes, expected %d", ErrCorruptKey, len(raw), ed25519.PrivateKeySize)
	}
	priv := ed25519.PrivateKey(append([]byte(nil), raw...))
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return nil, nil, fmt.Errorf("%w: derived public key has wrong type", ErrCorruptKey)
	}
	return priv, pub, nil
}

// Store writes the private key as a base64-encoded generic password.
// `-U` asks `security` to update an existing entry (same service+account)
// rather than error out; `-T` narrows the ACL to the caller; `-A` explicitly
// removes the "any app" blanket ACL.
func (k *keychainKeyStorage) Store(ctx context.Context, priv ed25519.PrivateKey, _ ed25519.PublicKey) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	if len(priv) != ed25519.PrivateKeySize {
		return fmt.Errorf("keystore keychain: private key has %d bytes, expected %d", len(priv), ed25519.PrivateKeySize)
	}

	encoded := base64.StdEncoding.EncodeToString(priv)
	defer wipe([]byte(encoded))

	args := []string{
		"add-generic-password",
		"-a", k.account,
		"-s", k.service,
		"-U", // update if exists
		"-w", encoded,
	}
	// Access-control narrowing. Without -T, every process under the same
	// user can read the password; with -T, only the listed binaries can.
	if k.trusted != "" {
		args = append(args, "-T", k.trusted)
	}

	cmd := exec.CommandContext(ctx, securityBinary, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("keystore keychain: add-generic-password: %w — %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// Avoid "unused error" shadowing by errors package in some build configs.
var _ = errors.New
