//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import (
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/opusmath"
)

// TestKfBfly2M4SIMDMatchesScalar checks the vector m == 4 radix-2 stage
// against the scalar loop, including signed zeros and infinities.
func TestKfBfly2M4SIMDMatchesScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(0x2b4))
	pick := func() float32 {
		switch rng.Intn(12) {
		case 0:
			return float32(math.Copysign(0, float64(rng.Float32()-0.5)))
		case 1:
			return float32(math.Inf(1 - 2*rng.Intn(2)))
		default:
			return float32(rng.NormFloat64() * math.Pow(10, float64(rng.Intn(12)-4)))
		}
	}
	for _, n := range []int{1, 2, 3, 15, 30, 60} {
		for iter := 0; iter < 50; iter++ {
			got := make([]float32, 16*n)
			for i := range got {
				got[i] = pick()
			}
			want := append([]float32(nil), got...)
			kfBfly2M4SIMD(got, n)
			kfBfly2M4SIMDReference(want, n)
			for k := range want {
				if math.Float32bits(got[k]) != math.Float32bits(want[k]) {
					t.Fatalf("n=%d iter %d: fout[%d] = %08x, want %08x", n, iter, k,
						math.Float32bits(got[k]), math.Float32bits(want[k]))
				}
			}
		}
	}
	fout := make([]float32, 16*60)
	if allocs := testing.AllocsPerRun(100, func() { kfBfly2M4SIMD(fout, 60) }); allocs != 0 {
		t.Fatalf("steady-state allocations = %g, want 0", allocs)
	}
}

// kfBfly2M4SIMDReference follows the GCC target-v3 vector blocks in
// celt/kiss_fft.c::kf_bfly2: lanes 1 and 3 fuse twiddle products into outputs
// for each four-group block, while the final one to three groups round first.
func kfBfly2M4SIMDReference(fout []float32, n int) {
	tw := kfBfly2M4Twiddle
	fusedGroups := n &^ 3
	for group := 0; group < n; group++ {
		base := group * 16
		for lane := 0; lane < 4; lane++ {
			lo := base + 2*lane
			hi := base + 8 + 2*lane
			ar, ai := fout[lo], fout[lo+1]
			br, bi := fout[hi], fout[hi+1]
			var tr, ti float32
			switch lane {
			case 0:
				tr, ti = br, bi
			case 1:
				tr, ti = round32(br+bi), round32(bi-br)
			case 2:
				tr, ti = bi, -br
			case 3:
				tr, ti = round32(bi-br), -round32(bi+br)
			}
			if kissFFTTargetV3FMA && group < fusedGroups && (lane == 1 || lane == 3) {
				fout[hi] = opusmath.FMA32(-tw, tr, ar)
				fout[lo] = opusmath.FMA32(tw, tr, ar)
				fout[hi+1] = opusmath.FMA32(-tw, ti, ai)
				fout[lo+1] = opusmath.FMA32(tw, ti, ai)
				continue
			}
			tr, ti = round32(tr*tw), round32(ti*tw)
			if lane == 0 || lane == 2 {
				tr, ti = br, bi
				if lane == 2 {
					tr, ti = bi, -br
				}
			}
			fout[hi], fout[hi+1] = ar-tr, ai-ti
			fout[lo], fout[lo+1] = ar+tr, ai+ti
		}
	}
}
