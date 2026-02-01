package silk

import (
	"math"

	"github.com/thesyncim/gopus/rangecoding"
)

// Constants for stereo prediction weight quantization matching libopus
// stereoQuantTabSize and stereoQuantSubSteps are defined in libopus_consts.go

// StereoQuantIndices holds the three quantization indices per predictor
// as used by libopus stereo_quant_pred.c
type StereoQuantIndices struct {
	// ix[n][0]: 0-2 (index within triplet)
	// ix[n][1]: 0-4 (sub-step index)
	// ix[n][2]: 0-4 (triplet index, derived from main index / 3)
	Ix [2][3]int8
}

// encodeStereo converts stereo to mid-side and computes prediction weights.
// Returns mid channel, side channel, and Q13 stereo weights.
// Per RFC 6716 Section 4.2.8.
func (e *Encoder) encodeStereo(left, right []float32) (mid []float32, side []float32, weights [2]int32) {
	n := len(left)
	mid = make([]float32, n)
	side = make([]float32, n)

	// Compute mid and side channels
	for i := 0; i < n; i++ {
		mid[i] = (left[i] + right[i]) / 2
		side[i] = (left[i] - right[i]) / 2
	}

	// Compute stereo prediction weights using linear regression
	// side[n] ~= w0 * mid[n] + w1 * mid[n-1]
	// Minimize sum((side[n] - w0*mid[n] - w1*mid[n-1])^2)
	var sumMM, sumMS, sumM1M1, sumM1S, sumMM1 float64

	for i := 1; i < n; i++ {
		m := float64(mid[i])
		m1 := float64(mid[i-1])
		s := float64(side[i])

		sumMM += m * m
		sumMS += m * s
		sumM1M1 += m1 * m1
		sumM1S += m1 * s
		sumMM1 += m * m1
	}

	// Solve 2x2 system for w0, w1
	// [sumMM   sumMM1] [w0]   [sumMS]
	// [sumMM1 sumM1M1] [w1] = [sumM1S]
	det := sumMM*sumM1M1 - sumMM1*sumMM1
	var w0, w1 float64
	if math.Abs(det) > 1e-10 {
		w0 = (sumMS*sumM1M1 - sumM1S*sumMM1) / det
		w1 = (sumMM*sumM1S - sumMM1*sumMS) / det
	}

	// Clamp to valid Q13 range [-16384, 16384] (approximately [-2, 2])
	// libopus uses [-1<<14, 1<<14] = [-16384, 16384]
	if w0 > 2.0 {
		w0 = 2.0
	}
	if w0 < -2.0 {
		w0 = -2.0
	}
	if w1 > 2.0 {
		w1 = 2.0
	}
	if w1 < -2.0 {
		w1 = -2.0
	}

	// Convert to Q13
	weights[0] = int32(w0 * 8192)
	weights[1] = int32(w1 * 8192)

	return mid, side, weights
}

// stereoQuantPred quantizes mid/side predictors using libopus 80-level quantization.
// This is a direct port of silk/stereo_quant_pred.c.
// predQ13 contains the predictors to quantize (modified in place).
// Returns the quantization indices.
func stereoQuantPred(predQ13 *[2]int32) StereoQuantIndices {
	var ix StereoQuantIndices

	const maxInt32 = int32(0x7FFFFFFF)

	// Quantize each predictor
	for n := 0; n < 2; n++ {
		// Brute-force search over quantization levels
		errMinQ13 := maxInt32
		var quantPredQ13 int32

		for i := 0; i < stereoQuantTabSize-1; i++ {
			lowQ13 := int32(silk_stereo_pred_quant_Q13[i])
			// step_Q13 = (high - low) * 0.5 / STEREO_QUANT_SUB_STEPS
			// In Q16: 0.5/5 = 0.1 = 6554 (approximately)
			highQ13 := int32(silk_stereo_pred_quant_Q13[i+1])
			stepQ13 := smulwb(highQ13-lowQ13, 6554) // 0.5/5 in Q16 = 6554

			for j := 0; j < stereoQuantSubSteps; j++ {
				// lvl_Q13 = low_Q13 + step_Q13 * (2*j + 1)
				lvlQ13 := lowQ13 + stepQ13*(int32(2*j+1))
				errQ13 := absInt32(predQ13[n] - lvlQ13)

				if errQ13 < errMinQ13 {
					errMinQ13 = errQ13
					quantPredQ13 = lvlQ13
					ix.Ix[n][0] = int8(i)
					ix.Ix[n][1] = int8(j)
				} else {
					// Error increasing, so we're past the optimum
					goto done
				}
			}
		}
	done:
		// Decompose index: ix[n][2] = ix[n][0] / 3, ix[n][0] = ix[n][0] % 3
		ix.Ix[n][2] = ix.Ix[n][0] / 3
		ix.Ix[n][0] = ix.Ix[n][0] - ix.Ix[n][2]*3
		predQ13[n] = quantPredQ13
	}

	// Subtract second from first predictor (delta coding)
	// This helps when actually applying these weights
	predQ13[0] -= predQ13[1]

	return ix
}

