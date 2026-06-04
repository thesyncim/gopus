package celt

import (
	"math"

	"github.com/thesyncim/gopus/internal/opusmath"
)

// Stereo processing for CELT decoding.
// CELT supports three stereo coding modes:
// 1. Mid-side stereo: encode M=(L+R)/2 and S=(L-R)/2 with angular rotation
// 2. Intensity stereo: encode mono and spread with optional sign flip
// 3. Dual stereo: encode left and right independently
//
// Reference: RFC 6716 Section 4.3.4, libopus celt/bands.c

// StereoMode specifies the stereo coding mode for a band.
type StereoMode int

const (
	// StereoMidSide uses mid-side encoding with theta rotation.
	// Good for correlated stereo content (most music).
	StereoMidSide StereoMode = iota

	// StereoIntensity uses mono with optional sign inversion.
	// Used for high frequency bands to save bits.
	StereoIntensity

	// StereoDual encodes left and right independently.
	// Used when channels are uncorrelated.
	StereoDual
)

// String returns the string representation of the stereo mode.
func (sm StereoMode) String() string {
	switch sm {
	case StereoMidSide:
		return "mid-side"
	case StereoIntensity:
		return "intensity"
	case StereoDual:
		return "dual"
	default:
		return "unknown"
	}
}

// MidSideToLR converts mid-side stereo to left-right.
// The conversion uses a rotation matrix controlled by theta:
//
//	L = cos(theta) * M + sin(theta) * S
//	R = cos(theta) * M - sin(theta) * S
//
// Parameters:
//   - mid: mid channel coefficients (M = (L+R)/2)
//   - side: side channel coefficients (S = (L-R)/2)
//   - theta: stereo angle in radians (0 = mono, pi/2 = full stereo)
//
// Returns: left and right channel coefficient arrays
func MidSideToLR(mid, side []float32, theta float32) (left, right []float32) {
	n := len(mid)
	if n == 0 {
		return nil, nil
	}

	// Handle mismatched lengths
	if len(side) != n {
		// If side is empty/shorter, treat as mono
		side = make([]float32, n)
	}

	left = make([]float32, n)
	right = make([]float32, n)

	cosT := opusmath.CosF32(theta)
	sinT := opusmath.SinF32(theta)

	for i := 0; i < n; i++ {
		// Rotation matrix:
		// [L]   [cos(theta)  sin(theta)] [M]
		// [R] = [cos(theta) -sin(theta)] [S]
		left[i] = cosT*mid[i] + sinT*side[i]
		right[i] = cosT*mid[i] - sinT*side[i]
	}

	return left, right
}

// MidSideToLRGains converts mid-side to left-right using precomputed gains.
// This is more efficient when gains are already computed from theta.
//
// Parameters:
//   - mid, side: frequency-domain coefficients
//   - midGain, sideGain: rotation gains (cos(theta), sin(theta))
//
// Returns: left and right coefficient arrays
func MidSideToLRGains(mid, side []float32, midGain, sideGain float32) (left, right []float32) {
	n := len(mid)
	if n == 0 {
		return nil, nil
	}

	if len(side) != n {
		side = make([]float32, n)
	}

	left = make([]float32, n)
	right = make([]float32, n)

	for i := 0; i < n; i++ {
		left[i] = midGain*mid[i] + sideGain*side[i]
		right[i] = midGain*mid[i] - sideGain*side[i]
	}

	return left, right
}

// IntensityStereo creates stereo from mono with optional inversion.
// In intensity stereo mode, both channels share the same spectral shape
// but may have opposite signs. This is efficient for high-frequency
// content where the ear is less sensitive to phase.
//
// Parameters:
//   - mono: the mid channel coefficients
//   - invert: if true, right channel is inverted (sign flipped)
//
// Returns: left and right coefficient arrays
func IntensityStereo(mono []float32, invert bool) (left, right []float32) {
	n := len(mono)
	if n == 0 {
		return nil, nil
	}

	left = make([]float32, n)
	right = make([]float32, n)

	copy(left, mono)

	if invert {
		for i := 0; i < n; i++ {
			right[i] = -mono[i]
		}
	} else {
		copy(right, mono)
	}

	return left, right
}

// DualStereoSplit handles dual stereo mode where channels are independent.
// Simply returns copies of the input slices for consistent interface.
//
// Parameters:
//   - coeffsL, coeffsR: independently decoded left and right coefficients
//
// Returns: left and right arrays (copies)
func DualStereoSplit(coeffsL, coeffsR []float32) (left, right []float32) {
	left = make([]float32, len(coeffsL))
	right = make([]float32, len(coeffsR))
	copy(left, coeffsL)
	copy(right, coeffsR)
	return left, right
}

// GetStereoMode determines the stereo mode for a band.
// The mode depends on:
//   - band index relative to intensity stereo start band
//   - whether dual stereo mode is enabled
//   - bit allocation for the band
//
// Parameters:
//   - band: band index (0 to nbBands-1)
//   - intensityBand: band where intensity stereo starts (-1 if not used)
//   - dualStereo: true if dual stereo mode is enabled
//
// Returns: the stereo mode to use for this band
func GetStereoMode(band, intensityBand int, dualStereo bool) StereoMode {
	// Check intensity stereo first
	if intensityBand >= 0 && band >= intensityBand {
		return StereoIntensity
	}

	// Dual stereo for explicitly flagged bands
	if dualStereo {
		return StereoDual
	}

	// Default to mid-side
	return StereoMidSide
}

