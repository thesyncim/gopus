package celt

import (
	"math"

	"github.com/thesyncim/gopus/plc"
)

const celtPLCLPCOrder = 24

func (d *Decoder) resetPLCCadence(frameSize, channels int) {
	d.plcLossDuration = 0
	d.plcDuration = 0
	d.plcLastFrameType = frameNormal
	d.plcPrefilterAndFoldPending = false
	d.plcPrevLossWasPeriodic = false
	if d.plcState == nil {
		d.plcState = plc.NewState()
	}
	d.plcState.Reset()
	d.plcState.SetLastFrameParams(plc.ModeCELT, frameSize, channels)
}

func (d *Decoder) beginDecodedPacketPLCState() {
	if d == nil {
		return
	}
	if d.plcLossDuration == 0 {
		d.plcSkip = false
	}
}

func plcFrameIsNeural(frameType int) bool {
	return frameType == framePLCNeural || frameType == frameDRED
}

func (d *Decoder) lastPLCFrameWasNeural() bool {
	if d == nil {
		return false
	}
	return plcFrameIsNeural(d.plcLastFrameType)
}

func (d *Decoder) lastPLCFrameWasPeriodic() bool {
	if d == nil {
		return false
	}
	return d.plcLastFrameType == framePLCPeriodic
}

func (d *Decoder) chooseLostFrameType(start int, allowNeural, allowDRED bool) int {
	currFrameType := framePLCPeriodic
	if d == nil {
		return currFrameType
	}
	if d.plcDuration >= 40 || start != 0 || d.plcSkip {
		currFrameType = framePLCNoise
	}
	if start == 0 && allowNeural && d.plcDuration < 80 && !d.plcSkip {
		currFrameType = framePLCNeural
		if allowDRED {
			currFrameType = frameDRED
		}
	}
	return currFrameType
}

func (d *Decoder) finishLostFrame(currFrameType, frameSize int) {
	if d == nil {
		return
	}
	d.accumulatePLCLossDuration(frameSize)
	switch currFrameType {
	case framePLCNoise:
		d.plcSkip = true
	case frameDRED:
		d.plcDuration = 0
		d.plcSkip = false
	}
	d.plcLastFrameType = currFrameType
	d.plcPrevLossWasPeriodic = currFrameType == framePLCPeriodic
}

func (d *Decoder) applyPendingPLCPrefilterAndFold() {
	if !d.plcPrefilterAndFoldPending {
		return
	}
	// Match libopus cadence: consume the pending fold exactly once.
	d.plcPrefilterAndFoldPending = false

	if d.channels <= 0 || Overlap <= 0 {
		return
	}
	if len(d.plcDecodeMem) < plcDecodeBufferSize*d.channels {
		return
	}
	if len(d.overlapBuffer) < Overlap*d.channels {
		return
	}

	const history = combFilterHistory
	const segLen = Overlap
	if history <= 0 || segLen <= 0 || plcDecodeBufferSize < history {
		return
	}

	bufLen := history + segLen
	d.scratchPLCFoldSrc = ensureFloat64Slice(&d.scratchPLCFoldSrc, bufLen)
	d.scratchPLCFoldDst = ensureFloat64Slice(&d.scratchPLCFoldDst, bufLen)
	window := GetWindowBuffer(segLen)
	half := segLen >> 1

	for ch := 0; ch < d.channels; ch++ {
		hist := d.plcDecodeMem[ch*plcDecodeBufferSize : (ch+1)*plcDecodeBufferSize]
		overlap := d.overlapBuffer[ch*segLen : (ch+1)*segLen]
		src := d.scratchPLCFoldSrc[:bufLen]
		dst := d.scratchPLCFoldDst[:bufLen]

		copy(src[:history], hist[plcDecodeBufferSize-history:])
		copy(src[history:], overlap)

		combFilterWithInputF32(
			dst, src, history,
			d.postfilterPeriodOld, d.postfilterPeriod, segLen,
			-d.postfilterGainOld, -d.postfilterGain,
			d.postfilterTapsetOld, d.postfilterTapset,
			nil, 0,
		)

		etmp := dst[history : history+segLen]
		for i := 0; i < half; i++ {
			// Simulate TDAC blending exactly where libopus mutates decode_mem.
			w0 := float32(window[i])
			w1 := float32(window[segLen-1-i])
			x0 := float32(etmp[segLen-1-i])
			x1 := float32(etmp[i])
			overlap[i] = float64(w0*x0 + w1*x1)
		}
	}
}

