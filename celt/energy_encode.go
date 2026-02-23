// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides energy encoding functions that mirror the decoder.

package celt

import (
	"math"

	"github.com/thesyncim/gopus/rangecoding"
)

// ComputeBandEnergies computes energy for each frequency band from MDCT coefficients.
// Returns energies in log2 scale, RELATIVE TO MEAN (same as libopus).
// energies[c*nbBands + band] = log2(amplitude) - eMeans[band]
//
// The energy computation extracts loudness per frequency band:
// 1. For each band, sum squares of MDCT coefficients
// 2. Divide by band width to get average power
// 3. Convert to log2 scale: energy = 0.5 * log2(sumSq)
// 4. Subtract eMeans to make values mean-relative (like libopus amp2Log2)
//
// The decoder adds eMeans back during denormalization, recovering the original.
// This ensures encoder and decoder use matching gain values.
//
// Reference: RFC 6716 Section 4.3.2, libopus celt/quant_bands.c amp2Log2()
func (e *Encoder) ComputeBandEnergies(mdctCoeffs []float64, nbBands, frameSize int) []float64 {
	// Use default scratch buffer - caller should use ComputeBandEnergiesInto if they need
	// a specific destination buffer to avoid aliasing
	energiesLen := nbBands * e.channels
	dst := ensureFloat64Slice(&e.scratch.energies, energiesLen)
	e.ComputeBandEnergiesInto(mdctCoeffs, nbBands, frameSize, dst)
	return dst
}

// ComputeBandEnergiesInto computes band energies into the provided destination buffer.
// Use this instead of ComputeBandEnergies when you need to avoid buffer aliasing.
func (e *Encoder) ComputeBandEnergiesInto(mdctCoeffs []float64, nbBands, frameSize int, dst []float64) {
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands < 0 {
		nbBands = 0
	}

	// Determine number of channels from coefficient length
	channels := e.channels
	coeffsPerChannel := frameSize
	if len(mdctCoeffs) < coeffsPerChannel*channels {
		// Handle mono or incomplete data
		if len(mdctCoeffs) < coeffsPerChannel {
			channels = 1
			coeffsPerChannel = len(mdctCoeffs)
		} else {
			channels = 1
		}
	}

	energies := dst
	silence := 0.5 * math.Log2(1e-27)

	for c := 0; c < channels; c++ {
		// Get coefficients for this channel
		channelStart := c * coeffsPerChannel
		channelEnd := channelStart + coeffsPerChannel
		if channelEnd > len(mdctCoeffs) {
			channelEnd = len(mdctCoeffs)
		}

		channelCoeffs := mdctCoeffs[channelStart:channelEnd]

		for band := 0; band < nbBands; band++ {
			// Get band boundaries scaled for frame size
			start := ScaledBandStart(band, frameSize)
			end := ScaledBandEnd(band, frameSize)

			// Clamp to available coefficients
			if start >= len(channelCoeffs) {
				energy := silence
				if band < len(eMeans) {
					energy -= eMeans[band] * DB6
				}
				energies[c*nbBands+band] = energy
				continue
			}
			if end > len(channelCoeffs) {
				end = len(channelCoeffs)
			}
			if end <= start {
				energy := silence
				if band < len(eMeans) {
					energy -= eMeans[band] * DB6
				}
				energies[c*nbBands+band] = energy
				continue
			}

			// Compute RMS energy in log2 scale (raw/absolute)
			energy := computeBandRMS(channelCoeffs, start, end)

			// Subtract eMeans to make energy mean-relative (like libopus amp2Log2).
			// The decoder adds eMeans back during denormalization.
			if band < len(eMeans) {
				energy -= eMeans[band] * DB6
			}

			energies[c*nbBands+band] = energy
		}
	}
}

// ComputeBandEnergiesRaw computes energy for each frequency band WITHOUT eMeans subtraction.
// Returns raw energies in log2 scale (log2 of amplitude).
// Used for testing/debugging to compare with libopus intermediate values.
func (e *Encoder) ComputeBandEnergiesRaw(mdctCoeffs []float64, nbBands, frameSize int) []float64 {
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands < 0 {
		nbBands = 0
	}

	channels := e.channels
	coeffsPerChannel := frameSize
	if len(mdctCoeffs) < coeffsPerChannel*channels {
		if len(mdctCoeffs) < coeffsPerChannel {
			channels = 1
			coeffsPerChannel = len(mdctCoeffs)
		} else {
			channels = 1
		}
	}

	energies := make([]float64, nbBands*channels)
	silence := 0.5 * math.Log2(1e-27)

	for c := 0; c < channels; c++ {
		channelStart := c * coeffsPerChannel
		channelEnd := channelStart + coeffsPerChannel
		if channelEnd > len(mdctCoeffs) {
			channelEnd = len(mdctCoeffs)
		}

		channelCoeffs := mdctCoeffs[channelStart:channelEnd]

		for band := 0; band < nbBands; band++ {
			start := ScaledBandStart(band, frameSize)
			end := ScaledBandEnd(band, frameSize)

			if start >= len(channelCoeffs) {
				energies[c*nbBands+band] = silence
				continue
			}
			if end > len(channelCoeffs) {
				end = len(channelCoeffs)
			}
			if end <= start {
				energies[c*nbBands+band] = silence
				continue
			}

			// Compute RMS energy in log2 scale (raw, no eMeans subtraction)
			energies[c*nbBands+band] = computeBandRMS(channelCoeffs, start, end)
		}
	}

	return energies
}

