package celt

import (
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/rangecoding"
)

func denormalizeBandsPackedDownsampleIntoFloat32(dst []float32, src []celtNorm, energies []celtGLog, start, end, lm int, edges []int, downsample int) {
	if len(dst) == 0 || len(src) == 0 || len(energies) == 0 || end <= start || len(edges) < end+1 {
		return
	}
	if start < 0 {
		start = 0
	}
	if end > len(energies) {
		end = len(energies)
	}
	if end <= start {
		return
	}

	M := 1 << lm
	bound := edges[end] * M
	if downsample > 1 {
		if limit := len(dst) / downsample; bound > limit {
			bound = limit
		}
	}
	if bound > len(dst) {
		bound = len(dst)
	}
	if start != 0 {
		prefix := edges[start] * M
		if prefix > len(dst) {
			prefix = len(dst)
		}
		clear(dst[:prefix])
	}
	f := edges[start] * M
	if f > len(dst) {
		f = len(dst)
	}

	for band := start; band < end; band++ {
		j := edges[band] * M
		bandEnd := edges[band+1] * M
		if j >= len(src) {
			break
		}
		if bandEnd > len(src) {
			bandEnd = len(src)
		}
		gain := denormalizeBandGain(energies, band)
		for ; j < bandEnd && f < len(dst); j++ {
			dst[f] = float32(src[j]) * gain
			f++
		}
	}
	if bound < len(dst) {
		clear(dst[bound:])
	}
}

