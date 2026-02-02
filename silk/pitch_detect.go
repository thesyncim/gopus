package silk

import "math"

// Pitch estimation constants from libopus pitch_est_defines.h
const (
	peMaxNbSubfr      = 4
	peSubfrLengthMS   = 5
	peLTPMemLengthMS  = 20 // 4 * PE_SUBFR_LENGTH_MS
	peMaxLagMS        = 18 // 18 ms -> 56 Hz
	peMinLagMS        = 2  // 2 ms -> 500 Hz
	peDSrchLength     = 24
	peNbStage3Lags    = 5
	peNbCbksStage2    = 3
	peNbCbksStage2Ext = 11
	peNbCbksStage3Max = 34
	peNbCbksStage3Mid = 24
	peNbCbksStage3Min = 16
	peNbCbksStage310ms = 12
	peNbCbksStage210ms = 3

	// Aliases for libopus_decode.go compatibility
	peMinLagMs          = peMinLagMS
	peMaxLagMs          = peMaxLagMS
	peNbCbksStage2_10ms = peNbCbksStage210ms
	peNbCbksStage3_10ms = peNbCbksStage310ms

	// Bias constants from libopus
	peShortlagBias    = 0.2  // for logarithmic weighting
	pePrevlagBias     = 0.2  // for logarithmic weighting
	peFlatcontourBias = 0.05
)

// Pitch contour codebooks for stage 2 (matching libopus silk_CB_lags_stage2)
var pitchCBLagsStage2 = [peMaxNbSubfr][peNbCbksStage2Ext]int8{
	{0, 2, -1, -1, -1, 0, 0, 1, 1, 0, 1},
	{0, 1, 0, 0, 0, 0, 0, 1, 0, 0, 0},
	{0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 0},
	{0, -1, 2, 1, 0, 1, 1, 0, 0, -1, -1},
}

// Pitch contour codebooks for stage 3 (matching libopus silk_CB_lags_stage3)
var pitchCBLagsStage3 = [peMaxNbSubfr][peNbCbksStage3Max]int8{
	{0, 0, 1, -1, 0, 1, -1, 0, -1, 1, -2, 2, -2, -2, 2, -3, 2, 3, -3, -4, 3, -4, 4, 4, -5, 5, -6, -5, 6, -7, 6, 5, 8, -9},
	{0, 0, 1, 0, 0, 0, 0, 0, 0, 0, -1, 1, 0, 0, 1, -1, 0, 1, -1, -1, 1, -1, 2, 1, -1, 2, -2, -2, 2, -2, 2, 2, 3, -3},
	{0, 1, 0, 0, 0, 0, 0, 0, 1, 0, 1, 0, 0, 1, -1, 1, 0, 0, 2, 1, -1, 2, -1, -1, 2, -1, 2, 2, -1, 3, -2, -2, -2, 3},
	{0, 1, 0, 0, 1, 0, 1, -1, 2, -1, 2, -1, 2, 3, -2, 3, -2, -2, 4, 4, -3, 5, -3, -4, 6, -4, 6, 5, -5, 8, -6, -5, -7, 9},
}

// Lag range for stage 3 by complexity (matching libopus silk_Lag_range_stage3)
var pitchLagRangeStage3 = [3][peMaxNbSubfr][2]int8{
	// Low complexity
	{{-5, 8}, {-1, 6}, {-1, 6}, {-4, 10}},
	// Mid complexity
	{{-6, 10}, {-2, 6}, {-1, 6}, {-5, 10}},
	// Max complexity
	{{-9, 12}, {-3, 7}, {-2, 7}, {-7, 13}},
}

// Number of codebook searches per stage 3 complexity
var pitchNbCbkSearchsStage3 = [3]int{peNbCbksStage3Min, peNbCbksStage3Mid, peNbCbksStage3Max}

// Pitch contour codebooks for 10ms frames (stage 2)
var pitchCBLagsStage210ms = [2][peNbCbksStage210ms]int8{
	{0, 1, 0},
	{0, 0, 1},
}

// Pitch contour codebooks for 10ms frames (stage 3)
var pitchCBLagsStage310ms = [2][peNbCbksStage310ms]int8{
	{0, 0, 1, -1, 1, -1, 2, -2, 2, -2, 3, -3},
	{0, 1, 0, 1, -1, 2, -1, 2, -2, 3, -2, 3},
}

// Lag range for stage 3 10ms frames
var pitchLagRangeStage310ms = [2][2]int8{
	{-3, 7},
	{-2, 7},
}