// computeBandRMS computes the per-band log2 amplitude from MDCT coefficients.
// Returns log2(sqrt(sum(x^2))) using the same epsilon as libopus.
// This matches libopus compute_band_energies() + amp2Log2() (float path).
func computeBandRMS(coeffs []float64, start, end int) float64 {
	if end <= start || start < 0 || end > len(coeffs) {
		return 0.5 * math.Log2(1e-27)
	}

	// Compute sum of squares with libopus epsilon.
	// BCE hint: we verified end <= len(coeffs) above.
	c := coeffs[start:end:end]
	sumSq := float32(1e-27)
	for _, cv := range c {
		v := float32(cv)
		sumSq += v * v
	}

	// log2(sqrt(sumSq)) = 0.5 * log2(sumSq).
	// Use the identity to eliminate math.Sqrt (~10ns/call × 42 bands/frame).
	return 0.5 * float64(celtLog2(sumSq))
}

func celtAbsInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// coarseLossDistortion mirrors libopus loss_distortion() for float mode.
// It estimates how expensive packet loss would be if we keep using inter prediction.
func coarseLossDistortion(energies, oldEBands []float64, nbBands, channels int) float64 {
	if nbBands <= 0 || channels <= 0 {
		return 0
	}
	var dist float64
	for c := 0; c < channels; c++ {
		base := c * nbBands
		oldBase := c * MaxBands
		for band := 0; band < nbBands; band++ {
			idx := base + band
			oldIdx := oldBase + band
			if idx >= len(energies) || oldIdx >= len(oldEBands) {
				continue
			}
			d := energies[idx] - oldEBands[oldIdx]
			dist += d * d
		}
	}
	// Match libopus loss_distortion(): normalize accumulated squared error.
	dist /= 128.0
	if dist > 200.0 {
		return 200.0
	}
	return dist
}

// coarseLossDistortionRange mirrors libopus loss_distortion() when coarse
// quantization operates on a band subset [start,end) (hybrid mode).
func coarseLossDistortionRange(energies, oldEBands []float64, start, end, nbBands, channels int) float64 {
	if nbBands <= 0 || channels <= 0 {
		return 0
	}
	if start < 0 {
		start = 0
	}
	if end > nbBands {
		end = nbBands
	}
	if end <= start {
		return 0
	}
	var dist float64
	for c := 0; c < channels; c++ {
		base := c * nbBands
		oldBase := c * MaxBands
		for band := start; band < end; band++ {
			idx := base + band
			oldIdx := oldBase + band
			if idx >= len(energies) || oldIdx >= len(oldEBands) {
				continue
			}
			d := energies[idx] - oldEBands[oldIdx]
			dist += d * d
		}
	}
	dist /= 128.0
	if dist > 200.0 {
		return 200.0
	}
	return dist
}

func (e *Encoder) encodeCoarseEnergyPass(energies []float64, startBand, nbBands int, intra bool, lm int, budget int, maxDecay32 float32, encodeIntraFlag bool) ([]float64, int) {
	if e.rangeEncoder == nil {
		return energies, 0
	}
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands < 0 {
		nbBands = 0
	}
	if lm < 0 {
		lm = 0
	}
	if lm > 3 {
		lm = 3
	}

	channels := e.channels
	if len(energies) < nbBands*channels {
		channels = 1
	}

	quantizedEnergies := ensureFloat64Slice(&e.scratch.quantizedEnergies, nbBands*channels)
	coarseError := ensureFloat64Slice(&e.scratch.coarseError, nbBands*channels)
	for i := range coarseError {
		coarseError[i] = 0
	}

	if encodeIntraFlag && e.rangeEncoder.Tell()+3 <= budget {
		bit := 0
		if intra {
			bit = 1
		}
		e.rangeEncoder.EncodeBit(bit, 3)
	}

	var coef, beta float64
	if intra {
		coef = 0.0
		beta = BetaIntra
	} else {
		coef = AlphaCoef[lm]
		beta = BetaCoefInter[lm]
	}
	coef32 := float32(coef)
	beta32 := float32(beta)

	prob := eProbModel[lm][0]
	if intra {
		prob = eProbModel[lm][1]
	}

	if startBand < 0 {
		startBand = 0
	}
	var prevBandEnergy [2]float32
	badness := 0
	for band := startBand; band < nbBands; band++ {
		for c := 0; c < channels; c++ {
			idx := c*nbBands + band
			if idx >= len(energies) {
				continue
			}

			x := float32(energies[idx])
			oldEBand := float32(e.prevEnergy[c*MaxBands+band])
			oldE := oldEBand
			minEnergy := float32(-9.0 * DB6)
			if oldE < minEnergy {
				oldE = minEnergy
			}

			predMul := noFMA32Mul(coef32, oldE)
			if tmpEnergyPredMulNativeEnabled {
				predMul = coef32 * oldE
			}
			pred := predMul + prevBandEnergy[c]
			f := x - pred
			qi := int(math.Floor(float64(f/float32(DB6) + 0.5)))
			qi0 := qi

			decayBound := oldEBand
			minDecay := float32(-28.0 * DB6)
			if decayBound < minDecay {
				decayBound = minDecay
			}
			decayBound -= maxDecay32
			if qi < 0 && x < decayBound {
				adjust := int((decayBound - x) / float32(DB6))
				qi += adjust
				if qi > 0 {
					qi = 0
				}
			}

			tell := e.rangeEncoder.Tell()
			bitsLeft := budget - tell - 3*channels*(nbBands-band)
			if band != 0 && bitsLeft < 30 {
				if bitsLeft < 24 && qi > 1 {
					qi = 1
				}
				if bitsLeft < 16 && qi < -1 {
					qi = -1
				}
			}
			if e.lfe && band >= 2 && qi > 0 {
				qi = 0
			}

			if budget-tell >= 15 {
				pi := 2 * band
				if pi > 40 {
					pi = 40
				}
				fs := int(prob[pi]) << 7
				decay := int(prob[pi+1]) << 6
				qi = e.encodeLaplace(qi, fs, decay)
			} else if budget-tell >= 2 {
				if qi > 1 {
					qi = 1
				}
				if qi < -1 {
					qi = -1
				}
				var s int
				if qi < 0 {
					s = -2*qi - 1
				} else {
					s = 2 * qi
				}
				e.rangeEncoder.EncodeICDF(s, smallEnergyICDF, 2)
			} else if budget-tell >= 1 {
				if qi > 0 {
					qi = 0
				}
				e.rangeEncoder.EncodeBit(-qi, 1)
			} else {
				qi = -1
			}
			badness += celtAbsInt(qi0 - qi)

			if e.coarseDecisionHook != nil {
				pi := 2 * band
				if pi > 40 {
					pi = 40
				}
				e.coarseDecisionHook(CoarseDecisionStats{
					Frame:     e.frameCount,
					Band:      band,
					Channel:   c,
					Intra:     intra,
					LM:        lm,
					ProbFS0:   int(prob[pi]),
					ProbDecay: int(prob[pi+1]),
					X:         float64(x),
					Pred:      float64(pred),
					Residual:  float64(f),
					QIInitial: qi0,
					QIFinal:   qi,
					Tell:      tell,
					BitsLeft:  bitsLeft,
				})
			}

			q := float32(qi) * float32(DB6)
			coarseError[idx] = float64(f - q)
			quantizedEnergy := pred + q
			quantizedEnergies[idx] = float64(quantizedEnergy)
			if tmpCoarseDumpEnabled && e.frameCount >= 74 && e.frameCount <= 80 && band == 18 && c == 0 {
				println("COARSE_DUMP frame", e.frameCount, "band", band, "x", x, "oldE", oldEBand, "pred", pred, "f", f, "qi", qi, "err", float32(coarseError[idx]), "tell", tell, "bitsLeft", bitsLeft)
			}
			betaMul := noFMA32Mul(beta32, q)
			if tmpEnergyPredMulNativeEnabled {
				betaMul = beta32 * q
			}
			prevBandEnergy[c] = prevBandEnergy[c] + q - betaMul
		}
	}

	for c := 0; c < channels; c++ {
		for band := 0; band < nbBands; band++ {
			idx := c*nbBands + band
			if idx >= len(quantizedEnergies) {
				continue
			}
			e.prevEnergy[c*MaxBands+band] = quantizedEnergies[idx]
		}
	}

	if e.lfe {
		return quantizedEnergies, 0
	}
	return quantizedEnergies, badness
}

