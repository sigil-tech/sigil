package keystore

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
)

// Default identifiers shared by the Keychain and Secret Service backends
// — both need a stable ("service", "account") pair for the same underlying
// secret. Defined at package scope so both platform-specific files can
// reference them.
const (
	defaultKeychainService = "tech.sigil.ledger"
	defaultKeychainAccount = "sigild"
)

// Sentinel errors. Callers use errors.Is to distinguish them.
var (
	// ErrKeyNotFound is returned by Load when no signing key exists yet
	// (first-boot path). The daemon generates a fresh keypair in this case
	// and writes it via Store.
	ErrKeyNotFound = errors.New("keystore: key not found")

	// ErrBackendUnavailable is returned by a backend's constructor when the
	// underlying storage facility is not reachable (e.g., no Secret Service
	// on the DBus system bus, or /usr/bin/security missing). Triggers
	// fallback in the Choose function.
	ErrBackendUnavailable = errors.New("keystore: backend unavailable")

	// ErrPassphraseUnavailable is returned by the age-file backend when
	// neither $CREDENTIALS_DIRECTORY nor $SIGILD_LEDGER_KEY_PASSPHRASE
	// supplies a passphrase. The daemon refuses to start in this case per
	// FR-013.
	ErrPassphraseUnavailable = errors.New("keystore: passphrase unavailable")

	// ErrCorruptKey indicates the on-disk / in-keychain material failed to
	// decode as a valid ed25519 keypair. Treated as fatal — operator must
	// restore from backup or explicitly reset.
	ErrCorruptKey = errors.New("keystore: stored key material is corrupt")
)

// Config holds the runtime-selectable parameters for the key storage
// backend chooser. Defaults are sensible for typical single-user
// installations; operators overriding these take responsibility for the
// deviation.
type Config struct {
	// AgeFilePath is the on-disk location used by the AgeFile backend. If
	// empty, DefaultAgeFilePath is used (~/.local/state/sigild/keys/
	// ledger.ed25519.age).
	AgeFilePath string

	// KeychainService is the `-s` name used by the macOS Keychain backend.
	// Defaults to "tech.sigil.ledger". Overridable for CI / test isolation.
	KeychainService string

	// KeychainAccount is the `-a` name used by the macOS Keychain backend.
	// Defaults to "sigild".
	KeychainAccount string

	// SecretServiceLabel is the display label applied to the Secret Service
	// entry on Linux. Defaults to "Sigil Ledger Signing Key".
	SecretServiceLabel string

	// ForceBackend, if non-empty, overrides runtime-autodetection. Valid
	// values: "keychain", "secret-service", "age-file". Used by tests and
	// by operators who want to pin a specific backend regardless of what
	// the autodetector would pick.
	ForceBackend string
}

// KeyStorage is the abstraction every signing-key backend satisfies.
// Implementations MUST be safe for concurrent Load calls and MUST
// serialise Store calls (the chain's single-writer invariant extends here
// — two parallel Stores would race the private key).
type KeyStorage interface {
	// Load returns the active ed25519 keypair. Returns ErrKeyNotFound on
	// first boot; any other error is fatal (daemon refuses to start).
	Load(ctx context.Context) (ed25519.PrivateKey, ed25519.PublicKey, error)

	// Store writes a new keypair, replacing any existing active key. MUST
	// be atomic: either the new key is fully written, or the previous key
	// survives unchanged.
	Store(ctx context.Context, priv ed25519.PrivateKey, pub ed25519.PublicKey) error

	// Backend returns the stable string identifier used in structured
	// logging and `sigilctl ledger key` output. One of "keychain",
	// "secret-service", "age-file".
	Backend() string
}

// Choose returns the KeyStorage backend appropriate for the current host.
// The selection order on each platform, with fallthroughs on
// ErrBackendUnavailable, is:
//
//	darwin : keychain → age-file
//	linux  : secret-service → age-file
//	other  : age-file
//
// Errors other than ErrBackendUnavailable abort the chooser — a
// misconfigured Keychain that returns an unrelated error is NOT silently
// downgraded to age-file (that would mask a security-relevant regression).
//
// Config.ForceBackend skips autodetection and returns the named backend
// directly, or fails if the named backend is unavailable on this platform.
func Choose(ctx context.Context, cfg Config, logger *slog.Logger) (KeyStorage, error) {
	if logger == nil {
		logger = slog.Default()
	}

	if cfg.ForceBackend != "" {
		return chooseForced(ctx, cfg, logger)
	}

	var primary func(context.Context, Config, *slog.Logger) (KeyStorage, error)
	switch runtime.GOOS {
	case "darwin":
		primary = newKeychain
	case "linux":
		primary = newSecretService
	}

	if primary != nil {
		ks, err := primary(ctx, cfg, logger)
		switch {
		case err == nil:
			logger.Info("ledger.key.backend", "backend", ks.Backend())
			return ks, nil
		case errors.Is(err, ErrBackendUnavailable):
			logger.Warn("ledger.key.backend_fallback",
				"reason", err.Error(),
				"falling_back_to", "age-file",
			)
			// fall through to age-file
		default:
			return nil, fmt.Errorf("keystore: primary backend failed: %w", err)
		}
	}

	ks, err := newAgeFile(ctx, cfg, logger)
	if err != nil {
		return nil, fmt.Errorf("keystore: age-file fallback failed: %w", err)
	}
	logger.Info("ledger.key.backend", "backend", ks.Backend())
	return ks, nil
}

func chooseForced(ctx context.Context, cfg Config, logger *slog.Logger) (KeyStorage, error) {
	var (
		ks  KeyStorage
		err error
	)
	switch cfg.ForceBackend {
	case "keychain":
		ks, err = newKeychain(ctx, cfg, logger)
	case "secret-service":
		ks, err = newSecretService(ctx, cfg, logger)
	case "age-file":
		ks, err = newAgeFile(ctx, cfg, logger)
	default:
		return nil, fmt.Errorf("keystore: unknown ForceBackend %q (expected keychain|secret-service|age-file)", cfg.ForceBackend)
	}
	if err != nil {
		return nil, fmt.Errorf("keystore: forced backend %q: %w", cfg.ForceBackend, err)
	}
	logger.Info("ledger.key.backend", "backend", ks.Backend(), "forced", true)
	return ks, nil
}
