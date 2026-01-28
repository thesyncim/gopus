// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file contains band encoding: normalization and PVQ quantization.

package celt

import "math"

// NormalizeBands divides each band's MDCT coefficients by its energy,
// producing unit-norm shapes ready for PVQ quantization.
// Returns shapes[band] = normalized coefficients for that band.
//
// The decoder does: output = shape * gain (denormalization)
// So encoder does: shape = input / gain (normalization)
//
// Parameters:
//   - mdctCoeffs: MDCT coefficients for all bands concatenated
//   - energies: per-band energy values (log2 scale from coarse + fine energy)
//   - nbBands: number of bands to process
//   - frameSize: frame size in samples (120, 240, 480, 960)
//
// Returns: shapes[band] = normalized float64 vector with unit L2 norm
//
// Reference: RFC 6716 Section 4.3.4.1
func (e *Encoder) NormalizeBands(mdctCoeffs []float64, energies []float64, nbBands, frameSize int) [][]float64 {
	if nbBands <= 0 || nbBands > MaxBands {
		return nil
	}
	if len(energies) < nbBands {
		return nil
	}

	shapes := make([][]float64, nbBands)
	offset := 0

	for band := 0; band < nbBands; band++ {
		// Get band boundaries
		n := ScaledBandWidth(band, frameSize)
		if n <= 0 {
			shapes[band] = []float64{}
			continue
		}

		// Extract coefficients for this band
		if offset+n > len(mdctCoeffs) {
			// Not enough coefficients - use zeros
			shapes[band] = make([]float64, n)
			for i := range shapes[band] {
				shapes[band][i] = 0
			}
			offset += n
			continue
		}

		// Compute gain from energy (log2 units, 1 = 6 dB)
		// The decoder uses: e = decoded_energy + eMeans*DB6; gain = 2^(e/DB6)
		// The encoder must use the SAME formula for normalization, using the
		// quantized energy that was encoded to the bitstream.
		// This ensures: normalized * decoder_gain = original (up to quantization)
		e_val := energies[band]
		if band < len(eMeans) {
			e_val += eMeans[band] * DB6
		}
		if e_val > 32*DB6 {
			e_val = 32 * DB6 // Match decoder's clamp (bands.go:102-103)
		}
		gain := math.Exp2(e_val / DB6)

		// Allocate shape vector
		shape := make([]float64, n)

		// Handle degenerate case: gain near zero
		if gain < 1e-15 {
			// Set shape to first-unit-vector [1, 0, 0, ...]
			shape[0] = 1.0
			for i := 1; i < n; i++ {
				shape[i] = 0.0
			}
			shapes[band] = shape
			offset += n
			continue
		}

		// Divide coefficients by gain
		allZero := true
		for i := 0; i < n; i++ {
			shape[i] = mdctCoeffs[offset+i] / gain
			if math.Abs(shape[i]) > 1e-15 {
				allZero = false
			}
		}

		// Handle case where all coefficients are zero
		if allZero {
			// Set shape to first-unit-vector [1, 0, 0, ...]
			shape[0] = 1.0
			for i := 1; i < n; i++ {
				shape[i] = 0.0
			}
		} else {
			// Normalize to unit L2 norm for PVQ encoding
			// PVQ expects unit-norm input vectors. The decoder will reconstruct
			// the shape (also unit-norm) and then scale by gain to get the
			// original magnitude back.
			var norm float64
			for i := 0; i < n; i++ {
				norm += shape[i] * shape[i]
			}
			if norm > 1e-30 {
				norm = math.Sqrt(norm)
				for i := 0; i < n; i++ {
					shape[i] /= norm
				}
			}
		}

		shapes[band] = shape
		offset += n
	}

	return shapes
}

// NormalizeBandsToArray normalizes bands into a single contiguous array (length = frameSize).
// This mirrors libopus normalise_bands(): divide by the per-band gain without re-normalizing.
// The input energies should be the original (unquantized) band energies.
func (e *Encoder) NormalizeBandsToArray(mdctCoeffs []float64, energies []float64, nbBands, frameSize int) []float64 {
	if nbBands <= 0 || nbBands > MaxBands {
		return nil
	}
	if len(energies) < nbBands {
		return nil
	}
	if frameSize <= 0 {
		return nil
	}

	norm := make([]float64, frameSize)
	offset := 0
	for band := 0; band < nbBands; band++ {
		n := ScaledBandWidth(band, frameSize)
		if n <= 0 {
			continue
		}
		if offset+n > len(mdctCoeffs) {
			offset += n
			continue
		}

		eVal := energies[band]
		if band < len(eMeans) {
			eVal += eMeans[band] * DB6
		}
		if eVal > 32*DB6 {
			eVal = 32 * DB6
		}
		gain := math.Exp2(eVal / DB6)

		if gain < 1e-15 {
			norm[offset] = 1.0
			for i := 1; i < n; i++ {
				norm[offset+i] = 0
			}
			offset += n
			continue
		}

		for i := 0; i < n; i++ {
			norm[offset+i] = mdctCoeffs[offset+i] / gain
		}
		offset += n
	}

	return norm
}

