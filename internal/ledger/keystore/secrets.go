package keystore

import (
	"crypto/subtle"
	"runtime"
)

// wipe overwrites the slice's backing array with zero bytes. Callers MUST
// retain the slice long enough for the wipe to run before the last
// reference drops (runtime.KeepAlive below) — otherwise the Go runtime
// is free to reuse the underlying array before the zero-out runs.
//
// This is a best-effort protection. The garbage collector may already
// have made copies (string conversions, map keys, reflect walks), the
// kernel may have retained read buffers, and debug tooling (coredump,
// race detector) may preserve pages. We layer it anyway because the
// window of exposure shrinks meaningfully when the buffer is zeroed
// immediately after last use.
func wipe(b []byte) {
	if len(b) == 0 {
		return
	}
	for i := range b {
		b[i] = 0
	}
	runtime.KeepAlive(b)
}

// constantTimeEqual returns true iff a and b are byte-identical. Unlike
// bytes.Equal, the comparison takes the same number of operations
// regardless of where a mismatch occurs, so a remote attacker who can
// observe timing cannot deduce how many leading bytes matched.
//
// Used for passphrase confirmation (compare against a known value) and
// for any other secret-material comparison that might leak via timing.
// Length mismatch is treated as unequal without leaking the real length.
func constantTimeEqual(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}
