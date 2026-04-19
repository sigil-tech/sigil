// Package launcherprofile provides a read-only Go mirror of the Swift
// LauncherProfile type defined at
// sigil-launcher-macos/SigilLauncher/Models/LauncherProfile.swift.
//
// The Swift struct is the source of truth. Any field added to the Swift struct
// without a corresponding Go field will cause Read to return a parse error
// (DisallowUnknownFields — FR-013a). When that happens, update Profile and
// regenerate testdata/launcher_profile_round_trip.json per the instructions in
// testdata/FIXTURE-SOURCE.md.
package launcherprofile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// ErrProfileMissing is returned by Read when the settings file does not exist.
// Callers that treat a missing profile as a recoverable condition (e.g. first
// run) should check errors.Is(err, ErrProfileMissing).
var ErrProfileMissing = errors.New("launcher profile not found")

// Profile mirrors sigil-launcher-macos/SigilLauncher/Models/LauncherProfile.swift.
//
// JSON key names are taken verbatim from the Swift CodingKeys enum; do not
// rename them. Types are mapped as follows:
//
//	Swift UInt64   → Go uint64
//	Swift Int      → Go int
//	Swift UInt16   → Go uint16
//	Swift String   → Go string
//	Swift String?  → Go *string  (nil when the JSON key is absent or null)
//
// Fields are declared in the same order as the Swift CodingKeys enum to make
// diff review against the Swift source straightforward.
type Profile struct {
	// MemorySize is RAM allocated to the VM in bytes.
	MemorySize uint64 `json:"memorySize"`

	// CPUCount is the number of CPU cores allocated to the VM.
	CPUCount int `json:"cpuCount"`

	// WorkspacePath is the host directory mounted as /workspace in the VM.
	WorkspacePath string `json:"workspacePath"`

	// DiskImagePath is the path to the VM disk image.
	DiskImagePath string `json:"diskImagePath"`

	// KernelPath is the path to the kernel image (vmlinuz).
	KernelPath string `json:"kernelPath"`

	// InitrdPath is the path to the initrd.
	InitrdPath string `json:"initrdPath"`

	// SSHPort is the localhost port forwarded to the VM's SSH daemon.
	SSHPort uint16 `json:"sshPort"`

	// KernelCommandLine contains the kernel boot arguments.
	KernelCommandLine string `json:"kernelCommandLine"`

	// Editor is the editor installed in the VM: "vscode", "neovim", "both", or "none".
	// Defaults to "vscode" when absent in the JSON.
	Editor string `json:"editor"`

	// ContainerEngine is the container runtime: "docker" or "none".
	// Defaults to "docker" when absent in the JSON.
	ContainerEngine string `json:"containerEngine"`

	// Shell is the default login shell: "zsh" or "bash".
	// Defaults to "zsh" when absent in the JSON.
	Shell string `json:"shell"`

	// NotificationLevel controls sigild suggestion verbosity
	// (0=silent, 1=digest, 2=ambient, 3=conversational, 4=autonomous).
	// Defaults to 2 when absent in the JSON.
	NotificationLevel int `json:"notificationLevel"`

	// ModelID is the selected local model ID from the catalog.
	// Nil means cloud-only inference.
	ModelID *string `json:"modelId"`

	// ModelPath is the path to the downloaded model file on disk.
	// Nil when no local model has been downloaded.
	ModelPath *string `json:"modelPath"`
}

// Read parses the LauncherProfile JSON at the platform-native settings path.
//
// On macOS the path is ~/.sigil/launcher/settings.json (matching the Swift
// LauncherProfile.settingsURL property). On Linux the path is
// $XDG_CONFIG_HOME/sigil-launcher/settings.json, falling back to
// ~/.config/sigil-launcher/settings.json (build-tagged; see paths_linux.go).
//
// FR-013a: Read uses json.Decoder.DisallowUnknownFields so that any field
// added to the Swift source without a corresponding Go field causes an
// explicit parse error instead of silent data loss. Update Profile and the
// round-trip fixture when this happens.
//
// Error behaviour:
//   - (Profile{}, ErrProfileMissing) when the file does not exist.
//   - (Profile{}, wrapped error) for any I/O or parse failure.
func Read() (Profile, error) {
	path, err := settingsPath()
	if err != nil {
		return Profile{}, fmt.Errorf("launcherprofile: resolve path: %w", err)
	}

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Profile{}, fmt.Errorf("launcherprofile: %w", ErrProfileMissing)
		}
		return Profile{}, fmt.Errorf("launcherprofile: open %s: %w", path, err)
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()

	var p Profile
	if err := dec.Decode(&p); err != nil {
		return Profile{}, fmt.Errorf("launcherprofile: parse %s: %w", path, err)
	}
	return p, nil
}
