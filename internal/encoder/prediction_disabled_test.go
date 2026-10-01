package encoder

import "testing"

// encodeCELTFrameForPrediction codes one 20 ms CELT-only frame.
func encodeCELTFrameForPrediction(t *testing.T, enc *Encoder) {
	t.Helper()
	pcm := make([]float32, 960*int(enc.channels))
	for i := range pcm {
		pcm[i] = float32(i%97) / 400
	}
	if _, err := enc.Encode(pcm, 960); err != nil {
		t.Fatalf("Encode: %v", err)
	}
}

// TestSetPredictionDisabledPropagatesToSubEncoders pins OPUS_SET_PREDICTION_DISABLED:
// it sets silk_mode.reducedDependency, which SILK reads on every frame, and
// CELT takes it through CELT_SET_PREDICTION at the next CELT or hybrid frame
// (src/opus_encoder.c:2288-2295).
func TestSetPredictionDisabledPropagatesToSubEncoders(t *testing.T) {
	enc := NewEncoder(48000, 2)
	enc.SetMode(ModeCELT)
	enc.ensureSILKEncoder()
	enc.ensureCELTEncoder()

	enc.SetPredictionDisabled(true)
	enc.configureSILKMode(ModeSILK, 960, 1276, 32000, 1275*8, false)
	if !enc.silkMode.ReducedDependency {
		t.Fatal("silk_mode.reducedDependency should be set when prediction is disabled")
	}
	if got := enc.celtEncoder.Prediction(); got != 2 {
		t.Fatalf("celtEncoder prediction mode before the next CELT frame = %d, want 2", got)
	}
	encodeCELTFrameForPrediction(t, enc)
	if got := enc.celtEncoder.Prediction(); got != 0 {
		t.Fatalf("celtEncoder prediction mode = %d, want 0 when prediction is disabled", got)
	}

	enc.SetPredictionDisabled(false)
	enc.configureSILKMode(ModeSILK, 960, 1276, 32000, 1275*8, false)
	if enc.silkMode.ReducedDependency {
		t.Fatal("silk_mode.reducedDependency should be cleared")
	}
	encodeCELTFrameForPrediction(t, enc)
	if got := enc.celtEncoder.Prediction(); got != 2 {
		t.Fatalf("celtEncoder prediction mode = %d, want 2 when prediction is enabled", got)
	}
}

func TestSetPredictionDisabledPersistsAcrossReset(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeCELT)
	enc.SetPredictionDisabled(true)
	enc.ensureSILKEncoder()
	enc.ensureCELTEncoder()

	enc.Reset()

	if !enc.PredictionDisabled() {
		t.Fatal("PredictionDisabled should remain true after Reset()")
	}
	enc.configureSILKMode(ModeSILK, 960, 1276, 32000, 1275*8, false)
	if !enc.silkMode.ReducedDependency {
		t.Fatal("silk_mode.reducedDependency should stay set after Reset()")
	}
	encodeCELTFrameForPrediction(t, enc)
	if got := enc.celtEncoder.Prediction(); got != 0 {
		t.Fatalf("celtEncoder prediction mode after Reset() = %d, want 0", got)
	}
}

func TestSetPhaseInversionDisabledPropagatesToLazyCELTEncoder(t *testing.T) {
	enc := NewEncoder(48000, 2)
	enc.SetMode(ModeCELT)
	enc.SetPhaseInversionDisabled(true)

	if enc.celtEncoder != nil {
		t.Fatal("SetPhaseInversionDisabled should not eagerly create the CELT encoder")
	}

	enc.ensureCELTEncoder()

	if enc.celtEncoder == nil {
		t.Fatal("ensureCELTEncoder should initialize the CELT encoder")
	}
	if !enc.celtEncoder.PhaseInversionDisabled() {
		t.Fatal("lazy CELT encoder did not inherit disabled phase inversion")
	}

	enc.SetPhaseInversionDisabled(false)
	if enc.celtEncoder.PhaseInversionDisabled() {
		t.Fatal("CELT encoder phase inversion flag should follow later control changes")
	}
}

func TestSetPhaseInversionDisabledRestrictedSilkNoop(t *testing.T) {
	enc := NewEncoder(48000, 2)
	enc.SetRestrictedSilkApplication(true)

	enc.SetPhaseInversionDisabled(true)

	if enc.PhaseInversionDisabled() {
		t.Fatal("restricted SILK should not report disabled phase inversion")
	}
}
