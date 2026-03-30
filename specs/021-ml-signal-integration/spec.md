# 021 — ML Signal Integration

**Status:** Draft
**Author:** Alec Feeman
**Date:** 2026-03-30

---

## Problem

sigild currently uses two separate mechanisms for generating suggestions:

1. **Go heuristic pattern detectors** (21 hardcoded detectors in `internal/analyzer/patterns.go`) that fire at fixed thresholds — e.g., context switching > 6/hour, stuck on file > 5 edits in 15 minutes. These thresholds don't adapt to individual users. A user who naturally context-switches heavily gets false alarms; a user who rarely switches gets no warning even when they're genuinely scattered.

2. **Hourly LLM summaries** (cloud pass in `internal/analyzer/analyzer.go`) where the entire event summary is sent to the LLM and it generates prose insights. This is expensive (full LLM inference call), unpredictable (the LLM may or may not notice patterns), and periodic (hourly, regardless of whether anything interesting happened).

Meanwhile, sigil-ml (the ML sidecar) already writes predictions to `ml_predictions` — but these are only used in two narrow places: the stuck model gates LLM calls on task transitions, and the workflow state prevents interrupting deep work. The ML models don't generate suggestions directly.

Spec 005 (sigil-ml) introduces an ML Signal Pipeline that emits structured, event-driven signals to a new `ml_signals` table. These signals replace the Go heuristics with personalized, learned pattern detection. This spec defines how sigild reads those signals and renders them into suggestions via the LLM.

## Goals

1. **Read ML signals from `ml_signals` and route high-confidence signals to the LLM** for rendering into human-readable suggestions with optional action commands.
2. **Replace the hourly LLM cloud pass** with on-demand, signal-triggered LLM calls — only call the LLM when the ML models detect something worth saying.
3. **Keep Go heuristics as fallback** when sigil-ml is unavailable or during cold start, so the system degrades gracefully.
4. **Pipe suggestion feedback back** so sigil-ml can use accepted/dismissed outcomes as training labels.
5. **Include the user's behavior profile** (from sigil-ml) as LLM context so rendered suggestions reference only tools and workflows the user actually uses.

## Non-Goals

- Training or running ML models in Go — that stays in sigil-ml (Python).
- Changing the notification UI or notification levels — those are handled by the existing notifier.
- Modifying sigilctl — signals are an internal mechanism, not user-facing CLI.

## Design

### Signal Reader

A new `SignalReader` in `internal/ml/` polls the `ml_signals` table for new, unexpired signals above a configurable confidence threshold. It runs as a background goroutine alongside the existing analyzer, on a short interval (5 seconds).

```
ml_signals table (written by sigil-ml, read by sigild)
    │
    SignalReader (polls every 5s, filters by confidence + expiry)
    │
    ├── High confidence signal → LLM rendering → notifier.Surface()
    ├── Medium confidence signal → store as pending, batch for digest
    └── Low confidence signal → store only (analytics, training labels)
```

### LLM Rendering

When a signal passes the confidence gate, the SignalReader constructs a focused LLM prompt containing:

- The signal's structured evidence (deviation from baseline, predicted vs actual behavior)
- The user's behavior profile summary (top tools, typical cadence, active plugins)
- A directive: "Turn this signal into one actionable sentence. Reference only tools the user uses. If an action command is appropriate, include it."

This replaces the current hourly cloud pass. Instead of "summarize my last hour of work," the LLM receives "the ML models detected X specific thing — explain it to the user."

### Heuristic Fallback

The existing Go pattern detectors (`internal/analyzer/patterns.go`) continue running. When `ml_signals` has recent entries (sigil-ml is active), heuristic suggestions are suppressed for any pattern category that the ML signals already cover. When `ml_signals` is stale (no entries in the last 5 minutes), heuristics are unsuppressed and operate normally.

This ensures:
- Fresh sigil-ml install: heuristics run while ML models are cold-starting
- sigil-ml crashes: heuristics resume within 5 minutes
- sigil-ml running: ML signals take priority, heuristics only fire for gaps

### Feedback Loop

When a suggestion derived from an ML signal is accepted, dismissed, or ignored:

1. The suggestion's `resolved_at` and `status` are already written by the notifier
2. A new `signal_id` column on the `suggestions` table links back to the originating `ml_signals` row
3. sigil-ml reads this linkage during training to use as labels