func (e *Encoder) coarseNbAvailableBytesForBudget(budget int) int {
	nbAvailableBytes := budget / 8
	if e.coarseAvailableBytes > 0 {
		nbAvailableBytes = e.coarseAvailableBytes
		maxBytes := budget / 8
		if nbAvailableBytes > maxBytes {
			nbAvailableBytes = maxBytes
		}
	}
	if nbAvailableBytes < 0 {
		nbAvailableBytes = 0
	}
	return nbAvailableBytes
}

// DecideIntraMode runs libopus-style two-pass intra/inter selection for coarse energy.
// It performs trial encodes on a saved range coder state and restores state before returning.
// startBand specifies the first band to encode (0 for CELT-only, 17 for hybrid mode).
// This matches libopus quant_coarse_energy which iterates from start to end.
func (e *Encoder) DecideIntraMode(energies []float64, startBand, nbBands int, lm int) bool {
	if e.rangeEncoder == nil {
		return false
	}
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands <= 0 {
		return false
	}
	if lm < 0 {
		lm = 0
	}
	if lm > 3 {
		lm = 3
	}

	channels := e.channels
	if channels < 1 {
		channels = 1
	}

	budget := e.rangeEncoder.StorageBits()
	if e.frameBits > 0 && e.frameBits < budget {
		budget = e.frameBits
	}
	nbAvailableBytes := e.coarseNbAvailableBytesForBudget(budget)

	codedBands := nbBands - startBand
	if codedBands < 0 {
		codedBands = 0
	}
	twoPass := e.complexity >= 4
	intra := e.forceIntra || (!twoPass &&
		e.delayedIntra > float64(2*channels*codedBands) &&
		nbAvailableBytes > codedBands*channels)

	intraBias := 0
	if channels > 0 {
		intraBias = int(float64(budget) * e.delayedIntra * float64(e.packetLoss) / float64(channels*512))
	}

	tell := e.rangeEncoder.Tell()
	if tell+3 > budget {
		twoPass = false
		intra = false
	}

	maxDecay32 := float32(16.0 * DB6)
	// Match libopus quant_coarse_energy(): decay clamp is based on coded span
	// (end-start), not absolute end band index.
	if codedBands > 10 {
		limit := float32(0.125 * float64(nbAvailableBytes) * DB6)
		if limit < maxDecay32 {
			maxDecay32 = limit
		}
	}
	if e.lfe {
		maxDecay32 = float32(3.0 * DB6)
	}

	startState := &e.scratch.coarseStartState
	e.rangeEncoder.SaveStateInto(startState)

	oldStart := ensureFloat64Slice(&e.scratch.coarseOldStart, len(e.prevEnergy))
	copy(oldStart, e.prevEnergy)

	badnessIntra := 0
	tellIntra := 0
	if twoPass || intra {
		_, badnessIntra = e.encodeCoarseEnergyPass(energies, startBand, nbBands, true, lm, budget, maxDecay32, true)
		tellIntra = e.rangeEncoder.TellFrac()
		e.rangeEncoder.RestoreState(startState)
		copy(e.prevEnergy, oldStart)
	}

	if !intra {
		_, badnessInter := e.encodeCoarseEnergyPass(energies, startBand, nbBands, false, lm, budget, maxDecay32, true)
		useIntra := badnessIntra < badnessInter
		if badnessIntra == badnessInter && e.rangeEncoder.TellFrac()+intraBias > tellIntra {
			useIntra = true
		}
		if twoPass && useIntra {
			intra = true
		} else {
			intra = false
		}
		e.rangeEncoder.RestoreState(startState)
		copy(e.prevEnergy, oldStart)
	}

	return intra
}

