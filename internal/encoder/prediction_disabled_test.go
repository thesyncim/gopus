package encoder

import "testing"

func TestSetPredictionDisabledPropagatesToSubEncoders(t *testing.T) {
	enc := NewEncoder(48000, 2)

	enc.SetPredictionDisabled(true)
	enc.ensureSILKEncoder()
	enc.ensureCELTEncoder()

	// silk_mode.reducedDependency follows the prediction control for every
	// SILK frame (src/opus_encoder.c).
	enc.configureSILKMode(960, 32000, 1275*8, false)
	if !enc.silkMode.ReducedDependency {
		t.Fatal("silk_mode.reducedDependency should be set when prediction is disabled")
	}
	if got := enc.celtEncoder.Prediction(); got != 0 {
		t.Fatalf("celtEncoder prediction mode = %d, want 0 when prediction is disabled", got)
	}

	enc.SetPredictionDisabled(false)
	enc.configureSILKMode(960, 32000, 1275*8, false)
	if enc.silkMode.ReducedDependency {
		t.Fatal("silk_mode.reducedDependency should be cleared")
	}
	if got := enc.celtEncoder.Prediction(); got != 2 {
		t.Fatalf("celtEncoder prediction mode = %d, want 2 when prediction is enabled", got)
	}
}

func TestSetPredictionDisabledPersistsAcrossReset(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetPredictionDisabled(true)
	enc.ensureSILKEncoder()
	enc.ensureCELTEncoder()

	enc.Reset()

	if !enc.PredictionDisabled() {
		t.Fatal("PredictionDisabled should remain true after Reset()")
	}
	enc.configureSILKMode(960, 32000, 1275*8, false)
	if !enc.silkMode.ReducedDependency {
		t.Fatal("silk_mode.reducedDependency should stay set after Reset()")
	}
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
