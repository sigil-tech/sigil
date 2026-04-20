//go:build !darwin

package keystore

import (
	"context"
	"fmt"
	"log/slog"
)

// newKeychain on non-darwin platforms always returns ErrBackendUnavailable
// so the Choose function falls through to the next candidate. The stub
// exists only to let the darwin constructor share the package-level
// function name with the Linux and fallback backends.
func newKeychain(_ context.Context, _ Config, _ *slog.Logger) (KeyStorage, error) {
	return nil, fmt.Errorf("%w: keychain backend is macOS-only", ErrBackendUnavailable)
}
