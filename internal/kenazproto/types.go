// Package kenazproto is the single privacy-filtering point between sigild's
// raw event.Event stream and the Kenaz app's observer-events push topic.
//
// It owns the KenazEvent wire type and the Serialize function that projects
// a raw daemon event into a privacy-filtered payload.  It is a leaf: it
// imports only sigil/internal/event and stdlib.  Nothing else daemon-internal
// may be imported here without violating the DAG.
//
// DAG position:  event → kenazproto → {collector, cmd/sigild}
package kenazproto

// KenazEvent is the privacy-filtered wire payload emitted on the
// "observer-events" and "vm-events" push topics.  Field names match the JSON
// keys consumed by the Kenaz frontend (spec 024 / spec 027 / spec 028).
//
// All string fields are bounded by the length caps in validateLengths.
// The zero value is valid and represents a dropped / unmapped event.
type KenazEvent struct {
	ID           int64  `json:"id"`
	Origin       string `json:"origin"`
	SourceID     string `json:"source_id"`
	Timestamp    int64  `json:"timestamp"`
	Kind         string `json:"kind"`
	Subject      string `json:"subject"`
	SubjectDim   string `json:"subject_dim"`
	SizeChip     string `json:"size_chip"`
	ContentClass string `json:"content_class"`
	// VMID carries the session UUID for events whose Origin is "vm:<uuid>".
	// It is populated by Serialize when evt.Source has the "vm:" prefix (spec
	// 028 Phase 6).  For host-origin events VMID is always "".  The topic-
	// server close-after predicate uses VMID to match vm.session_terminal
	// events to the subscribing client's vm_id.
	VMID string `json:"vm_id,omitempty"`
}

// Content-class constants describe the privacy treatment applied to the
// Subject field of a KenazEvent.  Defined in contract §Content classes.
const (
	// ContentClassMetadataOnly indicates the Subject carries only structural
	// metadata (path, process name, host) — no user-authored content.
	ContentClassMetadataOnly = "metadata-only"

	// ContentClassHash indicates the Subject carries only a cryptographic
	// hash of the original content (clipboard SHA-256 hex).
	ContentClassHash = "hash"

	// ContentClassTruncated indicates the Subject was truncated to fit the
	// byte cap; the original may have contained user-authored text.
	ContentClassTruncated = "truncated"

	// ContentClassRedacted indicates the field was blanked because it would
	// have carried user-authored content.
	ContentClassRedacted = "redacted"

	// ContentClassUnknown is a fallback for future kinds whose content class
	// has not yet been specified.
	ContentClassUnknown = "unknown"
)
