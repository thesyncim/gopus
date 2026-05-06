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

import (
	"math"
)

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
	MaskMetric    float64 // Raw mask metric value.
	WeakTransient bool    // Whether this is a "weak" transient (for hybrid mode)
	ToneFreq      float64 // Detected tone frequency in radians/sample (-1 if no tone)
	Toneishness   float64 // How "pure" the tone is (0.0-1.0, higher = purer)
}

// toneLPC computes 2nd-order LPC coefficients using forward+backward least-squares fit.
// This is used to detect pure tones by analyzing the resonant characteristics.
// Returns (lpc0, lpc1, success) where success=false if the computation fails.
// Reference: libopus celt/celt_encoder.c tone_lpc()
func toneLPC(x []float32, delay int, lane4Corr bool) (float32, float32, bool) {
	n := len(x)
	if n <= 2*delay {
		return 0, 0, false
	}
	if delay == 1 {
		return toneLPCDelay1(x, lane4Corr)
	}

	// BCE hint: the maximum index accessed in the correlation loop is (cnt-1)+2*delay = n-1.
	_ = x[n-1]

	// Compute correlations using forward prediction covariance method.
	cnt := n - 2*delay
	delay2 := 2 * delay
	var r00, r01, r02 float32
	if lane4Corr {
		r00, r01, r02 = toneLPCCorrLane4(x, cnt, delay, delay2)
	} else {
		r00, r01, r02 = toneLPCCorr(x, cnt, delay, delay2)
	}

	// Edge corrections for r11, r22, r12.
	// Precompute base offsets to avoid repeated arithmetic.
	base1 := n - delay2 // n-2*delay
	base2 := n - delay
	var edges float32
	for i := 0; i < delay; i++ {
		a := x[base1+i]
		b := x[i]
		edges += a*a - b*b
	}
	r11 := r00 + edges

	edges = 0
	for i := 0; i < delay; i++ {
		a := x[base2+i]
		b := x[i+delay]
		edges += a*a - b*b
	}
	r22 := r11 + edges

	edges = 0
	for i := 0; i < delay; i++ {
		edges += x[base1+i]*x[base2+i] - x[i]*x[i+delay]
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
	if den < float32(0.001)*R00*R11 {
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
	if float32(0.5)*num0 >= den {
		lpc0 = 1.999999
	} else if float32(0.5)*num0 <= -den {
		lpc0 = -1.999999
	} else {
		lpc0 = num0 / den
	}

	return lpc0, lpc1, true
}

func toneLPCDelay1(x []float32, lane4Corr bool) (float32, float32, bool) {
	n := len(x)

	// BCE hint: the maximum index accessed in the correlation loop is n-1.
	_ = x[n-1]

	cnt := n - 2
	var r00, r01, r02 float32
	if lane4Corr {
		r00, r01, r02 = toneLPCCorrLane4(x, cnt, 1, 2)
	} else {
		r00, r01, r02 = toneLPCCorrDelay1(x, cnt)
	}

	r11 := r00 + x[n-2]*x[n-2] - x[0]*x[0]
	r22 := r11 + x[n-1]*x[n-1] - x[1]*x[1]
	r12 := r01 + x[n-2]*x[n-1] - x[0]*x[1]

	R00 := r00 + r22
	R01 := r01 + r12
	R11 := 2 * r11
	R02 := 2 * r02
	R12 := r12 + r01

	den := R00*R11 - R01*R01
	if den < float32(0.001)*R00*R11 {
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
	if float32(0.5)*num0 >= den {
		lpc0 = 1.999999
	} else if float32(0.5)*num0 <= -den {
		lpc0 = -1.999999
	} else {
		lpc0 = num0 / den
	}

	return lpc0, lpc1, true
}

// toneLPCCorrLane4 mirrors the four-lane reduction shape used by the
// libopus float build for non-amd64 stereo tone detection.
func toneLPCCorrLane4(x []float32, cnt, delay, delay2 int) (r00, r01, r02 float32) {
	i := 0
	var r00v0, r00v1, r00v2, r00v3 float32
	var r01v0, r01v1, r01v2, r01v3 float32
	var r02v0, r02v1, r02v2, r02v3 float32
	for ; i+3 < cnt; i += 4 {
		xi := x[i]
		r00v0 += xi * xi
		r01v0 += xi * x[i+delay]
		r02v0 += xi * x[i+delay2]

		xi = x[i+1]
		r00v1 += xi * xi
		r01v1 += xi * x[i+1+delay]
		r02v1 += xi * x[i+1+delay2]

		xi = x[i+2]
		r00v2 += xi * xi
		r01v2 += xi * x[i+2+delay]
		r02v2 += xi * x[i+2+delay2]

		xi = x[i+3]
		r00v3 += xi * xi
		r01v3 += xi * x[i+3+delay]
		r02v3 += xi * x[i+3+delay2]
	}
	r00 = (r00v0 + r00v1) + (r00v2 + r00v3)
	r01 = (r01v0 + r01v1) + (r01v2 + r01v3)
	r02 = (r02v0 + r02v1) + (r02v2 + r02v3)
	for ; i < cnt; i++ {
		xi := x[i]
		r00 += xi * xi
		r01 += xi * x[i+delay]
		r02 += xi * x[i+delay2]
	}
	return
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
			// libopus sums the two celt_sig float channels inside tone_detect.
			x[i] = float32(in[i*2]) + float32(in[i*2+1])
		}
	} else {
		for i := 0; i < n; i++ {
			x[i] = float32(in[i])
		}
	}

	lane4Corr := channels == 2 && toneLPCStereoLane4
	return toneDetectFloat32Mono(x, sampleRate, lane4Corr)
}

func toneDetectFloat32Mono(x []float32, sampleRate int, lane4Corr bool) (float64, float64) {
	n := len(x)
	if n < 4 {
		return -1, 0
	}

	delay := 1
	lpc0, lpc1, success := toneLPCDelay1(x, lane4Corr)

	// If LPC resonates too close to DC, retry with downsampling
	// (delay <= sampleRate/3000 corresponds to frequencies > ~1500 Hz)
	maxDelay := sampleRate / 3000
	if maxDelay < 1 {
		maxDelay = 1
	}
	for delay <= maxDelay && (!success || (lpc0 > float32(1.0) && lpc1 < 0)) {
		delay *= 2
		if 2*delay >= n {
			break
		}
		lpc0, lpc1, success = toneLPC(x, delay, lane4Corr)
	}

	// Check that our filter has complex roots: lpc0^2 + 4*lpc1 < 0
	// This indicates a resonant (tonal) system
	if success && (lpc0*lpc0+float32(3.999999)*lpc1) < 0 {
		// Toneishness is the squared radius of the poles.
		toneishness := -lpc1
		// Frequency from the angle of the complex pole.
		freq := float32(math.Acos(float64(float32(0.5)*lpc0)) / float64(delay))
		return float64(freq), float64(toneishness)
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
		e.scratch.transientEnergy)
}

func (e *Encoder) transientAnalysisMonoFloat32(pcm []float32, frameSize int, allowWeakTransients bool) TransientAnalysisResult {
	result := TransientAnalysisResult{
		TfEstimate:  0.0,
		TfChannel:   0,
		ToneFreq:    -1,
		Toneishness: 0,
	}

	if len(pcm) == 0 || frameSize <= 0 {
		return result
	}
	samplesPerChannel := len(pcm)
	if samplesPerChannel < 16 {
		return result
	}

	toneFreq, toneishness := toneDetectFloat32Mono(pcm[:samplesPerChannel], 48000, false)
	result.ToneFreq = toneFreq
	result.Toneishness = toneishness

	forwardDecay := float32(0.0625)
	forwardRetain := float32(1.0) - forwardDecay
	if allowWeakTransients {
		forwardDecay = 0.03125
		forwardRetain = float32(1.0) - forwardDecay
	}

	len2 := samplesPerChannel / 2
	var energy []float32
	if len(e.scratch.transientEnergy) >= len2 {
		energy = e.scratch.transientEnergy[:len2]
	} else {
		energy = make([]float32, len2)
	}

	const (
		hpFeedback     = float32(0.5)
		backwardRetain = float32(0.875)
		backwardScale  = float32(0.125)
		warmupPairs    = 6
	)
	var hp0, hp1 float32
	var mask float32
	mean := float32(0)
	src := pcm[:samplesPerChannel]
	_ = src[2*len2-1]
	for i := 0; i < len2; i++ {
		j := i << 1

		x0 := src[j]
		y0 := hp0 + x0
		hp00 := hp0
		hp0 = hp0 - x0 + hpFeedback*hp1
		hp1 = x0 - hp00

		x1 := src[j+1]
		y1 := hp0 + x1
		hp00 = hp0
		hp0 = hp0 - x1 + hpFeedback*hp1
		hp1 = x1 - hp00

		if i < warmupPairs {
			y0 = 0
			y1 = 0
		}

		pair := y0*y0 + y1*y1
		mean += pair
		mask = pair + forwardRetain*mask
		energy[i] = forwardDecay * mask
	}

	var maxE float32
	mask = 0
	for i := len2; i > 0; {
		i--
		mask = energy[i] + backwardRetain*mask
		ei := backwardScale * mask
		energy[i] = ei
		if ei > maxE {
			maxE = ei
		}
	}

	meanGeom := math.Sqrt(float64(mean * maxE * float32(0.5*float64(len2))))
	const epsilon = 1e-15
	normE := float32(float64(64*len2) / (meanGeom + epsilon))

	const epsF32 = float32(1e-15)
	var unmask int
	for i := 12; i < len2-5; i += 4 {
		id := int(normE * (energy[i] + epsF32))
		if id > 127 {
			id = 127
		}
		unmask += transientInvTable[id]
	}

	maxMaskMetric := 0
	if len2 > 17 {
		maxMaskMetric = 64 * unmask * 4 / (6 * (len2 - 17))
	}

	result.TfChannel = 0
	result.IsTransient = maxMaskMetric > 200
	if result.Toneishness > 0.98 && result.ToneFreq >= 0 && result.ToneFreq < 0.026 {
		result.IsTransient = false
		maxMaskMetric = 0
	}
	if allowWeakTransients && result.IsTransient && maxMaskMetric < 600 {
		result.IsTransient = false
		result.WeakTransient = true
	}
	result.MaskMetric = float64(maxMaskMetric)

	tfMax := math.Sqrt(27*float64(maxMaskMetric)) - 42
	if tfMax < 0 {
		tfMax = 0
	}
	if tfMax > 163 {
		tfMax = 163
	}
	tfEstimateSquared := 0.0069*tfMax - 0.139
	if tfEstimateSquared < 0 {
		tfEstimateSquared = 0
	}
	result.TfEstimate = math.Sqrt(tfEstimateSquared)
	if result.TfEstimate > 1.0 {
		result.TfEstimate = 1.0
	}

	return result
}

// transientInvTable is the inverse table for computing harmonic mean (6*64/x, trained on real data).
// Hoisted to package level to avoid re-initializing on every call. This matches libopus exactly.
var transientInvTable = [128]int{
	255, 255, 156, 110, 86, 70, 59, 51, 45, 40, 37, 33, 31, 28, 26, 25,
	23, 22, 21, 20, 19, 18, 17, 16, 16, 15, 15, 14, 13, 13, 12, 12,
	12, 12, 11, 11, 11, 10, 10, 10, 9, 9, 9, 9, 9, 9, 8, 8,
	8, 8, 8, 7, 7, 7, 7, 7, 7, 6, 6, 6, 6, 6, 6, 6,
	6, 6, 6, 6, 6, 6, 6, 6, 6, 5, 5, 5, 5, 5, 5, 5,
	5, 5, 5, 5, 5, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4,
	4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 3, 3,
	3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 2,
}

// transientAnalysisScratch is the scratch-aware version of TransientAnalysis.
func (e *Encoder) transientAnalysisScratch(pcm []float64, frameSize int, allowWeakTransients bool,
	toneBuf []float32, energyBuf []float32) TransientAnalysisResult {
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

	// Detect pure tones before transient analysis. Mono can fill the tone
	// buffer while computing pair energies below; stereo keeps the standalone
	// path because toneBuf is reused for right-channel energy.
	deferMonoToneDetect := channels == 1 && len(toneBuf) >= samplesPerChannel
	deferStereoToneDetect := channels == 2 && len(toneBuf) >= samplesPerChannel && len(e.scratch.transientEnergyR) >= samplesPerChannel/2
	var monoToneX []float32
	if deferMonoToneDetect {
		monoToneX = toneBuf[:samplesPerChannel]
	} else if !deferStereoToneDetect {
		toneFreq, toneishness := toneDetectScratch(pcm, channels, 48000, toneBuf)
		result.ToneFreq = toneFreq
		result.Toneishness = toneishness
	}

	// Forward masking decay: 6.7 dB/ms (default) or 3.3 dB/ms (weak transients)
	// At 48kHz, we process pairs of samples, so decay per pair:
	// Default: forward_decay = 0.0625 (1/16)
	// Weak: forward_decay = 0.03125 (1/32)
	// Precompute retain = 1 - decay to avoid per-iteration subtraction.
	forwardDecay := float32(0.0625)
	forwardRetain := float32(1.0) - forwardDecay
	if allowWeakTransients {
		forwardDecay = 0.03125
		forwardRetain = float32(1.0) - forwardDecay
	}

	var maxMaskMetric int
	tfChannel := 0

	len2 := samplesPerChannel / 2
	var energy []float32
	if energyBuf != nil && len(energyBuf) >= len2 {
		energy = energyBuf[:len2]
	} else {
		energy = make([]float32, len2)
	}

	// Stereo can process both channels in one pass over the interleaved PCM
	// while preserving each channel's exact arithmetic order.
	if channels == 2 {
		var energyR []float32
		if deferStereoToneDetect {
			energyR = e.scratch.transientEnergyR[:len2]
		} else if len(toneBuf) >= len2 {
			energyR = toneBuf[:len2]
		} else {
			energyR = make([]float32, len2)
		}

		const (
			hpFeedback     = float32(0.5)
			backwardRetain = float32(0.875)
			backwardScale  = float32(0.125)
			warmupPairs    = 6
		)
		var hp0L, hp1L float32
		var hp0R, hp1R float32
		var maskL, maskR float32
		meanL := float32(0)
		meanR := float32(0)
		idx := 0
		_ = pcm[4*len2-1]
		for i := 0; i < len2; i++ {
			xL0 := float32(pcm[idx])
			xR0 := float32(pcm[idx+1])
			xL1 := float32(pcm[idx+2])
			xR1 := float32(pcm[idx+3])
			if deferStereoToneDetect {
				toneBuf[i<<1] = xL0 + xR0
				toneBuf[(i<<1)+1] = xL1 + xR1
			}
			idx += 4

			yL0 := hp0L + xL0
			hp00L := hp0L
			hp0L = hp0L - xL0 + hpFeedback*hp1L
			hp1L = xL0 - hp00L

			yL1 := hp0L + xL1
			hp00L = hp0L
			hp0L = hp0L - xL1 + hpFeedback*hp1L
			hp1L = xL1 - hp00L

			yR0 := hp0R + xR0
			hp00R := hp0R
			hp0R = hp0R - xR0 + hpFeedback*hp1R
			hp1R = xR0 - hp00R

			yR1 := hp0R + xR1
			hp00R = hp0R
			hp0R = hp0R - xR1 + hpFeedback*hp1R
			hp1R = xR1 - hp00R

			if i < warmupPairs {
				yL0 = 0
				yL1 = 0
				yR0 = 0
				yR1 = 0
			}

			pairL := yL0*yL0 + yL1*yL1
			meanL += pairL
			maskL = pairL + forwardRetain*maskL
			energy[i] = forwardDecay * maskL

			pairR := yR0*yR0 + yR1*yR1
			meanR += pairR
			maskR = pairR + forwardRetain*maskR
			energyR[i] = forwardDecay * maskR
		}

		var maxEL, maxER float32
		maskL = 0
		maskR = 0
		for i := len2 - 1; i >= 0; i-- {
			maskL = energy[i] + backwardRetain*maskL
			eiL := backwardScale * maskL
			energy[i] = eiL
			if eiL > maxEL {
				maxEL = eiL
			}

			maskR = energyR[i] + backwardRetain*maskR
			eiR := backwardScale * maskR
			energyR[i] = eiR
			if eiR > maxER {
				maxER = eiR
			}
		}

		const epsilon = 1e-15
		normEL := float32(float64(64*len2) / (math.Sqrt(float64(meanL*maxEL*float32(0.5*float64(len2)))) + epsilon))
		normER := float32(float64(64*len2) / (math.Sqrt(float64(meanR*maxER*float32(0.5*float64(len2)))) + epsilon))

		const epsF32 = float32(1e-15)
		var unmaskL, unmaskR int
		for i := 12; i < len2-5; i += 4 {
			idL := int(normEL * (energy[i] + epsF32))
			if idL > 127 {
				idL = 127
			}
			unmaskL += transientInvTable[idL]

			idR := int(normER * (energyR[i] + epsF32))
			if idR > 127 {
				idR = 127
			}
			unmaskR += transientInvTable[idR]
		}

		maskMetricL := 0
		if len2 > 17 {
			maskMetricL = 64 * unmaskL * 4 / (6 * (len2 - 17))
		}
		if maskMetricL > maxMaskMetric {
			tfChannel = 0
			maxMaskMetric = maskMetricL
		}
		maskMetricR := 0
		if len2 > 17 {
			maskMetricR = 64 * unmaskR * 4 / (6 * (len2 - 17))
		}
		if maskMetricR > maxMaskMetric {
			tfChannel = 1
			maxMaskMetric = maskMetricR
		}
		if deferStereoToneDetect {
			toneFreq, toneishness := toneDetectFloat32Mono(toneBuf[:samplesPerChannel], 48000, toneLPCStereoLane4)
			result.ToneFreq = toneFreq
			result.Toneishness = toneishness
		}
		goto transientMetricsDone
	}

	// Process each channel
	for c := 0; c < channels; c++ {
		// Fuse the HP filter with pair-energy accumulation so we don't round-trip
		// through a temporary sample buffer before masking.
		_ = energy[len2-1]
		const (
			hpFeedback     = float32(0.5)
			backwardRetain = float32(0.875)
			backwardScale  = float32(0.125)
			warmupPairs    = 6
		)
		var hp0, hp1 float32
		var mask float32
		mean := float32(0)
		if channels == 1 {
			src := pcm[:samplesPerChannel]
			_ = src[2*len2-1]
			for i := 0; i < len2; i++ {
				j := i << 1

				x0 := float32(src[j])
				if deferMonoToneDetect {
					monoToneX[j] = x0
				}
				y0 := hp0 + x0
				hp00 := hp0
				hp0 = hp0 - x0 + hpFeedback*hp1
				hp1 = x0 - hp00

				x1 := float32(src[j+1])
				if deferMonoToneDetect {
					monoToneX[j+1] = x1
				}
				y1 := hp0 + x1
				hp00 = hp0
				hp0 = hp0 - x1 + hpFeedback*hp1
				hp1 = x1 - hp00

				if i < warmupPairs {
					y0 = 0
					y1 = 0
				}

				pair := y0*y0 + y1*y1
				mean += pair
				mask = pair + forwardRetain*mask
				energy[i] = forwardDecay * mask
			}
			if deferMonoToneDetect && samplesPerChannel > 2*len2 {
				monoToneX[samplesPerChannel-1] = float32(src[samplesPerChannel-1])
			}
		} else {
			stride := channels
			idx := c
			_ = pcm[(2*len2-1)*stride+c]
			for i := 0; i < len2; i++ {
				x0 := float32(pcm[idx])
				idx += stride
				y0 := hp0 + x0
				hp00 := hp0
				hp0 = hp0 - x0 + hpFeedback*hp1
				hp1 = x0 - hp00

				x1 := float32(pcm[idx])
				idx += stride
				y1 := hp0 + x1
				hp00 = hp0
				hp0 = hp0 - x1 + hpFeedback*hp1
				hp1 = x1 - hp00

				if i < warmupPairs {
					y0 = 0
					y1 = 0
				}

				pair := y0*y0 + y1*y1
				mean += pair
				mask = pair + forwardRetain*mask
				energy[i] = forwardDecay * mask
			}
		}

		// Backward pass: compute pre-echo threshold
		// Backward masking: 13.9 dB/ms (decay = 0.125)
		var maxE float32
		mask = 0
		for i := len2 - 1; i >= 0; i-- {
			mask = energy[i] + backwardRetain*mask
			ei := backwardScale * mask
			energy[i] = ei
			if ei > maxE {
				maxE = ei
			}
		}

		// Compute frame energy as geometric mean of mean and max
		// This is a compromise between old and new transient detectors
		meanGeom := math.Sqrt(float64(mean * maxE * float32(0.5*float64(len2))))

		// Inverse of mean energy (with epsilon to avoid division by zero)
		const epsilon = 1e-15
		normE := float32(float64(64*len2) / (meanGeom + epsilon))

		// Compute harmonic mean using inverse table
		// Skip unreliable boundaries, sample every 4th point
		const epsF32 = float32(1e-15)
		var unmask int
		for i := 12; i < len2-5; i += 4 {
			// Map energy to table index
			// For non-negative values, int(x) truncates toward zero which equals floor.
			// energy[i] + epsilon is always >= 0, so int() is equivalent to math.Floor.
			id := int(normE * (energy[i] + epsF32))
			if id > 127 {
				id = 127
			}
			unmask += transientInvTable[id]
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

	if deferMonoToneDetect {
		toneFreq, toneishness := toneDetectFloat32Mono(monoToneX, 48000, false)
		result.ToneFreq = toneFreq
		result.Toneishness = toneishness
	}

transientMetricsDone:
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
		maxMaskMetric = 0
	}

	// Weak transient handling for hybrid mode
	if allowWeakTransients && result.IsTransient && maxMaskMetric < 600 {
		result.IsTransient = false
		result.WeakTransient = true
	}
	result.MaskMetric = float64(maxMaskMetric)

	// Compute tf_estimate from mask_metric
	// tf_max = max(0, sqrt(27 * mask_metric) - 42)
	// tf_estimate = sqrt(max(0, 0.0069 * min(163, tf_max) - 0.139))
	// Avoid math.Max/math.Min calls -- use branchless clamping.
	tfMax := math.Sqrt(27*float64(maxMaskMetric)) - 42
	if tfMax < 0 {
		tfMax = 0
	}
	if tfMax > 163 {
		tfMax = 163
	}
	tfEstimateSquared := 0.0069*tfMax - 0.139
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
//  1. Using persistent HP filter state across frames for better attack detection
//  2. Tracking attack duration for multi-frame transient handling
//  3. Applying hysteresis to prevent rapid toggling
//  4. Adaptive thresholding based on signal level
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
			norm = float32(len2) / (geoMean + 1e-15)
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
// Useful for adaptive thresholding.
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
	return PatchTransientDecisionWithScratch(newE, oldE, nbEBands, start, end, channels, nil)
}

// PatchTransientDecisionWithScratch is PatchTransientDecision using caller-owned
// scratch for the spread-old-energy workspace.
func PatchTransientDecisionWithScratch(newE, oldE []float64, nbEBands, start, end, channels int, spreadOld []float64) bool {
	if len(newE) < end || len(oldE) < end {
		return false
	}

	// Apply an aggressive (-6 dB/Bark) spreading function to the old frame
	// to avoid false detection caused by irrelevant bands.
	// GCONST(1.0f) in libopus is 1.0 in the log-energy domain (corresponds to ~6dB).
	if len(spreadOld) < end {
		spreadOld = make([]float64, end)
	} else {
		spreadOld = spreadOld[:end]
	}

	if channels == 1 {
		spreadOld[start] = oldE[start]
		for i := start + 1; i < end; i++ {
			v := spreadOld[i-1] - 1.0
			if oldE[i] > v {
				v = oldE[i]
			}
			spreadOld[i] = v
		}
	} else {
		// Stereo: use max of left and right channel
		v := oldE[start]
		if oldE[start+nbEBands] > v {
			v = oldE[start+nbEBands]
		}
		spreadOld[start] = v
		for i := start + 1; i < end; i++ {
			v = oldE[i]
			if oldE[i+nbEBands] > v {
				v = oldE[i+nbEBands]
			}
			if prev := spreadOld[i-1] - 1.0; prev > v {
				v = prev
			}
			spreadOld[i] = v
		}
	}

	// Backward pass: spread from high to low frequencies
	for i := end - 2; i >= start; i-- {
		if v := spreadOld[i+1] - 1.0; v > spreadOld[i] {
			spreadOld[i] = v
		}
	}

	// Compute mean increase
	var meanDiff float64
	startBand := start
	if startBand < 2 {
		startBand = 2
	}

	for c := 0; c < channels; c++ {
		for i := startBand; i < end-1; i++ {
			x1 := newE[i+c*nbEBands]
			if x1 < 0 {
				x1 = 0
			}
			x2 := spreadOld[i]
			if x2 < 0 {
				x2 = 0
			}
			if diff := x1 - x2; diff > 0 {
				meanDiff += diff
			}
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
//  1. Inspect transient detection behavior against libopus
//  2. Adjust TF resolution for optimal attack preservation
//  3. Guide pre-echo reduction in VBR mode
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
