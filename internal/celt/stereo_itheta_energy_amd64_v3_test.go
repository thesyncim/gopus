//go:build amd64.v3 && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes && (!goexperiment.simd || nosimd || purego)

package celt

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

var stereoIthetaEnergyAllocationSink float32

func TestStereoIthetaNonStereoEnergyMatchesLibopusCapturedBand18(t *testing.T) {
	requireCELTV3OracleTarget(t)
	requirePairedCELTOracleMode(t)
	libopustest.RequireOracle(t)

	// These are the exact XBefore/YBefore vectors from frame 1, band 18,
	// non-stereo theta event 1 in the strict scalar CVBR trace.
	xBits := [...]uint32{0x3e17aeff, 0x3e191154, 0x3db7aaed, 0xbb678cad, 0xbdb490cf, 0xbe05c12b,
		0x38fd6a47, 0xb8cdc0cd, 0x38a72f8d, 0xb8909084, 0x386db2c7, 0xb84dda2e}
	yBits := [...]uint32{0x3ee2db69, 0xbe2d988c, 0xbf29212a, 0xbf560ced, 0xbf2550e6, 0xbe47252b,
		0xb77cba68, 0x378aad79, 0xb78bb339, 0x3781701a, 0xb75ac0aa, 0x376a12cf}
	x := make([]celtNorm, len(xBits))
	y := make([]celtNorm, len(yBits))
	xOracle := make([]float32, len(xBits))
	yOracle := make([]float32, len(yBits))
	for i, bits := range xBits {
		xOracle[i] = math.Float32frombits(bits)
		x[i] = celtNorm(xOracle[i])
	}
	for i, bits := range yBits {
		yOracle[i] = math.Float32frombits(bits)
		y[i] = celtNorm(yOracle[i])
	}
	oracle, err := libopustest.ProbeCELTStereoIthetaQ30([]libopustest.CELTStereoIthetaCase{{Stereo: false, X: xOracle, Y: yOracle}})
	if err != nil {
		t.Fatalf("probe captured CELT non-stereo theta: %v", err)
	}
	if oracle.SelectedArch != 0 || oracle.RTCDEnabled || oracle.PresumeNEON {
		t.Fatalf("captured theta oracle selected non-scalar C path: arch=%d rtcd=%t presumeNEON=%t",
			oracle.SelectedArch, oracle.RTCDEnabled, oracle.PresumeNEON)
	}
	got := stereoIthetaQ30Norm(x, y, false)
	if int32(got) != int32(oracle.Values[0]) {
		t.Fatalf("captured band-18 non-stereo theta=%d (%08x), want linked C %d (%08x)",
			int32(got), uint32(got), int32(oracle.Values[0]), oracle.Values[0])
	}
}

func TestStereoIthetaNonStereoEnergyZeroAlloc(t *testing.T) {
	x := make([]celtNorm, 12)
	y := make([]celtNorm, 12)
	for i := range x {
		x[i] = celtNorm(float32(i+1) / 13)
		y[i] = celtNorm(float32(i+3) / 17)
	}
	run := func() {
		mid, side := stereoIthetaNonStereoEnergy(x, y)
		stereoIthetaEnergyAllocationSink = mid + side
	}
	run()
	if got := testing.AllocsPerRun(100, run); got != 0 {
		t.Fatalf("stereoIthetaNonStereoEnergy allocates %v per run", got)
	}
}
