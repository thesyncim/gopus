//go:build arm64 && goexperiment.simd && !nosimd && !purego

package silk

import "simd/archsimd"

const usePitchXcorrArm64SIMD = true

func celtPitchXcorrFloatImplArm64SIMD(x, y []float32, out []float32, length, maxPitch int) {
	if length <= 0 || maxPitch <= 0 {
		return
	}
	vectorPitchCount := maxPitch &^ 3
	x = x[:length]
	y = y[:length+maxPitch-1]
	out = out[:maxPitch]

	lag := 0
	for ; lag+8 <= vectorPitchCount; lag += 8 {
		pitchXcorrAccumulateEightLags(x, y[lag:], out[lag:lag+8], length)
	}
	for ; lag < vectorPitchCount; lag += 4 {
		yl := y[lag:]
		acc := archsimd.BroadcastFloat32x4(0)
		i := 0
		for ; i+8 < length; i += 8 {
			yi := yl[i : i+12 : i+12]
			y0 := archsimd.LoadFloat32x4Array((*[4]float32)(yi[0:4]))
			y1 := archsimd.LoadFloat32x4Array((*[4]float32)(yi[4:8]))
			y2 := archsimd.LoadFloat32x4Array((*[4]float32)(yi[8:12]))
			x0 := archsimd.LoadFloat32x4Array((*[4]float32)(x[i:]))
			x1 := archsimd.LoadFloat32x4Array((*[4]float32)(x[i+4:]))
			acc = y0.MulAdd(archsimd.BroadcastFloat32x4(x0.GetElem(0)), acc)
			acc = pitchXcorrShift4(y0, y1).MulAdd(archsimd.BroadcastFloat32x4(x0.GetElem(1)), acc)
			acc = pitchXcorrShift8(y0, y1).MulAdd(archsimd.BroadcastFloat32x4(x0.GetElem(2)), acc)
			acc = pitchXcorrShift12(y0, y1).MulAdd(archsimd.BroadcastFloat32x4(x0.GetElem(3)), acc)
			acc = y1.MulAdd(archsimd.BroadcastFloat32x4(x1.GetElem(0)), acc)
			acc = pitchXcorrShift4(y1, y2).MulAdd(archsimd.BroadcastFloat32x4(x1.GetElem(1)), acc)
			acc = pitchXcorrShift8(y1, y2).MulAdd(archsimd.BroadcastFloat32x4(x1.GetElem(2)), acc)
			acc = pitchXcorrShift12(y1, y2).MulAdd(archsimd.BroadcastFloat32x4(x1.GetElem(3)), acc)
		}
		if i+4 < length {
			yi := yl[i : i+8 : i+8]
			y0 := archsimd.LoadFloat32x4Array((*[4]float32)(yi[0:4]))
			y1 := archsimd.LoadFloat32x4Array((*[4]float32)(yi[4:8]))
			acc = y0.MulAdd(archsimd.BroadcastFloat32x4(x[i]), acc)
			acc = pitchXcorrShift4(y0, y1).MulAdd(archsimd.BroadcastFloat32x4(x[i+1]), acc)
			acc = pitchXcorrShift8(y0, y1).MulAdd(archsimd.BroadcastFloat32x4(x[i+2]), acc)
			acc = pitchXcorrShift12(y0, y1).MulAdd(archsimd.BroadcastFloat32x4(x[i+3]), acc)
			i += 4
		}
		for ; i < length; i++ {
			acc = archsimd.LoadFloat32x4Array((*[4]float32)(yl[i:])).MulAdd(archsimd.BroadcastFloat32x4(x[i]), acc)
		}
		acc.StoreArray((*[4]float32)(out[lag:]))
	}
	for lag := vectorPitchCount; lag < maxPitch; lag++ {
		out[lag] = pitchXcorrInnerProductArm64SIMD(x, y[lag:], length)
	}
}

