package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/wambozi/sigil/internal/config"
	"github.com/wambozi/sigil/internal/socket"
)

// registerNotificationHandlers adds DND and category mute socket methods.
func registerNotificationHandlers(srv *socket.Server, cfg daemonConfig) {
	// dnd-schedule — get/set Do Not Disturb schedule.
	srv.Handle("dnd-schedule", func(_ context.Context, req socket.Request) socket.Response {
		// GET: no payload or empty payload.
		if req.Payload == nil || string(req.Payload) == "{}" || string(req.Payload) == "null" {
			schedule := cfg.fileCfg.Notifier.DND
			payload, _ := json.Marshal(map[string]any{
				"enabled": schedule.Enabled,
				"start":   schedule.Start,
				"end":     schedule.End,
				"days":    schedule.Days,
			})
			return socket.Response{OK: true, Payload: payload}
		}

		// SET: update DND schedule in config.
		var p struct {
			Enabled bool     `json:"enabled"`
			Start   string   `json:"start"`
			End     string   `json:"end"`
			Days    []string `json:"days"`
		}
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return socket.Response{Error: fmt.Sprintf("invalid payload: %v", err)}
		}

		cfg.fileCfg.Notifier.DND = config.DNDSchedule{
			Enabled: p.Enabled,
			Start:   p.Start,
			End:     p.End,
			Days:    p.Days,
		}

		if err := writeConfigFile(cfg.fileCfg); err != nil {
			return socket.Response{Error: fmt.Sprintf("write config: %v", err)}
		}

		payload, _ := json.Marshal(map[string]any{"ok": true})
		return socket.Response{OK: true, Payload: payload}
	})

	// mute-category — get/set muted suggestion categories.
	srv.Handle("mute-category", func(_ context.Context, req socket.Request) socket.Response {
		if req.Payload == nil || string(req.Payload) == "{}" || string(req.Payload) == "null" {
			payload, _ := json.Marshal(map[string]any{
				"muted": cfg.fileCfg.Notifier.MutedCategories,
			})
			return socket.Response{OK: true, Payload: payload}
		}

		var p struct {
			Muted []string `json:"muted"`
		}
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return socket.Response{Error: fmt.Sprintf("invalid payload: %v", err)}
		}

		cfg.fileCfg.Notifier.MutedCategories = p.Muted

		if err := writeConfigFile(cfg.fileCfg); err != nil {
			return socket.Response{Error: fmt.Sprintf("write config: %v", err)}
		}

		payload, _ := json.Marshal(map[string]any{"ok": true})
		return socket.Response{OK: true, Payload: payload}
	})
}

// writeConfigFile serializes the config back to disk.
func writeConfigFile(cfg *config.Config) error {
	cfgPath := config.DefaultPath()
	data, err := config.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(cfgPath, data, 0o600)
}
