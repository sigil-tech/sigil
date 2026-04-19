// Package config loads and merges sigild's file-based TOML configuration.
// File values are defaults; CLI flags passed by the caller always win.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// DefaultFleetEndpoint is the canonical fleet reporting URL.
const DefaultFleetEndpoint = "https://fleet.sigil.dev/api/v1"

// DefaultCloudSyncURL is the canonical cloud sync ingest URL.
const DefaultCloudSyncURL = "https://ingest.sigil.cloud/api/v1"

// Config holds every tunable parameter for sigild.
// Zero values mean "use the built-in default" so callers can detect which
// fields were actually set by the file.
type Config struct {
	Daemon    DaemonConfig            `toml:"daemon" json:"daemon"`
	Notifier  NotifierConfig          `toml:"notifier" json:"notifier"`
	Inference InferenceConfig         `toml:"inference" json:"inference"`
	ML        MLConfig                `toml:"ml" json:"ml"`
	Plugins   map[string]PluginConfig `toml:"plugins" json:"plugins,omitempty"`
	Retention RetentionConfig         `toml:"retention" json:"retention"`
	Schedule  ScheduleConfig          `toml:"schedule" json:"schedule"`
	Fleet     FleetConfig             `toml:"fleet" json:"fleet"`
	Network   NetworkConfig           `toml:"network" json:"network"`
	Cloud     CloudConfig             `toml:"cloud" json:"cloud"`
	CloudSync CloudSyncConfig         `toml:"cloud_sync" json:"cloud_sync"`
	Sync      SyncConfig              `toml:"sync" json:"sync"`
	Sources   SourcesConfig           `toml:"sources" json:"sources"`
	VM        VMConfig                `toml:"vm" json:"vm"`
	Merge     MergeConfig             `toml:"merge" json:"merge"`
	Corpus    CorpusConfig            `toml:"corpus" json:"corpus"`
}

// SourcesConfig controls per-source enable/disable and poll intervals.
//
// The Frequency field sets all sources to a preset:
//   - "high":   fastest polling, most data, higher CPU
//   - "medium": balanced (default)
//   - "low":    minimal polling, least CPU
//
// Individual sources can override with their own poll_interval.
type SourcesConfig struct {
	Frequency    string               `toml:"frequency" json:"frequency"` // "high", "medium", "low"
	Idle         SourceConfig         `toml:"idle" json:"idle"`
	Typing       SourceConfig         `toml:"typing" json:"typing"`
	Pointer      SourceConfig         `toml:"pointer" json:"pointer"`
	Desktop      SourceConfig         `toml:"desktop" json:"desktop"`
	Display      SourceConfig         `toml:"display" json:"display"`
	Audio        SourceConfig         `toml:"audio" json:"audio"`
	Power        SourceConfig         `toml:"power" json:"power"`
	Network      NetworkSourceConfig  `toml:"network" json:"network"`
	FocusMode    SourceConfig         `toml:"focus_mode" json:"focus_mode"`
	AppLifecycle SourceConfig         `toml:"app_lifecycle" json:"app_lifecycle"`
	Screenshot   SourceConfig         `toml:"screenshot" json:"screenshot"`
	Download     DownloadSourceConfig `toml:"download" json:"download"`
	Calendar     CalendarSourceConfig `toml:"calendar" json:"calendar"`
	Browser      BrowserSourceConfig  `toml:"browser" json:"browser"`
	Files        SourceConfig         `toml:"files" json:"files"`
	Git          SourceConfig         `toml:"git" json:"git"`
	Clipboard    SourceConfig         `toml:"clipboard" json:"clipboard"`
	Process      SourceConfig         `toml:"process" json:"process"`
}

// SourceConfig is the common config for a polled source.
type SourceConfig struct {
	Enabled      *bool  `toml:"enabled" json:"enabled"`
	PollInterval string `toml:"poll_interval" json:"poll_interval"` // duration string, e.g. "5s", "30s"
}

// IsEnabled returns whether the source is enabled, using the provided default
// when the user hasn't explicitly set a value.
func (s SourceConfig) IsEnabled(defaultOn bool) bool {
	if s.Enabled == nil {
		return defaultOn
	}
	return *s.Enabled
}