### Profile Context

sigil-ml writes the user's behavior profile to a row in `ml_predictions` with model name `"profile"`. The SignalReader reads this alongside signals to include in the LLM prompt context.

## Schema Changes

### New table: `ml_signals` (owned by Python, read by Go)

```sql
CREATE TABLE IF NOT EXISTS ml_signals (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    signal_type     TEXT    NOT NULL,   -- model-generated type (not hardcoded enum)
    confidence      REAL    NOT NULL,   -- 0.0 to 1.0
    evidence        TEXT    NOT NULL,   -- JSON: observed values, baseline, deviation
    suggested_action TEXT,              -- generic action hint for LLM
    created_at      INTEGER NOT NULL,   -- unix ms
    expires_at      INTEGER,            -- unix ms, NULL = no expiry
    rendered        INTEGER NOT NULL DEFAULT 0,  -- 1 = LLM has processed this signal
    suggestion_id   INTEGER             -- FK to suggestions.id after rendering
);
CREATE INDEX IF NOT EXISTS idx_ml_signals_created ON ml_signals(created_at);
CREATE INDEX IF NOT EXISTS idx_ml_signals_rendered ON ml_signals(rendered);
```

### Modified table: `suggestions`

```sql
ALTER TABLE suggestions ADD COLUMN signal_id INTEGER REFERENCES ml_signals(id);
```

## Changes Required

### `internal/ml/signal_reader.go` (new)

- `SignalReader` struct with `Run(ctx)` goroutine
- Polls `ml_signals` for `rendered = 0 AND confidence >= threshold AND (expires_at IS NULL OR expires_at > now)`
- Marks signals as `rendered = 1` after processing
- Routes to LLM or stores directly based on confidence level

### `internal/ml/signal_reader.go` — LLM prompt construction

- Reads user profile from `ml_predictions` where `model = 'profile'`
- Builds focused prompt: signal evidence + profile + rendering directive
- Calls `inference.Engine.Complete()` with a signal-specific system prompt
- Surfaces result via `notifier.Surface()` with `signal_id` linkage

### `internal/analyzer/analyzer.go` — suppress heuristics when ML active

- Check `ml_signals` freshness before running heuristic detection
- If fresh ML signals exist (within last 5 minutes), skip heuristic patterns
- Log suppression for observability

### `internal/analyzer/analyzer.go` — remove hourly cloud pass

- Make the cloud pass (LLM summary) conditional: only run if no ML signals were processed in this cycle
- Eventually deprecate entirely once ML signal coverage is proven

### `internal/store/store.go` — schema and queries

- Add `ml_signals` table creation to schema bootstrap
- Add `QueryNewSignals(ctx, minConfidence) -> []Signal`
- Add `MarkSignalRendered(ctx, signalID, suggestionID)`
- Add `signal_id` column to suggestions table
- Add `QuerySignalFeedback(ctx, since) -> []Feedback` (for sigil-ml training reads)

### `cmd/sigild/main.go` — wire SignalReader

- Initialize `SignalReader` alongside analyzer
- Pass inference engine and notifier references
- Start as background goroutine

## Rollout

1. **Phase 1**: Add `ml_signals` table, SignalReader, and LLM rendering. Heuristics continue running in parallel. ML signals produce suggestions alongside heuristic suggestions.
2. **Phase 2**: Add suppression logic — when ML signals are fresh, suppress overlapping heuristic patterns. Cloud pass becomes signal-triggered only.
3. **Phase 3**: Remove hourly cloud pass entirely. Heuristics remain as fallback only.

## Dependencies

- **sigil-ml Feature 005** (ML Signal Pipeline) must be implemented first — it writes the `ml_signals` table and behavior profile that this feature reads.
- Existing inference engine (local or cloud LLM) must be operational for signal rendering.

## Risks

- **LLM latency**: Signal-triggered LLM calls must not block the SignalReader. Use async processing with a bounded queue.
- **Signal volume**: If ML models emit too many signals, LLM costs spike. Confidence gating and rate limiting (existing notifier rate limits) mitigate this.
- **Feedback sparsity**: Most suggestions are ignored (836/950 currently pending). Need to treat "ignored" as a weak negative signal, not discard it.
- **Profile staleness**: If sigil-ml stops updating the profile, the LLM may use stale context. Include a freshness check on the profile data.