func (d *Decoder) accumulatePLCLossDuration(frameSize int) {
	lm := GetModeConfig(frameSize).LM
	if lm < 0 {
		lm = 0
	}
	if lm > 30 {
		lm = 30
	}
	d.plcLossDuration += 1 << uint(lm)
	if d.plcLossDuration > 10000 {
		d.plcLossDuration = 10000
	}
	d.plcDuration += 1 << uint(lm)
	if d.plcDuration > 10000 {
		d.plcDuration = 10000
	}
}

func (d *Decoder) applyLossEnergySafety(intra bool, start, end, lm int) {
	// Port of libopus celt_decode_with_ec() loss-recovery safety before
	// unquant_coarse_energy(): clamp oldBandE prediction after packet loss.
	if intra || d.plcLossDuration == 0 {
		return
	}
	if start < 0 {
		start = 0
	}
	if end > MaxBands {
		end = MaxBands
	}
	if start >= end {
		return
	}
	if lm < 0 {
		lm = 0
	}
	if lm > 30 {
		lm = 30
	}

	missing := min(10, d.plcLossDuration>>uint(lm))
	safety := 0.0
	switch lm {
	case 0:
		safety = 1.5
	case 1:
		safety = 0.5
	}

	for c := 0; c < d.channels; c++ {
		base := c * MaxBands
		if base+end > len(d.prevEnergy) || base+end > len(d.prevLogE) || base+end > len(d.prevLogE2) {
			continue
		}
		for i := start; i < end; i++ {
			idx := base + i
			e0 := d.prevEnergy[idx]
			e1 := d.prevLogE[idx]
			e2 := d.prevLogE2[idx]

			maxPrev := e1
			if e2 > maxPrev {
				maxPrev = e2
			}
			if e0 < maxPrev {
				slope := e1 - e0
				halfSlope := 0.5 * (e2 - e0)
				if halfSlope > slope {
					slope = halfSlope
				}
				if slope > 2.0 {
					slope = 2.0
				}
				dec := float64(1+missing) * slope
				if dec < 0 {
					dec = 0
				}
				e0 -= dec
				if e0 < -20.0 {
					e0 = -20.0
				}
			} else {
				if e1 < e0 {
					e0 = e1
				}
				if e2 < e0 {
					e0 = e2
				}
			}
			d.prevEnergy[idx] = e0 - safety
		}
	}
}

