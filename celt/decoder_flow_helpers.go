package celt

import "github.com/thesyncim/gopus/rangecoding"

func (d *Decoder) synthesizeDecodedFrame(frameSize, modeLM, end, lm, shortBlocks int, transient bool, postfilterPeriod int, postfilterGain float64, postfilterTapset int, energies, coeffsL, coeffsR []float64, qext *preparedQEXTDecode) []float64 {
	// Step 6: Synthesis (IMDCT + window + overlap-add)
	var samples []float64
	directStereoFloat32 := d.channels == 2 && len(d.directOutPCM) >= frameSize*2
	directMonoFloat32 := d.channels == 1 &&
		len(d.directOutPCM) >= frameSize &&
		!transient &&
		d.postfilterGainOld == 0 &&
		d.postfilterGain == 0 &&
		postfilterGain == 0

	if d.channels == 2 {
		energiesL := energies[:end]
		energiesR := energies[end:]
		if qext != nil && qext.end > 0 {
			specL := ensureFloat64Slice(&d.scratchQEXTSpectrumL, len(coeffsL))
			specR := ensureFloat64Slice(&d.scratchQEXTSpectrumR, len(coeffsR))
			denormalizeBandsPackedInto(specL, coeffsL, energiesL, 0, end, lm, EBands[:])
			denormalizeBandsPackedInto(specR, coeffsR, energiesR, 0, end, lm, EBands[:])
			if qext.coeffsL != nil {
				denormalizeBandsPackedInto(specL, qext.coeffsL, qext.energies[:qext.end], 0, qext.end, lm, qext.cfg.EBands)
			}
			if qext.coeffsR != nil {
				denormalizeBandsPackedInto(specR, qext.coeffsR, qext.energies[qext.end:], 0, qext.end, lm, qext.cfg.EBands)
			}
			coeffsL = specL
			coeffsR = specR
		} else {
			denormalizeCoeffs(coeffsL, energiesL, end, frameSize)
			denormalizeCoeffs(coeffsR, energiesR, end, frameSize)
		}
		if directStereoFloat32 {
			samplesL, samplesR := d.synthesizeStereoPlanar(coeffsL, coeffsR, transient, shortBlocks)
			d.applyPostfilterStereoPlanar(samplesL, samplesR, frameSize, modeLM, postfilterPeriod, postfilterGain, postfilterTapset)
			d.applyDeemphasisAndScaleStereoPlanarToFloat32(d.directOutPCM[:frameSize*2], samplesL, samplesR, 1.0/32768.0)
		} else {
			samples = d.SynthesizeStereo(coeffsL, coeffsR, transient, shortBlocks)
		}
	} else {
		if qext != nil && qext.end > 0 {
			specL := ensureFloat64Slice(&d.scratchQEXTSpectrumL, len(coeffsL))
			denormalizeBandsPackedInto(specL, coeffsL, energies, 0, end, lm, EBands[:])
			if qext.coeffsL != nil {
				denormalizeBandsPackedInto(specL, qext.coeffsL, qext.energies[:qext.end], 0, qext.end, lm, qext.cfg.EBands)
			}
			coeffsL = specL
		} else {
			denormalizeCoeffs(coeffsL, energies, end, frameSize)
		}
		if directMonoFloat32 {
			samplesF32 := d.synthesizeMonoLongToFloat32(coeffsL)
			d.applyPostfilterNoGainMonoFromFloat32(samplesF32, frameSize, modeLM, postfilterPeriod, postfilterGain, postfilterTapset)
			d.applyDeemphasisAndScaleMonoFloat32ToFloat32(d.directOutPCM[:frameSize], samplesF32, 1.0/32768.0)
		} else {
			samples = d.Synthesize(coeffsL, transient, shortBlocks)
		}
	}

	if directStereoFloat32 || directMonoFloat32 {
		return samples
	}

	// Trace synthesis output before postfilter/de-emphasis for libopus comparison.
	traceLen := len(samples)
	if traceLen > 16 {
		traceLen = 16
	}

	d.applyPostfilter(samples, frameSize, modeLM, postfilterPeriod, postfilterGain, postfilterTapset)

	// Step 7: Apply de-emphasis filter
	if len(d.directOutPCM) >= len(samples) {
		d.applyDeemphasisAndScaleToFloat32(d.directOutPCM[:len(samples)], samples, 1.0/32768.0)
	} else {
		d.applyDeemphasisAndScale(samples, 1.0/32768.0)
	}

	// Trace final synthesis output
	traceLen = len(samples)
	if traceLen > 16 {
		traceLen = 16
	}

	return samples
}

func (d *Decoder) finalizeDecodedFrameState(frameSize, start, end, lm int, transient bool, energies, prev1Energy []float64, qext *preparedQEXTDecode, rd *rangecoding.Decoder) error {
	// Update energy state for next frame.
	d.updateLogE(energies, end, transient)
	d.SetPrevEnergyWithPrev(prev1Energy, energies)
	d.updateBackgroundEnergy(lm)

	// Mirror libopus: clear energies/logs outside [start,end).
	d.clearFrameHistoryOutsideRange(start, end, d.channels)
	if qext != nil && qext.dec.Tell() > qext.dec.StorageBits() {
		return ErrInvalidFrame
	}

	var extDec *rangecoding.Decoder
	if qext != nil {
		extDec = qext.dec
	}
	d.rng = combineFinalRange(rd, extDec)

	// Reset PLC state after successful decode.
	d.resetPLCCadence(frameSize, d.channels)
	return nil
}

func (d *Decoder) clearFrameHistoryOutsideRange(start, end, channels int) {
	for c := 0; c < channels; c++ {
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
}
