//go:build linux

package sources

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sigil-tech/sigil/internal/event"
)

// NetworkSource detects network connection changes on Linux by reading
// /sys/class/net/*/operstate and querying nmcli for connection details.
type NetworkSource struct{}

func NewNetworkSource() *NetworkSource { return &NetworkSource{} }

func (s *NetworkSource) Name() string { return "network" }

func (s *NetworkSource) Events(ctx context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event, 8)

	go func() {
		defer close(ch)

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		var lastState string

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				connType, ssidHash, iface := readNetworkState()
				state := fmt.Sprintf("%s|%s|%s", connType, ssidHash, iface)

				if lastState != "" && state != lastState {
					payload := map[string]any{
						"action":          "changed",
						"connection_type": connType,
						"interface":       iface,
					}
					if ssidHash != "" {
						payload["ssid_hash"] = ssidHash
					}
					emit(ch, ctx, event.Event{
						Kind:      event.KindNetwork,
						Source:    s.Name(),
						Payload:   payload,
						Timestamp: time.Now(),
					})
				}
				lastState = state
			}
		}
	}()

	return ch, nil
}

// readNetworkState determines the connection type, hashed SSID, and active
// interface by reading sysfs and nmcli.
func readNetworkState() (connType, ssidHash, iface string) {
	// Try nmcli first for richer information.
	connType, ssidHash, iface = readNmcli()
	if connType != "" {
		return
	}

	// Fallback: read /sys/class/net/*/operstate.
	connType, iface = readSysfsNetwork()
	return connType, "", iface
}

// readNmcli queries NetworkManager for active connection info.
func readNmcli() (connType, ssidHash, iface string) {
	out, err := exec.Command("nmcli", "-t", "-f", "TYPE,STATE,CONNECTION", "dev").Output()
	if err != nil {
		return "", "", ""
	}

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.SplitN(line, ":", 3)
		if len(fields) < 3 {
			continue
		}
		devType := fields[0]
		state := fields[1]
		conn := fields[2]

		if state != "connected" {
			continue
		}

		switch devType {
		case "wifi":
			h := sha256.Sum256([]byte(conn))
			return "wifi", fmt.Sprintf("%x", h[:8]), conn
		case "ethernet":
			return "ethernet", "", conn
		case "vpn", "wireguard":
			return "vpn", "", conn
		}
	}
	return "", "", ""
}

// readSysfsNetwork scans /sys/class/net for an interface that is up.
func readSysfsNetwork() (connType, iface string) {
	netDir := "/sys/class/net"
	entries, err := os.ReadDir(netDir)
	if err != nil {
		return "unknown", ""
	}

	for _, entry := range entries {
		name := entry.Name()
		if name == "lo" {
			continue
		}

		stateBytes, err := os.ReadFile(filepath.Join(netDir, name, "operstate"))
		if err != nil {
			continue
		}
		state := strings.TrimSpace(string(stateBytes))
		if state != "up" {
			continue
		}

		// Heuristic: wireless interfaces typically start with "wl".
		if strings.HasPrefix(name, "wl") {
			return "wifi", name
		}
		if strings.HasPrefix(name, "en") || strings.HasPrefix(name, "eth") {
			return "ethernet", name
		}
		return "other", name
	}

	return "disconnected", ""
}
