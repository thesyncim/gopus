//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

var toneLPCCorrUseAVX = archsimd.X86.AVX() && (!celtAMD64SIMDUseFMA || archsimd.X86.FMA())

// add4x3OrderedAMD64 accumulates 4 consecutive samples of 3 parallel
// correlation products into acc = [r00, r01, r02, 0] in exact ascending
// sample order.
func add4x3OrderedAMD64(acc, p00, p01, p02 archsimd.Float32x4) archsimd.Float32x4 {
	var zero archsimd.Uint32x4
	b0, b1, b2 := p00.ToBits(), p01.ToBits(), p02.ToBits()
	t0 := b0.InterleaveLo(b1).ReshapeToUint64s()
	t1 := b0.InterleaveHi(b1).ReshapeToUint64s()
	t2 := b2.InterleaveLo(zero).ReshapeToUint64s()
	t3 := b2.InterleaveHi(zero).ReshapeToUint64s()
	s0 := t0.InterleaveLo(t2).ReshapeToUint32s().BitsToFloat32()
	s1 := t0.InterleaveHi(t2).ReshapeToUint32s().BitsToFloat32()
	s2 := t1.InterleaveLo(t3).ReshapeToUint32s().BitsToFloat32()
	s3 := t1.InterleaveHi(t3).ReshapeToUint32s().BitsToFloat32()
	return acc.Add(s0).Add(s1).Add(s2).Add(s3)
}

// fma4x3OrderedAMD64 accumulates 4 consecutive samples of 3 parallel
// correlations into acc = [r00, r01, r02, 0] using fused multiply-add in exact
// ascending sample order.
func fma4x3OrderedAMD64(acc, va, v0, v1, v2 archsimd.Float32x4) archsimd.Float32x4 {
	var zero archsimd.Uint32x4
	b0, b1, b2 := v0.ToBits(), v1.ToBits(), v2.ToBits()
	t0 := b0.InterleaveLo(b1).ReshapeToUint64s()
	t1 := b0.InterleaveHi(b1).ReshapeToUint64s()
	t2 := b2.InterleaveLo(zero).ReshapeToUint64s()
	t3 := b2.InterleaveHi(zero).ReshapeToUint64s()
	s0 := t0.InterleaveLo(t2).ReshapeToUint32s().BitsToFloat32()
	s1 := t0.InterleaveHi(t2).ReshapeToUint32s().BitsToFloat32()
	s2 := t1.InterleaveLo(t3).ReshapeToUint32s().BitsToFloat32()
	s3 := t1.InterleaveHi(t3).ReshapeToUint32s().BitsToFloat32()
	acc = va.ConcatPermuteScalars(0, 0, 0, 0, va).MulAdd(s0, acc)
	acc = va.ConcatPermuteScalars(1, 1, 1, 1, va).MulAdd(s1, acc)
	acc = va.ConcatPermuteScalars(2, 2, 2, 2, va).MulAdd(s2, acc)
	acc = va.ConcatPermuteScalars(3, 3, 3, 3, va).MulAdd(s3, acc)
	return acc
}

// add4x4OrderedAMD64 accumulates 4 consecutive samples of 4 parallel
// correlation products into acc = [c0, c1, c2, c3] in exact ascending sample
// order.
func add4x4OrderedAMD64(acc, p0, p1, p2, p3 archsimd.Float32x4) archsimd.Float32x4 {
	b0, b1, b2, b3 := p0.ToBits(), p1.ToBits(), p2.ToBits(), p3.ToBits()
	t0 := b0.InterleaveLo(b1).ReshapeToUint64s()
	t1 := b0.InterleaveHi(b1).ReshapeToUint64s()
	t2 := b2.InterleaveLo(b3).ReshapeToUint64s()
	t3 := b2.InterleaveHi(b3).ReshapeToUint64s()
	s0 := t0.InterleaveLo(t2).ReshapeToUint32s().BitsToFloat32()
	s1 := t0.InterleaveHi(t2).ReshapeToUint32s().BitsToFloat32()
	s2 := t1.InterleaveLo(t3).ReshapeToUint32s().BitsToFloat32()
	s3 := t1.InterleaveHi(t3).ReshapeToUint32s().BitsToFloat32()
	return acc.Add(s0).Add(s1).Add(s2).Add(s3)
}

