// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides energy encoding functions that mirror the decoder.

package celt

import (
	"math"

	"github.com/thesyncim/gopus/internal/rangecoding"
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

	energies := make([]float64, nbBands*channels)
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

	return energies
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
	sumSq := float32(1e-27)
	for i := start; i < end; i++ {
		v := float32(coeffs[i])
		sumSq += v * v
	}

	// log2(sqrt(sumSq)) = 0.5 * log2(sumSq)
	amp := float32(math.Sqrt(float64(sumSq)))
	return float64(float32(math.Log2(float64(amp))))
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

	quantizedEnergies := make([]float64, nbBands*channels)

	// Prediction coefficients (libopus quant_coarse_energy_impl).
	var coef, beta float64
	if intra {
		coef = 0.0
		beta = BetaIntra
	} else {
		coef = AlphaCoef[lm]
		beta = BetaCoefInter[lm]
	}

	prob := eProbModel[lm][0]
	if intra {
		prob = eProbModel[lm][1]
	}

	budget := e.rangeEncoder.StorageBits()
	if e.frameBits > 0 && e.frameBits < budget {
		budget = e.frameBits
	}

	// Max decay bound (libopus uses nbAvailableBytes-based clamp).
	maxDecay := 16.0 * DB6
	nbAvailableBytes := budget / 8
	if nbBands > 10 {
		limit := 0.125 * float64(nbAvailableBytes) * DB6
		if limit < maxDecay {
			maxDecay = limit
		}
	}

	prevBandEnergy := make([]float64, channels)
	for band := 0; band < nbBands; band++ {
		for c := 0; c < channels; c++ {
			idx := c*nbBands + band
			if idx >= len(energies) {
				continue
			}

			x := energies[idx]

			// Previous frame energy (for prediction and decay bound).
			oldEBand := e.prevEnergy[c*MaxBands+band]
			oldE := oldEBand
			minEnergy := -9.0 * DB6
			if oldE < minEnergy {
				oldE = minEnergy
			}

			// Prediction residual.
			f := x - coef*oldE - prevBandEnergy[c]
			qi := int(math.Floor(f/DB6 + 0.5))

			// Prevent energy from decaying too quickly.
			decayBound := math.Max(-28.0*DB6, oldEBand) - maxDecay
			if qi < 0 && x < decayBound {
				adjust := int((decayBound - x) / DB6)
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
				// Encoder (inverse): qi=0 -> s=0, qi=-1 -> s=1, qi=1 -> s=2
				var s int
				if qi < 0 {
					s = -2*qi - 1 // For qi=-1: s = 2 - 1 = 1
				} else {
					s = 2 * qi // For qi=0: s=0, qi=1: s=2
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

			q := float64(qi) * DB6
			quantizedEnergy := coef*oldE + prevBandEnergy[c] + q
			quantizedEnergies[idx] = quantizedEnergy

			// Update inter-band predictor.
			prevBandEnergy[c] = prevBandEnergy[c] + q - beta*q
		}
	}

	// Update previous-frame energy state after encoding completes.
	for c := 0; c < channels; c++ {
		for band := 0; band < nbBands; band++ {
			idx := c*nbBands + band
			if idx >= len(quantizedEnergies) {
				continue
			}
			if band < MaxBands {
				e.prevEnergy[c*MaxBands+band] = quantizedEnergies[idx]
			}
		}
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
		scale := float64(ft)
		for c := 0; c < channels; c++ {
			idx := c*nbBands + band
			if idx >= len(energies) || idx >= len(quantizedCoarse) {
				continue
			}

			// Compute residual: fine = energy - quantizedCoarse
			fine := energies[idx] - quantizedCoarse[idx]

			// Quantize to fineBits[band] levels
			q := int(math.Floor((fine/DB6+0.5)*scale + 1e-9))

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
			offset := (float64(q)+0.5)/scale - 0.5
			quantizedCoarse[idx] += offset * DB6
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
				errorVal := energies[idx] - quantizedEnergies[idx]
				q2 := 0
				if errorVal >= 0 {
					q2 = 1
				}
				re.EncodeRawBits(uint32(q2), 1)
				offset := (float64(q2) - 0.5) / float64(uint(1)<<(fineQuant[band]+1))
				quantizedEnergies[idx] += offset * DB6
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

// absInt returns the absolute value of an integer.
// Note: we use absInt to avoid conflict with abs in cwrs.go
func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// EncodeCoarseEnergyHybrid encodes coarse energies for hybrid mode.
// Only encodes bands from startBand onwards (typically band 17).
func (e *Encoder) EncodeCoarseEnergyHybrid(energies []float64, nbBands int, intra bool, lm int, startBand int) []float64 {
	if e.rangeEncoder == nil || nbBands == 0 {
		return make([]float64, nbBands*e.channels)
	}

	// For hybrid mode, simply delegate to the regular encode for the relevant bands
	// and zero out the lower bands that aren't encoded
	quantized := e.EncodeCoarseEnergy(energies, nbBands, intra, lm)

	// Zero out bands below startBand (they're handled by SILK)
	for c := 0; c < e.channels; c++ {
		for i := 0; i < startBand && i < nbBands; i++ {
			idx := c*nbBands + i
			if idx < len(quantized) {
				quantized[idx] = 0
			}
		}
	}

	return quantized
}

// EncodeFineEnergyHybrid encodes fine energies for hybrid mode.
// Only encodes bands from startBand onwards.
func (e *Encoder) EncodeFineEnergyHybrid(energies []float64, quantizedCoarse []float64, nbBands int, fineBits []int, startBand int) {
	if e.rangeEncoder == nil || nbBands == 0 {
		return
	}

	// For hybrid mode, encode fine bits only for bands from startBand onwards
	// This delegates to the regular fine energy encoding
	e.EncodeFineEnergy(energies, quantizedCoarse, nbBands, fineBits)
}