// stereoEncodePred encodes the stereo prediction indices to the bitstream.
// This is a direct port of silk/stereo_encode_pred.c.
func stereoEncodePred(enc *rangecoding.Encoder, ix StereoQuantIndices) {
	// Encode joint index: n = 5 * ix[0][2] + ix[1][2]
	n := 5*int(ix.Ix[0][2]) + int(ix.Ix[1][2])
	// Joint index must be < 25
	if n >= 25 {
		n = 24
	}
	enc.EncodeICDF(n, silk_stereo_pred_joint_iCDF, 8)

	// Encode individual indices for each predictor
	for i := 0; i < 2; i++ {
		// Encode ix[n][0] using uniform3 (3 symbols: 0, 1, 2)
		idx0 := int(ix.Ix[i][0])
		if idx0 < 0 {
			idx0 = 0
		}
		if idx0 > 2 {
			idx0 = 2
		}
		enc.EncodeICDF(idx0, silk_uniform3_iCDF, 8)

		// Encode ix[n][1] using uniform5 (5 symbols: 0, 1, 2, 3, 4)
		idx1 := int(ix.Ix[i][1])
		if idx1 < 0 {
			idx1 = 0
		}
		if idx1 > 4 {
			idx1 = 4
		}
		enc.EncodeICDF(idx1, silk_uniform5_iCDF, 8)
	}
}

// stereoEncodeMidOnly encodes the mid-only flag to the bitstream.
// This is a direct port of silk/stereo_encode_pred.c silk_stereo_encode_mid_only.
func stereoEncodeMidOnly(enc *rangecoding.Encoder, midOnlyFlag int8) {
	enc.EncodeICDF(int(midOnlyFlag), silk_stereo_only_code_mid_iCDF, 8)
}

// smulwb and absInt32 are defined in other files (resample_libopus.go and gain_encode.go)

// encodeStereoWeights encodes stereo prediction weights to bitstream.
// This implements the libopus 80-level quantization scheme per RFC 6716 Section 4.2.8.3.
func (e *Encoder) encodeStereoWeights(weights [2]int32) {
	// Quantize the predictors
	predQ13 := weights
	ix := stereoQuantPred(&predQ13)

	// Encode to bitstream
	stereoEncodePred(e.rangeEncoder, ix)

	// Store quantized weights for state tracking
	// Note: predQ13[0] now contains the delta (pred0 - pred1)
	// To get the actual values back:
	// quantized_pred1 = predQ13[1]
	// quantized_pred0 = predQ13[0] + predQ13[1]
	e.prevStereoWeights[0] = int16(predQ13[0] + predQ13[1])
	e.prevStereoWeights[1] = int16(predQ13[1])
}

// reconstructSide reconstructs side channel from mid using weights.
// For verification that encoding matches decoding.
func reconstructSide(mid []float32, weights [2]int16) []float32 {
	n := len(mid)
	side := make([]float32, n)

	w0 := float32(weights[0]) / 8192.0
	w1 := float32(weights[1]) / 8192.0

	for i := 0; i < n; i++ {
		var m1 float32
		if i > 0 {
			m1 = mid[i-1]
		}
		side[i] = w0*mid[i] + w1*m1
	}

	return side
}

// EncodeStereoMidSide is the public method to convert stereo to mid-side
// and compute prediction weights. Used by hybrid mode encoder.
func (e *Encoder) EncodeStereoMidSide(left, right []float32) (mid []float32, side []float32, weights [2]int32) {
	return e.encodeStereo(left, right)
}

// EncodeStereoWeightsToRange encodes stereo prediction weights to the range encoder.
// Used by hybrid mode encoder.
func (e *Encoder) EncodeStereoWeightsToRange(weights [2]int32) {
	if e.rangeEncoder == nil {
		return
	}
	e.encodeStereoWeights(weights)
}

// GetRangeEncoderPtr returns the current range encoder pointer.
// Used to share range encoder between mid and side channel encoders.
func (e *Encoder) GetRangeEncoderPtr() *rangecoding.Encoder {
	return e.rangeEncoder
}

// QuantizeStereoWeights quantizes stereo prediction weights and returns the indices.
// This is the public interface for external callers who need the indices.
func QuantizeStereoWeights(predQ13 [2]int32) (quantized [2]int32, ix StereoQuantIndices) {
	quantized = predQ13
	ix = stereoQuantPred(&quantized)
	return quantized, ix
}

