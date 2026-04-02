package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

// CloudStatusResult holds the cloud connection state.
type CloudStatusResult struct {
	Connected         bool   `json:"connected"`
	Tier              string `json:"tier"`
	SyncState         string `json:"sync_state"`
	MLPredictionsUsed int    `json:"ml_predictions_used"`
	LLMTokensUsed     int    `json:"llm_tokens_used"`
	LLMTokensLimit    int    `json:"llm_tokens_limit"`
}

// GetCloudStatus returns the current cloud connection state.
func (a *App) GetCloudStatus() (CloudStatusResult, error) {
	resp, err := a.call("cloud-status", nil)
	if err != nil {
		return CloudStatusResult{}, err
	}
	if !resp.OK {
		return CloudStatusResult{}, fmt.Errorf("daemon error: %s", resp.Error)
	}
	var result CloudStatusResult
	if err := json.Unmarshal(resp.Payload, &result); err != nil {
		return CloudStatusResult{}, fmt.Errorf("unmarshal cloud status: %w", err)
	}
	return result, nil
}

// CloudSignIn starts the OAuth flow: opens browser, waits for callback token,
// sends it to the daemon's cloud-auth handler.
func (a *App) CloudSignIn() error {
	// Start a local HTTP server on a random port.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("start oauth listener: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	tokenCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		if token == "" {
			http.Error(w, "missing token", http.StatusBadRequest)
			return
		}
		fmt.Fprint(w, "<html><body><h2>Signed in to Sigil Cloud</h2><p>You can close this tab.</p></body></html>")
		tokenCh <- token
	})

	srv := &http.Server{Handler: mux}
	// OAuth callback server — runs until token received or timeout.
	go func() {
		if serveErr := srv.Serve(listener); serveErr != nil && serveErr != http.ErrServerClosed {
			errCh <- serveErr
		}
	}()

	// Open the browser to the OAuth page.
	authURL := fmt.Sprintf("https://app.sigilos.io/oauth/desktop?redirect_port=%d", port)
	wailsrt.BrowserOpenURL(a.ctx, authURL)

	// Wait for token or timeout.
	var token string
	select {
	case token = <-tokenCh:
	case err := <-errCh:
		srv.Close()
		return fmt.Errorf("oauth server: %w", err)
	case <-time.After(5 * time.Minute):
		srv.Close()
		return fmt.Errorf("oauth timed out after 5 minutes")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	srv.Shutdown(shutdownCtx)

	// Send the token to the daemon.
	resp, err := a.call("cloud-auth", map[string]any{"api_key": token})
	if err != nil {
		return fmt.Errorf("cloud auth: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}
	return nil
}

// CloudSignOut clears cloud credentials.
func (a *App) CloudSignOut() error {
	resp, err := a.call("cloud-signout", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}
	return nil
}
