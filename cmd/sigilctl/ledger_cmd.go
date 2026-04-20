package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
)

// cmdLedger dispatches `sigilctl ledger <subcommand>` to the
// spec-029 Phase 7 handlers. The subcommand tree mirrors the four
// socket methods added in Phase 6 plus a "key rotate" entry which
// Phase 9 will wire to the Rotator.
func cmdLedger(socketPath string, args []string) error {
	if len(args) == 0 {
		fmt.Println("Usage: sigilctl ledger <list|get|verify|key> [flags]")
		return nil
	}
	switch args[0] {
	case "list":
		return cmdLedgerList(socketPath, args[1:])
	case "get":
		return cmdLedgerGet(socketPath, args[1:])
	case "verify":
		return cmdLedgerVerify(socketPath, args[1:])
	case "key":
		return cmdLedgerKey(socketPath, args[1:])
	default:
		return fmt.Errorf("unknown ledger subcommand %q — use list, get, verify, or key", args[0])
	}
}

// ledgerEntryPayload mirrors ledger.Entry's JSON shape for decoding
// socket responses. Keeping the struct local to sigilctl means
// sigilctl doesn't import the ledger package (which brings in
// SQLite driver init and ~30 MB of transitive deps); a drift between
// this and ledger.Entry surfaces as a decode mismatch in the tests.
type ledgerEntryPayload struct {
	ID           int64  `json:"ID"`
	Timestamp    string `json:"Timestamp"`
	Type         string `json:"Type"`
	Subject      string `json:"Subject"`
	PayloadJSON  string `json:"PayloadJSON"`
	PrevHash     string `json:"PrevHash"`
	Hash         string `json:"Hash"`
	Signature    string `json:"Signature"`
	SigningKeyFP string `json:"SigningKeyFP"`
}

type ledgerVerifyPayload struct {
	OK             bool   `json:"ok"`
	EntriesChecked int    `json:"entries_checked"`
	BreakAtID      int64  `json:"break_at_id,omitempty"`
	Reason         string `json:"reason,omitempty"`
	Detail         string `json:"detail,omitempty"`
}

type ledgerKeyRecord struct {
	Fingerprint string `json:"Fingerprint"`
	PublicKey   string `json:"PublicKey"`
	GeneratedAt string `json:"GeneratedAt"`
	RetiredAt   string `json:"RetiredAt"`
}

// cmdLedgerList implements `sigilctl ledger list [--before <id>]
// [--limit <n>] [--type <type>] [--format json|table]`. Default
// format is newline-JSON (one entry per line) so UNIX tools like
// jq can pipeline against it without pre-parsing; --format table
// produces an aligned-column human-readable view.
func cmdLedgerList(socketPath string, args []string) error {
	fs := flag.NewFlagSet("ledger list", flag.ContinueOnError)
	before := fs.Int64("before", 0, "before_id cursor (exclusive upper bound)")
	limit := fs.Int("limit", 0, "max rows to return (0 = server default)")
	typeFilter := fs.String("type", "", "narrow to a single event type")
	format := fs.String("format", "json", "output format: json (default) or table")
	if err := fs.Parse(args); err != nil {
		return err
	}

	payload := map[string]any{}
	if *before > 0 {
		payload["before_id"] = *before
	}
	if *limit > 0 {
		payload["limit"] = *limit
	}
	if *typeFilter != "" {
		payload["type_filter"] = *typeFilter
	}

	resp, err := call(socketPath, "ledger-list", payload)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var body struct {
		Entries []ledgerEntryPayload `json:"entries"`
	}
	if err := json.Unmarshal(resp.Payload, &body); err != nil {
		return fmt.Errorf("decode ledger-list response: %w", err)
	}

	switch *format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		for _, e := range body.Entries {
			if err := enc.Encode(e); err != nil {
				return err
			}
		}
	case "table":
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tTS\tTYPE\tSUBJECT\tHASH")
		for _, e := range body.Entries {
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n",
				e.ID, shortTS(e.Timestamp), e.Type, truncate(e.Subject, 40), hashSuffix(e.Hash))
		}
		w.Flush()
	default:
		return fmt.Errorf("unknown --format %q — use json or table", *format)
	}
	return nil
}

// cmdLedgerGet implements `sigilctl ledger get <id> [--format
// json|table]`.
func cmdLedgerGet(socketPath string, args []string) error {
	fs := flag.NewFlagSet("ledger get", flag.ContinueOnError)
	format := fs.String("format", "json", "output format: json or table")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return fmt.Errorf("usage: sigilctl ledger get <id> [--format json|table]")
	}
	id, err := strconv.ParseInt(rest[0], 10, 64)
	if err != nil || id <= 0 {
		return fmt.Errorf("ledger get: invalid id %q (must be a positive integer)", rest[0])
	}

	resp, err := call(socketPath, "ledger-get", map[string]any{"id": id})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var entry ledgerEntryPayload
	if err := json.Unmarshal(resp.Payload, &entry); err != nil {
		return fmt.Errorf("decode ledger-get response: %w", err)
	}

	switch *format {
	case "json":
		b, _ := json.MarshalIndent(entry, "", "  ")
		fmt.Println(string(b))
	case "table":
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintf(w, "ID\t%d\n", entry.ID)
		fmt.Fprintf(w, "Timestamp\t%s\n", entry.Timestamp)
		fmt.Fprintf(w, "Type\t%s\n", entry.Type)
		fmt.Fprintf(w, "Subject\t%s\n", entry.Subject)
		fmt.Fprintf(w, "Hash\t%s\n", entry.Hash)
		fmt.Fprintf(w, "PrevHash\t%s\n", entry.PrevHash)
		fmt.Fprintf(w, "Signature\t%s\n", entry.Signature)
		fmt.Fprintf(w, "SigningKeyFP\t%s\n", entry.SigningKeyFP)
		fmt.Fprintf(w, "Payload\t%s\n", entry.PayloadJSON)
		w.Flush()
	default:
		return fmt.Errorf("unknown --format %q — use json or table", *format)
	}
	return nil
}

