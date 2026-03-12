package event

import "testing"

func TestIsTestOrBuildCmd(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"go test ./...", true},
		{"go build .", true},
		{"go vet ./...", true},
		{"make all", true},
		{"cargo test", true},
		{"cargo build --release", true},
		{"npm test", true},
		{"npm run test", true},
		{"npm run build", true},
		{"pytest -v", true},
		{"python -m pytest tests/", true},
		{"./gradlew test", true},
		{"mvn test", true},
		{"mvn build", true},
		{"GO TEST ./...", true},
		{"  go test ./...", true},
		{"git commit -m 'fix'", false},
		{"ls -la", false},
		{"echo hello", false},
		{"go run main.go", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			if got := IsTestOrBuildCmd(tt.cmd); got != tt.want {
				t.Errorf("IsTestOrBuildCmd(%q) = %v; want %v", tt.cmd, got, tt.want)
			}
		})
	}
}

func TestExitCodeFromPayload(t *testing.T) {
	tests := []struct {
		name    string
		payload map[string]any
		want    int
		wantOK  bool
	}{
		{"float64", map[string]any{"exit_code": float64(0)}, 0, true},
		{"float64 non-zero", map[string]any{"exit_code": float64(1)}, 1, true},
		{"int", map[string]any{"exit_code": int(2)}, 2, true},
		{"int64", map[string]any{"exit_code": int64(127)}, 127, true},
		{"missing", map[string]any{}, 0, false},
		{"wrong type", map[string]any{"exit_code": "zero"}, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ExitCodeFromPayload(tt.payload)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("ExitCodeFromPayload(%v) = (%d, %v); want (%d, %v)",
					tt.payload, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestCmdFromPayload(t *testing.T) {
	tests := []struct {
		name    string
		payload map[string]any
		want    string
	}{
		{"present", map[string]any{"cmd": "go test ./..."}, "go test ./..."},
		{"missing", map[string]any{}, ""},
		{"wrong type", map[string]any{"cmd": 42}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CmdFromPayload(tt.payload); got != tt.want {
				t.Errorf("CmdFromPayload(%v) = %q; want %q", tt.payload, got, tt.want)
			}
		})
	}
}