// EncodeCoarseEnergy encodes coarse (6dB step) band energies.
// This mirrors decoder's DecodeCoarseEnergy exactly (in reverse).
// intra=true: no inter-frame prediction (first frame or after loss)
// intra=false: uses alpha prediction from previous frame
//
// Returns the quantized energies (after encoding) for use by fine energy encoding.
//
// Reference: RFC 6716 Section 4.3.2, libopus celt/quant_bands.c quant_coarse_energy()
func (e *Encoder) EncodeCoarseEnergy(energies []float64, nbBands int, intra bool, lm int) []float64 {
	if e.rangeEncoder == nil {
		return energies
	}
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands < 0 {
		nbBands = 0
	}
	if lm < 0 {
		lm = 0
	}
	if lm > 3 {
		lm = 3
	}

	channels := e.channels
	if len(energies) < nbBands*channels {
		channels = 1
	}

	newDistortion := coarseLossDistortion(energies, e.prevEnergy, nbBands, channels)

	budget := e.rangeEncoder.StorageBits()
	if e.frameBits > 0 && e.frameBits < budget {
		budget = e.frameBits
	}

	// Max decay bound (full-band path: coded span is nbBands-startBand).
	maxDecay32 := float32(16.0 * DB6)
	nbAvailableBytes := e.coarseNbAvailableBytesForBudget(budget)
	if nbBands > 10 {
		limit := float32(0.125 * float64(nbAvailableBytes) * DB6)
		if limit < maxDecay32 {
			maxDecay32 = limit
		}
	}
	if e.lfe {
		maxDecay32 = float32(3.0 * DB6)
	}

	quantizedEnergies, _ := e.encodeCoarseEnergyPass(energies, 0, nbBands, intra, lm, budget, maxDecay32, false)

	alpha := AlphaCoef[lm]
	if intra {
		e.delayedIntra = newDistortion
	} else {
		e.delayedIntra = alpha*alpha*e.delayedIntra + newDistortion
	}

	return quantizedEnergies
}

// EncodeCoarseEnergyRange encodes coarse energies for bands in [start, end).
// This mirrors EncodeCoarseEnergy but only processes the specified band range.
// Bands outside the range keep their previous energy values.
func (e *Encoder) EncodeCoarseEnergyRange(energies []float64, start, end int, intra bool, lm int) []float64 {
	if e.rangeEncoder == nil {
		return energies
	}
	if start < 0 {
		start = 0
	}
	if end > MaxBands {
		end = MaxBands
	}
	if end <= start {
		return energies
	}
	if lm < 0 {
		lm = 0
	}
	if lm > 3 {
		lm = 3
	}

	nbBands := end
	channels := e.channels
	if len(energies) < nbBands*channels {
		channels = 1
	}
	newDistortion := coarseLossDistortionRange(energies, e.prevEnergy, start, end, nbBands, channels)

	quantizedEnergies := ensureFloat64Slice(&e.scratch.quantizedEnergies, nbBands*channels)
	coarseError := ensureFloat64Slice(&e.scratch.coarseError, nbBands*channels)
	for i := range coarseError {
		coarseError[i] = 0
	}
	// Initialize with previous energies so bands outside [start,end) remain unchanged.
	for c := 0; c < channels; c++ {
		basePrev := c * MaxBands
		base := c * nbBands
		for band := 0; band < nbBands; band++ {
			quantizedEnergies[base+band] = e.prevEnergy[basePrev+band]
		}
	}

	// Prediction coefficients.
	var coef, beta float64
	if intra {
		coef = 0.0
		beta = BetaIntra
	} else {
		coef = AlphaCoef[lm]
		beta = BetaCoefInter[lm]
	}
	coef32 := float32(coef)
	beta32 := float32(beta)

	prob := eProbModel[lm][0]
	if intra {
		prob = eProbModel[lm][1]
	}

	budget := e.rangeEncoder.StorageBits()
	if e.frameBits > 0 && e.frameBits < budget {
		budget = e.frameBits
	}

	// Max decay bound (range path: clamp based on coded span, not absolute end band).
	maxDecay32 := float32(16.0 * DB6)
	nbAvailableBytes := e.coarseNbAvailableBytesForBudget(budget)
	if end-start > 10 {
		limit := float32(0.125 * float64(nbAvailableBytes) * DB6)
		if limit < maxDecay32 {
			maxDecay32 = limit
		}
	}
	if e.lfe {
		maxDecay32 = float32(3.0 * DB6)
	}

	var prevBandEnergy [2]float32
	for band := start; band < end; band++ {
		for c := 0; c < channels; c++ {
			idx := c*nbBands + band
			if idx >= len(energies) {
				continue
			}
			x := float32(energies[idx])

			// Previous frame energy (for prediction and decay bound).
			oldEBand := float32(e.prevEnergy[c*MaxBands+band])
			oldE := oldEBand
			minEnergy := float32(-9.0 * DB6)
			if oldE < minEnergy {
				oldE = minEnergy
			}

			// Prediction residual.
			predMul := noFMA32Mul(coef32, oldE)
			if tmpEnergyPredMulNativeEnabled {
				predMul = coef32 * oldE
			}
			pred := predMul + prevBandEnergy[c]
			f := x - pred
			qi := int(math.Floor(float64(f/float32(DB6) + 0.5)))
			qi0 := qi

			// Prevent energy from decaying too quickly.
			decayBound := oldEBand
			minDecay := float32(-28.0 * DB6)
			if decayBound < minDecay {
				decayBound = minDecay
			}
			decayBound -= maxDecay32
			if qi < 0 && x < decayBound {
				adjust := int((decayBound - x) / float32(DB6))
				qi += adjust
				if qi > 0 {
					qi = 0
				}
			}

			tell := e.rangeEncoder.Tell()
			bitsLeft := budget - tell - 3*channels*(end-band)
			if band != start && bitsLeft < 30 {
				if bitsLeft < 24 && qi > 1 {
					qi = 1
				}
				if bitsLeft < 16 && qi < -1 {
					qi = -1
				}
			}
			if e.lfe && band >= 2 && qi > 0 {
				qi = 0
			}

			// Encode with Laplace or fallback models.
			if budget-tell >= 15 {
				pi := 2 * band
				if pi > 40 {
					pi = 40
				}
				fs := int(prob[pi]) << 7
				decay := int(prob[pi+1]) << 6
				qi = e.encodeLaplace(qi, fs, decay)
			} else if budget-tell >= 2 {
				if qi > 1 {
					qi = 1
				}
				if qi < -1 {
					qi = -1
				}
				// Encode using zigzag mapping to match decoder's decoding:
				// Decoder: qi = (s >> 1) ^ -(s & 1)
				//   s=0 -> qi=0, s=1 -> qi=-1, s=2 -> qi=1
				var s int
				if qi < 0 {
					s = -2*qi - 1
				} else {
					s = 2 * qi
				}
				e.rangeEncoder.EncodeICDF(s, smallEnergyICDF, 2)
			} else if budget-tell >= 1 {
				if qi > 0 {
					qi = 0
				}
				e.rangeEncoder.EncodeBit(-qi, 1)
			} else {
				qi = -1
			}

			if e.coarseDecisionHook != nil {
				pi := 2 * band
				if pi > 40 {
					pi = 40
				}
				e.coarseDecisionHook(CoarseDecisionStats{
					Frame:     e.frameCount,
					Band:      band,
					Channel:   c,
					Intra:     intra,
					LM:        lm,
					ProbFS0:   int(prob[pi]),
					ProbDecay: int(prob[pi+1]),
					X:         float64(x),
					Pred:      float64(pred),
					Residual:  float64(f),
					QIInitial: qi0,
					QIFinal:   qi,
					Tell:      tell,
					BitsLeft:  bitsLeft,
				})
			}

			// Update energy and prediction state.
			q := float32(qi) * float32(DB6)
			coarseError[idx] = float64(f - q)
			energy := pred + q
			quantizedEnergies[idx] = float64(energy)
			betaMul := noFMA32Mul(beta32, q)
			if tmpEnergyPredMulNativeEnabled {
				betaMul = beta32 * q
			}
			prevBandEnergy[c] = prevBandEnergy[c] + q - betaMul
		}
	}

	alpha := AlphaCoef[lm]
	if intra {
		e.delayedIntra = newDistortion
	} else {
		e.delayedIntra = alpha*alpha*e.delayedIntra + newDistortion
	}

	return quantizedEnergies
}