// PitchAnalysisState holds state for pitch analysis across frames
type PitchAnalysisState struct {
	prevLag     int     // Previous frame's pitch lag
	ltpCorr     float64 // LTP correlation from previous frame
	complexity  int     // Complexity setting (0-2)
}

// detectPitch performs three-stage coarse-to-fine pitch detection matching libopus.
// Returns pitch lags for each subframe (voiced frames only).
//
// Per libopus pitch_analysis_core_FLP.c:
// Stage 1: Coarse search at 4kHz with normalized autocorrelation
// Stage 2: Refined search at 8kHz with contour codebook
// Stage 3: Fine search at full rate with interpolation
func (e *Encoder) detectPitch(pcm []float32, numSubframes int) []int {
	config := GetBandwidthConfig(e.bandwidth)
	fsKHz := config.SampleRate / 1000

	// Frame length parameters matching libopus
	frameLength := (peLTPMemLengthMS + numSubframes*peSubfrLengthMS) * fsKHz
	frameLength4kHz := (peLTPMemLengthMS + numSubframes*peSubfrLengthMS) * 4
	frameLength8kHz := (peLTPMemLengthMS + numSubframes*peSubfrLengthMS) * 8
	sfLength := peSubfrLengthMS * fsKHz
	sfLength4kHz := peSubfrLengthMS * 4
	sfLength8kHz := peSubfrLengthMS * 8
	minLag := peMinLagMS * fsKHz
	minLag4kHz := peMinLagMS * 4
	minLag8kHz := peMinLagMS * 8
	maxLag := peMaxLagMS*fsKHz - 1
	maxLag4kHz := peMaxLagMS * 4
	maxLag8kHz := peMaxLagMS*8 - 1

	// Ensure we have enough samples
	if len(pcm) < frameLength {
		frameLength = len(pcm)
	}

	// Resample to 8kHz using scratch buffer
	dsRatio8k := fsKHz / 8
	if dsRatio8k < 1 {
		dsRatio8k = 1
	}
	frame8kHz := downsampleLowpassInto(pcm[:frameLength], dsRatio8k, &e.scratchFrame8kHz)

	// Ensure frame8kHz has correct length
	if len(frame8kHz) > frameLength8kHz {
		frame8kHz = frame8kHz[:frameLength8kHz]
	}

	// Decimate to 4kHz using scratch buffer
	frame4kHz := downsampleLowpassInto(frame8kHz, 2, &e.scratchFrame4kHz)
	if len(frame4kHz) > frameLength4kHz {
		frame4kHz = frame4kHz[:frameLength4kHz]
	}

	// Apply simple low-pass filter to 4kHz signal
	for i := len(frame4kHz) - 1; i > 0; i-- {
		frame4kHz[i] = frame4kHz[i] + frame4kHz[i-1]
	}

	// Stage 1: Coarse search at 4kHz using scratch buffer
	C := ensureFloat64Slice(&e.scratchPitchC, maxLag4kHz+5)
	for i := range C {
		C[i] = 0 // Clear
	}

	targetStart := sfLength4kHz * 4 // Start after LTP memory
	if targetStart >= len(frame4kHz) {
		targetStart = len(frame4kHz) / 2
	}

	// Compute normalized autocorrelation at 4kHz
	// Process pairs of subframes for stage 1
	for k := 0; k < numSubframes/2; k++ {
		targetIdx := targetStart + k*sfLength8kHz
		if targetIdx+sfLength8kHz > len(frame4kHz) {
			break
		}
		target := frame4kHz[targetIdx : targetIdx+sfLength8kHz]

		// Compute energy of target
		var targetEnergy float64
		for _, s := range target {
			targetEnergy += float64(s) * float64(s)
		}

		// Search all lags with recursive energy update
		basisIdx := targetIdx - minLag4kHz
		if basisIdx < 0 {
			basisIdx = 0
		}

		// Initial energy at minimum lag
		var basisEnergy float64
		for i := 0; i < sfLength8kHz && basisIdx+i < len(frame4kHz); i++ {
			basisEnergy += float64(frame4kHz[basisIdx+i]) * float64(frame4kHz[basisIdx+i])
		}

		// Compute normalizer
		// The bias term 4000.0 is for int16 signals (range ±32768).
		// For float32 signals (range ±1.0), scale by 1/(32768^2) ≈ 9.3e-10.
		// Use a small constant to prevent division by zero.
		normalizer := targetEnergy + basisEnergy + 0.001

		// Compute initial cross-correlation
		var xcorr float64
		for i := 0; i < sfLength8kHz && targetIdx+i < len(frame4kHz) && basisIdx+i < len(frame4kHz); i++ {
			xcorr += float64(target[i]) * float64(frame4kHz[basisIdx+i])
		}
		if minLag4kHz < len(C) {
			C[minLag4kHz] += 2 * xcorr / normalizer
		}

		// Recursive update for remaining lags
		for d := minLag4kHz + 1; d <= maxLag4kHz; d++ {
			basisIdx--
			if basisIdx < 0 {
				break
			}

			// Recompute cross-correlation for this lag
			xcorr = 0
			for i := 0; i < sfLength8kHz && targetIdx+i < len(frame4kHz) && basisIdx+i < len(frame4kHz); i++ {
				xcorr += float64(target[i]) * float64(frame4kHz[basisIdx+i])
			}

			// Update energy recursively
			if basisIdx >= 0 && basisIdx+sfLength8kHz < len(frame4kHz) {
				basisEnergy += float64(frame4kHz[basisIdx])*float64(frame4kHz[basisIdx]) -
					float64(frame4kHz[basisIdx+sfLength8kHz])*float64(frame4kHz[basisIdx+sfLength8kHz])
			}
			normalizer = targetEnergy + basisEnergy + 0.001

			if d < len(C) {
				C[d] += 2 * xcorr / normalizer
			}
		}
	}

	// Apply short-lag bias (matching libopus)
	for i := maxLag4kHz; i >= minLag4kHz; i-- {
		if i < len(C) {
			C[i] -= C[i] * float64(i) / 4096.0
		}
	}

	// Find top candidates using insertion sort
	complexity := 2 // Use maximum complexity
	lengthDSrch := 4 + 2*complexity
	if lengthDSrch > peDSrchLength {
		lengthDSrch = peDSrchLength
	}

	dSrch := ensureIntSlice(&e.scratchDSrch, lengthDSrch)
	dSrchCorr := ensureFloat64Slice(&e.scratchDSrchCorr, lengthDSrch)
	for i := range dSrch {
		dSrch[i] = 0
	}
	for i := range dSrchCorr {
		dSrchCorr[i] = -1e10
	}

	// Insertion sort to find top candidates
	for d := minLag4kHz; d <= maxLag4kHz && d < len(C); d++ {
		if C[d] > dSrchCorr[lengthDSrch-1] {
			// Insert this candidate
			for j := 0; j < lengthDSrch; j++ {
				if C[d] > dSrchCorr[j] {
					// Shift everything down
					copy(dSrchCorr[j+1:], dSrchCorr[j:lengthDSrch-1])
					copy(dSrch[j+1:], dSrch[j:lengthDSrch-1])
					dSrchCorr[j] = C[d]
					dSrch[j] = d
					break
				}
			}
		}
	}

	// Check if correlation is too low
	Cmax := dSrchCorr[0]
	if Cmax < 0.2 {
		// Unvoiced - return minimum lags using scratch buffer
		pitchLags := ensureIntSlice(&e.scratchPitchLags, numSubframes)
		for i := range pitchLags {
			pitchLags[i] = minLag
		}
		return pitchLags
	}

	// Threshold for candidate selection
	searchThres1 := 0.8 - 0.5*float64(complexity)/2.0
	threshold := searchThres1 * Cmax

	// Convert to 8kHz indices and expand search range
	dComp := ensureInt16Slice(&e.scratchDComp, maxLag8kHz+5)
	for i := range dComp {
		dComp[i] = 0 // Clear
	}
	validDSrch := 0
	for i := 0; i < lengthDSrch; i++ {
		if dSrchCorr[i] > threshold {
			idx := dSrch[i]*2 // Convert to 8kHz
			if idx >= minLag8kHz && idx <= maxLag8kHz {
				dComp[idx] = 1
			}
			validDSrch++
		}
	}

	// Convolution to expand search range
	for i := maxLag8kHz + 3; i >= minLag8kHz; i-- {
		if i < len(dComp) && i-1 >= 0 && i-2 >= 0 {
			dComp[i] += dComp[i-1] + dComp[i-2]
		}
	}

	// Collect expanded search lags
	lengthDSrch = 0
	for i := minLag8kHz; i <= maxLag8kHz; i++ {
		if i+1 < len(dComp) && dComp[i+1] > 0 {
			if lengthDSrch < len(dSrch) {
				dSrch[lengthDSrch] = i
				lengthDSrch++
			}
		}
	}

	// Stage 2: Refined search at 8kHz
	// Use flat array: C8kHz[k][d] -> C8kHz[k*c8kHzStride + d]
	c8kHzStride := maxLag8kHz + 5
	C8kHz := ensureFloat64Slice(&e.scratchC8kHz, numSubframes*c8kHzStride)
	for i := range C8kHz {
		C8kHz[i] = 0 // Clear
	}

	targetStart8k := peLTPMemLengthMS * 8
	if fsKHz == 8 {
		targetStart8k = peLTPMemLengthMS * 8
	}

	for k := 0; k < numSubframes; k++ {
		targetIdx := targetStart8k + k*sfLength8kHz
		if targetIdx+sfLength8kHz > len(frame8kHz) {
			break
		}
		target := frame8kHz[targetIdx : targetIdx+sfLength8kHz]

		var targetEnergy float64
		for _, s := range target {
			targetEnergy += float64(s) * float64(s)
		}
		targetEnergy += 1.0 // Avoid division by zero

		for j := 0; j < lengthDSrch; j++ {
			d := dSrch[j]
			basisIdx := targetIdx - d
			if basisIdx < 0 || basisIdx+sfLength8kHz > len(frame8kHz) {
				continue
			}
			basis := frame8kHz[basisIdx : basisIdx+sfLength8kHz]

			var xcorr, basisEnergy float64
			for i := 0; i < sfLength8kHz; i++ {
				xcorr += float64(target[i]) * float64(basis[i])
				basisEnergy += float64(basis[i]) * float64(basis[i])
			}

			if xcorr > 0 && d < c8kHzStride {
				C8kHz[k*c8kHzStride+d] = 2 * xcorr / (targetEnergy + basisEnergy)
			}
		}
	}

	// Search over lag range and contour codebook
	var CCmax float64 = -1000
	CCmaxB := -1000.0
	CBimax := 0
	lag := -1

	// Previous lag handling
	prevLag := e.pitchState.prevLag
	var prevLagLog2 float64
	if prevLag > 0 {
		prevLag8k := prevLag
		if fsKHz == 12 {
			prevLag8k = prevLag * 2 / 3
		} else if fsKHz == 16 {
			prevLag8k = prevLag / 2
		}
		prevLagLog2 = math.Log2(float64(prevLag8k))
	}

	// Determine number of codebook searches
	var cbkSize, nbCbkSearch int
	var lagCBPtr *[peMaxNbSubfr][peNbCbksStage2Ext]int8
	if numSubframes == peMaxNbSubfr {
		cbkSize = peNbCbksStage2Ext
		lagCBPtr = &pitchCBLagsStage2
		if fsKHz == 8 && complexity > 0 {
			nbCbkSearch = peNbCbksStage2Ext
		} else {
			nbCbkSearch = peNbCbksStage2
		}
	} else {
		cbkSize = peNbCbksStage210ms
		nbCbkSearch = peNbCbksStage210ms
	}

	searchThres2 := 0.4 - 0.25*float64(complexity)/2.0

	for k := 0; k < lengthDSrch; k++ {
		d := dSrch[k]

		// Sum correlation across subframes for each codebook entry
		for j := 0; j < nbCbkSearch; j++ {
			var CC float64
			for i := 0; i < numSubframes; i++ {
				var cbOffset int8
				if lagCBPtr != nil && i < peMaxNbSubfr && j < cbkSize {
					cbOffset = lagCBPtr[i][j]
				}
				idx := d + int(cbOffset)
				if idx >= 0 && idx < c8kHzStride {
					CC += C8kHz[i*c8kHzStride+idx]
				}
			}

			// Find best codebook
			if CC > CCmax {
				CCmax = CC
				CBimax = j
				lag = d
			}
		}
	}

	// Apply biases
	if lag > 0 {
		lagLog2 := math.Log2(float64(lag))
		CCmaxNewB := CCmax - peShortlagBias*float64(numSubframes)*lagLog2

		// Bias toward previous lag
		if prevLag > 0 {
			deltaLagLog2Sqr := lagLog2 - prevLagLog2
			deltaLagLog2Sqr *= deltaLagLog2Sqr
			CCmaxNewB -= pePrevlagBias * float64(numSubframes) * e.pitchState.ltpCorr *
				deltaLagLog2Sqr / (deltaLagLog2Sqr + 0.5)
		}

		if CCmaxNewB > CCmaxB && CCmax > float64(numSubframes)*searchThres2 {
			CCmaxB = CCmaxNewB
		} else if lag == -1 {
			// No suitable candidate - use scratch buffer
			pitchLags := ensureIntSlice(&e.scratchPitchLags, numSubframes)
			for i := range pitchLags {
				pitchLags[i] = minLag
			}
			return pitchLags
		}
	}

	// Update LTP correlation
	if lag > 0 {
		e.pitchState.ltpCorr = CCmax / float64(numSubframes)
	}

	// Stage 3: Fine search at full rate (if not 8kHz) - use scratch buffer
	pitchLags := ensureIntSlice(&e.scratchPitchLags, numSubframes)

	if fsKHz > 8 && lag > 0 {
		// Convert lag to full rate
		if fsKHz == 12 {
			lag = (lag*3 + 1) / 2
		} else if fsKHz == 16 {
			lag = lag * 2
		}

		// Clamp to valid range
		if lag < minLag {
			lag = minLag
		}
		if lag > maxLag {
			lag = maxLag
		}

		// Search range for stage 3
		startLag := lag - 2
		if startLag < minLag {
			startLag = minLag
		}
		endLag := lag + 2
		if endLag > maxLag {
			endLag = maxLag
		}

		// Get contour codebook parameters for stage 3
		nbCbkSearch3 := pitchNbCbkSearchsStage3[complexity]
		lagRangePtr := &pitchLagRangeStage3[complexity]

		// Fine search with Lagrangian interpolation
		var bestLag int
		var bestCC float64 = -1000

		targetStartFull := peLTPMemLengthMS * fsKHz

		for d := startLag; d <= endLag; d++ {
			for j := 0; j < nbCbkSearch3; j++ {
				var crossCorr, energy float64

				for k := 0; k < numSubframes; k++ {
					cbOffset := pitchCBLagsStage3[k][j]
					lagK := d + int(cbOffset)

					// Check if within valid lag range for this subframe
					if k < peMaxNbSubfr {
						lagLow := int(lagRangePtr[k][0])
						lagHigh := int(lagRangePtr[k][1])
						if int(cbOffset) < lagLow || int(cbOffset) > lagHigh {
							continue
						}
					}

					if lagK < minLag || lagK > maxLag {
						continue
					}

					targetIdx := targetStartFull + k*sfLength
					if targetIdx+sfLength > len(pcm) {
						break
					}
					target := pcm[targetIdx : targetIdx+sfLength]

					basisIdx := targetIdx - lagK
					if basisIdx < 0 || basisIdx+sfLength > len(pcm) {
						continue
					}
					basis := pcm[basisIdx : basisIdx+sfLength]

					for i := 0; i < sfLength; i++ {
						crossCorr += float64(target[i]) * float64(basis[i])
						energy += float64(basis[i]) * float64(basis[i])
					}
				}

				// Compute normalized correlation with contour bias
				if crossCorr > 0 && energy > 0 {
					var targetEnergy float64
					for k := 0; k < numSubframes; k++ {
						targetIdx := targetStartFull + k*sfLength
						if targetIdx+sfLength > len(pcm) {
							break
						}
						for i := 0; i < sfLength; i++ {
							targetEnergy += float64(pcm[targetIdx+i]) * float64(pcm[targetIdx+i])
						}
					}
					CC := 2 * crossCorr / (energy + targetEnergy + 1.0)

					// Apply flat contour bias
					contourBias := peFlatcontourBias / float64(d)
					CC *= 1.0 - contourBias*float64(j)

					if CC > bestCC && (d+int(pitchCBLagsStage3[0][j])) <= maxLag {
						bestCC = CC
						bestLag = d
						CBimax = j
					}
				}
			}
		}

		lag = bestLag

		// Compute final pitch lags using contour
		for k := 0; k < numSubframes; k++ {
			cbOffset := pitchCBLagsStage3[k][CBimax]
			pitchLags[k] = lag + int(cbOffset)

			// Clamp to valid range
			if pitchLags[k] < minLag {
				pitchLags[k] = minLag
			}
			if pitchLags[k] > maxLag {
				pitchLags[k] = maxLag
			}
		}
	} else {
		// 8kHz: use stage 2 results directly
		for k := 0; k < numSubframes; k++ {
			var cbOffset int8
			if lagCBPtr != nil && k < peMaxNbSubfr && CBimax < cbkSize {
				cbOffset = lagCBPtr[k][CBimax]
			}
			pitchLags[k] = lag + int(cbOffset)

			// Clamp to valid range
			if pitchLags[k] < minLag8kHz {
				pitchLags[k] = minLag8kHz
			}
			if pitchLags[k] > maxLag8kHz {
				pitchLags[k] = maxLag8kHz
			}
		}
	}

	// Update state for next frame
	e.pitchState.prevLag = lag

	return pitchLags
}

