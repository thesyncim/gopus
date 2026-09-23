//go:build !arm64 || nosimd || !goexperiment.simd

package celt

// celtAbsSumUsesNeon is false off the fused arm64 build, so the float abs-sum
// callers keep the scalar left-to-right reduction and the amd64/nosimd
// byte-exact gate holds.
const celtAbsSumUsesNeon = false

// l1AbsSumNeon preserves the four-lane reduction order used by the arm64
// vector path while keeping the default and nosimd builds assembly-free.
func l1AbsSumNeon(tmp []float32, n int) float32 {
	n = min(n, len(tmp))
	var acc [4]float32
	i := 0
	for ; i+4 <= n; i += 4 {
		for lane := range 4 {
			v := tmp[i+lane]
			if v < 0 {
				v = -v
			}
			acc[lane] += v
		}
	}
	var tail float32
	for ; i < n; i++ {
		v := tmp[i]
		if v < 0 {
			v = -v
		}
		tail += v
	}
	return ((acc[0] + acc[1]) + (acc[2] + acc[3])) + tail
}
