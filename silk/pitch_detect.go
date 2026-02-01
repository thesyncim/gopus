package silk

import "math"

// detectPitch performs three-stage coarse-to-fine pitch detection.
// Returns pitch lags for each subframe (voiced frames only).
//
// Per draft-vos-silk-01 Section 2.1.2.5:
// Stage 1: Coarse search at 4kHz (1/4 rate for WB, 1/2 for NB/MB)
// Stage 2: Refined search at 8kHz (1/2 rate for WB)
// Stage 3: Fine search at full rate per subframe
func (e *Encoder) detectPitch(pcm []float32, numSubframes int) []int {
	config := GetBandwidthConfig(e.bandwidth)
	subframeSamples := len(pcm) / numSubframes

	// Stage 1: Coarse search at 4kHz (downsample by 4 for WB, 2 for NB/MB)
	dsRatio := config.SampleRate / 4000
	if dsRatio < 1 {
		dsRatio = 1
	}
	ds4k := downsample(pcm, dsRatio)

	coarseLagMin := config.PitchLagMin / dsRatio
	coarseLagMax := config.PitchLagMax / dsRatio
	coarseLag := autocorrPitchSearch(ds4k, coarseLagMin, coarseLagMax)

	// Stage 2: Refined search at 8kHz
	dsRatio2 := config.SampleRate / 8000
	if dsRatio2 < 1 {
		dsRatio2 = 1
	}
	ds8k := downsample(pcm, dsRatio2)

	// Search around coarse lag (+/- 4 samples at 8kHz)
	midLagMin := pitchMax(config.PitchLagMin/dsRatio2, (coarseLag*dsRatio/dsRatio2)-4)
	midLagMax := pitchMin(config.PitchLagMax/dsRatio2, (coarseLag*dsRatio/dsRatio2)+4)
	midLag := autocorrPitchSearch(ds8k, midLagMin, midLagMax)

	// Stage 3: Fine search at full rate per subframe
	pitchLags := make([]int, numSubframes)
	for sf := 0; sf < numSubframes; sf++ {
		start := sf * subframeSamples
		end := start + subframeSamples
		if end > len(pcm) {
			end = len(pcm)
		}
		subframe := pcm[start:end]

		// Search around mid lag (+/- 2 samples at full rate)
		fineLagMin := pitchMax(config.PitchLagMin, (midLag*dsRatio2)-2)
		fineLagMax := pitchMin(config.PitchLagMax, (midLag*dsRatio2)+2)

		pitchLags[sf] = autocorrPitchSearchSubframe(subframe, pcm, start, fineLagMin, fineLagMax)
	}

	return pitchLags
}

// autocorrPitchSearch finds best pitch lag using normalized autocorrelation.
// Uses bias toward shorter lags to avoid octave errors.
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

// downsample reduces sample rate by averaging factor samples.
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

// encodePitchLags encodes pitch lags to the bitstream.
// First subframe is absolute, subsequent are delta-coded via contour.
// Per RFC 6716 Section 4.2.7.6.
// Uses existing ICDF tables: ICDFPitchLagNB/MB/WB, ICDFPitchLowBitsQ*, ICDFPitchContourNB/MB/WB
func (e *Encoder) encodePitchLags(pitchLags []int, numSubframes int) {
	config := GetBandwidthConfig(e.bandwidth)

	// Select pitch contour table based on bandwidth and frame size
	var pitchContour [][4]int8
	var contourICDF []uint16

	switch e.bandwidth {
	case BandwidthNarrowband:
		if numSubframes == 4 {
			pitchContour = make([][4]int8, len(PitchContourNB20ms))
			for i := range PitchContourNB20ms {
				pitchContour[i] = PitchContourNB20ms[i]
			}
		} else {
			// Convert [16][2]int8 to [][4]int8 for 10ms frames
			pitchContour = make([][4]int8, len(PitchContourNB10ms))
			for i := range PitchContourNB10ms {
				pitchContour[i] = [4]int8{PitchContourNB10ms[i][0], PitchContourNB10ms[i][1], 0, 0}
			}
		}
		contourICDF = ICDFPitchContourNB
	case BandwidthMediumband:
		if numSubframes == 4 {
			pitchContour = make([][4]int8, len(PitchContourMB20ms))
			for i := range PitchContourMB20ms {
				pitchContour[i] = PitchContourMB20ms[i]
			}
		} else {
			pitchContour = make([][4]int8, len(PitchContourMB10ms))
			for i := range PitchContourMB10ms {
				pitchContour[i] = [4]int8{PitchContourMB10ms[i][0], PitchContourMB10ms[i][1], 0, 0}
			}
		}
		contourICDF = ICDFPitchContourMB
	default: // Wideband
		if numSubframes == 4 {
			pitchContour = make([][4]int8, len(PitchContourWB20ms))
			for i := range PitchContourWB20ms {
				pitchContour[i] = PitchContourWB20ms[i]
			}
		} else {
			pitchContour = make([][4]int8, len(PitchContourWB10ms))
			for i := range PitchContourWB10ms {
				pitchContour[i] = [4]int8{PitchContourWB10ms[i][0], PitchContourWB10ms[i][1], 0, 0}
			}
		}
		contourICDF = ICDFPitchContourWB
	}

	// Find best matching contour and base lag
	contourIdx, baseLag := e.findBestPitchContour(pitchLags, pitchContour, numSubframes)

	// Encode absolute lag for first subframe
	lagIdx := baseLag - config.PitchLagMin
	if lagIdx < 0 {
		lagIdx = 0
	}
	if lagIdx > config.PitchLagMax-config.PitchLagMin {
		lagIdx = config.PitchLagMax - config.PitchLagMin
	}

	// Encode lag high bits (MSB) - use bandwidth-specific ICDF
	var lagHighICDF []uint16

	switch e.bandwidth {
	case BandwidthNarrowband:
		lagHighICDF = ICDFPitchLagNB
	case BandwidthMediumband:
		lagHighICDF = ICDFPitchLagMB
	default: // Wideband
		lagHighICDF = ICDFPitchLagWB
	}

	// Low bits are ALWAYS 2 bits (Q2) per RFC 6716 Section 4.2.7.6.1
	// lag = min_lag + high * 4 + low (low is always 0-3)
	lagLowICDF := ICDFPitchLowBitsQ2
	divisor := 4

	lagHigh := lagIdx / divisor
	lagLow := lagIdx % divisor

	// Clamp to valid range for the ICDF table
	maxHighIdx := len(lagHighICDF) - 2
	if lagHigh > maxHighIdx {
		lagHigh = maxHighIdx
	}
	maxLowIdx := len(lagLowICDF) - 2
	if lagLow > maxLowIdx {
		lagLow = maxLowIdx
	}

	e.rangeEncoder.EncodeICDF16(lagHigh, lagHighICDF, 8)
	e.rangeEncoder.EncodeICDF16(lagLow, lagLowICDF, 8)

	// Encode contour index for delta pattern
	maxContourIdx := len(contourICDF) - 2
	if contourIdx > maxContourIdx {
		contourIdx = maxContourIdx
	}
	e.rangeEncoder.EncodeICDF16(contourIdx, contourICDF, 8)
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
