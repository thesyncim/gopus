//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

// haar1Scale is haar1's QCONST32(.70710678f,31) in the float build.
const haar1Scale = float32(0.7071067811865476)

var haar1UsesAVX2 = archsimd.X86.AVX2() && (!haar1UsesFMA || archsimd.X86.FMA())

// haar1Stride1 runs haar1's stride-1 butterfly over n0 (even, odd) pairs,
// eight pairs per step on AVX2 or four on AVX1.
func haar1Stride1(x []float32, n0 int) {
	if haar1UsesAVX2 && n0 >= 8 {
		haar1Stride1AVX2(x, n0)
		return
	}
	if !archsimd.X86.AVX() {
		haar1StrideScalarAMD64(x, n0, 1)
		return
	}
	haar1Stride1AVX(x, n0)
}

//go:noinline
func haar1Stride1AVX2(x []float32, n0 int) {
	if n0 <= 0 {
		return
	}
	x = x[:2*n0]
	scale := archsimd.BroadcastFloat32x8(haar1Scale)
	perm0213 := archsimd.LoadUint32x8Array(&kfBflyPerm0213U32x8)
	i := 0
	for ; i+8 <= n0; i += 8 {
		p := x[2*i : 2*i+16 : 2*i+16]
		even, odd := bflyLoadCpx8AMD64(p, perm0213)
		sum, diff := haar1ScaledPairVectors8(even, odd, scale)
		bflyStoreCpx8AMD64(p, sum, diff)
	}
	archsimd.ClearAVXUpperBits()
	if i+4 <= n0 {
		scale4 := broadcastF32x4Arch(haar1Scale)
		p := x[2*i : 2*i+8 : 2*i+8]
		a := archsimd.LoadFloat32x4Array((*[4]float32)(p[0:4]))
		b := archsimd.LoadFloat32x4Array((*[4]float32)(p[4:8]))
		even := a.ConcatPermuteScalars(0, 2, 4, 6, b)
		odd := a.ConcatPermuteScalars(1, 3, 5, 7, b)
		sum, diff := haar1ScaledPairVectors(even, odd, scale4)
		sumBits := sum.ToBits()
		diffBits := diff.ToBits()
		sumBits.InterleaveLo(diffBits).BitsToFloat32().StoreArray((*[4]float32)(p[0:4]))
		sumBits.InterleaveHi(diffBits).BitsToFloat32().StoreArray((*[4]float32)(p[4:8]))
		i += 4
	}
	for ; i < n0; i++ {
		haar1PairNorm(x, 2*i, 2*i+1, haar1Scale)
	}
}

//go:noinline
func haar1Stride1AVX(x []float32, n0 int) {
	if n0 <= 0 {
		return
	}
	x = x[:2*n0]
	scale := broadcastF32x4Arch(haar1Scale)
	i := 0
	for ; i+4 <= n0; i += 4 {
		p := x[2*i : 2*i+8 : 2*i+8]
		a := archsimd.LoadFloat32x4Array((*[4]float32)(p[0:4]))
		b := archsimd.LoadFloat32x4Array((*[4]float32)(p[4:8]))
		even := a.ConcatPermuteScalars(0, 2, 4, 6, b)
		odd := a.ConcatPermuteScalars(1, 3, 5, 7, b)
		sum, diff := haar1ScaledPairVectors(even, odd, scale)
		sumBits := sum.ToBits()
		diffBits := diff.ToBits()
		sumBits.InterleaveLo(diffBits).BitsToFloat32().StoreArray((*[4]float32)(p[0:4]))
		sumBits.InterleaveHi(diffBits).BitsToFloat32().StoreArray((*[4]float32)(p[4:8]))
	}
	for ; i < n0; i++ {
		haar1PairNorm(x, 2*i, 2*i+1, haar1Scale)
	}
}

