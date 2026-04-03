package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/wambozi/sigil/internal/socket"
)

// serviceHealth describes the health of a single backend service.
type serviceHealth struct {
	Name    string `json:"name"`
	Status  string `json:"status"`  // "ok", "degraded", "down", "disabled"
	Message string `json:"message"` // human-readable explanation
	Fix     string `json:"fix"`     // actionable fix suggestion
}

// registerHealthHandler adds the health socket method.
func registerHealthHandler(srv *socket.Server, cfg daemonConfig) {
	srv.Handle("health", func(ctx context.Context, _ socket.Request) socket.Response {
		services := []serviceHealth{
			checkDaemon(cfg),
			checkLLM(cfg),
			checkML(cfg),
		}

		payload, _ := json.Marshal(map[string]any{
			"services": services,
		})
		return socket.Response{OK: true, Payload: payload}
	})
}

func checkDaemon(cfg daemonConfig) serviceHealth {
	return serviceHealth{
		Name:    "Daemon",
		Status:  "ok",
		Message: fmt.Sprintf("Running, RSS %dMB", 0), // filled by caller if needed
	}
}

func checkLLM(cfg daemonConfig) serviceHealth {
	mode := cfg.fileCfg.Inference.Mode
	localEnabled := cfg.fileCfg.Inference.Local.Enabled
	cloudEnabled := cfg.fileCfg.Inference.Cloud.Enabled

	if mode == "" {
		mode = "localfirst"
	}

	// If both are disabled, inference is off.
	if !localEnabled && !cloudEnabled {
		return serviceHealth{
			Name:    "LLM Inference",
			Status:  "disabled",
			Message: "No inference backend enabled",
			Fix:     "Enable local or cloud inference in Settings > LLM Inference",
		}
	}

	// Check local if enabled.
	if localEnabled {
		url := cfg.fileCfg.Inference.Local.ServerURL
		if url == "" {
			url = "http://127.0.0.1:11434"
		}
		if err := pingHTTP(url + "/health"); err != nil {
			if !cloudEnabled {
				return serviceHealth{
					Name:    "LLM Inference",
					Status:  "down",
					Message: fmt.Sprintf("Local server unreachable at %s", url),
					Fix:     "Start llama-server or enable cloud fallback in Settings",
				}
			}
			// Local down but cloud available.
			if cloudEnabled {
				if cfg.fileCfg.Inference.Cloud.APIKey == "" && cfg.fileCfg.Cloud.APIKey == "" {
					return serviceHealth{
						Name:    "LLM Inference",
						Status:  "degraded",
						Message: "Local server down, cloud has no credentials",
						Fix:     "Start llama-server or sign in to Sigil Cloud",
					}
				}
				return serviceHealth{
					Name:    "LLM Inference",
					Status:  "degraded",
					Message: "Local server down, using cloud fallback",
				}
			}
		}
		return serviceHealth{
			Name:    "LLM Inference",
			Status:  "ok",
			Message: fmt.Sprintf("Local server reachable (%s)", url),
		}
	}

	// Cloud only.
	if cfg.fileCfg.Inference.Cloud.APIKey == "" && cfg.fileCfg.Cloud.APIKey == "" {
		return serviceHealth{
			Name:    "LLM Inference",
			Status:  "down",
			Message: "Cloud mode enabled but no credentials",
			Fix:     "Sign in to Sigil Cloud in Settings",
		}
	}
	return serviceHealth{
		Name:    "LLM Inference",
		Status:  "ok",
		Message: "Cloud inference configured",
	}
}

func checkML(cfg daemonConfig) serviceHealth {
	mode := cfg.fileCfg.ML.Mode
	if mode == "disabled" || mode == "" {
		return serviceHealth{
			Name:    "ML Pipeline",
			Status:  "disabled",
			Message: "ML predictions disabled",
			Fix:     "Enable ML in Settings > ML Pipeline",
		}
	}

	localEnabled := cfg.fileCfg.ML.Local.Enabled
	cloudEnabled := cfg.fileCfg.ML.Cloud.Enabled

	if localEnabled {
		url := cfg.fileCfg.ML.Local.ServerURL
		if url == "" {
			url = "http://127.0.0.1:7774"
		}
		if err := pingHTTP(url + "/health"); err != nil {
			if !cloudEnabled {
				return serviceHealth{
					Name:    "ML Pipeline",
					Status:  "down",
					Message: fmt.Sprintf("sigil-ml unreachable at %s", url),
					Fix:     "Start sigil-ml or enable cloud ML in Settings",
				}
			}
			return serviceHealth{
				Name:    "ML Pipeline",
				Status:  "degraded",
				Message: "Local sigil-ml down, using cloud fallback",
			}
		}
		return serviceHealth{
			Name:    "ML Pipeline",
			Status:  "ok",
			Message: "sigil-ml reachable",
		}
	}

	if cloudEnabled {
		if cfg.fileCfg.ML.Cloud.APIKey == "" && cfg.fileCfg.Cloud.APIKey == "" {
			return serviceHealth{
				Name:    "ML Pipeline",
				Status:  "down",
				Message: "Cloud ML enabled but no credentials",
				Fix:     "Sign in to Sigil Cloud in Settings",
			}
		}
		return serviceHealth{
			Name:    "ML Pipeline",
			Status:  "ok",
			Message: "Cloud ML configured",
		}
	}

	return serviceHealth{
		Name:    "ML Pipeline",
		Status:  "disabled",
		Message: "No ML backend enabled",
		Fix:     "Enable local or cloud ML in Settings > ML Pipeline",
	}
}

// pingHTTP does a quick GET to check if a service is reachable.
func pingHTTP(url string) error {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
