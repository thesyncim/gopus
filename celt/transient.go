// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides transient detection for short block decisions.
//
// Transient detection identifies frames with sudden energy changes (attacks)
// that benefit from using multiple short MDCTs instead of one long MDCT.
// Short blocks provide better time resolution at the cost of frequency resolution,
// which is crucial for preserving the quality of percussive sounds like drums,
// hand claps, and other impulsive audio.
//
// The implementation provides multiple detection methods:
//
// 1. TransientAnalysis: Standard libopus-compatible analysis using forward/backward
//    masking to compute a mask_metric. Threshold > 200 triggers transient mode.
//
// 2. TransientAnalysisWithState: Enhanced version with persistent state across frames.
//    This improves detection of attacks spanning frame boundaries and uses:
//    - Persistent HP filter state for continuous attack tracking
//    - Attack duration tracking for percussive passage handling
//    - Hysteresis to prevent rapid toggling
//    - Adaptive thresholding based on signal level
//
// 3. DetectPercussiveAttack: Specialized detector for sharp percussive attacks
//    using envelope analysis and crest factor computation. Useful for catching
//    attacks that masking-based detection might miss.
//
// 4. PatchTransientDecision: Secondary check using band energy comparison to
//    catch transients missed by time-domain analysis.
//
// Reference: libopus celt/celt_encoder.c transient_analysis(), patch_transient_decision()

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
func toneLPC(x []float32, delay int) (float32, float32, bool) {
	n := len(x)
	if n <= 2*delay {
		return 0, 0, false
	}

	// Compute correlations using forward prediction covariance method.
	var r00, r01, r02 float32
	for i := 0; i < n-2*delay; i++ {
		r00 += x[i] * x[i]
		r01 += x[i] * x[i+delay]
		r02 += x[i] * x[i+2*delay]
	}

	// Edge corrections for r11, r22, r12.
	var edges float32
	for i := 0; i < delay; i++ {
		edges += x[n+i-2*delay]*x[n+i-2*delay] - x[i]*x[i]
	}
	r11 := r00 + edges

	edges = 0
	for i := 0; i < delay; i++ {
		edges += x[n+i-delay]*x[n+i-delay] - x[i+delay]*x[i+delay]
	}
	r22 := r11 + edges

	edges = 0
	for i := 0; i < delay; i++ {
		edges += x[n+i-2*delay]*x[n+i-delay] - x[i]*x[i+delay]
	}
	r12 := r01 + edges

	// Combine forward and backward for symmetric solution.
	R00 := r00 + r22
	R01 := r01 + r12
	R11 := 2 * r11
	R02 := 2 * r02
	R12 := r12 + r01

	// Solve A*x=b where A=[R00, R01; R01, R11] and b=[R02; R12].
	den := R00*R11 - R01*R01
	if den < 0.001*R00*R11 {
		return 0, 0, false
	}

	num1 := R02*R11 - R01*R12
	var lpc1 float32
	if num1 >= den {
		lpc1 = 1.0
	} else if num1 <= -den {
		lpc1 = -1.0
	} else {
		lpc1 = num1 / den
	}

	num0 := R00*R12 - R02*R01
	var lpc0 float32
	if 0.5*num0 >= den {
		lpc0 = 1.999999
	} else if 0.5*num0 <= -den {
		lpc0 = -1.999999
	} else {
		lpc0 = num0 / den
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
	return toneDetectScratch(in, channels, sampleRate, nil)
}

// toneDetectScratch is the scratch-aware version of toneDetect.
func toneDetectScratch(in []float64, channels int, sampleRate int, xBuf []float32) (float64, float64) {
	n := len(in) / channels
	if n < 4 {
		return -1, 0
	}

	// Use provided buffer or allocate
	var x []float32
	if xBuf != nil && len(xBuf) >= n {
		x = xBuf[:n]
	} else {
		x = make([]float32, n)
	}

	// Mix down to mono if stereo (matching libopus behavior)
	if channels == 2 {
		for i := 0; i < n; i++ {
			// libopus sums channels (no 0.5 scale in float builds).
			x[i] = float32(in[i*2] + in[i*2+1])
		}
	} else {
		for i := 0; i < n; i++ {
			x[i] = float32(in[i])
		}
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
	if success && float64(lpc0*lpc0+3.999999*lpc1) < 0 {
		// Toneishness is the squared radius of the poles.
		toneishness := -lpc1
		// Frequency from the angle of the complex pole.
		freq := math.Acos(0.5*float64(lpc0)) / float64(delay)
		return freq, float64(toneishness)
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
	return e.transientAnalysisScratch(pcm, frameSize, allowWeakTransients,
		e.scratch.transientX,
		e.scratch.transientChannelSamps,
		e.scratch.transientTmp,
		e.scratch.transientEnergy)
}

// transientAnalysisScratch is the scratch-aware version of TransientAnalysis.
func (e *Encoder) transientAnalysisScratch(pcm []float64, frameSize int, allowWeakTransients bool,
	toneBuf []float32, channelBuf []float64, tmpBuf []float64, energyBuf []float64) TransientAnalysisResult {
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
	toneFreq, toneishness := toneDetectScratch(pcm, channels, 48000, toneBuf)
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

	var maxMaskMetric int
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

	// Ensure scratch buffers are large enough
	var channelSamples []float64
	if channelBuf != nil && len(channelBuf) >= samplesPerChannel {
		channelSamples = channelBuf[:samplesPerChannel]
	} else {
		channelSamples = make([]float64, samplesPerChannel)
	}

	var tmp []float64
	if tmpBuf != nil && len(tmpBuf) >= samplesPerChannel {
		tmp = tmpBuf[:samplesPerChannel]
	} else {
		tmp = make([]float64, samplesPerChannel)
	}

	len2 := samplesPerChannel / 2
	var energy []float64
	if energyBuf != nil && len(energyBuf) >= len2 {
		energy = energyBuf[:len2]
	} else {
		energy = make([]float64, len2)
	}

	// Process each channel
	for c := 0; c < channels; c++ {
		// Extract channel samples
		for i := 0; i < samplesPerChannel; i++ {
			channelSamples[i] = pcm[i*channels+c]
		}

		// High-pass filter: (1 - 2*z^-1 + z^-2) / (1 - z^-1 + 0.5*z^-2)
		// This removes DC and low frequencies to focus on transient energy
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

		// Forward pass: compute post-echo threshold with forward masking
		// Group by two to reduce complexity
		// Note: In libopus FLOAT mode, PSHR32 is a no-op, so no scaling is applied
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

		// Use the exact integer normalization from libopus:
		// mask_metric = 64*unmask*4/(6*(len2-17))
		maskMetric := 0
		if len2 > 17 {
			maskMetric = 64 * unmask * 4 / (6 * (len2 - 17))
		}

		if maskMetric > maxMaskMetric {
			tfChannel = c
			maxMaskMetric = maskMetric
		}
	}

	result.MaskMetric = float64(maxMaskMetric)
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

	tfMax := math.Max(0, math.Sqrt(27*float64(maxMaskMetric))-42)
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

// TransientAnalysisWithState performs enhanced transient analysis using persistent state.
// This improves detection of percussive sounds by:
//   1. Using persistent HP filter state across frames for better attack detection
//   2. Tracking attack duration for multi-frame transient handling
//   3. Applying hysteresis to prevent rapid toggling
//   4. Adaptive thresholding based on signal level
//
// This function updates the encoder's transient state and should be used
// when encoding sequences of frames for optimal percussive sound quality.
//
// Parameters:
//   - pcm: input PCM samples (mono or interleaved stereo, pre-emphasized)
//   - frameSize: frame size in samples (total including overlap)
//   - allowWeakTransients: for hybrid mode at low bitrate
//
// Returns: TransientAnalysisResult with all metrics
//
// Reference: libopus celt/celt_encoder.c transient_analysis() with state persistence
func (e *Encoder) TransientAnalysisWithState(pcm []float64, frameSize int, allowWeakTransients bool) TransientAnalysisResult {
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
	toneFreq, toneishness := toneDetect(pcm, channels, 48000)
	result.ToneFreq = toneFreq
	result.Toneishness = toneishness

	// Forward masking decay
	forwardDecay := float32(0.0625)
	if allowWeakTransients {
		forwardDecay = 0.03125
	}

	// Inverse table for computing harmonic mean (6*64/x)
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

	var maxMaskMetric float64
	tfChannel := 0
	var totalFrameEnergy float64

	// Process each channel with PERSISTENT HP filter state
	for c := 0; c < channels; c++ {
		// Extract channel samples
		channelSamples := make([]float32, samplesPerChannel)
		for i := 0; i < samplesPerChannel; i++ {
			channelSamples[i] = float32(pcm[i*channels+c])
		}

		// High-pass filter with PERSISTENT memory
		// This is key for detecting attacks that span frame boundaries
		tmp := make([]float32, samplesPerChannel)
		mem0 := e.transientHPMem[c][0]
		mem1 := e.transientHPMem[c][1]

		for i := 0; i < samplesPerChannel; i++ {
			x := channelSamples[i]
			y := mem0 + x
			// Modified code to shorten dependency chains (matches libopus float)
			mem00 := mem0
			mem0 = mem0 - x + 0.5*mem1
			mem1 = x - mem00
			tmp[i] = y
		}

		// Save HP filter state for next frame
		e.transientHPMem[c][0] = mem0
		e.transientHPMem[c][1] = mem1

		// Clear first few samples (filter warm-up) - but ONLY for first frame
		// For subsequent frames, the HP memory carries valid state
		warmupSamples := 12
		if e.frameCount > 0 {
			warmupSamples = 4 // Less warmup needed when state is valid
		}
		for i := 0; i < warmupSamples && i < len(tmp); i++ {
			tmp[i] = 0
		}

		len2 := samplesPerChannel / 2

		// Forward pass: compute post-echo threshold with forward masking
		energy := make([]float32, len2)
		var mean float32
		var memFwd float32
		for i := 0; i < len2; i++ {
			x2 := tmp[2*i]*tmp[2*i] + tmp[2*i+1]*tmp[2*i+1]
			mean += x2
			totalFrameEnergy += float64(x2)
			memFwd = x2 + (1.0-forwardDecay)*memFwd
			energy[i] = forwardDecay * memFwd
		}

		// Backward pass: compute pre-echo threshold (13.9 dB/ms decay)
		var maxE float32
		var memBwd float32
		for i := len2 - 1; i >= 0; i-- {
			memBwd = energy[i] + 0.875*memBwd
			energy[i] = 0.125 * memBwd
			if 0.125*memBwd > maxE {
				maxE = 0.125 * memBwd
			}
		}

		// Compute frame energy as geometric mean of mean and max
		geoMean := float32(math.Sqrt(float64(mean * maxE * 0.5 * float32(len2))))

		// Inverse of mean energy
		var norm float32
		if geoMean > 1e-15 {
			norm = float32(len2) / (geoMean*0.5 + 1e-15)
		}

		// Compute harmonic mean using inverse table
		var unmask int
		for i := 12; i < len2-5; i += 4 {
			id := int(math.Floor(float64(64 * norm * (energy[i] + 1e-15))))
			if id < 0 {
				id = 0
			}
			if id > 127 {
				id = 127
			}
			unmask += invTable[id]
		}

		// Normalize
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

	// Update peak energy tracking for adaptive thresholding
	if totalFrameEnergy > e.peakEnergy {
		e.peakEnergy = totalFrameEnergy
	} else {
		// Slow decay of peak energy (adapt to quieter passages)
		e.peakEnergy = 0.99*e.peakEnergy + 0.01*totalFrameEnergy
	}

	// Adaptive threshold based on signal level
	// For quiet signals, be more aggressive to catch small transients
	// For loud signals, use standard threshold
	threshold := 200.0
	if e.peakEnergy > 0 && totalFrameEnergy < 0.1*e.peakEnergy {
		// Quieter than 10dB below peak - lower threshold to catch subtle attacks
		threshold = 150.0
	}

	// Apply hysteresis: if we were in transient state, use lower threshold
	// to stay in transient mode (prevents rapid toggling on percussive passages)
	if e.attackDuration > 0 {
		threshold *= 0.8 // 20% lower threshold to maintain transient state
	}

	// Transient decision with hysteresis
	result.IsTransient = maxMaskMetric > threshold

	// Prevent false positives on very low frequency tones
	if result.Toneishness > 0.98 && result.ToneFreq >= 0 && result.ToneFreq < 0.026 {
		result.IsTransient = false
		result.MaskMetric = 0
	}

	// Update attack duration tracking
	if result.IsTransient {
		e.attackDuration++
		// Cap at 10 to prevent overflow and indicate sustained percussive passage
		if e.attackDuration > 10 {
			e.attackDuration = 10
		}
	} else {
		// Decay attack duration (don't reset immediately for continuity)
		if e.attackDuration > 0 {
			e.attackDuration--
		}
	}

	// Weak transient handling for hybrid mode
	if allowWeakTransients && result.IsTransient && maxMaskMetric < 600 {
		result.IsTransient = false
		result.WeakTransient = true
	}

	// Store last mask metric for hysteresis
	e.lastMaskMetric = maxMaskMetric

	// Compute tf_estimate from mask_metric
	tfMax := math.Max(0, math.Sqrt(27*maxMaskMetric)-42)
	clampedTfMax := math.Min(163, tfMax)
	tfEstimateSquared := 0.0069*clampedTfMax - 0.139
	if tfEstimateSquared < 0 {
		tfEstimateSquared = 0
	}
	result.TfEstimate = math.Sqrt(tfEstimateSquared)
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

// PatchTransientDecision looks for sudden increases of energy to decide whether
// we need to patch the transient decision. This is a "second chance" to detect
// transients that the time-domain transient_analysis() may have missed.
//
// This is particularly important for the first frame where the time-domain
// analysis may fail due to zero-padded buffers, but the frequency-domain
// energy increase from silence to signal is obvious.
//
// Parameters:
//   - newE: current frame's band log-energies (log2 domain)
//   - oldE: previous frame's band log-energies (log2 domain)
//   - nbEBands: number of effective bands
//   - start: first band to consider (usually 0)
//   - end: last band + 1 to consider (usually nbEBands)
//   - channels: number of channels (1 or 2)
//
// Returns: true if mean energy increase > 1.0 dB and transient should be forced
//
// Reference: libopus celt/celt_encoder.c patch_transient_decision()
func PatchTransientDecision(newE, oldE []float64, nbEBands, start, end, channels int) bool {
	if len(newE) < end || len(oldE) < end {
		return false
	}

	// Apply an aggressive (-6 dB/Bark) spreading function to the old frame
	// to avoid false detection caused by irrelevant bands.
	// GCONST(1.0f) in libopus is 1.0 in the log-energy domain (corresponds to ~6dB).
	spreadOld := make([]float64, end)

	if channels == 1 {
		spreadOld[start] = oldE[start]
		for i := start + 1; i < end; i++ {
			spreadOld[i] = math.Max(spreadOld[i-1]-1.0, oldE[i])
		}
	} else {
		// Stereo: use max of left and right channel
		spreadOld[start] = math.Max(oldE[start], oldE[start+nbEBands])
		for i := start + 1; i < end; i++ {
			spreadOld[i] = math.Max(spreadOld[i-1]-1.0,
				math.Max(oldE[i], oldE[i+nbEBands]))
		}
	}

	// Backward pass: spread from high to low frequencies
	for i := end - 2; i >= start; i-- {
		spreadOld[i] = math.Max(spreadOld[i], spreadOld[i+1]-1.0)
	}

	// Compute mean increase
	var meanDiff float64
	startBand := start
	if startBand < 2 {
		startBand = 2
	}

	for c := 0; c < channels; c++ {
		for i := startBand; i < end-1; i++ {
			x1 := math.Max(0, newE[i+c*nbEBands])
			x2 := math.Max(0, spreadOld[i])
			meanDiff += math.Max(0, x1-x2)
		}
	}

	numBands := end - 1 - startBand
	if numBands < 1 {
		numBands = 1
	}
	meanDiff /= float64(channels * numBands)

	// Return true if mean increase > 1.0 (in log domain, this is ~6 dB)
	return meanDiff > 1.0
}

// DetectPercussiveAttack performs specialized detection for sharp percussive attacks.
// This is optimized for drum hits, hand claps, and other impulsive sounds that
// require very fine time resolution.
//
// Unlike the standard transient detector which uses forward/backward masking,
// this function looks for:
//   - Very rapid energy rise (attack time < 5ms)
//   - High peak-to-average ratio (crest factor)
//   - Broadband energy distribution (not tonal)
//
// Parameters:
//   - pcm: input PCM samples (mono or interleaved stereo)
//   - frameSize: frame size in samples
//
// Returns: (isPercussive, attackPosition, attackStrength)
//   - isPercussive: true if a sharp percussive attack is detected
//   - attackPosition: sample index where attack begins (0 to frameSize-1)
//   - attackStrength: measure of attack sharpness (0.0 to 1.0)
//
// This can be used to:
//   1. Force transient mode even when standard detection misses it
//   2. Adjust TF resolution for optimal attack preservation
//   3. Guide pre-echo reduction in VBR mode
func (e *Encoder) DetectPercussiveAttack(pcm []float64, frameSize int) (bool, int, float64) {
	if len(pcm) == 0 || frameSize <= 0 {
		return false, 0, 0
	}

	channels := e.channels
	samplesPerChannel := len(pcm) / channels

	if samplesPerChannel < 32 {
		return false, 0, 0
	}

	// Compute envelope using RMS in small windows
	// Use 1ms windows at 48kHz = 48 samples
	windowSize := 48
	if windowSize > samplesPerChannel/4 {
		windowSize = samplesPerChannel / 4
	}
	if windowSize < 8 {
		windowSize = 8
	}

	numWindows := samplesPerChannel / windowSize
	envelope := make([]float64, numWindows)

	for w := 0; w < numWindows; w++ {
		start := w * windowSize
		end := start + windowSize

		var sumSq float64
		for c := 0; c < channels; c++ {
			for i := start; i < end && i < samplesPerChannel; i++ {
				idx := i*channels + c
				if idx < len(pcm) {
					sumSq += pcm[idx] * pcm[idx]
				}
			}
		}
		envelope[w] = math.Sqrt(sumSq / float64(windowSize*channels))
	}

	// Find the maximum envelope value and its position
	var maxEnv float64
	maxPos := 0
	for w, env := range envelope {
		if env > maxEnv {
			maxEnv = env
			maxPos = w
		}
	}

	if maxEnv < 1e-10 {
		return false, 0, 0
	}

	// Compute average envelope (excluding the peak region and near-silence)
	// Only include windows with significant energy to get meaningful crest factor
	var sumEnv float64
	count := 0
	noiseFloor := maxEnv * 0.01 // 40dB below peak
	for w, env := range envelope {
		// Exclude 3 windows around peak and very quiet windows
		if (w < maxPos-1 || w > maxPos+1) && env > noiseFloor {
			sumEnv += env
			count++
		}
	}
	avgEnv := 0.0
	if count > 0 {
		avgEnv = sumEnv / float64(count)
	}

	// Crest factor: ratio of peak to average (of non-silent regions)
	var crestFactor float64
	if avgEnv > 1e-10 {
		crestFactor = maxEnv / avgEnv
	} else {
		// If average is near zero (signal from silence), consider it high crest factor
		crestFactor = 100.0
	}

	// Attack detection: look for rapid rise in envelope
	// Check if there's a >6dB jump in 2ms (2 windows at 1ms each)
	attackDetected := false
	attackPos := 0
	attackStrength := 0.0

	for w := 1; w < numWindows; w++ {
		if envelope[w] > 1e-10 && envelope[w-1] > 1e-10 {
			ratio := envelope[w] / envelope[w-1]
			// 6dB rise = factor of 2
			if ratio > 2.0 {
				attackDetected = true
				attackPos = w * windowSize
				// Normalize attack strength by crest factor
				attackStrength = math.Min(1.0, (ratio-1.0)/3.0) // Normalized 0-1
				break
			}
		} else if envelope[w] > 1e-6 && envelope[w-1] < 1e-10 {
			// Attack from silence - definite percussive event
			attackDetected = true
			attackPos = w * windowSize
			attackStrength = 1.0
			break
		}
	}

	// Determine if this is a percussive attack
	// Either:
	// 1. Attack detected AND high crest factor (> 4 = 12dB peak-to-average)
	// 2. Attack from silence (attackStrength == 1.0)
	// 3. In ongoing percussive passage with moderate crest factor
	isPercussive := false
	if attackDetected {
		if attackStrength >= 1.0 {
			// Attack from silence is always percussive
			isPercussive = true
		} else if crestFactor > 4.0 {
			// High crest factor indicates impulsive nature
			isPercussive = true
		} else if e.attackDuration > 2 && crestFactor > 2.5 {
			// In ongoing percussive passage, lower threshold
			isPercussive = true
		}
	}

	return isPercussive, attackPos, attackStrength
}

// GetAttackDuration returns the number of consecutive transient frames.
// This is useful for adapting encoding parameters during percussive passages.
// A value > 1 indicates sustained percussive activity (e.g., drum roll).
func (e *Encoder) GetAttackDuration() int {
	return e.attackDuration
}

// ResetTransientState clears the transient detection state.
// Call this when starting a new audio segment or after a discontinuity.
func (e *Encoder) ResetTransientState() {
	for c := 0; c < 2; c++ {
		e.transientHPMem[c][0] = 0
		e.transientHPMem[c][1] = 0
	}
	e.attackDuration = 0
	e.lastMaskMetric = 0
	e.peakEnergy = 0
}

// ShouldUseShortBlocks combines multiple transient indicators to decide
// whether to use short blocks. This is a high-level decision function
// that considers:
//   - Standard transient analysis (mask_metric)
//   - Percussive attack detection
//   - Attack duration (hysteresis)
//   - Frame budget constraints
//
// Parameters:
//   - transientResult: result from TransientAnalysis or TransientAnalysisWithState
//   - percussiveDetected: result from DetectPercussiveAttack
//   - lm: log2 of frame size multiplier (0-3)
//   - totalBits: available bits for the frame
//
// Returns: true if short blocks should be used
func ShouldUseShortBlocks(transientResult TransientAnalysisResult, percussiveDetected bool, lm int, totalBits int) bool {
	// LM=0 (2.5ms frames) cannot use short blocks
	if lm == 0 {
		return false
	}

	// Need at least 3 bits to encode the transient flag
	if totalBits < 3 {
		return false
	}

	// Primary: use TransientAnalysis result
	if transientResult.IsTransient {
		return true
	}

	// Secondary: percussive attack detection can override
	// This helps catch sharp attacks that masking-based detection might miss
	if percussiveDetected && !transientResult.IsTransient {
		// Only override if tf_estimate suggests some temporal variation
		// (tf_estimate near 0 means very steady signal)
		if transientResult.TfEstimate > 0.1 || transientResult.MaskMetric > 100 {
			return true
		}
	}

	return false
}