// fma4x4OrderedAMD64 accumulates 4 consecutive samples of 4 parallel
// correlations into acc = [c0, c1, c2, c3] using fused multiply-add in exact
// ascending sample order.
func fma4x4OrderedAMD64(acc, va, v0, v1, v2, v3 archsimd.Float32x4) archsimd.Float32x4 {
	b0, b1, b2, b3 := v0.ToBits(), v1.ToBits(), v2.ToBits(), v3.ToBits()
	t0 := b0.InterleaveLo(b1).ReshapeToUint64s()
	t1 := b0.InterleaveHi(b1).ReshapeToUint64s()
	t2 := b2.InterleaveLo(b3).ReshapeToUint64s()
	t3 := b2.InterleaveHi(b3).ReshapeToUint64s()
	s0 := t0.InterleaveLo(t2).ReshapeToUint32s().BitsToFloat32()
	s1 := t0.InterleaveHi(t2).ReshapeToUint32s().BitsToFloat32()
	s2 := t1.InterleaveLo(t3).ReshapeToUint32s().BitsToFloat32()
	s3 := t1.InterleaveHi(t3).ReshapeToUint32s().BitsToFloat32()
	acc = va.ConcatPermuteScalars(0, 0, 0, 0, va).MulAdd(s0, acc)
	acc = va.ConcatPermuteScalars(1, 1, 1, 1, va).MulAdd(s1, acc)
	acc = va.ConcatPermuteScalars(2, 2, 2, 2, va).MulAdd(s2, acc)
	acc = va.ConcatPermuteScalars(3, 3, 3, 3, va).MulAdd(s3, acc)
	return acc
}

func toneLPCCorr(x []float32, cnt, delay, delay2 int) (r00, r01, r02 float32) {
	if cnt <= 0 {
		return
	}
	if toneLPCCorrUseAVX && cnt >= 4 {
		return toneLPCCorrAVX(x, cnt, delay, delay2)
	}
	x0 := x[:cnt:cnt]
	x1 := x[delay : delay+cnt : delay+cnt]
	x2 := x[delay2 : delay2+cnt : delay2+cnt]
	for i := range cnt {
		xi := x0[i]
		r00 += xi * xi
		r01 += xi * x1[i]
		r02 += xi * x2[i]
	}
	return
}

//go:noinline
func toneLPCCorrAVX(x []float32, cnt, delay, delay2 int) (r00, r01, r02 float32) {
	x0 := x[:cnt:cnt]
	x1 := x[delay : delay+cnt : delay+cnt]
	x2 := x[delay2 : delay2+cnt : delay2+cnt]
	var acc archsimd.Float32x4
	i := 0
	for ; i+4 <= cnt; i += 4 {
		va := archsimd.LoadFloat32x4Array((*[4]float32)(x0[i : i+4]))
		vb := archsimd.LoadFloat32x4Array((*[4]float32)(x1[i : i+4]))
		vc := archsimd.LoadFloat32x4Array((*[4]float32)(x2[i : i+4]))
		if celtAMD64SIMDUseFMA {
			acc = fma4x3OrderedAMD64(acc, va, va, vb, vc)
		} else {
			acc = add4x3OrderedAMD64(acc, va.Mul(va), va.Mul(vb), va.Mul(vc))
		}
	}
	r00 = acc.GetElem(0)
	r01 = acc.GetElem(1)
	r02 = acc.GetElem(2)
	for ; i < cnt; i++ {
		xi := x0[i]
		r00 += xi * xi
		r01 += xi * x1[i]
		r02 += xi * x2[i]
	}
	return
}