// pitchXcorrAccumulateEightLags interleaves two ordered four-lag FMA chains.
func pitchXcorrAccumulateEightLags(x, y, out []float32, length int) {
	acc0 := archsimd.BroadcastFloat32x4(0)
	acc1 := archsimd.BroadcastFloat32x4(0)
	i := 0
	for ; i+8 < length; i += 8 {
		yi := y[i : i+16 : i+16]
		y0 := archsimd.LoadFloat32x4Array((*[4]float32)(yi[0:4]))
		y1 := archsimd.LoadFloat32x4Array((*[4]float32)(yi[4:8]))
		y2 := archsimd.LoadFloat32x4Array((*[4]float32)(yi[8:12]))
		y3 := archsimd.LoadFloat32x4Array((*[4]float32)(yi[12:16]))
		x0 := archsimd.LoadFloat32x4Array((*[4]float32)(x[i:]))
		x1 := archsimd.LoadFloat32x4Array((*[4]float32)(x[i+4:]))
		value := archsimd.BroadcastFloat32x4(x0.GetElem(0))
		acc0 = y0.MulAdd(value, acc0)
		acc1 = y1.MulAdd(value, acc1)
		value = archsimd.BroadcastFloat32x4(x0.GetElem(1))
		acc0 = pitchXcorrShift4(y0, y1).MulAdd(value, acc0)
		acc1 = pitchXcorrShift4(y1, y2).MulAdd(value, acc1)
		value = archsimd.BroadcastFloat32x4(x0.GetElem(2))
		acc0 = pitchXcorrShift8(y0, y1).MulAdd(value, acc0)
		acc1 = pitchXcorrShift8(y1, y2).MulAdd(value, acc1)
		value = archsimd.BroadcastFloat32x4(x0.GetElem(3))
		acc0 = pitchXcorrShift12(y0, y1).MulAdd(value, acc0)
		acc1 = pitchXcorrShift12(y1, y2).MulAdd(value, acc1)
		value = archsimd.BroadcastFloat32x4(x1.GetElem(0))
		acc0 = y1.MulAdd(value, acc0)
		acc1 = y2.MulAdd(value, acc1)
		value = archsimd.BroadcastFloat32x4(x1.GetElem(1))
		acc0 = pitchXcorrShift4(y1, y2).MulAdd(value, acc0)
		acc1 = pitchXcorrShift4(y2, y3).MulAdd(value, acc1)
		value = archsimd.BroadcastFloat32x4(x1.GetElem(2))
		acc0 = pitchXcorrShift8(y1, y2).MulAdd(value, acc0)
		acc1 = pitchXcorrShift8(y2, y3).MulAdd(value, acc1)
		value = archsimd.BroadcastFloat32x4(x1.GetElem(3))
		acc0 = pitchXcorrShift12(y1, y2).MulAdd(value, acc0)
		acc1 = pitchXcorrShift12(y2, y3).MulAdd(value, acc1)
	}
	if i+4 < length {
		yi := y[i : i+12 : i+12]
		y0 := archsimd.LoadFloat32x4Array((*[4]float32)(yi[0:4]))
		y1 := archsimd.LoadFloat32x4Array((*[4]float32)(yi[4:8]))
		y2 := archsimd.LoadFloat32x4Array((*[4]float32)(yi[8:12]))
		x0 := archsimd.LoadFloat32x4Array((*[4]float32)(x[i:]))
		value := archsimd.BroadcastFloat32x4(x0.GetElem(0))
		acc0 = y0.MulAdd(value, acc0)
		acc1 = y1.MulAdd(value, acc1)
		value = archsimd.BroadcastFloat32x4(x0.GetElem(1))
		acc0 = pitchXcorrShift4(y0, y1).MulAdd(value, acc0)
		acc1 = pitchXcorrShift4(y1, y2).MulAdd(value, acc1)
		value = archsimd.BroadcastFloat32x4(x0.GetElem(2))
		acc0 = pitchXcorrShift8(y0, y1).MulAdd(value, acc0)
		acc1 = pitchXcorrShift8(y1, y2).MulAdd(value, acc1)
		value = archsimd.BroadcastFloat32x4(x0.GetElem(3))
		acc0 = pitchXcorrShift12(y0, y1).MulAdd(value, acc0)
		acc1 = pitchXcorrShift12(y1, y2).MulAdd(value, acc1)
		i += 4
	}
	for ; i < length; i++ {
		value := archsimd.BroadcastFloat32x4(x[i])
		acc0 = archsimd.LoadFloat32x4Array((*[4]float32)(y[i:])).MulAdd(value, acc0)
		acc1 = archsimd.LoadFloat32x4Array((*[4]float32)(y[i+4:])).MulAdd(value, acc1)
	}
	acc0.StoreArray((*[4]float32)(out[0:4]))
	acc1.StoreArray((*[4]float32)(out[4:8]))
}

func pitchXcorrInnerProductArm64SIMD(x, y []float32, length int) float32 {
	x = x[:length]
	y = y[:length]
	acc := archsimd.BroadcastFloat32x4(0)
	i := 0
	// celt_inner_prod_neon calls vmlaq_f32(acc, x, y) at
	// pitch_neon_intr.c:196. Reversing Go MulAdd operands emits the same
	// vector FMLA source order and NaN payload precedence.
	for ; i+8 <= length; i += 8 {
		acc = archsimd.LoadFloat32x4Array((*[4]float32)(y[i:])).MulAdd(archsimd.LoadFloat32x4Array((*[4]float32)(x[i:])), acc)
		acc = archsimd.LoadFloat32x4Array((*[4]float32)(y[i+4:])).MulAdd(archsimd.LoadFloat32x4Array((*[4]float32)(x[i+4:])), acc)
	}
	if i+4 <= length {
		acc = archsimd.LoadFloat32x4Array((*[4]float32)(y[i:])).MulAdd(archsimd.LoadFloat32x4Array((*[4]float32)(x[i:])), acc)
		i += 4
	}
	// celt_inner_prod_neon reduces low+high pairs with vadd_f32 and then
	// vpadd_f32. Reversing the Go expression order matches the instruction
	// operand order and keeps the same NaN payload selection.
	sum := (acc.GetElem(2) + acc.GetElem(0)) + (acc.GetElem(3) + acc.GetElem(1))
	for ; i < length; i++ {
		// celt_inner_prod_neon finishes its scalar remainder with
		// MAC16_16(sum, x[i], y[i]) at pitch_neon_intr.c:214. This operand
		// order makes Go lower to the same arm64 FMADD source order, including
		// which NaN payload the scalar tail returns.
		sum = y[i]*x[i] + sum
	}
	return sum
}

func pitchXcorrShift4(lo, hi archsimd.Float32x4) archsimd.Float32x4 {
	return hi.ToBits().ReshapeToUint8s().ConcatShiftBytesRight(lo.ToBits().ReshapeToUint8s(), 4).ReshapeToUint32s().BitsToFloat32()
}

func pitchXcorrShift8(lo, hi archsimd.Float32x4) archsimd.Float32x4 {
	return hi.ToBits().ReshapeToUint8s().ConcatShiftBytesRight(lo.ToBits().ReshapeToUint8s(), 8).ReshapeToUint32s().BitsToFloat32()
}

func pitchXcorrShift12(lo, hi archsimd.Float32x4) archsimd.Float32x4 {
	return hi.ToBits().ReshapeToUint8s().ConcatShiftBytesRight(lo.ToBits().ReshapeToUint8s(), 12).ReshapeToUint32s().BitsToFloat32()
}