// Interval returns the poll interval, falling back to the given default.
func (s SourceConfig) Interval(fallback time.Duration) time.Duration {
	if s.PollInterval == "" {
		return fallback
	}
	d, err := time.ParseDuration(s.PollInterval)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

// frequencyDefaults maps the frequency preset to per-source default intervals.
// Sources not listed here use their own hardcoded defaults.
var frequencyDefaults = map[string]map[string]time.Duration{
	"high": {
		"idle":          3 * time.Second,
		"typing":        15 * time.Second,
		"pointer":       15 * time.Second,
		"desktop":       1 * time.Second,
		"display":       10 * time.Second,
		"audio":         5 * time.Second,
		"power":         30 * time.Second,
		"network":       15 * time.Second,
		"app_lifecycle": 3 * time.Second,
		"browser":       1 * time.Second,
	},
	"medium": {
		"idle":          5 * time.Second,
		"typing":        30 * time.Second,
		"pointer":       30 * time.Second,
		"desktop":       2 * time.Second,
		"display":       30 * time.Second,
		"audio":         10 * time.Second,
		"power":         60 * time.Second,
		"network":       30 * time.Second,
		"app_lifecycle": 5 * time.Second,
		"browser":       2 * time.Second,
	},
	"low": {
		"idle":          10 * time.Second,
		"typing":        60 * time.Second,
		"pointer":       60 * time.Second,
		"desktop":       5 * time.Second,
		"display":       60 * time.Second,
		"audio":         30 * time.Second,
		"power":         120 * time.Second,
		"network":       60 * time.Second,
		"app_lifecycle": 10 * time.Second,
		"browser":       5 * time.Second,
	},
}

// SourceInterval returns the effective poll interval for a source,
// checking: (1) source-level override, (2) frequency preset, (3) fallback.
func (c SourcesConfig) SourceInterval(sourceName string, sourceOverride string, fallback time.Duration) time.Duration {
	// 1. Explicit per-source override.
	if sourceOverride != "" {
		if d, err := time.ParseDuration(sourceOverride); err == nil && d > 0 {
			return d
		}
	}

	// 2. Frequency preset.
	freq := c.Frequency
	if freq == "" {
		freq = "medium"
	}
	if defaults, ok := frequencyDefaults[freq]; ok {
		if d, ok := defaults[sourceName]; ok {
			return d
		}
	}

	// 3. Hardcoded fallback.
	return fallback
}

// NetworkSourceConfig adds SSID hashing option.
type NetworkSourceConfig struct {
	Enabled      *bool  `toml:"enabled" json:"enabled"`
	PollInterval string `toml:"poll_interval" json:"poll_interval"`
	HashSSID     bool   `toml:"hash_ssid" json:"hash_ssid"`
}

// IsEnabled returns whether the network source is enabled, using the provided
// default when the user hasn't explicitly set a value.
func (s NetworkSourceConfig) IsEnabled(defaultOn bool) bool {
	if s.Enabled == nil {
		return defaultOn
	}
	return *s.Enabled
}

// DownloadSourceConfig adds watch directory.
type DownloadSourceConfig struct {
	Enabled  *bool  `toml:"enabled" json:"enabled"`
	WatchDir string `toml:"watch_dir" json:"watch_dir"`
}

// IsEnabled returns whether the download source is enabled, using the provided
// default when the user hasn't explicitly set a value.
func (s DownloadSourceConfig) IsEnabled(defaultOn bool) bool {
	if s.Enabled == nil {
		return defaultOn
	}
	return *s.Enabled
}

// CalendarSourceConfig adds calendar filter.
type CalendarSourceConfig struct {
	Enabled      *bool    `toml:"enabled" json:"enabled"`
	PollInterval string   `toml:"poll_interval" json:"poll_interval"`
	Calendars    []string `toml:"calendars" json:"calendars"`
}

// IsEnabled returns whether the calendar source is enabled, using the provided
// default when the user hasn't explicitly set a value.
func (s CalendarSourceConfig) IsEnabled(defaultOn bool) bool {
	if s.Enabled == nil {
		return defaultOn
	}
	return *s.Enabled
}

// BrowserSourceConfig adds domain blocklist and poll interval.
type BrowserSourceConfig struct {
	Enabled        *bool    `toml:"enabled" json:"enabled"`
	PollInterval   string   `toml:"poll_interval" json:"poll_interval"`
	BlockedDomains []string `toml:"blocked_domains" json:"blocked_domains"`
}

// IsEnabled returns whether the browser source is enabled, using the provided
// default when the user hasn't explicitly set a value.
func (s BrowserSourceConfig) IsEnabled(defaultOn bool) bool {
	if s.Enabled == nil {
		return defaultOn
	}
	return *s.Enabled
}

// SyncConfig controls the Sync Agent that streams SQLite changes to the cloud.
type SyncConfig struct {
	Enabled  bool   `toml:"enabled" json:"enabled"`
	APIURL   string `toml:"api_url" json:"api_url"`
	APIKey   string `toml:"api_key" json:"api_key"`
	Interval string `toml:"interval" json:"interval"`
	Batch    int    `toml:"batch_size" json:"batch_size"`
}

// PluginConfig defines a single plugin's configuration.
type PluginConfig struct {
	Enabled      bool              `toml:"enabled" json:"enabled"`
	Binary       string            `toml:"binary" json:"binary"`
	Daemon       bool              `toml:"daemon" json:"daemon"`
	PollInterval string            `toml:"poll_interval" json:"poll_interval"`
	HealthURL    string            `toml:"health_url" json:"health_url"`
	Env          map[string]string `toml:"env" json:"env,omitempty"`
}

// MLConfig configures the ML prediction sidecar.
type MLConfig struct {
	Mode         string        `toml:"mode" json:"mode"`
	RetrainEvery int           `toml:"retrain_every" json:"retrain_every"`
	Local        MLLocalConfig `toml:"local" json:"local"`
	Cloud        MLCloudConfig `toml:"cloud" json:"cloud"`
}

// MLLocalConfig configures the local sigil-ml sidecar.
type MLLocalConfig struct {
	Enabled   bool   `toml:"enabled" json:"enabled"`
	ServerURL string `toml:"server_url" json:"server_url"`
	ServerBin string `toml:"server_bin" json:"server_bin"`
}

// MLCloudConfig configures the cloud ML API.
type MLCloudConfig struct {
	Enabled bool   `toml:"enabled" json:"enabled"`
	BaseURL string `toml:"base_url" json:"base_url"`
	APIKey  string `toml:"api_key" json:"api_key"`
}

// CloudConfig holds cloud tier and authentication settings.
type CloudConfig struct {
	Tier   string `toml:"tier" json:"tier"`
	APIKey string `toml:"api_key" json:"api_key"`
	OrgID  string `toml:"org_id" json:"org_id"`
}

// CloudSyncConfig controls the sync agent behavior.
type CloudSyncConfig struct {
	Enabled      *bool  `toml:"enabled" json:"enabled"`
	APIURL       string `toml:"api_url" json:"api_url"`
	BatchSize    int    `toml:"batch_size" json:"batch_size"`
	PollInterval string `toml:"poll_interval" json:"poll_interval"`
}

// IsEnabled returns whether cloud sync is enabled (defaults to false if unset).
func (c CloudSyncConfig) IsEnabled() bool {
	if c.Enabled == nil {
		return false
	}
	return *c.Enabled
}

// NetworkConfig controls the optional TCP listener.
type NetworkConfig struct {
	Enabled            bool     `toml:"enabled" json:"enabled"`
	Bind               string   `toml:"bind" json:"bind"`
	Port               int      `toml:"port" json:"port"`
	AllowedCredentials []string `toml:"allowed_credentials" json:"allowed_credentials"`
}

// DaemonConfig covers process-level settings.
type DaemonConfig struct {
	LogLevel          string   `toml:"log_level" json:"log_level"`
	WatchDirs         []string `toml:"watch_dirs" json:"watch_dirs"`
	RepoDirs          []string `toml:"repo_dirs" json:"repo_dirs"`
	IgnorePatterns    []string `toml:"ignore_patterns" json:"ignore_patterns"`
	DBPath            string   `toml:"db_path" json:"db_path"`
	SocketPath        string   `toml:"socket_path" json:"socket_path"`
	MaxWatches        int      `toml:"max_watches" json:"max_watches"`
	ActuationsEnabled *bool    `toml:"actuations_enabled" json:"actuations_enabled"`
}

// IsActuationsEnabled returns whether actuations are enabled (defaults to true).
func (d DaemonConfig) IsActuationsEnabled() bool {
	if d.ActuationsEnabled == nil {
		return true
	}
	return *d.ActuationsEnabled
}

// NotifierConfig controls how suggestions are surfaced.
type NotifierConfig struct {
	Level           *int        `toml:"level" json:"level"`
	DigestTime      string      `toml:"digest_time" json:"digest_time"`
	DND             DNDSchedule `toml:"dnd" json:"dnd"`
	MutedCategories []string    `toml:"muted_categories" json:"muted_categories"`
}

// DNDSchedule defines a Do Not Disturb window.
type DNDSchedule struct {
	Enabled bool     `toml:"enabled" json:"enabled"`
	Start   string   `toml:"start" json:"start"`
	End     string   `toml:"end" json:"end"`
	Days    []string `toml:"days" json:"days"`
}

// LevelOrDefault returns the notification level, defaulting to 2 (Ambient).
func (n NotifierConfig) LevelOrDefault() int {
	if n.Level == nil {
		return 2
	}
	return *n.Level
}

// ScheduleConfig controls analysis timing.
type ScheduleConfig struct {
	AnalyzeEvery string `toml:"analyze_every" json:"analyze_every"`
}

// InferenceConfig configures the inference engine backends.
type InferenceConfig struct {
	Mode  string               `toml:"mode" json:"mode"`
	Local InferenceLocalConfig `toml:"local" json:"local"`
	Cloud InferenceCloudConfig `toml:"cloud" json:"cloud"`
}

// InferenceLocalConfig configures the local inference backend.
type InferenceLocalConfig struct {
	Enabled   bool   `toml:"enabled" json:"enabled"`
	ServerURL string `toml:"server_url" json:"server_url"`
	ServerBin string `toml:"server_bin" json:"server_bin"`
	ModelPath string `toml:"model_path" json:"model_path"`
	ModelName string `toml:"model_name" json:"model_name"`
	CtxSize   int    `toml:"ctx_size" json:"ctx_size"`
	GPULayers int    `toml:"gpu_layers" json:"gpu_layers"`
}

// InferenceCloudConfig configures the cloud inference backend.
type InferenceCloudConfig struct {
	Enabled  bool   `toml:"enabled" json:"enabled"`
	Provider string `toml:"provider" json:"provider"`
	BaseURL  string `toml:"base_url" json:"base_url"`
	APIKey   string `toml:"api_key" json:"api_key"`
	Model    string `toml:"model" json:"model"`
}

// RetentionConfig controls how long raw data is kept.
type RetentionConfig struct {
	RawEventDays int `toml:"raw_event_days" json:"raw_event_days"`
}

// VMConfig is the [vm] section of config.Config.
// It is read by both host and guest sigild instances.
type VMConfig struct {
	Mode                string   `toml:"mode" json:"mode"`                                   // "host" | "vm" | "" (disabled)
	Enabled             bool     `toml:"enabled" json:"enabled"`                             // host: start HostContextServer
	DBPath              string   `toml:"db_path" json:"db_path"`                             // vm: SQLite path
	VsockControlPort    int      `toml:"vsock_control_port" json:"vsock_control_port"`       // default 7700
	VsockInferencePort  int      `toml:"vsock_inference_port" json:"vsock_inference_port"`   // default 7701
	Denylist            []string `toml:"denylist" json:"denylist,omitempty"`                 // glob patterns for process name filtering
	RateLimitPerSource  int      `toml:"rate_limit_per_source" json:"rate_limit_per_source"` // default 1000
	InferenceQueueDepth int      `toml:"inference_queue_depth" json:"inference_queue_depth"` // default 50
}

// VMControlPort returns the vsock control port, defaulting to 7700.
func (v VMConfig) VMControlPort() int {
	if v.VsockControlPort == 0 {
		return 7700
	}
	return v.VsockControlPort
}

// VMInferencePort returns the vsock inference port, defaulting to 7701.
func (v VMConfig) VMInferencePort() int {
	if v.VsockInferencePort == 0 {
		return 7701
	}
	return v.VsockInferencePort
}

// RateLimit returns the per-source rate limit, defaulting to 1000.
func (v VMConfig) RateLimit() int {
	if v.RateLimitPerSource == 0 {
		return 1000
	}
	return v.RateLimitPerSource
}

// QueueDepth returns the inference queue depth, defaulting to 50.
func (v VMConfig) QueueDepth() int {
	if v.InferenceQueueDepth == 0 {
		return 50
	}
	return v.InferenceQueueDepth
}

// MergeConfig is the [merge] section of config.Config.
// It controls the VM-to-host merge pipeline behavior.
type MergeConfig struct {
	Denylist                []string `toml:"denylist" json:"denylist,omitempty"`
	MaxDBSizeMB             int      `toml:"max_db_size_mb" json:"max_db_size_mb"`                       // default 512
	SessionBudgetMB         int      `toml:"session_budget_mb" json:"session_budget_mb"`                 // default 256
	MaxRowPayloadBytes      int      `toml:"max_row_payload_bytes" json:"max_row_payload_bytes"`         // default 65536
	QuarantineRetentionDays int      `toml:"quarantine_retention_days" json:"quarantine_retention_days"` // default 30
	FilterVersion           string   `toml:"filter_version" json:"filter_version"`                       // semantic version of the ruleset
	BatchSize               int      `toml:"batch_size" json:"batch_size"`                               // default 500
}

// MaxDBSize returns the max VM DB size in bytes, defaulting to 512MB.
func (m MergeConfig) MaxDBSize() int64 {
	if m.MaxDBSizeMB <= 0 {
		return 512 * 1024 * 1024
	}
	return int64(m.MaxDBSizeMB) * 1024 * 1024
}

// SessionBudget returns the per-session merge budget in bytes, defaulting to 256MB.
func (m MergeConfig) SessionBudget() int64 {
	if m.SessionBudgetMB <= 0 {
		return 256 * 1024 * 1024
	}
	return int64(m.SessionBudgetMB) * 1024 * 1024
}

// MaxRowPayload returns the per-row payload size limit in bytes, defaulting to 64KB.
func (m MergeConfig) MaxRowPayload() int {
	if m.MaxRowPayloadBytes <= 0 {
		return 65536
	}
	return m.MaxRowPayloadBytes
}

// QuarantineRetention returns the retention period in days, defaulting to 30.
func (m MergeConfig) QuarantineRetention() int {
	if m.QuarantineRetentionDays <= 0 {
		return 30
	}
	return m.QuarantineRetentionDays
}

// MergeBatchSize returns the batch size, defaulting to 500.
func (m MergeConfig) MergeBatchSize() int {
	if m.BatchSize <= 0 {
		return 500
	}
	return m.BatchSize
}

// DefaultDenylist returns the default merge filter denylist patterns.
func DefaultDenylist() []string {
	return []string{
		"*.pem", "*.key", "*.env", "id_rsa", "*secret*", "*password*",
		"*token*", "*.p12", "*.pfx", "*credential*", "*bearer*", "*.gpg", "*.asc",
	}
}

// EffectiveDenylist returns the configured denylist if non-empty, otherwise the default.
func (m MergeConfig) EffectiveDenylist() []string {
	if len(m.Denylist) > 0 {
		return m.Denylist
	}
	return DefaultDenylist()
}

// CorpusConfig controls the training corpus ingestion and annotation pipeline.
type CorpusConfig struct {
	RetentionDays       int    `toml:"retention_days" json:"retention_days"`               // default 90
	MaxSizeMB           int    `toml:"max_size_mb" json:"max_size_mb"`                     // default 500
	AnnotationInterval  string `toml:"annotation_interval" json:"annotation_interval"`     // default "15m"
	AnnotationBatchSize int    `toml:"annotation_batch_size" json:"annotation_batch_size"` // default 200
	AnnotationMode      string `toml:"annotation_mode" json:"annotation_mode"`             // "local" (default) or "remote"
	VacuumInterval      string `toml:"vacuum_interval" json:"vacuum_interval"`             // default "24h"
}

// RetentionDaysOrDefault returns the retention period, defaulting to 90 days.
func (c CorpusConfig) RetentionDaysOrDefault() int {
	if c.RetentionDays <= 0 {
		return 90
	}
	return c.RetentionDays
}

// MaxSizeBytes returns the maximum corpus size in bytes, defaulting to 500MB.
func (c CorpusConfig) MaxSizeBytes() int64 {
	if c.MaxSizeMB <= 0 {
		return 500 * 1024 * 1024
	}
	return int64(c.MaxSizeMB) * 1024 * 1024
}

// AnnotationIntervalDuration returns the annotation interval, defaulting to 15m.
func (c CorpusConfig) AnnotationIntervalDuration() time.Duration {
	if c.AnnotationInterval == "" {
		return 15 * time.Minute
	}
	d, err := time.ParseDuration(c.AnnotationInterval)
	if err != nil || d <= 0 {
		return 15 * time.Minute
	}
	return d
}

// AnnotationBatchSizeOrDefault returns the annotation batch size, clamped to [50, 1000].
func (c CorpusConfig) AnnotationBatchSizeOrDefault() int {
	if c.AnnotationBatchSize <= 0 {
		return 200
	}
	if c.AnnotationBatchSize < 50 {
		return 50
	}
	if c.AnnotationBatchSize > 1000 {
		return 1000
	}
	return c.AnnotationBatchSize
}

// AnnotationModeOrDefault returns the annotation mode, defaulting to "local".
func (c CorpusConfig) AnnotationModeOrDefault() string {
	if c.AnnotationMode == "" {
		return "local"
	}
	return c.AnnotationMode
}

// VacuumIntervalDuration returns the vacuum interval, defaulting to 24h.
func (c CorpusConfig) VacuumIntervalDuration() time.Duration {
	if c.VacuumInterval == "" {
		return 24 * time.Hour
	}
	d, err := time.ParseDuration(c.VacuumInterval)
	if err != nil || d <= 0 {
		return 24 * time.Hour
	}
	return d
}

// FleetConfig controls the Fleet Reporter subsystem.
type FleetConfig struct {
	Enabled  bool   `toml:"enabled" json:"enabled"`
	Endpoint string `toml:"endpoint" json:"endpoint"`
	Interval string `toml:"interval" json:"interval"`
	NodeID   string `toml:"node_id" json:"node_id"`
}

// ApplyDefaults fills in zero-value fields that have well-known defaults.
// Call after Load to ensure fleet-enabled configs get the production endpoint.
func (c *Config) ApplyDefaults() {
	if c.Fleet.Endpoint == "" && c.Fleet.Enabled {
		c.Fleet.Endpoint = DefaultFleetEndpoint
	}
}

// Defaults returns a Config populated with sensible built-in values.
// This is what the daemon uses when no config file exists.
func Defaults() *Config {
	return &Config{
		Daemon: DaemonConfig{
			LogLevel: "info",
		},
		Notifier: NotifierConfig{
			DigestTime: "09:00",
		},
		Inference: InferenceConfig{
			Mode: "localfirst",
		},
		Retention: RetentionConfig{
			RawEventDays: 90,
		},
		Fleet: FleetConfig{
			Endpoint: DefaultFleetEndpoint,
		},
		CloudSync: CloudSyncConfig{
			APIURL: DefaultCloudSyncURL,
		},
	}
}

// DefaultPath returns the canonical config file location, respecting XDG_CONFIG_HOME.
// On Windows, uses %APPDATA% (Roaming) as the config base.
func DefaultPath() string {
	if runtime.GOOS == "windows" {
		appdata := os.Getenv("APPDATA")
		if appdata == "" {
			appdata = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Roaming")
		}
		return filepath.Join(appdata, "sigil", "config.toml")
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		h, _ := os.UserHomeDir()
		base = filepath.Join(h, ".config")
	}
	return filepath.Join(base, "sigil", "config.toml")
}

// Load reads the TOML file at path and merges it on top of built-in defaults.
// If the file does not exist, defaults are returned without error.
// An invalid TOML file returns an error.
func Load(path string) (*Config, error) {
	cfg := Defaults()

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	// Decode into a temporary struct so zero values in the file don't silently
	// overwrite defaults (e.g. level=0 is valid, but an absent [notifier]
	// section should leave the default level intact).
	var file Config
	if err := toml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}

	merge(cfg, &file)
	return cfg, nil
}

