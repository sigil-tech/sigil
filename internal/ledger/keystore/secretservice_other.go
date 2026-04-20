//go:build !linux

package keystore

import (
	"context"
	"fmt"
	"log/slog"
)

// newSecretService on non-linux platforms always returns
// ErrBackendUnavailable so the chooser falls through. The real
// implementation lives in secretservice_linux.go — the freedesktop
// Secret Service spec is Linux-centric and wrapping godbus on other
// platforms has no practical destination to connect to.
func newSecretService(_ context.Context, _ Config, _ *slog.Logger) (KeyStorage, error) {
	return nil, fmt.Errorf("%w: secret-service backend is Linux-only", ErrBackendUnavailable)
}