// haar1Stride2 runs haar1's stride-2 butterfly over n0 groups of four,
// pairing x[4j+i] with x[4j+2+i]; two groups per step.
func haar1Stride2(x []float32, n0 int) {
	if !archsimd.X86.AVX() {
		haar1StrideScalarAMD64(x, n0, 2)
		return
	}
	haar1Stride2AVX(x, n0)
}

//go:noinline
func haar1Stride2AVX(x []float32, n0 int) {
	if n0 <= 0 {
		return
	}
	x = x[:4*n0]
	scale := broadcastF32x4Arch(haar1Scale)
	i := 0
	for ; i+2 <= n0; i += 2 {
		p := x[4*i : 4*i+8 : 4*i+8]
		a := archsimd.LoadFloat32x4Array((*[4]float32)(p[0:4]))
		b := archsimd.LoadFloat32x4Array((*[4]float32)(p[4:8]))
		lo := a.ConcatPermuteScalars(0, 1, 4, 5, b)
		hi := a.ConcatPermuteScalars(2, 3, 6, 7, b)
		sum, diff := haar1ScaledPairVectors(lo, hi, scale)
		sum.ConcatPermuteScalars(0, 1, 4, 5, diff).StoreArray((*[4]float32)(p[0:4]))
		sum.ConcatPermuteScalars(2, 3, 6, 7, diff).StoreArray((*[4]float32)(p[4:8]))
	}
	for ; i < n0; i++ {
		off := 4 * i
		haar1PairNorm(x, off, off+2, haar1Scale)
		haar1PairNorm(x, off+1, off+3, haar1Scale)
	}
}

// haar1Stride4 runs haar1's stride-4 butterfly over n0 groups of eight,
// pairing the low and high four lanes of each group.
func haar1Stride4(x []float32, n0 int) {
	if !archsimd.X86.AVX() {
		haar1StrideScalarAMD64(x, n0, 4)
		return
	}
	haar1Stride4AVX(x, n0)
}

//go:noinline
func haar1Stride4AVX(x []float32, n0 int) {
	if n0 <= 0 {
		return
	}
	x = x[:8*n0]
	scale := broadcastF32x4Arch(haar1Scale)
	for i := range n0 {
		p := x[8*i : 8*i+8 : 8*i+8]
		lo := archsimd.LoadFloat32x4Array((*[4]float32)(p[0:4]))
		hi := archsimd.LoadFloat32x4Array((*[4]float32)(p[4:8]))
		sum, diff := haar1ScaledPairVectors(lo, hi, scale)
		sum.StoreArray((*[4]float32)(p[0:4]))
		diff.StoreArray((*[4]float32)(p[4:8]))
	}
}

// haar1StrideScalarAMD64 keeps the selected per-pair operation order when the
// host does not support AVX.
func haar1StrideScalarAMD64(x []float32, n0, stride int) {
	for i := 0; i < n0; i++ {
		base := 2 * stride * i
		for j := 0; j < stride; j++ {
			haar1PairNorm(x, base+j, base+stride+j, haar1Scale)
		}
	}
}

func haar1ScaledPairVectors(first, second, scale archsimd.Float32x4) (sum, diff archsimd.Float32x4) {
	if haar1UsesFMA {
		roundedSecond := second.Mul(scale)
		return first.MulAdd(scale, roundedSecond), first.MulAdd(scale, roundedSecond.Neg())
	}
	first = first.Mul(scale)
	second = second.Mul(scale)
	return first.Add(second), first.Sub(second)
}

func haar1ScaledPairVectors8(first, second, scale archsimd.Float32x8) (sum, diff archsimd.Float32x8) {
	if haar1UsesFMA {
		roundedSecond := second.Mul(scale)
		return first.MulAdd(scale, roundedSecond), first.MulAdd(scale, roundedSecond.Neg())
	}
	first = first.Mul(scale)
	second = second.Mul(scale)
	return first.Add(second), first.Sub(second)
}
