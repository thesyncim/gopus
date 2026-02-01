// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides stereo mode encoding for the CELT encoder.

package celt

import "math"

// IntensityDecay is the decay parameter for intensity stereo Laplace encoding.
// Matches the decoder's expectation for stereo param decoding.
// Reference: libopus celt/celt_decoder.c, stereo parameter decoding
const IntensityDecay = 16384

// EncodeStereoParams encodes stereo mode parameters to the bitstream.
// For the initial implementation, this encodes mid-side stereo only:
// - intensity = nbBands (meaning no intensity stereo, all bands use mid-side)
// - dual_stereo = 0 (meaning mid-side mode, not dual stereo)
//
// Returns the intensity band (-1 since intensity stereo is disabled in this mode).
//
// The decoder reads stereo params in decodeStereoParams() which expects:
// 1. intensity band index encoded with Laplace model
// 2. dual_stereo flag encoded as single bit
//
// Reference: RFC 6716 Section 4.3.4, libopus celt/celt_decoder.c
func (e *Encoder) EncodeStereoParams(nbBands int) int {
	if e.rangeEncoder == nil {
		return -1
	}

	// For dual stereo mode (simpler, encoding L and R independently):
	// intensity = nbBands (disabled)
	// dual_stereo = 1 (enabled)
	e.encodeLaplaceIntensity(nbBands, IntensityDecay)

	// dual_stereo = 1 means "use dual stereo"
	// Encoded as a single bit with 50% probability
	e.rangeEncoder.EncodeBit(1, 1)

	// Return -1 to indicate intensity stereo is disabled
	return -1
}

// encodeLaplaceIntensity encodes the intensity stereo band using Laplace model.
// This mirrors the decoder's decodeLaplace for stereo params.
// val is the intensity band (nbBands for mid-side only mode).
func (e *Encoder) encodeLaplaceIntensity(val int, decay int) {
	re := e.rangeEncoder
	if re == nil {
		return
	}

	// Compute center frequency (probability of value 0)
	laplaceScale := laplaceFS - laplaceNMin
	fs0 := laplaceNMin + (laplaceScale*decay)>>15
	if fs0 > laplaceFS-1 {
		fs0 = laplaceFS - 1
	}

	if val == 0 {
		re.Encode(0, uint32(fs0), uint32(laplaceFS))
		return
	}

	// For positive values (intensity band is always >= 0)
	k := 1
	cumFL := fs0
	prevFk := fs0

	for k < val {
		fk := (prevFk * decay) >> 15
		if fk < laplaceNMin {
			fk = laplaceNMin
		}
		cumFL += fk
		prevFk = fk
		k++
	}

	fk := (prevFk * decay) >> 15
	if fk < laplaceNMin {
		fk = laplaceNMin
	}

	re.Encode(uint32(cumFL), uint32(cumFL+fk), uint32(laplaceFS))
}

// EncodeStereoParamsWithIntensity encodes stereo params with optional intensity stereo.
// intensityBand: band where intensity stereo starts (-1 to disable)
// dualStereo: true for dual stereo mode
//
// For future use when intensity stereo is implemented.
func (e *Encoder) EncodeStereoParamsWithIntensity(nbBands, intensityBand int, dualStereo bool) int {
	if e.rangeEncoder == nil {
		return -1
	}

	// Encode intensity band
	// If intensityBand < 0, encode nbBands (meaning no intensity stereo)
	encodeVal := nbBands
	if intensityBand >= 0 && intensityBand < nbBands {
		encodeVal = intensityBand
	}
	e.encodeLaplaceIntensity(encodeVal, IntensityDecay)

	// Encode dual_stereo flag
	var dualFlag int
	if dualStereo {
		dualFlag = 1
	}
	e.rangeEncoder.EncodeBit(dualFlag, 1)

	if intensityBand >= 0 && intensityBand < nbBands {
		return intensityBand
	}
	return -1
}

// ConvertToMidSide converts L/R stereo to mid/side representation.
// This is the inverse of MidSideToLR.
//
// The conversion is:
//
//	mid[i] = (left[i] + right[i]) / sqrt(2)
//	side[i] = (left[i] - right[i]) / sqrt(2)
//
// The sqrt(2) normalization preserves energy: |L|^2 + |R|^2 = |M|^2 + |S|^2
//
// Parameters:
//   - left: left channel samples
//   - right: right channel samples
//
// Returns: mid and side channel arrays
//
// Reference: RFC 6716 Section 4.3.4
func ConvertToMidSide(left, right []float64) (mid, side []float64) {
	n := len(left)
	if n == 0 {
		return nil, nil
	}

	// Handle mismatched lengths
	if len(right) < n {
		n = len(right)
	}
	if len(right) > len(left) {
		n = len(left)
	}

	mid = make([]float64, n)
	side = make([]float64, n)

	// sqrt(2) for energy preservation
	invSqrt2 := 1.0 / math.Sqrt(2.0)

	for i := 0; i < n; i++ {
		mid[i] = (left[i] + right[i]) * invSqrt2
		side[i] = (left[i] - right[i]) * invSqrt2
	}

	return mid, side
}

// ConvertToMidSideInPlace converts L/R to M/S in-place.
// The left array becomes mid, right array becomes side.
// More efficient when copies are not needed.
func ConvertToMidSideInPlace(left, right []float64) {
	n := len(left)
	if len(right) < n {
		n = len(right)
	}

	invSqrt2 := 1.0 / math.Sqrt(2.0)

	for i := 0; i < n; i++ {
		l := left[i]
		r := right[i]
		left[i] = (l + r) * invSqrt2  // mid
		right[i] = (l - r) * invSqrt2 // side
	}
}

