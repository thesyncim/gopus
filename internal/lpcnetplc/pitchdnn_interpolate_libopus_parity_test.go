//go:build gopus_osce

package lpcnetplc

import (
	"math"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

var libopusPitchDNNInterpolateHelper libopustest.HelperCache
var pitchDNNInterpolateSink float32

// The helper compiles the pitchdnn.c return expression with the selected C
// flags. TestLPCNetPitchDNNNetIsolation checks the complete linked C function.
func TestPitchDNNInterpolateMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	path, err := cachedLibopusPLCHelperPath(&libopusPitchDNNInterpolateHelper, "libopus_pitchdnn_interpolate.c", "gopus_libopus_pitchdnn_interpolate")
	if err != nil {
		libopustest.HelperUnavailable(t, "pitchdnn interpolation", err)
	}
	cases := []struct{ sum, count float32 }{
		{math.Float32frombits(0x429fd8f0), 1},
		{math.Float32frombits(0x429fd8f1), 1},
		{math.Float32frombits(0x429fd8f4), 1},
		{math.Float32frombits(0x42b40001), 1},
		{math.Float32frombits(0x429fd8f0) * 2, 2},
		{0, 1},   // one-hot class at the lower end of the pitch window
		{179, 1}, // one-hot class at the upper end of the pitch window
		{0, 0},   // the C expression produces its selected NaN for zero weight
		{90.125, 1.25},
		{4.5, 0.05},
		{179.125, 2.125},
	}
	payload := libopustest.NewOraclePayload("GPWI", uint32(len(cases)))
	for _, tc := range cases {
		payload.Float32(tc.sum)
		payload.Float32(tc.count)
	}
	reader, err := libopustest.RunOracle(path, payload.Bytes(), "pitchdnn interpolation", "GPWO")
	if err != nil {
		t.Fatalf("selected C interpolation: %v", err)
	}
	if n := reader.Count(len(cases)); n != len(cases) {
		t.Fatalf("selected C record count=%d want %d", n, len(cases))
	}
	for i, tc := range cases {
		want := reader.Float32()
		got := pitchDNNInterpolate(tc.sum, tc.count)
		if math.Float32bits(got) != math.Float32bits(want) {
			t.Errorf("case %d sum=%08x count=%08x: Go=%08x C=%08x", i,
				math.Float32bits(tc.sum), math.Float32bits(tc.count),
				math.Float32bits(got), math.Float32bits(want))
		}
		if i == 0 && runtime.GOARCH == "arm64" && math.Float32bits(want) != 0xbe2bf7f8 {
			t.Errorf("selected ARM C rounding witness=%08x want be2bf7f8", math.Float32bits(want))
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
}

func TestPitchDNNInterpolateWarmZeroAlloc(t *testing.T) {
	sum := math.Float32frombits(0x429fd8f0)
	call := func() { pitchDNNInterpolateSink = pitchDNNInterpolate(sum, 1) }
	call()
	if allocs := testing.AllocsPerRun(1000, call); allocs != 0 {
		t.Fatalf("warm interpolation allocations=%g want 0", allocs)
	}
}
