// Command sigilctl is the command-line interface for interacting with a
// running sigild daemon.  It communicates over the Unix domain socket and
// also supports direct SQLite queries when the daemon is not running.
//
// Usage:
//
//	sigilctl status                        — daemon health check
//	sigilctl events [-n N] [-offline]      — list recent events
//	sigilctl tail                          — poll and print events continuously
//	sigilctl files                         — top files by edit count today
//	sigilctl commands                      — command frequency table today
//	sigilctl patterns                      — detected patterns with confidence
//	sigilctl suggestions                   — suggestion history with status
//	sigilctl summary                       — trigger an immediate analysis cycle
//	sigilctl level                         — show current notification level
//	sigilctl level N                       — set notification level (0-4)
//	sigilctl feedback <id> accept|dismiss  — respond to a suggestion
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/sigil-tech/sigil/internal/config"
	"github.com/sigil-tech/sigil/internal/event"
	"github.com/sigil-tech/sigil/internal/inference"
	"github.com/sigil-tech/sigil/internal/plugin"
	"github.com/sigil-tech/sigil/internal/socket"
	"github.com/sigil-tech/sigil/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "sigilctl:", err)
		os.Exit(1)
	}
}

func run() error {
	socketPath := flag.String("socket", defaultSocketPath(), "sigild Unix socket path")
	dbPath := flag.String("db", defaultDBPath(), "SQLite database path (used when daemon is offline)")
	flag.Parse()

	if flag.NArg() == 0 {
		printUsage()
		return nil
	}

	cmd, args := flag.Arg(0), flag.Args()[1:]

	switch cmd {
	case "status":
		return cmdStatus(*socketPath)
	case "events":
		return cmdEvents(*socketPath, *dbPath, args)
	case "tail":
		return cmdTail(*socketPath, *dbPath)
	case "tail-suggestions":
		return cmdTailSuggestions(*socketPath)
	case "files":
		return cmdFiles(*socketPath)
	case "commands":
		return cmdCommands(*socketPath)
	case "patterns":
		return cmdPatterns(*socketPath)
	case "suggestions":
		return cmdSuggestions(*socketPath)
	case "summary":
		return cmdSummary(*socketPath)
	case "level":
		return cmdLevel(*socketPath, args)
	case "feedback":
		return cmdFeedback(*socketPath, args)
	case "config":
		return cmdConfig(*socketPath)
	case "actions":
		return cmdActions(*socketPath)
	case "purge":
		return cmdPurge(*dbPath)
	case "export":
		return cmdExport(*dbPath)
	case "model":
		return cmdModel(args)
	case "sessions":
		return cmdSessions(*socketPath)
	case "fleet":
		return cmdFleet(*socketPath, args)
	case "credential":
		return cmdCredential(*socketPath, args)
	case "task":
		return cmdTask(*socketPath, args)
	case "day":
		return cmdDay(*socketPath)
	case "ml":
		return cmdML(*socketPath, args)
	case "plugin":
		return cmdPlugin(args)
	case "sync":
		return cmdSync(*socketPath, args)
	case "ask":
		return cmdAsk(*socketPath, args)
	case "correct":
		return cmdCorrect(*socketPath, args)
	case "stop":
		return cmdStop(*socketPath)
	case "start":
		return cmdStart(*socketPath)
	case "auth":
		return cmdAuth(*socketPath, args)
	case "vm":
		return cmdVM(*socketPath, args)
	case "merge":
		return cmdMerge(*socketPath, args)
	case "corpus":
		return cmdCorpus(*socketPath, args)
	case "audit":
		return cmdAudit(*socketPath, args)
	default:
		return fmt.Errorf("unknown command %q — run sigilctl -help", cmd)
	}
}

// --- Commands ---------------------------------------------------------------

