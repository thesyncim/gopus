package silk

// SILK stereo uses mid-side coding with prediction.
// Per RFC 6716 Section 4.2.8.
//
// Mid channel = (L + R) / 2
// Side channel = (L - R) / 2
// Plus stereo prediction weights for enhanced quality.

// decodeStereoWeights decodes the stereo prediction weights.
// Per RFC 6716 Section 4.2.8.
// Returns w0 and w1 in Q13 format.
func (d *Decoder) decodeStereoWeights() (w0, w1 int16) {
	// Decode prediction weight index
	// Uses ICDFStereoPredWeight for first value or ICDFStereoPredWeightDelta for delta
	var predIdx int
	if d.haveDecoded {
		// Delta from previous weight
		delta := d.rangeDecoder.DecodeICDF16(ICDFStereoPredWeightDelta, 8)
		predIdx = int(d.prevStereoWeights[0]) + delta - 4
		if predIdx < 0 {
			predIdx = 0
		}
		if predIdx > 7 {
			predIdx = 7
		}
	} else {
		predIdx = d.rangeDecoder.DecodeICDF16(ICDFStereoPredWeight, 8)
	}

	// Clamp predIdx to valid range [0, 7] to guard against corrupted bitstream
	if predIdx < 0 {
		predIdx = 0
	}
	if predIdx > 7 {
		predIdx = 7
	}

	// Look up weights from table (Q13)
	// Weights control how much mid predicts side
	// Per RFC 6716 Section 4.2.8:
	// w0 = pred_Q13[predIdx]
	// w1 = pred_Q13[7 - predIdx]
	w0 = stereoPredWeights[predIdx]
	w1 = stereoPredWeights[7-predIdx]

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
func stereoUnmix(mid, side []float32, w0, w1 int16, left, right []float32) {
	if len(mid) != len(side) || len(mid) != len(left) || len(mid) != len(right) {
		panic("stereoUnmix: mismatched lengths")
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
}

// Stereo prediction weights in Q13 format per RFC 6716 Section 4.2.8.
// Index 0-7 maps to prediction coefficient.
// Values are symmetric: stereoPredWeights[i] relates to stereoPredWeights[7-i].
var stereoPredWeights = [8]int16{
	-13732, // -1.677 in Q13 (strong negative)
	-10050, // -1.227
	-5765,  // -0.703
	-1776,  // -0.217
	1776,   // 0.217
	5765,   // 0.703
	10050,  // 1.227
	13732,  // 1.677 (strong positive)
}
