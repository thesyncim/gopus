//go:build arm64 && goexperiment.simd && !nosimd

package silk

import "simd/archsimd"

const usePitchXcorrArm64SIMD = true

func celtPitchXcorrFloatImplArm64SIMD(x, y []float32, out []float32, length, maxPitch int) {
	if length <= 0 || maxPitch <= 0 {
		return
	}
	vectorPitchCount := maxPitch &^ 3
	_ = x[length-1]
	_ = y[length+maxPitch-2]
	_ = out[maxPitch-1]

	for lag := 0; lag < vectorPitchCount; lag += 4 {
		acc := archsimd.BroadcastFloat32x4(0)
		i := 0
		for ; i+8 < length; i += 8 {
			y0 := archsimd.LoadFloat32x4(y[lag+i:])
			y1 := archsimd.LoadFloat32x4(y[lag+i+4:])
			y2 := archsimd.LoadFloat32x4(y[lag+i+8:])
			acc = y0.MulAdd(archsimd.BroadcastFloat32x4(x[i]), acc)
			acc = pitchXcorrShift4(y0, y1).MulAdd(archsimd.BroadcastFloat32x4(x[i+1]), acc)
			acc = pitchXcorrShift8(y0, y1).MulAdd(archsimd.BroadcastFloat32x4(x[i+2]), acc)
			acc = pitchXcorrShift12(y0, y1).MulAdd(archsimd.BroadcastFloat32x4(x[i+3]), acc)
			acc = y1.MulAdd(archsimd.BroadcastFloat32x4(x[i+4]), acc)
			acc = pitchXcorrShift4(y1, y2).MulAdd(archsimd.BroadcastFloat32x4(x[i+5]), acc)
			acc = pitchXcorrShift8(y1, y2).MulAdd(archsimd.BroadcastFloat32x4(x[i+6]), acc)
			acc = pitchXcorrShift12(y1, y2).MulAdd(archsimd.BroadcastFloat32x4(x[i+7]), acc)
		}
		if i+4 < length {
			y0 := archsimd.LoadFloat32x4(y[lag+i:])
			y1 := archsimd.LoadFloat32x4(y[lag+i+4:])
			acc = y0.MulAdd(archsimd.BroadcastFloat32x4(x[i]), acc)
			acc = pitchXcorrShift4(y0, y1).MulAdd(archsimd.BroadcastFloat32x4(x[i+1]), acc)
			acc = pitchXcorrShift8(y0, y1).MulAdd(archsimd.BroadcastFloat32x4(x[i+2]), acc)
			acc = pitchXcorrShift12(y0, y1).MulAdd(archsimd.BroadcastFloat32x4(x[i+3]), acc)
			i += 4
		}
		for ; i < length; i++ {
			acc = archsimd.LoadFloat32x4(y[lag+i:]).MulAdd(archsimd.BroadcastFloat32x4(x[i]), acc)
		}
		acc.Store(out[lag : lag+4])
	}
	for lag := vectorPitchCount; lag < maxPitch; lag++ {
		out[lag] = pitchXcorrInnerProductArm64SIMD(x, y[lag:], length)
	}
}

func pitchXcorrInnerProductArm64SIMD(x, y []float32, length int) float32 {
	acc := archsimd.BroadcastFloat32x4(0)
	i := 0
	for ; i+8 <= length; i += 8 {
		acc = archsimd.LoadFloat32x4(x[i:]).MulAdd(archsimd.LoadFloat32x4(y[i:]), acc)
		acc = archsimd.LoadFloat32x4(x[i+4:]).MulAdd(archsimd.LoadFloat32x4(y[i+4:]), acc)
	}
	if i+4 <= length {
		acc = archsimd.LoadFloat32x4(x[i:]).MulAdd(archsimd.LoadFloat32x4(y[i:]), acc)
		i += 4
	}
	sum := (acc.GetElem(0) + acc.GetElem(2)) + (acc.GetElem(1) + acc.GetElem(3))
	for ; i < length; i++ {
		sum += noFMA32(x[i], y[i])
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
