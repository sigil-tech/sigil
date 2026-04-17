// Package filter provides shared denylist filtering for the merge and corpus
// pipelines. It is extracted from internal/merge to allow both VM merge and
// host corpus ingestion to apply the same privacy filters.
//
// DAG position: imports config only. Must not import store, inference,
// analyzer, notifier, actuator, socket, or collector.
package filter

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// DefaultDenylist is the baseline set of glob patterns applied when the
// operator has not configured a custom denylist. Patterns are matched
// case-insensitively against every string value found anywhere in a row's
// payload via a recursive walk.
var DefaultDenylist = []string{
	"*.pem", "*.key", "*.env", "id_rsa",
	"*secret*", "*password*", "*token*",
	"*.p12", "*.pfx", "*credential*", "*bearer*",
	"*.gpg", "*.asc",
}

// MatchesDenylist reports whether value matches any pattern in patterns.
// Matching is case-insensitive; filepath.Match is used for glob expansion.
// For path-like values (containing a "/"), the basename is tested in addition
// to the full value so that "*.pem" catches "/etc/tls/server.pem".
// The matched pattern is returned alongside the boolean so callers can log it.
func MatchesDenylist(value string, patterns []string) (string, bool) {
	lower := strings.ToLower(value)
	base := lower
	if idx := strings.LastIndex(lower, "/"); idx >= 0 {
		base = lower[idx+1:]
	}

	for _, p := range patterns {
		lp := strings.ToLower(p)
		if matched, err := filepath.Match(lp, lower); err == nil && matched {
			return p, true
		}
		if base != lower {
			if matched, err := filepath.Match(lp, base); err == nil && matched {
				return p, true
			}
		}
	}
	return "", false
}

// WalkPayloadStrings recurses into a decoded JSON payload and tests every
// map key and string value against patterns. The first match causes an early
// return with the matched pattern name and true.
func WalkPayloadStrings(payload map[string]any, patterns []string) (string, bool) {
	for k, v := range payload {
		if p, hit := MatchesDenylist(k, patterns); hit {
			return p, true
		}
		switch val := v.(type) {
		case string:
			if p, hit := MatchesDenylist(val, patterns); hit {
				return p, true
			}
		case map[string]any:
			if p, hit := WalkPayloadStrings(val, patterns); hit {
				return p, true
			}
		case []any:
			if p, hit := walkSlice(val, patterns); hit {
				return p, true
			}
		}
	}
	return "", false
}

// walkSlice recurses into a JSON array looking for denylist hits.
func walkSlice(items []any, patterns []string) (string, bool) {
	for _, item := range items {
		switch val := item.(type) {
		case string:
			if p, hit := MatchesDenylist(val, patterns); hit {
				return p, true
			}
		case map[string]any:
			if p, hit := WalkPayloadStrings(val, patterns); hit {
				return p, true
			}
		case []any:
			if p, hit := walkSlice(val, patterns); hit {
				return p, true
			}
		}
	}
	return "", false
}

// StripProcessArgs removes the "args" and "cmdline" fields from a process
// event payload. The original map is not mutated; a shallow copy is returned.
func StripProcessArgs(payload map[string]any) map[string]any {
	out := make(map[string]any, len(payload))
	for k, v := range payload {
		if k == "args" || k == "cmdline" {
			continue
		}
		out[k] = v
	}
	return out
}

// privateRanges is the set of RFC 1918 / RFC 4193 / loopback CIDR blocks.
var privateRanges []*net.IPNet

func init() {
	cidrs := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"::1/128",
		"fc00::/7",
		"169.254.0.0/16",
	}
	for _, cidr := range cidrs {
		_, block, err := net.ParseCIDR(cidr)
		if err != nil {
			panic("filter: bad private CIDR " + cidr)
		}
		privateRanges = append(privateRanges, block)
	}
}

// IsRFC1918 reports whether addr (host or host:port) belongs to a private or
// loopback address range.
func IsRFC1918(addr string) bool {
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, block := range privateRanges {
		if block.Contains(ip) {
			return true
		}
	}
	return false
}

// internalSuffixes are TLD/suffix patterns that indicate an internal hostname.
var internalSuffixes = []string{
	".internal", ".corp", ".local", ".lan", ".intranet", ".home",
}

// IsInternalHostname reports whether host looks like an internal / non-public
// hostname. Single-label names (no dot) are always considered internal.
func IsInternalHostname(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if !strings.Contains(host, ".") {
		return true
	}
	lower := strings.ToLower(host)
	for _, sfx := range internalSuffixes {
		if strings.HasSuffix(lower, sfx) {
			return true
		}
	}
	return false
}

// PayloadHashSHA256 returns the hex-encoded SHA-256 of data.
func PayloadHashSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// PayloadHashHMAC returns the hex-encoded HMAC-SHA256 of data using the given key.
func PayloadHashHMAC(data, key []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}

// LoadOrCreateHMACKey loads the HMAC key from path, or generates a new 32-byte
// key and writes it to path with 0600 permissions if the file does not exist.
func LoadOrCreateHMACKey(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err == nil && len(data) == 32 {
		return data, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("filter: read hmac key %s: %w", path, err)
	}

	// Generate a new key.
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("filter: generate hmac key: %w", err)
	}

	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("filter: create hmac key dir: %w", err)
	}

	if err := os.WriteFile(path, key, 0600); err != nil {
		return nil, fmt.Errorf("filter: write hmac key %s: %w", path, err)
	}
	return key, nil
}
