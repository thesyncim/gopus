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

// TransientAnalysisResult holds the results of transient analysis.
// This provides both the transient decision and the tf_estimate metric.
type TransientAnalysisResult struct {
	IsTransient   bool    // Whether a transient was detected
	TfEstimate    float64 // Time-frequency estimate (0.0 = time, 1.0 = freq) for TF analysis bias
	TfChannel     int     // Which channel had the strongest transient (0 or 1)
	MaskMetric    float64 // The raw mask metric value (for debugging)
	WeakTransient bool    // Whether this is a "weak" transient (for hybrid mode)
}

// TransientAnalysis performs full transient analysis matching libopus.
// This computes:
//   - Whether the frame is transient (should use short blocks)
//   - tf_estimate: bias for TF resolution analysis (0 = time, 1 = freq)
//   - tf_chan: which channel has the strongest transient
//
// The algorithm uses a high-pass filter followed by forward/backward masking
// to detect temporal energy variations. The mask_metric measures how much
// the signal energy varies over time relative to a masked threshold.
//
// Parameters:
//   - pcm: input PCM samples (mono or interleaved stereo)
//   - frameSize: frame size in samples (120, 240, 480, or 960)
//   - allowWeakTransients: for hybrid mode at low bitrate
//
// Returns: TransientAnalysisResult with all metrics
//
// Reference: libopus celt/celt_encoder.c transient_analysis()
func (e *Encoder) TransientAnalysis(pcm []float64, frameSize int, allowWeakTransients bool) TransientAnalysisResult {
	result := TransientAnalysisResult{
		TfEstimate: 0.0,
		TfChannel:  0,
	}

	if len(pcm) == 0 || frameSize <= 0 {
		return result
	}

	channels := e.channels
	samplesPerChannel := len(pcm) / channels
	if samplesPerChannel < 16 {
		return result
	}

	// Forward masking decay: 6.7 dB/ms (default) or 3.3 dB/ms (weak transients)
	// At 48kHz, we process pairs of samples, so decay per pair:
	// Default: forward_decay = 0.0625 (1/16)
	// Weak: forward_decay = 0.03125 (1/32)
	forwardDecay := 0.0625
	if allowWeakTransients {
		forwardDecay = 0.03125
	}

	var maxMaskMetric float64
	tfChannel := 0

	// Inverse table for computing harmonic mean (6*64/x, trained on real data)
	// This matches libopus exactly
	invTable := [128]int{
		255, 255, 156, 110, 86, 70, 59, 51, 45, 40, 37, 33, 31, 28, 26, 25,
		23, 22, 21, 20, 19, 18, 17, 16, 16, 15, 15, 14, 13, 13, 12, 12,
		12, 12, 11, 11, 11, 10, 10, 10, 9, 9, 9, 9, 9, 9, 8, 8,
		8, 8, 8, 7, 7, 7, 7, 7, 7, 6, 6, 6, 6, 6, 6, 6,
		6, 6, 6, 6, 6, 6, 6, 6, 6, 5, 5, 5, 5, 5, 5, 5,
		5, 5, 5, 5, 5, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4,
		4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 3, 3,
		3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 2,
	}

	// Process each channel
	for c := 0; c < channels; c++ {
		// Extract channel samples
		channelSamples := make([]float64, samplesPerChannel)
		for i := 0; i < samplesPerChannel; i++ {
			channelSamples[i] = pcm[i*channels+c]
		}

		// High-pass filter: (1 - 2*z^-1 + z^-2) / (1 - z^-1 + 0.5*z^-2)
		// This removes DC and low frequencies to focus on transient energy
		tmp := make([]float64, samplesPerChannel)
		var mem0, mem1 float64
		for i := 0; i < samplesPerChannel; i++ {
			x := channelSamples[i]
			y := mem0 + x
			// Modified code to shorten dependency chains (matches libopus float)
			mem00 := mem0
			mem0 = mem0 - x + 0.5*mem1
			mem1 = x - mem00
			tmp[i] = y * 0.25 // SROUND16(y, 2) equivalent
		}

		// Clear first few samples (filter warm-up)
		for i := 0; i < 12 && i < len(tmp); i++ {
			tmp[i] = 0
		}

		len2 := samplesPerChannel / 2

		// Forward pass: compute post-echo threshold with forward masking
		// Group by two to reduce complexity
		energy := make([]float64, len2)
		var mean float64
		mem0 = 0
		for i := 0; i < len2; i++ {
			// Energy of pair of samples
			x2 := tmp[2*i]*tmp[2*i] + tmp[2*i+1]*tmp[2*i+1]
			x2 *= 0.0625 // /16 for scaling

			mean += x2 * 0.000244140625 // /4096 for averaging

			// Forward masking: exponential decay
			mem0 = x2 + (1.0-forwardDecay)*mem0
			energy[i] = forwardDecay * mem0
		}

		// Backward pass: compute pre-echo threshold
		// Backward masking: 13.9 dB/ms (decay = 0.125)
		var maxE float64
		mem0 = 0
		for i := len2 - 1; i >= 0; i-- {
			mem0 = energy[i] + 0.875*mem0
			energy[i] = 0.125 * mem0
			if 0.125*mem0 > maxE {
				maxE = 0.125 * mem0
			}
		}

		// Compute frame energy as geometric mean of mean and max
		// This is a compromise between old and new transient detectors
		mean = math.Sqrt(mean * maxE * 0.5 * float64(len2))

		// Inverse of mean energy (with epsilon to avoid division by zero)
		epsilon := 1e-15
		norm := float64(len2) * 64 / (mean*0.5 + epsilon)

		// Compute harmonic mean using inverse table
		// Skip unreliable boundaries, sample every 4th point
		var unmask int
		for i := 12; i < len2-5; i += 4 {
			// Map energy to table index
			id := int(math.Floor(64 * norm * (energy[i] + epsilon)))
			if id < 0 {
				id = 0
			}
			if id > 127 {
				id = 127
			}
			unmask += invTable[id]
		}

		// Normalize: compensate for 1/4 sampling and factor of 6 in inverse table
		numSamples := (len2 - 17) / 4
		if numSamples < 1 {
			numSamples = 1
		}
		maskMetric := float64(64*unmask*4) / float64(6*numSamples*4)

		if maskMetric > maxMaskMetric {
			tfChannel = c
			maxMaskMetric = maskMetric
		}
	}

	result.MaskMetric = maxMaskMetric
	result.TfChannel = tfChannel

	// Transient decision: mask_metric > 200
	result.IsTransient = maxMaskMetric > 200

	// Weak transient handling for hybrid mode
	if allowWeakTransients && result.IsTransient && maxMaskMetric < 600 {
		result.IsTransient = false
		result.WeakTransient = true
	}

	// Compute tf_estimate from mask_metric
	// This is the key formula from libopus:
	// tf_max = max(0, sqrt(27 * mask_metric) - 42)
	// tf_estimate = sqrt(max(0, 0.0069 * min(163, tf_max) - 0.139))
	//
	// In fixed-point terms from libopus:
	// tf_max = MAX16(0, celt_sqrt(27*mask_metric) - 42)
	// tf_estimate = celt_sqrt(MAX32(0, SHL32(MULT16_16(0.0069*2^14, MIN16(163, tf_max)), 14) - 0.139*2^28))
	// Which simplifies to (in Q14 for tf_estimate):
	// tf_estimate = sqrt(max(0, 0.0069 * min(163, tf_max) - 0.139))

	tfMax := math.Max(0, math.Sqrt(27*maxMaskMetric)-42)
	clampedTfMax := math.Min(163, tfMax)
	tfEstimateSquared := 0.0069*clampedTfMax - 0.139
	if tfEstimateSquared < 0 {
		tfEstimateSquared = 0
	}
	result.TfEstimate = math.Sqrt(tfEstimateSquared)

	// Clamp to [0, 1] range
	if result.TfEstimate > 1.0 {
		result.TfEstimate = 1.0
	}

	return result
}

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
