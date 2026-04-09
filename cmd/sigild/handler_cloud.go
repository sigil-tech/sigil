package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/sigil-tech/sigil/internal/config"
	"github.com/sigil-tech/sigil/internal/socket"
)

// registerCloudHandlers adds cloud-auth and cloud-status socket methods.
func registerCloudHandlers(srv *socket.Server, cfg daemonConfig) {
	srv.Handle("cloud-auth", func(_ context.Context, req socket.Request) socket.Response {
		var p struct {
			APIKey string `json:"api_key"`
		}
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return socket.Response{Error: fmt.Sprintf("invalid payload: %v", err)}
		}
		if p.APIKey == "" {
			return socket.Response{Error: "api_key is required"}
		}

		if err := writeCloudConfig(p.APIKey); err != nil {
			return socket.Response{Error: fmt.Sprintf("write cloud config: %v", err)}
		}

		tier := "free"
		if len(p.APIKey) > 20 {
			tier = "pro"
		}

		payload, _ := json.Marshal(map[string]any{
			"ok":   true,
			"tier": tier,
		})
		return socket.Response{OK: true, Payload: payload}
	})

	srv.Handle("cloud-status", func(_ context.Context, _ socket.Request) socket.Response {
		fileCfg := cfg.fileCfg
		connected := fileCfg.Cloud.APIKey != ""
		tier := fileCfg.Cloud.Tier
		if tier == "" {
			tier = "free"
		}

		syncState := "disabled"
		if fileCfg.CloudSync.IsEnabled() {
			syncState = "active"
		}

		payload, _ := json.Marshal(map[string]any{
			"connected":           connected,
			"tier":                tier,
			"sync_state":          syncState,
			"ml_predictions_used": 0,
			"llm_tokens_used":     0,
			"llm_tokens_limit":    0,
		})
		return socket.Response{OK: true, Payload: payload}
	})

	srv.Handle("cloud-signout", func(_ context.Context, _ socket.Request) socket.Response {
		if err := writeCloudConfig(""); err != nil {
			return socket.Response{Error: fmt.Sprintf("clear cloud config: %v", err)}
		}
		payload, _ := json.Marshal(map[string]any{"ok": true})
		return socket.Response{OK: true, Payload: payload}
	})
}

// writeCloudConfig updates the cloud API key in the config file.
func writeCloudConfig(apiKey string) error {
	cfgPath := config.DefaultPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	cfg.Cloud.APIKey = apiKey
	if apiKey == "" {
		cfg.Cloud.Tier = "free"
	}

	data, err := config.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(cfgPath, data, 0o600)
}
