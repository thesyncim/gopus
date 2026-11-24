//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

// mdctUseSSEForward runs the bulk of the forward MDCT pre- and post-rotation
// on eight float32 lanes (AVX2) or four float32 lanes (AVX1). The vector
// helpers remain behind noinline calls so unsupported instructions cannot
// cross this gate.
var mdctUseSSEForward = archsimd.X86.AVX()
var mdctUseAVX2Forward = archsimd.X86.AVX2() && (!mdctUseFMALikeMixEnabled || archsimd.X86.FMA())

// mdctMidRotateSSE is clt_mdct_forward_c's unwindowed fold plus pre-rotation
// for 4*blocks outputs starting at i0: re = in[xp2-2k], im = in[xp1+2k],
// yr = re*t0 - im*t1, yi = im*t0 + re*t1, each scaled and stored at
// fftStage[bitrev[i0+k]]. The loads read in[xp1 : xp1+8*blocks] and
// in[xp2-8*blocks+1 : xp2+1].
//
//go:noinline
func mdctMidRotateSSE(fftStage []float32, bitrev []int, in, trig []float32, i0, n4, xp1, xp2, blocks int, scale float32) {
	_ = in[xp1+8*blocks-1]
	_ = in[xp2]
	_ = in[xp2-8*blocks+1]
	_ = trig[n4+i0+4*blocks-1]
	br := bitrev[i0 : i0+4*blocks]
	b := 0
	if mdctUseAVX2Forward && blocks >= 2 {
		perm0213 := archsimd.LoadUint32x8Array(&kfBflyPerm0213U32x8)
		s8 := archsimd.BroadcastFloat32x8(scale)
		for ; b+2 <= blocks; b += 2 {
			i := i0 + 4*b
			im := mdctEvenAsc8(in[xp1+8*b:], perm0213)
			re := mdctEvenDesc8(in[xp2-8*b-15:], perm0213)
			t0 := archsimd.LoadFloat32x8Array((*[8]float32)(trig[i:]))
			t1 := archsimd.LoadFloat32x8Array((*[8]float32)(trig[n4+i:]))
			mdctRotateStore8(fftStage, br[4*b:], t0, t1, s8, re, im)
		}
		archsimd.ClearAVXUpperBits()
	}
	s4 := broadcastF32x4Arch(scale)
	for ; b < blocks; b++ {
		i := i0 + 4*b
		s1 := in[xp1+8*b:]
		lo1 := archsimd.LoadFloat32x4Array((*[4]float32)(s1[0:]))
		hi1 := archsimd.LoadFloat32x4Array((*[4]float32)(s1[4:]))
		im := lo1.ConcatPermuteScalars(0, 2, 4, 6, hi1)
		s2 := in[xp2-8*b-7:]
		lo2 := archsimd.LoadFloat32x4Array((*[4]float32)(s2[0:]))
		hi2 := archsimd.LoadFloat32x4Array((*[4]float32)(s2[4:]))
		re := hi2.ConcatPermuteScalars(3, 1, 7, 5, lo2)
		t0 := archsimd.LoadFloat32x4Array((*[4]float32)(trig[i:]))
		t1 := archsimd.LoadFloat32x4Array((*[4]float32)(trig[n4+i:]))
		mdctRotateStore(fftStage, br[4*b:], t0, t1, s4, re, im)
	}
}

