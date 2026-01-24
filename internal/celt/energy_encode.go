// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides energy encoding functions that mirror the decoder.

package celt

import (
	"math"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

// ComputeBandEnergies computes energy for each frequency band from MDCT coefficients.
// Returns energies in log2 scale (same as decoder expects).
// energies[c*nbBands + band] = log2(RMS energy of band for channel c)
//
// The energy computation extracts loudness per frequency band:
// 1. For each band, sum squares of MDCT coefficients
// 2. Divide by band width to get average power
// 3. Convert to log2 scale: energy = 0.5 * log2(sumSq / width)
//
// This mirrors the decoder's denormalization which scales bands by 2^energy.
//
// Reference: RFC 6716 Section 4.3.2, libopus celt/bands.c
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
				energies[c*nbBands+band] = -28.0 // Minimum energy (D03-01-01)
				continue
			}
			if end > len(channelCoeffs) {
				end = len(channelCoeffs)
			}
			if end <= start {
				energies[c*nbBands+band] = -28.0
				continue
			}

			// Compute RMS energy in log2 scale
			energy := computeBandRMS(channelCoeffs, start, end)
			energies[c*nbBands+band] = energy
		}
	}

	return energies
}

// computeBandRMS computes the log2-scale energy of coefficients in [start, end).
// Returns energy = 0.5 * log2(sumSq / width) = log2(RMS)
// For zero input, returns -28.0 (minimum energy per D03-01-01).
func computeBandRMS(coeffs []float64, start, end int) float64 {
	if end <= start || start < 0 || end > len(coeffs) {
		return -28.0
	}

	// Compute sum of squares
	sumSq := 0.0
	for i := start; i < end; i++ {
		sumSq += coeffs[i] * coeffs[i]
	}

	// Handle zero energy
	if sumSq < 1e-30 {
		return -28.0 // Minimum energy (D03-01-01)
	}

	// Compute band width
	width := float64(end - start)

	// Energy in log2 scale: energy = log2(sqrt(sumSq/width)) = 0.5 * log2(sumSq/width)
	// Using change of base: log2(x) = ln(x) / ln(2)
	energy := 0.5 * math.Log2(sumSq/width)

	// Clamp to valid range
	if energy < -28.0 {
		energy = -28.0
	}
	if energy > 16.0 {
		energy = 16.0 // Reasonable upper limit
	}

	return energy
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

	quantizedEnergies := make([]float64, len(energies))
	copy(quantizedEnergies, energies)

	// Get prediction coefficients (same as decoder)
	var alpha, beta float64
	if intra {
		// Intra-frame: no inter-frame prediction, only inter-band
		alpha = 0.0
		beta = BetaIntra // Fixed 0.15 for intra mode
	} else {
		// Inter-frame: use both alpha (previous frame) and beta (previous band)
		alpha = AlphaCoef[lm]
		beta = BetaCoefInter[lm] // LM-dependent for inter mode
	}

	prob := eProbModel[lm][0]
	if intra {
		prob = eProbModel[lm][1]
	}

	// Encode for each channel
	channels := e.channels
	if len(energies) < nbBands*channels {
		channels = 1
	}

	prevBandEnergy := make([]float64, channels)
	for band := 0; band < nbBands; band++ {
		for c := 0; c < channels; c++ {
			idx := c*nbBands + band
			if idx >= len(energies) {
				continue
			}

			energy := energies[idx]

			// Compute prediction (same formula as decoder)
			prevFrameEnergy := e.prevEnergy[c*MaxBands+band]
			pred := alpha*prevFrameEnergy + prevBandEnergy[c]

			// Compute residual and quantize to 6dB steps
			residual := energy - pred
			qi := int(math.Round(residual / DB6))

			// Encode with Laplace model
			pi := 2 * band
			if pi > 40 {
				pi = 40
			}
			fs := int(prob[pi]) << 7
			decay := int(prob[pi+1]) << 6
			qi = e.encodeLaplace(qi, fs, decay)

			// Compute quantized energy
			quantizedEnergy := pred + float64(qi)*DB6
			quantizedEnergies[idx] = quantizedEnergy

			// Update prev band energy for next band's inter-band prediction
			// Per libopus: prevBandEnergy accumulates a filtered version of quantized deltas
			// Formula: prev = prev + q - beta*q, where q = qi*DB6
			q := float64(qi) * DB6
			prevBandEnergy[c] = prevBandEnergy[c] + q - beta*q
		}
	}

	// Update previous frame energy for next frame's inter-frame prediction
	for c := 0; c < channels; c++ {
		for band := 0; band < nbBands && band < MaxBands; band++ {
			idx := c*nbBands + band
			if idx < len(quantizedEnergies) {
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
	re.Encode(uint32(fl), uint32(fl+fs), uint32(laplaceFS))
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
