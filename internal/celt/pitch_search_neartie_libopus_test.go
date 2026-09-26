package celt

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// The encoder pitch search can choose a different lag when ordered scalar
// correlations are replaced with partial sums. Periodic inputs keep several
// lags close; cancellation exercises the accumulator order directly.
func TestPitchSearchNearTieMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const length = 960
	assertMatch := func(t *testing.T, x, y []float32, maxPitch int) {
		t.Helper()
		want := probeLibopusPLCPitchSearch(t, x, y, length, maxPitch)
		var scratch encoderScratch
		got := pitchSearch(x, y, length, maxPitch, &scratch)
		if got != want {
			t.Fatalf("pitch lag=%d selected libopus=%d", got, want)
		}
		if allocs := testing.AllocsPerRun(100, func() {
			pitchSearch(x, y, length, maxPitch, &scratch)
		}); allocs != 0 {
			t.Fatalf("warm pitch search allocated %.2f times per call", allocs)
		}
	}

	for _, tc := range []struct {
		maxPitch int
		period   float64
	}{
		{975, 50.3},
		{976, 23.7},
		{977, 73.1},
	} {
		t.Run(fmt.Sprintf("max%d_period%.1f", tc.maxPitch, tc.period), func(t *testing.T) {
			y := make([]float32, length+tc.maxPitch)
			phase := 0.0
			for i := range y {
				wobble := 0.000003 * math.Sin(float64(i)*0.011)
				phase += 2 * math.Pi * (1/tc.period + wobble)
				y[i] = float32(0.45*math.Sin(phase) + 0.12*math.Sin(2*phase) +
					0.001*math.Sin(0.37*float64(i)))
			}
			x := make([]float32, length)
			copy(x, y[512:512+length])
			assertMatch(t, x, y, tc.maxPitch)
		})
	}

	t.Run("finite_cancellation_max979", func(t *testing.T) {
		const maxPitch = 979
		y := make([]float32, length+maxPitch)
		x := make([]float32, length)
		for i := range y {
			y[i] = float32(0.9 + 0.001*math.Sin(float64(i)*0.17))
		}
		for i := range x {
			sign := float32(1)
			if i&1 != 0 {
				sign = -1
			}
			x[i] = sign + float32(0.0001*math.Sin(float64(i)*0.037))
		}
		assertMatch(t, x, y, maxPitch)
	})
}
