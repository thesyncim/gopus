//go:build darwin && arm64 && goexperiment.simd && !nosimd && !purego && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package celt

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestPitchAutocorrFourTermTailMatchesLibopus(t *testing.T) {
	requirePairedCELTOracleMode(t)
	libopustest.RequireOracle(t)
	const length = 8
	pcm := makeCELTPLCTestSignal(length*2, 0x5f150000+length*4+1, 2600)
	x := make([]float32, length)
	x[0] = float32(0.25)*pcm[1] + float32(0.5)*pcm[0]
	pitchDownsample2(x, pcm, nil)
	want := probeLibopusRawAutocorr(t, x, 4, nil)
	var got [5]float32
	run := func() { pitchAutocorr5F32(x, length, &got) }
	run()
	assertFloat32Bits(t, "raw autocorrelation", got[:], want)
	run()
	if allocs := testing.AllocsPerRun(100, run); allocs != 0 {
		t.Fatalf("raw autocorrelation allocated %g objects per call", allocs)
	}
}
