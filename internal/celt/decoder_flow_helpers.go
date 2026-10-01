package celt

import (
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/rangecoding"
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
		prefix := min(edges[start]*M, len(dst))
		clear(dst[:prefix])
	}
	// Band k covers src[edges[k]*M : edges[k+1]*M] and lands at the same
	// offset of dst, as the freq and X cursors of libopus denormalise_bands()
	// advance together.
	limit := min(len(src), len(dst))
	dstL, srcL := dst[:limit], src[:limit]
	edges = edges[:end+1]

	var gainBuf [denormGainBands]float32
	var gains []float32
	if end <= denormGainBands {
		gains = gainBuf[:end]
		denormalizeBandGains(gains, energies, start, end)
	}
	for band := start; band < end; band++ {
		j := edges[band] << lm
		if j >= limit {
			break
		}
		bandEnd := min(edges[band+1]<<lm, limit)
		if bandEnd <= j {
			continue
		}
		var gain float32
		if gains != nil {
			gain = gains[band]
		} else {
			gain = denormalizeBandGain(energies, band)
		}
		out := dstL[j:bandEnd]
		in := srcL[j:bandEnd]
		// Low bands are only a few bins wide; their vector call/setup cost
		// beats the per-lane win, so keep them on the tight inline loop and
		// vector only the wide bands. Each product is bare, so the result
		// matches on every build.
		if len(out) < 8 {
			for k := range out {
				out[k] = float32(in[k]) * gain
			}
			continue
		}
		scaleFloat32Into(out, in, gain)
	}
	if bound < len(dst) {
		clear(dst[bound:])
	}
}

func (d *Decoder) synthesizeDecodedFrame(frameSize, modeLM, end, lm, shortBlocks int, transient bool, postfilterPeriod int, postfilterGain float32, postfilterTapset int, energies []celtGLog, coeffsL, coeffsR []celtNorm, qext *preparedQEXTDecode) []float32 {
	// Step 6: Synthesis (IMDCT + window + overlap-add)
	var samples []float32
	channels := int(d.channels)
	if d.synthTrace != nil {
		d.synthTrace.captureBaseEnergy(energies, end, channels)
		d.synthTrace.captureBaseNorm(0, coeffsL, frameSize)
		if channels == 2 {
			d.synthTrace.captureBaseNorm(1, coeffsR, frameSize)
		}
	}
	if d.synthTrace != nil && extsupport.QEXT && qext != nil {
		d.synthTrace.captureQEXTEnergy(qext.energies, qext.end, channels)
		d.synthTrace.captureQEXTNorm(0, qext.coeffsL, frameSize)
		if channels == 2 {
			d.synthTrace.captureQEXTNorm(1, qext.coeffsR, frameSize)
		}
	}
	downsample := d.downsampleFactor()
	outputFrameSize := frameSize / d.outputDownsample(d.directOutPCM, frameSize)
	// The native 96 kHz HD mode needs the HD-specific de-emphasis (2-tap) and
	// comb-filter postfilter (comb_filter_qext), which live on the non-direct
	// synthesis path. Disable the direct-output fast paths so HD frames route
	// through Synthesize/SynthesizeStereo + the HD-aware deemphasis/postfilter,
	// which still write into directOutPCM at the end of this function.
	hdMode := d.synthOverlap == 240 || d.customScaleBase > 0
	directStereoFloat32 := !hdMode && d.channels == 2 && len(d.directOutPCM) >= outputFrameSize*2
	directMonoFloat32 := !hdMode && d.channels == 1 &&
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
			denormalizeBandsPackedDownsampleIntoFloat32(specL, coeffsL, energiesL, 0, end, lm, d.modeEdges(), downsample)
			denormalizeBandsPackedDownsampleIntoFloat32(specR, coeffsR, energiesR, 0, end, lm, d.modeEdges(), downsample)
		}
		if directStereoFloat32 && !transient {
			if d.synthTrace != nil {
				d.synthTrace.captureSpec(0, specL[:frameSize])
				d.synthTrace.captureSpec(1, specR[:frameSize])
			}
			samplesL, samplesR := d.synthesizeStereoPlanarLongToFloat32(specL, specR)
			if d.synthTrace != nil {
				d.synthTrace.captureIMDCT(0, samplesL[:frameSize])
				d.synthTrace.captureIMDCT(1, samplesR[:frameSize])
			}
			if d.postfilterGainOld == 0 && d.postfilterGain == 0 && postfilterGain == 0 {
				d.applyPostfilterNoGainStereoPlanarFromFloat32(samplesL[:frameSize], samplesR[:frameSize], frameSize, modeLM, postfilterPeriod, postfilterGain, postfilterTapset)
			} else {
				d.applyPostfilterStereoPlanarFromFloat32(samplesL[:frameSize], samplesR[:frameSize], frameSize, modeLM, postfilterPeriod, postfilterGain, postfilterTapset)
			}
			if d.synthTrace != nil {
				d.synthTrace.capturePostComb(0, samplesL[:frameSize])
				d.synthTrace.capturePostComb(1, samplesR[:frameSize])
			}
			d.deemphasisPlanarToDirectOut(samplesL[:frameSize], samplesR[:frameSize], frameSize)
		} else if directStereoFloat32 {
			if d.synthTrace != nil {
				d.synthTrace.captureSpec(0, specL[:frameSize])
				d.synthTrace.captureSpec(1, specR[:frameSize])
			}
			samplesL, samplesR := d.synthesizeStereoPlanar(specL, specR, transient, shortBlocks)
			if d.synthTrace != nil {
				d.synthTrace.captureIMDCT(0, samplesL[:frameSize])
				d.synthTrace.captureIMDCT(1, samplesR[:frameSize])
			}
			d.applyPostfilterStereoPlanarFromFloat32(samplesL, samplesR, frameSize, modeLM, postfilterPeriod, postfilterGain, postfilterTapset)
			if d.synthTrace != nil {
				d.synthTrace.capturePostComb(0, samplesL[:frameSize])
				d.synthTrace.capturePostComb(1, samplesR[:frameSize])
			}
			d.deemphasisPlanarToDirectOut(samplesL[:frameSize], samplesR[:frameSize], frameSize)
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
			denormalizeBandsPackedDownsampleIntoFloat32(specL, coeffsL, energies, 0, end, lm, d.modeEdges(), downsample)
		}
		if directMonoFloat32 {
			samplesF32 := d.synthesizeMonoLongToFloat32(specL)
			d.applyPostfilterNoGainMonoFromFloat32(samplesF32, frameSize, modeLM, postfilterPeriod, postfilterGain, postfilterTapset)
			if d.synthTrace != nil {
				d.synthTrace.capturePostComb(0, samplesF32[:frameSize])
			}
			d.deemphasisPlanarToDirectOut(samplesF32[:frameSize], nil, frameSize)
		} else {
			if d.synthTrace != nil {
				d.synthTrace.captureSpec(0, specL[:frameSize])
			}
			samples = d.Synthesize(specL, transient, shortBlocks)
			if d.synthTrace != nil {
				d.synthTrace.captureIMDCT(0, samples[:frameSize])
			}
		}
	}

	if directStereoFloat32 || directMonoFloat32 {
		return samples
	}

	d.applyPostfilterFloat32(samples, frameSize, modeLM, postfilterPeriod, postfilterGain, postfilterTapset)
	if d.synthTrace != nil && channels == 1 {
		d.synthTrace.capturePostComb(0, samples[:frameSize])
	}

	// Step 7: Apply de-emphasis filter
	return d.deemphasisInterleaved(samples, frameSize)
}

