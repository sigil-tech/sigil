package ledger

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/sigil-tech/sigil/internal/ledger/jcs"
)

// sampleInput returns a deterministic CanonicalInput suitable for use as
// the baseline in field-sensitivity tests. Every test mutates one field at
// a time and asserts the hash changes.
func sampleInput(t *testing.T) CanonicalInput {
	t.Helper()
	payload := map[string]any{
		"vm_id":   "abc-123",
		"profile": "sandbox-default",
		"apps":    []any{"cursor", "zsh"},
	}
	canon, err := jcs.CanonicalizeValue(payload)
	if err != nil {
		t.Fatalf("canonicalize baseline payload: %v", err)
	}
	return CanonicalInput{
		ID:               42,
		Timestamp:        "2026-04-19T12:34:56.789012345Z",
		Type:             "vm.spawn",
		Subject:          "vm-abc-123",
		CanonicalPayload: canon,
		PrevHash:         mustHex32(t, "0101010101010101010101010101010101010101010101010101010101010101"),
		SigningKeyFP:     mustHex16(t, "abcdef0123456789abcdef0123456789"),
	}
}

func mustHex32(t *testing.T, s string) [32]byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 32 {
		t.Fatalf("invalid 32-byte hex %q: %v", s, err)
	}
	var out [32]byte
	copy(out[:], b)
	return out
}

func mustHex16(t *testing.T, s string) [16]byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 16 {
		t.Fatalf("invalid 16-byte hex %q: %v", s, err)
	}
	var out [16]byte
	copy(out[:], b)
	return out
}

// TestHash_Deterministic asserts the hash function is a pure function of
// its input — repeated calls with the same CanonicalInput return the same
// bytes. Load-bearing for verify: a non-deterministic hash breaks every
// invariant the ledger rests on.
func TestHash_Deterministic(t *testing.T) {
	in := sampleInput(t)
	first := Hash(in)
	for i := range 100 {
		got := Hash(in)
		if got != first {
			t.Fatalf("hash not deterministic at iteration %d:\n  first: %x\n  got  : %x", i, first, got)
		}
	}
}

// TestHash_FieldSensitivity asserts that mutating any single field changes
// the hash. Covers the tamper scenarios enumerated in US4 — payload, ts,
// subject, type, prev_hash, signing_key_fp — so the Verifier can detect any
// one of them being rewritten.
func TestHash_FieldSensitivity(t *testing.T) {
	base := sampleInput(t)
	baseHash := Hash(base)

	cases := []struct {
		name   string
		mutate func(in *CanonicalInput)
	}{
		{"id", func(in *CanonicalInput) { in.ID = 43 }},
		{"timestamp", func(in *CanonicalInput) { in.Timestamp = "2026-04-19T12:34:56.789012346Z" }},
		{"type", func(in *CanonicalInput) { in.Type = "vm.teardown" }},
		{"subject", func(in *CanonicalInput) { in.Subject = "vm-xyz-999" }},
		{"payload", func(in *CanonicalInput) {
			alt, err := jcs.CanonicalizeValue(map[string]any{"vm_id": "different"})
			if err != nil {
				t.Fatalf("canonicalize alt payload: %v", err)
			}
			in.CanonicalPayload = alt
		}},
		{"prev_hash", func(in *CanonicalInput) {
			in.PrevHash[0] ^= 0x01
		}},
		{"signing_key_fp", func(in *CanonicalInput) {
			in.SigningKeyFP[0] ^= 0x01
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mutated := base
			// Copy payload so we don't alias the baseline slice.
			mutated.CanonicalPayload = append([]byte(nil), base.CanonicalPayload...)
			tc.mutate(&mutated)
			got := Hash(mutated)
			if got == baseHash {
				t.Fatalf("mutating %s produced the same hash:\n  base   : %x\n  mutated: %x", tc.name, baseHash, got)
			}
		})
	}
}

