package celt

import (
	"fmt"

	"github.com/thesyncim/gopus/rangecoding"
)

// DecodeFrame decodes a complete CELT frame from raw bytes.
// If data is nil or empty, performs Packet Loss Concealment (PLC) instead of decoding.
// data: raw CELT frame bytes (without Opus framing), or nil/empty for PLC
// frameSize: expected output samples (120, 240, 480, or 960)
// Returns: PCM samples as float64 slice, interleaved if stereo
//
// The decoding pipeline:
// 1. Initialize range decoder
// 2. Decode frame header flags (silence, transient, intra)
// 3. Decode energy envelope (coarse + fine)
// 4. Compute bit allocation
// 5. Decode bands via PVQ
// 6. Synthesis: IMDCT + windowing + overlap-add
// 7. Apply de-emphasis filter
//
// Reference: RFC 6716 Section 4.3, libopus celt/celt_decoder.c celt_decode_with_ec()
func (d *Decoder) DecodeFrame(data []byte, frameSize int) ([]float64, error) {
	// Track channel count for transition detection (normal decode uses decoder's channels)
	d.handleChannelTransition(d.channels)
	qextPayload := d.takeQEXTPayload()

	// Handle PLC for nil/empty data (lost packet)
	if data == nil || len(data) == 0 {
		return d.decodePLC(frameSize)
	}

	setup, err := d.prepareDecodeFrame(data, frameSize)
	if err != nil {
		return nil, err
	}
	start := 0
	rd := setup.rd
	mode := setup.mode
	lm := setup.lm
	end := setup.end
	prev1Energy := setup.prev1Energy
	prev1LogE := setup.prev1LogE
	prev2LogE := setup.prev2LogE

	totalBits := len(data) * 8
	tell := rd.Tell()
	silence := false
	if tell >= totalBits {
		silence = true
	} else if tell == 1 {
		silence = rd.DecodeBit(15) == 1
	}
	if silence {
		return d.handleDecodedSilenceFrame(frameSize, lm, prev1Energy, rd), nil
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

	// Determine short blocks for transient mode
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
	qext := d.prepareQEXTDecode(qextPayload, rd, end, lm, frameSize)
	if qext != nil {
		d.decodeFineEnergyWithDecoderPrev(qext.dec, energies, end, fineQuant, qext.extraQuant[:end])
		if tmpQEXTHeaderDumpEnabled {
			fmt.Printf("QEXT_MAIN_FINE_DEC channels=%d tell=%d\n", d.channels, qext.dec.TellFrac())
		}
	}
	traceRange("fine", rd)

	coeffsL, coeffsR, collapse := quantAllBandsDecodeWithScratch(rd, d.channels, frameSize, lm, start, end, pulses, shortBlocks, spread,
		dualStereo, intensity, tfRes, (totalBits<<bitRes)-antiCollapseRsv, balance, codedBands, d.channels == 1, &d.rng, &d.scratchBands, &d.bandDebug,
		func() *rangecoding.Decoder {
			if qext == nil {
				return nil
			}
			return qext.dec
		}(), func() []int {
			if qext == nil {
				return nil
			}
			return qext.extraPulses[:end]
		}(), func() int {
			if qext == nil {
				return 0
			}
			return qext.totalBitsQ3
		}())
	if qext != nil {
		d.decodeQEXTBands(frameSize, lm, shortBlocks, spread, d.channels == 1, qext)
	}
	traceRange("pvq", rd)

	antiCollapseOn := false
	if antiCollapseRsv > 0 {
		antiCollapseOn = rd.DecodeRawBits(1) == 1
	}
	traceFlag("anticollapse_on", boolToInt(antiCollapseOn))
	traceRange("anticollapse", rd)

	bitsLeft := totalBits - rd.Tell()
	if len(qextPayload) != 0 {
		d.DecodeEnergyFinaliseRange(start, end, nil, fineQuant, finePriority, bitsLeft)
	} else {
		d.DecodeEnergyFinalise(energies, end, fineQuant, finePriority, bitsLeft)
	}
	traceRange("finalise", rd)

	if antiCollapseOn {
		antiCollapse(coeffsL, coeffsR, collapse, lm, d.channels, start, end, energies, prev1LogE, prev2LogE, pulses, d.rng)
	}
	d.applyPendingPLCPrefilterAndFold()
	samples := d.synthesizeDecodedFrame(frameSize, mode.LM, end, lm, shortBlocks, transient, postfilterPeriod, postfilterGain, postfilterTapset, energies, coeffsL, coeffsR, qext)
	if err := d.finalizeDecodedFrameState(frameSize, start, end, lm, transient, energies, prev1Energy, qext, rd); err != nil {
		return nil, err
	}
	return samples, nil
}
