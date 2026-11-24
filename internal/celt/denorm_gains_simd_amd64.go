//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import (
	"simd/archsimd"

	"github.com/thesyncim/gopus/internal/opusmath"
)

var denormGainsUseAVX = archsimd.X86.AVX()
var denormGainsUseAVX2 = archsimd.X86.AVX2() && (!denormTargetV3FMA || archsimd.X86.FMA())
var denormAbsMaskI32x4 = [4]int32{0x7fffffff, 0x7fffffff, 0x7fffffff, 0x7fffffff}
var denormBelowLimitI32x4 = [4]int32{-50, -50, -50, -50}
var denormAbsMaskI32x8 = [8]int32{0x7fffffff, 0x7fffffff, 0x7fffffff, 0x7fffffff, 0x7fffffff, 0x7fffffff, 0x7fffffff, 0x7fffffff}
var denormBelowLimitI32x8 = [8]int32{-50, -50, -50, -50, -50, -50, -50, -50}

// denormalizeBandGains sets gains[band] to the denormalise_bands gain
// celt_exp2_db(MIN32(32, bandLogE[band] + eMeans[band])) for band in
// [start, end), eight bands per vector on AVX2 or four on AVX1. Each lane runs
// the scalar celt_exp2 steps: floor, the target's Horner polynomial, and the
// exponent-field add.
func denormalizeBandGains(gains []float32, energies []celtGLog, start, end int) {
	band := start
	if end <= len(eMeans) && end <= len(energies) {
		if denormGainsUseAVX2 && end-start >= 8 {
			band = denormalizeBandGainsAVX2(gains, energies, start, end)
		} else if denormGainsUseAVX {
			band = denormalizeBandGainsAVX(gains, energies, start, end)
		}
	}
	for ; band < end; band++ {
		gains[band] = denormalizeBandGain(energies, band)
	}
}

//go:noinline
func denormalizeBandGainsAVX2(gains []float32, energies []celtGLog, start, end int) int {
	lim := archsimd.BroadcastFloat32x8(32)
	band := start
	for ; band+8 <= end; band += 8 {
		e := archsimd.LoadFloat32x8Array((*[8]float32)(energies[band : band+8])).
			Add(archsimd.LoadFloat32x8Array((*[8]float32)(eMeans[band : band+8])))
		// MIN32(32, e) keeps e unless 32 < e, so a NaN e stays NaN.
		e = lim.IfElse(lim.Less(e), e)
		celtExp2x8(e).StoreArray((*[8]float32)(gains[band : band+8]))
	}
	archsimd.ClearAVXUpperBits()
	if band+4 <= end {
		lim4 := broadcastF32x4Arch(32)
		e := archsimd.LoadFloat32x4Array((*[4]float32)(energies[band : band+4])).
			Add(archsimd.LoadFloat32x4Array((*[4]float32)(eMeans[band : band+4])))
		e = lim4.IfElse(lim4.Less(e), e)
		celtExp2x4(e).StoreArray((*[4]float32)(gains[band : band+4]))
		band += 4
	}
	return band
}

