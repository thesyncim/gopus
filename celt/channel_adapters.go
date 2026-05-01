package celt

import (
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/rangecoding"
)

func packetChannelsFromStereoFlag(packetStereo bool) int {
	if packetStereo {
		return 2
	}
	return 1
}

// DecodeFrameWithPacketStereo decodes a CELT frame with explicit packet stereo flag.
// This handles the case where the packet's stereo flag differs from the decoder's configured channels.
func (d *Decoder) DecodeFrameWithPacketStereo(data []byte, frameSize int, packetStereo bool) ([]float64, error) {
	packetChannels := packetChannelsFromStereoFlag(packetStereo)
	d.handleChannelTransition(packetChannels)
	if packetChannels == d.channels {
		return d.DecodeFrame(data, frameSize)
	}
	if packetChannels == 1 && d.channels == 2 {
		return d.decodeMonoPacketToStereo(data, frameSize)
	}
	return d.decodeStereoPacketToMono(data, frameSize)
}

// DecodeFrameWithPacketStereoToFloat32 decodes a CELT frame directly into a
// caller-provided float32 buffer for the common packetChannels==decoder
// channels path, falling back to the float64-returning path otherwise.
func (d *Decoder) DecodeFrameWithPacketStereoToFloat32(data []byte, frameSize int, packetStereo bool, out []float32) error {
	outLen := frameSize * d.channels
	if len(out) < outLen {
		return ErrOutputTooSmall
	}

	packetChannels := packetChannelsFromStereoFlag(packetStereo)
	if data != nil && len(data) != 0 && packetChannels == 1 && d.channels == 2 {
		d.directOutPCM = out[:outLen]
		defer func() {
			d.directOutPCM = nil
		}()
		_, err := d.decodeMonoPacketToStereo(data, frameSize)
		return err
	}

	if data == nil || len(data) == 0 || packetChannels != d.channels {
		samples, err := d.DecodeFrameWithPacketStereo(data, frameSize, packetStereo)
		if err != nil {
			return err
		}
		copyFloat64ToFloat32(out[:outLen], samples)
		return nil
	}

	d.directOutPCM = out[:outLen]
	defer func() {
		d.directOutPCM = nil
	}()
	_, err := d.DecodeFrame(data, frameSize)
	return err
}