// DecodeHybridFECPLC generates CELT concealment for hybrid cadence.
// This mirrors the decode_fec behavior where CELT PLC is accumulated on top of
// SILK LBRR, and it is also used for the 5 ms hybrid->CELT transition decode.
// Decoder-side postfilter/de-emphasis ordering matches libopus.
func (d *Decoder) DecodeHybridFECPLC(frameSize int) ([]float64, error) {
	if frameSize != 240 && frameSize != 480 && frameSize != 960 {
		return nil, ErrInvalidFrameSize
	}

	if d.plcState == nil {
		d.plcState = plc.NewState()
	}
	_ = d.plcState.RecordLoss()
	prevLossDuration := d.plcLossDuration
	d.accumulatePLCLossDuration(frameSize)
	d.plcPrevLossWasPeriodic = false
	d.plcPrefilterAndFoldPending = false
	d.plcLastFrameType = framePLCNoise
	d.plcSkip = true

	outLen := frameSize * d.channels
	d.scratchPLC = ensureFloat64Slice(&d.scratchPLC, outLen)
	decayDB := 0.5
	if prevLossDuration == 0 {
		decayDB = 1.5
	}
	d.ensureBackgroundEnergyState()
	d.scratchPrevEnergy = ensureFloat64Slice(&d.scratchPrevEnergy, len(d.prevEnergy))
	concealEnergy := d.scratchPrevEnergy[:len(d.prevEnergy)]
	copy(concealEnergy, d.prevEnergy)

	// Match libopus celt_decode_lost() noise PLC cadence: in hybrid mode,
	// only the coded CELT band range [start,end) gets decayed/floored.
	mode := GetModeConfig(frameSize)
	start := HybridCELTStartBand
	end := EffectiveBandsForFrameSize(d.bandwidth, frameSize)
	if end > mode.EffBands {
		end = mode.EffBands
	}
	if end < start {
		end = start
	}
	for c := 0; c < d.channels; c++ {
		base := c * MaxBands
		for band := start; band < end; band++ {
			idx := base + band
			e := d.prevEnergy[idx] - decayDB
			if d.backgroundEnergy[idx] > e {
				e = d.backgroundEnergy[idx]
			}
			concealEnergy[idx] = e
		}
	}

	seed := d.rng
	if d.channels == 2 {
		d.scratchPLCHybridNormL = ensureFloat64Slice(&d.scratchPLCHybridNormL, frameSize)
		d.scratchPLCHybridNormR = ensureFloat64Slice(&d.scratchPLCHybridNormR, frameSize)
		coeffsL := d.scratchPLCHybridNormL[:frameSize]
		coeffsR := d.scratchPLCHybridNormR[:frameSize]
		clear(coeffsL)
		clear(coeffsR)
		fillHybridPLCNoiseCoeffs(coeffsL, frameSize, start, end, &seed)
		fillHybridPLCNoiseCoeffs(coeffsR, frameSize, start, end, &seed)
		denormalizeCoeffs(coeffsL, concealEnergy[:MaxBands], end, frameSize)
		denormalizeCoeffs(coeffsR, concealEnergy[MaxBands:], end, frameSize)
		samples := d.SynthesizeStereo(coeffsL, coeffsR, false, 1)
		copy(d.scratchPLC[:outLen], samples[:min(outLen, len(samples))])
	} else {
		d.scratchPLCHybridNormL = ensureFloat64Slice(&d.scratchPLCHybridNormL, frameSize)
		coeffs := d.scratchPLCHybridNormL[:frameSize]
		clear(coeffs)
		fillHybridPLCNoiseCoeffs(coeffs, frameSize, start, end, &seed)
		denormalizeCoeffs(coeffs, concealEnergy[:MaxBands], end, frameSize)
		samples := d.Synthesize(coeffs, false, 1)
		copy(d.scratchPLC[:outLen], samples[:min(outLen, len(samples))])
	}
	d.SetPrevEnergy(concealEnergy)
	d.rng = seed

	d.applyPostfilter(d.scratchPLC[:outLen], frameSize, mode.LM, d.postfilterPeriod, d.postfilterGain, d.postfilterTapset)
	d.applyDeemphasisAndScale(d.scratchPLC[:outLen], 1.0/32768.0)

	return d.scratchPLC[:outLen], nil
}

func fillHybridPLCNoiseCoeffs(coeffs []float64, frameSize, startBand, endBand int, seed *uint32) {
	if len(coeffs) < frameSize || frameSize <= 0 {
		return
	}
	if startBand < 0 {
		startBand = 0
	}
	if endBand > MaxBands {
		endBand = MaxBands
	}
	if endBand < startBand {
		endBand = startBand
	}

	for band := startBand; band < endBand; band++ {
		start := ScaledBandStart(band, frameSize)
		end := ScaledBandStart(band+1, frameSize)
		if start < 0 {
			start = 0
		}
		if end > frameSize {
			end = frameSize
		}
		if start >= end {
			continue
		}
		for i := start; i < end; i++ {
			*seed = *seed*1664525 + 1013904223
			coeffs[i] = float64(int32(*seed) >> 20)
		}
		renormalizeVector(coeffs[start:end], 1.0)
	}
}