// downsampleLowpass performs downsampling with a simple low-pass filter.
// This matches libopus's approach of using a 3-tap filter for anti-aliasing.
func downsampleLowpass(signal []float32, factor int) []float32 {
	if factor <= 1 {
		return signal
	}

	n := len(signal) / factor
	ds := make([]float32, n)
	downsampleLowpassToBuffer(signal, factor, ds)
	return ds
}

// downsampleLowpassInto performs downsampling using a scratch buffer.
// Zero-allocation version.
func downsampleLowpassInto(signal []float32, factor int, dsBuf *[]float32) []float32 {
	if factor <= 1 {
		return signal
	}

	n := len(signal) / factor
	ds := ensureFloat32Slice(dsBuf, n)
	downsampleLowpassToBuffer(signal, factor, ds)
	return ds
}

// downsampleLowpassToBuffer performs the actual downsampling into a provided buffer.
func downsampleLowpassToBuffer(signal []float32, factor int, ds []float32) {
	n := len(ds)
	// 3-tap low-pass filter: [0.25, 0.5, 0.25]
	offset := factor / 2
	for i := 0; i < n; i++ {
		idx := i * factor
		if i == 0 {
			// First sample: use available samples
			ds[i] = 0.5*signal[idx] + 0.5*signal[idx+offset]
		} else if idx+offset < len(signal) && idx-offset >= 0 {
			ds[i] = 0.25*signal[idx-offset] + 0.5*signal[idx] + 0.25*signal[idx+offset]
		} else {
			ds[i] = signal[idx]
		}
	}
}