// mdctPostTwiddleSSE is clt_mdct_forward_c's post-rotation for pairBlocks
// pairs of four-output blocks: forward outputs i..i+3 and their mirrors
// n4-4-i..n4-1-i, which together tile coeffs contiguously from both ends.
// yr = im*t1 - re*t0 lands at coeffs[2i] and yi = re*t1 + im*t0 at
// coeffs[n2-1-2i]. The caller finishes the n4%8 middle.
//
//go:noinline
func mdctPostTwiddleSSE(coeffs []float32, fftStage []float32, trig []float32, n2, n4, pairBlocks int) {
	if pairBlocks == 0 {
		return
	}
	coeffs = coeffs[:n2]
	trig = trig[:2*n4]
	ff := fftStage[:2*n4]
	if 8*pairBlocks > n4 {
		panic("mdct: post-twiddle blocks overlap")
	}
	b := 0
	if mdctUseAVX2Forward && pairBlocks >= 2 {
		perm0213 := archsimd.LoadUint32x8Array(&kfBflyPerm0213U32x8)
		for ; b+2 <= pairBlocks; b += 2 {
			i := 4 * b
			m := n4 - 8 - i
			reF, imF := bflyLoadCpx8AMD64(ff[2*i:], perm0213)
			t0F := archsimd.LoadFloat32x8Array((*[8]float32)(trig[i:]))
			t1F := archsimd.LoadFloat32x8Array((*[8]float32)(trig[n4+i:]))
			yrF := imF.Mul(t1F).Sub(reF.Mul(t0F))
			yiF := reF.Mul(t1F).Add(imF.Mul(t0F))
			if mdctUseFMALikeMixEnabled {
				yrF = imF.MulAdd(t1F, reF.Mul(t0F).Neg())
				yiF = reF.MulAdd(t1F, imF.Mul(t0F))
			}

			reM, imM := bflyLoadCpx8AMD64(ff[2*m:], perm0213)
			t0M := archsimd.LoadFloat32x8Array((*[8]float32)(trig[m:]))
			t1M := archsimd.LoadFloat32x8Array((*[8]float32)(trig[n4+m:]))
			yrM := imM.Mul(t1M).Sub(reM.Mul(t0M))
			yiM := reM.Mul(t1M).Add(imM.Mul(t0M))
			if mdctUseFMALikeMixEnabled {
				yrM = imM.MulAdd(t1M, reM.Mul(t0M).Neg())
				yiM = reM.MulAdd(t1M, imM.Mul(t0M))
			}

			bflyStoreCpx8AMD64(coeffs[2*i:], yrF, reverseF32x8(yiM))
			bflyStoreCpx8AMD64(coeffs[n2-16-2*i:], yrM, reverseF32x8(yiF))
		}
		archsimd.ClearAVXUpperBits()
	}
	for ; b < pairBlocks; b++ {
		i := 4 * b
		m := n4 - 4 - i
		reF, imF := mdctLoadCpx4(ff[2*i:])
		t0F := archsimd.LoadFloat32x4Array((*[4]float32)(trig[i:]))
		t1F := archsimd.LoadFloat32x4Array((*[4]float32)(trig[n4+i:]))
		yrF := imF.Mul(t1F).Sub(reF.Mul(t0F))
		yiF := reF.Mul(t1F).Add(imF.Mul(t0F))
		if mdctUseFMALikeMixEnabled {
			yrF = imF.MulAdd(t1F, reF.Mul(t0F).Neg())
			yiF = reF.MulAdd(t1F, imF.Mul(t0F))
		}

		reM, imM := mdctLoadCpx4(ff[2*m:])
		t0M := archsimd.LoadFloat32x4Array((*[4]float32)(trig[m:]))
		t1M := archsimd.LoadFloat32x4Array((*[4]float32)(trig[n4+m:]))
		yrM := imM.Mul(t1M).Sub(reM.Mul(t0M))
		yiM := reM.Mul(t1M).Add(imM.Mul(t0M))
		if mdctUseFMALikeMixEnabled {
			yrM = imM.MulAdd(t1M, reM.Mul(t0M).Neg())
			yiM = reM.MulAdd(t1M, imM.Mul(t0M))
		}

		low := coeffs[2*i:]
		rYiM := yiM.ToBits().PermuteScalars(3, 2, 1, 0)
		yrF.ToBits().InterleaveLo(rYiM).BitsToFloat32().StoreArray((*[4]float32)(low[0:]))
		yrF.ToBits().InterleaveHi(rYiM).BitsToFloat32().StoreArray((*[4]float32)(low[4:]))

		high := coeffs[n2-8-2*i:]
		rYiF := yiF.ToBits().PermuteScalars(3, 2, 1, 0)
		yrM.ToBits().InterleaveLo(rYiF).BitsToFloat32().StoreArray((*[4]float32)(high[0:]))
		yrM.ToBits().InterleaveHi(rYiF).BitsToFloat32().StoreArray((*[4]float32)(high[4:]))
	}
}