// TestHash_GenesisSentinelIsZero sanity-checks the FR-014 invariant: the
// genesis prev_hash is exactly 32 zero bytes.
func TestHash_GenesisSentinelIsZero(t *testing.T) {
	var zero [32]byte
	if GenesisPrevHash != zero {
		t.Fatalf("GenesisPrevHash must be 32 zero bytes, got %x", GenesisPrevHash)
	}
}

// TestHashPayload_CanonicalizesInput asserts HashPayload feeds the canonical
// form into Hash — two payload inputs that are semantically equal but
// differ in key order or whitespace MUST produce the same hash.
func TestHashPayload_CanonicalizesInput(t *testing.T) {
	var prev [32]byte
	var fp [16]byte
	copy(fp[:], bytes.Repeat([]byte{0xAA}, 16))

	raw1 := []byte(`{"a":1,"b":2,"c":3}`)
	raw2 := []byte(`  {"c":3,"a":1,"b":2}  `)

	h1, err := HashPayload(1, "2026-04-19T00:00:00Z", "vm.spawn", "x", raw1, prev, fp)
	if err != nil {
		t.Fatalf("hash raw1: %v", err)
	}
	h2, err := HashPayload(1, "2026-04-19T00:00:00Z", "vm.spawn", "x", raw2, prev, fp)
	if err != nil {
		t.Fatalf("hash raw2: %v", err)
	}
	if h1 != h2 {
		t.Fatalf("equivalent payloads produced different hashes:\n  raw1: %x\n  raw2: %x", h1, h2)
	}
}

// TestHashPayload_RejectsInvalidJSON verifies the wrapper surfaces JCS
// decode errors rather than silently hashing malformed input.
func TestHashPayload_RejectsInvalidJSON(t *testing.T) {
	var prev [32]byte
	var fp [16]byte
	_, err := HashPayload(1, "2026-04-19T00:00:00Z", "vm.spawn", "x", []byte(`{invalid}`), prev, fp)
	if err == nil {
		t.Fatalf("expected error for invalid JSON payload")
	}
}

// TestHash_GoldenFixtures locks the exact byte-level hash output for known
// inputs. A change to the hash-input format (field order, separator byte,
// endianness) will flip these fixtures and force a deliberate contract
// update in specs/029-kenaz-audit-ledger/contracts/ledger-hash.md.
func TestHash_GoldenFixtures(t *testing.T) {
	cases := []struct {
		name    string
		build   func(*testing.T) CanonicalInput
		wantHex string
	}{
		{
			name: "genesis_vm_spawn",
			build: func(t *testing.T) CanonicalInput {
				t.Helper()
				canon, err := jcs.CanonicalizeValue(map[string]any{
					"vm_id":      "vm-00000001",
					"profile_id": "sandbox-default",
				})
				if err != nil {
					t.Fatal(err)
				}
				return CanonicalInput{
					ID:               1,
					Timestamp:        "2026-04-19T00:00:00.000000000Z",
					Type:             "vm.spawn",
					Subject:          "vm-00000001",
					CanonicalPayload: canon,
					PrevHash:         GenesisPrevHash,
					SigningKeyFP:     mustHex16(t, "00112233445566778899aabbccddeeff"),
				}
			},
			// Locked on 2026-04-19 against the initial spec 029 Phase 1
			// format (plan §4.3). Any change to the hash-input layout flips
			// this fixture — update specs/029-kenaz-audit-ledger/contracts/
			// ledger-hash.md alongside the new value, and note that every
			// existing chain verifies only under the prior format.
			wantHex: "c3b02590f4c3f22c5f5cecdacb3da6d602a313ba2a813b93b41d31f3c616701b",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.build(t)
			got := Hash(in)
			gotHex := hex.EncodeToString(got[:])
			if gotHex != tc.wantHex {
				t.Fatalf("golden hash mismatch for %s:\n  got : %s\n  want: %s", tc.name, gotHex, tc.wantHex)
			}
		})
	}
}
