// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file contains band encoding: normalization and PVQ quantization.

package celt

import (
	"math"
)

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

		// Compute gain = 2^energy (energy is in log2 scale)
		// gain = exp(energy * ln(2))
		gain := math.Exp(energies[band] * 0.6931471805599453) // ln(2)

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
			// Normalize to unit L2 norm using existing NormalizeVector
			shape = NormalizeVector(shape)
		}

		shapes[band] = shape
		offset += n
	}

	return shapes
}
