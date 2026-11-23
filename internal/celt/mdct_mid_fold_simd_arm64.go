//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

// mdctUseNeonMidFold enables the archsimd middle-fold/store kernel.
const mdctUseNeonMidFold = true

// mdctMidFoldStoreNeon writes blocks*4 outputs of the forward-MDCT middle fold.
// Per output j: re=samples[xp2-2j] (descending), im=samples[xp1+2j] (ascending),
//
//	yr = (re*trig[i0+j] - round(im*trig[n4+i0+j])) * preScale
//	yi = (im*trig[i0+j] + round(re*trig[n4+i0+j])) * preScale
//	dst[bitrev[i0+j]] = {yr, yi}
//
// The compute is vectorized four lanes at a time (ConcatEven deinterleave, the
// descending re reversed with reverse4, fused MulAdd, scaling Mul), matching
// mdctStoreDirectStageFMALike bit-for-bit; the bit-reversed output placement
// remains scalar.
func mdctMidFoldStoreNeon(dst []kissCpx, bitrev []int, samples []float32, trig []float32, i0, n4, xp1, xp2, blocks int, preScale float32) {
	if blocks == 0 {
		return
	}
	_ = samples[xp1+8*blocks-1]
	_ = samples[xp2+1]
	_ = samples[xp2-6-8*(blocks-1)]
	_ = trig[n4+i0+4*blocks-1]
	_ = bitrev[i0+4*blocks-1]
	pv := archsimd.BroadcastFloat32x4(preScale)
	imOff := xp1
	reOff := xp2 - 6
	t0Off := i0
	t1Off := n4 + i0
	for b := 0; b < blocks; b++ {
		imSlice := samples[imOff:]
		reSlice := samples[reOff:]
		im := archsimd.LoadFloat32x4Array((*[4]float32)(imSlice)).ToBits().
			ConcatEven(archsimd.LoadFloat32x4Array((*[4]float32)(imSlice[4:])).ToBits()).BitsToFloat32()
		re := reverse4(archsimd.LoadFloat32x4Array((*[4]float32)(reSlice)).ToBits().
			ConcatEven(archsimd.LoadFloat32x4Array((*[4]float32)(reSlice[4:])).ToBits()).BitsToFloat32())
		t0 := archsimd.LoadFloat32x4Array((*[4]float32)(trig[t0Off:]))
		t1 := archsimd.LoadFloat32x4Array((*[4]float32)(trig[t1Off:]))
		yr := re.MulAdd(t0, im.Mul(t1).Neg()).Mul(pv)
		yi := im.MulAdd(t0, re.Mul(t1)).Mul(pv)
		br := bitrev[t0Off : t0Off+4]
		dst[br[0]] = kissCpx{r: yr.GetElem(0), i: yi.GetElem(0)}
		dst[br[1]] = kissCpx{r: yr.GetElem(1), i: yi.GetElem(1)}
		dst[br[2]] = kissCpx{r: yr.GetElem(2), i: yi.GetElem(2)}
		dst[br[3]] = kissCpx{r: yr.GetElem(3), i: yi.GetElem(3)}
		imOff += 8
		reOff -= 8
		t0Off += 4
		t1Off += 4
	}
}
