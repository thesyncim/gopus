//go:build !gopus_celt_trace && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package encoder

import "testing"

func TestAnalysisMeanStdTraceDisabledRunAnalysisZeroAlloc(t *testing.T) {
	if analysisMeanStdTraceEnabled {
		t.Fatal("mean/std trace is enabled in the disabled-trace build")
	}

	const (
		fs        = 48000
		frameSize = fs / 50
	)
	pcm := make([]float32, frameSize)
	for i := range pcm {
		pcm[i] = float32(i%257-128) * 0.0001
	}
	state := NewTonalityAnalysisState(fs)
	state.SetLSBDepth(24)
	run := func() { state.RunAnalysis(pcm, frameSize, 1) }
	for range 10 {
		run()
	}
	if allocs := testing.AllocsPerRun(100, run); allocs != 0 {
		t.Fatalf("warmed RunAnalysis allocated %g objects per frame with mean/std trace disabled", allocs)
	}
}
