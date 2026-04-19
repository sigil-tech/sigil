package kenazproto

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"github.com/sigil-tech/sigil/internal/event"
)

// droppedTotal counts events dropped by Serialize (oversize or unknown kind).
// Read via KenazEventDropped.
var droppedTotal atomic.Int64

// KenazEventDropped returns the cumulative count of events dropped by
// Serialize since process start.  Used by the FR-010d metric.
func KenazEventDropped() int64 {
	return droppedTotal.Load()
}

// SourceIDForKind is the single source of truth for the event.Kind → Kenaz
// UI source-row mapping.  Returns the sourceID string and true for all 12
// mapped kinds.  Returns ("", false) for all 10 unmapped kinds and for any
// future kind not yet in the table.
//
// Any call site that reimplements this mapping is a spec violation (contract
// §SourceIDForKind).
func SourceIDForKind(k event.Kind) (string, bool) {
	switch k {
	case event.KindFile, event.KindGit:
		return "filesystem", true
	case event.KindProcess, event.KindTerminal:
		return "process", true
	case event.KindClipboard:
		return "clipboard", true
	case event.KindNetwork:
		return "network", true
	case event.KindTyping:
		return "keystroke", true
	case event.KindHyprland, event.KindAppLifecycle,
		event.KindFocusMode, event.KindDesktop, event.KindAppState:
		return "app-context", true
	default:
		return "", false
	}
}

// Serialize projects a raw event.Event into a privacy-filtered KenazEvent.
//
// Returns (KenazEvent, true) for one of the 12 mapped kinds.
// Returns (zero-value, false) for:
//   - any of the 10 unmapped kinds (KindIdle, KindPointer, KindAudio,
//     KindPower, KindDisplay, KindScreenshot, KindDownload, KindCalendar,
//     KindBrowser, KindAI) — fail-closed per FR-010d.
//   - any future unknown kind (fail-closed per US4 Acceptance 2).
//   - any event whose serialized fields exceed the length caps.
//
// The function never panics.  Dropped events increment KenazEventDropped().
func Serialize(evt event.Event) (KenazEvent, bool) {
	sourceID, ok := SourceIDForKind(evt.Kind)
	if !ok {
		droppedTotal.Add(1)
		return KenazEvent{}, false
	}

	origin := evt.Source
	if origin == "" {
		origin = "host"
	}

	ke := KenazEvent{
		ID:        evt.ID,
		Origin:    origin,
		SourceID:  sourceID,
		Timestamp: evt.Timestamp.UnixMilli(),
		Kind:      string(evt.Kind),
	}

	switch evt.Kind {
	case event.KindFile:
		ke.Subject, ke.SubjectDim, ke.SizeChip, ke.ContentClass = serializeFile(evt.Payload)
	case event.KindGit:
		ke.Subject, ke.SubjectDim, ke.SizeChip, ke.ContentClass = serializeGit(evt.Payload)
	case event.KindProcess:
		ke.Subject, ke.SubjectDim, ke.SizeChip, ke.ContentClass = serializeProcess(evt.Payload)
	case event.KindTerminal:
		ke.Subject, ke.SubjectDim, ke.SizeChip, ke.ContentClass = serializeTerminal(evt.Payload)
	case event.KindClipboard:
		ke.Subject, ke.SubjectDim, ke.SizeChip, ke.ContentClass = serializeClipboard(evt.Payload)
	case event.KindNetwork:
		ke.Subject, ke.SubjectDim, ke.SizeChip, ke.ContentClass = serializeNetwork(evt.Payload)
	case event.KindTyping:
		ke.Subject, ke.SubjectDim, ke.SizeChip, ke.ContentClass = serializeTyping(evt.Payload)
	case event.KindHyprland:
		ke.Subject, ke.SubjectDim, ke.SizeChip, ke.ContentClass = serializeHyprland(evt.Payload)
	case event.KindAppLifecycle:
		ke.Subject, ke.SubjectDim, ke.SizeChip, ke.ContentClass = serializeAppLifecycle(evt.Payload)
	case event.KindFocusMode:
		ke.Subject, ke.SubjectDim, ke.SizeChip, ke.ContentClass = serializeFocusMode(evt.Payload)
	case event.KindDesktop:
		ke.Subject, ke.SubjectDim, ke.SizeChip, ke.ContentClass = serializeDesktop(evt.Payload)
	case event.KindAppState:
		ke.Subject, ke.SubjectDim, ke.SizeChip, ke.ContentClass = serializeAppState(evt.Payload)
	}

	normalizeFields(&ke)

	if err := validateLengths(&ke); err != nil {
		droppedTotal.Add(1)
		return KenazEvent{}, false
	}

	return ke, true
}