// decodePLC generates concealment audio for a lost CELT packet.
func (d *Decoder) decodePLC(frameSize int) ([]float64, error) {
	if !ValidFrameSize(frameSize) {
		return nil, ErrInvalidFrameSize
	}

	// Keep PLC loss cadence bookkeeping.
	prevLossDuration := d.plcLossDuration
	_ = d.plcState.RecordLoss()
	lossCount := d.plcState.LostCount()

	// Ensure scratch buffer is large enough
	outLen := frameSize * d.channels
	plcLen := (frameSize + Overlap) * d.channels
	d.scratchPLC = ensureFloat64Slice(&d.scratchPLC, plcLen)

	currFrameType := d.chooseLostFrameType(0, false, false)

	// Match libopus decode_lost() mode cadence: favor periodic concealment in the
	// early loss window and fall back to noise-based concealment when unavailable.
	if currFrameType == framePLCPeriodic &&
		d.concealPeriodicPLC(d.scratchPLC[:plcLen], frameSize, lossCount, d.lastPLCFrameWasPeriodic(), true) {
		d.finishLostFrame(framePLCPeriodic, frameSize)
		d.plcPrefilterAndFoldPending = true
		d.updatePLCOverlapBuffer(d.scratchPLC[:plcLen], frameSize)
		d.applyDeemphasisAndScale(d.scratchPLC[:outLen], 1.0/32768.0)
		return d.scratchPLC[:outLen], nil
	}
	// Match libopus noise-PLC transition cadence: if periodic PLC left a pending
	// fold, consume it before switching to noise concealment.
	d.applyPendingPLCPrefilterAndFold()
	d.plcPrefilterAndFoldPending = false

	d.concealNoisePLC(d.scratchPLC[:outLen], frameSize, prevLossDuration)
	d.finishLostFrame(framePLCNoise, frameSize)

	return d.scratchPLC[:outLen], nil
}

func (d *Decoder) concealNoisePLC(dst []float64, frameSize, prevLossDuration int) {
	if len(dst) < frameSize*d.channels {
		return
	}
	mode := GetModeConfig(frameSize)
	d.ensureBackgroundEnergyState()
	d.scratchPrevEnergy = ensureFloat64Slice(&d.scratchPrevEnergy, len(d.prevEnergy))
	concealEnergy := d.scratchPrevEnergy[:len(d.prevEnergy)]
	copy(concealEnergy, d.prevEnergy)

	decayDB := 0.5
	if prevLossDuration == 0 {
		decayDB = 1.5
	}
	start := 0
	end := EffectiveBandsForFrameSize(d.bandwidth, frameSize)
	if end > mode.EffBands {
		end = mode.EffBands
	}
	if end < start {
		end = start
	}
	for c := 0; c < d.channels; c++ {
		base := c * MaxBands
		for band := start; band < end; band++ {
			idx := base + band
			e := d.prevEnergy[idx] - decayDB
			if d.backgroundEnergy[idx] > e {
				e = d.backgroundEnergy[idx]
			}
			concealEnergy[idx] = e
		}
	}

	seed := d.rng
	if d.channels == 2 {
		d.scratchPLCHybridNormL = ensureFloat64Slice(&d.scratchPLCHybridNormL, frameSize)
		d.scratchPLCHybridNormR = ensureFloat64Slice(&d.scratchPLCHybridNormR, frameSize)
		coeffsL := d.scratchPLCHybridNormL[:frameSize]
		coeffsR := d.scratchPLCHybridNormR[:frameSize]
		clear(coeffsL)
		clear(coeffsR)
		fillHybridPLCNoiseCoeffs(coeffsL, frameSize, start, end, &seed)
		fillHybridPLCNoiseCoeffs(coeffsR, frameSize, start, end, &seed)
		denormalizeCoeffs(coeffsL, concealEnergy[:MaxBands], end, frameSize)
		denormalizeCoeffs(coeffsR, concealEnergy[MaxBands:], end, frameSize)
		samples := d.SynthesizeStereo(coeffsL, coeffsR, false, 1)
		copy(dst[:frameSize*d.channels], samples[:min(len(samples), frameSize*d.channels)])
	} else {
		d.scratchPLCHybridNormL = ensureFloat64Slice(&d.scratchPLCHybridNormL, frameSize)
		coeffs := d.scratchPLCHybridNormL[:frameSize]
		clear(coeffs)
		fillHybridPLCNoiseCoeffs(coeffs, frameSize, start, end, &seed)
		denormalizeCoeffs(coeffs, concealEnergy[:MaxBands], end, frameSize)
		samples := d.Synthesize(coeffs, false, 1)
		copy(dst[:frameSize*d.channels], samples[:min(len(samples), frameSize*d.channels)])
	}
	d.SetPrevEnergy(concealEnergy)
	d.rng = seed

	d.applyPostfilter(dst[:frameSize*d.channels], frameSize, mode.LM, d.postfilterPeriod, d.postfilterGain, d.postfilterTapset)
	d.applyDeemphasisAndScale(dst[:frameSize*d.channels], 1.0/32768.0)
}

