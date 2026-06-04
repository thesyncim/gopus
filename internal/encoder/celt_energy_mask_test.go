package encoder

import (
	"testing"
	"unsafe"

	"github.com/thesyncim/gopus/internal/celt"
)

func TestCELTEnergyMaskUsesFloat32Storage(t *testing.T) {
	enc := NewEncoder(48000, 2)
	mask := make([]float32, 2*celt.MaxBands)
	for i := range mask {
		mask[i] = float32(i)*0.01 + 0.00000013
	}

	enc.SetCELTEnergyMask(mask)
	if got := unsafe.Sizeof(enc.celtEnergyMask[0]); got != 4 {
		t.Fatalf("celtEnergyMask element size=%d want celt_glog-sized 4", got)
	}

	got := enc.CELTEnergyMask()
	if len(got) != len(mask) {
		t.Fatalf("CELTEnergyMask len=%d want %d", len(got), len(mask))
	}
	for i := range mask {
		if got[i] != mask[i] {
			t.Fatalf("CELTEnergyMask[%d]=%0.10g want %0.10g", i, got[i], mask[i])
		}
	}

	got[0] = 99
	if again := enc.CELTEnergyMask()[0]; again == 99 {
		t.Fatalf("CELTEnergyMask returned an alias")
	}
}

func TestCELTEnergyMaskSyncsRoundedValuesToCELT(t *testing.T) {
	enc := NewEncoder(48000, 2)
	mask := make([]float32, 2*celt.MaxBands)
	for i := range mask {
		mask[i] = -0.75 + float32(i)*0.03125 + 0.00000019
	}

	enc.SetCELTEnergyMask(mask)
	enc.ensureCELTEncoder()
	got := enc.celtEncoder.EnergyMask()
	if len(got) != len(mask) {
		t.Fatalf("CELT EnergyMask len=%d want %d", len(got), len(mask))
	}
	for i := range mask {
		if got[i] != mask[i] {
			t.Fatalf("CELT EnergyMask[%d]=%0.10g want %0.10g", i, got[i], mask[i])
		}
	}

	enc.SetCELTEnergyMask(mask[:celt.MaxBands-1])
	if got := len(enc.CELTEnergyMask()); got != 0 {
		t.Fatalf("invalid mask kept top-level length=%d", got)
	}
	if got := len(enc.celtEncoder.EnergyMask()); got != 0 {
		t.Fatalf("invalid mask kept CELT length=%d", got)
	}
}
