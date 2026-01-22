// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides energy encoding functions that mirror the decoder.

package celt

import (
	"math"

	"gopus/internal/rangecoding"
)

// ComputeBandEnergies computes energy for each frequency band from MDCT coefficients.
// Returns energies in log2 scale (same as decoder expects).
// energies[c*nbBands + band] = log2(RMS energy of band for channel c)
//
// The energy computation extracts loudness per frequency band:
// 1. For each band, sum squares of MDCT coefficients
// 2. Divide by band width to get average power
// 3. Convert to log2 scale: energy = 0.5 * log2(sumSq / width)
//
// This mirrors the decoder's denormalization which scales bands by 2^energy.
//
// Reference: RFC 6716 Section 4.3.2, libopus celt/bands.c
func (e *Encoder) ComputeBandEnergies(mdctCoeffs []float64, nbBands, frameSize int) []float64 {
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands < 0 {
		nbBands = 0
	}

	// Determine number of channels from coefficient length
	channels := e.channels
	coeffsPerChannel := frameSize
	if len(mdctCoeffs) < coeffsPerChannel*channels {
		// Handle mono or incomplete data
		if len(mdctCoeffs) < coeffsPerChannel {
			channels = 1
			coeffsPerChannel = len(mdctCoeffs)
		} else {
			channels = 1
		}
	}

	energies := make([]float64, nbBands*channels)

	for c := 0; c < channels; c++ {
		// Get coefficients for this channel
		channelStart := c * coeffsPerChannel
		channelEnd := channelStart + coeffsPerChannel
		if channelEnd > len(mdctCoeffs) {
			channelEnd = len(mdctCoeffs)
		}

		channelCoeffs := mdctCoeffs[channelStart:channelEnd]

		for band := 0; band < nbBands; band++ {
			// Get band boundaries scaled for frame size
			start := ScaledBandStart(band, frameSize)
			end := ScaledBandEnd(band, frameSize)

			// Clamp to available coefficients
			if start >= len(channelCoeffs) {
				energies[c*nbBands+band] = -28.0 // Minimum energy (D03-01-01)
				continue
			}
			if end > len(channelCoeffs) {
				end = len(channelCoeffs)
			}
			if end <= start {
				energies[c*nbBands+band] = -28.0
				continue
			}

			// Compute RMS energy in log2 scale
			energy := computeBandRMS(channelCoeffs, start, end)
			energies[c*nbBands+band] = energy
		}
	}

	return energies
}

// computeBandRMS computes the log2-scale energy of coefficients in [start, end).
// Returns energy = 0.5 * log2(sumSq / width) = log2(RMS)
// For zero input, returns -28.0 (minimum energy per D03-01-01).
func computeBandRMS(coeffs []float64, start, end int) float64 {
	if end <= start || start < 0 || end > len(coeffs) {
		return -28.0
	}

	// Compute sum of squares
	sumSq := 0.0
	for i := start; i < end; i++ {
		sumSq += coeffs[i] * coeffs[i]
	}

	// Handle zero energy
	if sumSq < 1e-30 {
		return -28.0 // Minimum energy (D03-01-01)
	}

	// Compute band width
	width := float64(end - start)

	// Energy in log2 scale: energy = log2(sqrt(sumSq/width)) = 0.5 * log2(sumSq/width)
	// Using change of base: log2(x) = ln(x) / ln(2)
	energy := 0.5 * math.Log2(sumSq/width)

	// Clamp to valid range
	if energy < -28.0 {
		energy = -28.0
	}
	if energy > 16.0 {
		energy = 16.0 // Reasonable upper limit
	}

	return energy
}
