package silk

// SILK stereo uses mid-side coding with prediction.
// Per RFC 6716 Section 4.2.8.
//
// Mid channel = (L + R) / 2
// Side channel = (L - R) / 2
// Plus stereo prediction weights for enhanced quality.
//
// libopus uses 80-level quantization (16 main levels x 5 sub-steps).
// The encoder quantizes and encodes the indices, the decoder decodes
// indices and dequantizes to get the prediction weights.

// stereoDecodePred decodes mid/side predictors from the bitstream.
// This is a direct port of libopus silk/stereo_decode_pred.c.
// Returns pred0 and pred1 in Q13 format.
// Note: pred0 is the delta (pred0_actual - pred1), pred1 is the second predictor.
func (d *Decoder) stereoDecodePred() (pred0, pred1 int32) {
	var ix [2][3]int

	// Entropy decoding
	// Decode joint index
	n := d.rangeDecoder.DecodeICDF(silk_stereo_pred_joint_iCDF, 8)
	ix[0][2] = n / 5
	ix[1][2] = n - 5*ix[0][2]

	// Decode individual indices
	for i := 0; i < 2; i++ {
		ix[i][0] = d.rangeDecoder.DecodeICDF(silk_uniform3_iCDF, 8)
		ix[i][1] = d.rangeDecoder.DecodeICDF(silk_uniform5_iCDF, 8)
	}

	// Dequantize
	const fixConst = 6554 // 0.5 / STEREO_QUANT_SUB_STEPS in Q16

	for i := 0; i < 2; i++ {
		// Reconstruct main index
		ix[i][0] += 3 * ix[i][2]

		// Clamp to valid range
		if ix[i][0] < 0 {
			ix[i][0] = 0
		}
		if ix[i][0] >= stereoQuantTabSize-1 {
			ix[i][0] = stereoQuantTabSize - 2
		}

		lowQ13 := int32(silk_stereo_pred_quant_Q13[ix[i][0]])
		highQ13 := int32(silk_stereo_pred_quant_Q13[ix[i][0]+1])

		// step_Q13 = (high - low) * 0.5 / STEREO_QUANT_SUB_STEPS
		stepQ13 := smulwb(highQ13-lowQ13, fixConst)

		// pred_Q13 = low_Q13 + step_Q13 * (2 * ix[n][1] + 1)
		if i == 0 {
			pred0 = lowQ13 + stepQ13*int32(2*ix[i][1]+1)
		} else {
			pred1 = lowQ13 + stepQ13*int32(2*ix[i][1]+1)
		}
	}

	// Subtract second from first predictor (delta coding)
	pred0 -= pred1

	return pred0, pred1
}

// stereoDecodeMidOnly decodes the mid-only flag from the bitstream.
// This is a direct port of libopus silk/stereo_decode_pred.c silk_stereo_decode_mid_only.
func (d *Decoder) stereoDecodeMidOnly() int {
	return d.rangeDecoder.DecodeICDF(silk_stereo_only_code_mid_iCDF, 8)
}

// decodeStereoWeights decodes the stereo prediction weights.
// Per RFC 6716 Section 4.2.8.
// Returns w0 and w1 in Q13 format (as int32 for full precision).
func (d *Decoder) decodeStereoWeights() (w0, w1 int32) {
	// Decode using 80-level quantization matching libopus
	pred0Delta, pred1 := d.stereoDecodePred()

	// pred0Delta is the delta: pred0_actual = pred0Delta + pred1
	w0 = pred0Delta + pred1
	w1 = pred1

	// Store for state tracking (convert to int16 for backward compatibility)
	d.prevStereoWeights[0] = int16(w0)
	d.prevStereoWeights[1] = int16(w1)

	return w0, w1
}

// stereoUnmix converts mid-side decoded samples to left-right.
// Per RFC 6716 Section 4.2.8.
//
// Basic mid-side: L = M + S, R = M - S
// With prediction weights:
//   - pred = (w0 * M[n] + w1 * M[n-1]) >> 13
//   - S' = S + pred
//   - L = M + S', R = M - S'
//
// For simplicity, we use the basic formula when weights are zero.
func stereoUnmix(mid, side []float32, w0, w1 int16, left, right []float32) error {
	if len(mid) != len(side) || len(mid) != len(left) || len(mid) != len(right) {
		return ErrMismatchedLengths
	}

	// Previous mid sample for prediction
	var prevMid float32

	for i := range mid {
		m := mid[i]
		s := side[i]

		// Apply stereo prediction if weights are non-zero
		if w0 != 0 || w1 != 0 {
			// pred = (w0 * m + w1 * prevMid) / 8192.0 (Q13 to float)
			pred := (float32(w0)*m + float32(w1)*prevMid) / 8192.0
			s += pred
		}

		// Unmix to left/right
		// L = M + S
		// R = M - S
		left[i] = m + s
		right[i] = m - s

		// Clamp to valid range [-1, 1]
		if left[i] > 1.0 {
			left[i] = 1.0
		} else if left[i] < -1.0 {
			left[i] = -1.0
		}
		if right[i] > 1.0 {
			right[i] = 1.0
		} else if right[i] < -1.0 {
			right[i] = -1.0
		}

		prevMid = m
	}
	return nil
}

// StereoUnmixInt32 is the same as stereoUnmix but accepts int32 weights.
// This is used when decoding with the new 80-level quantization.
func StereoUnmixInt32(mid, side []float32, w0, w1 int32, left, right []float32) error {
	return stereoUnmix(mid, side, int16(w0), int16(w1), left, right)
}