// downsample reduces sample rate by averaging factor samples.
// Kept for backward compatibility with existing tests.
func downsample(signal []float32, factor int) []float32 {
	if factor <= 1 {
		return signal
	}

	n := len(signal) / factor
	ds := make([]float32, n)

	for i := 0; i < n; i++ {
		var sum float32
		for j := 0; j < factor; j++ {
			sum += signal[i*factor+j]
		}
		ds[i] = sum / float32(factor)
	}

	return ds
}

// autocorrPitchSearch finds best pitch lag using normalized autocorrelation.
// Uses bias toward shorter lags to avoid octave errors.
// Kept for backward compatibility with existing tests.
func autocorrPitchSearch(signal []float32, minLag, maxLag int) int {
	n := len(signal)
	if maxLag >= n {
		maxLag = n - 1
	}
	if minLag < 1 {
		minLag = 1
	}
	if minLag > maxLag {
		return minLag
	}

	bestLag := minLag
	var bestCorr float64 = -1

	for lag := minLag; lag <= maxLag; lag++ {
		var corr, energy1, energy2 float64
		for i := lag; i < n; i++ {
			corr += float64(signal[i]) * float64(signal[i-lag])
			energy1 += float64(signal[i]) * float64(signal[i])
			energy2 += float64(signal[i-lag]) * float64(signal[i-lag])
		}

		if energy1 < 1e-10 || energy2 < 1e-10 {
			continue
		}

		// Normalized correlation
		normCorr := corr / math.Sqrt(energy1*energy2)

		// Bias toward shorter lags to avoid octave errors
		// Per draft-vos-silk-01 Section 2.1.2.5
		normCorr *= 1.0 - 0.001*float64(lag-minLag)

		if normCorr > bestCorr {
			bestCorr = normCorr
			bestLag = lag
		}
	}

	return bestLag
}