func (d *Decoder) concealPeriodicPLC(dst []float64, frameSize, lossCount int, continuePeriodic bool, commit bool) bool {
	if frameSize <= 0 || d.channels <= 0 {
		return false
	}
	totalSamples := frameSize + Overlap
	if len(dst) < totalSamples*d.channels {
		return false
	}
	if len(d.plcDecodeMem) < plcDecodeBufferSize*d.channels {
		return false
	}
	if len(d.plcLPC) < celtPLCLPCOrder*d.channels {
		return false
	}
	// Match libopus: prefer periodic PLC only for the early loss window.
	// celt_decode_lost() switches away once prior PLC duration reaches 40 units
	// (about 100 ms at 48 kHz).
	if lossCount > 1 && (lossCount-1)*frameSize >= 4800 {
		return false
	}

	fade := 1.0
	period := 0
	if continuePeriodic &&
		d.plcLastPitchPeriod >= combFilterMinPeriod &&
		d.plcLastPitchPeriod <= combFilterMaxPeriod {
		period = d.plcLastPitchPeriod
		fade = 0.8
	} else {
		period = d.searchPLCPitchPeriod()
	}
	if period < combFilterMinPeriod || period > combFilterMaxPeriod || period > combFilterHistory {
		return false
	}
	d.plcLastPitchPeriod = period

	const maxPeriod = combFilterMaxPeriod
	if frameSize > plcDecodeBufferSize-maxPeriod || totalSamples > plcDecodeBufferSize {
		return false
	}
	excLength := min(2*period, maxPeriod)
	if excLength <= 0 {
		return false
	}
	extrapolationOffset := maxPeriod - period
	if extrapolationOffset < 0 || extrapolationOffset+period > maxPeriod {
		return false
	}

	d.scratchPLCExc = ensureFloat64Slice(&d.scratchPLCExc, maxPeriod+celtPLCLPCOrder)
	d.scratchPLCFIRTmp = ensureFloat64Slice(&d.scratchPLCFIRTmp, excLength)
	d.scratchPLCBuf = ensureFloat64Slice(&d.scratchPLCBuf, plcDecodeBufferSize+Overlap)
	d.scratchPLCIIRMem = ensureFloat64Slice(&d.scratchPLCIIRMem, celtPLCLPCOrder)

	window := GetWindowBuffer(Overlap)
	continuePeriodic = lossCount > 1 && continuePeriodic
	channels := d.channels
	for ch := 0; ch < channels; ch++ {
		hist := d.plcDecodeMem[ch*plcDecodeBufferSize : (ch+1)*plcDecodeBufferSize]
		lpc := d.plcLPC[ch*celtPLCLPCOrder : (ch+1)*celtPLCLPCOrder]

		exc := d.scratchPLCExc[:maxPeriod+celtPLCLPCOrder]
		copy(exc, hist[plcDecodeBufferSize-maxPeriod-celtPLCLPCOrder:])

		if !continuePeriodic {
			d.computePLCLPC(exc[celtPLCLPCOrder:], lpc, window)
		}

		firStart := celtPLCLPCOrder + maxPeriod - excLength
		firTmp := d.scratchPLCFIRTmp[:excLength]
		for i := 0; i < excLength; i++ {
			idx := firStart + i
			sum := float32(exc[idx])
			// Match libopus celt_fir() accumulation order in float path:
			// rnum[j]=lpc[ord-1-j], x[i+j-ord].
			for j := 0; j < celtPLCLPCOrder; j++ {
				coeff := float32(lpc[celtPLCLPCOrder-1-j])
				sample := float32(exc[idx-celtPLCLPCOrder+j])
				sum += coeff * sample
			}
			firTmp[i] = float64(sum)
		}
		copy(exc[firStart:firStart+excLength], firTmp)

		decay := float32(1.0)
		decayLength := excLength >> 1
		if decayLength > 0 {
			e1 := float32(1.0)
			e2 := float32(1.0)
			base1 := celtPLCLPCOrder + maxPeriod - decayLength
			base2 := celtPLCLPCOrder + maxPeriod - 2*decayLength
			for i := 0; i < decayLength; i++ {
				v1 := float32(exc[base1+i])
				v2 := float32(exc[base2+i])
				e1 += v1 * v1
				e2 += v2 * v2
			}
			if e1 > e2 {
				e1 = e2
			}
			if e2 > 0 {
				decay = float32(math.Sqrt(float64(e1 / e2)))
			}
		}

		attenuation := float32(fade) * decay
		buf := d.scratchPLCBuf[:plcDecodeBufferSize+Overlap]
		copy(buf[:plcDecodeBufferSize], hist)
		copy(buf[:plcDecodeBufferSize-frameSize], buf[frameSize:plcDecodeBufferSize])
		chOut := buf[plcDecodeBufferSize-frameSize : plcDecodeBufferSize-frameSize+totalSamples]
		s1 := float32(0)
		s1Base := plcDecodeBufferSize - maxPeriod + extrapolationOffset
		j := 0
		for i := 0; i < totalSamples; i++ {
			if j >= period {
				j = 0
				attenuation *= decay
			}
			chOut[i] = float64(attenuation * float32(exc[celtPLCLPCOrder+extrapolationOffset+j]))
			srcIdx := s1Base + j
			if srcIdx >= 0 && srcIdx < len(hist) {
				v := float32(hist[srcIdx])
				s1 += v * v
			}
			j++
		}

		// Match libopus celt_iir()'s sample-by-sample state updates during
		// periodic PLC synthesis.
		ord := celtPLCLPCOrder
		iirMem := d.scratchPLCIIRMem[:ord]
		memBase := plcDecodeBufferSize - 1
		for i := 0; i < ord; i++ {
			iirMem[i] = hist[memBase-i]
		}
		for i := 0; i < totalSamples; i++ {
			sum := float32(chOut[i])
			for j := 0; j < ord; j++ {
				sum -= float32(lpc[j]) * float32(iirMem[j])
			}
			for j := ord - 1; j >= 1; j-- {
				iirMem[j] = iirMem[j-1]
			}
			iirMem[0] = float64(sum)
			chOut[i] = float64(sum)
		}

		s2 := float32(0)
		for i := 0; i < totalSamples; i++ {
			v := float32(chOut[i])
			s2 += v * v
		}
		if !(s1 > float32(0.2)*s2) {
			for i := 0; i < totalSamples; i++ {
				chOut[i] = 0
			}
		} else if s1 < s2 {
			ratio := float32(math.Sqrt(float64((s1 + 1.0) / (s2 + 1.0))))
			blend := min(Overlap, totalSamples)
			for i := 0; i < blend; i++ {
				g := float32(1.0) - float32(window[i])*(float32(1.0)-ratio)
				chOut[i] = float64(float32(chOut[i]) * g)
			}
			for i := blend; i < totalSamples; i++ {
				chOut[i] = float64(float32(chOut[i]) * ratio)
			}
		}

		for i := 0; i < totalSamples; i++ {
			dst[i*channels+ch] = chOut[i]
		}
	}

	if commit {
		d.updatePostfilterHistory(dst[:frameSize*channels], frameSize, combFilterHistory)
		d.updatePLCDecodeHistory(dst[:frameSize*channels], frameSize, plcDecodeBufferSize)
	}
	return true
}

