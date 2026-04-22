//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package lpcnetplc

import "testing"

func TestBurgCepstralAnalysisMatchesLibopus(t *testing.T) {
	var analysis Analysis
	var frame [FrameSize]float32
	var got [2 * NumBands]float32
	for i := range frame {
		frame[i] = float32((i%47)-23) / 21
	}
	want, err := probeLibopusBurgCepstrum(frame[:])
	if err != nil {
		t.Skipf("burg helper unavailable: %v", err)
	}
	if n := analysis.BurgCepstralAnalysis(got[:], frame[:]); n != 2*NumBands {
		t.Fatalf("BurgCepstralAnalysis()=%d want %d", n, 2*NumBands)
	}
	assertFloat32Close(t, got[:], want, 5e-3, "burg cepstrum")
}