// vectorToPulses converts a normalized float vector to an integer pulse vector.
// The result has L1 norm (sum of absolute values) equal to k.
// This is the encoder's inverse of decoder's pulse-to-vector reconstruction.
//
// Parameters:
//   - shape: normalized float vector (should have unit L2 norm)
//   - k: target L1 norm (number of pulses)
//
// Returns: integer pulse vector where sum(|pulses[i]|) == k
//
// Algorithm:
// 1. Compute L1 norm of shape
// 2. Scale shape so L1 norm = k
// 3. Round each component to nearest integer
// 4. Distribute remaining pulses to minimize distortion
//
// Reference: libopus celt/vq.c alg_quant()
func vectorToPulses(shape []float64, k int) []int {
	n := len(shape)
	if n == 0 || k <= 0 {
		return make([]int, n)
	}

	pulses := make([]int, n)

	// Compute L1 norm of shape
	var l1norm float64
	for _, x := range shape {
		l1norm += math.Abs(x)
	}

	// Handle degenerate case
	if l1norm < 1e-15 {
		// Put all pulses in first position
		pulses[0] = k
		return pulses
	}

	// Scale factor to make L1 norm = k
	scale := float64(k) / l1norm

	// Scaled values and track rounding errors
	type errorEntry struct {
		idx   int
		error float64 // Error from rounding (positive = rounded down too much)
		sign  int     // Sign of the original value
	}
	errors := make([]errorEntry, n)

	currentL1 := 0
	for i, x := range shape {
		scaled := x * scale
		sign := 1
		if scaled < 0 {
			sign = -1
			scaled = -scaled
		}

		// Round to nearest integer
		rounded := int(math.Floor(scaled + 0.5))
		if rounded < 0 {
			rounded = 0
		}

		pulses[i] = sign * rounded
		currentL1 += rounded

		// Track error: how much we lost by rounding
		// Positive error = we rounded down (want to add pulse)
		// Negative error = we rounded up (want to remove pulse)
		error := scaled - float64(rounded)
		errors[i] = errorEntry{idx: i, error: error, sign: sign}
	}

	// Distribute remaining pulses to minimize distortion
	remaining := k - currentL1

	// While we need to add pulses
	for remaining > 0 {
		// Find position with largest positive error (rounded down the most)
		bestIdx := -1
		bestError := -1.0
		for i, e := range errors {
			if e.error > bestError {
				bestError = e.error
				bestIdx = i
			}
		}

		if bestIdx < 0 {
			// No good candidate, just add to first position
			bestIdx = 0
		}

		// Add pulse with correct sign
		if pulses[bestIdx] >= 0 {
			pulses[bestIdx]++
		} else {
			pulses[bestIdx]--
		}
		errors[bestIdx].error -= 1.0
		remaining--
	}

	// While we need to remove pulses
	for remaining < 0 {
		// Find position with most negative error (rounded up the most)
		bestIdx := -1
		bestError := 1.0
		for i, e := range errors {
			absPulse := pulses[i]
			if absPulse < 0 {
				absPulse = -absPulse
			}
			if absPulse > 0 && e.error < bestError {
				bestError = e.error
				bestIdx = i
			}
		}

		if bestIdx < 0 {
			// Find any position with pulses
			for i := 0; i < n; i++ {
				if pulses[i] != 0 {
					bestIdx = i
					break
				}
			}
		}

		if bestIdx < 0 {
			break // No pulses to remove
		}

		// Remove pulse with correct sign
		if pulses[bestIdx] > 0 {
			pulses[bestIdx]--
		} else if pulses[bestIdx] < 0 {
			pulses[bestIdx]++
		}
		errors[bestIdx].error += 1.0
		remaining++
	}

	return pulses
}

// bitsToKEncode converts allocated bits to pulse count for encoding.
// This mirrors the decoder's bitsToK function.
//
// Parameters:
//   - bits: number of bits allocated to this band
//   - n: band width (number of MDCT bins)
//
// Returns: number of pulses K for PVQ coding.
func bitsToKEncode(bits, n int) int {
	// Use the same algorithm as decoder's bitsToK
	return bitsToK(bits, n)
}