// ---------------------------------------------------------------------------
// Per-kind serializers
// ---------------------------------------------------------------------------

func serializeFile(p map[string]any) (subject, subjectDim, sizeChip, contentClass string) {
	path, _ := p["path"].(string)
	ext, _ := p["ext"].(string)
	delta, _ := p["delta"].(string)

	subject = fileSubjectFromPath(path)
	subjectDim = ext
	sizeChip = delta
	contentClass = ContentClassMetadataOnly
	return
}

func serializeGit(p map[string]any) (subject, subjectDim, sizeChip, contentClass string) {
	repo, _ := p["repo"].(string)
	op, _ := p["op"].(string)
	branch, _ := p["branch"].(string)
	hash, _ := p["hash"].(string)

	if op != "" {
		subject = repo + " " + op
	} else {
		subject = repo
	}

	subjectDim = capBytes(branch, 64)
	if len(hash) >= 7 {
		sizeChip = hash[:7]
	}
	contentClass = ContentClassMetadataOnly
	return
}

func serializeProcess(p map[string]any) (subject, subjectDim, sizeChip, contentClass string) {
	name, _ := p["name"].(string)
	// PID may be float64 (JSON), int, or int64.
	pidStr := ""
	switch v := p["pid"].(type) {
	case float64:
		pidStr = fmt.Sprintf("pid %d", int64(v))
	case int:
		pidStr = fmt.Sprintf("pid %d", v)
	case int64:
		pidStr = fmt.Sprintf("pid %d", v)
	}

	if pidStr != "" {
		subject = name + " " + pidStr
	} else {
		subject = name
	}
	subjectDim = pidStr
	sizeChip = ""
	contentClass = ContentClassMetadataOnly
	return
}

func serializeTerminal(p map[string]any) (subject, subjectDim, sizeChip, contentClass string) {
	cmd, _ := p["cmd"].(string)
	// argv[0] only — split on any ASCII whitespace before normalization so
	// that inputs like "bash\n--norc\r\n-i" do not leak argv into Subject.
	if idx := strings.IndexAny(cmd, " \t\n\r"); idx >= 0 {
		subject = cmd[:idx]
	} else {
		subject = cmd
	}
	subjectDim = ""

	exitCode, hasExit := event.ExitCodeFromPayload(p)
	if hasExit {
		sizeChip = fmt.Sprintf("exit %d", exitCode)
	}
	contentClass = ContentClassMetadataOnly
	return
}

func serializeClipboard(p map[string]any) (subject, subjectDim, sizeChip, contentClass string) {
	hash, _ := p["hash"].(string)
	subject = hash

	size := ""
	switch v := p["size"].(type) {
	case float64:
		size = fmt.Sprintf("%d B", int64(v))
	case int:
		size = fmt.Sprintf("%d B", v)
	case int64:
		size = fmt.Sprintf("%d B", v)
	case string:
		size = v
	}

	subjectDim = ""
	sizeChip = size
	contentClass = ContentClassHash
	return
}

func serializeNetwork(p map[string]any) (subject, subjectDim, sizeChip, contentClass string) {
	host, _ := p["host"].(string)
	port, _ := p["port"].(string)
	proto, _ := p["proto"].(string)

	if port != "" {
		subject = host + ":" + port
	} else {
		subject = host
	}
	subjectDim = proto
	sizeChip = ""
	contentClass = ContentClassMetadataOnly
	return
}

func serializeTyping(p map[string]any) (subject, subjectDim, sizeChip, contentClass string) {
	cadence, _ := p["cadence"].(string)
	subject = cadence
	subjectDim = ""
	sizeChip = ""
	contentClass = ContentClassMetadataOnly
	return
}

func serializeHyprland(p map[string]any) (subject, subjectDim, sizeChip, contentClass string) {
	title, _ := p["title"].(string)
	ws, _ := p["workspace"].(string)
	bin, _ := p["bin"].(string)

	truncated := false
	subject, truncated = capBytesWithFlag(title, 64)

	if ws != "" {
		subjectDim = "ws " + ws
	} else {
		subjectDim = bin
	}
	sizeChip = ""

	if truncated {
		contentClass = ContentClassTruncated
	} else {
		contentClass = ContentClassMetadataOnly
	}
	return
}

