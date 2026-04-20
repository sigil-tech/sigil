package ledger

import (
	"crypto/sha256"
	"encoding/binary"

	"github.com/sigil-tech/sigil/internal/ledger/jcs"
)

// unitSeparator is the ASCII Unit Separator byte (0x1F). It disambiguates
// adjacent variable-width fields in the canonical hash input. The separator
// value was chosen because a JCS-canonical JSON output cannot contain a raw
// 0x1F byte (such a byte would appear only as the `\u001f` escape inside a
// string literal), so the separator cannot collide with payload content.
const unitSeparator byte = 0x1F

// SigningKeyFPLen is the byte length of the signing-key fingerprint field
// as fed into the hash function. The hex-encoded 32-character fingerprint
// carried on the wire corresponds to these 16 raw bytes.
const SigningKeyFPLen = 16

// PrevHashLen is the byte length of the prev_hash and hash fields.
const PrevHashLen = 32

// GenesisPrevHash is the 32-byte sentinel used as prev_hash for the first
// entry in a ledger. Per spec 029 FR-014 this is exactly 32 zero bytes;
// there is no synthetic row 0 — the sentinel exists only as a logical
// marker in the first real entry.
var GenesisPrevHash = [PrevHashLen]byte{}

// CanonicalInput is the tuple of fields from a ledger row that contribute to
// its hash. Callers populate one of these at Emit time; the Emitter writes
// the resulting hash into the stored row's `hash` column. The Verifier
// re-constructs an identical CanonicalInput from the stored row at verify
// time and checks the recomputed hash matches.
//
// CanonicalPayload MUST already be the RFC 8785 canonical form of the
// emitter's payload (see package jcs). Callers that hand in raw JSON will
// get a hash that disagrees with the verifier.
type CanonicalInput struct {
	ID               int64
	Timestamp        string
	Type             string
	Subject          string
	CanonicalPayload []byte
	PrevHash         [PrevHashLen]byte
	SigningKeyFP     [SigningKeyFPLen]byte
}

// Hash computes SHA-256 over the deterministic binary concatenation of the
// CanonicalInput fields. The format is (per spec 029 plan §4.3):
//
//	hash = SHA-256(
//	    id (u64 BE)
//	 || ts || 0x1F
//	 || type || 0x1F
//	 || subject || 0x1F
//	 || canonical_payload || 0x1F
//	 || prev_hash (32 bytes)
//	 || signing_key_fp (16 bytes)
//	)
//
// Fixed-width fields (id, prev_hash, signing_key_fp) do not need the
// separator because their length is implicit. Variable-width fields are
// separated by 0x1F, which cannot appear in a JCS-canonical string as a
// raw byte (only as a `\u001f` escape) and cannot appear in ts / type /
// subject because those are constrained to RFC 3339, enum strings, and
// printable identifiers respectively.
func Hash(in CanonicalInput) [PrevHashLen]byte {
	h := sha256.New()

	var idBE [8]byte
	binary.BigEndian.PutUint64(idBE[:], uint64(in.ID))
	h.Write(idBE[:])

	h.Write([]byte(in.Timestamp))
	h.Write([]byte{unitSeparator})

	h.Write([]byte(in.Type))
	h.Write([]byte{unitSeparator})

	h.Write([]byte(in.Subject))
	h.Write([]byte{unitSeparator})

	h.Write(in.CanonicalPayload)
	h.Write([]byte{unitSeparator})

	h.Write(in.PrevHash[:])
	h.Write(in.SigningKeyFP[:])

	var out [PrevHashLen]byte
	copy(out[:], h.Sum(nil))
	return out
}

// HashPayload is a convenience wrapper that canonicalises raw JSON payload
// bytes via jcs.Canonicalize, then invokes Hash. Emitters that already have
// a canonical payload (e.g. built from a typed struct) should call Hash
// directly to avoid re-encoding cost.
func HashPayload(id int64, ts, typ, subject string, rawPayload []byte, prev [PrevHashLen]byte, fp [SigningKeyFPLen]byte) ([PrevHashLen]byte, error) {
	canon, err := jcs.Canonicalize(rawPayload)
	if err != nil {
		return [PrevHashLen]byte{}, err
	}
	return Hash(CanonicalInput{
		ID:               id,
		Timestamp:        ts,
		Type:             typ,
		Subject:          subject,
		CanonicalPayload: canon,
		PrevHash:         prev,
		SigningKeyFP:     fp,
	}), nil
}
