package event

import "strings"

// ExitCodeFromPayload extracts the "exit_code" field from a terminal event
// payload. It returns the exit code and true if the field was present and
// numeric, or (0, false) if the field is missing or has an unexpected type.
func ExitCodeFromPayload(payload map[string]any) (int, bool) {
	switch v := payload["exit_code"].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case int64:
		return int(v), true
	}
	return 0, false
}

// IsTestOrBuildCmd reports whether cmd looks like a test or build invocation.
// The list is intentionally conservative — false negatives are safer than
// false positives for pattern detection.
func IsTestOrBuildCmd(cmd string) bool {
	if cmd == "" {
		return false
	}
	prefixes := []string{
		"go test", "go build", "go vet",
		"make", "cargo test", "cargo build",
		"npm test", "npm run test", "npm run build",
		"pytest", "python -m pytest",
		"./gradlew", "mvn test", "mvn build",
	}
	lower := strings.ToLower(strings.TrimSpace(cmd))
	for _, p := range prefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

// CmdFromPayload extracts the "cmd" field from a terminal event payload.
func CmdFromPayload(payload map[string]any) string {
	cmd, _ := payload["cmd"].(string)
	return cmd
}
