package celt

import (
	"math"
	"testing"
)

// scaleFloat32IntoRef is the scalar reference: dst[i] = src[i]*gain as a single
// per-lane float32 product (mul32 == round32(a*b), a no-op rounding on a value
// that is already float32). Whatever scaleFloat32IntoNEON the build selects —
// arm64 asm, amd64/arm64 archsimd, or the portable fallback — must reproduce
// this bit-for-bit, since every path is a bare multiply with no FMA contraction.
func scaleFloat32IntoRef(dst, src []float32, gain float32) {
	n := min(len(dst), len(src))
	for i := range n {
		dst[i] = mul32(src[i], gain)
	}
}

func scaleParityF32(seed uint64, i int) float32 {
	x := seed + uint64(i)*0x9e3779b97f4a7c15
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return float32(int64(x%4000001)-2000000) / 7000.0
}

// benchmarkScaleInto times the build-selected scaleFloat32IntoNEON (kernel=true:
// arm64 asm, archsimd under goexperiment.simd, or the portable fallback) against
// the always-compiled scalar reference (kernel=false), so a single binary anchors
// the kernel to scalar and an A/B across build tags compares asm vs archsimd.
func benchmarkScaleInto(b *testing.B, n int, kernel bool) {
	src := make([]float32, n)
	dst := make([]float32, n)
	for i := range src {
		src[i] = scaleParityF32(uint64(n)+1, i)
	}
	const gain = 0.9375
	b.SetBytes(int64(n * 4))
	b.ResetTimer()
	if kernel {
		for range b.N {
			scaleFloat32IntoNEON(dst, src, gain)
		}
		return
	}
	for range b.N {
		scaleFloat32IntoRef(dst, src, gain)
	}
}

func BenchmarkScaleIntoKernelN8(b *testing.B)   { benchmarkScaleInto(b, 8, true) }
func BenchmarkScaleIntoRefN8(b *testing.B)      { benchmarkScaleInto(b, 8, false) }
func BenchmarkScaleIntoKernelN16(b *testing.B)  { benchmarkScaleInto(b, 16, true) }
func BenchmarkScaleIntoRefN16(b *testing.B)     { benchmarkScaleInto(b, 16, false) }
func BenchmarkScaleIntoKernelN64(b *testing.B)  { benchmarkScaleInto(b, 64, true) }
func BenchmarkScaleIntoRefN64(b *testing.B)     { benchmarkScaleInto(b, 64, false) }
func BenchmarkScaleIntoKernelN176(b *testing.B) { benchmarkScaleInto(b, 176, true) }
func BenchmarkScaleIntoRefN176(b *testing.B)    { benchmarkScaleInto(b, 176, false) }
func BenchmarkScaleIntoKernelN480(b *testing.B) { benchmarkScaleInto(b, 480, true) }
func BenchmarkScaleIntoRefN480(b *testing.B)    { benchmarkScaleInto(b, 480, false) }

func TestScaleFloat32IntoBitExact(t *testing.T) {
	lengths := []int{0, 1, 2, 3, 4, 5, 7, 8, 9, 15, 16, 17, 31, 32, 33, 63, 64, 120, 480}
	gains := []float32{0, 1, -1, 0.5, 2, 0.123456, -7.5, 1e-3, 1e3, float32(math.Pi)}
	for _, n := range lengths {
		src := make([]float32, n)
		want := make([]float32, n)
		got := make([]float32, n)
		for gi, gain := range gains {
			seed := uint64(n)*1000003 + uint64(gi)*97 + 1
			for i := range src {
				src[i] = scaleParityF32(seed, i)
			}
			scaleFloat32IntoRef(want, src, gain)
			for i := range got {
				got[i] = math.Float32frombits(0x7fc00000) // poison to catch missed stores
			}
			scaleFloat32IntoNEON(got, src, gain)
			for i := range n {
				if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
					t.Fatalf("n=%d gain=%g i=%d: got %#08x want %#08x",
						n, gain, i, math.Float32bits(got[i]), math.Float32bits(want[i]))
				}
			}
		}
	}
}
