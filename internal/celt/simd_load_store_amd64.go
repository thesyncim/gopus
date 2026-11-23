//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import (
	"simd/archsimd"
)

// f32x4SignBitMask is loaded from memory because BroadcastUint32x4 currently
// lowers through an AVX2-only broadcast on amd64. These bitwise helpers keep
// the 128-bit CELT paths usable on AVX-only CPUs while avoiding the AVX2
// lowering in archsimd.Float32x4.Abs and Neg.
var f32x4SignBitMask = [4]uint32{0x80000000, 0x80000000, 0x80000000, 0x80000000}

func absF32x4AVX(x archsimd.Float32x4) archsimd.Float32x4 {
	return x.ToBits().And(archsimd.LoadUint32x4Array(&absSumMask)).BitsToFloat32()
}

func negF32x4AVX(x archsimd.Float32x4) archsimd.Float32x4 {
	return x.ToBits().Xor(archsimd.LoadUint32x4Array(&f32x4SignBitMask)).BitsToFloat32()
}