// mdctLoadCpx4 loads four interleaved {r, i} pairs and splits them into the
// real and imaginary lanes.
func mdctLoadCpx4(s []float32) (re, im archsimd.Float32x4) {
	a := archsimd.LoadFloat32x4Array((*[4]float32)(s[0:]))
	b := archsimd.LoadFloat32x4Array((*[4]float32)(s[4:]))
	return a.ConcatPermuteScalars(0, 2, 4, 6, b), a.ConcatPermuteScalars(1, 3, 5, 7, b)
}

// mdctEvenAsc returns s[0], s[2], s[4], s[6].
func mdctEvenAsc(s []float32) archsimd.Float32x4 {
	return archsimd.LoadFloat32x4Array((*[4]float32)(s[0:])).ConcatPermuteScalars(0, 2, 4, 6, archsimd.LoadFloat32x4Array((*[4]float32)(s[4:])))
}

// mdctEvenDesc returns s[7], s[5], s[3], s[1]: four samples stepping down by
// two from s[7].
func mdctEvenDesc(s []float32) archsimd.Float32x4 {
	return archsimd.LoadFloat32x4Array((*[4]float32)(s[4:])).ConcatPermuteScalars(3, 1, 7, 5, archsimd.LoadFloat32x4Array((*[4]float32)(s[0:])))
}

// mdctEvenAsc8 returns s[0], s[2], s[4], s[6], s[8], s[10], s[12], s[14].
func mdctEvenAsc8(s []float32, perm0213 archsimd.Uint32x8) archsimd.Float32x8 {
	lo := archsimd.LoadFloat32x8Array((*[8]float32)(s[0:]))
	hi := archsimd.LoadFloat32x8Array((*[8]float32)(s[8:]))
	return lo.ConcatPermuteScalarsGrouped(0, 2, 4, 6, hi).Permute(perm0213)
}

// mdctEvenDesc8 returns s[15], s[13], s[11], s[9], s[7], s[5], s[3], s[1].
func mdctEvenDesc8(s []float32, perm0213 archsimd.Uint32x8) archsimd.Float32x8 {
	lo := archsimd.LoadFloat32x8Array((*[8]float32)(s[0:]))
	hi := archsimd.LoadFloat32x8Array((*[8]float32)(s[8:]))
	odd := lo.ConcatPermuteScalarsGrouped(1, 3, 5, 7, hi).Permute(perm0213)
	return reverseF32x8(odd)
}

// mdctRotateStore8 pre-rotates eight folded (re, im) values and scatters them
// to fftStage[bitrev[0..7]].
func mdctRotateStore8(fftStage []float32, br []int, t0, t1, s8, re, im archsimd.Float32x8) {
	yr := re.Mul(t0).Sub(im.Mul(t1)).Mul(s8)
	yi := im.Mul(t0).Add(re.Mul(t1)).Mul(s8)
	var yrArr, yiArr [8]float32
	yr.StoreArray(&yrArr)
	yi.StoreArray(&yiArr)
	k := br[:8:8]
	k0, k1, k2, k3 := 2*k[0], 2*k[1], 2*k[2], 2*k[3]
	k4, k5, k6, k7 := 2*k[4], 2*k[5], 2*k[6], 2*k[7]
	fftStage[k0], fftStage[k0+1] = yrArr[0], yiArr[0]
	fftStage[k1], fftStage[k1+1] = yrArr[1], yiArr[1]
	fftStage[k2], fftStage[k2+1] = yrArr[2], yiArr[2]
	fftStage[k3], fftStage[k3+1] = yrArr[3], yiArr[3]
	fftStage[k4], fftStage[k4+1] = yrArr[4], yiArr[4]
	fftStage[k5], fftStage[k5+1] = yrArr[5], yiArr[5]
	fftStage[k6], fftStage[k6+1] = yrArr[6], yiArr[6]
	fftStage[k7], fftStage[k7+1] = yrArr[7], yiArr[7]
}

