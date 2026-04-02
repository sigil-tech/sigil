package main

import (
	"encoding/json"
	"fmt"
)

// CheckInitResult is the response from the check-init socket method.
type CheckInitResult struct {
	Initialized bool   `json:"initialized"`
	ConfigPath  string `json:"config_path"`
}

// InitConfig is the payload sent to the init socket method.
type InitConfig struct {
	WatchDirs         []string `json:"watch_dirs"`
	InferenceMode     string   `json:"inference_mode"`
	NotificationLevel int      `json:"notification_level"`
	Plugins           []string `json:"plugins"`
	CloudEnabled      bool     `json:"cloud_enabled"`
	CloudProvider     string   `json:"cloud_provider"`
	CloudAPIKey       string   `json:"cloud_api_key"`
	LocalInference    bool     `json:"local_inference"`
	FleetEnabled      bool     `json:"fleet_enabled"`
	FleetEndpoint     string   `json:"fleet_endpoint"`
}

// DetectedEnvironment describes what the wizard auto-detected on the system.
type DetectedEnvironment struct {
	IDEs    []string `json:"ides"`
	Tools   []string `json:"tools"`
	Plugins []string `json:"plugins"`
}

// CheckInit calls the daemon's check-init method to determine if config exists.
func (a *App) CheckInit() (CheckInitResult, error) {
	resp, err := a.call("check-init", nil)
	if err != nil {
		return CheckInitResult{}, err
	}
	if !resp.OK {
		return CheckInitResult{}, fmt.Errorf("daemon error: %s", resp.Error)
	}
	var result CheckInitResult
	if err := json.Unmarshal(resp.Payload, &result); err != nil {
		return CheckInitResult{}, fmt.Errorf("unmarshal check-init response: %w", err)
	}
	return result, nil
}

// RunInit sends the wizard's configuration to the daemon to write config.toml.
func (a *App) RunInit(cfg InitConfig) error {
	resp, err := a.call("init", cfg)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}
	return nil
}

// DetectEnvironment probes the local system for installed IDEs and dev tools.
// This runs locally without needing the daemon.
func (a *App) DetectEnvironment() DetectedEnvironment {
	return detectEnvironment()
}
