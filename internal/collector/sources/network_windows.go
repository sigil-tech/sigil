//go:build windows

package sources

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/sigil-tech/sigil/internal/event"
)

// NetworkSource detects network connection type changes on Windows by polling
// PowerShell Get-NetConnectionProfile. It emits events when the network
// adapter name, interface alias, or connectivity type changes.
type NetworkSource struct{}

// NewNetworkSource creates a NetworkSource.
func NewNetworkSource() *NetworkSource { return &NetworkSource{} }

func (s *NetworkSource) Name() string { return "network" }

// networkState holds the parsed network profile information.
type networkState struct {
	interfaceAlias string
	networkName    string
	connectivity   string // "Internet", "Local", "NoTraffic", "Disconnected"
}

func (s *NetworkSource) Events(ctx context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event, 8)

	go func() {
		defer close(ch)

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		var lastState *networkState

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				state := readNetworkState()
				if state == nil {
					continue
				}

				if lastState != nil && *state == *lastState {
					continue
				}

				action := "changed"
				if lastState != nil && lastState.connectivity == "Internet" && state.connectivity != "Internet" {
					action = "disconnected"
				} else if lastState != nil && lastState.connectivity != "Internet" && state.connectivity == "Internet" {
					action = "connected"
				}

				select {
				case ch <- event.Event{
					Kind:   event.KindNetwork,
					Source: s.Name(),
					Payload: map[string]any{
						"action":       action,
						"interface":    state.interfaceAlias,
						"network_name": state.networkName,
						"connectivity": state.connectivity,
					},
					Timestamp: time.Now(),
				}:
				case <-ctx.Done():
					return
				}

				lastState = state
			}
		}
	}()

	return ch, nil
}

// readNetworkState calls PowerShell Get-NetConnectionProfile to determine
// the current network connection state. Returns nil if the command fails.
func readNetworkState() *networkState {
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		`Get-NetConnectionProfile | Select-Object -First 1 -Property InterfaceAlias,Name,IPv4Connectivity | Format-List`,
	).Output()
	if err != nil {
		return nil
	}

	state := &networkState{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		switch key {
		case "InterfaceAlias":
			state.interfaceAlias = val
		case "Name":
			state.networkName = val
		case "IPv4Connectivity":
			state.connectivity = val
		}
	}

	if state.interfaceAlias == "" && state.networkName == "" {
		return nil
	}
	return state
}