func (d *Decoder) computePLCLPC(frame, lpc, window []float64) {
	n := len(frame)
	if n <= 0 {
		for i := range lpc {
			lpc[i] = 0
		}
		return
	}
	d.scratchPLCWindowed = ensureFloat64Slice(&d.scratchPLCWindowed, n)
	x := d.scratchPLCWindowed[:n]
	copy(x, frame)

	overlap := Overlap
	if overlap > n>>1 {
		overlap = n >> 1
	}
	for i := 0; i < overlap && i < len(window); i++ {
		w := float32(window[i])
		x[i] = float64(float32(x[i]) * w)
		x[n-1-i] = float64(float32(x[n-1-i]) * w)
	}

	var ac [celtPLCLPCOrder + 1]float64
	fastN := n - celtPLCLPCOrder
	if fastN < 0 {
		fastN = 0
	}
	for lag := 0; lag <= celtPLCLPCOrder; lag++ {
		sum := float32(0)
		for i := 0; i < fastN; i++ {
			sum += float32(x[i]) * float32(x[i+lag])
		}
		tail := float32(0)
		for i := lag + fastN; i < n; i++ {
			tail += float32(x[i]) * float32(x[i-lag])
		}
		ac[lag] = float64(sum + tail)
	}

	// Match libopus float path: add a tiny noise floor and lag windowing.
	ac[0] = float64(float32(ac[0]) * float32(1.0001))
	for i := 1; i <= celtPLCLPCOrder; i++ {
		f := float32(0.008) * float32(i)
		ac[i] = float64(float32(ac[i]) - float32(ac[i])*f*f)
	}
	plcLPCFromAutocorr(ac[:], lpc)
}

