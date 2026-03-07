package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name        string
		toml        string // empty string means: no file
		wantErr     bool
		check       func(t *testing.T, cfg *Config)
	}{
		{
			name: "missing file returns defaults",
			toml: "", // no file written
			check: func(t *testing.T, cfg *Config) {
				if cfg.Daemon.LogLevel != "info" {
					t.Errorf("LogLevel = %q, want %q", cfg.Daemon.LogLevel, "info")
				}
				if cfg.Notifier.Level != 2 {
					t.Errorf("Notifier.Level = %d, want 2", cfg.Notifier.Level)
				}
				if cfg.Cactus.URL != "http://127.0.0.1:8080" {
					t.Errorf("Cactus.URL = %q, want default", cfg.Cactus.URL)
				}
				if cfg.Retention.RawEventDays != 90 {
					t.Errorf("RawEventDays = %d, want 90", cfg.Retention.RawEventDays)
				}
			},
		},
		{
			name: "partial file merges with defaults",
			toml: `
[daemon]
log_level = "debug"

[cactus]
url = "http://10.0.0.1:8080"
`,
			check: func(t *testing.T, cfg *Config) {
				if cfg.Daemon.LogLevel != "debug" {
					t.Errorf("LogLevel = %q, want %q", cfg.Daemon.LogLevel, "debug")
				}
				if cfg.Cactus.URL != "http://10.0.0.1:8080" {
					t.Errorf("Cactus.URL = %q, want overridden value", cfg.Cactus.URL)
				}
				// Unset fields keep defaults.
				if cfg.Notifier.Level != 2 {
					t.Errorf("Notifier.Level = %d, want 2 (default)", cfg.Notifier.Level)
				}
				if cfg.Retention.RawEventDays != 90 {
					t.Errorf("RawEventDays = %d, want 90 (default)", cfg.Retention.RawEventDays)
				}
				// Cactus routing_mode not set in file, should keep default.
				if cfg.Cactus.RoutingMode != "localfirst" {
					t.Errorf("RoutingMode = %q, want %q", cfg.Cactus.RoutingMode, "localfirst")
				}
			},
		},
		{
			name: "full file overrides all defaults",
			toml: `
[daemon]
log_level = "warn"
watch_dirs = ["/home/user/code"]
repo_dirs = ["/home/user/code/myproject"]
db_path = "/tmp/test.db"
socket_path = "/tmp/test.sock"

[notifier]
level = 3
digest_time = "08:00"

[cactus]
url = "http://remote:9090"
routing_mode = "remote"
timeout_seconds = 120

[retention]
raw_event_days = 30
`,
			check: func(t *testing.T, cfg *Config) {
				if cfg.Daemon.LogLevel != "warn" {
					t.Errorf("LogLevel = %q", cfg.Daemon.LogLevel)
				}
				if len(cfg.Daemon.WatchDirs) != 1 || cfg.Daemon.WatchDirs[0] != "/home/user/code" {
					t.Errorf("WatchDirs = %v", cfg.Daemon.WatchDirs)
				}
				if cfg.Notifier.Level != 3 {
					t.Errorf("Notifier.Level = %d, want 3", cfg.Notifier.Level)
				}
				if cfg.Notifier.DigestTime != "08:00" {
					t.Errorf("DigestTime = %q, want 08:00", cfg.Notifier.DigestTime)
				}
				if cfg.Cactus.RoutingMode != "remote" {
					t.Errorf("RoutingMode = %q", cfg.Cactus.RoutingMode)
				}
				if cfg.Cactus.TimeoutSeconds != 120 {
					t.Errorf("TimeoutSeconds = %d", cfg.Cactus.TimeoutSeconds)
				}
				if cfg.Retention.RawEventDays != 30 {
					t.Errorf("RawEventDays = %d, want 30", cfg.Retention.RawEventDays)
				}
			},
		},
		{
			name:    "invalid TOML returns error",
			toml:    `[daemon\nNOT VALID TOML`,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.toml")

			if tc.toml != "" {
				if err := os.WriteFile(path, []byte(tc.toml), 0o644); err != nil {
					t.Fatal(err)
				}
			} else {
				// Use a non-existent path so Load sees ErrNotExist.
				path = filepath.Join(dir, "nonexistent.toml")
			}

			cfg, err := Load(path)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.check != nil {
				tc.check(t, cfg)
			}
		})
	}
}
