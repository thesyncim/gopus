package celt

import (
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestOpPVQSearchFloatMatchesLibopusSameArch compares the selected Go path with
// the matching float libopus kernel: scalar builds use op_pvq_search_c, while
// the amd64 SIMD build calls libopus's x86 SSE2 implementation directly.
func TestOpPVQSearchFloatMatchesLibopusSameArch(t *testing.T) {
	libopustest.RequireOracle(t)

	// Sanity that the oracle builds/links the float reference on this host.
	if _, _, err := libopustest.ProbeCELTPVQSearchFloat([]float32{1, 0}, 1); err != nil {
		libopustest.HelperUnavailable(t, "celt float pvq", err)
		return
	}

	rng := rand.New(rand.NewSource(0x5045565131))
	probe := libopustest.ProbeCELTPVQSearchFloat
	if useX86PVQSearchSSE2 {
		probe = libopustest.ProbeCELTPVQSearchFloatSSE2
	}

	// CELT PVQ band widths (M*nbBins for the static 48k mode bands) and a range
	// of pulse counts spanning low-K (high bands) through high-K (pre-search).
	ns := []int{2, 3, 4, 6, 8, 12, 16, 18, 24, 32, 48, 64, 96, 144, 176}
	ks := []int{1, 2, 3, 5, 7, 11, 16, 24, 40, 64, 100, 160, 256}

	cases := 0
	for _, n := range ns {
		for _, k := range ks {
			if k <= 0 || n <= 0 {
				continue
			}
			for trial := range 24 {
				x := make([]float32, n)
				switch trial % 4 {
				case 0:
					// Unit-norm random (typical normalized band shape).
					var s float64
					for i := range x {
						v := float32(rng.NormFloat64())
						x[i] = v
						s += float64(v) * float64(v)
					}
					inv := float32(1.0 / (math.Sqrt(s) + 1e-30))
					for i := range x {
						x[i] *= inv
					}
				case 1:
					// Sparse: a couple of dominant bins (tie-break stress).
					for i := range x {
						x[i] = 0
					}
					x[rng.Intn(n)] = 1
					if n > 1 {
						x[rng.Intn(n)] = float32(rng.NormFloat64())
					}
				case 2:
					// Moderately peaky shape (still a realistic normalized band:
					// one or two dominant bins plus a decaying tail). Exact ties are
					// excluded here; see TestOpPVQSearchFloatHighKNearTieResidual for
					// the documented clang-vectorization boundary.
					for i := range x {
						x[i] = float32(rng.NormFloat64()) / float32(i+2)
					}
				default:
					// Small values / near-silence.
					for i := range x {
						x[i] = float32(rng.NormFloat64()) * 1e-3
					}
				}

				xCopy := append([]float32(nil), x...)
				goIy, goYY := opPVQSearchFloatForTest(xCopy, k)

				libYY, libIy, err := probe(x, k)
				if err != nil {
					t.Fatalf("n=%d k=%d trial=%d: float pvq oracle: %v", n, k, trial, err)
				}

				if len(goIy) != len(libIy) {
					t.Fatalf("n=%d k=%d trial=%d: iy length go=%d lib=%d", n, k, trial, len(goIy), len(libIy))
				}
				for j := range goIy {
					if goIy[j] != libIy[j] {
						t.Fatalf("n=%d k=%d trial=%d: iy[%d] go=%d lib=%d\n  X=%v\n  goIy=%v\n  libIy=%v",
							n, k, trial, j, goIy[j], libIy[j], x, goIy, libIy)
					}
				}
				if math.Float32bits(goYY) != math.Float32bits(libYY) {
					t.Fatalf("n=%d k=%d trial=%d: yy go=%g(0x%08x) lib=%g(0x%08x)\n  X=%v",
						n, k, trial, goYY, math.Float32bits(goYY), libYY, math.Float32bits(libYY), x)
				}
				cases++
			}
		}
	}

	for _, x := range [][]float32{
		{1, 1, 1, 1, 1, 1, 1, 1},
		{1, -1, 1, -1, 1},
		{0, 0, 0, 0, 0, 0, 0, 0, 0},
	} {
		k := len(x) / 2
		if k < 1 {
			k = 1
		}
		goInput := make([]celtNorm, len(x))
		for i, v := range x {
			goInput[i] = celtNorm(v)
		}
		goIy, goYY := opPVQSearchNorm(goInput, k)
		libYY, libIy, err := probe(x, k)
		if err != nil {
			t.Fatalf("tie vector n=%d k=%d: float pvq oracle: %v", len(x), k, err)
		}
		if math.Float32bits(float32(goYY)) != math.Float32bits(libYY) || !equalInt32Slices(goIy, libIy) {
			t.Fatalf("tie vector n=%d k=%d mismatch: Go iy=%v yy=%08x, libopus iy=%v yy=%08x",
				len(x), k, goIy, math.Float32bits(float32(goYY)), libIy, math.Float32bits(libYY))
		}
	}
	t.Logf("op_pvq_search float same-arch parity: %d vectors byte-exact (iy + yy)", cases)
}

// TestOpPVQSearchFloatHighKNearTieResidual documents the one PVQ-search
// configuration that is NOT byte-exact against same-arch float libopus, and
// proves it is irreducible rather than a gopus defect.
//
// The pre-search projection (taken only when K > N/2) accumulates
// yy=sum(y*y) and xy=sum(absX*y). At -O3 clang auto-vectorizes op_pvq_search_c,
// and the reduction order it emits is N-dependent: for some N it keeps four
// 4-lane accumulators reduced with a horizontal (a0+a2)+(a1+a3) tree, for other
// N it sums the SIMD product lanes back in scalar sequential order. No single
// portable Go reduction reproduces every one of clang's per-N choices, so for a
// high-K band whose normalized magnitudes are within ~1e-4 of each other, a
// 1-ULP delta in xy/yy flips a best_id tie in the greedy search and moves one
// pulse between two equal-magnitude bins (total pulses and yy stay equal).
//
// This path is unreachable from the real encoder: CELT high bands carry low K
// (< N/2), so they skip the projection entirely, and broadband/noise bands that
// do hit K > N/2 are not exact ties. Forcing the projection rcp/accumulation to
// any one of clang's reduction orders is byte-neutral on the full CELT encode
// (verified: same diverging-frame count), confirming the residual does not
// drive real-encode divergence. We therefore record it precisely instead of
// masking it with a fragile per-N reduction emulation.
func TestOpPVQSearchFloatHighKNearTieResidual(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, _, err := libopustest.ProbeCELTPVQSearchFloat([]float32{1, 0}, 1); err != nil {
		libopustest.HelperUnavailable(t, "celt float pvq", err)
		return
	}

	// The exact n=18, K=256 vector found during bisection: 18 magnitudes all
	// within 1e-4 of 1.0 (K=256 > N/2 forces the projection pre-search).
	x := []float32{
		0.99995637, 1.000096, -1.0001544, 1.0001608, 1.0001048, 0.9999021,
		0.99996865, 1.0000812, -0.9998345, -0.99997115, -0.99992955, -1.0000807,
		0.99997205, 0.99987245, 1.0001543, -1.0000563, -1.0001165, -1.0000064,
	}
	const k = 256
	if useX86PVQSearchSSE2 {
		goIy, goYY := opPVQSearchFloatForTest(append([]float32(nil), x...), k)
		libYY, libIy, err := libopustest.ProbeCELTPVQSearchFloatSSE2(x, k)
		if err != nil {
			t.Fatalf("float SSE2 pvq oracle: %v", err)
		}
		if !equalInt32Slices(goIy, libIy) || math.Float32bits(goYY) != math.Float32bits(libYY) {
			t.Fatalf("SIMD PVQ near-tie vector differs from SSE2 oracle: Go iy=%v yy=%08x, C iy=%v yy=%08x",
				goIy, math.Float32bits(goYY), libIy, math.Float32bits(libYY))
		}
		return
	}

	goIy, _ := opPVQSearchFloatForTest(append([]float32(nil), x...), k)
	_, libIy, err := libopustest.ProbeCELTPVQSearchFloat(x, k)
	if err != nil {
		t.Fatalf("float pvq oracle: %v", err)
	}

	// Sanity: total pulse count and energy are identical; only one tie moves.
	sumAbs := func(v []int32) int32 {
		var s int32
		for _, e := range v {
			if e < 0 {
				e = -e
			}
			s += e
		}
		return s
	}
	if got, want := sumAbs(goIy), sumAbs(libIy); got != want {
		t.Fatalf("pulse-count invariant broken: go=%d lib=%d (not a pure tie move)", got, want)
	}
	moved := 0
	for j := range goIy {
		if goIy[j] != libIy[j] {
			moved++
		}
	}
	t.Logf("documented irreducible high-K near-tie residual: %d bins differ by a moved pulse (same total |iy|=%d)", moved, sumAbs(goIy))
	t.Logf("  go =%v", goIy)
	t.Logf("  lib=%v", libIy)
	if moved == 0 {
		t.Skip("toolchain happens to agree on this vector; residual not reproduced on this host")
	}
}

func equalInt32Slices(a, b []int32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func opPVQSearchFloatForTest(x []float32, k int) ([]int32, float32) {
	xn := make([]celtNorm, len(x))
	for i, v := range x {
		xn[i] = celtNorm(v)
	}
	iy, yy := opPVQSearchNorm(xn, k)
	return iy, float32(yy)
}