// celtExp2x8 is celt_exp2 (celt/mathops.h, float build) on eight lanes.
func celtExp2x8(x archsimd.Float32x8) archsimd.Float32x8 {
	integer := x.Floor().ConvertToInt32()
	frac := x.Sub(integer.ConvertToFloat32())
	var res archsimd.Float32x8
	if denormTargetV3FMA {
		res = frac.MulAdd(archsimd.BroadcastFloat32x8(opusmath.CeltExp2CoeffA5), archsimd.BroadcastFloat32x8(opusmath.CeltExp2CoeffA4))
		res = frac.MulAdd(res, archsimd.BroadcastFloat32x8(opusmath.CeltExp2CoeffA3))
		res = frac.MulAdd(res, archsimd.BroadcastFloat32x8(opusmath.CeltExp2CoeffA2))
		res = frac.MulAdd(res, archsimd.BroadcastFloat32x8(opusmath.CeltExp2CoeffA1))
		res = frac.MulAdd(res, archsimd.BroadcastFloat32x8(opusmath.CeltExp2CoeffA0))
	} else {
		res = frac.Mul(archsimd.BroadcastFloat32x8(opusmath.CeltExp2CoeffA5)).Add(archsimd.BroadcastFloat32x8(opusmath.CeltExp2CoeffA4))
		res = frac.Mul(res).Add(archsimd.BroadcastFloat32x8(opusmath.CeltExp2CoeffA3))
		res = frac.Mul(res).Add(archsimd.BroadcastFloat32x8(opusmath.CeltExp2CoeffA2))
		res = frac.Mul(res).Add(archsimd.BroadcastFloat32x8(opusmath.CeltExp2CoeffA1))
		res = frac.Mul(res).Add(archsimd.BroadcastFloat32x8(opusmath.CeltExp2CoeffA0))
	}
	bits := res.AsInt32x8().Add(integer.ShiftAllLeft(23)).And(archsimd.LoadInt32x8Array(&denormAbsMaskI32x8))
	var zero archsimd.Float32x8
	return zero.IfElse(integer.Less(archsimd.LoadInt32x8Array(&denormBelowLimitI32x8)), bits.AsFloat32x8())
}

//go:noinline
func denormalizeBandGainsAVX(gains []float32, energies []celtGLog, start, end int) int {
	lim := broadcastF32x4Arch(32)
	band := start
	for ; band+4 <= end; band += 4 {
		e := archsimd.LoadFloat32x4Array((*[4]float32)(energies[band : band+4])).
			Add(archsimd.LoadFloat32x4Array((*[4]float32)(eMeans[band : band+4])))
		// MIN32(32, e) keeps e unless 32 < e, so a NaN e stays NaN.
		e = lim.IfElse(lim.Less(e), e)
		celtExp2x4(e).StoreArray((*[4]float32)(gains[band : band+4]))
	}
	return band
}

// celtExp2x4 is celt_exp2 (celt/mathops.h, float build) on four lanes.
func celtExp2x4(x archsimd.Float32x4) archsimd.Float32x4 {
	integer := x.Floor().ConvertToInt32()
	frac := x.Sub(integer.ConvertToFloat32())
	var res archsimd.Float32x4
	if denormTargetV3FMA {
		// GCC contracts the scalar libopus Horner polynomial on x86-64-v3.
		res = frac.MulAdd(broadcastF32x4Arch(opusmath.CeltExp2CoeffA5), broadcastF32x4Arch(opusmath.CeltExp2CoeffA4))
		res = frac.MulAdd(res, broadcastF32x4Arch(opusmath.CeltExp2CoeffA3))
		res = frac.MulAdd(res, broadcastF32x4Arch(opusmath.CeltExp2CoeffA2))
		res = frac.MulAdd(res, broadcastF32x4Arch(opusmath.CeltExp2CoeffA1))
		res = frac.MulAdd(res, broadcastF32x4Arch(opusmath.CeltExp2CoeffA0))
	} else {
		res = frac.Mul(broadcastF32x4Arch(opusmath.CeltExp2CoeffA5)).Add(broadcastF32x4Arch(opusmath.CeltExp2CoeffA4))
		res = frac.Mul(res).Add(broadcastF32x4Arch(opusmath.CeltExp2CoeffA3))
		res = frac.Mul(res).Add(broadcastF32x4Arch(opusmath.CeltExp2CoeffA2))
		res = frac.Mul(res).Add(broadcastF32x4Arch(opusmath.CeltExp2CoeffA1))
		res = frac.Mul(res).Add(broadcastF32x4Arch(opusmath.CeltExp2CoeffA0))
	}
	bits := res.AsInt32x4().Add(integer.ShiftAllLeft(23)).And(archsimd.LoadInt32x4Array(&denormAbsMaskI32x4))
	var zero archsimd.Float32x4
	return zero.IfElse(integer.Less(archsimd.LoadInt32x4Array(&denormBelowLimitI32x4)), bits.AsFloat32x4())
}
