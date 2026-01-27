// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file implements the spread decision algorithm for the encoder.

package celt

import "math"

// SpreadingDecision analyzes the normalized MDCT coefficients to decide the
// optimal spread parameter for PVQ quantization.
//
// The spread parameter controls how pulses are distributed across the band:
// - SPREAD_AGGRESSIVE (3): More spreading, better for tonal signals
// - SPREAD_NORMAL (2): Default spreading
// - SPREAD_LIGHT (1): Less spreading
// - SPREAD_NONE (0): No spreading, for very noisy signals
//
// The algorithm counts how many coefficients fall below certain thresholds
// relative to the band energy. Tonal signals have energy concentrated in
// few bins (low counts), while noisy signals have energy spread across
// many bins (high counts).
//
// Parameters:
//   - normX: normalized MDCT coefficients (unit-norm per band)
//   - nbBands: number of bands to analyze
//   - channels: number of audio channels (1 or 2)
//   - frameSize: frame size in samples (determines M scaling)
//   - updateHF: whether to update high-frequency average for tapset decision
//
// Returns: spread decision (0=SPREAD_NONE, 1=SPREAD_LIGHT, 2=SPREAD_NORMAL, 3=SPREAD_AGGRESSIVE)
//
// Reference: libopus celt/bands.c spreading_decision()
func (e *Encoder) SpreadingDecision(normX []float64, nbBands, channels, frameSize int, updateHF bool) int {
	if nbBands <= 0 || len(normX) == 0 {
		return spreadNormal
	}

	// M is the time resolution multiplier (frameSize / shortMdctSize)
	// shortMdctSize is 120 for 48kHz
	M := frameSize / 120
	if M < 1 {
		M = 1
	}

	// N0 = total MDCT coefficients per channel (M * shortMdctSize)
	N0 := M * 120

	// Check if the last band is too narrow for spread decision
	// libopus: if (M*(eBands[end]-eBands[end-1]) <= 8) return SPREAD_NONE
	lastBandWidth := ScaledBandWidth(nbBands-1, frameSize)
	if lastBandWidth <= 8 {
		return spreadNone
	}

	// Compute spread weights based on band importance
	// For simplicity, use uniform weights (libopus uses dynamic weights based on masking)
	spreadWeight := make([]int, nbBands)
	for i := 0; i < nbBands; i++ {
		spreadWeight[i] = 1
	}

	sum := 0
	nbBandsTotal := 0
	hfSum := 0

	// Process each channel
	for c := 0; c < channels; c++ {
		// Process each band
		for band := 0; band < nbBands; band++ {
			// Get band boundaries
			bandStart := ScaledBandStart(band, frameSize)
			bandEnd := ScaledBandEnd(band, frameSize)
			N := bandEnd - bandStart

			if N <= 8 {
				continue
			}

			// Extract coefficients for this band and channel
			// Coefficients are organized as [ch0_coeffs][ch1_coeffs]
			xOffset := c*N0 + bandStart
			if xOffset+N > len(normX) {
				continue
			}

			// Count coefficients below thresholds
			// libopus uses x2N = x[j]^2 * N, then checks thresholds:
			// - tcount[0]: x2N < 0.25 (|x[j]| < 0.5/sqrt(N))
			// - tcount[1]: x2N < 0.0625 (|x[j]| < 0.25/sqrt(N))
			// - tcount[2]: x2N < 0.015625 (|x[j]| < 0.125/sqrt(N))
			tcount := [3]int{0, 0, 0}
			Nf := float64(N)

			for j := 0; j < N; j++ {
				x := normX[xOffset+j]
				x2N := x * x * Nf

				if x2N < 0.25 {
					tcount[0]++
				}
				if x2N < 0.0625 {
					tcount[1]++
				}
				if x2N < 0.015625 {
					tcount[2]++
				}
			}

			// High frequency bands contribution (last 4 bands, ~8kHz and up)
			// libopus: if (i > m->nbEBands-4)
			if band > nbBands-4 {
				hfSum += (32 * (tcount[1] + tcount[0])) / N
			}

			// Compute tmp: count of thresholds where majority of coeffs are below
			// tmp = (2*tcount[2] >= N) + (2*tcount[1] >= N) + (2*tcount[0] >= N)
			tmp := 0
			if 2*tcount[2] >= N {
				tmp++
			}
			if 2*tcount[1] >= N {
				tmp++
			}
			if 2*tcount[0] >= N {
				tmp++
			}

			sum += tmp * spreadWeight[band]
			nbBandsTotal += spreadWeight[band]
		}
	}

	// Update high-frequency average for tapset decision
	if updateHF {
		hfBandCount := 4 - nbBands + nbBands // Number of HF bands (simplification)
		if hfBandCount < 1 {
			hfBandCount = 1
		}
		if hfSum > 0 {
			hfSum = hfSum / (channels * hfBandCount)
		}
		e.hfAverage = (e.hfAverage + hfSum) >> 1

		// Adjust for current tapset decision
		adjustedHF := e.hfAverage
		if e.tapsetDecision == 2 {
			adjustedHF += 4
		} else if e.tapsetDecision == 0 {
			adjustedHF -= 4
		}

		// Update tapset decision with hysteresis
		if adjustedHF > 22 {
			e.tapsetDecision = 2
		} else if adjustedHF > 18 {
			e.tapsetDecision = 1
		} else {
			e.tapsetDecision = 0
		}
	}

	if nbBandsTotal <= 0 {
		return spreadNormal
	}

	// Normalize sum to Q8 (multiply by 256, divide by band count)
	sum = (sum << 8) / nbBandsTotal

	// Recursive averaging with previous
	sum = (sum + e.tonalAverage) >> 1
	e.tonalAverage = sum

	// Apply hysteresis based on last decision
	// libopus: sum = (3*sum + (((3-last_decision)<<7) + 64) + 2)>>2
	sum = (3*sum + ((3 - e.spreadDecision) << 7) + 64 + 2) >> 2

	// Make decision based on thresholds
	var decision int
	if sum < 80 {
		decision = spreadAggressive
	} else if sum < 256 {
		decision = spreadNormal
	} else if sum < 384 {
		decision = spreadLight
	} else {
		decision = spreadNone
	}

	e.spreadDecision = decision
	return decision
}

