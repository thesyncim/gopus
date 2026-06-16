//go:build arm64 && goexperiment.simd && !purego

package silk

import (
	"simd/archsimd"
	"unsafe"
)

// floatToInt16ScaledCore writes out[i] = sat16(round_even(in[i]*scale)) for the
// first n elements (n a multiple of 8) with archsimd. The op sequence mirrors the
// hand NEON kernel and the scalar reference bit-for-bit:
//
//   - Mul(scale): the same single-rounding scaled product.
//   - Round(): VFRINTN, round-to-nearest with ties to even, matching the scalar
//     round-half-to-even (Float32ToInt16Raw) and the asm FCVTNS rounding.
//   - ConvertToInt32(): VFCVTZS truncates toward zero, but the value is already an
//     exact integer after Round, so the truncation is lossless.
//   - SaturateToInt16(): VSQXTN narrows int32 to int16 with signed saturation,
//     packing the 4 results into the low half of an Int16x8 — the SQXTN/SQXTN2
//     clamp to [-32768, 32767], proven equal to the scalar clamp-then-round order
//     by TestFloatToInt16ScaledBoundaries.
//
// Two int32 quads are saturated separately, then their low halves are merged with
// a VZIP1 (Uint64x2.InterleaveLo) into one 8-lane vector, reproducing SQXTN2.
// Loads and stores use raw advancing pointers to drop the per-access bounds check.
func floatToInt16ScaledCore(out []int16, in []float32, scale float32, n int) {
	s := archsimd.BroadcastFloat32x4(scale)
	ip := unsafe.Pointer(unsafe.SliceData(in))
	op := unsafe.Pointer(unsafe.SliceData(out))
	for i := 0; i+8 <= n; i += 8 {
		lo := loadF32x4(ip).Mul(s).Round().ConvertToInt32().SaturateToInt16()
		hi := loadF32x4(unsafe.Add(ip, 16)).Mul(s).Round().ConvertToInt32().SaturateToInt16()
		merged := lo.ToBits().ReshapeToUint64s().
			InterleaveLo(hi.ToBits().ReshapeToUint64s()).
			ReshapeToUint16s().BitsToInt16()
		storeInt16x8(op, merged)
		ip = unsafe.Add(ip, 32)
		op = unsafe.Add(op, 16)
	}
}

// floatToInt16Scaled vectorizes the bulk of the conversion and finishes the
// sub-block remainder with the scalar reference.
func floatToInt16Scaled(out []int16, in []float32, scale float32, n int) {
	n8 := n &^ 7
	if n8 > 0 {
		floatToInt16ScaledCore(out, in, scale, n8)
	}
	for i := n8; i < n; i++ {
		out[i] = floatToInt16Round(in[i] * scale)
	}
}