func cmdStatus(socketPath string) error {
	resp, err := call(socketPath, "status", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var payload map[string]any
	_ = json.Unmarshal(resp.Payload, &payload)

	fmt.Printf("sigild  status=%v  version=%v  rss_mb=%v\n",
		payload["status"], payload["version"], payload["rss_mb"])
	return nil
}

func cmdEvents(socketPath, dbPath string, args []string) error {
	fs := flag.NewFlagSet("events", flag.ContinueOnError)
	n := fs.Int("n", 20, "number of events to show")
	offline := fs.Bool("offline", false, "read directly from SQLite (bypasses daemon)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *offline {
		return eventsFromDB(dbPath, *n)
	}

	resp, err := call(socketPath, "events", nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: daemon unreachable, falling back to direct DB read")
		return eventsFromDB(dbPath, *n)
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	return printEventsJSON(resp.Payload, *n)
}

// cmdTail polls the events endpoint every two seconds and prints new entries.
// Phase 2 will replace this with a proper push subscription over the socket.
func cmdTail(socketPath, dbPath string) error {
	fmt.Fprintln(os.Stderr, "sigilctl tail: polling every 2s (Ctrl-C to stop)...")
	for {
		_ = cmdEvents(socketPath, dbPath, nil)
		time.Sleep(2 * time.Second)
	}
}

// cmdTailSuggestions polls the suggestions endpoint and prints only new ones.
func cmdTailSuggestions(socketPath string) error {
	fmt.Fprintln(os.Stderr, "sigilctl tail-suggestions: polling every 5s (Ctrl-C to stop)...")
	seen := make(map[int64]bool)
	first := true
	for {
		resp, err := call(socketPath, "suggestions", nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: %v\n", err)
			time.Sleep(5 * time.Second)
			continue
		}
		if !resp.OK {
			time.Sleep(5 * time.Second)
			continue
		}

		var suggestions []struct {
			ID         int64   `json:"id"`
			Status     string  `json:"status"`
			Confidence float64 `json:"confidence"`
			Title      string  `json:"title"`
			Body       string  `json:"body"`
		}
		if err := json.Unmarshal(resp.Payload, &suggestions); err != nil {
			time.Sleep(5 * time.Second)
			continue
		}

		for _, s := range suggestions {
			if seen[s.ID] {
				continue
			}
			seen[s.ID] = true
			if first {
				// On first poll, mark existing suggestions as seen without printing
				continue
			}
			ts := time.Now().Format("15:04:05")
			fmt.Printf("[%s] #%d %s (%.2f) — %s\n", ts, s.ID, s.Status, s.Confidence, s.Title)
			if s.Body != "" {
				fmt.Printf("         %s\n", s.Body)
			}
			fmt.Println()
		}
		first = false
		time.Sleep(5 * time.Second)
	}
}

// cmdFiles prints the top edited files from the last 24 hours.
func cmdFiles(socketPath string) error {
	resp, err := call(socketPath, "files", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var files []struct {
		Path  string `json:"Path"`
		Count int64  `json:"Count"`
	}
	if err := json.Unmarshal(resp.Payload, &files); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "FILE\tEDITS")
	for _, f := range files {
		fmt.Fprintf(w, "%s\t%d\n", f.Path, f.Count)
	}
	return w.Flush()
}

// cmdCommands prints the command frequency table for the last 24 hours.
func cmdCommands(socketPath string) error {
	resp, err := call(socketPath, "commands", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var rows []struct {
		Cmd          string `json:"cmd"`
		Count        int    `json:"count"`
		LastExitCode int    `json:"last_exit_code"`
	}
	if err := json.Unmarshal(resp.Payload, &rows); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "COMMAND\tCOUNT\tLAST EXIT")
	for _, r := range rows {
		fmt.Fprintf(w, "%s\t%d\t%d\n", r.Cmd, r.Count, r.LastExitCode)
	}
	return w.Flush()
}

// cmdPatterns prints detected patterns with their confidence scores.
func cmdPatterns(socketPath string) error {
	resp, err := call(socketPath, "patterns", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var patterns []struct {
		ID         int64   `json:"id"`
		Category   string  `json:"category"`
		Confidence float64 `json:"confidence"`
		Title      string  `json:"title"`
		Body       string  `json:"body"`
	}
	if err := json.Unmarshal(resp.Payload, &patterns); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if len(patterns) == 0 {
		fmt.Println("No patterns detected yet.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PATTERN\tCONFIDENCE\tBODY")
	for _, p := range patterns {
		fmt.Fprintf(w, "%s\t%.2f\t%s\n", p.Title, p.Confidence, p.Body)
	}
	return w.Flush()
}

// cmdSuggestions prints the suggestion history with lifecycle status.
func cmdSuggestions(socketPath string) error {
	resp, err := call(socketPath, "suggestions", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var suggestions []struct {
		ID         int64   `json:"id"`
		Status     string  `json:"status"`
		Confidence float64 `json:"confidence"`
		Title      string  `json:"title"`
		Body       string  `json:"body"`
	}
	if err := json.Unmarshal(resp.Payload, &suggestions); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if len(suggestions) == 0 {
		fmt.Println("No suggestions recorded yet.")
		return nil
	}

	for _, s := range suggestions {
		fmt.Printf("[%d] %s (%.2f) — %s\n", s.ID, s.Status, s.Confidence, s.Title)
		if s.Body != "" {
			fmt.Printf("    %s\n", s.Body)
		}
		fmt.Println()
	}
	return nil
}

// cmdSummary triggers an immediate analysis cycle in the daemon.
func cmdSummary(socketPath string) error {
	resp, err := call(socketPath, "trigger-summary", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var payload map[string]any
	_ = json.Unmarshal(resp.Payload, &payload)
	fmt.Printf("sigild: %v\n", payload["message"])
	return nil
}

// cmdLevel shows or sets the notifier level.
//
// With no args it reads the current level from the status endpoint.
// With a single numeric arg it sets the level via set-level.
func cmdLevel(socketPath string, args []string) error {
	if len(args) == 0 {
		return showLevel(socketPath)
	}
	return setLevel(socketPath, args[0])
}

func showLevel(socketPath string) error {
	resp, err := call(socketPath, "status", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var payload map[string]any
	_ = json.Unmarshal(resp.Payload, &payload)

	level, _ := payload["notifier_level"].(float64)
	fmt.Printf("Notification level: %d (%s)\n", int(level), levelName(int(level)))
	return nil
}

func setLevel(socketPath, arg string) error {
	n, err := strconv.Atoi(arg)
	if err != nil || n < 0 || n > 4 {
		return fmt.Errorf("level must be an integer 0-4, got %q", arg)
	}

	resp, err := call(socketPath, "set-level", map[string]any{"level": n})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	fmt.Printf("Notification level set to %d (%s)\n", n, levelName(n))
	return nil
}

// levelName returns the human-readable name for a notifier level integer.
func levelName(n int) string {
	switch n {
	case 0:
		return "silent"
	case 1:
		return "digest"
	case 2:
		return "ambient"
	case 3:
		return "conversational"
	case 4:
		return "autonomous"
	default:
		return "unknown"
	}
}

// cmdFeedback records an explicit accept or dismiss outcome for a suggestion.
// Usage: sigilctl feedback <id> accept|dismiss
func cmdFeedback(socketPath string, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: sigilctl feedback <id> accept|dismiss")
	}

	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || id <= 0 {
		return fmt.Errorf("id must be a positive integer, got %q", args[0])
	}

	outcome := args[1]
	switch outcome {
	case "accept":
		outcome = "accepted"
	case "dismiss":
		outcome = "dismissed"
	case "accepted", "dismissed":
		// already canonical
	default:
		return fmt.Errorf("outcome must be accept or dismiss, got %q", args[1])
	}

	resp, err := call(socketPath, "feedback", map[string]any{
		"suggestion_id": id,
		"outcome":       outcome,
	})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	fmt.Printf("Suggestion %d marked %s.\n", id, outcome)
	return nil
}

// --- Socket helpers ---------------------------------------------------------

func call(socketPath, method string, payload any) (socket.Response, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return socket.Response{}, fmt.Errorf("connect to daemon at %s: %w", socketPath, err)
	}
	defer conn.Close()

	req := socket.Request{Method: method}
	if payload != nil {
		req.Payload, _ = json.Marshal(payload)
	}

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return socket.Response{}, fmt.Errorf("send request: %w", err)
	}

	var resp socket.Response
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&resp); err != nil {
		return socket.Response{}, fmt.Errorf("read response: %w", err)
	}
	return resp, nil
}

// --- Store helpers ----------------------------------------------------------

func eventsFromDB(dbPath string, n int) error {
	db, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer db.Close()

	ctx := context.Background()
	events, err := db.QueryEvents(ctx, "", n)
	if err != nil {
		return err
	}
	return printEvents(events)
}

func printEventsJSON(raw json.RawMessage, n int) error {
	var events []map[string]any
	if err := json.Unmarshal(raw, &events); err != nil {
		return err
	}
	if len(events) > n {
		events = events[:n]
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tKIND\tSOURCE\tTIMESTAMP")
	for _, e := range events {
		fmt.Fprintf(w, "%v\t%v\t%v\t%v\n",
			e["id"], e["kind"], e["source"], e["timestamp"])
	}
	return w.Flush()
}

func printEvents(events []event.Event) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tKIND\tSOURCE\tTIMESTAMP")
	for _, e := range events {
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\n",
			e.ID, e.Kind, e.Source, e.Timestamp.Format(time.RFC3339))
	}
	return w.Flush()
}

// --- Path helpers -----------------------------------------------------------

func defaultSocketPath() string {
	if goruntime.GOOS == "windows" {
		appdata := os.Getenv("LOCALAPPDATA")
		if appdata == "" {
			appdata = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local")
		}
		return filepath.Join(appdata, "sigil", "sigild.sock")
	}
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "sigild.sock")
	}
	if goruntime.GOOS == "darwin" {
		return filepath.Join(os.TempDir(), "sigild.sock")
	}
	return fmt.Sprintf("/run/user/%d/sigild.sock", currentUID())
}

func defaultDBPath() string {
	if goruntime.GOOS == "windows" {
		appdata := os.Getenv("LOCALAPPDATA")
		if appdata == "" {
			appdata = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local")
		}
		return filepath.Join(appdata, "sigil", "sigild", "data.db")
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		h, _ := os.UserHomeDir()
		base = filepath.Join(h, ".local", "share")
	}
	return filepath.Join(base, "sigild", "data.db")
}

