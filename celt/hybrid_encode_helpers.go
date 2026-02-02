package celt

import "github.com/thesyncim/gopus/rangecoding"

// NormalizeBandsToArrayMonoWithBandE normalizes MDCT coefficients for mono
// and returns the normalized coefficients and linear band amplitudes.
func (e *Encoder) NormalizeBandsToArrayMonoWithBandE(mdctCoeffs []float64, nbBands, frameSize int) (norm []float64, bandE []float64) {
	norm = ensureFloat64Slice(&e.scratch.normL, frameSize)
	bandE = ensureFloat64Slice(&e.scratch.bandE, nbBands)
	NormalizeBandsToArrayInto(mdctCoeffs, nbBands, frameSize, norm, bandE)
	return norm, bandE
}

// NormalizeBandsToArrayStereoWithBandE normalizes MDCT coefficients for stereo
// and returns normalized L/R coefficients plus combined linear band amplitudes.
// The bandE layout is [L bands][R bands].
func (e *Encoder) NormalizeBandsToArrayStereoWithBandE(mdctLeft, mdctRight []float64, nbBands, frameSize int) (normL, normR, bandE []float64) {
	normL = ensureFloat64Slice(&e.scratch.normL, frameSize)
	normR = ensureFloat64Slice(&e.scratch.normR, frameSize)
	bandEL := ensureFloat64Slice(&e.scratch.bandEL, nbBands)
	bandER := ensureFloat64Slice(&e.scratch.bandER, nbBands)
	NormalizeBandsToArrayInto(mdctLeft, nbBands, frameSize, normL, bandEL)
	NormalizeBandsToArrayInto(mdctRight, nbBands, frameSize, normR, bandER)
	bandE = ensureFloat64Slice(&e.scratch.bandE, nbBands*2)
	copy(bandE[:nbBands], bandEL)
	copy(bandE[nbBands:], bandER)
	return normL, normR, bandE
}

// TFResScratch returns a scratch TF resolution slice sized for nbBands.
func (e *Encoder) TFResScratch(nbBands int) []int {
	return ensureIntSlice(&e.scratch.tfRes, nbBands)
}

// CapsScratch returns a scratch caps slice sized for nbBands.
func (e *Encoder) CapsScratch(nbBands int) []int {
	return ensureIntSlice(&e.scratch.caps, nbBands)
}

// OffsetsScratch returns a scratch offsets slice sized for nbBands.
func (e *Encoder) OffsetsScratch(nbBands int) []int {
	return ensureIntSlice(&e.scratch.offsets, nbBands)
}

// ComputeAllocationHybridScratch computes hybrid bit allocation using encoder scratch.
// This mirrors ComputeAllocationHybrid but avoids per-call allocations.
func (e *Encoder) ComputeAllocationHybridScratch(re *rangecoding.Encoder, totalBitsQ3, nbBands int, cap, offsets []int, trim int, intensity int, dualStereo bool, lm int, prev int, signalBandwidth int) *AllocationResult {
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands < 0 {
		nbBands = 0
	}
	channels := e.channels
	if channels < 1 {
		channels = 1
	}
	if channels > 2 {
		channels = 2
	}
	if lm < 0 {
		lm = 0
	}
	if lm > 3 {
		lm = 3
	}

	result := &e.scratch.allocResult
	result.BandBits = ensureIntSlice(&e.scratch.allocBits, nbBands)
	result.FineBits = ensureIntSlice(&e.scratch.allocFineBits, nbBands)
	result.FinePriority = ensureIntSlice(&e.scratch.allocFinePrio, nbBands)
	result.Caps = ensureIntSlice(&e.scratch.allocCaps, nbBands)
	result.Balance = 0
	result.CodedBands = nbBands
	result.Intensity = 0
	result.DualStereo = false

	for i := 0; i < nbBands; i++ {
		result.BandBits[i] = 0
		result.FineBits[i] = 0
		result.FinePriority[i] = 0
		result.Caps[i] = 0
	}

	if nbBands == 0 || totalBitsQ3 <= 0 {
		return result
	}

	if cap == nil || len(cap) < nbBands {
		cap = initCaps(nbBands, lm, channels)
	}
	copy(result.Caps, cap[:nbBands])

	if offsets == nil {
		offsets = e.scratch.offsets[:nbBands]
		for i := range offsets {
			offsets[i] = 0
		}
	}

	intensityVal := intensity
	dualVal := 0
	if dualStereo {
		dualVal = 1
	}
	balance := 0
	pulses := result.BandBits
	fineBits := result.FineBits
	finePriority := result.FinePriority

	codedBands := cltComputeAllocationEncode(re, HybridCELTStartBand, nbBands, offsets, cap, trim, &intensityVal, &dualVal,
		totalBitsQ3, &balance, pulses, fineBits, finePriority, channels, lm, prev, signalBandwidth)

	result.CodedBands = codedBands
	result.Balance = balance
	result.Intensity = intensityVal
	result.DualStereo = dualVal != 0

	return result
}

// QuantAllBandsEncodeScratch encodes PVQ bands using the encoder's scratch buffers.
func (e *Encoder) QuantAllBandsEncodeScratch(re *rangecoding.Encoder, channels, frameSize, lm int, start, end int,
	normL, normR []float64, pulses []int, shortBlocks int, spread int, tapset int, dualStereo int, intensity int,
	tfRes []int, totalBitsQ3 int, balance int, codedBands int, seed *uint32, complexity int, bandE []float64) {
	quantAllBandsEncodeScratch(
		re,
		channels,
		frameSize,
		lm,
		start,
		end,
		normL,
		normR,
		pulses,
		shortBlocks,
		spread,
		tapset,
		dualStereo,
		intensity,
		tfRes,
		totalBitsQ3,
		balance,
		codedBands,
		seed,
		complexity,
		bandE,
		nil,
		nil,
		&e.bandEncScratch,
	)
}

// LastCodedBands returns the last coded band count used for allocation skip decisions.
func (e *Encoder) LastCodedBands() int {
	return e.lastCodedBands
}

// SetLastCodedBands updates the last coded band count.
func (e *Encoder) SetLastCodedBands(val int) {
	e.lastCodedBands = val
}

// ConsecTransient returns the number of consecutive transient frames.
func (e *Encoder) ConsecTransient() int {
	return e.consecTransient
}

// UpdateConsecTransient updates the consecutive transient counter.
func (e *Encoder) UpdateConsecTransient(transient bool) {
	if transient {
		e.consecTransient++
	} else {
		e.consecTransient = 0
	}
}
