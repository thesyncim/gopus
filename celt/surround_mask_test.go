package celt

import "testing"

func TestEncoderSetEnergyMask(t *testing.T) {
	enc := NewEncoder(2)

	mask := make([]float64, 2*MaxBands)
	for i := range mask {
		mask[i] = float64(i) * 0.01
	}
	enc.SetEnergyMask(mask)
	got := enc.EnergyMask()
	if len(got) != len(mask) {
		t.Fatalf("EnergyMask len=%d want=%d", len(got), len(mask))
	}
	for i := range mask {
		if got[i] != mask[i] {
			t.Fatalf("EnergyMask[%d]=%f want=%f", i, got[i], mask[i])
		}
	}

	enc.SetEnergyMask(mask[:MaxBands-1])
	if len(enc.EnergyMask()) != 0 {
		t.Fatalf("invalid-size mask should clear state, got len=%d", len(enc.EnergyMask()))
	}
}

func TestComputeSurroundDynallocFromMask(t *testing.T) {
	enc := NewEncoder(2)
	enc.lastCodedBands = 17

	mask := make([]float64, 2*MaxBands)
	for i := 0; i < MaxBands; i++ {
		mask[i] = -2.0
		mask[MaxBands+i] = -2.0
	}
	mask[5] = 0
	mask[6] = 0
	mask[MaxBands+5] = 0
	mask[MaxBands+6] = 0
	enc.SetEnergyMask(mask)

	out := make([]float64, MaxBands)
	trim, ok := enc.computeSurroundDynallocFromMask(MaxBands, out)
	if !ok {
		t.Fatalf("computeSurroundDynallocFromMask returned ok=false")
	}
	if trim == 0 {
		t.Fatalf("expected non-zero surround trim from mask")
	}
	nonZero := 0
	for i := 0; i < MaxBands; i++ {
		if out[i] > 0 {
			nonZero++
		}
	}
	if nonZero == 0 {
		t.Fatalf("expected non-zero surround dynalloc bands")
	}
}
