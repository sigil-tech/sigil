package jcs

import (
	"bytes"
	"strings"
	"testing"
)

// TestJCS covers the common RFC 8785 rules the Sigil ledger needs.
// Each subtest is independent; failures report the exact rule that broke.
func TestJCS(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		want  string
		error bool
	}{
		{"null", `null`, `null`, false},
		{"true", `true`, `true`, false},
		{"false", `false`, `false`, false},
		{"empty_string", `""`, `""`, false},
		{"ascii_string", `"hello"`, `"hello"`, false},
		{"string_with_quote", `"a\"b"`, `"a\"b"`, false},
		{"string_with_backslash", `"a\\b"`, `"a\\b"`, false},
		{"string_with_tab", "\"a\\tb\"", `"a\tb"`, false},
		{"string_with_control_0x01", "\"a\\u0001b\"", `"a\u0001b"`, false},
		{"string_unicode_non_ascii", `"café"`, `"café"`, false},
		{"positive_integer", `42`, `42`, false},
		{"negative_integer", `-7`, `-7`, false},
		{"zero", `0`, `0`, false},
		{"neg_zero", `-0`, `0`, false},
		{"big_integer", `9223372036854775807`, `9223372036854775807`, false},
		{"float_simple", `1.5`, `1.5`, false},
		{"empty_array", `[]`, `[]`, false},
		{"array_preserves_order", `[3,1,2]`, `[3,1,2]`, false},
		{"nested_array", `[[1,2],[3,4]]`, `[[1,2],[3,4]]`, false},
		{"empty_object", `{}`, `{}`, false},
		{"object_sorts_keys", `{"b":1,"a":2}`, `{"a":2,"b":1}`, false},
		{"object_nested_sorts", `{"b":{"y":1,"x":2},"a":0}`, `{"a":0,"b":{"x":2,"y":1}}`, false},
		{"object_mixed_types", `{"s":"x","n":42,"b":true,"z":null,"a":[1,2]}`, `{"a":[1,2],"b":true,"n":42,"s":"x","z":null}`, false},
		{"object_numeric_looking_keys", `{"10":1,"2":2,"1":3}`, `{"1":3,"10":1,"2":2}`, false},
		{"whitespace_stripped", "  {\n  \"a\" : 1 ,\n  \"b\" : [ 1 , 2 ]\n}  ", `{"a":1,"b":[1,2]}`, false},

		// Errors
		{"trailing_content", `{"a":1}{"b":2}`, "", true},
		{"invalid_json", `{"a":}`, "", true},
		{"NaN_not_permitted", `NaN`, "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Canonicalize([]byte(tc.in))
			if tc.error {
				if err == nil {
					t.Fatalf("expected error, got nil (output=%q)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !bytes.Equal(got, []byte(tc.want)) {
				t.Fatalf("canonical form mismatch\n got : %q\n want: %q", got, tc.want)
			}
		})
	}
}

// TestJCS_Idempotent verifies that canonicalizing an already-canonical input
// yields the same bytes — a load-bearing property for the ledger's chain
// (re-canonicalising at verify time must produce the same hash input).
func TestJCS_Idempotent(t *testing.T) {
	inputs := []string{
		`null`,
		`{"a":1,"b":2,"c":[1,2,3]}`,
		`[{"z":1,"a":2},{"m":null,"b":true}]`,
		`{"s":"a\"b\\c","n":-42}`,
	}
	for _, in := range inputs {
		t.Run(in, func(t *testing.T) {
			first, err := Canonicalize([]byte(in))
			if err != nil {
				t.Fatalf("first pass: %v", err)
			}
			second, err := Canonicalize(first)
			if err != nil {
				t.Fatalf("second pass: %v", err)
			}
			if !bytes.Equal(first, second) {
				t.Fatalf("idempotency violated\n 1st: %q\n 2nd: %q", first, second)
			}
		})
	}
}

// TestJCS_SemanticEqualInputsProduceEqualOutputs is the property the ledger
// relies on: two JSON documents that differ only in key order and
// whitespace MUST produce identical canonical forms and therefore identical
// hashes.
func TestJCS_SemanticEqualInputsProduceEqualOutputs(t *testing.T) {
	cases := [][]string{
		{
			`{"a":1,"b":2}`,
			`{"b":2,"a":1}`,
			" { \"a\" : 1 , \"b\" : 2 } ",
			"{\n  \"b\": 2,\n  \"a\": 1\n}",
		},
		{
			`[{"k":"v","n":0},{"n":1,"k":"w"}]`,
			`[{"n":0,"k":"v"},{"k":"w","n":1}]`,
		},
	}
	for i, group := range cases {
		t.Run("group_"+string(rune('A'+i)), func(t *testing.T) {
			first, err := Canonicalize([]byte(group[0]))
			if err != nil {
				t.Fatalf("%q: %v", group[0], err)
			}
			for _, alt := range group[1:] {
				got, err := Canonicalize([]byte(alt))
				if err != nil {
					t.Fatalf("%q: %v", alt, err)
				}
				if !bytes.Equal(got, first) {
					t.Fatalf("semantic-equal mismatch\n  canonical  : %q\n  alternative: %q\n  produced   : %q", first, alt, got)
				}
			}
		})
	}
}

// TestJCS_UTF16OrdersKeysAboveBMP confirms the §3.2.3 ordering rule. The
// character U+10000 (LINEAR B SYLLABLE B008 A) has UTF-16 encoding
// (0xD800, 0xDC00). Under byte-wise UTF-8 order it sorts after U+FFFD
// (replacement character, 0xEF 0xBF 0xBD) because its UTF-8 leading byte
// is 0xF0; under UTF-16 code-unit order it also sorts after U+FFFD
// (0xD800 < 0xFFFD is FALSE — 0xD800 surrogate first code unit is less
// than 0xFFFD but comes from a different plane). Use a clearer test:
// a BMP key vs an above-BMP key.
func TestJCS_UTF16OrdersKeysAboveBMP(t *testing.T) {
	// U+FFFD (replacement) vs U+10000 (linear B).
	// UTF-16 code units: 0xFFFD vs (0xD800, 0xDC00).
	// Expected order under UTF-16: 0xD800 < 0xFFFD, so U+10000 sorts first.
	input := `{"\uFFFD":"bmp","\uD800\uDC00":"supra"}`
	got, err := Canonicalize([]byte(input))
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	// Look for the supra key preceding the BMP key.
	supraIdx := strings.Index(string(got), `"supra"`)
	bmpIdx := strings.Index(string(got), `"bmp"`)
	if supraIdx < 0 || bmpIdx < 0 {
		t.Fatalf("both keys must appear in output %q", got)
	}
	if supraIdx >= bmpIdx {
		t.Fatalf("UTF-16 ordering violated: supra should precede bmp (got %q)", got)
	}
}
