package silk

import "math"

// stereo_lp_filter.go implements LP/HP filtering for stereo mid/side channels.
// This matches libopus silk/stereo_LR_to_MS.c and silk/stereo_MS_to_LR.c.
//
// The LP filter is a 3-tap FIR with coefficients [1, 2, 1]/4:
//   LP[n] = (signal[n] + 2*signal[n+1] + signal[n+2] + 2) >> 2
//
// The HP component is the difference:
//   HP[n] = signal[n+1] - LP[n]
//
// This separation allows computing separate predictors for LP and HP bands,
// improving stereo prediction quality.

// stereoLPFilter applies the [1,2,1]/4 lowpass filter to a signal.
// Input signal must have length frameLength+2 (includes 2 history samples at start).
// Output LP has length frameLength.
// Returns HP as well: HP[n] = signal[n+1] - LP[n]
//
// This matches libopus stereo_LR_to_MS.c lines 77-92.
func stereoLPFilter(signal []int16, frameLength int) (lp, hp []int16) {
	lp = make([]int16, frameLength)
	hp = make([]int16, frameLength)

	for n := 0; n < frameLength; n++ {
		// sum = (signal[n] + 2*signal[n+1] + signal[n+2] + 2) >> 2
		// Using silk_ADD_LSHIFT32 pattern: (a + b<<shift)
		// sum = round((signal[n] + signal[n+2] + 2*signal[n+1]) / 4)
		sum := silkRSHIFT_ROUND(
			silkADD_LSHIFT32(int32(signal[n])+int32(signal[n+2]), int32(signal[n+1]), 1),
			2,
		)
		lp[n] = int16(sum)
		hp[n] = signal[n+1] - int16(sum)
	}

	return lp, hp
}

// stereoLPFilterFloat applies the [1,2,1]/4 lowpass filter to float32 signal.
// This is used for encoder-side analysis with float input.
// Input signal must have length frameLength+2 (includes 2 history samples at start).
func stereoLPFilterFloat(signal []float32, frameLength int) (lp, hp []float32) {
	lp = make([]float32, frameLength)
	hp = make([]float32, frameLength)

	for n := 0; n < frameLength; n++ {
		// LP[n] = (signal[n] + 2*signal[n+1] + signal[n+2]) / 4
		lpVal := (signal[n] + 2*signal[n+1] + signal[n+2]) / 4.0
		lp[n] = lpVal
		hp[n] = signal[n+1] - lpVal
	}

	return lp, hp
}

// stereoConvertLRToMS converts left/right signals to mid/side.
// Output mid and side arrays must have length frameLength+2 to hold history samples.
// This matches libopus stereo_LR_to_MS.c lines 62-68.
func stereoConvertLRToMS(left, right []int16, mid, side []int16, frameLength int) {
	for n := 0; n < frameLength+2; n++ {
		sum := int32(left[n]) + int32(right[n])
		diff := int32(left[n]) - int32(right[n])
		mid[n] = int16(silkRSHIFT_ROUND(sum, 1))
		side[n] = silkSAT16(silkRSHIFT_ROUND(diff, 1))
	}
}

// stereoConvertLRToMSFloat converts left/right float signals to mid/side.
// Output arrays have length frameLength+2 to include 2 samples of look-ahead.
func stereoConvertLRToMSFloat(left, right []float32, frameLength int) (mid, side []float32) {
	mid = make([]float32, frameLength+2)
	side = make([]float32, frameLength+2)

	for n := 0; n < frameLength+2; n++ {
		if n < len(left) && n < len(right) {
			mid[n] = (left[n] + right[n]) / 2
			side[n] = (left[n] - right[n]) / 2
		}
	}

	return mid, side
}

// stereoFindPredictor computes the least-squares predictor from basis (mid) to target (side).
// This matches libopus silk_stereo_find_predictor.
// Returns predictor in Q13 format and updates smoothed amplitude norms.
//
// Parameters:
//   - x: basis signal (LP or HP filtered mid)
//   - y: target signal (LP or HP filtered side)
//   - midResAmpQ0: [2]int32 holding smoothed [mid_norm, residual_norm]
//   - smoothCoefQ16: smoothing coefficient in Q16
//
// Returns:
//   - predQ13: predictor coefficient in Q13
//   - ratioQ14: ratio of residual to mid energies in Q14
// stereoFindPredictorFloat is the float version for encoder analysis.
func stereoFindPredictorFloat(x, y []float32, length int) (predQ13 int32) {
	// Compute energies and correlation
	var nrgx, nrgy, corr float64

	for i := 0; i < length; i++ {
		xi := float64(x[i])
		yi := float64(y[i])
		nrgx += xi * xi
		nrgy += yi * yi
		corr += xi * yi
	}

	if nrgx < 1e-10 {
		return 0
	}

	// Compute predictor
	pred := corr / nrgx

	// Convert to Q13 and clamp
	predQ13 = int32(pred * 8192)
	if predQ13 > (1 << 14) {
		predQ13 = 1 << 14
	}
	if predQ13 < -(1 << 14) {
		predQ13 = -(1 << 14)
	}

	return predQ13
}

// stereoFindPredictorFloatWithRatio computes the predictor and updates smoothed
// mid/residual amplitudes, returning the residual-to-mid ratio.
// This is a float-domain approximation of silk_stereo_find_predictor.
func stereoFindPredictorFloatWithRatio(x, y []float32, length int, midResAmp *[2]float64, smoothCoef float64) (predQ13 int32, ratio float64) {
	if length <= 0 {
		return 0, 0
	}

	var nrgx, nrgy, corr float64
	for i := 0; i < length; i++ {
		xi := float64(x[i])
		yi := float64(y[i])
		nrgx += xi * xi
		nrgy += yi * yi
		corr += xi * yi
	}

	if nrgx < 1e-10 {
		return 0, 0
	}

	pred := corr / nrgx
	if pred > 2.0 {
		pred = 2.0
	} else if pred < -2.0 {
		pred = -2.0
	}
	predQ13 = int32(pred * 8192.0)

	// Match libopus smoothing behavior: smoothCoef >= pred^2/64.
	pred2 := pred * pred
	if smoothCoef < pred2/64.0 {
		smoothCoef = pred2 / 64.0
	}
	if smoothCoef > 1.0 {
		smoothCoef = 1.0
	}

	// Smoothed mid and residual norms.
	midAmp := math.Sqrt(nrgx)
	resEnergy := nrgy - 2.0*pred*corr + pred2*nrgx
	if resEnergy < 0 {
		resEnergy = 0
	}
	resAmp := math.Sqrt(resEnergy)

	midResAmp[0] += smoothCoef * (midAmp - midResAmp[0])
	midResAmp[1] += smoothCoef * (resAmp - midResAmp[1])

	den := midResAmp[0]
	if den < 1e-9 {
		den = 1e-9
	}
	ratio = midResAmp[1] / den
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 2.0 {
		ratio = 2.0
	}

	return predQ13, ratio
}

// isqrt32 computes integer square root of a 32-bit unsigned integer.
func isqrt32(n uint32) uint32 {
	if n == 0 {
		return 0
	}

	// Newton's method
	x := n
	y := (x + 1) >> 1

	for y < x {
		x = y
		y = (x + n/x) >> 1
	}

	return x
}
