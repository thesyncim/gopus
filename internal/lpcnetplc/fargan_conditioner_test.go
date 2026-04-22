package lpcnetplc

import (
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
)

func makeFARGANConditionerTestBlob() []byte {
	var blob []byte
	for _, spec := range FARGANConditionerLayerSpecs() {
		if spec.Bias != "" {
			blob = appendTestBlobRecord(blob, spec.Bias, dnnblob.TypeFloat, 4*spec.NbOutputs)
		}
		if spec.Subias != "" {
			blob = appendTestBlobRecord(blob, spec.Subias, dnnblob.TypeFloat, 4*spec.NbOutputs)
		}
		if spec.Scale != "" {
			blob = appendTestBlobRecord(blob, spec.Scale, dnnblob.TypeFloat, 4*spec.NbOutputs)
		}
		if spec.FloatWeights != "" {
			blob = appendTestBlobRecord(blob, spec.FloatWeights, dnnblob.TypeFloat, 4*spec.NbInputs*spec.NbOutputs)
		}
	}
	return blob
}

func newFARGANConditionerForTest(t *testing.T) *FARGANConditioner {
	t.Helper()
	blob, err := dnnblob.Clone(makeFARGANConditionerTestBlob())
	if err != nil {
		t.Fatalf("dnnblob.Clone error: %v", err)
	}
	var conditioner FARGANConditioner
	if err := conditioner.SetModel(blob); err != nil {
		t.Fatalf("FARGANConditioner.SetModel error: %v", err)
	}
	return &conditioner
}

func TestFARGANConditionerDoesNotAllocate(t *testing.T) {
	conditioner := newFARGANConditionerForTest(t)
	var features [NumFeatures]float32
	var cond [FARGANCondDense2Size]float32
	for i := range features {
		features[i] = float32(i-7) / 9
	}
	allocs := testing.AllocsPerRun(100, func() {
		if n := conditioner.Compute(cond[:], features[:]); n != FARGANCondDense2Size {
			t.Fatalf("Compute()=%d want %d", n, FARGANCondDense2Size)
		}
	})
	if allocs != 0 {
		t.Fatalf("Compute allocs/run=%v want 0", allocs)
	}
}

func TestFARGANConditionerRetainsState(t *testing.T) {
	conditioner := newFARGANConditionerForTest(t)
	var features [NumFeatures]float32
	var cond [FARGANCondDense2Size]float32
	for i := range features {
		features[i] = float32((i%7)-3) / 8
	}
	period := 71
	if n := conditioner.ComputeWithPeriod(cond[:], features[:], period); n != FARGANCondDense2Size {
		t.Fatalf("ComputeWithPeriod()=%d want %d", n, FARGANCondDense2Size)
	}
	if conditioner.LastPeriod() != period {
		t.Fatalf("LastPeriod()=%d want %d", conditioner.LastPeriod(), period)
	}
	var state [FARGANCondConv1State]float32
	if n := conditioner.FillCondConv1State(state[:]); n != FARGANCondConv1State {
		t.Fatalf("FillCondConv1State()=%d want %d", n, FARGANCondConv1State)
	}
	conditioner.Reset()
	if conditioner.LastPeriod() != 0 {
		t.Fatalf("LastPeriod after Reset()=%d want 0", conditioner.LastPeriod())
	}
	clear(state[:])
	conditioner.FillCondConv1State(state[:])
	for i, v := range state[:] {
		if v != 0 {
			t.Fatalf("state[%d]=%v want 0 after Reset()", i, v)
		}
	}
}

func TestPeriodFromFeatures(t *testing.T) {
	var features [NumFeatures]float32
	if got := PeriodFromFeatures(features[:]); got != 91 {
		t.Fatalf("PeriodFromFeatures(zero)=%d want 91", got)
	}
	features[NumBands] = -1.5
	if got := PeriodFromFeatures(features[:]); got != PitchMaxPeriod {
		t.Fatalf("PeriodFromFeatures(min)=%d want %d", got, PitchMaxPeriod)
	}
	features[NumBands] = 3
	if got := PeriodFromFeatures(features[:]); got != 11 {
		t.Fatalf("PeriodFromFeatures(high)=%d want 11", got)
	}
}
