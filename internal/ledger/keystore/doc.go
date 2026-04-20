// Package keystore defines the KeyStorage interface and the three
// platform-specific backends the Sigil audit ledger uses to persist its
// ed25519 signing key on disk. Spec 029 §3 is the source of truth; this
// doc summarises the runtime-visible contract.
//
// # Backends
//
// Autodetection in Choose picks the primary backend per platform and
// falls through to age-file on ErrBackendUnavailable:
//
//   - darwin : keychain        → age-file
//   - linux  : secret-service  → age-file
//   - other  : age-file
//
// The chosen backend is logged at startup under the "ledger.key.backend"
// key so operators can audit the selection without having to grep
// implementation files.
//
// # Contract
//
// Every backend MUST satisfy the behavioural contract tested in
// contract_test.go — in particular: Load returns ErrKeyNotFound on first
// boot, Store replaces any existing key atomically, and concurrent Store
// calls serialise under the backend's own mutex. Private key bytes never
// leave the process except as the backend's ciphertext payload.
//
// # Security posture (constitution §I, spec 029 §3)
//
// Plaintext-on-disk is not permitted. The age-file fallback encrypts
// with a scrypt-KDF recipient using a passphrase sourced from
// $CREDENTIALS_DIRECTORY (preferred) or $SIGILD_LEDGER_KEY_PASSPHRASE.
// Missing passphrase → ErrPassphraseUnavailable → the daemon refuses to
// start. The macOS Keychain backend narrows the ACL to the current
// sigild binary via `security add-generic-password -T`. The Linux
// Secret Service backend stores the key in the user's default collection;
// the DBus session bus is scoped to the caller's UID.
package keystore