// autocorrPitchSearchSubframe searches for pitch in a subframe.
// Uses preceding samples for lookback.
// Kept for backward compatibility with existing tests.
func autocorrPitchSearchSubframe(subframe, fullSignal []float32, subframeStart, minLag, maxLag int) int {
	n := len(subframe)
	if maxLag >= subframeStart {
		maxLag = subframeStart - 1
	}
	if minLag < 1 {
		minLag = 1
	}
	if minLag > maxLag {
		return minLag
	}

	bestLag := minLag
	var bestCorr float64 = -1

	for lag := minLag; lag <= maxLag; lag++ {
		var corr, energy1, energy2 float64
		for i := 0; i < n && subframeStart-lag+i >= 0; i++ {
			s := float64(subframe[i])
			past := float64(fullSignal[subframeStart-lag+i])
			corr += s * past
			energy1 += s * s
			energy2 += past * past
		}

		if energy1 < 1e-10 || energy2 < 1e-10 {
			continue
		}

		normCorr := corr / math.Sqrt(energy1*energy2)
		normCorr *= 1.0 - 0.001*float64(lag-minLag)

		if normCorr > bestCorr {
			bestCorr = normCorr
			bestLag = lag
		}
	}

	return bestLag
}

// lagrangianInterpolate performs Lagrangian interpolation for sub-sample pitch refinement.
// Given correlation values at lags [d-1, d, d+1], returns fractional offset.
// This matches libopus's approach in remove_doubling/pitch_search.
func lagrangianInterpolate(corrMinus, corrCenter, corrPlus float64) float64 {
	// Quadratic interpolation to find sub-sample offset
	// offset = 0.5 * (corrMinus - corrPlus) / (corrMinus - 2*corrCenter + corrPlus)
	denom := corrMinus - 2*corrCenter + corrPlus
	if math.Abs(denom) < 1e-10 {
		return 0
	}
	offset := 0.5 * (corrMinus - corrPlus) / denom

	// Clamp to [-0.5, 0.5]
	if offset < -0.5 {
		offset = -0.5
	}
	if offset > 0.5 {
		offset = 0.5
	}
	return offset
}

