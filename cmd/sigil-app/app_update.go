package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Version is the compiled-in version. Overridden by -ldflags at release time.
var Version = "0.1.0-dev"

// UpdateInfo describes an available update.
type UpdateInfo struct {
	Version   string `json:"version"`
	Changelog string `json:"changelog"`
	URL       string `json:"url"`
	Checksum  string `json:"checksum"`
}

// updateState tracks in-progress download state.
type updateState struct {
	stagedPath string
}

// ghRelease is the subset of the GitHub Releases API response we need.
type ghRelease struct {
	TagName string    `json:"tag_name"`
	Body    string    `json:"body"`
	Assets  []ghAsset `json:"assets"`
}

// ghAsset is a single release asset from GitHub.
type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

const (
	ghReleasesURL = "https://api.github.com/repos/sigil-tech/sigil/releases/latest"
	httpTimeout   = 30 * time.Second
)

// GetVersion returns the compiled-in version string.
func (a *App) GetVersion() string {
	return Version
}

// CheckForUpdate queries GitHub Releases for a newer version. Returns nil if
// up-to-date or on error (errors are logged, not surfaced to the UI).
func (a *App) CheckForUpdate() (*UpdateInfo, error) {
	client := &http.Client{Timeout: httpTimeout}
	req, err := http.NewRequest("GET", ghReleasesURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "sigil-app/"+Version)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch latest release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github api returned %d", resp.StatusCode)
	}

	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}

	latestVer := strings.TrimPrefix(rel.TagName, "v")
	currentVer := strings.TrimPrefix(Version, "v")

	if !semverLessThan(currentVer, latestVer) {
		return nil, nil // up-to-date
	}

	// Find the binary asset and optional checksum file.
	var assetURL, checksum string
	binaryName := expectedBinaryName()
	checksumName := binaryName + ".sha256"
	for _, asset := range rel.Assets {
		if asset.Name == binaryName {
			assetURL = asset.BrowserDownloadURL
		}
		if asset.Name == checksumName {
			checksum = asset.BrowserDownloadURL
		}
	}

	// If we found a checksum URL, fetch the actual checksum value.
	if checksum != "" {
		if val, err := fetchChecksumFile(client, checksum); err == nil {
			checksum = val
		} else {
			checksum = ""
		}
	}

	if assetURL == "" {
		return nil, fmt.Errorf("no asset found for %s", binaryName)
	}

	return &UpdateInfo{
		Version:   latestVer,
		Changelog: rel.Body,
		URL:       assetURL,
		Checksum:  checksum,
	}, nil
}

// DownloadUpdate downloads the binary from url to a temp directory and verifies
// the SHA256 checksum. Only HTTPS URLs are accepted.
func (a *App) DownloadUpdate(url, checksum string) error {
	if !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("refusing non-HTTPS download url")
	}

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("download update: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %d", resp.StatusCode)
	}

	tmpDir, err := os.MkdirTemp("", "sigil-update-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}

	staged := filepath.Join(tmpDir, "sigil-app-new")
	f, err := os.OpenFile(staged, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("create staged file: %w", err)
	}

	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(f, hasher), resp.Body)
	if closeErr := f.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write staged binary: %w", err)
	}

	// Emit progress event (download complete).
	if a.ctx != nil {
		wailsrt.EventsEmit(a.ctx, "update:download-complete", written)
	}

	// Verify checksum if provided.
	if checksum != "" {
		got := hex.EncodeToString(hasher.Sum(nil))
		if !strings.EqualFold(got, checksum) {
			if rmErr := os.RemoveAll(tmpDir); rmErr != nil {
				a.log.Warn("cleanup temp dir after checksum mismatch", "err", rmErr)
			}
			return fmt.Errorf("checksum mismatch: expected %s, got %s", checksum, got)
		}
	}

	a.mu.Lock()
	a.update.stagedPath = staged
	a.mu.Unlock()

	return nil
}

