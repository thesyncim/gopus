//go:build amd64

package silk

import (
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/opusmath"
)

// TestFloatToInt16ScaledMatchesRaw checks floatToInt16Scaled against the
// element-wise opusmath.Float32ToInt16Raw conversion on random products and
// on the rounding, saturation, overflow and NaN edges.
func TestFloatToInt16ScaledMatchesRaw(t *testing.T) {
	rng := rand.New(rand.NewSource(0xf16))
	specials := []float32{
		0, 0.5, -0.5, 1.5, -1.5, 2.5, -2.5, 32766.5, 32767, 32767.5, 32768, -32767.5,
		-32768, -32768.5, -32769, 4194303.5, 1 << 30, 2147483520, 1 << 31, -(1 << 31),
		-2147483904, 3e38, -3e38, float32(math.Inf(1)), float32(math.Inf(-1)), float32(math.NaN()),
	}
	scales := []float32{1, 0.5, 2, 32768, 7.3, 1e-4}
	for iter := range 2000 {
		n := rng.Intn(70)
		in := make([]float32, n)
		for i := range in {
			switch rng.Intn(5) {
			case 0:
				in[i] = specials[rng.Intn(len(specials))]
			case 1:
				in[i] = float32(rng.NormFloat64() * 60000)
			default:
				in[i] = float32(rng.NormFloat64() * 8000)
			}
		}
		scale := scales[iter%len(scales)]
		got := make([]int16, n)
		floatToInt16Scaled(got, in, scale, n)
		for i, v := range in {
			if want := opusmath.Float32ToInt16Raw(v * scale); got[i] != want {
				t.Fatalf("iter %d: in[%d]=%v scale %v: got %d want %d", iter, i, v, scale, got[i], want)
			}
		}
	}
}
