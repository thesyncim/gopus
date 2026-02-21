package encoder

import "testing"

func TestSetPredictionDisabledPropagatesToSubEncoders(t *testing.T) {
	enc := NewEncoder(48000, 2)

	enc.SetPredictionDisabled(true)
	enc.ensureSILKEncoder()
	enc.ensureSILKSideEncoder()
	enc.ensureCELTEncoder()

	if !enc.silkEncoder.ReducedDependency() {
		t.Fatal("silkEncoder should have reduced dependency when prediction is disabled")
	}
	if enc.silkSideEncoder == nil || !enc.silkSideEncoder.ReducedDependency() {
		t.Fatal("silkSideEncoder should have reduced dependency when prediction is disabled")
	}
	if got := enc.celtEncoder.Prediction(); got != 0 {
		t.Fatalf("celtEncoder prediction mode = %d, want 0 when prediction is disabled", got)
	}

	enc.SetPredictionDisabled(false)
	if enc.silkEncoder.ReducedDependency() {
		t.Fatal("silkEncoder reduced dependency should be disabled")
	}
	if enc.silkSideEncoder.ReducedDependency() {
		t.Fatal("silkSideEncoder reduced dependency should be disabled")
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
	if !enc.silkEncoder.ReducedDependency() {
		t.Fatal("silkEncoder should keep reduced dependency after Reset()")
	}
	if got := enc.celtEncoder.Prediction(); got != 0 {
		t.Fatalf("celtEncoder prediction mode after Reset() = %d, want 0", got)
	}
}
