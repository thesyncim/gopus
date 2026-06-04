//go:build arm64 && !purego

package silk

import (
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/opusmath"
)

func scalarFloatToInt16Scaled(out []int16, in []float32, scale float32, n int) {
	for i := 0; i < n; i++ {
		out[i] = opusmath.Float32ToInt16Raw(in[i] * scale)
	}
}

// TestFloatToInt16ScaledBitExact asserts the NEON FCVTNS+SQXTN conversion is
// byte-identical to the scalar saturate-then-round-even path across random data,
// every sub-block length, and the saturation / ties-to-even boundaries.
func TestFloatToInt16ScaledBitExact(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	scales := []float32{1.0, 0.5, 2.0, 32768.0, 0.0001, 7.3}
	for _, scale := range scales {
		for _, n := range []int{0, 1, 7, 8, 9, 15, 16, 17, 31, 64, 240, 481} {
			in := make([]float32, n)
			for i := range in {
				switch rng.Intn(4) {
				case 0:
					in[i] = float32(rng.NormFloat64()) * 20000
				case 1:
					in[i] = float32(rng.NormFloat64()) * 60000 // exercise saturation
				default:
					in[i] = float32(rng.NormFloat64())
				}
			}
			got := make([]int16, n)
			want := make([]int16, n)
			floatToInt16Scaled(got, in, scale, n)
			scalarFloatToInt16Scaled(want, in, scale, n)
			for i := 0; i < n; i++ {
				if got[i] != want[i] {
					t.Fatalf("scale=%v n=%d i=%d in=%v: neon=%d scalar=%d",
						scale, n, i, in[i], got[i], want[i])
				}
			}
		}
	}
}

// TestFloatToInt16ScaledBoundaries hits exact half-way and saturation values.
func TestFloatToInt16ScaledBoundaries(t *testing.T) {
	vals := []float32{
		0, 0.5, -0.5, 1.5, 2.5, -1.5, -2.5,
		32766.5, 32767.0, 32767.4, 32767.5, 32767.6, 32768.0, 40000,
		-32767.5, -32768.0, -32768.4, -32768.5, -32768.6, -40000,
	}
	// Pad to a multiple of 8 plus a remainder so both paths run.
	in := append([]float32(nil), vals...)
	for len(in)%8 != 3 {
		in = append(in, float32(0))
	}
	n := len(in)
	got := make([]int16, n)
	want := make([]int16, n)
	floatToInt16Scaled(got, in, 1.0, n)
	scalarFloatToInt16Scaled(want, in, 1.0, n)
	for i := 0; i < n; i++ {
		if got[i] != want[i] {
			t.Fatalf("i=%d in=%v: neon=%d scalar=%d", i, in[i], got[i], want[i])
		}
	}
}

func BenchmarkFloatToInt16ScaledNeon(b *testing.B) {
	const n = 320
	rng := rand.New(rand.NewSource(7))
	in := make([]float32, n)
	out := make([]int16, n)
	for i := range in {
		in[i] = float32(rng.NormFloat64()) * 10000
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		floatToInt16Scaled(out, in, 1.0, n)
	}
	_ = math.Float32bits(in[0])
}

func BenchmarkFloatToInt16ScaledScalar(b *testing.B) {
	const n = 320
	rng := rand.New(rand.NewSource(7))
	in := make([]float32, n)
	out := make([]int16, n)
	for i := range in {
		in[i] = float32(rng.NormFloat64()) * 10000
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scalarFloatToInt16Scaled(out, in, 1.0, n)
	}
}
