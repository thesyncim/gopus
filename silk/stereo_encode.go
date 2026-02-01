package silk

import (
	"math"

	"github.com/thesyncim/gopus/rangecoding"
)

// encodeStereo converts stereo to mid-side and computes prediction weights.
// Returns mid channel, side channel, and Q13 stereo weights.
// Per RFC 6716 Section 4.2.8.
func (e *Encoder) encodeStereo(left, right []float32) (mid []float32, side []float32, weights [2]int16) {
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

	// Clamp to valid range [-1, 1]
	if w0 > 1.0 {
		w0 = 1.0
	}
	if w0 < -1.0 {
		w0 = -1.0
	}
	if w1 > 1.0 {
		w1 = 1.0
	}
	if w1 < -1.0 {
		w1 = -1.0
	}

	// Convert to Q13
	weights[0] = int16(w0 * 8192)
	weights[1] = int16(w1 * 8192)

	return mid, side, weights
}

// encodeStereoWeights encodes stereo prediction weights to bitstream.
// Per RFC 6716 Section 4.2.8.3.
// Uses existing ICDF tables: ICDFStereoPredWeight, ICDFStereoPredWeightDelta
func (e *Encoder) encodeStereoWeights(weights [2]int16) {
	if e.haveEncoded {
		// Delta coding from previous weights
		delta0 := weights[0] - e.prevStereoWeights[0]
		delta1 := weights[1] - e.prevStereoWeights[1]

		// Quantize delta to index [0, 7]
		idx0 := e.quantizeStereoWeightDelta(delta0)
		idx1 := e.quantizeStereoWeightDelta(delta1)

		e.rangeEncoder.EncodeICDF16(idx0, ICDFStereoPredWeightDelta, 8)
		e.rangeEncoder.EncodeICDF16(idx1, ICDFStereoPredWeightDelta, 8)
	} else {
		// First frame: encode absolute weights
		idx0 := e.quantizeStereoWeight(weights[0])
		idx1 := e.quantizeStereoWeight(weights[1])

		e.rangeEncoder.EncodeICDF16(idx0, ICDFStereoPredWeight, 8)
		e.rangeEncoder.EncodeICDF16(idx1, ICDFStereoPredWeight, 8)
	}

	e.prevStereoWeights = weights
}

// quantizeStereoWeight quantizes absolute weight to index [0, 7].
func (e *Encoder) quantizeStereoWeight(weight int16) int {
	// Map Q13 weight [-8192, 8192] to index [0, 7]
	// Linear mapping: idx = (weight + 8192) * 8 / 16384
	idx := int(weight+8192) * 8 / 16384
	if idx < 0 {
		idx = 0
	}
	if idx > 7 {
		idx = 7
	}
	return idx
}

// quantizeStereoWeightDelta quantizes weight delta to index [0, 7].
func (e *Encoder) quantizeStereoWeightDelta(delta int16) int {
	// Map delta to index centered at 4
	// Each step is ~1024 Q13 units
	idx := int(delta)/1024 + 4
	if idx < 0 {
		idx = 0
	}
	if idx > 7 {
		idx = 7
	}
	return idx
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
func (e *Encoder) EncodeStereoMidSide(left, right []float32) (mid []float32, side []float32, weights [2]int16) {
	return e.encodeStereo(left, right)
}

// EncodeStereoWeightsToRange encodes stereo prediction weights to the range encoder.
// Used by hybrid mode encoder.
func (e *Encoder) EncodeStereoWeightsToRange(weights [2]int16) {
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
