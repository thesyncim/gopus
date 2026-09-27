package encoder

import (
	"math"
	"math/rand"
	"testing"
)

// analysisBinsEdgeValue returns FFT bin values that stress fast_atan2f's
// branches: exact zeros of both signs, quadrant boundaries, equal magnitudes,
// values below the 1e-18 cutoff, and large magnitudes.
func analysisBinsEdgeValue(rng *rand.Rand) float32 {
	switch rng.Intn(10) {
	case 0:
		return 0
	case 1:
		return float32(math.Copysign(0, -1))
	case 2:
		return float32(rng.NormFloat64()) * 1e-10
	case 3:
		return float32(rng.Intn(5) - 2)
	case 4:
		return float32(rng.NormFloat64()) * 3e4
	default:
		return float32(rng.NormFloat64()) * 100
	}
}

// TestAnalysisBinsMatchesScalar requires the per-bin loop the build selects
// (the amd64 SIMD kernel for most bins) to reproduce the scalar loop bit for
// bit in every output and in the phase history.
func TestAnalysisBinsMatchesScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(0x5eed7a5))
	for trial := range 400 {
		var out [480]complex64
		for i := range out {
			if trial%3 == 0 {
				out[i] = complex(analysisBinsEdgeValue(rng), analysisBinsEdgeValue(rng))
			} else {
				out[i] = complex(float32(rng.NormFloat64())*1000, float32(rng.NormFloat64())*1000)
			}
		}
		if trial%7 == 0 {
			// Mirror pairs with equal and opposite magnitudes.
			for i := 1; i < 240; i += 5 {
				out[480-i] = complex(real(out[i]), -imag(out[i]))
			}
		}
		var want, got TonalityAnalysisState
		for i := range want.Angle {
			want.Angle[i] = float32(rng.Float64() - 0.5)
			want.DAngle[i] = float32(rng.Float64() - 0.5)
			want.D2Angle[i] = float32(rng.Float64())
		}
		got.Angle, got.DAngle, got.D2Angle = want.Angle, want.DAngle, want.D2Angle
		var wantT, wantT2, wantN, gotT, gotT2, gotN [240]float32
		want.analysisBinsScalar(&out, 1, wantT[:], wantT2[:], wantN[:])
		got.analysisBins(&out, gotT[:], gotT2[:], gotN[:])
		for i := 1; i < 240; i++ {
			for _, c := range []struct {
				name string
				g, w float32
			}{
				{"tonality", gotT[i], wantT[i]},
				{"tonality2", gotT2[i], wantT2[i]},
				{"noisiness", gotN[i], wantN[i]},
				{"angle", got.Angle[i], want.Angle[i]},
				{"dAngle", got.DAngle[i], want.DAngle[i]},
				{"d2Angle", got.D2Angle[i], want.D2Angle[i]},
			} {
				if math.Float32bits(c.g) != math.Float32bits(c.w) {
					t.Fatalf("trial %d bin %d %s: got %v (%08x), want %v (%08x)", trial, i, c.name,
						c.g, math.Float32bits(c.g), c.w, math.Float32bits(c.w))
				}
			}
		}
	}
}

func TestAnalysisBinsZeroAllocs(t *testing.T) {
	var s TonalityAnalysisState
	var out [480]complex64
	for i := range out {
		out[i] = complex(float32(i%17)-8, float32(i%11)-5)
	}
	var tn, tn2, noise [240]float32
	run := func() { s.analysisBins(&out, tn[:], tn2[:], noise[:]) }
	run()
	if got := testing.AllocsPerRun(100, run); got != 0 {
		t.Fatalf("analysisBins allocated: %g allocs/run", got)
	}
}

// fastAtan2fRef is analysis.c fast_atan2f written with its branches.
func fastAtan2fRef(y, x float32) float32 {
	x2 := round32(x * x)
	y2 := round32(y * y)
	if x2+y2 < 1e-18 {
		return 0
	}
	cE := analysisAtanCE
	s1 := cE
	if y < 0 {
		s1 = -cE
	}
	if x2 < y2 {
		den := (y2 + analysisAtanCB*x2) * (y2 + analysisAtanCC*x2)
		return -x*y*(y2+analysisAtanCA*x2)/den + s1
	}
	den := (x2 + analysisAtanCB*y2) * (x2 + analysisAtanCC*y2)
	s2 := cE
	if x*y < 0 {
		s2 = -cE
	}
	return x*y*(x2+analysisAtanCA*y2)/den + s1 - s2
}

func TestAnalysisAtan2MatchesBranchyForm(t *testing.T) {
	rng := rand.New(rand.NewSource(0xa7a2))
	for i := range 200000 {
		var y, x float32
		if i%2 == 0 {
			y, x = analysisBinsEdgeValue(rng), analysisBinsEdgeValue(rng)
		} else {
			y, x = float32(rng.NormFloat64()), float32(rng.NormFloat64())
		}
		got, want := analysisAtan2(y, x), fastAtan2fRef(y, x)
		if math.Float32bits(got) != math.Float32bits(want) {
			t.Fatalf("analysisAtan2(%v, %v) = %v (%08x), want %v (%08x)", y, x, got, math.Float32bits(got), want, math.Float32bits(want))
		}
	}
}
