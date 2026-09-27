//go:build amd64 && goexperiment.simd && !nosimd

package celt

import (
	"simd/archsimd"

	"github.com/thesyncim/gopus/internal/opusmath"
)

var denormGainsUseAVX = archsimd.X86.AVX()

// denormalizeBandGains sets gains[band] to the denormalise_bands gain
// celt_exp2_db(MIN32(32, bandLogE[band] + eMeans[band])) for band in
// [start, end), four bands per vector. Each lane runs the scalar celt_exp2
// steps: floor, the unfused Horner polynomial, and the exponent-field add.
func denormalizeBandGains(gains []float32, energies []celtGLog, start, end int) {
	band := start
	if denormGainsUseAVX && end <= len(eMeans) && end <= len(energies) {
		lim := archsimd.BroadcastFloat32x4(32)
		for ; band+4 <= end; band += 4 {
			e := archsimd.LoadFloat32x4Array((*[4]float32)(energies[band : band+4])).
				Add(archsimd.LoadFloat32x4Array((*[4]float32)(eMeans[band : band+4])))
			// MIN32(32, e) keeps e unless 32 < e, so a NaN e stays NaN.
			e = lim.IfElse(lim.Less(e), e)
			celtExp2x4(e).StoreArray((*[4]float32)(gains[band : band+4]))
		}
	}
	for ; band < end; band++ {
		gains[band] = denormalizeBandGain(energies, band)
	}
}

// celtExp2x4 is celt_exp2 (celt/mathops.h, float build) on four lanes.
func celtExp2x4(x archsimd.Float32x4) archsimd.Float32x4 {
	integer := x.Floor().ConvertToInt32()
	frac := x.Sub(integer.ConvertToFloat32())
	res := frac.Mul(archsimd.BroadcastFloat32x4(opusmath.CeltExp2CoeffA5)).Add(archsimd.BroadcastFloat32x4(opusmath.CeltExp2CoeffA4))
	res = frac.Mul(res).Add(archsimd.BroadcastFloat32x4(opusmath.CeltExp2CoeffA3))
	res = frac.Mul(res).Add(archsimd.BroadcastFloat32x4(opusmath.CeltExp2CoeffA2))
	res = frac.Mul(res).Add(archsimd.BroadcastFloat32x4(opusmath.CeltExp2CoeffA1))
	res = frac.Mul(res).Add(archsimd.BroadcastFloat32x4(opusmath.CeltExp2CoeffA0))
	bits := res.AsInt32x4().Add(integer.ShiftAllLeft(23)).And(archsimd.BroadcastInt32x4(0x7fffffff))
	var zero archsimd.Float32x4
	return zero.IfElse(integer.Less(archsimd.BroadcastInt32x4(-50)), bits.AsFloat32x4())
}