// Marshal serializes a Config to TOML bytes.
func Marshal(cfg *Config) ([]byte, error) {
	return toml.Marshal(cfg)
}

// expandHome replaces a leading ~ with the user's home directory.
func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		return filepath.Join(h, p[2:])
	}
	return p
}

// merge overlays non-zero fields from src onto dst.
func merge(dst, src *Config) {
	if src.Daemon.LogLevel != "" {
		dst.Daemon.LogLevel = src.Daemon.LogLevel
	}
	if len(src.Daemon.WatchDirs) > 0 {
		dst.Daemon.WatchDirs = expandDirs(src.Daemon.WatchDirs)
	}
	if len(src.Daemon.RepoDirs) > 0 {
		dst.Daemon.RepoDirs = expandDirs(src.Daemon.RepoDirs)
	}
	if len(src.Daemon.IgnorePatterns) > 0 {
		dst.Daemon.IgnorePatterns = src.Daemon.IgnorePatterns
	}
	if src.Daemon.DBPath != "" {
		dst.Daemon.DBPath = expandHome(src.Daemon.DBPath)
	}
	if src.Daemon.SocketPath != "" {
		dst.Daemon.SocketPath = expandHome(src.Daemon.SocketPath)
	}
	if src.Daemon.MaxWatches != 0 {
		dst.Daemon.MaxWatches = src.Daemon.MaxWatches
	}

	// Notifier: *int pointer distinguishes absent from explicitly 0 (Silent).
	if src.Notifier.Level != nil {
		dst.Notifier.Level = src.Notifier.Level
	}
	if src.Notifier.DigestTime != "" {
		dst.Notifier.DigestTime = src.Notifier.DigestTime
	}

	// Schedule
	if src.Schedule.AnalyzeEvery != "" {
		dst.Schedule.AnalyzeEvery = src.Schedule.AnalyzeEvery
	}

	if src.Inference.Mode != "" {
		dst.Inference.Mode = src.Inference.Mode
	}
	if src.Inference.Local.Enabled {
		dst.Inference.Local.Enabled = true
	}
	if src.Inference.Local.ServerURL != "" {
		dst.Inference.Local.ServerURL = src.Inference.Local.ServerURL
	}
	if src.Inference.Local.ServerBin != "" {
		dst.Inference.Local.ServerBin = src.Inference.Local.ServerBin
	}
	if src.Inference.Local.ModelPath != "" {
		dst.Inference.Local.ModelPath = src.Inference.Local.ModelPath
	}
	if src.Inference.Local.ModelName != "" {
		dst.Inference.Local.ModelName = src.Inference.Local.ModelName
	}
	if src.Inference.Local.CtxSize != 0 {
		dst.Inference.Local.CtxSize = src.Inference.Local.CtxSize
	}
	if src.Inference.Local.GPULayers != 0 {
		dst.Inference.Local.GPULayers = src.Inference.Local.GPULayers
	}
	if src.Inference.Cloud.Enabled {
		dst.Inference.Cloud.Enabled = true
	}
	if src.Inference.Cloud.Provider != "" {
		dst.Inference.Cloud.Provider = src.Inference.Cloud.Provider
	}
	if src.Inference.Cloud.BaseURL != "" {
		dst.Inference.Cloud.BaseURL = src.Inference.Cloud.BaseURL
	}
	if src.Inference.Cloud.APIKey != "" {
		dst.Inference.Cloud.APIKey = src.Inference.Cloud.APIKey
	}
	if src.Inference.Cloud.Model != "" {
		dst.Inference.Cloud.Model = src.Inference.Cloud.Model
	}

	if src.Retention.RawEventDays != 0 {
		dst.Retention.RawEventDays = src.Retention.RawEventDays
	}

	if src.Fleet.Enabled {
		dst.Fleet.Enabled = true
	}
	if src.Fleet.Endpoint != "" {
		dst.Fleet.Endpoint = src.Fleet.Endpoint
	}
	if src.Fleet.Interval != "" {
		dst.Fleet.Interval = src.Fleet.Interval
	}
	if src.Fleet.NodeID != "" {
		dst.Fleet.NodeID = src.Fleet.NodeID
	}

	if src.Network.Enabled {
		dst.Network.Enabled = true
	}
	if src.Network.Bind != "" {
		dst.Network.Bind = src.Network.Bind
	}
	if src.Network.Port != 0 {
		dst.Network.Port = src.Network.Port
	}
	if len(src.Network.AllowedCredentials) > 0 {
		dst.Network.AllowedCredentials = src.Network.AllowedCredentials
	}

	// Sync
	if src.Sync.Enabled {
		dst.Sync.Enabled = true
	}
	if src.Sync.APIURL != "" {
		dst.Sync.APIURL = src.Sync.APIURL
	}
	if src.Sync.APIKey != "" {
		dst.Sync.APIKey = src.Sync.APIKey
	}
	if src.Sync.Interval != "" {
		dst.Sync.Interval = src.Sync.Interval
	}
	if src.Sync.Batch != 0 {
		dst.Sync.Batch = src.Sync.Batch
	}

	// Plugins (map — just replace entirely if set in file)
	if len(src.Plugins) > 0 {
		dst.Plugins = src.Plugins
	}

	// ML
	if src.ML.Mode != "" {
		dst.ML.Mode = src.ML.Mode
	}
	if src.ML.RetrainEvery != 0 {
		dst.ML.RetrainEvery = src.ML.RetrainEvery
	}
	if src.ML.Local.Enabled {
		dst.ML.Local.Enabled = true
	}
	if src.ML.Local.ServerURL != "" {
		dst.ML.Local.ServerURL = src.ML.Local.ServerURL
	}
	if src.ML.Local.ServerBin != "" {
		dst.ML.Local.ServerBin = src.ML.Local.ServerBin
	}
	if src.ML.Cloud.Enabled {
		dst.ML.Cloud.Enabled = true
	}
	if src.ML.Cloud.BaseURL != "" {
		dst.ML.Cloud.BaseURL = src.ML.Cloud.BaseURL
	}
	if src.ML.Cloud.APIKey != "" {
		dst.ML.Cloud.APIKey = src.ML.Cloud.APIKey
	}

	// Cloud tier
	if src.Cloud.Tier != "" {
		dst.Cloud.Tier = src.Cloud.Tier
	}
	if src.Cloud.APIKey != "" {
		dst.Cloud.APIKey = src.Cloud.APIKey
	}
	if src.Cloud.OrgID != "" {
		dst.Cloud.OrgID = src.Cloud.OrgID
	}

	// Cloud sync
	if src.CloudSync.Enabled != nil {
		dst.CloudSync.Enabled = src.CloudSync.Enabled
	}
	if src.CloudSync.APIURL != "" {
		dst.CloudSync.APIURL = src.CloudSync.APIURL
	}
	if src.CloudSync.BatchSize != 0 {
		dst.CloudSync.BatchSize = src.CloudSync.BatchSize
	}
	if src.CloudSync.PollInterval != "" {
		dst.CloudSync.PollInterval = src.CloudSync.PollInterval
	}

	// VM
	if src.VM.Mode != "" {
		dst.VM.Mode = src.VM.Mode
	}
	if src.VM.Enabled {
		dst.VM.Enabled = true
	}
	if src.VM.DBPath != "" {
		dst.VM.DBPath = src.VM.DBPath
	}
	if src.VM.VsockControlPort != 0 {
		dst.VM.VsockControlPort = src.VM.VsockControlPort
	}
	if src.VM.VsockInferencePort != 0 {
		dst.VM.VsockInferencePort = src.VM.VsockInferencePort
	}
	if len(src.VM.Denylist) > 0 {
		dst.VM.Denylist = src.VM.Denylist
	}
	if src.VM.RateLimitPerSource != 0 {
		dst.VM.RateLimitPerSource = src.VM.RateLimitPerSource
	}
	if src.VM.InferenceQueueDepth != 0 {
		dst.VM.InferenceQueueDepth = src.VM.InferenceQueueDepth
	}

	// Merge
	if len(src.Merge.Denylist) > 0 {
		dst.Merge.Denylist = src.Merge.Denylist
	}
	if src.Merge.MaxDBSizeMB != 0 {
		dst.Merge.MaxDBSizeMB = src.Merge.MaxDBSizeMB
	}
	if src.Merge.SessionBudgetMB != 0 {
		dst.Merge.SessionBudgetMB = src.Merge.SessionBudgetMB
	}
	if src.Merge.MaxRowPayloadBytes != 0 {
		dst.Merge.MaxRowPayloadBytes = src.Merge.MaxRowPayloadBytes
	}
	if src.Merge.QuarantineRetentionDays != 0 {
		dst.Merge.QuarantineRetentionDays = src.Merge.QuarantineRetentionDays
	}
	if src.Merge.FilterVersion != "" {
		dst.Merge.FilterVersion = src.Merge.FilterVersion
	}
	if src.Merge.BatchSize != 0 {
		dst.Merge.BatchSize = src.Merge.BatchSize
	}
}