func plcLPCFromAutocorr(ac []float64, lpc []float64) {
	for i := range lpc {
		lpc[i] = 0
	}
	if len(ac) < len(lpc)+1 || ac[0] <= 1e-10 {
		return
	}

	var lpc32 [celtPLCLPCOrder]float32
	base := float32(ac[0])
	errorPower := base
	for i := 0; i < len(lpc); i++ {
		if errorPower <= 0 {
			break
		}
		rr := float32(0)
		for j := 0; j < i; j++ {
			rr += lpc32[j] * float32(ac[i-j])
		}
		rr += float32(ac[i+1])
		r := -rr / errorPower
		if math.IsNaN(float64(r)) || math.IsInf(float64(r), 0) {
			break
		}
		lpc32[i] = r
		for j := 0; j < (i+1)>>1; j++ {
			tmp1 := lpc32[j]
			tmp2 := lpc32[i-1-j]
			lpc32[j] = tmp1 + r*tmp2
			lpc32[i-1-j] = tmp2 + r*tmp1
		}
		errorPower -= (r * r) * errorPower
		if errorPower <= float32(0.001)*base {
			break
		}
	}
	for i := range lpc {
		lpc[i] = float64(lpc32[i])
	}
}

func (d *Decoder) updatePLCOverlapBuffer(plcSamples []float64, frameSize int) {
	if Overlap <= 0 || frameSize <= 0 || d.channels <= 0 {
		return
	}
	channels := d.channels
	totalSamples := frameSize + Overlap
	if len(plcSamples) < totalSamples*channels {
		return
	}

	overlapNeeded := Overlap * channels
	if len(d.overlapBuffer) < overlapNeeded {
		d.overlapBuffer = make([]float64, overlapNeeded)
	}

	if channels == 1 {
		copy(d.overlapBuffer[:Overlap], plcSamples[frameSize:frameSize+Overlap])
		return
	}

	src := frameSize * channels
	for i := 0; i < Overlap; i++ {
		d.overlapBuffer[i] = plcSamples[src+i*channels]
		d.overlapBuffer[Overlap+i] = plcSamples[src+i*channels+1]
	}
}

func pitchXCorrFloat32(x, y, xcorr []float64, length, maxPitch int) {
	if length <= 0 || maxPitch <= 0 {
		return
	}
	_ = x[length-1]
	_ = y[maxPitch+length-2]
	_ = xcorr[maxPitch-1]
	for i := 0; i < maxPitch; i++ {
		sum := float32(0)
		for j := 0; j < length; j++ {
			sum += float32(x[j]) * float32(y[i+j])
		}
		xcorr[i] = float64(sum)
	}
}

func innerProdFloat32(x, y []float64, length int) float64 {
	if length <= 0 {
		return 0
	}
	_ = x[length-1]
	_ = y[length-1]
	sum := float32(0)
	for i := 0; i < length; i++ {
		sum += float32(x[i]) * float32(y[i])
	}
	return float64(sum)
}

