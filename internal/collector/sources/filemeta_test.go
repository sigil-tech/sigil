package sources

import (
	"testing"

	"github.com/sigil-tech/sigil/internal/event"
)

func TestEnrichFileEvent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		path     string
		wantLang string
		wantTest bool
		wantCfg  bool
	}{
		{"go_source", "/src/main.go", "go", false, false},
		{"go_test", "/src/store_test.go", "go", true, false},
		{"typescript", "/app/index.ts", "typescript", false, false},
		{"tsx_test", "/app/App.test.tsx", "typescript", true, false},
		{"jest_spec", "/app/utils.spec.js", "javascript", true, false},
		{"python_test_prefix", "/tests/test_model.py", "python", true, false},
		{"python_test_suffix", "/tests/model_test.py", "python", true, false},
		{"rust_test", "/src/lib_test.rs", "rust", true, false},
		{"config_toml", "/config.toml", "toml", false, true},
		{"config_yaml", "/.github/workflows/ci.yml", "yaml", false, true},
		{"dockerfile", "/Dockerfile", "docker", false, true},
		{"makefile", "/Makefile", "make", false, true},
		{"go_mod", "/go.mod", "", false, true},
		{"package_json", "/package.json", "json", false, true},
		{"terraform", "/main.tf", "terraform", false, true},
		{"markdown", "/README.md", "markdown", false, false},
		{"html", "/index.html", "html", false, false},
		{"css", "/style.css", "css", false, false},
		{"shell", "/setup.sh", "shell", false, false},
		{"sql", "/schema.sql", "sql", false, false},
		{"unknown", "/data.bin", "", false, false},
		{"no_extension", "/LICENSE", "", false, false},
		{"kotlin", "/Main.kt", "kotlin", false, false},
		{"swift", "/App.swift", "swift", false, false},
		{"proto", "/api.proto", "protobuf", false, false},
		{"env_file", "/.env", "", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := &event.Event{
				Payload: map[string]any{
					"path": tt.path,
					"op":   "WRITE",
				},
			}
			EnrichFileEvent(e)

			lang, _ := e.Payload["language"].(string)
			if lang != tt.wantLang {
				t.Errorf("language = %q, want %q", lang, tt.wantLang)
			}
			isTest, _ := e.Payload["is_test"].(bool)
			if isTest != tt.wantTest {
				t.Errorf("is_test = %v, want %v", isTest, tt.wantTest)
			}
			isCfg, _ := e.Payload["is_config"].(bool)
			if isCfg != tt.wantCfg {
				t.Errorf("is_config = %v, want %v", isCfg, tt.wantCfg)
			}
		})
	}
}

func TestIsTestFile(t *testing.T) {
	t.Parallel()
	if !isTestFile("store_test.go", ".go") {
		t.Error("expected store_test.go to be test")
	}
	if isTestFile("store.go", ".go") {
		t.Error("expected store.go to not be test")
	}
}

func TestIsConfigFile(t *testing.T) {
	t.Parallel()
	if !isConfigFile("config.toml", ".toml") {
		t.Error("expected config.toml to be config")
	}
	if isConfigFile("main.go", ".go") {
		t.Error("expected main.go to not be config")
	}
}
