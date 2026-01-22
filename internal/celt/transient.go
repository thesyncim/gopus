// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides transient detection for short block decisions.

package celt

import "math"

// TransientThreshold is the energy ratio threshold for transient detection.
// A ratio > 4.0 (6dB difference) between adjacent sub-blocks triggers short blocks.
// Reference: libopus celt/celt_encoder.c transient_analysis()
const TransientThreshold = 4.0

// TransientMinEnergy is the minimum energy to consider for transient detection.
// Very quiet frames are not considered transient.
const TransientMinEnergy = 1e-10

// DetectTransient analyzes PCM for sudden energy changes.
// Returns true if the frame should use short MDCT blocks.
//
// Transient detection identifies frames with:
// - Sharp attacks (drum hits, plucks)
// - Sudden silence
// - Energy jumps > 6dB between adjacent sub-blocks
//
// When transient is detected, the encoder uses multiple short MDCTs instead
// of one long MDCT for better time resolution at the cost of frequency resolution.
//
// Parameters:
//   - pcm: input PCM samples (mono or interleaved stereo)
//   - frameSize: frame size in samples (120, 240, 480, or 960)
//
// Returns: true if transient detected and short blocks should be used
//
// Reference: RFC 6716 Section 4.3.1, libopus celt/celt_encoder.c
func (e *Encoder) DetectTransient(pcm []float64, frameSize int) bool {
	if len(pcm) == 0 || frameSize <= 0 {
		return false
	}

	// Number of sub-blocks for analysis
	// Use 8 sub-blocks to match libopus transient detection
	numSubBlocks := 8

	// Handle different channel configurations
	channels := e.channels
	samplesPerChannel := len(pcm) / channels
	if samplesPerChannel < numSubBlocks {
		return false
	}

	subBlockSize := samplesPerChannel / numSubBlocks
	if subBlockSize <= 0 {
		return false
	}

	// Compute energy for each sub-block (sum across channels)
	energies := make([]float64, numSubBlocks)

	for b := 0; b < numSubBlocks; b++ {
		start := b * subBlockSize
		end := start + subBlockSize

		var sumSq float64
		for c := 0; c < channels; c++ {
			for i := start; i < end && i < samplesPerChannel; i++ {
				idx := i*channels + c
				if idx < len(pcm) {
					sumSq += pcm[idx] * pcm[idx]
				}
			}
		}
		energies[b] = sumSq
	}

	// Find maximum energy ratio between adjacent blocks
	maxRatio := 0.0
	for b := 1; b < numSubBlocks; b++ {
		var ratio float64

		// Compute ratio based on which block has more energy
		if energies[b-1] > TransientMinEnergy && energies[b] > TransientMinEnergy {
			if energies[b] > energies[b-1] {
				ratio = energies[b] / energies[b-1]
			} else {
				ratio = energies[b-1] / energies[b]
			}
		} else if energies[b] > TransientMinEnergy {
			// Previous block very quiet, current has energy -> attack
			ratio = TransientThreshold + 1
		} else if energies[b-1] > TransientMinEnergy {
			// Current block very quiet, previous had energy -> decay
			ratio = TransientThreshold + 1
		}

		if ratio > maxRatio {
			maxRatio = ratio
		}
	}

	// Return true if any adjacent block ratio exceeds threshold
	return maxRatio > TransientThreshold
}

// DetectTransientWithCustomThreshold detects transients with a custom threshold.
// This allows tuning for different audio content types.
func (e *Encoder) DetectTransientWithCustomThreshold(pcm []float64, frameSize int, threshold float64) bool {
	if len(pcm) == 0 || frameSize <= 0 {
		return false
	}

	channels := e.channels
	samplesPerChannel := len(pcm) / channels
	numSubBlocks := 8

	if samplesPerChannel < numSubBlocks {
		return false
	}

	subBlockSize := samplesPerChannel / numSubBlocks
	if subBlockSize <= 0 {
		return false
	}

	energies := make([]float64, numSubBlocks)

	for b := 0; b < numSubBlocks; b++ {
		start := b * subBlockSize
		end := start + subBlockSize

		var sumSq float64
		for c := 0; c < channels; c++ {
			for i := start; i < end && i < samplesPerChannel; i++ {
				idx := i*channels + c
				if idx < len(pcm) {
					sumSq += pcm[idx] * pcm[idx]
				}
			}
		}
		energies[b] = sumSq
	}

	maxRatio := 0.0
	for b := 1; b < numSubBlocks; b++ {
		var ratio float64

		if energies[b-1] > TransientMinEnergy && energies[b] > TransientMinEnergy {
			if energies[b] > energies[b-1] {
				ratio = energies[b] / energies[b-1]
			} else {
				ratio = energies[b-1] / energies[b]
			}
		} else if energies[b] > TransientMinEnergy || energies[b-1] > TransientMinEnergy {
			ratio = threshold + 1
		}

		if ratio > maxRatio {
			maxRatio = ratio
		}
	}

	return maxRatio > threshold
}

// ComputeSubBlockEnergies computes energy per sub-block for analysis.
// Returns energy values for each of the 8 sub-blocks.
// Useful for debugging or adaptive thresholding.
func (e *Encoder) ComputeSubBlockEnergies(pcm []float64, frameSize int) []float64 {
	if len(pcm) == 0 || frameSize <= 0 {
		return nil
	}

	channels := e.channels
	samplesPerChannel := len(pcm) / channels
	numSubBlocks := 8

	if samplesPerChannel < numSubBlocks {
		return nil
	}

	subBlockSize := samplesPerChannel / numSubBlocks
	if subBlockSize <= 0 {
		return nil
	}

	energies := make([]float64, numSubBlocks)

	for b := 0; b < numSubBlocks; b++ {
		start := b * subBlockSize
		end := start + subBlockSize

		var sumSq float64
		for c := 0; c < channels; c++ {
			for i := start; i < end && i < samplesPerChannel; i++ {
				idx := i*channels + c
				if idx < len(pcm) {
					sumSq += pcm[idx] * pcm[idx]
				}
			}
		}
		// Convert to dB-like scale for easier analysis
		if sumSq > 0 {
			energies[b] = 10 * math.Log10(sumSq)
		} else {
			energies[b] = -100 // Minimum energy in dB
		}
	}

	return energies
}

// GetShortBlockCount returns the number of short blocks for a given frame size.
// This is the ShortBlocks value from ModeConfig when transient is detected.
func GetShortBlockCount(frameSize int) int {
	mode := GetModeConfig(frameSize)
	return mode.ShortBlocks
}
