//go:build amd64.v3 && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package encoder

import "testing"

var analysisSlopeZeroAllocSink float32

func TestRunAnalysisSlopeV3ZeroAllocs(t *testing.T) {
	const (
		fs        = 48000
		frameSize = fs / 50
	)
	pcm := make([]float32, frameSize)
	for i := range pcm {
		pcm[i] = float32((i*37)%101-50) / 64
	}

	state := NewTonalityAnalysisState(fs)
	state.SetLSBDepth(24)
	for range 4 {
		state.RunAnalysis(pcm, frameSize, 1)
	}
	run := func() {
		analysisSlopeZeroAllocSink = state.RunAnalysis(pcm, frameSize, 1).TonalitySlope
	}
	run()
	if got := testing.AllocsPerRun(100, run); got != 0 {
		t.Fatalf("warmed RunAnalysis allocated %g objects per frame", got)
	}
}
