package payload

// TrainingTunePayload is written at the start AND the end of every
// LoRA fine-tune job per FR-009. Two rows land in the ledger per run;
// the `phase` field discriminates them. Compliance reviewers can
// reconstruct training cadence, loss curves (by pairing start/end
// rows via run_id), and failure triage from this payload alone
// without consulting the `finetune_runs` table.
//
// Fields:
//
//   - Phase           "start" | "end". Discriminates the two rows.
//   - RunID           uuidv4 of the finetune_runs row. Ties the
//     start/end pair together and lets auditors correlate with the
//     local `finetune_runs` table.
//   - BaseModelVer    semver of the base LLM the run fine-tuned.
//   - CorpusRowCount  number of rows selected from training_corpus
//     into this batch. Available at start AND end.
//   - Status          "running" at start; "complete"|"failed"|
//     "skipped" at end. Mirrors finetune_runs.status.
//   - DurationSec     elapsed wall-clock seconds. Zero on start rows.
//   - LossFinal       final loss reported by the training backend.
//     Zero on start rows and on failed runs where loss was never
//     produced.
//   - AdapterSHA256   SHA-256 of the output adapter file. Empty on
//     start rows and on failed runs.
//   - EmittedAt       RFC 3339 UTC timestamp of the emission.
type TrainingTunePayload struct {
	Phase          string  `json:"phase"`
	RunID          string  `json:"run_id"`
	BaseModelVer   string  `json:"base_model_ver"`
	CorpusRowCount int     `json:"corpus_row_count"`
	Status         string  `json:"status"`
	DurationSec    int64   `json:"duration_seconds"`
	LossFinal      float64 `json:"loss_final"`
	AdapterSHA256  string  `json:"adapter_sha256"`
	EmittedAt      string  `json:"emitted_at"`
}

// TrainingTunePhase values narrow the Phase discriminator so call
// sites don't stringly-type start/end.
const (
	TrainingTunePhaseStart = "start"
	TrainingTunePhaseEnd   = "end"
)
