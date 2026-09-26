//go:build gopus_qext

package celt

import "testing"

func TestQEXTEnergyHistorySurvivesControlToggleAndResetsAllChannels(t *testing.T) {
	e := NewEncoder(2)
	e.SetQEXTEnabled(true)
	history := e.ensureQEXTOldBandE(2)
	for i := range history {
		history[i] = celtGLog(float32(i+1) * 0.125)
	}

	e.SetQEXTEnabled(false)
	mono := e.ensureQEXTOldBandE(1)
	if len(mono) != MaxBands || len(e.qext.oldBandE) != 2*MaxBands {
		t.Fatalf("mono view=%d stored history=%d; want %d and %d", len(mono), len(e.qext.oldBandE), MaxBands, 2*MaxBands)
	}
	e.SetQEXTEnabled(true)
	for i, got := range e.ensureQEXTOldBandE(2) {
		want := celtGLog(float32(i+1) * 0.125)
		if got != want {
			t.Fatalf("control toggle changed history[%d]: got %v want %v", i, got, want)
		}
	}
	if allocs := testing.AllocsPerRun(1000, func() { e.ensureQEXTOldBandE(1) }); allocs != 0 {
		t.Fatalf("warmed QEXT history view allocated %.1f times", allocs)
	}

	e.Reset()
	if !e.QEXTEnabled() {
		t.Fatal("reset changed the QEXT control")
	}
	for i, got := range e.ensureQEXTOldBandE(2) {
		if got != 0 {
			t.Fatalf("reset retained history[%d]=%v", i, got)
		}
	}
}