// cmdSessions prints terminal session summaries from the last 24 hours.
func cmdSessions(socketPath string) error {
	resp, err := call(socketPath, "sessions", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var sessions []struct {
		SessionID string `json:"session_id"`
		CmdCount  int    `json:"cmd_count"`
		FirstTS   int64  `json:"first_ts"`
		LastTS    int64  `json:"last_ts"`
		LastCwd   string `json:"last_cwd"`
	}
	if err := json.Unmarshal(resp.Payload, &sessions); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if len(sessions) == 0 {
		fmt.Println("No terminal sessions in the last 24 hours.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SESSION\tCOMMANDS\tFIRST\tLAST\tCWD")
	for _, s := range sessions {
		first := time.Unix(s.FirstTS, 0).Format("15:04")
		last := time.Unix(s.LastTS, 0).Format("15:04")
		fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\n", s.SessionID, s.CmdCount, first, last, s.LastCwd)
	}
	return w.Flush()
}

// cmdActions prints recent undoable actions.
func cmdActions(socketPath string) error {
	resp, err := call(socketPath, "actions", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var actions []struct {
		ID          string `json:"id"`
		Description string `json:"description"`
		UndoCmd     string `json:"undo_cmd"`
		ExpiresAt   string `json:"expires_at"`
	}
	if err := json.Unmarshal(resp.Payload, &actions); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if len(actions) == 0 {
		fmt.Println("No undoable actions.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tDESCRIPTION\tUNDO CMD\tEXPIRES")
	for _, a := range actions {
		undoLabel := a.UndoCmd
		if undoLabel == "" {
			undoLabel = "(irreversible)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", a.ID, a.Description, undoLabel, a.ExpiresAt)
	}
	return w.Flush()
}

// cmdConfig fetches the resolved daemon configuration and prints it as a
// key = value table.
func cmdConfig(socketPath string) error {
	resp, err := call(socketPath, "config", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var payload map[string]any
	if err := json.Unmarshal(resp.Payload, &payload); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	order := []string{
		"db_path", "socket_path",
		"inference_mode",
		"watch_paths", "repo_paths",
		"analyze_every", "notifier_level", "log_level", "digest_time",
		"raw_event_days",
	}
	for _, k := range order {
		v, ok := payload[k]
		if !ok {
			continue
		}
		fmt.Fprintf(w, "%s\t= %v\n", k, v)
	}
	return w.Flush()
}

// cmdPurge prompts for confirmation and deletes all local data directly from
// SQLite (works without a running daemon).
func cmdPurge(dbPath string) error {
	fmt.Fprint(os.Stdout, "This will delete all local data. Type 'yes' to confirm: ")
	var answer string
	if _, err := fmt.Fscan(os.Stdin, &answer); err != nil {
		return fmt.Errorf("read confirmation: %w", err)
	}
	if answer != "yes" {
		fmt.Println("Aborted.")
		return nil
	}

	db, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}

	if err := db.Purge(); err != nil {
		return fmt.Errorf("purge: %w", err)
	}
	fmt.Println("All local data deleted.")
	return nil
}

// cmdExport writes all events and suggestions as newline-delimited JSON to
// stdout.  Works without a running daemon (direct DB access).
func cmdExport(dbPath string) error {
	db, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer db.Close()

	return db.Export(os.Stdout)
}

// cmdModel handles model subcommands: pull, list, path.
func cmdModel(args []string) error {
	if len(args) == 0 {
		fmt.Println("Usage: sigilctl model <pull|list|path> [name]")
		return nil
	}
	switch args[0] {
	case "pull":
		return cmdModelPull(args[1:])
	case "list":
		return cmdModelList()
	case "path":
		return cmdModelPath(args[1:])
	default:
		return fmt.Errorf("unknown model command %q — use pull, list, or path", args[0])
	}
}

func cmdModelPull(args []string) error {
	name := inference.DefaultModel
	if len(args) > 0 {
		name = args[0]
	}
	_, err := inference.EnsureModel(context.Background(), name, os.Stdout)
	return err
}

func cmdModelList() error {
	cached := inference.ListCachedModels()
	if len(cached) == 0 {
		fmt.Println("No cached models. Run 'sigilctl model pull' to download one.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSIZE (MB)\tPATH")
	for _, m := range cached {
		fmt.Fprintf(w, "%s\t%d\t%s\n", m.Name, m.Size/(1024*1024), m.Path)
	}
	return w.Flush()
}

func cmdModelPath(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: sigilctl model path <name>")
	}
	p := inference.ModelPath(args[0])
	if p == "" {
		return fmt.Errorf("model %q not found locally", args[0])
	}
	fmt.Println(p)
	return nil
}

// cmdFleet handles fleet subcommands: status, preview, opt-out.
func cmdFleet(socketPath string, args []string) error {
	if len(args) == 0 {
		fmt.Println("Usage: sigilctl fleet <status|preview|opt-out>")
		return nil
	}
	switch args[0] {
	case "status":
		return cmdFleetStatus(socketPath)
	case "preview":
		return cmdFleetPreview(socketPath)
	case "opt-out":
		return cmdFleetOptOut(socketPath)
	default:
		return fmt.Errorf("unknown fleet command %q — use status, preview, or opt-out", args[0])
	}
}

func cmdFleetStatus(socketPath string) error {
	resp, err := call(socketPath, "fleet-preview", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		fmt.Println("Fleet reporting: disabled")
		return nil
	}
	fmt.Println("Fleet reporting: enabled")
	var report map[string]any
	_ = json.Unmarshal(resp.Payload, &report)
	fmt.Printf("  Node ID: %v\n", report["node_id"])
	fmt.Printf("  Adoption tier: %v\n", report["adoption_tier"])
	fmt.Printf("  Total events: %v\n", report["total_events"])
	return nil
}

func cmdFleetPreview(socketPath string) error {
	resp, err := call(socketPath, "fleet-preview", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("fleet preview: %s", resp.Error)
	}
	var pretty json.RawMessage
	if err := json.Unmarshal(resp.Payload, &pretty); err != nil {
		return err
	}
	out, _ := json.MarshalIndent(pretty, "", "  ")
	fmt.Println(string(out))
	return nil
}

func cmdFleetOptOut(socketPath string) error {
	resp, err := call(socketPath, "fleet-opt-out", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("fleet opt-out: %s", resp.Error)
	}
	fmt.Println("Fleet reporting disabled. Pending queue cleared.")
	return nil
}

// --- Sync commands ----------------------------------------------------------

func cmdSync(socketPath string, args []string) error {
	if len(args) == 0 {
		fmt.Println("Usage: sigilctl sync <status|pause|resume>")
		return nil
	}
	switch args[0] {
	case "status":
		return cmdSyncStatus(socketPath)
	case "pause":
		return cmdSyncPause(socketPath)
	case "resume":
		return cmdSyncResume(socketPath)
	default:
		return fmt.Errorf("unknown sync command %q — use status, pause, or resume", args[0])
	}
}

func cmdSyncStatus(socketPath string) error {
	resp, err := call(socketPath, "sync-status", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("sync status: %s", resp.Error)
	}
	var status map[string]any
	_ = json.Unmarshal(resp.Payload, &status)

	enabled, _ := status["enabled"].(bool)
	if !enabled {
		fmt.Println("Sync agent: disabled")
		return nil
	}

	paused, _ := status["paused"].(bool)
	state := "running"
	if paused {
		state = "paused"
	}
	fmt.Printf("Sync agent: %s\n", state)
	fmt.Printf("  API URL:  %v\n", status["api_url"])
	fmt.Printf("  Interval: %v\n", status["interval"])

	if cursors, ok := status["cursors"].(map[string]any); ok {
		fmt.Println("  Cursors:")
		for table, cur := range cursors {
			fmt.Printf("    %-16s %v\n", table, cur)
		}
	}
	return nil
}

func cmdSyncPause(socketPath string) error {
	resp, err := call(socketPath, "sync-pause", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("sync pause: %s", resp.Error)
	}
	fmt.Println("Sync agent paused.")
	return nil
}

func cmdSyncResume(socketPath string) error {
	resp, err := call(socketPath, "sync-resume", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("sync resume: %s", resp.Error)
	}
	fmt.Println("Sync agent resumed.")
	return nil
}

// cmdCredential dispatches credential subcommands: add, list, revoke.
func cmdCredential(socketPath string, args []string) error {
	if len(args) == 0 {
		fmt.Println("Usage: sigilctl credential <add|list|revoke> [name]")
		return nil
	}
	switch args[0] {
	case "add":
		return cmdCredentialAdd(socketPath, args[1:])
	case "list":
		return cmdCredentialList(socketPath)
	case "revoke":
		return cmdCredentialRevoke(socketPath, args[1:])
	default:
		return fmt.Errorf("unknown credential command %q — use add, list, or revoke", args[0])
	}
}

func cmdCredentialAdd(socketPath string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: sigilctl credential add <name>")
	}
	id := args[0]

	resp, err := call(socketPath, "credential.add", map[string]any{"id": id})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var bundle map[string]any
	if err := json.Unmarshal(resp.Payload, &bundle); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	// Pretty-print the credential bundle JSON.
	out, _ := json.MarshalIndent(bundle, "", "  ")
	fmt.Println(string(out))
	fmt.Fprintln(os.Stderr, "\n⚠  Keep this credential secret — it contains a plaintext bearer token.")
	return nil
}

func cmdCredentialList(socketPath string) error {
	resp, err := call(socketPath, "credential.list", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var payload struct {
		Credentials []struct {
			ID        string `json:"id"`
			CreatedAt string `json:"created_at"`
			Revoked   bool   `json:"revoked"`
		} `json:"credentials"`
	}
	if err := json.Unmarshal(resp.Payload, &payload); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if len(payload.Credentials) == 0 {
		fmt.Println("No credentials.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tCREATED\tREVOKED")
	for _, c := range payload.Credentials {
		revoked := "no"
		if c.Revoked {
			revoked = "yes"
		}
		// Parse and reformat the timestamp for readability.
		created := c.CreatedAt
		if t, err := time.Parse(time.RFC3339, c.CreatedAt); err == nil {
			created = t.UTC().Format("2006-01-02 15:04")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", c.ID, created, revoked)
	}
	return w.Flush()
}

func cmdCredentialRevoke(socketPath string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: sigilctl credential revoke <name>")
	}
	id := args[0]

	resp, err := call(socketPath, "credential.revoke", map[string]any{"id": id})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	fmt.Printf("Credential %q revoked. Delete the credential file on the remote host.\n", id)
	return nil
}

// cmdAuth dispatches auth subcommands: login, status, logout.
func cmdAuth(socketPath string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: sigilctl auth login|status|logout")
	}
	switch args[0] {
	case "login":
		return cmdAuthLogin()
	case "status":
		return cmdAuthStatus()
	case "logout":
		return cmdAuthLogout()
	default:
		return fmt.Errorf("unknown auth command: %s", args[0])
	}
}

// cmdAuthLogin prompts for an API key and writes it to the config file.
func cmdAuthLogin() error {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Enter your Sigil API key: ")
	key, readErr := reader.ReadString('\n')
	if readErr != nil {
		return fmt.Errorf("read API key: %w", readErr)
	}
	key = strings.TrimSpace(key)

	if key == "" {
		return fmt.Errorf("API key cannot be empty")
	}
	if !strings.HasPrefix(key, "sk-sigil-") {
		return fmt.Errorf("invalid API key format: must start with \"sk-sigil-\"")
	}

	cfgPath := config.DefaultPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		cfg = config.Defaults()
	}

	cfg.Cloud.APIKey = key

	return writeConfig(cfgPath, cfg)
}

// cmdAuthStatus reads the config and displays tier, API key validity, and enabled features.
func cmdAuthStatus() error {
	cfgPath := config.DefaultPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	tier := cfg.Cloud.Tier
	if tier == "" {
		tier = "free"
	}
	fmt.Printf("Tier:      %s\n", tier)

	if cfg.Cloud.APIKey != "" {
		key := cfg.Cloud.APIKey
		if len(key) >= 13 {
			fmt.Printf("API key:   %s...%s\n", key[:9], key[len(key)-4:])
		} else {
			fmt.Printf("API key:   (set, too short to display)\n")
		}
	} else {
		fmt.Println("API key:   (not set)")
	}

	if cfg.Cloud.OrgID != "" {
		fmt.Printf("Org ID:    %s\n", cfg.Cloud.OrgID)
	}

	fmt.Printf("Inference: %s\n", cfg.Inference.Mode)
	fmt.Printf("Sync:      %t\n", cfg.CloudSync.IsEnabled())

	return nil
}

// cmdAuthLogout removes the API key from the config file.
func cmdAuthLogout() error {
	cfgPath := config.DefaultPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if cfg.Cloud.APIKey == "" {
		fmt.Println("No API key configured.")
		return nil
	}

	cfg.Cloud.APIKey = ""

	if err := writeConfig(cfgPath, cfg); err != nil {
		return err
	}
	fmt.Println("API key removed.")
	return nil
}

// writeConfig marshals cfg to TOML and writes it atomically to path
// via a temp-file + rename pattern.
func writeConfig(path string, cfg *config.Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename config: %w", err)
	}
	fmt.Printf("Config written to %s\n", path)
	return nil
}

func printUsage() {
	fmt.Print(`sigilctl — Sigil OS daemon CLI

Commands:
  start                         Start the sigild daemon
  stop                          Stop the running sigild daemon
  status                        Show daemon health and version
  events [-n N] [-offline]      List the N most recent events (default 20)
  tail                          Poll and stream live events every 2s
  tail-suggestions              Stream new suggestions as they appear
  files                         Top files by edit count in the last 24h
  commands                      Command frequency table for the last 24h
  patterns                      Detected patterns with confidence scores
  sessions                      Terminal session summaries (last 24h)
  suggestions                   Suggestion history with lifecycle status
  summary                       Trigger an immediate analysis cycle
  level                         Show current notification level
  level N                       Set notification level (0=silent 1=digest
                                2=ambient 3=conversational 4=autonomous)
  feedback <id> accept|dismiss  Respond to a suggestion by ID
  actions                       Show recent undoable actions
  config                        Print resolved daemon configuration
  model pull [name]             Download a model (default: lfm2-24b-a2b-q4_k_m)
  model list                    List locally cached models
  model path <name>             Print path to a cached model
  fleet status                  Show fleet reporting opt-in status
  fleet preview                 Show what fleet data will be sent
  fleet opt-out                 Disable fleet reporting
  auth login                    Authenticate with a Sigil cloud API key
  auth status                   Show current tier and API key status
  auth logout                   Remove API key from config
  sync status                   Show sync agent status and cursors
  sync pause                    Temporarily pause cloud sync
  sync resume                   Resume cloud sync after pause
  vm start --image PATH         Start a VM from a disk image
  vm stop [--session ID]        Stop a running VM session
  vm status [--session ID]      Show VM session status
  vm list [--limit N]           List recent VM sessions
  vm merge --session ID         Trigger merge for a stopped VM session
  merge log [--session ID]      Show merge log for a session
  merge status --session ID     Show merge status for a session
  merge purge --session ID      Purge merge state for a session
  merge retry --session ID      Retry a failed merge for a session
  credential add <name>         Generate a new remote-access credential
  credential list               List all credentials
  credential revoke <name>      Revoke a credential immediately
  task                          Show current inferred task
  task history                  Recent task transitions
  day                           Today's work summary
  purge                         Delete all local data (requires confirmation)
  export                        Export all data as newline-delimited JSON

Flags:
  -socket PATH    Unix socket path (default: $XDG_RUNTIME_DIR/sigild.sock)
  -db PATH        SQLite path for offline reads
`)
}

// --- Task commands ----------------------------------------------------------

func cmdTask(socketPath string, args []string) error {
	if len(args) > 0 && args[0] == "history" {
		return cmdTaskHistory(socketPath)
	}

	resp, err := call(socketPath, "task", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var t struct {
		ID          string `json:"id"`
		Phase       string `json:"phase"`
		RepoRoot    string `json:"repo_root"`
		Branch      string `json:"branch"`
		ElapsedMin  int    `json:"elapsed_min"`
		CommitCount int    `json:"commit_count"`
		TestRuns    int    `json:"test_runs"`
		TestFails   int    `json:"test_failures"`
		Files       []struct {
			Path  string `json:"path"`
			Edits int    `json:"edits"`
		} `json:"files"`
	}
	if err := json.Unmarshal(resp.Payload, &t); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if t.Phase == "idle" {
		fmt.Println("No active task (idle)")
		return nil
	}

	repo := filepath.Base(t.RepoRoot)
	fmt.Printf("Task: %s on %s (%s)\n", t.Phase, t.Branch, repo)
	fmt.Printf("  Phase:    %s (%dm)\n", t.Phase, t.ElapsedMin)
	if t.Branch != "" {
		fmt.Printf("  Branch:   %s\n", t.Branch)
	}
	fmt.Printf("  Repo:     %s\n", t.RepoRoot)

	if len(t.Files) > 0 {
		fmt.Printf("  Files:    ")
		for i, f := range t.Files {
			if i > 0 {
				fmt.Print(", ")
			}
			fmt.Printf("%s (+%d)", filepath.Base(f.Path), f.Edits)
			if i >= 4 {
				fmt.Printf(" ... +%d more", len(t.Files)-5)
				break
			}
		}
		fmt.Println()
	}
	fmt.Printf("  Tests:    %d runs, %d failures\n", t.TestRuns, t.TestFails)
	fmt.Printf("  Commits:  %d\n", t.CommitCount)
	return nil
}

func cmdTaskHistory(socketPath string) error {
	resp, err := call(socketPath, "task-history", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var tasks []struct {
		ID        string `json:"id"`
		Phase     string `json:"phase"`
		RepoRoot  string `json:"repo_root"`
		Branch    string `json:"branch"`
		StartedAt string `json:"started_at"`
		Commits   int    `json:"commits"`
		Files     int    `json:"files"`
	}
	if err := json.Unmarshal(resp.Payload, &tasks); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if len(tasks) == 0 {
		fmt.Println("No task history")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TIME\tPHASE\tREPO\tBRANCH\tCOMMITS\tFILES")
	for _, t := range tasks {
		ts, _ := time.Parse(time.RFC3339, t.StartedAt)
		repo := filepath.Base(t.RepoRoot)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%d\n",
			ts.Format("15:04"), t.Phase, repo, t.Branch, t.Commits, t.Files)
	}
	return w.Flush()
}

func cmdDay(socketPath string) error {
	resp, err := call(socketPath, "day-summary", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var d struct {
		Date             string   `json:"date"`
		Repos            []string `json:"repos"`
		TasksStarted     int      `json:"tasks_started"`
		TasksCompleted   int      `json:"tasks_completed"`
		TotalCommits     int      `json:"total_commits"`
		FilesTouched     int      `json:"files_touched"`
		EditingMinutes   int      `json:"editing_minutes"`
		VerifyingMinutes int      `json:"verifying_minutes"`
		StuckMinutes     int      `json:"stuck_minutes"`
		SpeedScore       float64  `json:"speed_score"`
		Tasks            []struct {
			Branch      string  `json:"branch"`
			RepoRoot    string  `json:"repo_root"`
			Phase       string  `json:"phase"`
			DurationMin int     `json:"duration_min"`
			Files       int     `json:"files"`
			TotalEdits  int     `json:"total_edits"`
			Commits     int     `json:"commits"`
			TestRuns    int     `json:"test_runs"`
			TestFails   int     `json:"test_failures"`
			Completed   bool    `json:"completed"`
			SpeedScore  float64 `json:"speed_score"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(resp.Payload, &d); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	fmt.Printf("Today (%s)\n", d.Date)

	if len(d.Repos) > 0 {
		repos := make([]string, len(d.Repos))
		for i, r := range d.Repos {
			repos[i] = filepath.Base(r)
		}
		fmt.Printf("  Repos:     %s\n", strings.Join(repos, ", "))
	}
	fmt.Printf("  Tasks:     %d started, %d completed\n", d.TasksStarted, d.TasksCompleted)
	fmt.Printf("  Commits:   %d\n", d.TotalCommits)
	fmt.Printf("  Files:     %d touched\n", d.FilesTouched)
	fmt.Printf("  Time:      editing %dm | verifying %dm | stuck %dm\n",
		d.EditingMinutes, d.VerifyingMinutes, d.StuckMinutes)

	if d.SpeedScore > 0 {
		fmt.Printf("  Speed:     %.1f edits/min (size-weighted)\n", d.SpeedScore)
	}

	if len(d.Tasks) > 0 {
		fmt.Println()
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "  BRANCH\tPHASE\tTIME\tFILES\tCOMMITS\tTESTS")
		for _, t := range d.Tasks {
			status := t.Phase
			if t.Completed {
				status = "done"
			}
			fmt.Fprintf(w, "  %s\t%s\t%dm\t%d\t%d\t%d/%d\n",
				t.Branch, status, t.DurationMin, t.Files, t.Commits, t.TestRuns-t.TestFails, t.TestRuns)
		}
		w.Flush()
	}
	return nil
}

// --- ML commands -----------------------------------------------------------

func cmdML(socketPath string, args []string) error {
	if len(args) == 0 {
		return cmdMLStatus(socketPath)
	}
	switch args[0] {
	case "status":
		return cmdMLStatus(socketPath)
	case "train":
		return cmdMLTrain(socketPath)
	case "predict":
		if len(args) < 2 {
			return fmt.Errorf("usage: sigilctl ml predict <endpoint> [key=value ...]")
		}
		return cmdMLPredict(socketPath, args[1], args[2:])
	case "finetune":
		return cmdMLFinetune(socketPath)
	case "history":
		return cmdMLHistory(socketPath)
	case "rollback":
		if len(args) < 2 {
			return fmt.Errorf("usage: sigilctl ml rollback <adapter-id>")
		}
		return cmdMLRollback(socketPath, args[1])
	default:
		return fmt.Errorf("unknown ml subcommand %q — use: status, train, predict, finetune, history, rollback", args[0])
	}
}

func cmdMLStatus(socketPath string) error {
	resp, err := call(socketPath, "ml-status", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}
	var s struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(resp.Payload, &s); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	fmt.Printf("ML engine: %s\n", s.Status)
	return nil
}

func cmdMLTrain(socketPath string) error {
	resp, err := call(socketPath, "ml-train", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}
	fmt.Println("Training triggered")
	return nil
}

func cmdMLPredict(socketPath string, endpoint string, kvPairs []string) error {
	features := make(map[string]any)
	for _, kv := range kvPairs {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			continue
		}
		// Try to parse as number.
		if f, err := strconv.ParseFloat(parts[1], 64); err == nil {
			features[parts[0]] = f
		} else {
			features[parts[0]] = parts[1]
		}
	}

	resp, err := call(socketPath, "ml-predict", map[string]any{
		"endpoint": endpoint,
		"features": features,
	})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var pred struct {
		Endpoint  string         `json:"endpoint"`
		Result    map[string]any `json:"result"`
		Routing   string         `json:"routing"`
		LatencyMS int64          `json:"latency_ms"`
	}
	if err := json.Unmarshal(resp.Payload, &pred); err != nil {
		return fmt.Errorf("decode: %w", err)
	}

	fmt.Printf("Endpoint:  %s\n", pred.Endpoint)
	fmt.Printf("Routing:   %s\n", pred.Routing)
	fmt.Printf("Latency:   %dms\n", pred.LatencyMS)
	for k, v := range pred.Result {
		fmt.Printf("  %s: %v\n", k, v)
	}
	return nil
}

func cmdMLFinetune(socketPath string) error {
	resp, err := call(socketPath, "ml-finetune", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}
	var result struct {
		RunID       string  `json:"run_id"`
		Status      string  `json:"status"`
		RowsTrained int     `json:"rows_trained"`
		AdapterPath string  `json:"adapter_path"`
		LossFinal   float64 `json:"loss_final"`
		Duration    string  `json:"duration"`
		Error       string  `json:"error"`
	}
	_ = json.Unmarshal(resp.Payload, &result)
	fmt.Printf("Fine-tune run: %s\n", result.RunID)
	fmt.Printf("  Status:   %s\n", result.Status)
	if result.RowsTrained > 0 {
		fmt.Printf("  Rows:     %d\n", result.RowsTrained)
	}
	if result.AdapterPath != "" {
		fmt.Printf("  Adapter:  %s\n", result.AdapterPath)
	}
	if result.LossFinal > 0 {
		fmt.Printf("  Loss:     %.4f\n", result.LossFinal)
	}
	if result.Duration != "" {
		fmt.Printf("  Duration: %s\n", result.Duration)
	}
	if result.Error != "" {
		fmt.Printf("  Error:    %s\n", result.Error)
	}
	return nil
}

func cmdMLHistory(socketPath string) error {
	resp, err := call(socketPath, "ml-history", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}
	var entries []struct {
		RunID       string  `json:"run_id"`
		StartedAt   int64   `json:"started_at"`
		Status      string  `json:"status"`
		Mode        string  `json:"mode"`
		RowsTrained int     `json:"rows_trained"`
		LossFinal   float64 `json:"loss_final"`
	}
	_ = json.Unmarshal(resp.Payload, &entries)
	if len(entries) == 0 {
		fmt.Println("No fine-tune runs found.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "RUN ID\tSTATUS\tMODE\tROWS\tLOSS\tDATE")
	for _, e := range entries {
		date := time.UnixMilli(e.StartedAt).Format("2006-01-02 15:04")
		loss := "-"
		if e.LossFinal > 0 {
			loss = fmt.Sprintf("%.4f", e.LossFinal)
		}
		rid := e.RunID
		if len(rid) > 8 {
			rid = rid[:8]
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\n", rid, e.Status, e.Mode, e.RowsTrained, loss, date)
	}
	w.Flush()
	return nil
}

func cmdMLRollback(socketPath string, adapterID string) error {
	resp, err := call(socketPath, "ml-rollback", map[string]any{
		"adapter_id": adapterID,
	})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}
	fmt.Printf("Rolled back to adapter %s\n", adapterID)
	return nil
}

// --- Plugin commands --------------------------------------------------------

func cmdPlugin(args []string) error {
	if len(args) == 0 {
		return cmdPluginList()
	}
	switch args[0] {
	case "list":
		return cmdPluginList()
	case "list-available":
		return cmdPluginListAvailable(args[1:])
	case "install":
		if len(args) < 2 {
			return fmt.Errorf("usage: sigilctl plugin install <name> [--brew]")
		}
		method := plugin.DetectInstallMethod()
		for _, a := range args[2:] {
			if a == "--brew" {
				method = plugin.InstallBrew
			}
		}
		return plugin.Install(args[1], method)
	case "setup":
		if len(args) < 2 {
			return fmt.Errorf("usage: sigilctl plugin setup <name>")
		}
		reader := bufio.NewReader(os.Stdin)
		toml, err := plugin.Setup(args[1], reader)
		if err != nil {
			return err
		}
		fmt.Println("\nAdd this to your ~/.config/sigil/config.toml:")
		fmt.Println(toml)
		return nil
	default:
		return fmt.Errorf("unknown plugin subcommand %q — use: list, list-available, install, setup", args[0])
	}
}

func cmdPluginList() error {
	reg := plugin.Registry()
	installed := 0

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PLUGIN\tSTATUS\tCATEGORY\tDESCRIPTION")
	for _, e := range reg {
		status := "not installed"
		if plugin.IsInstalled(e.Name) {
			status = "installed"
			installed++
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", e.Name, status, e.Category, e.Description)
	}
	w.Flush()
	fmt.Printf("\n%d/%d plugins installed\n", installed, len(reg))
	return nil
}

func cmdPluginListAvailable(args []string) error {
	version := ""
	if len(args) > 0 {
		version = args[0]
	}

	var entries []plugin.RegistryEntry
	if version != "" {
		entries = plugin.ByVersion(version)
		if len(entries) == 0 {
			return fmt.Errorf("no plugins found for version %q", version)
		}
	} else {
		entries = plugin.Registry()
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PLUGIN\tVERSION\tCATEGORY\tLANG\tINSTALLED\tDESCRIPTION")
	for _, e := range entries {
		installed := "no"
		if plugin.IsInstalled(e.Name) {
			installed = "yes"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			e.Name, e.Version, e.Category, e.Language, installed, e.Description)
	}
	w.Flush()
	return nil
}

// --- Ask command -----------------------------------------------------------

func cmdAsk(socketPath string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: sigilctl ask \"your question here\"")
	}
	query := strings.Join(args, " ")

	resp, err := call(socketPath, "ask", map[string]string{"query": query})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var result struct {
		Answer        string `json:"answer"`
		ToolCallsMade int    `json:"tool_calls_made"`
		LatencyMS     int64  `json:"latency_ms"`
	}
	if err := json.Unmarshal(resp.Payload, &result); err != nil {
		return fmt.Errorf("decode: %w", err)
	}

	fmt.Println(result.Answer)
	if result.ToolCallsMade > 0 {
		fmt.Printf("\n[%d tool calls, %dms]\n", result.ToolCallsMade, result.LatencyMS)
	}
	return nil
}

// --- Correct command -------------------------------------------------------

func cmdCorrect(socketPath string, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: sigilctl correct <event_id> <category>\n  categories: creating, refining, verifying, navigating, researching, integrating, communicating, idle")
	}

	eventID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return fmt.Errorf("invalid event_id %q: %w", args[0], err)
	}

	resp, err := call(socketPath, "correct", map[string]any{
		"event_id":         eventID,
		"correct_category": args[1],
	})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}
	fmt.Printf("Correction recorded: event %d → %s\n", eventID, args[1])
	return nil
}

// --- Start/Stop commands ---------------------------------------------------

func cmdStop(socketPath string) error {
	resp, err := call(socketPath, "shutdown", nil)
	if err != nil {
		fmt.Println("sigild is not running")
		return nil
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}
	fmt.Println("sigild is shutting down")
	return nil
}

func cmdStart(socketPath string) error {
	// Check if daemon is already running.
	if _, err := call(socketPath, "status", nil); err == nil {
		fmt.Println("sigild is already running")
		return nil
	}

	switch goruntime.GOOS {
	case "darwin":
		return startDarwin()
	case "linux":
		return startLinux()
	default:
		return startDirect()
	}
}

func startDarwin() error {
	label := "com.sigil.sigild"
	// Try launchctl kickstart first (works if agent is loaded but stopped).
	uid := currentUID()
	out, err := exec.Command("launchctl", "kickstart", fmt.Sprintf("gui/%d/%s", uid, label)).CombinedOutput()
	if err == nil {
		fmt.Println("sigild started via launchd")
		return nil
	}

	// Fall back to bootstrap/load if the agent isn't loaded.
	home, _ := os.UserHomeDir()
	plist := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
	if _, statErr := os.Stat(plist); statErr != nil {
		fmt.Println("launchd plist not found — run 'sigild init' first, or starting directly")
		return startDirect()
	}

	out, err = exec.Command("launchctl", "load", plist).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl load: %w — %s", err, strings.TrimSpace(string(out)))
	}
	fmt.Println("sigild started via launchd")
	return nil
}

func startLinux() error {
	out, err := exec.Command("systemctl", "--user", "start", "sigild.service").CombinedOutput()
	if err == nil {
		fmt.Println("sigild started via systemd")
		return nil
	}

	// If systemd unit not found, fall back to direct start.
	if strings.Contains(string(out), "not found") || strings.Contains(string(out), "No such file") {
		fmt.Println("systemd unit not found — run 'sigild init' first, or starting directly")
		return startDirect()
	}
	return fmt.Errorf("systemctl start: %w — %s", err, strings.TrimSpace(string(out)))
}

func startDirect() error {
	exe, err := exec.LookPath("sigild")
	if err != nil {
		return fmt.Errorf("sigild not found in PATH: %w", err)
	}

	cmd := exec.Command(exe)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start sigild: %w", err)
	}

	// Detach — don't wait for the child.
	_ = cmd.Process.Release()
	fmt.Printf("sigild started (pid %d)\n", cmd.Process.Pid)
	return nil
}

// --- VM commands ------------------------------------------------------------

// cmdVM dispatches vm subcommands: start, stop, status, list, merge.
func cmdVM(socketPath string, args []string) error {
	if len(args) == 0 {
		fmt.Println("Usage: sigilctl vm <start|stop|status|list|merge> [flags]")
		return nil
	}
	switch args[0] {
	case "start":
		return cmdVMStart(socketPath, args[1:])
	case "stop":
		return cmdVMStop(socketPath, args[1:])
	case "status":
		return cmdVMStatus(socketPath, args[1:])
	case "list":
		return cmdVMList(socketPath, args[1:])
	case "merge":
		return cmdVMMerge(socketPath, args[1:])
	default:
		return fmt.Errorf("unknown vm command %q — use start, stop, status, list, or merge", args[0])
	}
}

func cmdVMStart(socketPath string, args []string) error {
	fs := flag.NewFlagSet("vm start", flag.ContinueOnError)
	image := fs.String("image", "", "path to the VM disk image (required)")
	overlay := fs.String("overlay", "", "path to the overlay disk image")
	vmDB := fs.String("vm-db", "", "path to the VM SQLite database")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *image == "" {
		return fmt.Errorf("usage: sigilctl vm start --image PATH [--overlay PATH] [--vm-db PATH]")
	}

	payload := map[string]any{"image": *image}
	if *overlay != "" {
		payload["overlay"] = *overlay
	}
	if *vmDB != "" {
		payload["vm_db"] = *vmDB
	}

	resp, err := call(socketPath, "VMStart", payload)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var result struct {
		SessionID string `json:"session_id"`
		State     string `json:"state"`
		PID       int    `json:"pid"`
	}
	if err := json.Unmarshal(resp.Payload, &result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	fmt.Printf("Session:  %s\n", result.SessionID)
	fmt.Printf("State:    %s\n", result.State)
	if result.PID != 0 {
		fmt.Printf("PID:      %d\n", result.PID)
	}
	return nil
}

func cmdVMStop(socketPath string, args []string) error {
	fs := flag.NewFlagSet("vm stop", flag.ContinueOnError)
	sessionID := fs.String("session", "", "session ID to stop (defaults to active session)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	payload := map[string]any{}
	if *sessionID != "" {
		payload["session_id"] = *sessionID
	}

	resp, err := call(socketPath, "VMStop", payload)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var result struct {
		SessionID string `json:"session_id"`
		State     string `json:"state"`
	}
	if err := json.Unmarshal(resp.Payload, &result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	fmt.Printf("Session %s stopped (state: %s)\n", result.SessionID, result.State)
	return nil
}

func cmdVMStatus(socketPath string, args []string) error {
	fs := flag.NewFlagSet("vm status", flag.ContinueOnError)
	sessionID := fs.String("session", "", "session ID (defaults to active session)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	payload := map[string]any{}
	if *sessionID != "" {
		payload["session_id"] = *sessionID
	}

	resp, err := call(socketPath, "VMStatus", payload)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var result struct {
		SessionID string `json:"session_id"`
		State     string `json:"state"`
		Image     string `json:"image"`
		StartedAt string `json:"started_at"`
		PID       int    `json:"pid"`
		UptimeSec int64  `json:"uptime_sec"`
	}
	if err := json.Unmarshal(resp.Payload, &result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	fmt.Printf("Session:  %s\n", result.SessionID)
	fmt.Printf("State:    %s\n", result.State)
	if result.Image != "" {
		fmt.Printf("Image:    %s\n", result.Image)
	}
	if result.StartedAt != "" {
		if t, err := time.Parse(time.RFC3339, result.StartedAt); err == nil {
			fmt.Printf("Started:  %s\n", t.Local().Format("2006-01-02 15:04:05"))
		} else {
			fmt.Printf("Started:  %s\n", result.StartedAt)
		}
	}
	if result.PID != 0 {
		fmt.Printf("PID:      %d\n", result.PID)
	}
	if result.UptimeSec > 0 {
		fmt.Printf("Uptime:   %s\n", formatDuration(result.UptimeSec))
	}
	return nil
}

func cmdVMList(socketPath string, args []string) error {
	fs := flag.NewFlagSet("vm list", flag.ContinueOnError)
	limit := fs.Int("limit", 20, "maximum number of sessions to show")
	if err := fs.Parse(args); err != nil {
		return err
	}

	resp, err := call(socketPath, "VMList", map[string]any{"limit": *limit})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var sessions []struct {
		SessionID string `json:"session_id"`
		State     string `json:"state"`
		Image     string `json:"image"`
		StartedAt string `json:"started_at"`
		StoppedAt string `json:"stopped_at"`
	}
	if err := json.Unmarshal(resp.Payload, &sessions); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if len(sessions) == 0 {
		fmt.Println("No VM sessions found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SESSION\tSTATE\tSTARTED\tSTOPPED\tIMAGE")
	for _, s := range sessions {
		started := formatTimestamp(s.StartedAt)
		stopped := formatTimestamp(s.StoppedAt)
		if stopped == "" {
			stopped = "-"
		}
		image := filepath.Base(s.Image)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", s.SessionID, s.State, started, stopped, image)
	}
	return w.Flush()
}

func cmdVMMerge(socketPath string, args []string) error {
	fs := flag.NewFlagSet("vm merge", flag.ContinueOnError)
	sessionID := fs.String("session", "", "session ID to merge (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sessionID == "" {
		return fmt.Errorf("usage: sigilctl vm merge --session ID")
	}

	resp, err := call(socketPath, "VMMerge", map[string]any{"session_id": *sessionID})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var result struct {
		SessionID string `json:"session_id"`
		Status    string `json:"status"`
		Rows      int    `json:"rows_merged"`
	}
	if err := json.Unmarshal(resp.Payload, &result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	fmt.Printf("Merge complete: session=%s status=%s rows=%d\n", result.SessionID, result.Status, result.Rows)
	return nil
}

// --- Merge commands ---------------------------------------------------------

// cmdMerge dispatches merge subcommands: log, status, purge, retry.
func cmdMerge(socketPath string, args []string) error {
	if len(args) == 0 {
		fmt.Println("Usage: sigilctl merge <log|status|purge|retry> [flags]")
		return nil
	}
	switch args[0] {
	case "log":
		return cmdMergeLog(socketPath, args[1:])
	case "status":
		return cmdMergeStatus(socketPath, args[1:])
	case "purge":
		return cmdMergePurge(socketPath, args[1:])
	case "retry":
		return cmdMergeRetry(socketPath, args[1:])
	default:
		return fmt.Errorf("unknown merge command %q — use log, status, purge, or retry", args[0])
	}
}

func cmdMergeLog(socketPath string, args []string) error {
	fs := flag.NewFlagSet("merge log", flag.ContinueOnError)
	sessionID := fs.String("session", "", "filter by session ID")
	if err := fs.Parse(args); err != nil {
		return err
	}

	payload := map[string]any{}
	if *sessionID != "" {
		payload["session_id"] = *sessionID
	}

	resp, err := call(socketPath, "merge-log", payload)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var entries []struct {
		ID        int64  `json:"id"`
		SessionID string `json:"session_id"`
		Op        string `json:"op"`
		State     string `json:"state"`
		Path      string `json:"path"`
		Error     string `json:"error"`
		CreatedAt string `json:"created_at"`
	}
	if err := json.Unmarshal(resp.Payload, &entries); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if len(entries) == 0 {
		fmt.Println("No merge log entries.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSESSION\tOP\tSTATE\tCREATED\tPATH")
	for _, e := range entries {
		ts := formatTimestamp(e.CreatedAt)
		path := e.Path
		if path == "" {
			path = "-"
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\n", e.ID, e.SessionID, e.Op, e.State, ts, path)
	}
	if err := w.Flush(); err != nil {
		return err
	}

	// Print errors separately for readability.
	for _, e := range entries {
		if e.Error != "" {
			fmt.Printf("\n[%d] error: %s\n", e.ID, e.Error)
		}
	}
	return nil
}

func cmdMergeStatus(socketPath string, args []string) error {
	fs := flag.NewFlagSet("merge status", flag.ContinueOnError)
	sessionID := fs.String("session", "", "session ID (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sessionID == "" {
		return fmt.Errorf("usage: sigilctl merge status --session ID")
	}

	resp, err := call(socketPath, "merge-log", map[string]any{"session_id": *sessionID})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var entries []struct {
		ID        int64  `json:"id"`
		Op        string `json:"op"`
		State     string `json:"state"`
		Path      string `json:"path"`
		Error     string `json:"error"`
		CreatedAt string `json:"created_at"`
	}
	if err := json.Unmarshal(resp.Payload, &entries); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if len(entries) == 0 {
		fmt.Printf("No merge activity for session %s.\n", *sessionID)
		return nil
	}

	// Summarise by state.
	counts := make(map[string]int)
	for _, e := range entries {
		counts[e.State]++
	}

	fmt.Printf("Session: %s\n", *sessionID)
	fmt.Printf("Entries: %d\n", len(entries))
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  STATE\tCOUNT")
	for state, n := range counts {
		fmt.Fprintf(w, "  %s\t%d\n", state, n)
	}
	if err := w.Flush(); err != nil {
		return err
	}

	// Show the most recent entry.
	last := entries[len(entries)-1]
	fmt.Printf("\nLast op: %s → %s at %s\n", last.Op, last.State, formatTimestamp(last.CreatedAt))
	if last.Error != "" {
		fmt.Printf("Error:   %s\n", last.Error)
	}
	return nil
}

func cmdMergePurge(socketPath string, args []string) error {
	fs := flag.NewFlagSet("merge purge", flag.ContinueOnError)
	sessionID := fs.String("session", "", "session ID (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sessionID == "" {
		return fmt.Errorf("usage: sigilctl merge purge --session ID")
	}

	resp, err := call(socketPath, "merge-purge", map[string]any{"session_id": *sessionID})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	fmt.Printf("Merge state for session %s purged.\n", *sessionID)
	return nil
}

func cmdMergeRetry(socketPath string, args []string) error {
	fs := flag.NewFlagSet("merge retry", flag.ContinueOnError)
	sessionID := fs.String("session", "", "session ID (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sessionID == "" {
		return fmt.Errorf("usage: sigilctl merge retry --session ID")
	}

	resp, err := call(socketPath, "VMMerge", map[string]any{"session_id": *sessionID})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var result struct {
		SessionID string `json:"session_id"`
		State     string `json:"state"`
	}
	if err := json.Unmarshal(resp.Payload, &result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	fmt.Printf("Merge retry for session %s: %s\n", result.SessionID, result.State)
	return nil
}

// --- Formatting helpers -----------------------------------------------------

// formatTimestamp parses an RFC3339 timestamp and returns a short local time
// string. Returns the original value unchanged if parsing fails.
func formatTimestamp(s string) string {
	if s == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return t.Local().Format("2006-01-02 15:04")
}

// formatDuration converts a duration in seconds to a human-readable string
// such as "2h 15m 30s".
func formatDuration(sec int64) string {
	h := sec / 3600
	m := (sec % 3600) / 60
	s := sec % 60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	case m > 0:
		return fmt.Sprintf("%dm %ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

// --- Corpus commands ---------------------------------------------------------

func cmdCorpus(socketPath string, args []string) error {
	if len(args) == 0 {
		fmt.Println("Usage: sigilctl corpus <stats|purge|export>")
		return nil
	}

	switch args[0] {
	case "stats":
		return cmdCorpusStats(socketPath)
	case "purge":
		return cmdCorpusPurge(socketPath, args[1:])
	case "export":
		return cmdCorpusExport(socketPath, args[1:])
	default:
		return fmt.Errorf("unknown corpus command %q — use stats, purge, or export", args[0])
	}
}

func cmdCorpusStats(socketPath string) error {
	resp, err := call(socketPath, "corpus-stats", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var stats struct {
		TotalRows        int            `json:"total_rows"`
		RowsByOrigin     map[string]int `json:"rows_by_origin"`
		LabelDist        map[string]int `json:"label_distribution"`
		AnnotatedCount   int            `json:"annotated_count"`
		UnannotatedCount int            `json:"unannotated_count"`
		OldestTS         int64          `json:"oldest_ts"`
		NewestTS         int64          `json:"newest_ts"`
	}
	if err := json.Unmarshal(resp.Payload, &stats); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	fmt.Printf("Training Corpus Statistics\n")
	fmt.Printf("  Total rows:    %d\n", stats.TotalRows)
	fmt.Printf("  Annotated:     %d\n", stats.AnnotatedCount)
	fmt.Printf("  Unannotated:   %d\n", stats.UnannotatedCount)

	if stats.TotalRows > 0 {
		fmt.Printf("  Date range:    %s — %s\n",
			time.UnixMilli(stats.OldestTS).Format("2006-01-02"),
			time.UnixMilli(stats.NewestTS).Format("2006-01-02"))
	}

	fmt.Printf("\n  By origin:\n")
	for origin, count := range stats.RowsByOrigin {
		fmt.Printf("    %-12s %d\n", origin, count)
	}

	fmt.Printf("\n  Label distribution:\n")
	for label, count := range stats.LabelDist {
		fmt.Printf("    %-16s %d\n", label, count)
	}

	return nil
}

func cmdCorpusPurge(socketPath string, args []string) error {
	fs := flag.NewFlagSet("corpus purge", flag.ContinueOnError)
	before := fs.String("before", "", "delete rows before this date (YYYY-MM-DD)")
	yes := fs.Bool("yes", false, "skip confirmation prompt")
	dryRun := fs.Bool("dry-run", false, "show count without deleting")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *before == "" {
		return fmt.Errorf("--before is required (format: YYYY-MM-DD)")
	}

	t, err := time.Parse("2006-01-02", *before)
	if err != nil {
		return fmt.Errorf("invalid date %q: %w", *before, err)
	}
	beforeTS := t.UnixMilli()

	if *dryRun {
		fmt.Printf("Would delete corpus rows before %s (dry run)\n", *before)
		return nil
	}

	if !*yes {
		fmt.Printf("This will delete all corpus rows before %s. Continue? [y/N] ", *before)
		var answer string
		fmt.Scanln(&answer)
		if answer != "y" && answer != "Y" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	resp, err := call(socketPath, "corpus-purge", map[string]any{
		"before_ts": beforeTS,
	})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var result struct {
		RowsDeleted int `json:"rows_deleted"`
	}
	_ = json.Unmarshal(resp.Payload, &result)
	fmt.Printf("Deleted %d corpus rows.\n", result.RowsDeleted)
	return nil
}

func cmdCorpusExport(socketPath string, args []string) error {
	fs := flag.NewFlagSet("corpus export", flag.ContinueOnError)
	output := fs.String("output", "corpus.jsonl", "output file path")
	if err := fs.Parse(args); err != nil {
		return err
	}

	resp, err := call(socketPath, "corpus-export", map[string]any{
		"output_path": *output,
	})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var result struct {
		RowsExported int    `json:"rows_exported"`
		OutputPath   string `json:"output_path"`
	}
	_ = json.Unmarshal(resp.Payload, &result)
	fmt.Printf("Exported %d rows to %s\n", result.RowsExported, result.OutputPath)
	return nil
}

// --- Audit viewer commands ---------------------------------------------------

func cmdAudit(socketPath string, args []string) error {
	if len(args) == 0 {
		fmt.Println("Usage: sigilctl audit <corpus|merge|filtered>")
		return nil
	}

	switch args[0] {
	case "corpus":
		return cmdAuditCorpus(socketPath)
	case "merge":
		return cmdAuditMerge(socketPath)
	case "filtered":
		return cmdAuditFiltered(socketPath)
	default:
		return fmt.Errorf("unknown audit command %q — use corpus, merge, or filtered", args[0])
	}
}

func cmdAuditCorpus(socketPath string) error {
	resp, err := call(socketPath, "audit-corpus", map[string]any{"limit": 20})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var rows []struct {
		ID          int64    `json:"id"`
		TS          int64    `json:"ts"`
		Origin      string   `json:"origin"`
		EventType   string   `json:"event_type"`
		Source      string   `json:"source"`
		PayloadHash string   `json:"payload_hash"`
		Label       *string  `json:"label"`
		Phase       *string  `json:"phase"`
		Confidence  *float64 `json:"confidence"`
	}
	_ = json.Unmarshal(resp.Payload, &rows)

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tTS\tORIGIN\tEVENT\tLABEL\tPHASE\tCONFIDENCE")
	for _, r := range rows {
		label, phase, conf := "-", "-", "-"
		if r.Label != nil {
			label = *r.Label
		}
		if r.Phase != nil {
			phase = *r.Phase
		}
		if r.Confidence != nil {
			conf = fmt.Sprintf("%.2f", *r.Confidence)
		}
		ts := time.UnixMilli(r.TS).Format("01-02 15:04")
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n", r.ID, ts, r.Origin, r.EventType, label, phase, conf)
	}
	w.Flush()
	return nil
}

func cmdAuditMerge(socketPath string) error {
	resp, err := call(socketPath, "audit-merge-log", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var rows []struct {
		SessionID    string `json:"session_id"`
		StartedAt    int64  `json:"started_at"`
		Status       string `json:"status"`
		RowsMerged   int    `json:"rows_merged"`
		RowsFiltered int    `json:"rows_filtered"`
	}
	_ = json.Unmarshal(resp.Payload, &rows)

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "SESSION\tSTARTED\tSTATUS\tMERGED\tFILTERED")
	for _, r := range rows {
		sid := r.SessionID
		if len(sid) > 12 {
			sid = sid[:12]
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\n",
			sid, time.UnixMilli(r.StartedAt).Format("01-02 15:04"), r.Status, r.RowsMerged, r.RowsFiltered)
	}
	w.Flush()
	return nil
}

func cmdAuditFiltered(socketPath string) error {
	resp, err := call(socketPath, "audit-filtered-log", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var rows []struct {
		SessionID      string `json:"session_id"`
		TS             int64  `json:"ts"`
		EventType      string `json:"event_type"`
		FilterRule     string `json:"filter_rule"`
		ExcludedReason string `json:"excluded_reason"`
	}
	_ = json.Unmarshal(resp.Payload, &rows)

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "SESSION\tTS\tEVENT\tRULE\tREASON")
	for _, r := range rows {
		sid := r.SessionID
		if len(sid) > 12 {
			sid = sid[:12]
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			sid, time.UnixMilli(r.TS).Format("01-02 15:04"), r.EventType, r.FilterRule, r.ExcludedReason)
	}
	w.Flush()
	return nil
}
