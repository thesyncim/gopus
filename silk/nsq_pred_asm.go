//go:build arm64 || amd64

package silk

// shortTermPrediction16 computes 16-tap LPC prediction using native assembly.
// Returns 8 + sum((sLPCQ14[idx-k] * int16(aQ12[k])) >> 16) for k=0..15.
// This is equivalent to silk_noise_shape_quantizer_short_prediction_c with order=16.
//
//go:noescape
func shortTermPrediction16(sLPCQ14 []int32, idx int, aQ12 []int16) int32

// shortTermPrediction10 computes 10-tap LPC prediction using native assembly.
// Returns 5 + sum((sLPCQ14[idx-k] * int16(aQ12[k])) >> 16) for k=0..9.
// This is equivalent to silk_noise_shape_quantizer_short_prediction_c with order=10.
//
//go:noescape
func shortTermPrediction10(sLPCQ14 []int32, idx int, aQ12 []int16) int32
