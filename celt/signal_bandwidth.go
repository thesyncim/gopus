package celt

import "math"

// celtMinSignalBandwidth mirrors libopus celt_encoder.c min_bandwidth selection.
func celtMinSignalBandwidth(equivRate, channels int) int {
	if equivRate < 32000*channels {
		return 13
	}
	if equivRate < 48000*channels {
		return 16
	}
	if equivRate < 60000*channels {
		return 18
	}
	if equivRate < 80000*channels {
		return 19
	}
	return 20
}

// estimateSignalBandwidthFromBandLogE estimates the analysis bandwidth index (1..20)
// from CELT band log-energies for use on the next frame's allocation gating.
func estimateSignalBandwidthFromBandLogE(bandLogE []float64, nbBands, channels, prev, lsbDepth int) int {
	if nbBands <= 0 || len(bandLogE) == 0 {
		if prev > 0 {
			return prev
		}
		return 20
	}
	if channels < 1 {
		channels = 1
	}

	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if lsbDepth < 8 {
		lsbDepth = 8
	}

	// Reconstruct a per-band power track (max across channels), then apply the
	// libopus-style active-band and masking tests from analysis.c.
	var perBandEnergy [MaxBands]float64
	maxEnergy := 0.0
	for b := 0; b < nbBands; b++ {
		bandEnergy := 0.0
		for c := 0; c < channels; c++ {
			idx := c*nbBands + b
			if idx >= len(bandLogE) {
				break
			}
			// Convert from mean-relative log amplitude back to power.
			v := bandLogE[idx]
			if b < len(eMeans) {
				v += eMeans[b] * DB6
			}
			amp := math.Exp2(v)
			power := amp * amp
			if power > bandEnergy {
				bandEnergy = power
			}
		}
		perBandEnergy[b] = bandEnergy
		if bandEnergy > maxEnergy {
			maxEnergy = bandEnergy
		}
	}

	if maxEnergy <= 0 {
		if prev > 0 {
			return prev
		}
		return 20
	}

	lsbShift := lsbDepth - 8
	if lsbShift < 0 {
		lsbShift = 0
	}
	noiseScale := float64(uint(1) << uint(lsbShift))
	noiseFloor := 5.7e-4 / noiseScale
	noiseFloor *= noiseFloor

	var masked [MaxBands]bool
	bw := 0
	bandwidthMask := 0.0
	for b := 0; b < nbBands; b++ {
		E := perBandEnergy[b]
		width := 1
		if b+1 < len(EBands) {
			width = EBands[b+1] - EBands[b]
			if width < 1 {
				width = 1
			}
		}
		if E*1e9 > maxEnergy && E > noiseFloor*float64(width) {
			bw = b + 1
		}

		maskThresh := 0.05
		if prev >= b+1 {
			maskThresh = 0.01
		}
		if E < maskThresh*bandwidthMask {
			masked[b] = true
		}
		bandwidthMask = math.Max(0.05*bandwidthMask, E)
	}

	if bw == 20 && nbBands > 0 && masked[nbBands-1] {
		bw -= 2
	} else if bw > 0 && bw <= nbBands && masked[bw-1] {
		bw--
	}

	if bw < 1 {
		bw = 1
	}
	if bw > 20 {
		bw = 20
	}
	return bw
}
