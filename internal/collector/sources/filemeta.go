package sources

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/sigil-tech/sigil/internal/event"
)

// EnrichFileEvent adds language, is_test, is_config, and size_bytes
// to a file event payload. This is best-effort — errors are silently
// ignored since file metadata is supplementary.
func EnrichFileEvent(e *event.Event) {
	path, _ := e.Payload["path"].(string)
	if path == "" {
		return
	}

	ext := strings.ToLower(filepath.Ext(path))
	base := strings.ToLower(filepath.Base(path))

	e.Payload["extension"] = ext

	if lang, ok := extToLanguage[ext]; ok {
		e.Payload["language"] = lang
	} else if lang, ok := nameToLanguage[base]; ok {
		e.Payload["language"] = lang
	}

	e.Payload["is_test"] = isTestFile(base, ext)
	e.Payload["is_config"] = isConfigFile(base, ext)

	if info, err := os.Stat(path); err == nil {
		e.Payload["size_bytes"] = info.Size()
	}
}

// extToLanguage maps file extensions to language identifiers.
var extToLanguage = map[string]string{
	".go":     "go",
	".py":     "python",
	".pyw":    "python",
	".ts":     "typescript",
	".tsx":    "typescript",
	".js":     "javascript",
	".jsx":    "javascript",
	".mjs":    "javascript",
	".rs":     "rust",
	".java":   "java",
	".kt":     "kotlin",
	".kts":    "kotlin",
	".c":      "c",
	".h":      "c",
	".cpp":    "cpp",
	".cc":     "cpp",
	".cxx":    "cpp",
	".hpp":    "cpp",
	".cs":     "csharp",
	".rb":     "ruby",
	".swift":  "swift",
	".sql":    "sql",
	".sh":     "shell",
	".bash":   "shell",
	".zsh":    "shell",
	".fish":   "shell",
	".ps1":    "powershell",
	".html":   "html",
	".htm":    "html",
	".css":    "css",
	".scss":   "css",
	".less":   "css",
	".yaml":   "yaml",
	".yml":    "yaml",
	".toml":   "toml",
	".json":   "json",
	".xml":    "xml",
	".md":     "markdown",
	".mdx":    "markdown",
	".tex":    "latex",
	".r":      "r",
	".R":      "r",
	".lua":    "lua",
	".php":    "php",
	".scala":  "scala",
	".ex":     "elixir",
	".exs":    "elixir",
	".erl":    "erlang",
	".hs":     "haskell",
	".tf":     "terraform",
	".hcl":    "hcl",
	".proto":  "protobuf",
	".dart":   "dart",
	".vue":    "vue",
	".svelte": "svelte",
}

// nameToLanguage maps specific filenames to languages.
var nameToLanguage = map[string]string{
	"makefile":       "make",
	"dockerfile":     "docker",
	"justfile":       "just",
	"rakefile":       "ruby",
	"gemfile":        "ruby",
	"cmakelists.txt": "cmake",
}

// isTestFile returns true if the filename matches common test patterns.
func isTestFile(base, ext string) bool {
	switch {
	case strings.HasSuffix(base, "_test.go"):
		return true
	case strings.HasSuffix(base, ".test.ts"),
		strings.HasSuffix(base, ".test.tsx"),
		strings.HasSuffix(base, ".test.js"),
		strings.HasSuffix(base, ".test.jsx"):
		return true
	case strings.HasSuffix(base, ".spec.ts"),
		strings.HasSuffix(base, ".spec.tsx"),
		strings.HasSuffix(base, ".spec.js"),
		strings.HasSuffix(base, ".spec.jsx"):
		return true
	case strings.HasPrefix(base, "test_") && ext == ".py":
		return true
	case strings.HasSuffix(base, "_test.py"):
		return true
	case strings.HasSuffix(base, "_test.rs"):
		return true
	case strings.HasSuffix(base, "test.java"),
		strings.HasSuffix(base, "tests.java"):
		return true
	}
	return false
}

// isConfigFile returns true if the filename matches common config patterns.
func isConfigFile(base, ext string) bool {
	switch ext {
	case ".toml", ".yaml", ".yml", ".json", ".xml", ".ini", ".cfg",
		".env", ".tf", ".hcl", ".conf":
		return true
	}
	switch base {
	case "makefile", "dockerfile", "justfile", "rakefile",
		"gemfile", ".gitignore", ".dockerignore", ".editorconfig",
		".eslintrc", ".prettierrc", "tsconfig.json", "package.json",
		"go.mod", "go.sum", "cargo.toml", "pyproject.toml",
		"setup.py", "setup.cfg", "requirements.txt", "pipfile":
		return true
	}
	return false
}