func (d *Decoder) finalizeDecodedFrameState(frameSize, start, end, lm int, transient bool, energies []celtGLog, qext *preparedQEXTDecode, rd *rangecoding.Decoder) error {
	// Update energy state for next frame.
	d.updateLogEGLog(energies, end, transient)
	d.setPrevEnergyGLog(energies)
	// libopus mirrors the left channel into the right slot on every mono frame
	// (`if (C==1) OPUS_COPY(&oldBandE[nbEBands], oldBandE, nbEBands)`), keeping
	// oldBandE/oldLogE/oldLogE2 two-channel-symmetric. This must happen before the
	// background-floor and outside-range updates so the right shadow stays a true
	// copy: after a concealed loss the noise PLC only decays the left channel, and
	// the recovery frame folds the (undecayed) right shadow back in.
	d.replicateMonoEnergyToSecondChannel()
	d.updateBackgroundEnergy(lm)

	// Mirror libopus: clear energies/logs outside [start,end) for both channels.
	channels := int(d.channels)
	clearChannels := channels
	if channels == 1 && len(d.prevEnergy) >= d.predStride()*2 {
		clearChannels = 2
	}
	d.clearFrameHistoryOutsideRange(start, end, clearChannels)
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

// replicateMonoEnergyToSecondChannel copies the left-channel energy-prediction
// history (prevEnergy/prevLogE/prevLogE2) into the right-channel slot for a mono
// decoder, matching libopus celt_decode_with_ec()'s per-frame mono shadow update
// (`if (C==1) OPUS_COPY(&oldBandE[nbEBands], oldBandE, nbEBands)` followed by the
// two-channel oldLogE/oldLogE2 refresh). backgroundLogE is left to
// updateBackgroundEnergy, which preserves the two-channel symmetry once the
// prediction history is symmetric. For a stereo decoder this is a no-op.
func (d *Decoder) replicateMonoEnergyToSecondChannel() {
	if d.channels != 1 {
		return
	}
	stride := d.predStride()
	if stride <= 0 || len(d.prevEnergy) < stride*2 {
		return
	}
	nbEBands := min(d.modeNbEBands(), stride)
	copy(d.prevEnergy[stride:stride+nbEBands], d.prevEnergy[:nbEBands])
	if len(d.prevLogE) >= stride*2 {
		copy(d.prevLogE[stride:stride+nbEBands], d.prevLogE[:nbEBands])
	}
	if len(d.prevLogE2) >= stride*2 {
		copy(d.prevLogE2[stride:stride+nbEBands], d.prevLogE2[:nbEBands])
	}
}

func (d *Decoder) clearFrameHistoryOutsideRange(start, end, channels int) {
	// libopus clears the energy/log history outside [start,end) up to nbEBands.
	// For a per-mode custom layout that is the mode's band count, and the buffers
	// use the same nbEBands per-channel prediction stride (mono keeps c==0, so the
	// static MaxBands stride and the per-mode nbEBands stride coincide).
	nbEBands := d.modeNbEBands()
	stride := d.predStride()
	for c := range channels {
		base := c * stride
		for band := range start {
			d.prevEnergy[base+band] = 0
			d.prevLogE[base+band] = -28.0
			d.prevLogE2[base+band] = -28.0
		}
		for band := end; band < nbEBands; band++ {
			d.prevEnergy[base+band] = 0
			d.prevLogE[base+band] = -28.0
			d.prevLogE2[base+band] = -28.0
		}
	}
}