// ApplyUpdate performs an atomic swap of the current binary with the staged
// download: current -> .backup, staged -> current. The caller should prompt
// the user to restart.
func (a *App) ApplyUpdate() error {
	a.mu.RLock()
	staged := a.update.stagedPath
	a.mu.RUnlock()

	if staged == "" {
		return fmt.Errorf("no staged update to apply")
	}

	current, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve current binary: %w", err)
	}
	current, err = filepath.EvalSymlinks(current)
	if err != nil {
		return fmt.Errorf("resolve symlinks: %w", err)
	}

	backup := current + ".backup"

	// Remove stale backup if present (best-effort; may not exist).
	if rmErr := os.Remove(backup); rmErr != nil && !os.IsNotExist(rmErr) {
		a.log.Warn("remove stale backup", "path", backup, "err", rmErr)
	}

	// Atomic rename: current -> backup.
	if err := os.Rename(current, backup); err != nil {
		return fmt.Errorf("backup current binary: %w", err)
	}

	// Move staged -> current.
	if err := os.Rename(staged, current); err != nil {
		// Attempt rollback.
		if rbErr := os.Rename(backup, current); rbErr != nil {
			a.log.Error("rollback failed after install error", "err", rbErr)
		}
		return fmt.Errorf("install staged binary: %w", err)
	}

	a.mu.Lock()
	a.update.stagedPath = ""
	a.mu.Unlock()

	return nil
}

// checkUpdateOnStartup is called during app startup. It respects the
// update_mode config value: "auto", "notify", or "disabled".
func (a *App) checkUpdateOnStartup() {
	mode := a.getUpdateMode()
	if mode == "disabled" {
		return
	}

	info, err := a.CheckForUpdate()
	if err != nil {
		a.log.Debug("update check failed", "err", err)
		return
	}
	if info == nil {
		return // up-to-date
	}

	a.log.Info("update available", "version", info.Version)

	if a.ctx != nil {
		wailsrt.EventsEmit(a.ctx, "update:available", info)
	}

	if mode == "auto" {
		if err := a.DownloadUpdate(info.URL, info.Checksum); err != nil {
			a.log.Warn("auto-download failed", "err", err)
		}
	}
}

// getUpdateMode reads the update_mode from the daemon config. Defaults to
// "notify" if unset or on error.
func (a *App) getUpdateMode() string {
	cfg, err := a.GetConfig()
	if err != nil {
		return "notify"
	}
	daemon, ok := cfg["daemon"].(map[string]any)
	if !ok {
		return "notify"
	}
	mode, ok := daemon["update_mode"].(string)
	if !ok || mode == "" {
		return "notify"
	}
	return mode
}

// SetUpdateMode updates the daemon config with the chosen update mode.
func (a *App) SetUpdateMode(mode string) error {
	switch mode {
	case "auto", "notify", "disabled":
	default:
		return fmt.Errorf("invalid update mode: %s", mode)
	}
	_, err := a.SetConfig(map[string]any{
		"daemon": map[string]any{
			"update_mode": mode,
		},
	})
	return err
}

// ---------------------------------------------------------------------------
// Semver comparison (major.minor.patch only, no pre-release ordering)
// ---------------------------------------------------------------------------

// semverLessThan returns true if a < b using simple major.minor.patch comparison.
// Pre-release suffixes (e.g. "-dev") are stripped for numeric comparison, but
// any pre-release version is considered less than the same version without one.
func semverLessThan(a, b string) bool {
	aParts, aPre := parseSemver(a)
	bParts, bPre := parseSemver(b)

	for i := 0; i < 3; i++ {
		if aParts[i] < bParts[i] {
			return true
		}
		if aParts[i] > bParts[i] {
			return false
		}
	}
	// Numeric parts equal — pre-release < release.
	if aPre != "" && bPre == "" {
		return true
	}
	return false
}

// parseSemver extracts [major, minor, patch] and any pre-release suffix.
func parseSemver(v string) ([3]int, string) {
	v = strings.TrimPrefix(v, "v")
	var parts [3]int
	pre := ""

	// Split off pre-release suffix.
	if idx := strings.IndexByte(v, '-'); idx >= 0 {
		pre = v[idx+1:]
		v = v[:idx]
	}

	segs := strings.SplitN(v, ".", 3)
	for i, seg := range segs {
		if i >= 3 {
			break
		}
		n := 0
		for _, c := range seg {
			if c >= '0' && c <= '9' {
				n = n*10 + int(c-'0')
			}
		}
		parts[i] = n
	}
	return parts, pre
}

// expectedBinaryName returns the expected release asset name for the current
// platform.
func expectedBinaryName() string {
	return "sigil-app"
}

// fetchChecksumFile downloads a .sha256 file and extracts the hex digest.
func fetchChecksumFile(client *http.Client, url string) (string, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("fetch checksum file: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return "", fmt.Errorf("read checksum file: %w", err)
	}

	// Format is typically "hexdigest  filename" or just "hexdigest".
	line := strings.TrimSpace(string(body))
	if idx := strings.IndexByte(line, ' '); idx > 0 {
		line = line[:idx]
	}
	return line, nil
}