// mdctRotateStore is clt_mdct_forward_c's pre-rotation of four folded
// (re, im) values: yr = re*t0 - im*t1, yi = im*t0 + re*t1, each scaled and
// stored at fftStage[bitrev[i..i+3]], as mdctStoreDirectStage does per element.
func mdctRotateStore(fftStage []float32, br []int, t0, t1, s4, re, im archsimd.Float32x4) {
	yr := re.Mul(t0).Sub(im.Mul(t1)).Mul(s4)
	yi := im.Mul(t0).Add(re.Mul(t1)).Mul(s4)
	k := br[:4:4]
	k0, k1, k2, k3 := 2*k[0], 2*k[1], 2*k[2], 2*k[3]
	fftStage[k0], fftStage[k0+1] = yr.GetElem(0), yi.GetElem(0)
	fftStage[k1], fftStage[k1+1] = yr.GetElem(1), yi.GetElem(1)
	fftStage[k2], fftStage[k2+1] = yr.GetElem(2), yi.GetElem(2)
	fftStage[k3], fftStage[k3+1] = yr.GetElem(3), yi.GetElem(3)
}

// mdctLeadFoldSSE is clt_mdct_forward_c's leading windowed fold and
// pre-rotation for 4*blocks outputs from i0.
//
//go:noinline
func mdctLeadFoldSSE(fftStage []float32, bitrev []int, samples, window, trig []float32, i0, n4, n2, xp1, xp2, wp1, wp2, blocks int, scale float32) {
	_ = samples[xp1+n2+8*blocks-1]
	_ = samples[xp2]
	_ = samples[xp2-n2-8*blocks+1]
	_ = window[wp1+8*blocks-1]
	_ = window[wp2]
	_ = window[wp2-8*blocks+1]
	_ = trig[n4+i0+4*blocks-1]
	br := bitrev[i0 : i0+4*blocks]
	b := 0
	if mdctUseAVX2Forward && blocks >= 2 {
		perm0213 := archsimd.LoadUint32x8Array(&kfBflyPerm0213U32x8)
		s8 := archsimd.BroadcastFloat32x8(scale)
		for ; b+2 <= blocks; b += 2 {
			d := 8 * b
			a := mdctEvenAsc8(samples[xp1+n2+d:], perm0213)
			a2 := mdctEvenAsc8(samples[xp1+d:], perm0213)
			wC := mdctEvenAsc8(window[wp1+d:], perm0213)
			bv := mdctEvenDesc8(samples[xp2-d-15:], perm0213)
			b2 := mdctEvenDesc8(samples[xp2-n2-d-15:], perm0213)
			wD := mdctEvenDesc8(window[wp2-d-15:], perm0213)
			re := a.Mul(wD).Add(bv.Mul(wC))
			im := a2.Mul(wC).Sub(b2.Mul(wD))
			if mdctUseFMALikeMixEnabled {
				re = a.MulAdd(wD, bv.Mul(wC))
				im = a2.MulAdd(wC, b2.Mul(wD).Neg())
			}
			i := i0 + 4*b
			mdctRotateStore8(fftStage, br[4*b:], archsimd.LoadFloat32x8Array((*[8]float32)(trig[i:])), archsimd.LoadFloat32x8Array((*[8]float32)(trig[n4+i:])), s8, re, im)
		}
		archsimd.ClearAVXUpperBits()
	}
	s4 := broadcastF32x4Arch(scale)
	for ; b < blocks; b++ {
		d := 8 * b
		a := mdctEvenAsc(samples[xp1+n2+d:])
		a2 := mdctEvenAsc(samples[xp1+d:])
		wC := mdctEvenAsc(window[wp1+d:])
		bv := mdctEvenDesc(samples[xp2-d-7:])
		b2 := mdctEvenDesc(samples[xp2-n2-d-7:])
		wD := mdctEvenDesc(window[wp2-d-7:])
		re := a.Mul(wD).Add(bv.Mul(wC))
		im := a2.Mul(wC).Sub(b2.Mul(wD))
		if mdctUseFMALikeMixEnabled {
			re = a.MulAdd(wD, bv.Mul(wC))
			im = a2.MulAdd(wC, b2.Mul(wD).Neg())
		}
		i := i0 + 4*b
		mdctRotateStore(fftStage, br[4*b:], archsimd.LoadFloat32x4Array((*[4]float32)(trig[i:])), archsimd.LoadFloat32x4Array((*[4]float32)(trig[n4+i:])), s4, re, im)
	}
}