// ComputeTheta converts quantized itheta to angle in radians.
// itheta is quantized to qn steps over [0, pi/2].
//
// Parameters:
//   - itheta: quantized angle (0 to qn)
//   - qn: number of quantization steps
//
// Returns: theta in radians [0, pi/2]
func ComputeTheta(itheta, qn int) float32 {
	if qn <= 0 {
		return 0
	}
	return float32(itheta) * float32(math.Pi/2) / float32(qn)
}

// ComputeGains converts itheta to mid and side gains.
// This is equivalent to cos(theta) and sin(theta).
//
// Parameters:
//   - itheta: quantized angle (0 to qn)
//   - qn: number of quantization steps
//
// Returns: mid gain (cos), side gain (sin)
func ComputeGains(itheta, qn int) (midGain, sideGain float32) {
	theta := ComputeTheta(itheta, qn)
	return opusmath.CosF32(theta), opusmath.SinF32(theta)
}

// QuantizeTheta quantizes an angle to the given number of steps.
// Used in encoder; provided here for completeness and testing.
//
// Parameters:
//   - theta: angle in radians [0, pi/2]
//   - qn: number of quantization steps
//
// Returns: quantized itheta [0, qn]
func QuantizeTheta(theta float32, qn int) int {
	if qn <= 0 {
		return 0
	}
	// Clamp theta to valid range
	if theta < 0 {
		theta = 0
	}
	if theta > float32(math.Pi/2) {
		theta = float32(math.Pi / 2)
	}

	// Convert to quantized value
	itheta := int(opusmath.RoundF32HalfAwayFromZeroToInt32(theta * float32(qn) / float32(math.Pi/2)))

	// Clamp to valid range
	if itheta < 0 {
		itheta = 0
	}
	if itheta > qn {
		itheta = qn
	}

	return itheta
}

// EstimateStereoAngle estimates the stereo angle from mid and side energies.
// Used for encoder decisions and analysis.
//
// Parameters:
//   - energyMid: energy of mid channel
//   - energySide: energy of side channel
//
// Returns: estimated theta in radians
func EstimateStereoAngle(energyMid, energySide float32) float32 {
	if energyMid <= 0 && energySide <= 0 {
		return 0
	}

	// theta = atan(sqrt(energySide / energyMid))
	// Handle edge cases
	if energyMid <= 0 {
		return float32(math.Pi / 2) // Full side
	}
	if energySide <= 0 {
		return 0 // Pure mono
	}

	ratio := opusmath.SqrtF32(energySide / energyMid)
	return opusmath.AtanF32(ratio)
}

// StereoWidth computes the perceived stereo width from mid and side.
// Returns a value in [0, 1] where 0 = mono, 1 = full stereo.
func StereoWidth(mid, side []float32) float32 {
	if len(mid) == 0 {
		return 0
	}

	var energyMid, energySide float32
	for i := range mid {
		energyMid += mid[i] * mid[i]
		if i < len(side) {
			energySide += side[i] * side[i]
		}
	}

	if energyMid+energySide <= 0 {
		return 0
	}

	// Width = ratio of side to total energy
	return opusmath.SqrtF32(energySide / (energyMid + energySide))
}

// LRToMidSide converts left-right stereo to mid-side.
// This is the inverse of MidSideToLR with theta=pi/4.
//
// Parameters:
//   - left, right: left and right channel coefficients
//
// Returns: mid and side coefficient arrays
func LRToMidSide(left, right []float32) (mid, side []float32) {
	n := len(left)
	if n == 0 {
		return nil, nil
	}

	if len(right) != n {
		right = make([]float32, n)
	}

	mid = make([]float32, n)
	side = make([]float32, n)

	for i := 0; i < n; i++ {
		// M = (L + R) / 2
		// S = (L - R) / 2
		mid[i] = (left[i] + right[i]) / 2
		side[i] = (left[i] - right[i]) / 2
	}

	return mid, side
}

// ApplyIntensityStereo applies intensity stereo to a band.
// This is a convenience function that decodes the inversion flag and applies it.
func ApplyIntensityStereo(mono []float32, inversionFlag int) (left, right []float32) {
	return IntensityStereo(mono, inversionFlag != 0)
}

// MixStereoToMono mixes stereo down to mono.
// Useful for decoder fallback or testing.
func MixStereoToMono(left, right []float32) []float32 {
	n := len(left)
	if len(right) < n {
		n = len(right)
	}
	if n == 0 {
		return nil
	}

	mono := make([]float32, n)
	for i := 0; i < n; i++ {
		mono[i] = (left[i] + right[i]) / 2
	}
	return mono
}

// DuplicateMonoToStereo creates stereo by duplicating mono to both channels.
func DuplicateMonoToStereo(mono []float32) (left, right []float32) {
	n := len(mono)
	left = make([]float32, n)
	right = make([]float32, n)
	copy(left, mono)
	copy(right, mono)
	return left, right
}
