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
	ToneFreq      float64 // Detected tone frequency in radians/sample (-1 if no tone)
	Toneishness   float64 // How "pure" the tone is (0.0-1.0, higher = purer)
}

// toneLPC computes 2nd-order LPC coefficients using forward+backward least-squares fit.
// This is used to detect pure tones by analyzing the resonant characteristics.
// Returns (lpc0, lpc1, success) where success=false if the computation fails.
// Reference: libopus celt/celt_encoder.c tone_lpc()
func toneLPC(x []float64, delay int) (float64, float64, bool) {
	len := len(x)
	if len <= 2*delay {
		return 0, 0, false
	}

	// Compute correlations using forward prediction covariance method
	var r00, r01, r02 float64
	for i := 0; i < len-2*delay; i++ {
		r00 += x[i] * x[i]
		r01 += x[i] * x[i+delay]
		r02 += x[i] * x[i+2*delay]
	}

	// Compute edge corrections for r11, r22, r12
	var edges float64
	for i := 0; i < delay; i++ {
		edges += x[len+i-2*delay]*x[len+i-2*delay] - x[i]*x[i]
	}
	r11 := r00 + edges

	edges = 0
	for i := 0; i < delay; i++ {
		edges += x[len+i-delay]*x[len+i-delay] - x[i+delay]*x[i+delay]
	}
	r22 := r11 + edges

	edges = 0
	for i := 0; i < delay; i++ {
		edges += x[len+i-2*delay]*x[len+i-delay] - x[i]*x[i+delay]
	}
	r12 := r01 + edges

	// Combine forward and backward for symmetric solution
	R00 := r00 + r22
	R01 := r01 + r12
	R11 := 2 * r11
	R02 := 2 * r02
	R12 := r12 + r01

	// Solve A*x=b where A=[R00, R01; R01, R11] and b=[R02; R12]
	den := R00*R11 - R01*R01

	// Check for near-singular matrix (as in libopus: den < 0.001*R00*R11)
	if den < 0.001*R00*R11 {
		return 0, 0, false
	}

	num1 := R02*R11 - R01*R12
	num0 := R00*R12 - R02*R01

	lpc1 := num1 / den
	lpc0 := num0 / den

	// Clamp to valid range
	if lpc1 > 1.0 {
		lpc1 = 1.0
	} else if lpc1 < -1.0 {
		lpc1 = -1.0
	}
	if lpc0 > 2.0 {
		lpc0 = 2.0
	} else if lpc0 < -2.0 {
		lpc0 = -2.0
	}

	return lpc0, lpc1, true
}

