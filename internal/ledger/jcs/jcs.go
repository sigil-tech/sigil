// Package jcs implements JSON Canonicalization Scheme (RFC 8785) for the
// Sigil audit ledger.
//
// The scheme defines a deterministic byte-level representation of any JSON
// document so that a cryptographic hash of the canonical form is stable
// across implementations, formatters, and key orderings.
//
// Scope for the Sigil ledger. Payloads in spec 029 are struct-typed
// (spec 029 FR-032), restricted to identifiers, integer counts, RFC 3339
// timestamps, digests, and bounded enums. The implementation here covers
// the RFC 8785 rules needed for that domain:
//
//   - Object member serialisation in UTF-16-code-unit key order (§3.2.3).
//   - String escaping restricted to the seven short forms plus `\u00XX` for
//     control bytes (§3.2.2).
//   - Whitespace elimination (§3.2.4).
//   - Array order preservation (§3.2.2).
//   - Integer numbers emitted as decimal strings with no leading zeros.
//
// The RFC's ECMAScript Number.prototype.toString algorithm for non-integer
// floats is approximated with Go's strconv.FormatFloat. This approximation
// is safe for ledger payloads (which do not carry arbitrary floats) but
// would be incorrect for a general-purpose JCS library — such a caller
// should reach for a fully RFC-conformant implementation.
package jcs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"unicode/utf16"
)

// Canonicalize decodes the JSON input with UseNumber() so integer vs float
// semantics are preserved, then re-emits the value in RFC 8785 form.
// Trailing content after the first JSON value is rejected.
func Canonicalize(input []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.UseNumber()

	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("jcs: decode: %w", err)
	}
	// Reject trailing content (RFC 8785 operates on a single JSON value).
	var tail any
	if err := dec.Decode(&tail); err == nil {
		return nil, fmt.Errorf("jcs: unexpected trailing content after JSON value")
	}

	return CanonicalizeValue(v)
}

// CanonicalizeValue is the in-memory entry point. The value MUST be a type
// the Go stdlib produces from json.Decoder with UseNumber(): nil, bool,
// json.Number, string, []any, or map[string]any. Other types are a
// programmer error and surface as a typed error.
func CanonicalizeValue(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := encode(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encode(w *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case nil:
		w.WriteString("null")
	case bool:
		if t {
			w.WriteString("true")
		} else {
			w.WriteString("false")
		}
	case json.Number:
		return encodeNumber(w, t)
	case float64:
		return encodeFloat(w, t)
	case string:
		encodeString(w, t)
	case []any:
		return encodeArray(w, t)
	case map[string]any:
		return encodeObject(w, t)
	default:
		return fmt.Errorf("jcs: unsupported type %T (expected nil/bool/json.Number/float64/string/[]any/map[string]any)", v)
	}
	return nil
}

// encodeNumber emits an integer as its decimal string when it fits in int64,
// and falls back to encodeFloat otherwise. RFC 8785 §3.2.2.
func encodeNumber(w *bytes.Buffer, n json.Number) error {
	if i, err := n.Int64(); err == nil {
		w.WriteString(strconv.FormatInt(i, 10))
		return nil
	}
	f, err := n.Float64()
	if err != nil {
		return fmt.Errorf("jcs: parse number %q: %w", n.String(), err)
	}
	return encodeFloat(w, f)
}

func encodeFloat(w *bytes.Buffer, f float64) error {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return fmt.Errorf("jcs: NaN and Infinity are not representable in JSON")
	}
	if f == 0 {
		// +0 and -0 both serialise as "0" per ECMAScript Number.toString.
		w.WriteString("0")
		return nil
	}
	// Integer fast path for values that fit cleanly. Everything wider than
	// ±2^53 is already handled by the Int64 branch of encodeNumber; only
	// non-integer floats reach this point.
	if f == math.Trunc(f) && math.Abs(f) < 1e21 {
		w.WriteString(strconv.FormatFloat(f, 'f', -1, 64))
		return nil
	}
	w.WriteString(strconv.FormatFloat(f, 'g', -1, 64))
	return nil
}

// encodeString emits a JSON string literal using only the escape forms
// listed in RFC 8785 §3.2.2 / RFC 8259 §7: the seven named short escapes
// (\" \\ \b \f \n \r \t) plus `\u00XX` for remaining control bytes.
// Non-ASCII characters are written as their UTF-8 bytes.
func encodeString(w *bytes.Buffer, s string) {
	w.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			w.WriteString(`\"`)
		case '\\':
			w.WriteString(`\\`)
		case '\b':
			w.WriteString(`\b`)
		case '\f':
			w.WriteString(`\f`)
		case '\n':
			w.WriteString(`\n`)
		case '\r':
			w.WriteString(`\r`)
		case '\t':
			w.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(w, `\u%04x`, r)
				continue
			}
			w.WriteRune(r)
		}
	}
	w.WriteByte('"')
}

func encodeArray(w *bytes.Buffer, a []any) error {
	w.WriteByte('[')
	for i, v := range a {
		if i > 0 {
			w.WriteByte(',')
		}
		if err := encode(w, v); err != nil {
			return err
		}
	}
	w.WriteByte(']')
	return nil
}

func encodeObject(w *bytes.Buffer, m map[string]any) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return lessUTF16(keys[i], keys[j])
	})
	w.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			w.WriteByte(',')
		}
		encodeString(w, k)
		w.WriteByte(':')
		if err := encode(w, m[k]); err != nil {
			return err
		}
	}
	w.WriteByte('}')
	return nil
}

// lessUTF16 compares two strings by their UTF-16 code-unit sequences, which
// is the ordering RFC 8785 §3.2.3 specifies for object member keys. For
// purely BMP strings (the overwhelming majority of ledger payload keys)
// this matches code-point ordering; above the BMP the surrogate-pair
// encoding of UTF-16 orders differently from UTF-8 code-point order, so
// byte-wise comparison would disagree.
func lessUTF16(a, b string) bool {
	ua := utf16.Encode([]rune(a))
	ub := utf16.Encode([]rune(b))
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			return ua[i] < ub[i]
		}
	}
	return len(ua) < len(ub)
}
