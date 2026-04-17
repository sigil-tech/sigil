package filter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMatchesDenylist(t *testing.T) {
	patterns := DefaultDenylist

	tests := []struct {
		value   string
		matched bool
		pattern string
	}{
		{"/etc/tls/server.pem", true, "*.pem"},
		{"/home/user/.ssh/id_rsa", true, "id_rsa"},
		{"my_secret_file", true, "*secret*"},
		{"bearer_token", true, "*token*"}, // matches *token* first in denylist order
		{"normal_code.go", false, ""},
		{"README.md", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			pattern, matched := MatchesDenylist(tt.value, patterns)
			assert.Equal(t, tt.matched, matched)
			if tt.matched {
				assert.Equal(t, tt.pattern, pattern)
			}
		})
	}
}

func TestWalkPayloadStrings(t *testing.T) {
	payload := map[string]any{
		"path":   "/etc/tls/server.pem",
		"action": "read",
	}
	pattern, hit := WalkPayloadStrings(payload, DefaultDenylist)
	assert.True(t, hit)
	assert.Equal(t, "*.pem", pattern)
}

func TestWalkPayloadStringsNested(t *testing.T) {
	payload := map[string]any{
		"metadata": map[string]any{
			"nested": map[string]any{
				"secret_key": "abc123",
			},
		},
	}
	pattern, hit := WalkPayloadStrings(payload, DefaultDenylist)
	assert.True(t, hit)
	assert.Equal(t, "*secret*", pattern)
}

func TestWalkPayloadStringsClean(t *testing.T) {
	payload := map[string]any{
		"path":   "/home/user/code/main.go",
		"action": "edit",
	}
	_, hit := WalkPayloadStrings(payload, DefaultDenylist)
	assert.False(t, hit)
}

func TestStripProcessArgs(t *testing.T) {
	payload := map[string]any{
		"name": "go",
		"pid":  1234,
		"args": []string{"test", "./..."},
	}
	stripped := StripProcessArgs(payload)
	assert.Contains(t, stripped, "name")
	assert.Contains(t, stripped, "pid")
	assert.NotContains(t, stripped, "args")
}

func TestIsRFC1918(t *testing.T) {
	assert.True(t, IsRFC1918("192.168.1.1"))
	assert.True(t, IsRFC1918("10.0.0.1:8080"))
	assert.True(t, IsRFC1918("172.16.0.1"))
	assert.False(t, IsRFC1918("8.8.8.8"))
	assert.False(t, IsRFC1918("1.2.3.4:443"))
}

func TestIsInternalHostname(t *testing.T) {
	assert.True(t, IsInternalHostname("service.internal"))
	assert.True(t, IsInternalHostname("db.corp"))
	assert.True(t, IsInternalHostname("myhost")) // single-label
	assert.False(t, IsInternalHostname("google.com"))
}

func TestPayloadHashHMAC(t *testing.T) {
	key := []byte("testkey1234567890testkey12345678")
	data := []byte("hello world")
	hash := PayloadHashHMAC(data, key)
	assert.Len(t, hash, 64) // hex-encoded SHA-256

	// Same input produces same hash.
	assert.Equal(t, hash, PayloadHashHMAC(data, key))

	// Different key produces different hash.
	key2 := []byte("different_key___different_key___")
	assert.NotEqual(t, hash, PayloadHashHMAC(data, key2))
}

func TestLoadOrCreateHMACKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hmac.key")

	// First call creates the key.
	key1, err := LoadOrCreateHMACKey(path)
	require.NoError(t, err)
	assert.Len(t, key1, 32)

	// File should exist.
	_, err = os.Stat(path)
	require.NoError(t, err)

	// Second call loads the same key.
	key2, err := LoadOrCreateHMACKey(path)
	require.NoError(t, err)
	assert.Equal(t, key1, key2)
}
