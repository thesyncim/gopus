package celt

import (
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/rangecoding"
)

func (d *Decoder) decodeHybridSpectrum(qextPayload []byte, rd *rangecoding.Decoder, totalBits, frameSize, start, end, lm, shortBlocks, spread, antiCollapseRsv, channels int, disableInv bool, energies, prev1LogE, prev2LogE []float64, pulses, fineQuant, finePriority, tfRes []int, intensity, dualStereo, balance, codedBands int) (coeffsL, coeffsR []float64, qext *preparedQEXTDecode) {
	d.DecodeFineEnergyRange(energies, start, end, fineQuant)
	if extsupport.QEXT {
		qext = d.prepareQEXTDecodeRange(qextPayload, rd, start, end, lm, frameSize)
	}
	if extsupport.QEXT && qext != nil {
		oldRD := d.rangeDecoder
		d.rangeDecoder = qext.dec
		d.decodeFineEnergyRange(energies, start, end, fineQuant, qext.extraQuant)
		d.rangeDecoder = oldRD
	}

	var extDec *rangecoding.Decoder
	var extPulses []int
	extTotalBitsQ3 := 0
	if extsupport.QEXT && qext != nil {
		extDec = qext.dec
		extPulses = qext.extraPulses[:end]
		extTotalBitsQ3 = qext.totalBitsQ3
	}
	coeffsL, coeffsR, collapse := quantAllBandsDecodeWithScratch(rd, channels, frameSize, lm, start, end, pulses, shortBlocks, spread,
		dualStereo, intensity, tfRes, (totalBits<<bitRes)-antiCollapseRsv, balance, codedBands, disableInv, &d.rng, &d.scratchBands,
		extDec, extPulses, extTotalBitsQ3)
	if extsupport.QEXT && qext != nil {
		d.decodeQEXTBands(frameSize, lm, shortBlocks, spread, disableInv, qext)
	}

	antiCollapseOn := false
	if antiCollapseRsv > 0 {
		antiCollapseOn = rd.DecodeRawBits(1) == 1
	}

	bitsLeft := totalBits - rd.Tell()
	// Hybrid finalisation only runs over the decoded CELT tail bands.
	if extsupport.QEXT && qext != nil {
		d.DecodeEnergyFinaliseRange(start, end, nil, fineQuant, finePriority, bitsLeft)
	} else {
		d.DecodeEnergyFinaliseRange(start, end, energies, fineQuant, finePriority, bitsLeft)
	}

	if antiCollapseOn {
		antiCollapse(coeffsL, coeffsR, collapse, lm, channels, start, end, energies, prev1LogE, prev2LogE, pulses, d.rng)
	}

	return coeffsL, coeffsR, qext
}

func (d *Decoder) synthesizeHybridDecodedFrame(frameSize, modeLM, end, hybridBinStart, shortBlocks int, transient bool, postfilterPeriod int, postfilterGain float64, postfilterTapset int, energies, coeffsL, coeffsR []float64, qext *preparedQEXTDecode) []float64 {
	var samples []float64
	if d.channels == 2 {
		energiesL := energies[:end]
		energiesR := energies[end:]
		if extsupport.QEXT && qext != nil && qext.end > 0 {
			qextState := d.ensureQEXTState()
			specL := ensureFloat64Slice(&qextState.scratchSpectrumL, len(coeffsL))
			specR := ensureFloat64Slice(&qextState.scratchSpectrumR, len(coeffsR))
			denormalizeBandsPackedInto(specL, coeffsL, energiesL, HybridCELTStartBand, end, modeLM, EBands[:])
			denormalizeBandsPackedInto(specR, coeffsR, energiesR, HybridCELTStartBand, end, modeLM, EBands[:])
			if qext.coeffsL != nil {
				denormalizeBandsPackedInto(specL, qext.coeffsL, qext.energies[:qext.end], 0, qext.end, modeLM, qext.cfg.EBands)
			}
			if qext.coeffsR != nil {
				denormalizeBandsPackedInto(specR, qext.coeffsR, qext.energies[qext.end:], 0, qext.end, modeLM, qext.cfg.EBands)
			}
			coeffsL = specL
			coeffsR = specR
		} else {
			denormalizeCoeffs(coeffsL, energiesL, end, frameSize)
			denormalizeCoeffs(coeffsR, energiesR, end, frameSize)
			for i := 0; i < hybridBinStart && i < len(coeffsL); i++ {
				coeffsL[i] = 0
			}
			for i := 0; i < hybridBinStart && i < len(coeffsR); i++ {
				coeffsR[i] = 0
			}
		}
		if !transient && len(d.directOutPCM) >= frameSize*2 {
			samplesL, samplesR := d.synthesizeStereoPlanarLongToFloat32(coeffsL, coeffsR)
			if d.postfilterGainOld == 0 && d.postfilterGain == 0 && postfilterGain == 0 {
				d.applyPostfilterNoGainStereoPlanarFromFloat32(samplesL[:frameSize], samplesR[:frameSize], frameSize, modeLM, postfilterPeriod, postfilterGain, postfilterTapset)
			} else {
				d.applyPostfilterStereoPlanarFromFloat32(samplesL[:frameSize], samplesR[:frameSize], frameSize, modeLM, postfilterPeriod, postfilterGain, postfilterTapset)
			}
			d.applyDeemphasisAndScaleStereoPlanarFloat32ToFloat32(d.directOutPCM[:frameSize*2], samplesL[:frameSize], samplesR[:frameSize], 1.0/32768.0)
			return nil
		}
		samples = d.SynthesizeStereo(coeffsL, coeffsR, transient, shortBlocks)
	} else {
		if extsupport.QEXT && qext != nil && qext.end > 0 {
			qextState := d.ensureQEXTState()
			specL := ensureFloat64Slice(&qextState.scratchSpectrumL, len(coeffsL))
			denormalizeBandsPackedInto(specL, coeffsL, energies, HybridCELTStartBand, end, modeLM, EBands[:])
			if qext.coeffsL != nil {
				denormalizeBandsPackedInto(specL, qext.coeffsL, qext.energies[:qext.end], 0, qext.end, modeLM, qext.cfg.EBands)
			}
			coeffsL = specL
		} else {
			denormalizeCoeffs(coeffsL, energies, end, frameSize)
			for i := 0; i < hybridBinStart && i < len(coeffsL); i++ {
				coeffsL[i] = 0
			}
		}
		if !transient &&
			len(d.directOutPCM) >= frameSize &&
			d.postfilterGainOld == 0 &&
			d.postfilterGain == 0 &&
			postfilterGain == 0 {
			samplesF32 := d.synthesizeMonoLongToFloat32(coeffsL)
			d.applyPostfilterNoGainMonoFromFloat32(samplesF32, frameSize, modeLM, postfilterPeriod, postfilterGain, postfilterTapset)
			d.applyDeemphasisAndScaleMonoFloat32ToFloat32(d.directOutPCM[:frameSize], samplesF32, 1.0/32768.0)
			return nil
		}
		samples = d.Synthesize(coeffsL, transient, shortBlocks)
	}

	d.applyPostfilter(samples, frameSize, modeLM, postfilterPeriod, postfilterGain, postfilterTapset)
	d.applyDeemphasisAndScale(samples, 1.0/32768.0)
	return samples
}
