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

	header := d.decodeFrameHeader(rd, totalBits, frameSize, start, end, lm, mode.ShortBlocks)
	postfilterGain := header.postfilterGain
	postfilterPeriod := header.postfilterPeriod
	postfilterTapset := header.postfilterTapset
	transient := header.transient
	intra := header.intra
	shortBlocks := header.shortBlocks

	// Step 1: Decode coarse energy
	energies := d.decodeCoarseEnergyInto(ensureFloat64Slice(&d.scratchEnergies, end*d.channels), end, intra, lm)
	traceRange("coarse", rd)

	allocation := d.decodeBandAllocation(rd, totalBits, start, end, lm, transient)
	tfRes := allocation.tfRes
	spread := allocation.spread
	antiCollapseRsv := allocation.antiCollapseRsv
	pulses := allocation.pulses
	fineQuant := allocation.fineQuant
	finePriority := allocation.finePriority
	intensity := allocation.intensity
	dualStereo := allocation.dualStereo
	balance := allocation.balance
	codedBands := allocation.codedBands

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