func pitchSearchPLC(xLP []float64, y []float64, length, maxPitch int, scratch *encoderScratch) int {
	if length <= 0 || maxPitch <= 0 {
		return 0
	}
	lag := length + maxPitch

	xLP4 := ensureFloat64Slice(&scratch.prefilterXLP4, length>>2)
	yLP4 := ensureFloat64Slice(&scratch.prefilterYLP4, lag>>2)
	xcorr := ensureFloat64Slice(&scratch.prefilterXcorr, maxPitch>>1)

	for j := 0; j < length>>2; j++ {
		xLP4[j] = xLP[2*j]
	}
	for j := 0; j < lag>>2; j++ {
		yLP4[j] = y[2*j]
	}

	pitchXCorrFloat32(xLP4, yLP4, xcorr, length>>2, maxPitch>>2)
	bestPitch := [2]int{0, 0}
	findBestPitch(xcorr, yLP4, length>>2, maxPitch>>2, &bestPitch)

	halfPitch := maxPitch >> 1
	ranges := pitchSearchFineRanges(bestPitch, halfPitch)
	halfLen := length >> 1
	Syy := float32(1)
	for j := 0; j < halfLen; j++ {
		yj := float32(y[j])
		Syy += yj * yj
	}
	bestNum := [2]float32{-1, -1}
	bestDen := [2]float32{0, 0}
	fineBestPitch := [2]int{0, 1}
	i := 0
	for _, r := range ranges {
		if r.hi < r.lo {
			continue
		}
		for ; i < r.lo; i++ {
			yi := float32(y[i])
			yil := float32(y[i+halfLen])
			Syy += yil*yil - yi*yi
			if Syy < 1 {
				Syy = 1
			}
		}
		for ; i <= r.hi; i++ {
			sum := innerProdFloat32(xLP, y[i:], halfLen)
			if sum < -1 {
				sum = -1
			}
			xcorr[i] = sum
			if sum > 0 {
				xc := float32(sum)
				xcorr16 := xc * pitchSearchXcorrScale
				num := xcorr16 * xcorr16
				if num*bestDen[1] > bestNum[1]*Syy {
					if num*bestDen[0] > bestNum[0]*Syy {
						bestNum[1] = bestNum[0]
						bestDen[1] = bestDen[0]
						fineBestPitch[1] = fineBestPitch[0]
						bestNum[0] = num
						bestDen[0] = Syy
						fineBestPitch[0] = i
					} else {
						bestNum[1] = num
						bestDen[1] = Syy
						fineBestPitch[1] = i
					}
				}
			}
			yi := float32(y[i])
			yil := float32(y[i+halfLen])
			Syy += yil*yil - yi*yi
			if Syy < 1 {
				Syy = 1
			}
		}
	}
	bestPitch = fineBestPitch

	offset := 0
	if bestPitch[0] > 0 && bestPitch[0] < halfPitch-1 {
		a := xcorr[bestPitch[0]-1]
		b := xcorr[bestPitch[0]]
		c := xcorr[bestPitch[0]+1]
		if (c - a) > 0.7*(b-a) {
			offset = 1
		} else if (a - c) > 0.7*(b-c) {
			offset = -1
		}
	}
	return 2*bestPitch[0] - offset
}

func (d *Decoder) searchPLCPitchPeriod() int {
	channels := d.channels
	if channels <= 0 {
		return 0
	}
	if len(d.plcDecodeMem) < plcDecodeBufferSize*channels {
		return 0
	}

	const (
		plcPitchLagMax = 720
		plcPitchLagMin = 100
	)
	searchLen := plcDecodeBufferSize - plcPitchLagMax
	maxPitch := plcPitchLagMax - plcPitchLagMin
	if searchLen <= 0 || maxPitch <= 0 {
		return 0
	}
	lpLen := plcDecodeBufferSize >> 1
	if lpLen <= (plcPitchLagMax >> 1) {
		return 0
	}
	d.scratchPLCPitchLP = ensureFloat64Slice(&d.scratchPLCPitchLP, lpLen)
	pitchDownsample(d.plcDecodeMem, d.scratchPLCPitchLP, lpLen, channels, 2)

	searchOut := pitchSearchPLC(
		d.scratchPLCPitchLP[plcPitchLagMax>>1:],
		d.scratchPLCPitchLP,
		searchLen,
		maxPitch,
		&d.scratchPLCPitchSearch,
	)
	pitch := plcPitchLagMax - searchOut
	if pitch < combFilterMinPeriod || pitch > combFilterMaxPeriod {
		return 0
	}
	if pitch < plcPitchLagMin || pitch > plcPitchLagMax {
		return 0
	}
	return pitch
}