// computePitchCorrelation computes normalized correlation at a specific lag.
// Used for sub-sample interpolation and voicing detection.
func computePitchCorrelation(target, basis []float32, lag int) (xcorr, targetEnergy, basisEnergy float64) {
	n := len(target)
	if lag > len(basis) {
		return 0, 0, 0
	}

	for i := 0; i < n && i+lag < len(basis); i++ {
		xcorr += float64(target[i]) * float64(basis[i])
		targetEnergy += float64(target[i]) * float64(target[i])
		basisEnergy += float64(basis[i]) * float64(basis[i])
	}
	return
}

// energyFLP computes sum of squares of a float32 array.
// Matches libopus silk_energy_FLP.
func energyFLP(data []float32) float64 {
	var result float64

	// 4x unrolled loop for performance
	n := len(data)
	i := 0
	for ; i < n-3; i += 4 {
		result += float64(data[i+0])*float64(data[i+0]) +
			float64(data[i+1])*float64(data[i+1]) +
			float64(data[i+2])*float64(data[i+2]) +
			float64(data[i+3])*float64(data[i+3])
	}

	// Handle remaining samples
	for ; i < n; i++ {
		result += float64(data[i]) * float64(data[i])
	}

	return result
}

// innerProductFLP computes inner product of two float32 arrays.
// Matches libopus silk_inner_product_FLP.
func innerProductFLP(a, b []float32, length int) float64 {
	var result float64

	// 4x unrolled loop for performance
	i := 0
	for ; i < length-3; i += 4 {
		result += float64(a[i+0])*float64(b[i+0]) +
			float64(a[i+1])*float64(b[i+1]) +
			float64(a[i+2])*float64(b[i+2]) +
			float64(a[i+3])*float64(b[i+3])
	}

	// Handle remaining samples
	for ; i < length; i++ {
		result += float64(a[i]) * float64(b[i])
	}

	return result
}