// encodeLaplace encodes a Laplace-distributed integer using the range encoder.
// This is the inverse of decoder's decodeLaplace.
// Uses symmetric Laplace encoding: 0, +1, -1, +2, -2, ...
//
// Parameters:
//   - val: the integer value to encode
//   - decay: controls the distribution spread
//
// Reference: libopus celt/laplace.c ec_laplace_encode()
func (e *Encoder) encodeLaplace(val int, fs int, decay int) int {
	re := e.rangeEncoder
	if re == nil {
		return val
	}

	fl := 0
	if val != 0 {
		s := 0
		if val < 0 {
			s = -1
		}
		absVal := (val + s) ^ s
		fl = fs
		fs = ec_laplace_get_freq1(fs, decay)
		i := 1
		for fs > 0 && i < absVal {
			fs *= 2
			fl += fs + 2*laplaceMinP
			fs = (fs * decay) >> 15
			i++
		}
		if fs == 0 {
			ndiMax := (laplaceFS - fl + laplaceMinP - 1) >> laplaceLogMinP
			ndiMax = (ndiMax - s) >> 1
			di := absVal - i
			if di > ndiMax-1 {
				di = ndiMax - 1
			}
			fl += (2*di + 1 + s) * laplaceMinP
			if laplaceFS-fl < laplaceMinP {
				fs = laplaceFS - fl
			} else {
				fs = laplaceMinP
			}
			absVal = i + di
			val = (absVal + s) ^ s
		} else {
			fs += laplaceMinP
			if s == 0 {
				fl += fs
			}
		}
	}
	if fl+fs > laplaceFS {
		fs = laplaceFS - fl
	}
	// Use EncodeBin with 15 bits (laplaceFS = 1 << 15 = 32768) to match libopus ec_encode_bin
	// This is critical for bit-exact encoding since EncodeBin uses shift (rng >> bits)
	// instead of division (rng / ft) which can give different results.
	re.EncodeBin(uint32(fl), uint32(fl+fs), laplaceFTBits)
	return val
}

// EncodeFineEnergy encodes fine energy refinement bits.
// This adds fractional precision to coarse energy values.
// fineBits[band] specifies bits allocated for refinement (0 = no refinement).
//
// This mirrors decoder's DecodeFineEnergy exactly (in reverse).
//
// Reference: RFC 6716 Section 4.3.2, libopus celt/quant_bands.c quant_fine_energy()
func (e *Encoder) EncodeFineEnergy(energies []float64, quantizedCoarse []float64, nbBands int, fineBits []int) {
	if e.rangeEncoder == nil {
		return
	}
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands > len(fineBits) {
		nbBands = len(fineBits)
	}

	channels := e.channels
	if len(energies) < nbBands*channels {
		channels = 1
	}

	re := e.rangeEncoder

	for band := 0; band < nbBands; band++ {
		bits := fineBits[band]
		if bits <= 0 {
			continue
		}

		ft := 1 << bits
		scale32 := float32(ft)
		for c := 0; c < channels; c++ {
			idx := c*nbBands + band
			if idx >= len(energies) || idx >= len(quantizedCoarse) {
				continue
			}

			// Keep fine-energy quantization in float32 precision to mirror libopus float path.
			fine := float32(energies[idx] - quantizedCoarse[idx])

			// Quantize to fineBits[band] levels
			q := int(math.Floor(float64((fine + 0.5) * scale32)))

			// Clamp to valid range
			if q < 0 {
				q = 0
			}
			if q >= ft {
				q = ft - 1
			}

			// Encode raw bits to match decoder
			re.EncodeRawBits(uint32(q), uint(bits))

			// Apply decoded offset to quantized energies for remainder coding
			offset := (float32(q)+0.5)/scale32 - 0.5
			quantizedCoarse[idx] = float64(float32(quantizedCoarse[idx]) + offset)
		}
	}
}