func (d *Decoder) synthesizeDecodedFrame(frameSize, modeLM, end, lm, shortBlocks int, transient bool, postfilterPeriod int, postfilterGain float32, postfilterTapset int, energies []celtGLog, coeffsL, coeffsR []celtNorm, qext *preparedQEXTDecode) []float32 {
	// Step 6: Synthesis (IMDCT + window + overlap-add)
	var samples []float32
	channels := int(d.channels)
	downsample := d.downsampleFactor()
	outputFrameSize := frameSize
	downsampleOutput := false
	if downsample > 1 && frameSize%downsample == 0 {
		apiFrameSize := frameSize / downsample
		if len(d.directOutPCM) < frameSize*channels && len(d.directOutPCM) >= apiFrameSize*channels {
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
		var specL []float32
		var specR []float32
		if extsupport.QEXT && qext != nil && qext.end > 0 {
			specL = ensureFloat32Slice(&d.scratchStereoF32, len(coeffsL))
			specR = ensureFloat32Slice(&d.scratchSpecRF32, len(coeffsR))
			denormalizeBandsPackedDownsampleIntoFloat32(specL, coeffsL, energiesL, 0, end, lm, EBands[:], downsample)
			denormalizeBandsPackedDownsampleIntoFloat32(specR, coeffsR, energiesR, 0, end, lm, EBands[:], downsample)
			if qext.coeffsL != nil {
				denormalizeBandsPackedDownsampleIntoFloat32(specL, qext.coeffsL, qext.energies[:qext.end], 0, qext.end, lm, qext.cfg.EBands, downsample)
			}
			if qext.coeffsR != nil {
				denormalizeBandsPackedDownsampleIntoFloat32(specR, qext.coeffsR, qext.energies[qext.end:], 0, qext.end, lm, qext.cfg.EBands, downsample)
			}
		} else {
			specL = ensureFloat32Slice(&d.scratchStereoF32, len(coeffsL))
			specR = ensureFloat32Slice(&d.scratchSpecRF32, len(coeffsR))
			denormalizeBandsPackedDownsampleIntoFloat32(specL, coeffsL, energiesL, 0, end, lm, EBands[:], downsample)
			denormalizeBandsPackedDownsampleIntoFloat32(specR, coeffsR, energiesR, 0, end, lm, EBands[:], downsample)
		}
		if directStereoFloat32 && !transient {
			samplesL, samplesR := d.synthesizeStereoPlanarLongToFloat32(specL, specR)
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
			samplesL, samplesR := d.synthesizeStereoPlanar(specL, specR, transient, shortBlocks)
			d.applyPostfilterStereoPlanarFromFloat32(samplesL, samplesR, frameSize, modeLM, postfilterPeriod, postfilterGain, postfilterTapset)
			if downsampleOutput {
				d.applyDeemphasisAndScaleStereoPlanarFloat32DownsampleToFloat32(d.directOutPCM[:outputFrameSize*2], samplesL, samplesR, downsample, 1.0/32768.0)
			} else {
				d.applyDeemphasisAndScaleStereoPlanarFloat32ToFloat32(d.directOutPCM[:frameSize*2], samplesL, samplesR, 1.0/32768.0)
			}
		} else {
			samples = d.SynthesizeStereo(specL, specR, transient, shortBlocks)
		}
	} else {
		var specL []float32
		if extsupport.QEXT && qext != nil && qext.end > 0 {
			specL = ensureFloat32Slice(&d.scratchStereoF32, len(coeffsL))
			denormalizeBandsPackedDownsampleIntoFloat32(specL, coeffsL, energies, 0, end, lm, EBands[:], downsample)
			if qext.coeffsL != nil {
				denormalizeBandsPackedDownsampleIntoFloat32(specL, qext.coeffsL, qext.energies[:qext.end], 0, qext.end, lm, qext.cfg.EBands, downsample)
			}
		} else {
			specL = ensureFloat32Slice(&d.scratchStereoF32, len(coeffsL))
			denormalizeBandsPackedDownsampleIntoFloat32(specL, coeffsL, energies, 0, end, lm, EBands[:], downsample)
		}
		if directMonoFloat32 {
			samplesF32 := d.synthesizeMonoLongToFloat32(specL)
			d.applyPostfilterNoGainMonoFromFloat32(samplesF32, frameSize, modeLM, postfilterPeriod, postfilterGain, postfilterTapset)
			if downsampleOutput {
				d.applyDeemphasisAndScaleMonoFloat32DownsampleToFloat32(d.directOutPCM[:outputFrameSize], samplesF32, downsample, 1.0/32768.0)
			} else {
				d.applyDeemphasisAndScaleMonoFloat32ToFloat32(d.directOutPCM[:frameSize], samplesF32, 1.0/32768.0)
			}
		} else {
			samples = d.Synthesize(specL, transient, shortBlocks)
		}
	}

	if directStereoFloat32 || directMonoFloat32 {
		return samples
	}

	d.applyPostfilterFloat32(samples, frameSize, modeLM, postfilterPeriod, postfilterGain, postfilterTapset)

	// Step 7: Apply de-emphasis filter
	if downsampleOutput && len(d.directOutPCM) >= outputFrameSize*channels {
		d.applyDeemphasisAndScaleDownsampleToFloat32(d.directOutPCM[:outputFrameSize*channels], samples, downsample, 1.0/32768.0)
		return nil
	} else if len(d.directOutPCM) >= len(samples) {
		d.applyDeemphasisAndScaleToFloat32(d.directOutPCM[:len(samples)], samples, 1.0/32768.0)
	} else {
		d.applyDeemphasisAndScale(samples, 1.0/32768.0)
	}

	return samples
}

func (d *Decoder) finalizeDecodedFrameState(frameSize, start, end, lm int, transient bool, energies, prev1Energy []celtGLog, qext *preparedQEXTDecode, rd *rangecoding.Decoder) error {
	// Update energy state for next frame.
	d.updateLogEGLog(energies, end, transient)
	d.setPrevEnergyGLogWithPrev(prev1Energy, energies)
	d.updateBackgroundEnergy(lm)

	// Mirror libopus: clear energies/logs outside [start,end).
	channels := int(d.channels)
	d.clearFrameHistoryOutsideRange(start, end, channels)
	if extsupport.QEXT && qext != nil && qext.dec.Tell() > qext.dec.StorageBits() {
		return ErrInvalidFrame
	}

	var extDec *rangecoding.Decoder
	if extsupport.QEXT && qext != nil {
		extDec = qext.dec
	}
	d.rng = combineFinalRange(rd, extDec)

	// Reset PLC state after successful decode.
	d.resetPLCCadence(frameSize, channels)
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