// decodeMonoPacketToStereo decodes a mono packet and converts output to stereo.
func (d *Decoder) decodeMonoPacketToStereo(data []byte, frameSize int) ([]float64, error) {
	qextPayload := d.takeQEXTPayload()
	if data == nil || len(data) == 0 {
		return d.decodePLC(frameSize)
	}
	if !ValidFrameSize(frameSize) {
		return nil, ErrInvalidFrameSize
	}

	d.beginDecodedPacketPLCState()
	origChannels := d.channels
	d.channels = 1

	rd := &d.rangeDecoderScratch
	rd.Init(data)
	d.SetRangeDecoder(rd)

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

	prev1Energy := ensureFloat64Slice(&d.scratchPrevEnergy, MaxBands)
	prev1Energy = prev1Energy[:MaxBands]
	prev1LogE := ensureFloat64Slice(&d.scratchPrevLogE, len(d.prevLogE))
	copy(prev1LogE, d.prevLogE)
	prev2LogE := ensureFloat64Slice(&d.scratchPrevLogE2, len(d.prevLogE2))
	copy(prev2LogE, d.prevLogE2)
	for i := 0; i < MaxBands; i++ {
		left := d.prevEnergy[i]
		if origChannels > 1 && len(d.prevEnergy) >= MaxBands*2 {
			right := d.prevEnergy[MaxBands+i]
			if right > left {
				left = right
			}
		}
		prev1Energy[i] = left
	}
	origPrevEnergy := d.prevEnergy
	d.prevEnergy = prev1Energy

	totalBits := len(data) * 8
	tell := rd.Tell()
	silence := false
	if tell >= totalBits {
		silence = true
	} else if tell == 1 {
		silence = rd.DecodeBit(15) == 1
	}

	defer func() {
		d.channels = origChannels
		d.prevEnergy = origPrevEnergy
	}()

	if silence {
		d.channels = origChannels
		samples := d.decodeSilenceFrame(frameSize, 0, 0, 0)
		silenceE := ensureFloat64Slice(&d.scratchSilenceE, MaxBands*origChannels)
		for i := range silenceE {
			silenceE[i] = -28.0
		}
		d.prevEnergy = origPrevEnergy
		for i := 0; i < MaxBands*origChannels && i < len(d.prevEnergy); i++ {
			d.prevEnergy[i] = -28.0
		}
		d.updateLogE(silenceE, MaxBands, false)
		d.updateBackgroundEnergy(lm)
		d.resetPLCCadence(frameSize, origChannels)
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

	transient := false
	if lm > 0 && tell+3 <= totalBits {
		transient = rd.DecodeBit(3) == 1
		tell = rd.Tell()
	}
	intra := false
	if tell+3 <= totalBits {
		intra = rd.DecodeBit(3) == 1
	}
	d.applyLossEnergySafety(intra, start, end, lm)

	shortBlocks := 1
	if transient {
		shortBlocks = mode.ShortBlocks
	}

	monoEnergies := d.decodeCoarseEnergyInto(ensureFloat64Slice(&d.scratchEnergies, end*d.channels), end, intra, lm)

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

	d.DecodeFineEnergy(monoEnergies, end, fineQuant)
	var qext *preparedQEXTDecode
	if extsupport.QEXT {
		qext = d.prepareQEXTDecode(qextPayload, rd, end, lm, frameSize)
	}
	if qext != nil {
		d.decodeFineEnergyWithDecoderPrev(qext.dec, monoEnergies, end, fineQuant, qext.extraQuant[:end])
	}

	coeffsMono, _, collapse := quantAllBandsDecodeWithScratch(rd, 1, frameSize, lm, start, end, pulses, shortBlocks, spread,
		dualStereo, intensity, tfRes, (totalBits<<bitRes)-antiCollapseRsv, balance, codedBands, false, &d.rng, &d.scratchBands,
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
		d.decodeQEXTBands(frameSize, lm, shortBlocks, spread, false, qext)
	}

	antiCollapseOn := false
	if antiCollapseRsv > 0 {
		antiCollapseOn = rd.DecodeRawBits(1) == 1
	}

	bitsLeft := totalBits - rd.Tell()
	if extsupport.QEXT && len(qextPayload) != 0 {
		d.DecodeEnergyFinaliseRange(start, end, nil, fineQuant, finePriority, bitsLeft)
	} else {
		d.DecodeEnergyFinalise(monoEnergies, end, fineQuant, finePriority, bitsLeft)
	}

	if antiCollapseOn {
		antiCollapse(coeffsMono, nil, collapse, lm, 1, start, end, monoEnergies, prev1LogE, prev2LogE, pulses, d.rng)
	}

	if qext != nil && qext.end > 0 {
		specMono := ensureFloat64Slice(&d.scratchQEXTSpectrumL, len(coeffsMono))
		denormalizeBandsPackedInto(specMono, coeffsMono, monoEnergies, 0, end, lm, EBands[:])
		if qext.coeffsL != nil {
			denormalizeBandsPackedInto(specMono, qext.coeffsL, qext.energies[:qext.end], 0, qext.end, lm, qext.cfg.EBands)
		}
		coeffsMono = specMono
	} else {
		denormalizeCoeffs(coeffsMono, monoEnergies, end, frameSize)
	}

	d.channels = origChannels
	d.prevEnergy = origPrevEnergy
	d.applyPendingPLCPrefilterAndFold()

	var samples []float64
	directPlanar := false
	if !transient {
		outL, outR := d.synthesizeStereoPlanarFromMonoLong(coeffsMono)
		if len(d.directOutPCM) >= frameSize*2 {
			left := outL[:frameSize]
			right := outR[:frameSize]
			d.applyPostfilterStereoPlanar(left, right, frameSize, mode.LM, postfilterPeriod, postfilterGain, postfilterTapset)
			d.applyDeemphasisAndScaleStereoPlanarToFloat32(d.directOutPCM[:frameSize*2], left, right, 1.0/32768.0)
			directPlanar = true
		} else {
			samples = ensureFloat64Slice(&d.scratchStereo, frameSize*2)
			InterleaveStereoInto(outL[:frameSize], outR[:frameSize], samples[:frameSize*2])
			samples = samples[:frameSize*2]
		}
	} else {
		coeffsL := coeffsMono
		coeffsR := ensureFloat64Slice(&d.scratchMonoToStereoR, len(coeffsMono))
		copy(coeffsR, coeffsMono)
		samples = d.SynthesizeStereo(coeffsL, coeffsR, transient, shortBlocks)
	}
	traceLen := len(samples)
	if traceLen > 16 {
		traceLen = 16
	}

	if !directPlanar {
		d.applyPostfilter(samples, frameSize, mode.LM, postfilterPeriod, postfilterGain, postfilterTapset)
		if len(d.directOutPCM) >= len(samples) {
			d.applyDeemphasisAndScaleToFloat32(d.directOutPCM[:len(samples)], samples, 1.0/32768.0)
		} else {
			d.applyDeemphasisAndScale(samples, 1.0/32768.0)
		}
	}

	var stereoEnergiesArr [MaxBands * 2]float64
	stereoEnergies := stereoEnergiesArr[:]
	for i := 0; i < end; i++ {
		stereoEnergies[i] = monoEnergies[i]
		stereoEnergies[MaxBands+i] = monoEnergies[i]
	}
	for i := end; i < MaxBands; i++ {
		stereoEnergies[i] = -28.0
		stereoEnergies[MaxBands+i] = -28.0
	}

	d.updateLogE(stereoEnergies, end, transient)
	for i := 0; i < MaxBands; i++ {
		d.prevEnergy[i] = stereoEnergies[i]
		d.prevEnergy[MaxBands+i] = stereoEnergies[MaxBands+i]
	}
	d.updateBackgroundEnergy(lm)
	d.clearFrameHistoryOutsideRange(start, end, origChannels)

	if qext != nil && qext.dec.Tell() > qext.dec.StorageBits() {
		return nil, ErrInvalidFrame
	}
	var extDec *rangecoding.Decoder
	if qext != nil {
		extDec = qext.dec
	}
	d.rng = combineFinalRange(rd, extDec)
	d.resetPLCCadence(frameSize, origChannels)

	return samples, nil
}

// decodeStereoPacketToMono decodes a stereo packet and converts output to mono.
func (d *Decoder) decodeStereoPacketToMono(data []byte, frameSize int) ([]float64, error) {
	qextPayload := d.takeQEXTPayload()
	if data == nil || len(data) == 0 {
		return d.decodePLC(frameSize)
	}
	if !ValidFrameSize(frameSize) {
		return nil, ErrInvalidFrameSize
	}

	d.beginDecodedPacketPLCState()
	d.ensureEnergyState(2)

	origChannels := d.channels
	d.channels = 2
	defer func() {
		d.channels = origChannels
	}()

	rd := &d.rangeDecoderScratch
	rd.Init(data)
	d.SetRangeDecoder(rd)

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

	totalBits := len(data) * 8
	tell := rd.Tell()
	silence := false
	if tell >= totalBits {
		silence = true
	} else if tell == 1 {
		silence = rd.DecodeBit(15) == 1
	}
	if silence {
		samples := make([]float64, frameSize)
		var silenceEArr [MaxBands * 2]float64
		silenceE := silenceEArr[:]
		for i := range silenceE {
			silenceE[i] = -28.0
		}
		d.updateLogE(silenceE, MaxBands, false)
		d.SetPrevEnergyWithPrev(prev1Energy, silenceE)
		d.updateBackgroundEnergy(lm)
		d.rng = rd.Range()
		d.resetPLCCadence(frameSize, origChannels)
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

	transient := false
	if lm > 0 && tell+3 <= totalBits {
		transient = rd.DecodeBit(3) == 1
		tell = rd.Tell()
	}
	intra := false
	if tell+3 <= totalBits {
		intra = rd.DecodeBit(3) == 1
	}
	d.applyLossEnergySafety(intra, start, end, lm)

	shortBlocks := 1
	if transient {
		shortBlocks = mode.ShortBlocks
	}

	energies := d.decodeCoarseEnergyInto(ensureFloat64Slice(&d.scratchEnergies, end*d.channels), end, intra, lm)

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
	var qext *preparedQEXTDecode
	if extsupport.QEXT {
		qext = d.prepareQEXTDecode(qextPayload, rd, end, lm, frameSize)
	}
	if qext != nil {
		d.decodeFineEnergyWithDecoderPrev(qext.dec, energies, end, fineQuant, qext.extraQuant[:end])
	}

	coeffsL, coeffsR, collapse := quantAllBandsDecodeWithScratch(rd, d.channels, frameSize, lm, start, end, pulses, shortBlocks, spread,
		dualStereo, intensity, tfRes, (totalBits<<bitRes)-antiCollapseRsv, balance, codedBands, origChannels == 1, &d.rng, &d.scratchBands,
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
		d.decodeQEXTBands(frameSize, lm, shortBlocks, spread, origChannels == 1, qext)
	}

	antiCollapseOn := false
	if antiCollapseRsv > 0 {
		antiCollapseOn = rd.DecodeRawBits(1) == 1
	}

	bitsLeft := totalBits - rd.Tell()
	if extsupport.QEXT && len(qextPayload) != 0 {
		d.DecodeEnergyFinaliseRange(start, end, nil, fineQuant, finePriority, bitsLeft)
	} else {
		d.DecodeEnergyFinalise(energies, end, fineQuant, finePriority, bitsLeft)
	}

	if antiCollapseOn {
		antiCollapse(coeffsL, coeffsR, collapse, lm, d.channels, start, end, energies, prev1LogE, prev2LogE, pulses, d.rng)
	}

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
	coeffsMono := ensureFloat64Slice(&d.scratchMonoMix, len(coeffsL))
	for i := range coeffsMono {
		coeffsMono[i] = 0.5 * (coeffsL[i] + coeffsR[i])
	}

	d.updateLogE(energies, end, transient)
	d.SetPrevEnergyWithPrev(prev1Energy, energies)
	d.updateBackgroundEnergy(lm)
	d.clearFrameHistoryOutsideRange(start, end, 2)
	if qext != nil && qext.dec.Tell() > qext.dec.StorageBits() {
		return nil, ErrInvalidFrame
	}
	var extDec *rangecoding.Decoder
	if qext != nil {
		extDec = qext.dec
	}
	d.rng = combineFinalRange(rd, extDec)

	d.channels = origChannels
	d.applyPendingPLCPrefilterAndFold()

	samples := d.Synthesize(coeffsMono, transient, shortBlocks)
	traceLen := len(samples)
	if traceLen > 16 {
		traceLen = 16
	}

	d.applyPostfilter(samples, frameSize, mode.LM, postfilterPeriod, postfilterGain, postfilterTapset)
	d.applyDeemphasisAndScale(samples, 1.0/32768.0)
	d.resetPLCCadence(frameSize, origChannels)

	return samples, nil
}

// DecodeFrameHybridWithPacketStereo decodes a hybrid CELT frame while honoring the packet stereo flag.
func (d *Decoder) DecodeFrameHybridWithPacketStereo(rd *rangecoding.Decoder, frameSize int, packetStereo bool) ([]float64, error) {
	packetChannels := packetChannelsFromStereoFlag(packetStereo)
	d.handleChannelTransition(packetChannels)
	if packetChannels == d.channels {
		return d.DecodeFrameHybrid(rd, frameSize)
	}
	if packetChannels == 1 && d.channels == 2 {
		return d.decodeMonoPacketToStereoHybrid(rd, frameSize)
	}
	return d.decodeStereoPacketToMonoHybrid(rd, frameSize)
}

// decodeMonoPacketToStereoHybrid decodes a mono hybrid frame and duplicates to stereo output.
func (d *Decoder) decodeMonoPacketToStereoHybrid(rd *rangecoding.Decoder, frameSize int) ([]float64, error) {
	if rd == nil {
		return nil, ErrNilDecoder
	}
	if frameSize != 480 && frameSize != 960 {
		return nil, ErrInvalidFrameSize
	}

	d.beginDecodedPacketPLCState()
	origChannels := d.channels
	d.channels = 1

	prev1Energy := ensureFloat64Slice(&d.scratchPrevEnergy, MaxBands)
	prev1LogE := ensureFloat64Slice(&d.scratchPrevLogE, len(d.prevLogE))
	copy(prev1LogE, d.prevLogE)
	prev2LogE := ensureFloat64Slice(&d.scratchPrevLogE2, len(d.prevLogE2))
	copy(prev2LogE, d.prevLogE2)
	for i := 0; i < MaxBands; i++ {
		left := d.prevEnergy[i]
		if origChannels > 1 && len(d.prevEnergy) >= MaxBands*2 {
			right := d.prevEnergy[MaxBands+i]
			if right > left {
				left = right
			}
		}
		prev1Energy[i] = left
	}
	origPrevEnergy := d.prevEnergy
	d.prevEnergy = prev1Energy

	defer func() {
		d.channels = origChannels
		d.prevEnergy = origPrevEnergy
	}()

	d.SetRangeDecoder(rd)

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

	totalBits := rd.StorageBits()
	tell := rd.Tell()
	silence := false
	if tell >= totalBits {
		silence = true
	} else if tell == 1 {
		silence = rd.DecodeBit(15) == 1
	}
	if silence {
		d.channels = origChannels
		samples := d.decodeSilenceFrame(frameSize, 0, 0, 0)
		var silenceEArr [MaxBands * 2]float64
		silenceE := silenceEArr[:MaxBands*origChannels]
		for i := range silenceE {
			silenceE[i] = -28.0
		}
		d.prevEnergy = origPrevEnergy
		d.updateLogE(silenceE, MaxBands, false)
		d.updateBackgroundEnergy(lm)
		d.rng = rd.Range()
		d.resetPLCCadence(frameSize, origChannels)
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

	transient := false
	if lm > 0 && tell+3 <= totalBits {
		transient = rd.DecodeBit(3) == 1
		tell = rd.Tell()
	}
	intra := false
	if tell+3 <= totalBits {
		intra = rd.DecodeBit(3) == 1
	}
	d.applyLossEnergySafety(intra, start, end, lm)

	shortBlocks := 1
	if transient {
		shortBlocks = mode.ShortBlocks
	}

	monoEnergies := ensureFloat64Slice(&d.scratchEnergies, end*d.channels)
	for band := 0; band < end; band++ {
		monoEnergies[band] = d.prevEnergy[band]
	}
	d.decodeCoarseEnergyRange(start, end, intra, lm, monoEnergies)

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

	coeffsMono, _ := d.decodeHybridSpectrum(rd, totalBits, frameSize, start, end, lm, shortBlocks, spread, antiCollapseRsv, 1, false, monoEnergies, prev1LogE, prev2LogE, pulses, fineQuant, finePriority, tfRes, intensity, dualStereo, balance, codedBands)

	denormalizeCoeffs(coeffsMono, monoEnergies, end, frameSize)

	d.channels = origChannels
	d.prevEnergy = origPrevEnergy
	d.applyPendingPLCPrefilterAndFold()

	var samples []float64
	if !transient {
		outL, outR := d.synthesizeStereoPlanarFromMonoLong(coeffsMono)
		samples = ensureFloat64Slice(&d.scratchStereo, frameSize*2)
		InterleaveStereoInto(outL[:frameSize], outR[:frameSize], samples[:frameSize*2])
		samples = samples[:frameSize*2]
	} else {
		coeffsL := coeffsMono
		coeffsR := ensureFloat64Slice(&d.scratchMonoToStereoR, len(coeffsMono))
		copy(coeffsR, coeffsMono)
		samples = d.SynthesizeStereo(coeffsL, coeffsR, transient, shortBlocks)
	}
	d.applyPostfilter(samples, frameSize, mode.LM, postfilterPeriod, postfilterGain, postfilterTapset)
	d.applyDeemphasisAndScale(samples, 1.0/32768.0)

	var stereoEnergiesArr [MaxBands * 2]float64
	stereoEnergies := stereoEnergiesArr[:]
	for i := 0; i < end; i++ {
		stereoEnergies[i] = monoEnergies[i]
		stereoEnergies[MaxBands+i] = monoEnergies[i]
	}
	for i := end; i < MaxBands; i++ {
		stereoEnergies[i] = -28.0
		stereoEnergies[MaxBands+i] = -28.0
	}

	d.updateLogE(stereoEnergies, end, transient)
	d.SetPrevEnergyWithPrev(prev1Energy, stereoEnergies)
	d.updateBackgroundEnergy(lm)
	d.clearFrameHistoryOutsideRange(start, end, origChannels)

	d.rng = rd.Range()
	d.resetPLCCadence(frameSize, origChannels)

	return samples, nil
}

// decodeStereoPacketToMonoHybrid decodes a stereo hybrid frame and downmixes to mono output.
func (d *Decoder) decodeStereoPacketToMonoHybrid(rd *rangecoding.Decoder, frameSize int) ([]float64, error) {
	if rd == nil {
		return nil, ErrNilDecoder
	}
	if frameSize != 480 && frameSize != 960 {
		return nil, ErrInvalidFrameSize
	}

	d.beginDecodedPacketPLCState()
	d.ensureEnergyState(2)

	origChannels := d.channels
	d.channels = 2
	defer func() {
		d.channels = origChannels
	}()

	d.SetRangeDecoder(rd)

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
		samples := ensureFloat64Slice(&d.scratchMonoMix, frameSize)
		clear(samples[:frameSize])
		var silenceEArr [MaxBands * 2]float64
		silenceE := silenceEArr[:]
		for i := range silenceE {
			silenceE[i] = -28.0
		}
		d.updateLogE(silenceE, MaxBands, false)
		d.SetPrevEnergyWithPrev(prev1Energy, silenceE)
		d.updateBackgroundEnergy(lm)
		d.rng = rd.Range()
		d.resetPLCCadence(frameSize, origChannels)
		return samples[:frameSize], nil
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

	transient := false
	if lm > 0 && tell+3 <= totalBits {
		transient = rd.DecodeBit(3) == 1
		tell = rd.Tell()
	}
	intra := false
	if tell+3 <= totalBits {
		intra = rd.DecodeBit(3) == 1
	}
	d.applyLossEnergySafety(intra, start, end, lm)

	shortBlocks := 1
	if transient {
		shortBlocks = mode.ShortBlocks
	}

	energies := ensureFloat64Slice(&d.scratchEnergies, end*d.channels)
	for c := 0; c < d.channels; c++ {
		for band := 0; band < end; band++ {
			energies[c*end+band] = d.prevEnergy[c*MaxBands+band]
		}
	}
	d.decodeCoarseEnergyRange(start, end, intra, lm, energies)

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

	coeffsL, coeffsR := d.decodeHybridSpectrum(rd, totalBits, frameSize, start, end, lm, shortBlocks, spread, antiCollapseRsv, d.channels, origChannels == 1, energies, prev1LogE, prev2LogE, pulses, fineQuant, finePriority, tfRes, intensity, dualStereo, balance, codedBands)

	hybridBinStart := ScaledBandStart(HybridCELTStartBand, frameSize)
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

	coeffsMono := ensureFloat64Slice(&d.scratchMonoMix, len(coeffsL))
	for i := range coeffsMono {
		coeffsMono[i] = 0.5 * (coeffsL[i] + coeffsR[i])
	}

	d.updateLogE(energies, end, transient)
	d.SetPrevEnergyWithPrev(prev1Energy, energies)
	d.updateBackgroundEnergy(lm)
	d.clearFrameHistoryOutsideRange(start, end, 2)
	d.rng = rd.Range()

	d.channels = origChannels
	d.applyPendingPLCPrefilterAndFold()

	samples := d.Synthesize(coeffsMono, transient, shortBlocks)
	d.applyPostfilter(samples, frameSize, mode.LM, postfilterPeriod, postfilterGain, postfilterTapset)
	d.applyDeemphasisAndScale(samples, 1.0/32768.0)
	d.resetPLCCadence(frameSize, origChannels)

	return samples, nil
}