// encodeFineEnergyFromError mirrors libopus quant_fine_energy() with prev_quant=NULL.
// It consumes and updates errorVals in-place so the same residual state can be used
// by energy finalisation and next-frame energyError clipping.
func (e *Encoder) encodeFineEnergyFromError(quantizedEnergies []float64, nbBands int, fineBits []int, errorVals []float64) {
	if e.rangeEncoder == nil {
		return
	}
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands > len(fineBits) {
		nbBands = len(fineBits)
	}

	channels := e.channels
	if len(quantizedEnergies) < nbBands*channels || len(errorVals) < nbBands*channels {
		channels = 1
	}

	re := e.rangeEncoder

	for band := 0; band < nbBands; band++ {
		bits := fineBits[band]
		if bits <= 0 {
			continue
		}
		// Match libopus quant_fine_energy(): if there is not enough storage
		// left to code this band's fine bits for all channels, skip the band.
		if re.Tell()+channels*bits > re.StorageBits() {
			continue
		}
		extra := 1 << bits
		scale32 := float32(extra)
		for c := 0; c < channels; c++ {
			idx := c*nbBands + band
			if idx >= len(quantizedEnergies) || idx >= len(errorVals) {
				continue
			}

			// libopus float: q2 = floor((error + 0.5) * extra)
			err := float32(errorVals[idx])
			qExpr := float64((err + 0.5) * scale32)
			if tmpFineQEpsEnabled {
				qExpr -= tmpFineQEpsValue
			}
			q2 := int(math.Floor(qExpr))
			if q2 < 0 {
				q2 = 0
			}
			if q2 > extra-1 {
				q2 = extra - 1
			}

			re.EncodeRawBits(uint32(q2), uint(bits))

			offset := (float32(q2)+0.5)/scale32 - 0.5
			quantizedEnergies[idx] = float64(float32(quantizedEnergies[idx]) + offset)
			errorVals[idx] = float64(err - offset)
			if tmpFineDumpEnabled && e.frameCount >= 74 && e.frameCount <= 80 && band == 18 && c == 0 {
				println("FINE_DUMP frame", e.frameCount, "band", band, "bits", bits, "err", err, "q2", q2, "offset", offset, "qexpr", qExpr)
			}
		}
	}
}

// EncodeFineEnergyRange encodes fine energies for bands in [start, end).
func (e *Encoder) EncodeFineEnergyRange(energies []float64, quantizedCoarse []float64, start, end int, fineBits []int) {
	if e.rangeEncoder == nil {
		return
	}
	if start < 0 {
		start = 0
	}
	if end > MaxBands {
		end = MaxBands
	}
	if end <= start {
		return
	}

	nbBands := end
	if nbBands > len(fineBits) {
		nbBands = len(fineBits)
	}

	channels := e.channels
	if len(energies) < nbBands*channels {
		channels = 1
	}

	re := e.rangeEncoder

	for band := start; band < nbBands; band++ {
		bits := fineBits[band]
		if bits <= 0 {
			continue
		}

		ft := 1 << bits
		scale32 := float32(ft)
		for c := 0; c < channels; c++ {
			idx := c*nbBands + band
			if idx >= len(energies) || idx >= len(quantizedCoarse) {
				continue
			}

			fine := float32(energies[idx] - quantizedCoarse[idx])
			q := int(math.Floor(float64((fine + 0.5) * scale32)))

			if q < 0 {
				q = 0
			}
			if q >= ft {
				q = ft - 1
			}

			re.EncodeRawBits(uint32(q), uint(bits))

			offset := (float32(q)+0.5)/scale32 - 0.5
			quantizedCoarse[idx] = float64(float32(quantizedCoarse[idx]) + offset)
		}
	}
}

// EncodeFineEnergyRangeFromError mirrors libopus quant_fine_energy() for hybrid
// range coding, consuming the in-place coarse error residual state.
func (e *Encoder) EncodeFineEnergyRangeFromError(quantizedEnergies []float64, start, end int, fineBits []int) {
	if e.rangeEncoder == nil {
		return
	}
	if start < 0 {
		start = 0
	}
	if end > MaxBands {
		end = MaxBands
	}
	if end <= start {
		return
	}

	nbBands := end
	if nbBands > len(fineBits) {
		nbBands = len(fineBits)
	}
	channels := e.channels
	if channels < 1 {
		channels = 1
	}

	required := nbBands * channels
	if len(quantizedEnergies) < required {
		channels = 1
		required = nbBands
	}

	errorVals := ensureFloat64Slice(&e.scratch.coarseError, required)
	re := e.rangeEncoder

	for band := start; band < nbBands; band++ {
		bits := fineBits[band]
		if bits <= 0 {
			continue
		}
		// Match libopus: if there is not enough storage left for this band's
		// fine bits for all channels, skip this band.
		if re.Tell()+channels*bits > re.StorageBits() {
			continue
		}
		extra := 1 << bits
		scale32 := float32(extra)
		for c := 0; c < channels; c++ {
			idx := c*nbBands + band
			if idx >= len(quantizedEnergies) || idx >= len(errorVals) {
				continue
			}

			err := float32(errorVals[idx])
			q2 := int(math.Floor(float64((err + 0.5) * scale32)))
			if q2 < 0 {
				q2 = 0
			}
			if q2 > extra-1 {
				q2 = extra - 1
			}

			re.EncodeRawBits(uint32(q2), uint(bits))

			offset := (float32(q2)+0.5)/scale32 - 0.5
			quantizedEnergies[idx] = float64(float32(quantizedEnergies[idx]) + offset)
			errorVals[idx] = float64(err - offset)
		}
	}
}

