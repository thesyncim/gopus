package celt

import "github.com/thesyncim/gopus/rangecoding"

// DecodeFrameWithDecoder decodes a frame using a pre-initialized range decoder.
// This is useful when the range decoder is shared with other layers (e.g., SILK in hybrid mode).
func (d *Decoder) DecodeFrameWithDecoder(rd *rangecoding.Decoder, frameSize int) ([]float64, error) {
	if rd == nil {
		return nil, ErrNilDecoder
	}

	if !ValidFrameSize(frameSize) {
		return nil, ErrInvalidFrameSize
	}

	// Keep transition/state behavior aligned with DecodeFrame().
	d.handleChannelTransition(d.channels)
	d.prepareMonoEnergyFromStereo()

	d.SetRangeDecoder(rd)

	// Get mode configuration
	mode := GetModeConfig(frameSize)
	lm := mode.LM
	end := EffectiveBandsForFrameSize(d.bandwidth, frameSize)
	if end > mode.EffBands {
		end = mode.EffBands
	}
	if end < 1 {
		end = 1
	}
	start := 0
	prev1Energy := ensureFloat64Slice(&d.scratchPrevEnergy, len(d.prevEnergy))
	copy(prev1Energy, d.prevEnergy)
	prev1LogE := ensureFloat64Slice(&d.scratchPrevLogE, len(d.prevLogE))
	copy(prev1LogE, d.prevLogE)
	prev2LogE := ensureFloat64Slice(&d.scratchPrevLogE2, len(d.prevLogE2))
	copy(prev2LogE, d.prevLogE2)

	totalBits := rd.StorageBits()
	tell := rd.Tell()
	silence := false
	if tell >= totalBits {
		silence = true
	} else if tell == 1 {
		silence = rd.DecodeBit(15) == 1
	}
	if silence {
		samples := d.decodeSilenceFrame(frameSize, 0, 0, 0)
		silenceE := ensureFloat64Slice(&d.scratchSilenceE, MaxBands*d.channels)
		for i := range silenceE {
			silenceE[i] = -28.0
		}
		d.updateLogE(silenceE, MaxBands, false)
		d.SetPrevEnergyWithPrev(prev1Energy, silenceE)
		d.updateBackgroundEnergy(lm)
		traceHeader(frameSize, d.channels, lm, 0, 0)
		traceEnergy(0, 0, 0, 0)
		traceLen := len(samples)
		if traceLen > 16 {
			traceLen = 16
		}
		if traceLen > 0 {
			traceSynthesis("final", samples[:traceLen])
		}
		d.resetPLCCadence(frameSize, d.channels)
		d.rng = rd.Range()
		return samples, nil
	}

	postfilterGain := 0.0
	postfilterPeriod := 0
	postfilterTapset := 0
	if start == 0 && tell+16 <= totalBits {
		if rd.DecodeBit(1) == 1 {
			octave := int(rd.DecodeUniform(6))
			postfilterPeriod = (16 << octave) + int(rd.DecodeRawBits(uint(4+octave))) - 1
			qg := int(rd.DecodeRawBits(3))
			if rd.Tell()+2 <= totalBits {
				postfilterTapset = rd.DecodeICDF(tapsetICDF, 2)
			}
			postfilterGain = 0.09375 * float64(qg+1)
		}
		tell = rd.Tell()
	}
	traceRange("postfilter", rd)

	transient := false
	if lm > 0 && tell+3 <= totalBits {
		transient = rd.DecodeBit(3) == 1
		tell = rd.Tell()
	}
	intra := false
	if tell+3 <= totalBits {
		intra = rd.DecodeBit(3) == 1
	}
	traceRange("intra", rd)

	// Trace frame header
	traceHeader(frameSize, d.channels, lm, boolToInt(intra), boolToInt(transient))
	d.applyLossEnergySafety(intra, start, end, lm)

	shortBlocks := 1
	if transient {
		shortBlocks = mode.ShortBlocks
	}

	// Step 1: Decode coarse energy
	energies := d.decodeCoarseEnergyInto(ensureFloat64Slice(&d.scratchEnergies, end*d.channels), end, intra, lm)
	traceRange("coarse", rd)

	tfRes := ensureIntSlice(&d.scratchTFRes, end)
	tfDecode(start, end, transient, tfRes, lm, rd)
	traceRange("tf", rd)

	spread := spreadNormal
	tell = rd.Tell()
	if tell+4 <= totalBits {
		spread = rd.DecodeICDF(spreadICDF, 5)
	}
	traceFlag("spread", spread)
	traceRange("spread", rd)

	cap := ensureIntSlice(&d.scratchCaps, end)
	initCapsInto(cap, end, lm, d.channels)
	offsets := ensureIntSlice(&d.scratchOffsets, end)
	dynallocLogp := 6
	totalBitsQ3 := totalBits << bitRes
	tellFrac := rd.TellFrac()
	for i := start; i < end; i++ {
		width := d.channels * (EBands[i+1] - EBands[i]) << lm
		quanta := min(width<<bitRes, max(6<<bitRes, width))
		dynallocLoopLogp := dynallocLogp
		boost := 0
		j := 0
		for ; tellFrac+(dynallocLoopLogp<<bitRes) < totalBitsQ3 && boost < cap[i]; j++ {
			flag := rd.DecodeBit(uint(dynallocLoopLogp))
			tellFrac = rd.TellFrac()
			if flag == 0 {
				break
			}
			boost += quanta
			totalBitsQ3 -= quanta
			dynallocLoopLogp = 1
		}
		offsets[i] = boost
		traceAllocation(i, boost, -1)
		if j > 0 {
			dynallocLogp = max(2, dynallocLogp-1)
		}
	}
	traceRange("dynalloc", rd)

	allocTrim := 5
	encodedTrim := tellFrac+(6<<bitRes) <= totalBitsQ3
	if encodedTrim {
		allocTrim = rd.DecodeICDF(trimICDF, 7)
	}
	traceFlag("alloc_trim", allocTrim)
	traceRange("trim", rd)

	bitsQ3 := (totalBits << bitRes) - rd.TellFrac() - 1
	antiCollapseRsv := 0
	if transient && lm >= 2 && bitsQ3 >= (lm+2)<<bitRes {
		antiCollapseRsv = 1 << bitRes
	}
	bitsQ3 -= antiCollapseRsv

	pulses := ensureIntSlice(&d.scratchPulses, end)
	fineQuant := ensureIntSlice(&d.scratchFineQuant, end)
	finePriority := ensureIntSlice(&d.scratchFinePriority, end)
	intensity := 0
	dualStereo := 0
	balance := 0
	allocScratch := d.allocationScratch()
	codedBands := cltComputeAllocationWithScratch(start, end, offsets, cap, allocTrim, &intensity, &dualStereo,
		bitsQ3, &balance, pulses, fineQuant, finePriority, d.channels, lm, rd, allocScratch)
	traceRange("alloc", rd)

	for i := start; i < end; i++ {
		width := 0
		if i+1 < len(EBands) {
			width = (EBands[i+1] - EBands[i]) << lm
		}
		k := 0
		if width > 0 {
			k = bitsToK(pulses[i], width)
		}
		traceAllocation(i, pulses[i], k)
		traceFineBits(i, fineQuant[i])
	}

	d.DecodeFineEnergy(energies, end, fineQuant)
	traceRange("fine", rd)

	coeffsL, coeffsR, collapse := quantAllBandsDecodeWithScratch(rd, d.channels, frameSize, lm, start, end, pulses, shortBlocks, spread,
		dualStereo, intensity, tfRes, (totalBits<<bitRes)-antiCollapseRsv, balance, codedBands, d.channels == 1, &d.rng, &d.scratchBands, &d.bandDebug, nil, nil, 0)
	traceRange("pvq", rd)

	antiCollapseOn := false
	if antiCollapseRsv > 0 {
		antiCollapseOn = rd.DecodeRawBits(1) == 1
	}
	traceFlag("anticollapse_on", boolToInt(antiCollapseOn))
	traceRange("anticollapse", rd)

	bitsLeft := totalBits - rd.Tell()
	d.DecodeEnergyFinalise(energies, end, fineQuant, finePriority, bitsLeft)
	traceRange("finalise", rd)

	if antiCollapseOn {
		antiCollapse(coeffsL, coeffsR, collapse, lm, d.channels, start, end, energies, prev1LogE, prev2LogE, pulses, d.rng)
	}
	d.applyPendingPLCPrefilterAndFold()

	// Step 6: Synthesis (IMDCT + window + overlap-add)
	var samples []float64
	if d.channels == 2 {
		energiesL := energies[:end]
		energiesR := energies[end:]
		denormalizeCoeffs(coeffsL, energiesL, end, frameSize)
		denormalizeCoeffs(coeffsR, energiesR, end, frameSize)
		samples = d.SynthesizeStereo(coeffsL, coeffsR, transient, shortBlocks)
	} else {
		denormalizeCoeffs(coeffsL, energies, end, frameSize)
		samples = d.Synthesize(coeffsL, transient, shortBlocks)
	}

	// Trace synthesis output before postfilter/de-emphasis for libopus comparison.
	traceLen := len(samples)
	if traceLen > 16 {
		traceLen = 16
	}
	traceSynthesis("synth_pre", samples[:traceLen])

	d.applyPostfilter(samples, frameSize, mode.LM, postfilterPeriod, postfilterGain, postfilterTapset)
	d.applyDeemphasisAndScale(samples, 1.0/32768.0)

	// Trace final synthesis output
	traceLen = len(samples)
	if traceLen > 16 {
		traceLen = 16
	}
	traceSynthesis("final", samples[:traceLen])

	// Update energy state for next frame
	d.updateLogE(energies, end, transient)
	d.SetPrevEnergyWithPrev(prev1Energy, energies)
	d.updateBackgroundEnergy(lm)
	// Mirror libopus: clear energies/logs outside [start,end).
	for c := 0; c < d.channels; c++ {
		base := c * MaxBands
		for band := 0; band < start; band++ {
			d.prevEnergy[base+band] = 0
			d.prevLogE[base+band] = -28.0
			d.prevLogE2[base+band] = -28.0
		}
		for band := end; band < MaxBands; band++ {
			d.prevEnergy[base+band] = 0
			d.prevLogE[base+band] = -28.0
			d.prevLogE2[base+band] = -28.0
		}
	}
	d.rng = rd.Range()

	// Reset PLC state after successful decode
	d.resetPLCCadence(frameSize, d.channels)

	return samples, nil
}

