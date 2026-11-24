//go:build arm64 && goexperiment.simd && !nosimd && !purego

package silk

import "simd/archsimd"

func innerProductFLPArm64(a, b []float32, length int) silkCReal {
	if length <= 0 {
		return 0
	}
	a = a[:length:length]
	b = b[:length:length]
	var result silkCReal
	i := 0
	for ; i+4 <= length; i += 4 {
		va := archsimd.LoadFloat32x4Array((*[4]float32)(a[i : i+4]))
		vb := archsimd.LoadFloat32x4Array((*[4]float32)(b[i : i+4]))
		pLo := va.ConvertLo2ToFloat64().Mul(vb.ConvertLo2ToFloat64())
		aHi := va.ToBits().ReshapeToUint64s()
		bHi := vb.ToBits().ReshapeToUint64s()
		pHi := aHi.InterleaveHi(aHi).ReshapeToUint32s().BitsToFloat32().ConvertLo2ToFloat64().Mul(
			bHi.InterleaveHi(bHi).ReshapeToUint32s().BitsToFloat32().ConvertLo2ToFloat64())
		result += ((pLo.GetElem(0) + pLo.GetElem(1)) + pHi.GetElem(0)) + pHi.GetElem(1)
	}
	for ; i < length; i++ {
		result += silkCReal(a[i]) * silkCReal(b[i])
	}
	return result
}

func innerProductFLPImpl(a, b []float32, length int) silkCReal {
	if length <= 0 {
		return 0
	}
	return innerProductFLPArm64(a, b, length)
}