// EncodeEnergyRemainder encodes any leftover precision bits.
// Called after PVQ bands decoded, uses leftover bits from bit allocation.
// This mirrors decoder's DecodeEnergyRemainder exactly (in reverse).
//
// Reference: RFC 6716 Section 4.3.2, libopus celt/quant_bands.c quant_energy_finalise()
func (e *Encoder) EncodeEnergyRemainder(energies []float64, quantizedEnergies []float64, nbBands int, remainderBits []int) {
	if e.rangeEncoder == nil {
		return
	}
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands > len(remainderBits) {
		nbBands = len(remainderBits)
	}

	channels := e.channels
	if len(energies) < nbBands*channels {
		channels = 1
	}

	for c := 0; c < channels; c++ {
		for band := 0; band < nbBands; band++ {
			bits := remainderBits[band]
			if bits <= 0 {
				continue
			}

			idx := c*nbBands + band
			if idx >= len(energies) || idx >= len(quantizedEnergies) {
				continue
			}

			// Compute residual from already-quantized energy
			residual := energies[idx] - quantizedEnergies[idx]

			// Each bit provides finer precision
			// Encode single bit for each remainder bit
			for i := 0; i < bits && i < 8; i++ {
				// Precision for this bit level
				precision := DB6 / float64(uint(1)<<(i+2))

				// Decide bit based on sign of residual
				var bit int
				if residual > 0 {
					bit = 1
				} else {
					bit = 0
				}

				// Encode the bit
				e.rangeEncoder.EncodeBit(bit, 1)

				// Update residual based on decision
				if bit == 1 {
					residual -= precision
				} else {
					residual += precision
				}
			}
		}
	}
}

// EncodeEnergyFinalise consumes leftover bits for additional energy refinement.
// This mirrors decoder's DecodeEnergyFinalise (libopus quant_energy_finalise).
// energies: original target energies
// quantizedEnergies: current quantized energies (coarse + fine)
// fineQuant/finePriority: allocation outputs
// bitsLeft: remaining whole bits available in the packet
func (e *Encoder) EncodeEnergyFinalise(energies []float64, quantizedEnergies []float64, nbBands int, fineQuant []int, finePriority []int, bitsLeft int) {
	if e.rangeEncoder == nil {
		return
	}
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands <= 0 {
		return
	}
	if bitsLeft < 0 {
		bitsLeft = 0
	}

	channels := e.channels
	if len(energies) < nbBands*channels || len(quantizedEnergies) < nbBands*channels {
		channels = 1
	}

	re := e.rangeEncoder

	for prio := 0; prio < 2; prio++ {
		for band := 0; band < nbBands && bitsLeft >= channels; band++ {
			if band >= len(fineQuant) || band >= len(finePriority) {
				continue
			}
			if fineQuant[band] >= maxFineBits || finePriority[band] != prio {
				continue
			}
			for c := 0; c < channels; c++ {
				idx := c*nbBands + band
				if idx >= len(energies) || idx >= len(quantizedEnergies) {
					continue
				}
				errorVal := float32(energies[idx] - quantizedEnergies[idx])
				q2 := 0
				if errorVal >= 0 {
					q2 = 1
				}
				re.EncodeRawBits(uint32(q2), 1)
				offset := (float32(q2) - 0.5) / float32(uint(1)<<(fineQuant[band]+1))
				quantizedEnergies[idx] = float64(float32(quantizedEnergies[idx]) + offset)
				bitsLeft--
			}
		}
	}
}

// encodeEnergyFinaliseFromError mirrors libopus quant_energy_finalise().
// It consumes the remaining bit budget using the in-place residual state.
func (e *Encoder) encodeEnergyFinaliseFromError(quantizedEnergies []float64, nbBands int, fineQuant []int, finePriority []int, bitsLeft int, errorVals []float64) {
	if e.rangeEncoder == nil {
		return
	}
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands <= 0 {
		return
	}
	if bitsLeft < 0 {
		bitsLeft = 0
	}

	channels := e.channels
	if len(quantizedEnergies) < nbBands*channels || len(errorVals) < nbBands*channels {
		channels = 1
	}

	re := e.rangeEncoder

	for prio := 0; prio < 2; prio++ {
		for band := 0; band < nbBands && bitsLeft >= channels; band++ {
			if band >= len(fineQuant) || band >= len(finePriority) {
				continue
			}
			if fineQuant[band] >= maxFineBits || finePriority[band] != prio {
				continue
			}
			for c := 0; c < channels; c++ {
				idx := c*nbBands + band
				if idx >= len(quantizedEnergies) || idx >= len(errorVals) {
					continue
				}

				q2 := 0
				if float32(errorVals[idx]) >= 0 {
					q2 = 1
				}
				re.EncodeRawBits(uint32(q2), 1)

				offset := (float32(q2) - 0.5) / float32(uint(1)<<(fineQuant[band]+1))
				quantizedEnergies[idx] = float64(float32(quantizedEnergies[idx]) + offset)
				errorVals[idx] = float64(float32(errorVals[idx]) - offset)
				bitsLeft--
			}
		}
	}
}

