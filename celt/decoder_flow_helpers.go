package celt

import (
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/rangecoding"
)

func (d *Decoder) synthesizeDecodedFrame(frameSize, modeLM, end, lm, shortBlocks int, transient bool, postfilterPeriod int, postfilterGain float64, postfilterTapset int, energies, coeffsL, coeffsR []float64, qext *preparedQEXTDecode) []float64 {
	// Step 6: Synthesis (IMDCT + window + overlap-add)
	var samples []float64
	downsample := d.downsampleFactor()
	outputFrameSize := frameSize
	downsampleOutput := false
	if downsample > 1 && frameSize%downsample == 0 {
		apiFrameSize := frameSize / downsample
		if len(d.directOutPCM) < frameSize*d.channels && len(d.directOutPCM) >= apiFrameSize*d.channels {
			outputFrameSize = apiFrameSize
			downsampleOutput = true
		}
	}
	directStereoFloat32 := d.channels == 2 && len(d.directOutPCM) >= outputFrameSize*2
	directMonoFloat32 := d.channels == 1 &&
		len(d.directOutPCM) >= outputFrameSize &&
		!transient &&
		d.postfilterGainOld == 0 &&
		d.postfilterGain == 0 &&
		postfilterGain == 0

	if d.channels == 2 {
		energiesL := energies[:end]
		energiesR := energies[end:]
		if extsupport.QEXT && qext != nil && qext.end > 0 {
			qextState := d.ensureQEXTState()
			specL := ensureFloat64Slice(&qextState.scratchSpectrumL, len(coeffsL))
			specR := ensureFloat64Slice(&qextState.scratchSpectrumR, len(coeffsR))
			denormalizeBandsPackedDownsampleInto(specL, coeffsL, energiesL, 0, end, lm, EBands[:], downsample)
			denormalizeBandsPackedDownsampleInto(specR, coeffsR, energiesR, 0, end, lm, EBands[:], downsample)
			if qext.coeffsL != nil {
				denormalizeBandsPackedDownsampleInto(specL, qext.coeffsL, qext.energies[:qext.end], 0, qext.end, lm, qext.cfg.EBands, downsample)
			}
			if qext.coeffsR != nil {
				denormalizeBandsPackedDownsampleInto(specR, qext.coeffsR, qext.energies[qext.end:], 0, qext.end, lm, qext.cfg.EBands, downsample)
			}
			coeffsL = specL
			coeffsR = specR
		} else {
			denormalizeCoeffsDownsample(coeffsL, energiesL, end, frameSize, downsample)
			denormalizeCoeffsDownsample(coeffsR, energiesR, end, frameSize, downsample)
		}
		if directStereoFloat32 && !transient {
			samplesL, samplesR := d.synthesizeStereoPlanarLongToFloat32(coeffsL, coeffsR)
			if d.postfilterGainOld == 0 && d.postfilterGain == 0 && postfilterGain == 0 {
				d.applyPostfilterNoGainStereoPlanarFromFloat32(samplesL[:frameSize], samplesR[:frameSize], frameSize, modeLM, postfilterPeriod, postfilterGain, postfilterTapset)
			} else {
				d.applyPostfilterStereoPlanarFromFloat32(samplesL[:frameSize], samplesR[:frameSize], frameSize, modeLM, postfilterPeriod, postfilterGain, postfilterTapset)
			}
			if downsampleOutput {
				d.applyDeemphasisAndScaleStereoPlanarFloat32DownsampleToFloat32(d.directOutPCM[:outputFrameSize*2], samplesL[:frameSize], samplesR[:frameSize], downsample, 1.0/32768.0)
			} else {
				d.applyDeemphasisAndScaleStereoPlanarFloat32ToFloat32(d.directOutPCM[:frameSize*2], samplesL[:frameSize], samplesR[:frameSize], 1.0/32768.0)
			}
		} else if directStereoFloat32 {
			samplesL, samplesR := d.synthesizeStereoPlanar(coeffsL, coeffsR, transient, shortBlocks)
			d.applyPostfilterStereoPlanar(samplesL, samplesR, frameSize, modeLM, postfilterPeriod, postfilterGain, postfilterTapset)
			if downsampleOutput {
				d.applyDeemphasisAndScaleStereoPlanarDownsampleToFloat32(d.directOutPCM[:outputFrameSize*2], samplesL, samplesR, downsample, 1.0/32768.0)
			} else {
				d.applyDeemphasisAndScaleStereoPlanarToFloat32(d.directOutPCM[:frameSize*2], samplesL, samplesR, 1.0/32768.0)
			}
		} else {
			samples = d.SynthesizeStereo(coeffsL, coeffsR, transient, shortBlocks)
		}
	} else {
		if extsupport.QEXT && qext != nil && qext.end > 0 {
			qextState := d.ensureQEXTState()
			specL := ensureFloat64Slice(&qextState.scratchSpectrumL, len(coeffsL))
			denormalizeBandsPackedDownsampleInto(specL, coeffsL, energies, 0, end, lm, EBands[:], downsample)
			if qext.coeffsL != nil {
				denormalizeBandsPackedDownsampleInto(specL, qext.coeffsL, qext.energies[:qext.end], 0, qext.end, lm, qext.cfg.EBands, downsample)
			}
			coeffsL = specL
		} else {
			denormalizeCoeffsDownsample(coeffsL, energies, end, frameSize, downsample)
		}
		if directMonoFloat32 {
			samplesF32 := d.synthesizeMonoLongToFloat32(coeffsL)
			d.applyPostfilterNoGainMonoFromFloat32(samplesF32, frameSize, modeLM, postfilterPeriod, postfilterGain, postfilterTapset)
			if downsampleOutput {
				d.applyDeemphasisAndScaleMonoFloat32DownsampleToFloat32(d.directOutPCM[:outputFrameSize], samplesF32, downsample, 1.0/32768.0)
			} else {
				d.applyDeemphasisAndScaleMonoFloat32ToFloat32(d.directOutPCM[:frameSize], samplesF32, 1.0/32768.0)
			}
		} else {
			samples = d.Synthesize(coeffsL, transient, shortBlocks)
		}
	}

	if directStereoFloat32 || directMonoFloat32 {
		return samples
	}

	d.applyPostfilter(samples, frameSize, modeLM, postfilterPeriod, postfilterGain, postfilterTapset)

	// Step 7: Apply de-emphasis filter
	if downsampleOutput && len(d.directOutPCM) >= outputFrameSize*d.channels {
		d.applyDeemphasisAndScaleDownsampleToFloat32(d.directOutPCM[:outputFrameSize*d.channels], samples, downsample, 1.0/32768.0)
		return nil
	} else if len(d.directOutPCM) >= len(samples) {
		d.applyDeemphasisAndScaleToFloat32(d.directOutPCM[:len(samples)], samples, 1.0/32768.0)
	} else {
		d.applyDeemphasisAndScale(samples, 1.0/32768.0)
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
	if extsupport.QEXT && qext != nil && qext.dec.Tell() > qext.dec.StorageBits() {
		return ErrInvalidFrame
	}

	var extDec *rangecoding.Decoder
	if extsupport.QEXT && qext != nil {
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