func serializeAppLifecycle(p map[string]any) (subject, subjectDim, sizeChip, contentClass string) {
	app, _ := p["app"].(string)
	lifecycleEvent, _ := p["event"].(string)

	if lifecycleEvent != "" {
		subject = app + " " + lifecycleEvent
	} else {
		subject = app
	}
	subjectDim = ""
	sizeChip = ""
	contentClass = ContentClassMetadataOnly
	return
}

func serializeFocusMode(p map[string]any) (subject, subjectDim, sizeChip, contentClass string) {
	state, _ := p["state"].(string)
	subject = state
	subjectDim = ""
	sizeChip = ""
	contentClass = ContentClassMetadataOnly
	return
}

func serializeDesktop(p map[string]any) (subject, subjectDim, sizeChip, contentClass string) {
	id, _ := p["id"].(string)
	name, _ := p["name"].(string)

	if name != "" {
		subject = name
	} else if id != "" {
		subject = "desktop " + id
	}
	subjectDim = ""
	sizeChip = ""
	contentClass = ContentClassMetadataOnly
	return
}

func serializeAppState(p map[string]any) (subject, subjectDim, sizeChip, contentClass string) {
	app, _ := p["app"].(string)
	state, _ := p["state"].(string)

	if state != "" {
		subject = app + " " + state
	} else {
		subject = app
	}
	subjectDim = ""
	sizeChip = ""
	contentClass = ContentClassMetadataOnly
	return
}

// ---------------------------------------------------------------------------
// Normalization (contract §Normalization rules)
// ---------------------------------------------------------------------------

// normalizeFields applies UTF-8 validation, control-char replacement, and
// newline/tab replacement to all string fields in a KenazEvent.
func normalizeFields(ke *KenazEvent) {
	ke.Origin = normalizeString(ke.Origin)
	ke.SourceID = normalizeString(ke.SourceID)
	ke.Kind = normalizeString(ke.Kind)
	ke.Subject = normalizeString(ke.Subject)
	ke.SubjectDim = normalizeString(ke.SubjectDim)
	ke.SizeChip = normalizeString(ke.SizeChip)
	ke.ContentClass = normalizeString(ke.ContentClass)
}

// normalizeString cleans a single string field:
//  1. Replace invalid UTF-8 sequences with U+FFFD.
//  2. Replace \n, \r, \t with a single space.
//  3. Replace remaining control characters (0x00–0x1F, 0x7F) with '?'.
//
// Fast path: if the string contains no bytes requiring replacement, the
// original string is returned without any allocation.
func normalizeString(s string) string {
	// Fast path: scan for any byte that needs attention.
	needsWork := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x80 {
			// ASCII: check for control characters.
			if c < 0x20 || c == 0x7F {
				needsWork = true
				break
			}
		} else {
			// High byte: validate UTF-8 sequence.
			_, size := utf8.DecodeRuneInString(s[i:])
			if size == 1 {
				// utf8.RuneError with size 1 means invalid byte.
				needsWork = true
				break
			}
			i += size - 1
		}
	}
	if !needsWork {
		return s
	}

	// Slow path: rebuild the string with replacements.
	// Combine UTF-8 validation and control-char replacement in one pass.
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		if r == utf8.RuneError && size == 1 {
			// Invalid UTF-8 byte.
			b.WriteRune('\uFFFD')
			continue
		}
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteByte(' ')
		case r < 0x20 || r == 0x7F:
			b.WriteByte('?')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Length validator (contract §Length validator)
// ---------------------------------------------------------------------------

// validateLengths checks every string field against the caps defined in the
// contract.  Returns a non-nil error if any field is oversize.
func validateLengths(e *KenazEvent) error {
	if len(e.Subject) > 256 {
		return fmt.Errorf("subject oversize: %d", len(e.Subject))
	}
	if len(e.SubjectDim) > 256 {
		return fmt.Errorf("subject_dim oversize: %d", len(e.SubjectDim))
	}
	if len(e.SizeChip) > 32 {
		return fmt.Errorf("size_chip oversize: %d", len(e.SizeChip))
	}
	if len(e.Origin) > 64 {
		return fmt.Errorf("origin oversize: %d", len(e.Origin))
	}
	if len(e.SourceID) > 16 {
		return fmt.Errorf("source_id oversize: %d", len(e.SourceID))
	}
	if len(e.Kind) > 32 {
		return fmt.Errorf("kind oversize: %d", len(e.Kind))
	}
	if len(e.ContentClass) > 16 {
		return fmt.Errorf("content_class oversize: %d", len(e.ContentClass))
	}
	return nil
}