// EncodeEnergyFinaliseRange consumes leftover bits for energy refinement in [start, end).
func (e *Encoder) EncodeEnergyFinaliseRange(energies []float64, quantizedEnergies []float64, start, end int, fineQuant []int, finePriority []int, bitsLeft int) {
	if e.rangeEncoder == nil {
		return
	}
	if start < 0 {
		start = 0
	}
	if end > MaxBands {
		end = MaxBands
	}
	if end <= start {
		return
	}
	if bitsLeft < 0 {
		bitsLeft = 0
	}

	nbBands := end
	channels := e.channels
	if len(energies) < nbBands*channels || len(quantizedEnergies) < nbBands*channels {
		channels = 1
	}

	re := e.rangeEncoder

	for prio := 0; prio < 2; prio++ {
		for band := start; band < nbBands && bitsLeft >= channels; band++ {
			if band >= len(fineQuant) || band >= len(finePriority) {
				continue
			}
			if fineQuant[band] >= maxFineBits || finePriority[band] != prio {
				continue
			}
			for c := 0; c < channels; c++ {
				idx := c*nbBands + band
				if idx >= len(energies) || idx >= len(quantizedEnergies) {
					continue
				}
				errorVal := float32(energies[idx] - quantizedEnergies[idx])
				q2 := 0
				if errorVal >= 0 {
					q2 = 1
				}
				re.EncodeRawBits(uint32(q2), 1)
				offset := (float32(q2) - 0.5) / float32(uint(1)<<(fineQuant[band]+1))
				quantizedEnergies[idx] = float64(float32(quantizedEnergies[idx]) + offset)
				bitsLeft--
			}
		}
	}
}

// EncodeEnergyFinaliseRangeFromError mirrors libopus quant_energy_finalise() for
// hybrid range coding, consuming the in-place residual state from coarse/fine.
func (e *Encoder) EncodeEnergyFinaliseRangeFromError(quantizedEnergies []float64, start, end int, fineQuant []int, finePriority []int, bitsLeft int) {
	if e.rangeEncoder == nil {
		return
	}
	if start < 0 {
		start = 0
	}
	if end > MaxBands {
		end = MaxBands
	}
	if end <= start {
		return
	}
	if bitsLeft < 0 {
		bitsLeft = 0
	}

	nbBands := end
	channels := e.channels
	if channels < 1 {
		channels = 1
	}

	required := nbBands * channels
	if len(quantizedEnergies) < required {
		channels = 1
		required = nbBands
	}

	errorVals := ensureFloat64Slice(&e.scratch.coarseError, required)
	re := e.rangeEncoder

	for prio := 0; prio < 2; prio++ {
		for band := start; band < nbBands && bitsLeft >= channels; band++ {
			if band >= len(fineQuant) || band >= len(finePriority) {
				continue
			}
			if fineQuant[band] >= maxFineBits || finePriority[band] != prio {
				continue
			}
			for c := 0; c < channels; c++ {
				idx := c*nbBands + band
				if idx >= len(quantizedEnergies) || idx >= len(errorVals) {
					continue
				}

				q2 := 0
				if float32(errorVals[idx]) >= 0 {
					q2 = 1
				}
				re.EncodeRawBits(uint32(q2), 1)

				offset := (float32(q2) - 0.5) / float32(uint(1)<<(fineQuant[band]+1))
				quantizedEnergies[idx] = float64(float32(quantizedEnergies[idx]) + offset)
				errorVals[idx] = float64(float32(errorVals[idx]) - offset)
				bitsLeft--
			}
		}
	}
}

// EncodeCoarseEnergyWithEncoder encodes coarse energies using an explicit range encoder.
// This variant allows passing a range encoder directly rather than using e.rangeEncoder.
func (e *Encoder) EncodeCoarseEnergyWithEncoder(re *rangecoding.Encoder, energies []float64, nbBands int, intra bool, lm int) []float64 {
	oldRE := e.rangeEncoder
	e.rangeEncoder = re
	defer func() { e.rangeEncoder = oldRE }()

	return e.EncodeCoarseEnergy(energies, nbBands, intra, lm)
}

// EncodeFineEnergyWithEncoder encodes fine energies using an explicit range encoder.
func (e *Encoder) EncodeFineEnergyWithEncoder(re *rangecoding.Encoder, energies []float64, quantizedCoarse []float64, nbBands int, fineBits []int) {
	oldRE := e.rangeEncoder
	e.rangeEncoder = re
	defer func() { e.rangeEncoder = oldRE }()

	e.EncodeFineEnergy(energies, quantizedCoarse, nbBands, fineBits)
}

// EncodeEnergyRemainderWithEncoder encodes remainder bits using an explicit range encoder.
func (e *Encoder) EncodeEnergyRemainderWithEncoder(re *rangecoding.Encoder, energies []float64, quantizedEnergies []float64, nbBands int, remainderBits []int) {
	oldRE := e.rangeEncoder
	e.rangeEncoder = re
	defer func() { e.rangeEncoder = oldRE }()

	e.EncodeEnergyRemainder(energies, quantizedEnergies, nbBands, remainderBits)
}

// EncodeCoarseEnergyHybrid encodes coarse energies for hybrid mode.
// Only encodes bands from startBand onwards (typically band 17).
func (e *Encoder) EncodeCoarseEnergyHybrid(energies []float64, nbBands int, intra bool, lm int, startBand int) []float64 {
	if e.rangeEncoder == nil || nbBands == 0 {
		return make([]float64, nbBands*e.channels)
	}

	return e.EncodeCoarseEnergyRange(energies, startBand, nbBands, intra, lm)
}

// EncodeFineEnergyHybrid encodes fine energies for hybrid mode.
// Only encodes bands from startBand onwards.
func (e *Encoder) EncodeFineEnergyHybrid(energies []float64, quantizedCoarse []float64, nbBands int, fineBits []int, startBand int) {
	if e.rangeEncoder == nil || nbBands == 0 {
		return
	}

	e.EncodeFineEnergyRange(energies, quantizedCoarse, startBand, nbBands, fineBits)
}