// EncodeStereoIndices encodes pre-computed stereo quantization indices.
// Use this when you've already called QuantizeStereoWeights.
func EncodeStereoIndices(enc *rangecoding.Encoder, ix StereoQuantIndices) {
	stereoEncodePred(enc, ix)
}

// encodeStereoWithLPFilter converts stereo to mid-side with proper LP/HP filtering.
// This matches libopus stereo_LR_to_MS.c by applying LP and HP filtering before
// computing the stereo predictors, which improves prediction quality.
//
// The LP filter is a 3-tap FIR [1,2,1]/4 that separates low and high frequency content.
// Separate predictors are computed for LP and HP bands and combined.
//
// Parameters:
//   - left, right: input stereo signals (should have length frameLength+2 for look-ahead)
//   - frameLength: number of samples in the frame (not including look-ahead)
//   - fsKHz: sample rate in kHz (8, 12, or 16)
//
// Returns:
//   - mid, side: converted mid/side signals (length frameLength+2)
//   - predQ13: two prediction coefficients in Q13 format
func (e *Encoder) encodeStereoWithLPFilter(left, right []float32, frameLength, fsKHz int) (mid, side []float32, predQ13 [2]int32) {
	// Ensure we have enough samples (need frameLength + 2 for LP filter)
	inputLen := len(left)
	if inputLen < frameLength+2 {
		// Pad with zeros if needed
		padLeft := make([]float32, frameLength+2)
		padRight := make([]float32, frameLength+2)
		copy(padLeft, left)
		copy(padRight, right)
		left = padLeft
		right = padRight
	}

	// Convert L/R to basic M/S with look-ahead samples
	mid, side = stereoConvertLRToMSFloat(left, right, frameLength)

	// Prepare mid/side with history for LP filtering
	// We need to prepend history from previous frame
	midWithHistory := make([]float32, frameLength+2)
	sideWithHistory := make([]float32, frameLength+2)

	// Copy state (history) from previous frame
	midWithHistory[0] = float32(e.stereo.sMid[0]) / 32768.0
	midWithHistory[1] = float32(e.stereo.sMid[1]) / 32768.0
	sideWithHistory[0] = float32(e.stereo.sSide[0]) / 32768.0
	sideWithHistory[1] = float32(e.stereo.sSide[1]) / 32768.0

	// Copy current frame
	copy(midWithHistory[2:], mid[2:frameLength+2])
	copy(sideWithHistory[2:], side[2:frameLength+2])

	// Overwrite beginning of mid/side with history for correct LP filtering
	mid[0] = midWithHistory[0]
	mid[1] = midWithHistory[1]
	side[0] = sideWithHistory[0]
	side[1] = sideWithHistory[1]

	// Update state with last 2 samples for next frame
	e.stereo.sMid[0] = int16(mid[frameLength] * 32768)
	e.stereo.sMid[1] = int16(mid[frameLength+1] * 32768)
	e.stereo.sSide[0] = int16(side[frameLength] * 32768)
	e.stereo.sSide[1] = int16(side[frameLength+1] * 32768)

	// Apply LP/HP filtering
	lpMid, hpMid := stereoLPFilterFloat(mid, frameLength)
	lpSide, hpSide := stereoLPFilterFloat(side, frameLength)

	// Find predictors for LP and HP bands
	predLP := stereoFindPredictorFloat(lpMid, lpSide, frameLength)
	predHP := stereoFindPredictorFloat(hpMid, hpSide, frameLength)

	predQ13[0] = predLP
	predQ13[1] = predHP

	return mid, side, predQ13
}

// EncodeStereoLRToMS is the public method that matches libopus stereo_LR_to_MS.
// It converts stereo L/R to M/S with proper LP filtering for predictor analysis.
// This should be used instead of EncodeStereoMidSide for proper libopus compatibility.
//
// Parameters:
//   - left, right: input stereo signals
//   - frameLength: number of samples (e.g., 160 for 10ms at 16kHz)
//   - fsKHz: sample rate in kHz
//
// Returns:
//   - mid, side: output mid/side signals (with 2 history samples prepended)
//   - predQ13: prediction coefficients [0]=LP predictor, [1]=HP predictor
func (e *Encoder) EncodeStereoLRToMS(left, right []float32, frameLength, fsKHz int) (mid, side []float32, predQ13 [2]int32) {
	return e.encodeStereoWithLPFilter(left, right, frameLength, fsKHz)
}

// ResetStereoState resets the stereo encoder state.
// Call this when starting a new stream.
func (e *Encoder) ResetStereoState() {
	e.stereo = stereoEncState{}
	e.prevStereoWeights = [2]int16{0, 0}
}