// ComputeSpreadWeights computes per-band weights for the spread decision.
// Higher weights for perceptually important bands.
// This is a simplified version; libopus uses dynamic weights from dynalloc_analysis.
//
// Parameters:
//   - bandLogE: log-domain band energies
//   - nbBands: number of bands
//
// Returns: weights per band (higher = more important)
func ComputeSpreadWeights(bandLogE []float64, nbBands int) []int {
	weights := make([]int, nbBands)

	if len(bandLogE) < nbBands {
		// Default to uniform weights
		for i := 0; i < nbBands; i++ {
			weights[i] = 1
		}
		return weights
	}

	// Compute average energy
	var avg float64
	for i := 0; i < nbBands; i++ {
		avg += bandLogE[i]
	}
	avg /= float64(nbBands)

	// Weight based on energy relative to average
	// Bands with higher energy get more weight
	for i := 0; i < nbBands; i++ {
		smr := bandLogE[i] - avg
		// Convert SMR to shift (libopus uses floor(0.5 + smr) clamped to [0,5])
		shift := int(math.Round(smr))
		if shift < 0 {
			shift = 0
		}
		if shift > 5 {
			shift = 5
		}
		// Weight = 32 >> shift
		weights[i] = 32 >> shift
	}

	return weights
}

// SpreadDecisionForShortBlocks returns spread decision for transient frames.
// For short blocks, spreading is typically disabled or minimal.
//
// Returns: SPREAD_NONE for transient frames (libopus behavior)
func SpreadDecisionForShortBlocks() int {
	return spreadNone
}