// cmdLedgerVerify implements `sigilctl ledger verify [--from <id>]
// [--to <id>] [--id <id>] [--format json|table]`. Exit codes per
// FR-028:
//
//	0  verified
//	1  infrastructure error
//	2  chain broken
//
// Callers use the exit code for CI gating; the text/JSON output is
// diagnostic.
func cmdLedgerVerify(socketPath string, args []string) error {
	fs := flag.NewFlagSet("ledger verify", flag.ContinueOnError)
	from := fs.Int64("from", 0, "from_id (inclusive)")
	to := fs.Int64("to", 0, "to_id (inclusive)")
	single := fs.Int64("id", 0, "single-entry shortcut")
	full := fs.Bool("full", false, "verify full chain (default if no scope set)")
	format := fs.String("format", "json", "output format: json or table")
	if err := fs.Parse(args); err != nil {
		return err
	}

	payload := map[string]any{}
	if *single > 0 {
		payload["id"] = *single
	} else if *from > 0 || *to > 0 {
		if *from > 0 {
			payload["from_id"] = *from
		}
		if *to > 0 {
			payload["to_id"] = *to
		}
	} else if *full {
		payload["full"] = true
	}

	resp, err := call(socketPath, "ledger-verify", payload)
	if err != nil {
		// Infrastructure error — exit 1 so CI gates distinguish.
		fmt.Fprintln(os.Stderr, "ledger verify: socket call failed:", err)
		os.Exit(1)
	}
	if !resp.OK {
		fmt.Fprintln(os.Stderr, "daemon error:", resp.Error)
		os.Exit(1)
	}

	var result ledgerVerifyPayload
	if err := json.Unmarshal(resp.Payload, &result); err != nil {
		fmt.Fprintln(os.Stderr, "decode ledger-verify response:", err)
		os.Exit(1)
	}

	switch *format {
	case "json":
		b, _ := json.Marshal(result)
		fmt.Println(string(b))
	case "table":
		if result.OK {
			fmt.Printf("VERIFIED  entries=%d\n", result.EntriesChecked)
		} else {
			fmt.Printf("BROKEN    break_at_id=%d  reason=%s  entries_checked=%d\n",
				result.BreakAtID, result.Reason, result.EntriesChecked)
			if result.Detail != "" {
				fmt.Printf("          detail=%s\n", result.Detail)
			}
		}
	default:
		return fmt.Errorf("unknown --format %q — use json or table", *format)
	}

	if !result.OK {
		os.Exit(2)
	}
	return nil
}

// cmdLedgerKey implements `sigilctl ledger key [--format json|table]`.
// Prints the active + retired signing-key registry records.
func cmdLedgerKey(socketPath string, args []string) error {
	fs := flag.NewFlagSet("ledger key", flag.ContinueOnError)
	format := fs.String("format", "table", "output format: json or table (table default for ergonomics)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	resp, err := call(socketPath, "ledger-key", nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var body struct {
		Active  []ledgerKeyRecord `json:"active"`
		Retired []ledgerKeyRecord `json:"retired"`
	}
	if err := json.Unmarshal(resp.Payload, &body); err != nil {
		return fmt.Errorf("decode ledger-key response: %w", err)
	}

	switch *format {
	case "json":
		b, _ := json.MarshalIndent(body, "", "  ")
		fmt.Println(string(b))
	case "table":
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "STATE\tFINGERPRINT\tGENERATED_AT\tRETIRED_AT")
		for _, k := range body.Active {
			fmt.Fprintf(w, "active\t%s\t%s\t-\n", k.Fingerprint, shortTS(k.GeneratedAt))
		}
		for _, k := range body.Retired {
			fmt.Fprintf(w, "retired\t%s\t%s\t%s\n", k.Fingerprint, shortTS(k.GeneratedAt), shortTS(k.RetiredAt))
		}
		w.Flush()
	default:
		return fmt.Errorf("unknown --format %q — use json or table", *format)
	}
	return nil
}

// shortTS condenses an RFC 3339 timestamp to the first 19 characters
// (YYYY-MM-DDTHH:MM:SS) so table output stays narrow.
func shortTS(ts string) string {
	if len(ts) < 19 {
		return ts
	}
	return ts[:19]
}

// truncate clips s to at most n characters, appending a trailing
// ellipsis. Zero-alloc on the short path.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// hashSuffix returns the last 8 hex chars of a full-length hash so
// table rows stay compact. Returns the whole string if it is shorter
// than 8 chars (which should never happen for real hashes).
func hashSuffix(h string) string {
	if len(h) <= 8 {
		return h
	}
	return strings.ToLower(h[len(h)-8:])
}