// ConvertMidSideToLR converts mid/side to L/R representation.
// This is the inverse of ConvertToMidSide.
//
// The conversion is:
//
//	left[i] = (mid[i] + side[i]) / sqrt(2)
//	right[i] = (mid[i] - side[i]) / sqrt(2)
//
// Combined with ConvertToMidSide, this forms an identity transform:
// L,R -> M,S -> L,R (with floating point precision)
func ConvertMidSideToLR(mid, side []float64) (left, right []float64) {
	n := len(mid)
	if n == 0 {
		return nil, nil
	}

	if len(side) < n {
		n = len(side)
	}

	left = make([]float64, n)
	right = make([]float64, n)

	// Using sqrt(2)/2 = 1/sqrt(2) for reconstruction
	invSqrt2 := 1.0 / math.Sqrt(2.0)

	for i := 0; i < n; i++ {
		// Inverse of the forward transform
		// M = (L+R)/sqrt(2), S = (L-R)/sqrt(2)
		// L = (M+S)/sqrt(2), R = (M-S)/sqrt(2)
		// But we need L = (M+S)*sqrt(2)/2 = (M+S)/sqrt(2) ... wait
		// Actually: M*sqrt(2) = L+R, S*sqrt(2) = L-R
		// So: L = (M+S)*sqrt(2)/2 = (M+S)/sqrt(2) ... hmm
		// Let me reconsider: if M = (L+R)/sqrt(2), S = (L-R)/sqrt(2)
		// then L+R = M*sqrt(2), L-R = S*sqrt(2)
		// 2L = (M+S)*sqrt(2), L = (M+S)*sqrt(2)/2 = (M+S)/sqrt(2)
		// Same for R: R = (M-S)/sqrt(2)
		left[i] = (mid[i] + side[i]) * invSqrt2
		right[i] = (mid[i] - side[i]) * invSqrt2
	}

	return left, right
}

// DeinterleaveStereo separates interleaved stereo samples into L and R arrays.
// Input: [L0, R0, L1, R1, ...]
// Output: [L0, L1, ...], [R0, R1, ...]
func DeinterleaveStereo(interleaved []float64) (left, right []float64) {
	if len(interleaved) < 2 {
		return nil, nil
	}

	n := len(interleaved) / 2
	left = make([]float64, n)
	right = make([]float64, n)

	for i := 0; i < n; i++ {
		left[i] = interleaved[i*2]
		right[i] = interleaved[i*2+1]
	}

	return left, right
}

// deinterleaveStereoScratch separates interleaved stereo using scratch buffers.
// This avoids heap allocations in the hot path.
func deinterleaveStereoScratch(interleaved []float64, leftBuf, rightBuf *[]float64) (left, right []float64) {
	if len(interleaved) < 2 {
		return nil, nil
	}

	n := len(interleaved) / 2

	// Ensure left buffer is large enough
	if cap(*leftBuf) < n {
		*leftBuf = make([]float64, n)
	}
	left = (*leftBuf)[:n]

	// Ensure right buffer is large enough
	if cap(*rightBuf) < n {
		*rightBuf = make([]float64, n)
	}
	right = (*rightBuf)[:n]

	for i := 0; i < n; i++ {
		left[i] = interleaved[i*2]
		right[i] = interleaved[i*2+1]
	}

	return left, right
}

// InterleaveStereo combines separate L and R arrays into interleaved format.
// Input: [L0, L1, ...], [R0, R1, ...]
// Output: [L0, R0, L1, R1, ...]
func InterleaveStereo(left, right []float64) []float64 {
	n := len(left)
	if len(right) < n {
		n = len(right)
	}
	if n == 0 {
		return nil
	}

	interleaved := make([]float64, n*2)
	for i := 0; i < n; i++ {
		interleaved[i*2] = left[i]
		interleaved[i*2+1] = right[i]
	}

	return interleaved
}

// ComputeStereoAngle computes the stereo angle from L/R energies.
// Returns theta in radians [0, pi/2] representing the stereo image width.
// theta = 0: mono (all energy in mid)
// theta = pi/4: balanced stereo
// theta = pi/2: pure side (opposite channels)
func ComputeStereoAngle(energyL, energyR float64) float64 {
	if energyL <= 0 && energyR <= 0 {
		return 0 // Silent
	}

	// Convert to mid/side energies
	// energyM = (sqrt(energyL) + sqrt(energyR))^2 / 2 approximately
	// energyS = (sqrt(energyL) - sqrt(energyR))^2 / 2 approximately
	// For energy-based estimation, use direct ratio

	// theta = atan2(energyS, energyM) approximately
	// A simpler heuristic: theta ~ pi/4 * |energyL - energyR| / (energyL + energyR)
	totalEnergy := energyL + energyR
	if totalEnergy < 1e-30 {
		return 0
	}

	// Stereo correlation: high when energies are similar
	// Low when very different (wide stereo or hard-panned)
	balance := math.Abs(energyL-energyR) / totalEnergy

	// Map to angle: balance=0 -> theta=pi/4, balance=1 -> theta=0 or pi/2
	// For encoding purposes, we use mid-side where theta controls M/S balance
	return (math.Pi / 4) * (1 - balance)
}
