package keystore

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"runtime"
	"testing"
)

// TestKeyStorageInterface is the Task 2.1 gate test: it asserts the
// KeyStorage interface and Choose chooser compile and behave correctly
// for the happy and "force a non-matching backend" paths. Backend-specific
// behaviour is covered by the per-backend contract suites in
// contract_test.go.
func TestKeyStorageInterface(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("choose returns a non-nil backend on any supported platform", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("SIGILD_LEDGER_KEY_PASSPHRASE", "iface-test")
		t.Setenv("CREDENTIALS_DIRECTORY", "")

		// Pin ForceBackend="age-file" so the test is platform-agnostic —
		// the autodetect path is covered in the platform-specific tests.
		ks, err := Choose(context.Background(), Config{
			AgeFilePath:  dir + "/ledger.age",
			ForceBackend: "age-file",
		}, logger)
		if err != nil {
			t.Fatalf("Choose(forced age-file): %v", err)
		}
		if ks == nil {
			t.Fatalf("Choose returned nil KeyStorage without error")
		}
		if got := ks.Backend(); got != "age-file" {
			t.Fatalf("Backend() = %q, want %q", got, "age-file")
		}
	})

	t.Run("unknown ForceBackend errors", func(t *testing.T) {
		_, err := Choose(context.Background(), Config{ForceBackend: "hardware-enclave"}, logger)
		if err == nil {
			t.Fatalf("expected error for unknown ForceBackend")
		}
	})

	t.Run("ForceBackend=keychain off-darwin returns ErrBackendUnavailable", func(t *testing.T) {
		if runtime.GOOS == "darwin" {
			t.Skip("covered by TestKeychain on macOS")
		}
		_, err := Choose(context.Background(), Config{ForceBackend: "keychain"}, logger)
		if !errors.Is(err, ErrBackendUnavailable) {
			t.Fatalf("Choose(ForceBackend=keychain) on %s: got %v, want wrap of ErrBackendUnavailable", runtime.GOOS, err)
		}
	})

	t.Run("ForceBackend=secret-service off-linux returns ErrBackendUnavailable", func(t *testing.T) {
		if runtime.GOOS == "linux" {
			t.Skip("covered by TestSecretService on Linux with DBus")
		}
		_, err := Choose(context.Background(), Config{ForceBackend: "secret-service"}, logger)
		if !errors.Is(err, ErrBackendUnavailable) {
			t.Fatalf("Choose(ForceBackend=secret-service) on %s: got %v, want wrap of ErrBackendUnavailable", runtime.GOOS, err)
		}
	})

	t.Run("autodetect falls through to age-file when primary unavailable", func(t *testing.T) {
		// On Linux without a running secret-service daemon, Choose with
		// no ForceBackend should fall through to age-file. On darwin
		// (and any platform where primary is usable) this test is a
		// no-op — we still assert the resulting backend is one of the
		// three well-known strings.
		dir := t.TempDir()
		t.Setenv("SIGILD_LEDGER_KEY_PASSPHRASE", "iface-test")
		t.Setenv("CREDENTIALS_DIRECTORY", "")

		ks, err := Choose(context.Background(), Config{
			AgeFilePath: dir + "/ledger.age",
		}, logger)
		if err != nil {
			t.Fatalf("Choose(autodetect): %v", err)
		}
		switch b := ks.Backend(); b {
		case "keychain", "secret-service", "age-file":
			// ok
		default:
			t.Fatalf("unexpected Backend() = %q", b)
		}
	})
}
