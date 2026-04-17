package merge

import (
	"github.com/sigil-tech/sigil/internal/filter"
)

// walkPayloadStrings delegates to the shared filter package.
func walkPayloadStrings(payload map[string]any, patterns []string) (string, bool) {
	return filter.WalkPayloadStrings(payload, patterns)
}

// stripProcessArgs delegates to the shared filter package.
func stripProcessArgs(payload map[string]any) map[string]any {
	return filter.StripProcessArgs(payload)
}

// isRFC1918 delegates to the shared filter package.
func isRFC1918(addr string) bool {
	return filter.IsRFC1918(addr)
}

// isInternalHostname delegates to the shared filter package.
func isInternalHostname(host string) bool {
	return filter.IsInternalHostname(host)
}

// payloadHash delegates to the shared filter package.
func payloadHash(data []byte) string {
	return filter.PayloadHashSHA256(data)
}
