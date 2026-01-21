package silk

// decodePitchLag decodes pitch lag for all subframes (voiced frames only).
// Per RFC 6716 Section 4.2.7.6.1.
//
// Returns absolute pitch lag in samples for each subframe.
// Pitch lag is constrained to [PitchLagMin, PitchLagMax] per bandwidth.
func (d *Decoder) decodePitchLag(bandwidth Bandwidth, numSubframes int) []int {
	config := GetBandwidthConfig(bandwidth)
	pitchLags := make([]int, numSubframes)

	// Decode pitch lag high part (coarse)
	var lagHigh int
	switch bandwidth {
	case BandwidthNarrowband:
		lagHigh = d.rangeDecoder.DecodeICDF16(ICDFPitchLagNB, 8)
	case BandwidthMediumband:
		lagHigh = d.rangeDecoder.DecodeICDF16(ICDFPitchLagMB, 8)
	case BandwidthWideband:
		lagHigh = d.rangeDecoder.DecodeICDF16(ICDFPitchLagWB, 8)
	}

	// Decode pitch lag low bits (fine, 2 bits for 4 values)
	lagLow := d.rangeDecoder.DecodeICDF16(ICDFPitchLowBitsQ2, 8)

	// Base pitch lag for first subframe
	// lag = min_lag + high * 4 + low
	baseLag := config.PitchLagMin + lagHigh*4 + lagLow
	pitchLags[0] = baseLag

	// Decode contour (delta per subframe)
	var contour []int8
	switch bandwidth {
	case BandwidthNarrowband:
		if numSubframes == 4 {
			contourIdx := d.rangeDecoder.DecodeICDF16(ICDFPitchContourNB, 8)
			contour = PitchContourNB20ms[contourIdx][:]
		} else {
			contourIdx := d.rangeDecoder.DecodeICDF16(ICDFPitchContourNB, 8)
			contour = PitchContourNB10ms[contourIdx][:]
		}
	case BandwidthMediumband:
		if numSubframes == 4 {
			contourIdx := d.rangeDecoder.DecodeICDF16(ICDFPitchContourMB, 8)
			contour = PitchContourMB20ms[contourIdx][:]
		} else {
			contourIdx := d.rangeDecoder.DecodeICDF16(ICDFPitchContourMB, 8)
			contour = PitchContourMB10ms[contourIdx][:]
		}
	case BandwidthWideband:
		if numSubframes == 4 {
			contourIdx := d.rangeDecoder.DecodeICDF16(ICDFPitchContourWB, 8)
			contour = PitchContourWB20ms[contourIdx][:]
		} else {
			contourIdx := d.rangeDecoder.DecodeICDF16(ICDFPitchContourWB, 8)
			contour = PitchContourWB10ms[contourIdx][:]
		}
	}

	// Apply contour to get per-subframe pitch lags
	for i := 0; i < numSubframes; i++ {
		if i > 0 && len(contour) > i {
			pitchLags[i] = baseLag + int(contour[i])
		} else if i > 0 {
			pitchLags[i] = baseLag
		}

		// Clamp to valid range
		if pitchLags[i] < config.PitchLagMin {
			pitchLags[i] = config.PitchLagMin
		}
		if pitchLags[i] > config.PitchLagMax {
			pitchLags[i] = config.PitchLagMax
		}
	}

	return pitchLags
}

// decodeLTPCoefficients decodes LTP filter coefficients (voiced frames only).
// Per RFC 6716 Section 4.2.7.6.2.
//
// Returns Q7 coefficients: [numSubframes][5 taps].
// Also returns periodicity index (0, 1, or 2) that selects the codebook.
func (d *Decoder) decodeLTPCoefficients(bandwidth Bandwidth, numSubframes int) ([][]int8, int) {
	// Decode periodicity index (selects codebook variant)
	// 0 = low periodicity, 1 = mid, 2 = high
	var periodicity int
	periodicityIdx := d.rangeDecoder.DecodeICDF16(ICDFLTPFilterIndexLowPeriod, 8)
	if periodicityIdx < 4 {
		periodicity = 0
	} else {
		periodicityIdx = d.rangeDecoder.DecodeICDF16(ICDFLTPFilterIndexMidPeriod, 8)
		if periodicityIdx < 5 {
			periodicity = 1
		} else {
			periodicity = 2
		}
	}

	ltpCoeffs := make([][]int8, numSubframes)

	for sf := 0; sf < numSubframes; sf++ {
		// Decode LTP filter index for this subframe based on periodicity
		var ltpIdx int
		switch periodicity {
		case 0:
			ltpIdx = d.rangeDecoder.DecodeICDF16(ICDFLTPGainLow, 8)
			if ltpIdx >= len(LTPFilterLow) {
				ltpIdx = len(LTPFilterLow) - 1
			}
		case 1:
			ltpIdx = d.rangeDecoder.DecodeICDF16(ICDFLTPGainMid, 8)
			if ltpIdx >= len(LTPFilterMid) {
				ltpIdx = len(LTPFilterMid) - 1
			}
		case 2:
			ltpIdx = d.rangeDecoder.DecodeICDF16(ICDFLTPGainHigh, 8)
			if ltpIdx >= len(LTPFilterHigh) {
				ltpIdx = len(LTPFilterHigh) - 1
			}
		}

		// Look up coefficients from codebook
		ltpCoeffs[sf] = make([]int8, 5)
		switch periodicity {
		case 0:
			copy(ltpCoeffs[sf], LTPFilterLow[ltpIdx][:])
		case 1:
			copy(ltpCoeffs[sf], LTPFilterMid[ltpIdx][:])
		case 2:
			copy(ltpCoeffs[sf], LTPFilterHigh[ltpIdx][:])
		}
	}

	return ltpCoeffs, periodicity
}

// decodeLTPScale decodes the LTP scale index.
// Per RFC 6716 Section 4.2.7.6.3.
//
// The scale adjusts the LTP gain for voiced frames.
// Returns scale index 0, 1, or 2.
func (d *Decoder) decodeLTPScale() int {
	// LTP scale is encoded with 3 values
	return d.rangeDecoder.DecodeICDF16(ICDFLTPFilterIndexLowPeriod, 8) % 3
}