// HybridCELTStartBand is the first CELT band decoded in hybrid mode.
// Bands 0-16 are covered by SILK; CELT only decodes bands 17-21.
const HybridCELTStartBand = 17

// DecodeFrameHybrid decodes a CELT frame for hybrid mode.
// In hybrid mode, CELT only decodes bands 17-21 (frequencies above ~8kHz).
// The range decoder should already have been partially consumed by SILK.
//
// Parameters:
//   - rd: Range decoder (SILK has already consumed its portion)
//   - frameSize: Expected output samples (480 or 960 for hybrid 10ms/20ms)
//
// Returns: PCM samples as float64 slice at 48kHz
//
// Implementation approach:
// - Decode all bands as usual but zero out bands 0-16 before synthesis
// - This ensures correct operation with the existing synthesis pipeline
// - Only bands 17-21 contribute to the output (high frequencies for hybrid)
//
// Reference: RFC 6716 Section 3.2 (Hybrid mode), libopus celt/celt_decoder.c
func (d *Decoder) DecodeFrameHybrid(rd *rangecoding.Decoder, frameSize int) ([]float64, error) {
	if rd == nil {
		return nil, ErrNilDecoder
	}

	// Hybrid only supports 10ms (480) and 20ms (960) frames
	if frameSize != 480 && frameSize != 960 {
		return nil, ErrInvalidFrameSize
	}

	d.SetRangeDecoder(rd)
	d.prepareMonoEnergyFromStereo()

	// Get mode configuration
	mode := GetModeConfig(frameSize)
	lm := mode.LM
	end := EffectiveBandsForFrameSize(d.bandwidth, frameSize)
	if end > mode.EffBands {
		end = mode.EffBands
	}
	if end < 1 {
		end = 1
	}
	start := HybridCELTStartBand
	prev1Energy := ensureFloat64Slice(&d.scratchPrevEnergy, len(d.prevEnergy))
	copy(prev1Energy, d.prevEnergy)
	prev1LogE := ensureFloat64Slice(&d.scratchPrevLogE, len(d.prevLogE))
	copy(prev1LogE, d.prevLogE)
	prev2LogE := ensureFloat64Slice(&d.scratchPrevLogE2, len(d.prevLogE2))
	copy(prev2LogE, d.prevLogE2)

	totalBits := rd.StorageBits()
	tell := rd.Tell()
	silence := false
	if tell >= totalBits {
		silence = true
	} else if tell == 1 {
		silence = rd.DecodeBit(15) == 1
	}
	if silence {
		samples := d.decodeSilenceFrame(frameSize, 0, 0, 0)
		silenceE := ensureFloat64Slice(&d.scratchSilenceE, MaxBands*d.channels)
		for i := range silenceE {
			silenceE[i] = -28.0
		}
		d.updateLogE(silenceE, MaxBands, false)
		d.SetPrevEnergyWithPrev(prev1Energy, silenceE)
		d.updateBackgroundEnergy(lm)
		d.rng = rd.Range()
		d.resetPLCCadence(frameSize, d.channels)
		return samples, nil
	}

	postfilterGain := 0.0
	postfilterPeriod := 0
	postfilterTapset := 0
	if start == 0 && tell+16 <= totalBits {
		if rd.DecodeBit(1) == 1 {
			octave := int(rd.DecodeUniform(6))
			postfilterPeriod = (16 << octave) + int(rd.DecodeRawBits(uint(4+octave))) - 1
			qg := int(rd.DecodeRawBits(3))
			if rd.Tell()+2 <= totalBits {
				postfilterTapset = rd.DecodeICDF(tapsetICDF, 2)
			}
			postfilterGain = 0.09375 * float64(qg+1)
		}
		tell = rd.Tell()
	}
	traceRange("postfilter", rd)

	transient := false
	if lm > 0 && tell+3 <= totalBits {
		transient = rd.DecodeBit(3) == 1
		tell = rd.Tell()
	}
	intra := false
	if tell+3 <= totalBits {
		intra = rd.DecodeBit(3) == 1
	}
	traceRange("intra", rd)
	d.applyLossEnergySafety(intra, start, end, lm)

	shortBlocks := 1
	if transient {
		shortBlocks = mode.ShortBlocks
	}

	// Initialize energies with previous state so bands below start are preserved.
	energies := ensureFloat64Slice(&d.scratchEnergies, end*d.channels)
	for c := 0; c < d.channels; c++ {
		for band := 0; band < end; band++ {
			energies[c*end+band] = d.prevEnergy[c*MaxBands+band]
		}
	}
	d.decodeCoarseEnergyRange(start, end, intra, lm, energies)
	traceRange("coarse", rd)

	tfRes := ensureIntSlice(&d.scratchTFRes, end)
	tfDecode(start, end, transient, tfRes, lm, rd)
	traceRange("tf", rd)

	spread := spreadNormal
	tell = rd.Tell()
	if tell+4 <= totalBits {
		spread = rd.DecodeICDF(spreadICDF, 5)
	}
	traceFlag("spread", spread)
	traceRange("spread", rd)

	cap := ensureIntSlice(&d.scratchCaps, end)
	initCapsInto(cap, end, lm, d.channels)
	offsets := ensureIntSlice(&d.scratchOffsets, end)
	for i := range offsets {
		offsets[i] = 0
	}
	dynallocLogp := 6
	totalBitsQ3 := totalBits << bitRes
	tellFrac := rd.TellFrac()
	for i := start; i < end; i++ {
		width := d.channels * (EBands[i+1] - EBands[i]) << lm
		quanta := min(width<<bitRes, max(6<<bitRes, width))
		dynallocLoopLogp := dynallocLogp
		boost := 0
		j := 0
		for ; tellFrac+(dynallocLoopLogp<<bitRes) < totalBitsQ3 && boost < cap[i]; j++ {
			flag := rd.DecodeBit(uint(dynallocLoopLogp))
			tellFrac = rd.TellFrac()
			if flag == 0 {
				break
			}
			boost += quanta
			totalBitsQ3 -= quanta
			dynallocLoopLogp = 1
		}
		offsets[i] = boost
		traceAllocation(i, boost, -1)
		if j > 0 {
			dynallocLogp = max(2, dynallocLogp-1)
		}
	}
	traceRange("dynalloc", rd)

	allocTrim := 5
	if tellFrac+(6<<bitRes) <= totalBitsQ3 {
		allocTrim = rd.DecodeICDF(trimICDF, 7)
	}
	traceFlag("alloc_trim", allocTrim)
	traceRange("trim", rd)

	bitsQ3 := (totalBits << bitRes) - rd.TellFrac() - 1
	antiCollapseRsv := 0
	if transient && lm >= 2 && bitsQ3 >= (lm+2)<<bitRes {
		antiCollapseRsv = 1 << bitRes
	}
	bitsQ3 -= antiCollapseRsv

	pulses := ensureIntSlice(&d.scratchPulses, end)
	fineQuant := ensureIntSlice(&d.scratchFineQuant, end)
	finePriority := ensureIntSlice(&d.scratchFinePriority, end)
	intensity := 0
	dualStereo := 0
	balance := 0
	allocScratch := d.allocationScratch()
	codedBands := cltComputeAllocationWithScratch(start, end, offsets, cap, allocTrim, &intensity, &dualStereo,
		bitsQ3, &balance, pulses, fineQuant, finePriority, d.channels, lm, rd, allocScratch)
	traceRange("alloc", rd)

	d.DecodeFineEnergy(energies, end, fineQuant)
	traceRange("fine", rd)

	coeffsL, coeffsR, collapse := quantAllBandsDecodeWithScratch(rd, d.channels, frameSize, lm, start, end, pulses, shortBlocks, spread,
		dualStereo, intensity, tfRes, (totalBits<<bitRes)-antiCollapseRsv, balance, codedBands, d.channels == 1, &d.rng, &d.scratchBands, &d.bandDebug, nil, nil, 0)
	traceRange("pvq", rd)

	antiCollapseOn := false
	if antiCollapseRsv > 0 {
		antiCollapseOn = rd.DecodeRawBits(1) == 1
	}
	traceFlag("anticollapse_on", boolToInt(antiCollapseOn))
	traceRange("anticollapse", rd)

	bitsLeft := totalBits - rd.Tell()
	// Use DecodeEnergyFinaliseRange with start=HybridCELTStartBand to match libopus.
	// libopus unquant_energy_finalise() loops from start to end, not from 0.
	d.DecodeEnergyFinaliseRange(start, end, energies, fineQuant, finePriority, bitsLeft)
	traceRange("finalise", rd)

	if antiCollapseOn {
		antiCollapse(coeffsL, coeffsR, collapse, lm, d.channels, start, end, energies, prev1LogE, prev2LogE, pulses, d.rng)
	}

	hybridBinStart := ScaledBandStart(HybridCELTStartBand, frameSize)
	d.applyPendingPLCPrefilterAndFold()
	var samples []float64
	if d.channels == 2 {
		energiesL := energies[:end]
		energiesR := energies[end:]
		denormalizeCoeffs(coeffsL, energiesL, end, frameSize)
		denormalizeCoeffs(coeffsR, energiesR, end, frameSize)
		for i := 0; i < hybridBinStart && i < len(coeffsL); i++ {
			coeffsL[i] = 0
		}
		for i := 0; i < hybridBinStart && i < len(coeffsR); i++ {
			coeffsR[i] = 0
		}
		samples = d.SynthesizeStereo(coeffsL, coeffsR, transient, shortBlocks)
	} else {
		denormalizeCoeffs(coeffsL, energies, end, frameSize)
		for i := 0; i < hybridBinStart && i < len(coeffsL); i++ {
			coeffsL[i] = 0
		}
		samples = d.Synthesize(coeffsL, transient, shortBlocks)
	}

	d.applyPostfilter(samples, frameSize, mode.LM, postfilterPeriod, postfilterGain, postfilterTapset)

	d.applyDeemphasisAndScale(samples, 1.0/32768.0)
	d.updateLogE(energies, end, transient)
	d.SetPrevEnergyWithPrev(prev1Energy, energies)
	d.updateBackgroundEnergy(lm)
	// Mirror libopus: clear energies/logs outside [start,end).
	for c := 0; c < d.channels; c++ {
		base := c * MaxBands
		for band := 0; band < start; band++ {
			d.prevEnergy[base+band] = 0
			d.prevLogE[base+band] = -28.0
			d.prevLogE2[base+band] = -28.0
		}
		for band := end; band < MaxBands; band++ {
			d.prevEnergy[base+band] = 0
			d.prevLogE[base+band] = -28.0
			d.prevLogE2[base+band] = -28.0
		}
	}
	d.rng = rd.Range()
	d.resetPLCCadence(frameSize, d.channels)

	return samples, nil
}