// encodePitchLags encodes pitch lags to the bitstream.
// First subframe is absolute, subsequent are delta-coded via contour.
// Per RFC 6716 Section 4.2.7.6.
// Uses libopus tables: silk_pitch_lag_iCDF, silk_uniform4/6/8_iCDF, silk_pitch_contour*_iCDF
func (e *Encoder) encodePitchLags(pitchLags []int, numSubframes int) {
	config := GetBandwidthConfig(e.bandwidth)
	fsKHz := config.SampleRate / 1000

	// Select pitch contour table based on bandwidth and frame size
	// Use scratch buffer for pitchContour
	// Per libopus control_codec.c:
	//   NB (8kHz): silk_pitch_contour_NB_iCDF (20ms) or silk_pitch_contour_10_ms_NB_iCDF (10ms)
	//   MB/WB:     silk_pitch_contour_iCDF (20ms) or silk_pitch_contour_10_ms_iCDF (10ms)
	var pitchContour [][4]int8
	var contourICDF []uint8

	switch e.bandwidth {
	case BandwidthNarrowband:
		if numSubframes == 4 {
			pitchContour = ensure2DInt8Slice(&e.scratchPitchContour, len(PitchContourNB20ms))
			for i := range PitchContourNB20ms {
				pitchContour[i] = PitchContourNB20ms[i]
			}
			contourICDF = silk_pitch_contour_NB_iCDF
		} else {
			// Convert [16][2]int8 to [][4]int8 for 10ms frames
			pitchContour = ensure2DInt8Slice(&e.scratchPitchContour, len(PitchContourNB10ms))
			for i := range PitchContourNB10ms {
				pitchContour[i] = [4]int8{PitchContourNB10ms[i][0], PitchContourNB10ms[i][1], 0, 0}
			}
			contourICDF = silk_pitch_contour_10_ms_NB_iCDF
		}
	default: // Mediumband or Wideband
		if numSubframes == 4 {
			if fsKHz == 12 {
				pitchContour = ensure2DInt8Slice(&e.scratchPitchContour, len(PitchContourMB20ms))
				for i := range PitchContourMB20ms {
					pitchContour[i] = PitchContourMB20ms[i]
				}
			} else {
				pitchContour = ensure2DInt8Slice(&e.scratchPitchContour, len(PitchContourWB20ms))
				for i := range PitchContourWB20ms {
					pitchContour[i] = PitchContourWB20ms[i]
				}
			}
			contourICDF = silk_pitch_contour_iCDF
		} else {
			if fsKHz == 12 {
				pitchContour = ensure2DInt8Slice(&e.scratchPitchContour, len(PitchContourMB10ms))
				for i := range PitchContourMB10ms {
					pitchContour[i] = [4]int8{PitchContourMB10ms[i][0], PitchContourMB10ms[i][1], 0, 0}
				}
			} else {
				pitchContour = ensure2DInt8Slice(&e.scratchPitchContour, len(PitchContourWB10ms))
				for i := range PitchContourWB10ms {
					pitchContour[i] = [4]int8{PitchContourWB10ms[i][0], PitchContourWB10ms[i][1], 0, 0}
				}
			}
			contourICDF = silk_pitch_contour_10_ms_iCDF
		}
	}

	// Find best matching contour and base lag
	contourIdx, baseLag := e.findBestPitchContour(pitchLags, pitchContour, numSubframes)

	// Encode absolute lag for first subframe
	// Per libopus silk/encode_indices.c:
	//   pitch_high_bits = lagIndex / (fs_kHz >> 1)
	//   pitch_low_bits  = lagIndex % (fs_kHz >> 1)
	// Uses silk_pitch_lag_iCDF (unified table, 32 entries) for high bits
	// Uses bandwidth-specific uniform ICDF for low bits
	lagIdx := baseLag - config.PitchLagMin
	if lagIdx < 0 {
		lagIdx = 0
	}

	// Divisor is sample rate in kHz divided by 2
	// (fsKHz already computed above for contour selection)
	divisor := fsKHz / 2 // 8 for 16kHz, 6 for 12kHz, 4 for 8kHz
	if divisor < 1 {
		divisor = 1
	}

	lagHigh := lagIdx / divisor
	lagLow := lagIdx % divisor

	// Clamp high bits to valid range (0-31 for silk_pitch_lag_iCDF)
	if lagHigh > 31 {
		lagHigh = 31
	}

	// Select low bits ICDF based on sample rate (matches decoder's pitchLagLowBitsICDF)
	var lagLowICDF []uint8
	switch fsKHz {
	case 16:
		lagLowICDF = silk_uniform8_iCDF
	case 12:
		lagLowICDF = silk_uniform6_iCDF
	default: // 8kHz
		lagLowICDF = silk_uniform4_iCDF
	}

	// Clamp low bits to valid range
	maxLowIdx := len(lagLowICDF) - 1
	if lagLow > maxLowIdx {
		lagLow = maxLowIdx
	}

	// Encode using libopus tables
	e.rangeEncoder.EncodeICDF(lagHigh, silk_pitch_lag_iCDF, 8)
	e.rangeEncoder.EncodeICDF(lagLow, lagLowICDF, 8)

	// Encode contour index for delta pattern
	maxContourIdx := len(contourICDF) - 1
	if contourIdx > maxContourIdx {
		contourIdx = maxContourIdx
	}
	e.rangeEncoder.EncodeICDF(contourIdx, contourICDF, 8)
}

// findBestPitchContour finds the contour that best matches pitch lag pattern.
// Returns contour index and base lag.
func (e *Encoder) findBestPitchContour(pitchLags []int, contours [][4]int8, numSubframes int) (int, int) {
	// Find mean lag
	var sumLag int
	for _, lag := range pitchLags {
		sumLag += lag
	}
	baseLag := sumLag / len(pitchLags)

	// Find best matching contour
	bestContour := 0
	bestDist := math.MaxInt32

	for cIdx := 0; cIdx < len(contours); cIdx++ {
		contour := contours[cIdx]

		var dist int
		for sf := 0; sf < numSubframes && sf < len(pitchLags); sf++ {
			predicted := baseLag + int(contour[sf])
			diff := pitchLags[sf] - predicted
			dist += diff * diff
		}

		if dist < bestDist {
			bestDist = dist
			bestContour = cIdx
		}
	}

	return bestContour, baseLag
}

// pitchMax returns the larger of a and b.
func pitchMax(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// pitchMin returns the smaller of a and b.
func pitchMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}
