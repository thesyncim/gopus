package celt

import (
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// HybridCELTStartBand is the first CELT band decoded in hybrid mode.
// Bands 0-16 are covered by SILK; CELT only decodes bands 17-21.
const HybridCELTStartBand = 17

// decodeFrameHybrid decodes a CELT frame for hybrid mode.
// In hybrid mode, CELT only decodes bands 17-21 (frequencies above ~8kHz).
// The range decoder should already have been partially consumed by SILK.
//
// Parameters:
//   - rd: Range decoder (SILK has already consumed its portion)
//   - frameSize: Expected output samples (480 or 960 for hybrid 10ms/20ms)
//
// Returns: PCM samples as float32 slice at 48kHz
//
// Reference: RFC 6716 Section 3.2 (Hybrid mode), libopus celt/celt_decoder.c
// celt_decode_with_ec() with st->start = 17.
func (d *Decoder) decodeFrameHybrid(rd *rangecoding.Decoder, frameSize int) ([]float32, error) {
	if rd == nil {
		return nil, ErrNilDecoder
	}

	// Hybrid only supports 10ms (480) and 20ms (960) frames
	if frameSize != 480 && frameSize != 960 {
		return nil, ErrInvalidFrameSize
	}

	d.beginDecodedPacketPLCState()
	d.SetRangeDecoder(rd)
	d.prepareMonoEnergyFromStereo()
	var qextPayload []byte
	if extsupport.QEXT {
		qextPayload = d.takeQEXTPayload()
	}

	mode := GetModeConfig(frameSize)
	lm := mode.LM
	end := min(EffectiveBandsForFrameSize(d.bandwidth, frameSize), mode.EffBands)
	if end < 1 {
		end = 1
	}
	start := HybridCELTStartBand
	prev1Energy, prev1LogE, prev2LogE := d.snapshotDecodeHistory()

	totalBits := rd.StorageBits()
	silence := decodeSilenceFlag(rd, totalBits)
	header := d.decodeFrameHeader(rd, totalBits, frameSize, start, end, lm, mode.ShortBlocks)

	// Initialize energies with previous state so bands below start are preserved.
	channels := int(d.channels)
	energies := ensureGLogSlice(&d.scratchEnergies, end*channels)
	for c := range channels {
		for band := 0; band < end; band++ {
			energies[c*end+band] = d.prevEnergy[c*MaxBands+band]
		}
	}
	d.decodeCoarseEnergyRangeGLog(start, end, header.intra, lm, energies)

	allocation := d.decodeBandAllocation(rd, totalBits, start, end, lm, header.transient)
	coeffsL, coeffsR, qext := d.decodeHybridSpectrum(qextPayload, rd, totalBits, frameSize, start, end, lm, header.shortBlocks, allocation.spread, allocation.antiCollapseRsv, channels, d.phaseInversionDisabled, energies, prev1LogE, prev2LogE,
		allocation.pulses, allocation.fineQuant, allocation.finePriority, allocation.tfRes, allocation.intensity, allocation.dualStereo, allocation.balance, allocation.codedBands)
	if silence {
		applyDecodedSilence(energies, coeffsL, coeffsR)
	}

	hybridBinStart := ScaledBandStart(HybridCELTStartBand, frameSize)
	d.applyPendingPLCPrefilterAndFold()
	samples := d.synthesizeHybridDecodedFrame(frameSize, mode.LM, end, hybridBinStart, header.shortBlocks, header.transient, header.postfilterPeriod, header.postfilterGain, header.postfilterTapset, energies, coeffsL, coeffsR, qext)
	if err := d.finalizeDecodedFrameState(frameSize, start, end, lm, header.transient, energies, prev1Energy, qext, rd); err != nil {
		return nil, err
	}
	return samples, nil
}
