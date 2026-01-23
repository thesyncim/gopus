package celt

import "math"

// PVQ (Pyramid Vector Quantization) decoding for CELT band shapes.
// PVQ encodes normalized vectors where the L1 norm (sum of absolute values)
// equals a fixed number of pulses K. The decoder reads a CWRS index and
// converts it to a normalized L2 unit vector.
//
// Reference: RFC 6716 Section 4.3.4.1, libopus celt/bands.c quant_band()

// DecodePVQ decodes a PVQ codeword from the range decoder.
// n: band width (number of MDCT bins)
// k: number of pulses (from bit allocation)
// Returns: normalized float64 vector of length n with unit L2 norm.
//
// If k == 0, returns a zero vector (caller should fold from another band).
func (d *Decoder) DecodePVQ(n, k int) []float64 {
	if k == 0 || n <= 0 {
		// No pulses - return zero vector (will be folded)
		return make([]float64, n)
	}

	// Read CWRS index from range coder
	// Index has V(n,k) possible values
	vSize := PVQ_V(n, k)
	if vSize == 0 {
		return make([]float64, n)
	}

	index := d.rangeDecoder.DecodeUniform(vSize)

	// Convert index to pulse vector using CWRS
	pulses := DecodePulses(index, n, k)

	// Normalize to unit L2 energy
	return NormalizeVector(intToFloat(pulses))
}

// DecodePVQWithTrace decodes a PVQ codeword and traces the result.
// band: the band index (for tracing purposes)
// n: band width (number of MDCT bins)
// k: number of pulses (from bit allocation)
// Returns: normalized float64 vector of length n with unit L2 norm.
//
// This is identical to DecodePVQ but also calls DefaultTracer.TracePVQ.
func (d *Decoder) DecodePVQWithTrace(band, n, k int) []float64 {
	if k == 0 || n <= 0 {
		// No pulses - return zero vector (will be folded)
		return make([]float64, n)
	}

	// Read CWRS index from range coder
	// Index has V(n,k) possible values
	vSize := PVQ_V(n, k)
	if vSize == 0 {
		return make([]float64, n)
	}

	index := d.rangeDecoder.DecodeUniform(vSize)

	// Convert index to pulse vector using CWRS
	pulses := DecodePulses(index, n, k)

	// Trace PVQ decode
	DefaultTracer.TracePVQ(band, index, k, n, pulses)

	// Normalize to unit L2 energy
	return NormalizeVector(intToFloat(pulses))
}

// NormalizeVector scales vector to unit L2 norm.
// If the input vector has zero energy, returns the input unchanged.
func NormalizeVector(v []float64) []float64 {
	if len(v) == 0 {
		return v
	}

	var energy float64
	for _, x := range v {
		energy += x * x
	}

	if energy < 1e-15 {
		// Avoid division by zero - return input unchanged
		return v
	}

	scale := 1.0 / math.Sqrt(energy)
	result := make([]float64, len(v))
	for i, x := range v {
		result[i] = x * scale
	}
	return result
}

// intToFloat converts a slice of ints to float64.
func intToFloat(v []int) []float64 {
	if v == nil {
		return nil
	}
	result := make([]float64, len(v))
	for i, x := range v {
		result[i] = float64(x)
	}
	return result
}

// DecodeTheta decodes the stereo angle for mid-side mixing.
// n: number of points in the itheta quantization (depends on bit allocation)
// Returns theta in [0, pi/2] range for mid-side rotation.
//
// The angle theta controls the balance between mid and side channels:
// - theta = 0: mono (all energy in mid)
// - theta = pi/2: full stereo (equal mid and side)
//
// Reference: libopus celt/bands.c quant_band_stereo()
func (d *Decoder) DecodeTheta(n int) float64 {
	if n <= 1 {
		return 0
	}

	// Decode itheta as uniform value in [0, n]
	itheta := d.rangeDecoder.DecodeUniform(uint32(n + 1))

	// Convert to angle in [0, pi/2]
	// itheta=0 -> theta=0, itheta=n -> theta=pi/2
	theta := float64(itheta) * (math.Pi / 2) / float64(n)

	return theta
}

// DecodeStereoTheta decodes theta with sign for stereo balance.
// qn: number of quantization steps (determines precision)
// Returns: itheta value (0 = pure mid, qn = pure side)
//
// Reference: libopus celt/bands.c compute_theta()
func (d *Decoder) DecodeStereoTheta(qn int) int {
	if qn <= 0 {
		return 0
	}

	// Decode as uniform value
	itheta := int(d.rangeDecoder.DecodeUniform(uint32(qn + 1)))

	return itheta
}

// ThetaToGains converts itheta to mid and side gains.
// itheta: quantized angle (0 to qn)
// qn: number of quantization steps
// Returns: mid gain, side gain (both in [0, 1])
//
// Reference: libopus celt/bands.c
func ThetaToGains(itheta, qn int) (mid, side float64) {
	if qn <= 0 {
		return 1.0, 0.0
	}

	// theta in [0, pi/2]
	theta := float64(itheta) * (math.Pi / 2) / float64(qn)

	mid = math.Cos(theta)
	side = math.Sin(theta)

	return mid, side
}

// ApplyMidSideRotation rotates mid-side vectors to left-right.
// mid: mid channel coefficients
// side: side channel coefficients
// midGain, sideGain: rotation gains from theta
// Returns: left and right channel coefficients
func ApplyMidSideRotation(mid, side []float64, midGain, sideGain float64) (left, right []float64) {
	n := len(mid)
	if len(side) != n {
		// Mismatch - return mid to both
		return mid, mid
	}

	left = make([]float64, n)
	right = make([]float64, n)

	for i := 0; i < n; i++ {
		// Left = mid*cos(theta) + side*sin(theta)
		// Right = mid*cos(theta) - side*sin(theta)
		left[i] = midGain*mid[i] + sideGain*side[i]
		right[i] = midGain*mid[i] - sideGain*side[i]
	}

	return left, right
}

// DecodeIntensityStereo decodes intensity stereo for a band.
// mid: the mid channel coefficients
// Returns: left and right with optional sign inversion on right.
//
// In intensity stereo, both channels share the same shape but may have
// opposite signs (determined by a single bit).
//
// Reference: RFC 6716 Section 4.3.4.3
func (d *Decoder) DecodeIntensityStereo(mid []float64) (left, right []float64) {
	n := len(mid)
	left = make([]float64, n)
	right = make([]float64, n)

	// Copy mid to both channels
	copy(left, mid)
	copy(right, mid)

	// Decode inversion flag (1 bit)
	inv := d.rangeDecoder.DecodeBit(1) == 1

	if inv {
		// Invert right channel
		for i := range right {
			right[i] = -right[i]
		}
	}

	return left, right
}
