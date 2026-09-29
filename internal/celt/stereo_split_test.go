package celt

import (
	"math"
	"math/rand"
	"testing"
)

// TestStereoSplitMatchesScalar checks the build-selected stereo_split()
// against the matching scalar contraction order bit for bit.
func TestStereoSplitMatchesScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(0x5917))
	for trial := range 1000 {
		n := rng.Intn(180)
		x := encodeKernelSlice(rng, n)
		y := encodeKernelSlice(rng, n)
		if trial%13 == 0 && n > 0 {
			x[rng.Intn(n)] = float32(math.Inf(1))
		}
		wx := append([]celtNorm(nil), x...)
		wy := append([]celtNorm(nil), y...)
		stereoSplit(x, y)
		stereoSplitScalarTarget(wx, wy)
		for i := range wx {
			if !sameSumBits(x[i], wx[i]) || !sameSumBits(y[i], wy[i]) {
				t.Fatalf("trial %d n=%d: [%d] got (%08x,%08x) want (%08x,%08x)", trial, n, i,
					math.Float32bits(x[i]), math.Float32bits(y[i]), math.Float32bits(wx[i]), math.Float32bits(wy[i]))
			}
		}
	}
}