// EncodeBandPVQ encodes a normalized band shape using PVQ.
// k is the number of pulses (determined by bit allocation via bitsToKEncode).
//
// Parameters:
//   - shape: normalized band shape (unit L2 norm)
//   - n: band width (number of MDCT bins)
//   - k: number of pulses
//
// The encoded data consists of a single PVQ index encoded uniformly
// with V(n,k) possible values.
//
// Reference: libopus celt/bands.c quant_band()
func (e *Encoder) EncodeBandPVQ(shape []float64, n, k int) {
	if e.rangeEncoder == nil || k <= 0 || n <= 0 {
		return
	}

	// Ensure shape has correct length
	if len(shape) != n {
		// Pad or truncate
		newShape := make([]float64, n)
		copy(newShape, shape)
		shape = newShape
	}

	// Convert shape to pulses
	pulses := vectorToPulses(shape, k)

	// Encode to CWRS index using existing EncodePulses function
	index := EncodePulses(pulses, n, k)

	// Get the number of possible codewords
	vSize := PVQ_V(n, k)
	if vSize == 0 {
		return
	}

	// Encode index uniformly
	e.rangeEncoder.EncodeUniform(index, vSize)
}

// EncodeBands encodes all bands using PVQ.
// shapesL, shapesR: normalized band shapes for Left/Right (R is nil for mono)
// bandBits: bit allocation per band from ComputeAllocation
// nbBands: number of bands
// frameSize: frame size in samples (120, 240, 480, 960)
//
// For each band:
// - If bits <= 0: skip (band will be folded by decoder)
// - Otherwise: compute k from bits and encode via EncodeBandPVQ
// - For stereo, bits are split between L and R (Dual Stereo)
//
// Reference: libopus celt/bands.c quant_all_bands()
func (e *Encoder) EncodeBands(shapesL, shapesR [][]float64, bandBits []int, nbBands, frameSize int) {
	if e.rangeEncoder == nil {
		return
	}
	if nbBands <= 0 || nbBands > MaxBands {
		return
	}
	if len(shapesL) < nbBands || len(bandBits) < nbBands {
		return
	}

	stereo := shapesR != nil && len(shapesR) >= nbBands

	for band := 0; band < nbBands; band++ {
		bits := bandBits[band]

		// If no bits allocated, skip this band (decoder will fold from other bands)
		if bits <= 0 {
			continue
		}

		// Get band width
		n := ScaledBandWidth(band, frameSize)
		if n <= 0 {
			continue
		}

		if stereo {
			// Dual stereo: split bits
			// Note: This allocation must match decoder's dual stereo split
			bitsL := bits / 2
			bitsR := bits - bitsL

			// Encode Left
			kL := bitsToKEncode(bitsL, n)
			if kL > 0 && len(shapesL[band]) > 0 {
				e.EncodeBandPVQ(shapesL[band], n, kL)
			}

			// Encode Right
			kR := bitsToKEncode(bitsR, n)
			if kR > 0 && len(shapesR[band]) > 0 {
				e.EncodeBandPVQ(shapesR[band], n, kR)
			}
		} else {
			// Mono
			k := bitsToKEncode(bits, n)
			if k <= 0 {
				continue
			}

			// Get shape for this band
			shape := shapesL[band]
			if len(shape) == 0 {
				continue
			}

			// Encode the band using PVQ
			e.EncodeBandPVQ(shape, n, k)
		}
	}
}

// EncodeBandsHybrid encodes bands for hybrid mode (starting from startBand).
// In hybrid mode, bands 0 to startBand-1 are handled by SILK.
// Only bands from startBand onwards are PVQ encoded.
//
// Reference: RFC 6716 Section 3.2 - Hybrid mode uses start_band=17 for CELT
func (e *Encoder) EncodeBandsHybrid(shapesL, shapesR [][]float64, bandBits []int, nbBands, frameSize, startBand int) {
	if e.rangeEncoder == nil {
		return
	}
	if nbBands <= 0 || nbBands > MaxBands {
		return
	}
	if len(shapesL) < nbBands || len(bandBits) < nbBands {
		return
	}

	stereo := shapesR != nil && len(shapesR) >= nbBands

	// Only encode bands from startBand onwards
	for band := startBand; band < nbBands; band++ {
		bits := bandBits[band]

		// If no bits allocated, skip this band (decoder will fold from other bands)
		if bits <= 0 {
			continue
		}

		// Get band width
		n := ScaledBandWidth(band, frameSize)
		if n <= 0 {
			continue
		}

		if stereo {
			// Dual stereo: split bits
			bitsL := bits / 2
			bitsR := bits - bitsL

			// Encode Left
			kL := bitsToKEncode(bitsL, n)
			if kL > 0 && len(shapesL[band]) > 0 {
				e.EncodeBandPVQ(shapesL[band], n, kL)
			}

			// Encode Right
			kR := bitsToKEncode(bitsR, n)
			if kR > 0 && len(shapesR[band]) > 0 {
				e.EncodeBandPVQ(shapesR[band], n, kR)
			}
		} else {
			// Mono
			k := bitsToKEncode(bits, n)
			if k <= 0 {
				continue
			}

			// Get shape for this band
			shape := shapesL[band]
			if len(shape) == 0 {
				continue
			}

			// Encode the band using PVQ
			e.EncodeBandPVQ(shape, n, k)
		}
	}
}
