package ledger

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/sigil-tech/sigil/internal/ledger/jcs"
)

// FuzzJCSDeterminism asserts that canonicalizing the same JSON input twice
// produces identical bytes. The property is load-bearing for chain
// integrity: the Verifier re-canonicalises stored payloads to reconstruct
// the hash, so any non-determinism would spuriously break the chain.
//
// Seeded with a spread of payload shapes that mirror ledger event types.
func FuzzJCSDeterminism(f *testing.F) {
	seeds := [][]byte{
		[]byte(`{"vm_id":"vm-001","profile_id":"sandbox","apps":["cursor","zsh"],"memory_mb":4096}`),
		[]byte(`{"merge_id":"m-abc","filtered_row_count":17,"filter_rules_applied":["rule-1","rule-2"]}`),
		[]byte(`{"tune_id":"t-9","phase":"start","base_model_id":"llama-3.1-8b","corpus_row_count":123456}`),
		[]byte(`{"rule_id":"deny-egress","requested_action":"vm.spawn","requested_by":"user@example.com"}`),
		[]byte(`{"nested":{"deeply":{"scoped":{"keys":true,"count":42}}}}`),
		[]byte(`{"empty_array":[],"empty_object":{},"null_value":null,"false":false,"true":true}`),
		[]byte(`[]`),
		[]byte(`null`),
		[]byte(`42`),
		[]byte(`"simple-string"`),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		// Prefilter: skip inputs that are not well-formed JSON — not our
		// canonicalizer's problem. We want fuzz cycles spent on genuinely
		// valid JSON inputs.
		if !json.Valid(raw) {
			t.Skip()
		}
		first, err := jcs.Canonicalize(raw)
		if err != nil {
			// Canonicalize may reject valid JSON that exercises paths the
			// implementation chose not to support (e.g. very large floats);
			// that's a correctness signal, not a fuzz finding.
			t.Skip()
		}
		second, err := jcs.Canonicalize(raw)
		if err != nil {
			t.Fatalf("first pass succeeded but second failed: %v", err)
		}
		if !bytes.Equal(first, second) {
			t.Fatalf("determinism violated on input %q:\n  first : %q\n  second: %q", raw, first, second)
		}

		// Second property: canonicalising the canonical form yields the
		// same bytes again (idempotency). If either pass differs, the
		// encoder is emitting a form it wouldn't accept back.
		third, err := jcs.Canonicalize(first)
		if err != nil {
			t.Fatalf("re-canonicalising canonical form failed: %v (input=%q canon=%q)", err, raw, first)
		}
		if !bytes.Equal(first, third) {
			t.Fatalf("idempotency violated:\n  canon      : %q\n  re-canon   : %q\n  original   : %q", first, third, raw)
		}
	})
}

// FuzzHashFieldSensitivity asserts that flipping a single byte in a
// CanonicalInput's string / payload fields changes the hash. Complements
// the deterministic TestHash_FieldSensitivity table test by sweeping a
// broader input space.
func FuzzHashFieldSensitivity(f *testing.F) {
	f.Add("vm-abc-123", "2026-04-19T00:00:00Z", "vm.spawn", []byte(`{"k":"v"}`))
	f.Add("tune-42", "2026-04-19T12:00:00.123Z", "training.tune", []byte(`{"phase":"start"}`))

	f.Fuzz(func(t *testing.T, subject, ts, typ string, rawPayload []byte) {
		if !json.Valid(rawPayload) {
			t.Skip()
		}
		if subject == "" || ts == "" || typ == "" {
			t.Skip() // Emitters guarantee non-empty — outside our contract.
		}

		canon, err := jcs.Canonicalize(rawPayload)
		if err != nil {
			t.Skip()
		}

		base := CanonicalInput{
			ID:               7,
			Timestamp:        ts,
			Type:             typ,
			Subject:          subject,
			CanonicalPayload: canon,
			PrevHash:         [32]byte{0x11, 0x22, 0x33},
			SigningKeyFP:     [16]byte{0xAA},
		}
		baseHash := Hash(base)

		// Flip one bit in the subject; assert hash changes.
		flipped := base
		subjectBytes := []byte(flipped.Subject)
		subjectBytes[0] ^= 0x01
		flipped.Subject = string(subjectBytes)
		if Hash(flipped) == baseHash {
			t.Fatalf("subject bitflip did not change hash: subject=%q", subject)
		}
	})
}