// ---------------------------------------------------------------------------
// Truncation helpers
// ---------------------------------------------------------------------------

// capBytes truncates s to at most max bytes at a valid UTF-8 boundary.
func capBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = strings.ToValidUTF8(s, "\uFFFD")
	if len(s) <= max {
		return s
	}
	b := []byte(s)[:max]
	// Back off to nearest valid UTF-8 boundary.
	for !utf8.Valid(b) {
		b = b[:len(b)-1]
	}
	return string(b)
}

// capBytesWithFlag truncates s to at most max bytes and returns whether
// truncation actually occurred.
func capBytesWithFlag(s string, max int) (result string, truncated bool) {
	if len(s) <= max {
		return s, false
	}
	return capBytes(s, max), true
}

// fileSubjectFromPath derives the privacy-filtered Subject for KindFile events.
//
// Algorithm (per ADR-027a):
//  1. Clean the path with filepath.Clean.
//  2. Split on filepath.Separator; discard empty segments (handles leading /).
//  3. Take the last two segments, joined with "/".
//  4. Prefix "…/" (U+2026 + /) when the original post-Clean depth exceeds 2.
//
// Edge cases:
//   - Empty input → "".
//   - Single component → that component (no prefix).
//   - Two components → "parent/filename" (no prefix).
//   - Root-only ("/") → "".
//
// The join separator is always "/" regardless of OS. On Windows (future scope),
// filepath.Separator is '\' but the display join remains '/'. No algorithm
// change is needed for Windows; the split-on-separator, join-with-slash rule
// handles both platforms correctly.
//
// The implementation uses a single-pass scan to avoid heap allocation on the
// hot path: rather than building a slice of all components, it records only the
// start indices of the last two non-empty segments.
func fileSubjectFromPath(rawPath string) string {
	if rawPath == "" {
		return ""
	}
	p := filepath.Clean(rawPath)
	sep := filepath.Separator

	// Single-pass scan: track positions of the last two segment boundaries.
	// prev2Start/prev1Start are byte offsets into p where the penultimate and
	// last segment begin.  n counts non-empty segments.
	n := 0
	prev2Start, prev1Start := -1, -1
	i := 0
	for i < len(p) {
		// Skip separator(s) and the leading root slash.
		for i < len(p) && rune(p[i]) == sep {
			i++
		}
		if i >= len(p) {
			break
		}
		// Mark start of this segment.
		start := i
		// Advance to next separator or end.
		for i < len(p) && rune(p[i]) != sep {
			i++
		}
		// Record last two start positions.
		n++
		prev2Start = prev1Start
		prev1Start = start
	}

	switch n {
	case 0:
		return ""
	case 1:
		return p[prev1Start:]
	case 2:
		return p[prev2Start:prev1Start-1] + "/" + p[prev1Start:]
	default:
		// n > 2: emit "…/<penultimate>/<last>".
		return "…/" + p[prev2Start:prev1Start-1] + "/" + p[prev1Start:]
	}
}

// truncatePathLeft truncates path to at most max bytes, keeping the rightmost
// bytes (filename + nearest parent dirs).  This is a left-truncation: the
// meaningful end of the path is preserved.
func truncatePathLeft(path string, max int) string {
	if len(path) <= max {
		return path
	}
	path = strings.ToValidUTF8(path, "\uFFFD")
	if len(path) <= max {
		return path
	}
	// Keep the rightmost max bytes.  Back off from the left to a valid
	// UTF-8 boundary (the right end is naturally valid since we encoded
	// all invalid sequences, so we only need to ensure we don't start mid-rune).
	b := []byte(path)
	start := len(b) - max
	// Advance start forward until it lands on a UTF-8 leading byte.
	for start < len(b) && !utf8.RuneStart(b[start]) {
		start++
	}
	return string(b[start:])
}