// Save atomically writes the config to the given path as TOML.
func Save(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename config: %w", err)
	}
	return nil
}

// MaskKeys returns a copy of the config with sensitive fields masked.
func MaskKeys(cfg *Config) *Config {
	c := *cfg // shallow copy
	c.Inference.Cloud.APIKey = maskString(c.Inference.Cloud.APIKey)
	// Copy the plugins map to avoid mutating the original
	if len(c.Plugins) > 0 {
		masked := make(map[string]PluginConfig, len(c.Plugins))
		for k, v := range c.Plugins {
			env := make(map[string]string, len(v.Env))
			for ek, ev := range v.Env {
				if strings.Contains(strings.ToLower(ek), "key") || strings.Contains(strings.ToLower(ek), "token") || strings.Contains(strings.ToLower(ek), "secret") {
					env[ek] = maskString(ev)
				} else {
					env[ek] = ev
				}
			}
			v.Env = env
			masked[k] = v
		}
		c.Plugins = masked
	}
	return &c
}

func maskString(s string) string {
	if len(s) <= 4 {
		if s == "" {
			return ""
		}
		return "****"
	}
	return "****" + s[len(s)-4:]
}

func expandDirs(dirs []string) []string {
	out := make([]string, len(dirs))
	for i, d := range dirs {
		out[i] = expandHome(d)
	}
	return out
}
