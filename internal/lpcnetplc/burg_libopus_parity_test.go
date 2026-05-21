//go:build gopus_extra_controls

package lpcnetplc

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestBurgCepstralAnalysisMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	var analysis Analysis
	var frame [FrameSize]float32
	var got [2 * NumBands]float32
	for i := range frame {
		frame[i] = float32((i%47)-23) / 21
	}
	want, err := probeLibopusBurgCepstrum(frame[:])
	if err != nil {
		libopustest.HelperUnavailable(t, "burg", err)
	}
	if n := analysis.BurgCepstralAnalysis(got[:], frame[:]); n != 2*NumBands {
		t.Fatalf("BurgCepstralAnalysis()=%d want %d", n, 2*NumBands)
	}
	assertFloat32Close(t, got[:], want, 5e-3, "burg cepstrum")
}