// mdctTailFoldSSE is clt_mdct_forward_c's trailing windowed fold and
// pre-rotation for 4*blocks outputs from i0.
//
//go:noinline
func mdctTailFoldSSE(fftStage []float32, bitrev []int, samples, window, trig []float32, i0, n4, n2, xp1, xp2, wp1, wp2, blocks int, scale float32) {
	_ = samples[xp1-n2]
	_ = samples[xp1+8*blocks-1]
	_ = samples[xp2+n2]
	_ = samples[xp2-8*blocks+1]
	_ = window[wp1+8*blocks-1]
	_ = window[wp2]
	_ = window[wp2-8*blocks+1]
	_ = trig[n4+i0+4*blocks-1]
	br := bitrev[i0 : i0+4*blocks]
	b := 0
	if mdctUseAVX2Forward && blocks >= 2 {
		perm0213 := archsimd.LoadUint32x8Array(&kfBflyPerm0213U32x8)
		s8 := archsimd.BroadcastFloat32x8(scale)
		for ; b+2 <= blocks; b += 2 {
			d := 8 * b
			a3 := mdctEvenAsc8(samples[xp1-n2+d:], perm0213)
			a2 := mdctEvenAsc8(samples[xp1+d:], perm0213)
			wC := mdctEvenAsc8(window[wp1+d:], perm0213)
			bv := mdctEvenDesc8(samples[xp2-d-15:], perm0213)
			b4 := mdctEvenDesc8(samples[xp2+n2-d-15:], perm0213)
			wD := mdctEvenDesc8(window[wp2-d-15:], perm0213)
			re := bv.Mul(wD).Sub(a3.Mul(wC))
			im := a2.Mul(wD).Add(b4.Mul(wC))
			if mdctUseFMALikeMixEnabled {
				re = bv.MulAdd(wD, a3.Mul(wC).Neg())
				im = a2.MulAdd(wD, b4.Mul(wC))
			}
			i := i0 + 4*b
			mdctRotateStore8(fftStage, br[4*b:], archsimd.LoadFloat32x8Array((*[8]float32)(trig[i:])), archsimd.LoadFloat32x8Array((*[8]float32)(trig[n4+i:])), s8, re, im)
		}
		archsimd.ClearAVXUpperBits()
	}
	s4 := broadcastF32x4Arch(scale)
	for ; b < blocks; b++ {
		d := 8 * b
		a3 := mdctEvenAsc(samples[xp1-n2+d:])
		a2 := mdctEvenAsc(samples[xp1+d:])
		wC := mdctEvenAsc(window[wp1+d:])
		bv := mdctEvenDesc(samples[xp2-d-7:])
		b4 := mdctEvenDesc(samples[xp2+n2-d-7:])
		wD := mdctEvenDesc(window[wp2-d-7:])
		re := bv.Mul(wD).Sub(a3.Mul(wC))
		im := a2.Mul(wD).Add(b4.Mul(wC))
		if mdctUseFMALikeMixEnabled {
			re = bv.MulAdd(wD, a3.Mul(wC).Neg())
			im = a2.MulAdd(wD, b4.Mul(wC))
		}
		i := i0 + 4*b
		mdctRotateStore(fftStage, br[4*b:], archsimd.LoadFloat32x4Array((*[4]float32)(trig[i:])), archsimd.LoadFloat32x4Array((*[4]float32)(trig[n4+i:])), s4, re, im)
	}
}