func toneLPCCorrDelay1(x []float32, cnt int) (r00, r01, r02 float32) {
	return toneLPCCorr(x, cnt, 1, 2)
}

// toneLPCRetry48kMonoFast computes all five 48 kHz mono retry-delay
// correlations (delays 2, 4, 8, 16, 32) in a single AVX prefix-shared pass.
// Because r00 is shared across all delays and r02(d) == r01(2d) over their
// common prefix, all 15 correlation sums reduce to 7 ordered accumulators
// packed into two Float32x4 registers: [s0, s2, s4, s8] and [s16, s32, s64, 0].
func toneLPCRetry48kMonoFast(x []float32, maxDelay int) (float32, float32, bool, int) {
	if !toneLPCCorrUseAVX || len(x) <= 64 {
		return toneLPCRetry48kMonoScalarFused(x, maxDelay)
	}
	return toneLPCRetry48kMonoFastAVX(x, maxDelay)
}

//go:noinline
func toneLPCRetry48kMonoFastAVX(x []float32, maxDelay int) (float32, float32, bool, int) {
	n := len(x)
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

	var accA, accB archsimd.Float32x4
	i := 0
	for ; i+4 <= cnt32; i += 4 {
		va := archsimd.LoadFloat32x4Array((*[4]float32)(x0[i : i+4]))
		v2 := archsimd.LoadFloat32x4Array((*[4]float32)(x2[i : i+4]))
		v4 := archsimd.LoadFloat32x4Array((*[4]float32)(x4[i : i+4]))
		v8 := archsimd.LoadFloat32x4Array((*[4]float32)(x8[i : i+4]))
		v16 := archsimd.LoadFloat32x4Array((*[4]float32)(x16[i : i+4]))
		v32 := archsimd.LoadFloat32x4Array((*[4]float32)(x32[i : i+4]))
		v64 := archsimd.LoadFloat32x4Array((*[4]float32)(x64[i : i+4]))
		if celtAMD64SIMDUseFMA {
			accA = fma4x4OrderedAMD64(accA, va, va, v2, v4, v8)
			accB = fma4x3OrderedAMD64(accB, va, v16, v32, v64)
		} else {
			accA = add4x4OrderedAMD64(accA, va.Mul(va), va.Mul(v2), va.Mul(v4), va.Mul(v8))
			accB = add4x3OrderedAMD64(accB, va.Mul(v16), va.Mul(v32), va.Mul(v64))
		}
	}
	if i < cnt32 {
		s0, s2, s4, s8 := accA.GetElem(0), accA.GetElem(1), accA.GetElem(2), accA.GetElem(3)
		s16, s32, s64 := accB.GetElem(0), accB.GetElem(1), accB.GetElem(2)
		for ; i < cnt32; i++ {
			a := x0[i]
			s0 += a * a
			s2 += a * x2[i]
			s4 += a * x4[i]
			s8 += a * x8[i]
			s16 += a * x16[i]
			s32 += a * x32[i]
			s64 += a * x64[i]
		}
		accA = archsimd.LoadFloat32x4Array(&[4]float32{s0, s2, s4, s8})
		accB = archsimd.LoadFloat32x4Array(&[4]float32{s16, s32, s64, 0})
	}
	r0032, r0132, r0232 := accA.GetElem(0), accB.GetElem(1), accB.GetElem(2)

	// cnt16 - cnt32 == 32 (always multiple of 4).
	for ; i+4 <= cnt16; i += 4 {
		va := archsimd.LoadFloat32x4Array((*[4]float32)(x0[i : i+4]))
		v2 := archsimd.LoadFloat32x4Array((*[4]float32)(x2[i : i+4]))
		v4 := archsimd.LoadFloat32x4Array((*[4]float32)(x4[i : i+4]))
		v8 := archsimd.LoadFloat32x4Array((*[4]float32)(x8[i : i+4]))
		v16 := archsimd.LoadFloat32x4Array((*[4]float32)(x16[i : i+4]))
		v32 := archsimd.LoadFloat32x4Array((*[4]float32)(x32[i : i+4]))
		if celtAMD64SIMDUseFMA {
			accA = fma4x4OrderedAMD64(accA, va, va, v2, v4, v8)
			accB = fma4x3OrderedAMD64(accB, va, v16, v32, archsimd.Float32x4{})
		} else {
			accA = add4x4OrderedAMD64(accA, va.Mul(va), va.Mul(v2), va.Mul(v4), va.Mul(v8))
			accB = add4x3OrderedAMD64(accB, va.Mul(v16), va.Mul(v32), archsimd.Float32x4{})
		}
	}
	r0016, r0116, r0216 := accA.GetElem(0), accB.GetElem(0), accB.GetElem(1)

	// cnt8 - cnt16 == 16 (always multiple of 4).
	s16 := accB.GetElem(0)
	for ; i+4 <= cnt8; i += 4 {
		va := archsimd.LoadFloat32x4Array((*[4]float32)(x0[i : i+4]))
		v2 := archsimd.LoadFloat32x4Array((*[4]float32)(x2[i : i+4]))
		v4 := archsimd.LoadFloat32x4Array((*[4]float32)(x4[i : i+4]))
		v8 := archsimd.LoadFloat32x4Array((*[4]float32)(x8[i : i+4]))
		if celtAMD64SIMDUseFMA {
			accA = fma4x4OrderedAMD64(accA, va, va, v2, v4, v8)
			s16 += x0[i] * x16[i]
			s16 += x0[i+1] * x16[i+1]
			s16 += x0[i+2] * x16[i+2]
			s16 += x0[i+3] * x16[i+3]
		} else {
			p16 := va.Mul(archsimd.LoadFloat32x4Array((*[4]float32)(x16[i : i+4])))
			accA = add4x4OrderedAMD64(accA, va.Mul(va), va.Mul(v2), va.Mul(v4), va.Mul(v8))
			s16 = (((s16 + p16.GetElem(0)) + p16.GetElem(1)) + p16.GetElem(2)) + p16.GetElem(3)
		}
	}
	r008, r018, r028 := accA.GetElem(0), accA.GetElem(3), s16

	// cnt4 - cnt8 == 8 (always multiple of 4).
	for ; i+4 <= cnt4; i += 4 {
		va := archsimd.LoadFloat32x4Array((*[4]float32)(x0[i : i+4]))
		v2 := archsimd.LoadFloat32x4Array((*[4]float32)(x2[i : i+4]))
		v4 := archsimd.LoadFloat32x4Array((*[4]float32)(x4[i : i+4]))
		v8 := archsimd.LoadFloat32x4Array((*[4]float32)(x8[i : i+4]))
		if celtAMD64SIMDUseFMA {
			accA = fma4x4OrderedAMD64(accA, va, va, v2, v4, v8)
		} else {
			accA = add4x4OrderedAMD64(accA, va.Mul(va), va.Mul(v2), va.Mul(v4), va.Mul(v8))
		}
	}
	r004, r014, r024 := accA.GetElem(0), accA.GetElem(2), accA.GetElem(3)

	// cnt2 - cnt4 == 4 (always 1 vector iteration).
	{
		va := archsimd.LoadFloat32x4Array((*[4]float32)(x0[i : i+4]))
		v2 := archsimd.LoadFloat32x4Array((*[4]float32)(x2[i : i+4]))
		v4 := archsimd.LoadFloat32x4Array((*[4]float32)(x4[i : i+4]))
		if celtAMD64SIMDUseFMA {
			accA = fma4x3OrderedAMD64(accA, va, va, v2, v4)
		} else {
			accA = add4x3OrderedAMD64(accA, va.Mul(va), va.Mul(v2), va.Mul(v4))
		}
	}
	r002, r012, r022 := accA.GetElem(0), accA.GetElem(1), accA.GetElem(2)

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
