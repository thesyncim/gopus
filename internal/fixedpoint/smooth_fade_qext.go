//go:build gopus_fixed_point && gopus_qext

package fixedpoint

// SmoothFadeResQEXT ports ENABLE_QEXT opus_res smooth_fade from
// src/opus_decoder.c. The QEXT fixed build keeps the overlap window and
// coefficient products in Q31, including the P31-rounded coefficient times
// opus_res products.
func SmoothFadeResQEXT(in1, in2, out []int32, overlap, channels, sampleRate int) {
	if overlap <= 0 || channels <= 0 || sampleRate <= 0 {
		return
	}
	inc := 48000 / sampleRate
	if inc <= 0 {
		inc = 1
	}
	window := staticQEXTMDCT48000Window[:]
	for c := 0; c < channels; c++ {
		for i := 0; i < overlap; i++ {
			windowIndex := i * inc
			if windowIndex >= len(window) {
				break
			}
			w := mult32x32q31(window[windowIndex], window[windowIndex])
			idx := i*channels + c
			if idx >= len(out) || idx >= len(in1) || idx >= len(in2) {
				break
			}
			out[idx] = qextMulCoef32P31(w, in2[idx]) + qextMulCoef32P31(q31One-w, in1[idx])
		}
	}
}

func qextMulCoef32P31(a, b int32) int32 {
	return int32((int64(a)*int64(b) + 1<<30) >> 31)
}
