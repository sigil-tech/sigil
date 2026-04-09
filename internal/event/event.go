// Package event defines the shared event types flowing through sigild.
// All collector sources emit Events; the store persists them; the analyzer
// reads them.  Keeping these types in their own package breaks import cycles.
package event

import "time"

// Kind identifies which subsystem produced an event.
type Kind string

const (
	KindFile         Kind = "file"          // inotify / fsnotify
	KindProcess      Kind = "process"       // /proc polling
	KindHyprland     Kind = "hyprland"      // Hyprland compositor IPC
	KindGit          Kind = "git"           // git repository activity
	KindTerminal     Kind = "terminal"      // shell command (pushed via socket ingest)
	KindAI           Kind = "ai"            // AI interaction (query, suggestion)
	KindClipboard    Kind = "clipboard"     // clipboard changes
	KindAppState     Kind = "app_state"     // frontmost app internal state
	KindIdle         Kind = "idle"          // active/idle/lock transitions
	KindTyping       Kind = "typing"        // keystroke rate (not keys)
	KindPointer      Kind = "pointer"       // mouse/trackpad aggregate metrics
	KindDesktop      Kind = "desktop"       // virtual desktop switches
	KindDisplay      Kind = "display"       // monitor connect/disconnect
	KindAudio        Kind = "audio"         // headphones/mic state
	KindPower        Kind = "power"         // AC/battery transitions
	KindNetwork      Kind = "network"       // connection type changes
	KindFocusMode    Kind = "focus_mode"    // OS DND/Focus state
	KindAppLifecycle Kind = "app_lifecycle" // app launch/quit/crash
	KindScreenshot   Kind = "screenshot"    // screenshot captured
	KindDownload     Kind = "download"      // file downloaded
	KindCalendar     Kind = "calendar"      // meeting boundaries
	KindBrowser      Kind = "browser"       // browser page context
)

// Event is the atomic unit of observation.  Payload is kept as a generic map
// so that each source can attach whatever fields make sense without requiring
// a separate type per source.  The store serialises Payload as JSON.
type Event struct {
	ID        int64          `json:"id,omitempty"`
	Kind      Kind           `json:"kind"`
	Source    string         `json:"source"` // e.g. "files", "hyprland"
	Payload   map[string]any `json:"payload"`
	Timestamp time.Time      `json:"timestamp"`
}

// AIInteraction records a single AI mode query or suggestion acceptance.
// These are stored in their own table so fleet metrics can aggregate them
// without touching raw event payloads.
type AIInteraction struct {
	ID            int64     `json:"id,omitempty"`
	QueryText     string    `json:"query_text,omitempty"`
	QueryCategory string    `json:"query_category,omitempty"` // "code_gen", "debug", "docs", …
	Routing       string    `json:"routing"`                  // "local" | "cloud"
	LatencyMS     int64     `json:"latency_ms"`
	Accepted      bool      `json:"accepted,omitempty"` // for suggestion events
	Timestamp     time.Time `json:"timestamp"`
}
