//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

// mdctUseSSEForward runs the bulk of the forward MDCT pre- and post-rotation
// on four float32 lanes when the host supports AVX. The vector helpers remain
// behind noinline calls so unsupported instructions cannot cross this gate.
var mdctUseSSEForward = archsimd.X86.AVX()

// mdctMidRotateSSE is clt_mdct_forward_c's unwindowed fold plus pre-rotation
// for 4*blocks outputs starting at i0: re = in[xp2-2k], im = in[xp1+2k],
// yr = re*t0 - im*t1, yi = im*t0 + re*t1, each scaled and stored at
// fftStage[bitrev[i0+k]]. The loads read in[xp1 : xp1+8*blocks] and
// in[xp2-8*blocks+1 : xp2+1].
//
//go:noinline
func mdctMidRotateSSE(fftStage []kissCpx, bitrev []int, in, trig []float32, i0, n4, xp1, xp2, blocks int, scale float32) {
	_ = in[xp1+8*blocks-1]
	_ = in[xp2]
	_ = in[xp2-8*blocks+1]
	_ = trig[n4+i0+4*blocks-1]
	br := bitrev[i0 : i0+4*blocks]
	s4 := broadcastF32x4Arch(scale)
	for b := range blocks {
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
		yr := re.Mul(t0).Sub(im.Mul(t1)).Mul(s4)
		yi := im.Mul(t0).Add(re.Mul(t1)).Mul(s4)
		k := br[4*b : 4*b+4]
		fftStage[k[0]] = kissCpx{r: yr.GetElem(0), i: yi.GetElem(0)}
		fftStage[k[1]] = kissCpx{r: yr.GetElem(1), i: yi.GetElem(1)}
		fftStage[k[2]] = kissCpx{r: yr.GetElem(2), i: yi.GetElem(2)}
		fftStage[k[3]] = kissCpx{r: yr.GetElem(3), i: yi.GetElem(3)}
	}
}

// mdctPostTwiddleSSE is clt_mdct_forward_c's post-rotation for pairBlocks
// pairs of four-output blocks: forward outputs i..i+3 and their mirrors
// n4-4-i..n4-1-i, which together tile coeffs contiguously from both ends.
// yr = im*t1 - re*t0 lands at coeffs[2i] and yi = re*t1 + im*t0 at
// coeffs[n2-1-2i]. The caller finishes the n4%8 middle.
//
//go:noinline
func mdctPostTwiddleSSE(coeffs []float32, fftStage []kissCpx, trig []float32, n2, n4, pairBlocks int) {
	if pairBlocks == 0 {
		return
	}
	coeffs = coeffs[:n2]
	trig = trig[:2*n4]
	ff := kissCpxAsFloat32(fftStage[:n4])
	if 8*pairBlocks > n4 {
		panic("mdct: post-twiddle blocks overlap")
	}
	for b := range pairBlocks {
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

// mdctRotateStore is clt_mdct_forward_c's pre-rotation of four folded
// (re, im) values: yr = re*t0 - im*t1, yi = im*t0 + re*t1, each scaled and
// stored at fftStage[bitrev[i..i+3]], as mdctStoreDirectStage does per element.
func mdctRotateStore(fftStage []kissCpx, br []int, t0, t1, s4, re, im archsimd.Float32x4) {
	yr := re.Mul(t0).Sub(im.Mul(t1)).Mul(s4)
	yi := im.Mul(t0).Add(re.Mul(t1)).Mul(s4)
	k := br[:4:4]
	fftStage[k[0]] = kissCpx{r: yr.GetElem(0), i: yi.GetElem(0)}
	fftStage[k[1]] = kissCpx{r: yr.GetElem(1), i: yi.GetElem(1)}
	fftStage[k[2]] = kissCpx{r: yr.GetElem(2), i: yi.GetElem(2)}
	fftStage[k[3]] = kissCpx{r: yr.GetElem(3), i: yi.GetElem(3)}
}

// mdctLeadFoldSSE is clt_mdct_forward_c's leading windowed fold and
// pre-rotation for 4*blocks outputs from i0. AMD64 v3 fuses the first source
// product and rounds the second for each fold output; AMD64 v1/v2 round both
// products. The SIMD pre-rotation rounds both products on every target. The
// loads read samples[xp1+n2 : xp1+n2+8*blocks], samples[xp2-n2-8*blocks+1 :
// xp2+1], window[wp1 : wp1+8*blocks] and window[wp2-8*blocks+1 : wp2+1].
//
//go:noinline
func mdctLeadFoldSSE(fftStage []kissCpx, bitrev []int, samples, window, trig []float32, i0, n4, n2, xp1, xp2, wp1, wp2, blocks int, scale float32) {
	_ = samples[xp1+n2+8*blocks-1]
	_ = samples[xp2]
	_ = samples[xp2-n2-8*blocks+1]
	_ = window[wp1+8*blocks-1]
	_ = window[wp2]
	_ = window[wp2-8*blocks+1]
	_ = trig[n4+i0+4*blocks-1]
	br := bitrev[i0 : i0+4*blocks]
	s4 := broadcastF32x4Arch(scale)
	for b := range blocks {
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
			// clt_mdct_forward_c() contracts the first source product and
			// rounds the second product before the add/subtract.
			re = a.MulAdd(wD, bv.Mul(wC))
			im = a2.MulAdd(wC, b2.Mul(wD).Neg())
		}
		i := i0 + 4*b
		mdctRotateStore(fftStage, br[4*b:], archsimd.LoadFloat32x4Array((*[4]float32)(trig[i:])), archsimd.LoadFloat32x4Array((*[4]float32)(trig[n4+i:])), s4, re, im)
	}
}

// mdctTailFoldSSE is clt_mdct_forward_c's trailing windowed fold and
// pre-rotation for 4*blocks outputs from i0. AMD64 v3 contracts the second
// source product for the real fold output and the first for the imaginary
// output; AMD64 v1/v2 round both fold products. The SIMD pre-rotation rounds
// both products on every target. The
// loads read samples[xp1-n2 : xp1+8*blocks], samples[xp2-8*blocks+1 :
// xp2+n2+1], window[wp1 : wp1+8*blocks] and window[wp2-8*blocks+1 : wp2+1].
//
//go:noinline
func mdctTailFoldSSE(fftStage []kissCpx, bitrev []int, samples, window, trig []float32, i0, n4, n2, xp1, xp2, wp1, wp2, blocks int, scale float32) {
	_ = samples[xp1-n2]
	_ = samples[xp1+8*blocks-1]
	_ = samples[xp2+n2]
	_ = samples[xp2-8*blocks+1]
	_ = window[wp1+8*blocks-1]
	_ = window[wp2]
	_ = window[wp2-8*blocks+1]
	_ = trig[n4+i0+4*blocks-1]
	br := bitrev[i0 : i0+4*blocks]
	s4 := broadcastF32x4Arch(scale)
	for b := range blocks {
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
			// The leading minus in the C expression contracts with the second
			// product; the other output fuses its first source product.
			re = bv.MulAdd(wD, a3.Mul(wC).Neg())
			im = a2.MulAdd(wD, b4.Mul(wC))
		}
		i := i0 + 4*b
		mdctRotateStore(fftStage, br[4*b:], archsimd.LoadFloat32x4Array((*[4]float32)(trig[i:])), archsimd.LoadFloat32x4Array((*[4]float32)(trig[n4+i:])), s4, re, im)
	}
}
