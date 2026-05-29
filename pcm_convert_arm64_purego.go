//go:build arm64 && purego

package gopus

import "math"

// The non-purego arm64 build converts float32 PCM to int16 with the NEON
// celt_float2int16 kernel (FMUL by 32768, FCVTAS, SMIN, SQXTN) over whole
// 16-sample blocks, then finishes the remainder with the scalar round-to-even
// float32ToInt16. FCVTAS rounds to nearest with ties away from zero, which
// differs from the round-to-even the scalar tail and the amd64 path emit. These
// pure-Go fallbacks reproduce the arm64 block/tail split exactly so the purego
// build matches the libopus arm64 oracle without hand-written assembly.

const pcmConvertBlock = 16

// fcvtasFloat32ToInt16 mirrors one lane of FCVTAS followed by SMIN/SQXTN: round
// v*32768 to nearest (ties away from zero) and saturate into int16.
func fcvtasFloat32ToInt16(v float32) int16 {
	y := v * 32768.0
	r := roundFloat32TiesAway(y)
	if r > 32767 {
		return 32767
	}
	if r < -32768 {
		return -32768
	}
	return int16(r)
}

// roundFloat32TiesAway rounds a float32 to the nearest integer with halfway
// cases rounded away from zero, matching AArch64 FCVTAS. Out-of-range and NaN
// inputs saturate to the int32 limits the way FCVTAS does before SQXTN narrows.
func roundFloat32TiesAway(y float32) int32 {
	if y != y { // NaN
		return 0
	}
	if y >= 2147483647.0 {
		return math.MaxInt32
	}
	if y <= -2147483648.0 {
		return math.MinInt32
	}
	if y >= 0 {
		return int32(math.Floor(float64(y) + 0.5))
	}
	return int32(math.Ceil(float64(y) - 0.5))
}

func convertFloat32ToInt16Unit(dst []int16, src []float32, n int) bool {
	if n <= 0 {
		return true
	}
	_ = src[n-1]
	_ = dst[n-1]
	blocks := n &^ (pcmConvertBlock - 1)
	for i := 0; i < blocks; i++ {
		v := src[i]
		// Matches FABS + FCMGE(1.0, |v|): out-of-range or NaN samples bail out so
		// the caller's soft-clip fallback reprocesses the whole frame.
		if !(v >= -1 && v <= 1) {
			return false
		}
		dst[i] = fcvtasFloat32ToInt16(v)
	}
	for i := blocks; i < n; i++ {
		v := src[i]
		if !(v >= -1 && v <= 1) {
			return false
		}
		dst[i] = float32ToInt16(v)
	}
	return true
}

func convertFloat32ToInt16NoSoftClipUnit(dst []int16, src []float32, n int) {
	if n <= 0 {
		return
	}
	_ = src[n-1]
	_ = dst[n-1]
	blocks := n &^ (pcmConvertBlock - 1)
	for i := 0; i < blocks; i++ {
		dst[i] = fcvtasFloat32ToInt16(src[i])
	}
	for i := blocks; i < n; i++ {
		dst[i] = float32ToInt16(src[i])
	}
}
