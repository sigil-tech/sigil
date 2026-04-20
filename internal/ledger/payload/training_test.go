package payload

import "testing"

func TestTrainingPayloadAllowlist(t *testing.T) {
	t.Run("TrainingTunePayload field set", func(t *testing.T) {
		assertMarshalledKeys(t, TrainingTunePayload{
			Phase:          TrainingTunePhaseStart,
			RunID:          "run-1",
			BaseModelVer:   "llama-3-8b-q4",
			CorpusRowCount: 100,
			Status:         "running",
			DurationSec:    0,
			LossFinal:      0,
			AdapterSHA256:  "",
			EmittedAt:      "2026-04-19T00:00:00Z",
		}, []string{
			"phase", "run_id", "base_model_ver", "corpus_row_count",
			"status", "duration_seconds", "loss_final",
			"adapter_sha256", "emitted_at",
		})
	})

	t.Run("phase discriminator constants are stable", func(t *testing.T) {
		if TrainingTunePhaseStart != "start" || TrainingTunePhaseEnd != "end" {
			t.Fatalf("phase constants drifted — audit viewers depend on literal values")
		}
	})
}