// toneDetect detects pure or nearly pure tones in the input signal.
// Returns (toneFreq, toneishness) where:
//   - toneFreq: angular frequency in radians/sample (-1 if no tone detected)
//   - toneishness: how pure the tone is (0.0-1.0, higher = purer)
//
// Reference: libopus celt/celt_encoder.c tone_detect()
func toneDetect(in []float64, channels int, sampleRate int) (float64, float64) {
	n := len(in) / channels
	if n < 4 {
		return -1, 0
	}

	// Mix down to mono if stereo (matching libopus behavior)
	x := make([]float64, n)
	if channels == 2 {
		for i := 0; i < n; i++ {
			x[i] = 0.5 * (in[i*2] + in[i*2+1])
		}
	} else {
		copy(x, in[:n])
	}

	delay := 1
	lpc0, lpc1, success := toneLPC(x, delay)

	// If LPC resonates too close to DC, retry with downsampling
	// (delay <= sampleRate/3000 corresponds to frequencies > ~1500 Hz)
	maxDelay := sampleRate / 3000
	if maxDelay < 1 {
		maxDelay = 1
	}
	for delay <= maxDelay && (!success || (lpc0 > 1.0 && lpc1 < 0)) {
		delay *= 2
		if 2*delay >= n {
			break
		}
		lpc0, lpc1, success = toneLPC(x, delay)
	}

	// Check that our filter has complex roots: lpc0^2 + 4*lpc1 < 0
	// This indicates a resonant (tonal) system
	if success && lpc0*lpc0+4*lpc1 < 0 {
		// Toneishness is the squared radius of the poles
		toneishness := -lpc1
		// Frequency from the angle of the complex pole
		freq := math.Acos(0.5*lpc0) / float64(delay)
		return freq, toneishness
	}

	return -1, 0
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
		TfEstimate:  0.0,
		TfChannel:   0,
		ToneFreq:    -1,
		Toneishness: 0,
	}

	if len(pcm) == 0 || frameSize <= 0 {
		return result
	}

	channels := e.channels
	samplesPerChannel := len(pcm) / channels
	if samplesPerChannel < 16 {
		return result
	}

	// Detect pure tones before transient analysis
	// This is used to prevent false transient detection on low-frequency tones
	// Reference: libopus celt/celt_encoder.c tone_detect() called before transient_analysis()
	toneFreq, toneishness := toneDetect(pcm, channels, 48000) // Assume 48kHz
	result.ToneFreq = toneFreq
	result.Toneishness = toneishness

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
			// In float builds, SROUND16 is a no-op (see libopus arch.h),
			// so we should not scale the high-pass output.
			tmp[i] = y
		}

		// Clear first few samples (filter warm-up)
		for i := 0; i < 12 && i < len(tmp); i++ {
			tmp[i] = 0
		}

		len2 := samplesPerChannel / 2

		// Forward pass: compute post-echo threshold with forward masking
		// Group by two to reduce complexity
		// Note: In libopus FLOAT mode, PSHR32 is a no-op, so no scaling is applied
		energy := make([]float64, len2)
		var mean float64
		mem0 = 0
		for i := 0; i < len2; i++ {
			// Energy of pair of samples
			// libopus FLOAT: x2 = tmp[2*i]² + tmp[2*i+1]² (no PSHR32 scaling)
			x2 := tmp[2*i]*tmp[2*i] + tmp[2*i+1]*tmp[2*i+1]

			// libopus FLOAT: mean += x2 (no PSHR32 scaling)
			mean += x2

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
		// libopus FLOAT path uses:
		//   norm = SHL32(len2, 6+14) / (EPSILON + SHR32(mean,1))
		// but SHL32/SHR32 are no-ops in float builds, so:
		//   norm = len2 / (EPSILON + mean/2)
		// Using the fixed-point scaling here would over-normalize and
		// suppress the mask metric.
		epsilon := 1e-15
		norm := float64(len2) / (mean*0.5 + epsilon)

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

	// Prevent the transient detector from confusing the partial cycle of a
	// very low frequency tone with a transient.
	// Reference: libopus celt/celt_encoder.c lines 445-451
	// toneishness > 0.98 AND tone_freq < 0.026 radians/sample (~198 Hz at 48kHz)
	// Note: This check ONLY applies to very low frequency tones. Higher frequency
	// pure tones (e.g., 440 Hz) can legitimately trigger transient detection,
	// especially on the first frame where pre-emphasis buffer is empty.
	if result.Toneishness > 0.98 && result.ToneFreq >= 0 && result.ToneFreq < 0.026 {
		result.IsTransient = false
		result.MaskMetric = 0
	}

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

// patchTransientDecision implements libopus's secondary transient check based on band energies.
// This is a "last chance" detector that can flip a non-transient decision to transient
// when spectral changes indicate a sudden attack.
// Reference: libopus celt_encoder.c patch_transient_decision()
func patchTransientDecision(newE, oldE []float64, nbBands, start, end, channels int) bool {
	if nbBands <= 0 || channels <= 0 {
		return false
	}
	if start < 0 {
		start = 0
	}
	if end > nbBands {
		end = nbBands
	}
	if end-start < 3 {
		return false
	}

	// Determine old energy stride (gopus stores prev energies in MaxBands stride).
	oldStride := nbBands
	if len(oldE) >= MaxBands*channels {
		oldStride = MaxBands
	}

	getOld := func(c, band int) float64 {
		idx := c*oldStride + band
		if idx >= 0 && idx < len(oldE) {
			return oldE[idx]
		}
		return 0
	}
	getNew := func(c, band int) float64 {
		idx := c*nbBands + band
		if idx >= 0 && idx < len(newE) {
			return newE[idx]
		}
		return 0
	}

	// Spread old energies with a -1 dB/Bark slope (aggressive).
	spreadOld := make([]float64, end)
	if channels == 1 {
		spreadOld[start] = getOld(0, start)
		for i := start + 1; i < end; i++ {
			prev := spreadOld[i-1] - 1.0
			val := getOld(0, i)
			if prev > val {
				spreadOld[i] = prev
			} else {
				spreadOld[i] = val
			}
		}
	} else {
		base := getOld(0, start)
		alt := getOld(1, start)
		if alt > base {
			base = alt
		}
		spreadOld[start] = base
		for i := start + 1; i < end; i++ {
			val := getOld(0, i)
			alt = getOld(1, i)
			if alt > val {
				val = alt
			}
			prev := spreadOld[i-1] - 1.0
			if prev > val {
				spreadOld[i] = prev
			} else {
				spreadOld[i] = val
			}
		}
	}

	for i := end - 2; i >= start; i-- {
		prev := spreadOld[i+1] - 1.0
		if prev > spreadOld[i] {
			spreadOld[i] = prev
		}
	}

	// Compute mean increase across bands and channels.
	meanDiff := 0.0
	count := 0
	bandStart := start
	if bandStart < 2 {
		bandStart = 2
	}
	for c := 0; c < channels; c++ {
		for i := bandStart; i < end-1; i++ {
			x1 := getNew(c, i)
			if x1 < 0 {
				x1 = 0
			}
			x2 := spreadOld[i]
			if x2 < 0 {
				x2 = 0
			}
			diff := x1 - x2
			if diff < 0 {
				diff = 0
			}
			meanDiff += diff
			count++
		}
	}
	if count == 0 {
		return false
	}
	meanDiff /= float64(count)

	return meanDiff > 1.0
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
