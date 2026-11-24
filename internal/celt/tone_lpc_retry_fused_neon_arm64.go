//go:build arm64 && (!goexperiment.simd || nosimd || purego)

package celt

// toneLPCRetry48kMonoFast computes all five 48 kHz mono retry-delay
// correlations (delays 2, 4, 8, 16, 32) in a single prefix-shared scalar FMADD
// pass over 7 shared accumulators, matching separate toneLPC calls bit-for-bit.
func toneLPCRetry48kMonoFast(x []float32, maxDelay int) (float32, float32, bool, int) {
	n := len(x)
	if n <= 64 {
		return toneLPCRetry48kMonoSequential(x, maxDelay)
	}
	cnt2 := n - 4
	cnt4 := n - 8
	cnt8 := n - 16
	cnt16 := n - 32
	cnt32 := n - 64

	x0 := x[:cnt2:cnt2]
	x2 := x[2 : 2+cnt2 : 2+cnt2]
	x4 := x[4 : 4+cnt2 : 4+cnt2]
	x8 := x[8 : 8+cnt4 : 8+cnt4]
	x16 := x[16 : 16+cnt8 : 16+cnt8]
	x32 := x[32 : 32+cnt16 : 32+cnt16]
	x64 := x[64 : 64+cnt32 : 64+cnt32]

	var s0, s2, s4, s8, s16, s32, s64 float32
	i := 0
	for ; i < cnt32; i++ {
		xi := x0[i]
		s0 = fma32(xi, xi, s0)
		s2 = fma32(xi, x2[i], s2)
		s4 = fma32(xi, x4[i], s4)
		s8 = fma32(xi, x8[i], s8)
		s16 = fma32(xi, x16[i], s16)
		s32 = fma32(xi, x32[i], s32)
		s64 = fma32(xi, x64[i], s64)
	}
	r0032, r0132, r0232 := s0, s32, s64

	for ; i < cnt16; i++ {
		xi := x0[i]
		s0 = fma32(xi, xi, s0)
		s2 = fma32(xi, x2[i], s2)
		s4 = fma32(xi, x4[i], s4)
		s8 = fma32(xi, x8[i], s8)
		s16 = fma32(xi, x16[i], s16)
		s32 = fma32(xi, x32[i], s32)
	}
	r0016, r0116, r0216 := s0, s16, s32

	for ; i < cnt8; i++ {
		xi := x0[i]
		s0 = fma32(xi, xi, s0)
		s2 = fma32(xi, x2[i], s2)
		s4 = fma32(xi, x4[i], s4)
		s8 = fma32(xi, x8[i], s8)
		s16 = fma32(xi, x16[i], s16)
	}
	r008, r018, r028 := s0, s8, s16

	for ; i < cnt4; i++ {
		xi := x0[i]
		s0 = fma32(xi, xi, s0)
		s2 = fma32(xi, x2[i], s2)
		s4 = fma32(xi, x4[i], s4)
		s8 = fma32(xi, x8[i], s8)
	}
	r004, r014, r024 := s0, s4, s8

	for ; i < cnt2; i++ {
		xi := x0[i]
		s0 = fma32(xi, xi, s0)
		s2 = fma32(xi, x2[i], s2)
		s4 = fma32(xi, x4[i], s4)
	}
	r002, r012, r022 := s0, s2, s4

	delay := 2
	lpc0, lpc1, success := toneLPCSolveFromCorr(x, delay, r002, r012, r022)
	if delay > maxDelay || !toneLPCRetryNeeded(lpc0, lpc1, success) {
		return lpc0, lpc1, success, delay
	}
	delay = 4
	lpc0, lpc1, success = toneLPCSolveFromCorr(x, delay, r004, r014, r024)
	if delay > maxDelay || !toneLPCRetryNeeded(lpc0, lpc1, success) {
		return lpc0, lpc1, success, delay
	}
	delay = 8
	lpc0, lpc1, success = toneLPCSolveFromCorr(x, delay, r008, r018, r028)
	if delay > maxDelay || !toneLPCRetryNeeded(lpc0, lpc1, success) {
		return lpc0, lpc1, success, delay
	}
	delay = 16
	lpc0, lpc1, success = toneLPCSolveFromCorr(x, delay, r0016, r0116, r0216)
	if delay > maxDelay || !toneLPCRetryNeeded(lpc0, lpc1, success) {
		return lpc0, lpc1, success, delay
	}
	delay = 32
	lpc0, lpc1, success = toneLPCSolveFromCorr(x, delay, r0032, r0132, r0232)
	return lpc0, lpc1, success, delay
}
