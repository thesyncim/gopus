package celt

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"

	"github.com/thesyncim/gopus/util"
)

type prefilterResult struct {
	on     bool
	pitch  int
	qg     int
	tapset int
	gain   float64
}

// runPrefilter applies the CELT prefilter (comb filter) and returns the
// postfilter parameters to signal in the bitstream.
// This mirrors libopus run_prefilter() in celt_encoder.c.
func (e *Encoder) runPrefilter(preemph []float64, frameSize int, tapset int, enabled bool, tfEstimate float64, nbAvailableBytes int, toneFreq, toneishness, maxPitchRatio float64) prefilterResult {
	result := prefilterResult{on: false, pitch: combFilterMinPeriod, qg: 0, tapset: tapset, gain: 0}
	channels := e.channels
	if channels <= 0 || frameSize <= 0 || len(preemph) == 0 {
		return result
	}
	var dbg *PrefilterDebugStats
	if e.prefilterDebugHook != nil {
		d := PrefilterDebugStats{
			Frame:         e.frameCount,
			Enabled:       enabled,
			TFEstimate:    tfEstimate,
			NBBytes:       nbAvailableBytes,
			ToneFreq:      toneFreq,
			Toneishness:   toneishness,
			MaxPitchRatio: maxPitchRatio,
		}
		dbg = &d
	}

	if tapset < 0 {
		tapset = 0
	}
	if tapset >= len(combFilterGains) {
		tapset = len(combFilterGains) - 1
	}

	maxPeriod := combFilterMaxPeriod
	minPeriod := combFilterMinPeriod
	prevPeriod := e.prefilterPeriod
	if prevPeriod < minPeriod {
		prevPeriod = minPeriod
	}
	if prevPeriod > maxPeriod-2 {
		prevPeriod = maxPeriod - 2
	}
	prevTapset := e.prefilterTapset
	if prevTapset < 0 {
		prevTapset = 0
	}
	if prevTapset >= len(combFilterGains) {
		prevTapset = len(combFilterGains) - 1
	}
	perChanLen := maxPeriod + frameSize
	pre := ensureFloat64Slice(&e.scratch.prefilterPre, perChanLen*channels)
	out := ensureFloat64Slice(&e.scratch.prefilterOut, perChanLen*channels)

	for ch := 0; ch < channels; ch++ {
		hist := e.prefilterMem[ch*maxPeriod : (ch+1)*maxPeriod]
		copy(pre[ch*perChanLen:ch*perChanLen+maxPeriod], hist)
		for i := 0; i < frameSize; i++ {
			pre[ch*perChanLen+maxPeriod+i] = preemph[i*channels+ch]
		}
	}
	// Keep prefilter inputs at float32 precision to match libopus celt_sig path.
	if !tmpSkipPrefInputRoundEnabled {
		roundFloat64ToFloat32(pre)
	}

	pitchIndex := minPeriod
	gain1 := 0.0
	qg := 0
	pfOn := false

	if enabled && toneishness > 0.99 {
		if dbg != nil {
			dbg.UsedTonePath = true
		}
		freq := toneFreq
		if freq >= math.Pi {
			freq = math.Pi - freq
		}
		multiple := 1
		for freq >= float64(multiple)*0.39 {
			multiple++
		}
		if freq > 0.006148 {
			pitchIndex = int(math.Floor(0.5 + 2.0*math.Pi*float64(multiple)/freq))
			if pitchIndex > maxPeriod-2 {
				pitchIndex = maxPeriod - 2
			}
		} else {
			pitchIndex = minPeriod
		}
		gain1 = 0.75
	} else if enabled && e.complexity >= 5 {
		if dbg != nil {
			dbg.UsedPitchPath = true
		}
		pitchBufLen := (maxPeriod + frameSize) >> 1
		if pitchBufLen < 1 {
			pitchBufLen = 1
		}
		pitchBuf := ensureFloat64Slice(&e.scratch.prefilterPitchBuf, pitchBufLen)
		pitchDownsample(pre, pitchBuf, pitchBufLen, channels, 2)
		maxPitch := maxPeriod - 3*minPeriod
		if maxPitch < 1 {
			maxPitch = 1
		}
		searchOut := pitchSearch(pitchBuf[maxPeriod>>1:], pitchBuf, frameSize, maxPitch, &e.scratch)
		if dbg != nil {
			dbg.PitchSearchOut = searchOut
		}
		pitchIndex = searchOut
		pitchIndex = maxPeriod - pitchIndex
		if dbg != nil {
			dbg.PitchBeforeRD = pitchIndex
		}
		gain1 = removeDoubling(pitchBuf, maxPeriod, minPeriod, frameSize, &pitchIndex, e.prefilterPeriod, e.prefilterGain, &e.scratch)
		if dbg != nil {
			dbg.PitchAfterRD = pitchIndex
		}
		if pitchIndex > maxPeriod-2 {
			pitchIndex = maxPeriod - 2
		}
		gain1 *= 0.7
		if e.packetLoss > 2 {
			gain1 *= 0.5
		}
		if e.packetLoss > 4 {
			gain1 *= 0.5
		}
		if e.packetLoss > 8 {
			gain1 = 0
		}
	} else {
		gain1 = 0
		pitchIndex = minPeriod
	}
	// Match libopus run_prefilter() scaling by analysis->max_pitch_ratio.
	if maxPitchRatio < 0 {
		maxPitchRatio = 0
	}
	if maxPitchRatio > 1 {
		maxPitchRatio = 1
	}
	gain1 *= maxPitchRatio

	// Gain threshold for enabling the prefilter/postfilter
	pfThreshold := 0.2
	if util.Abs(pitchIndex-e.prefilterPeriod)*10 > pitchIndex {
		pfThreshold += 0.2
		if tfEstimate > 0.98 {
			gain1 = 0
		}
	}
	if nbAvailableBytes < 25 {
		pfThreshold += 0.1
	}
	if nbAvailableBytes < 35 {
		pfThreshold += 0.1
	}
	if e.prefilterGain > 0.4 {
		pfThreshold -= 0.1
	}
	if e.prefilterGain > 0.55 {
		pfThreshold -= 0.1
	}
	if pfThreshold < 0.2 {
		pfThreshold = 0.2
	}
	if gain1 < pfThreshold {
		gain1 = 0
		pfOn = false
		qg = 0
	} else {
		if math.Abs(gain1-e.prefilterGain) < 0.1 {
			gain1 = e.prefilterGain
		}
		qg = int(math.Floor(0.5+gain1*32.0/3.0)) - 1
		if qg < 0 {
			qg = 0
		}
		if qg > 7 {
			qg = 7
		}
		gain1 = 0.09375 * float64(qg+1)
		pfOn = true
	}

	mode := GetModeConfig(frameSize)
	overlap := Overlap
	if overlap > frameSize {
		overlap = frameSize
	}
	shortMdctSize := frameSize / mode.ShortBlocks
	offset := shortMdctSize - overlap
	if offset < 0 {
		offset = 0
	}
	window := GetWindowBuffer(Overlap)

	var before [2]float64
	var after [2]float64
	for ch := 0; ch < channels; ch++ {
		preCh := pre[ch*perChanLen : (ch+1)*perChanLen]
		outCh := out[ch*perChanLen : (ch+1)*perChanLen]
		preSub := preCh[maxPeriod : maxPeriod+frameSize]
		for _, v := range preSub {
			before[ch] += math.Abs(v)
		}
		if offset > 0 {
			if tmpPrefilterF64Enabled {
				combFilterWithInput(outCh, preCh, maxPeriod, prevPeriod, prevPeriod, offset, -e.prefilterGain, -e.prefilterGain, prevTapset, prevTapset, nil, 0)
			} else {
				combFilterWithInputF32(outCh, preCh, maxPeriod, prevPeriod, prevPeriod, offset, -e.prefilterGain, -e.prefilterGain, prevTapset, prevTapset, nil, 0)
			}
		}
		if tmpPrefilterF64Enabled {
			combFilterWithInput(outCh, preCh, maxPeriod+offset, prevPeriod, pitchIndex, frameSize-offset, -e.prefilterGain, -gain1, prevTapset, tapset, window, overlap)
		} else {
			combFilterWithInputF32(outCh, preCh, maxPeriod+offset, prevPeriod, pitchIndex, frameSize-offset, -e.prefilterGain, -gain1, prevTapset, tapset, window, overlap)
		}
		if tmpPrefCombDumpEnabled && channels == 1 && frameSize == 480 && e.frameCount < 32 {
			dumpFloat64AsF32Raw(fmt.Sprintf("/tmp/go_prefcomb_pre_call%d.f32", e.frameCount), preCh)
			dumpFloat64AsF32Raw(fmt.Sprintf("/tmp/go_prefcomb_out_call%d.f32", e.frameCount), outCh)
			metaPath := fmt.Sprintf("/tmp/go_prefcomb_meta_call%d.txt", e.frameCount)
			_ = os.WriteFile(metaPath, []byte(fmt.Sprintf("start=%d n=%d overlap=%d t0=%d t1=%d g0=%.9g g1=%.9g tap0=%d tap1=%d offset=%d frame=%d\n",
				maxPeriod+offset, frameSize-offset, overlap, prevPeriod, pitchIndex, -e.prefilterGain, -gain1, prevTapset, tapset, offset, e.frameCount)), 0o644)
		}
		outSub := outCh[maxPeriod : maxPeriod+frameSize]
		for _, v := range outSub {
			after[ch] += math.Abs(v)
		}
	}

	cancelPitch := false
	if channels == 2 {
		thresh0 := 0.25*gain1*before[0] + 0.01*before[1]
		thresh1 := 0.25*gain1*before[1] + 0.01*before[0]
		if after[0]-before[0] > thresh0 || after[1]-before[1] > thresh1 {
			cancelPitch = true
		}
		if before[0]-after[0] < thresh0 && before[1]-after[1] < thresh1 {
			cancelPitch = true
		}
	} else {
		if after[0] > before[0] {
			cancelPitch = true
		}
	}

	if cancelPitch {
		for ch := 0; ch < channels; ch++ {
			preCh := pre[ch*perChanLen : (ch+1)*perChanLen]
			outCh := out[ch*perChanLen : (ch+1)*perChanLen]
			copy(outCh[maxPeriod:maxPeriod+frameSize], preCh[maxPeriod:maxPeriod+frameSize])
			if tmpPrefilterF64Enabled {
				combFilterWithInput(outCh, preCh, maxPeriod+offset, prevPeriod, pitchIndex, overlap, -e.prefilterGain, -0, prevTapset, tapset, window, overlap)
			} else {
				combFilterWithInputF32(outCh, preCh, maxPeriod+offset, prevPeriod, pitchIndex, overlap, -e.prefilterGain, -0, prevTapset, tapset, window, overlap)
			}
		}
		gain1 = 0
		pfOn = false
		qg = 0
	}

	if overlap > 0 {
		need := channels * overlap
		if len(e.overlapBuffer) < need {
			newBuf := make([]float64, need)
			copy(newBuf, e.overlapBuffer)
			e.overlapBuffer = newBuf
		}
	}

	for ch := 0; ch < channels; ch++ {
		preCh := pre[ch*perChanLen : (ch+1)*perChanLen]
		outCh := out[ch*perChanLen : (ch+1)*perChanLen]
		mem := e.prefilterMem[ch*maxPeriod : (ch+1)*maxPeriod]
		if frameSize > maxPeriod {
			copy(mem, preCh[frameSize:frameSize+maxPeriod])
		} else {
			copy(mem, mem[frameSize:])
			copy(mem[maxPeriod-frameSize:], preCh[maxPeriod:maxPeriod+frameSize])
		}
		if !tmpSkipPrefMemRoundEnabled {
			roundFloat64ToFloat32(mem)
		}
		outSub2 := outCh[maxPeriod : maxPeriod+frameSize]
		for i, v := range outSub2 {
			preemph[i*channels+ch] = v
		}
		if overlap > 0 && len(e.overlapBuffer) >= (ch+1)*overlap && frameSize >= overlap {
			hist := e.overlapBuffer[ch*overlap : (ch+1)*overlap]
			copy(hist, outSub2[frameSize-overlap:])
			if !tmpSkipPrefMemRoundEnabled {
				roundFloat64ToFloat32(hist)
			}
		}
	}

	e.prefilterPeriod = pitchIndex
	e.prefilterGain = gain1
	e.prefilterTapset = tapset

	result.on = pfOn
	result.pitch = pitchIndex
	result.qg = qg
	result.tapset = tapset
	result.gain = gain1
	if dbg != nil {
		dbg.PitchAfterRD = pitchIndex
		dbg.PFOn = pfOn
		dbg.QG = qg
		dbg.Gain = gain1
		e.prefilterDebugHook(*dbg)
	}
	return result
}

func dumpFloat64AsF32Raw(path string, vals []float64) {
	if len(vals) == 0 {
		return
	}
	buf := make([]byte, len(vals)*4)
	for i, v := range vals {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(float32(v)))
	}
	_ = os.WriteFile(path, buf, 0o644)
}

// estimateMaxPitchRatio approximates libopus analysis->max_pitch_ratio by
// comparing low-frequency and high-frequency spectral energy after a 2x
// downsampling step (48 kHz -> 24 kHz) with a split at bin 64 (~3.2 kHz).
// This stateless helper is kept for diagnostics/tests. EncodeFrame uses a
// stateful variant to better match analysis-window cadence in libopus.
func estimateMaxPitchRatio(pcm []float64, channels int, scratch *encoderScratch) float64 {
	if channels <= 0 || len(pcm) < channels {
		return 1
	}
	n := (len(pcm) / channels) / 2
	if n < 8 {
		return 1
	}
	down := ensureFloat64Slice(&scratch.pitchDown, n)
	hpPrefix := ensureFloat64Slice(&scratch.pitchHPPrefix, n+1)
	state := [3]float64{}
	downmixAndDown2HP(pcm, channels, down, hpPrefix, &state)
	return maxPitchRatioFromDownsampled(down, hpPrefix[n], scratch)
}

// estimateMaxPitchRatioStateful tracks analysis history so max_pitch_ratio is
// updated on window boundaries instead of independently for each frame.
func (e *Encoder) estimateMaxPitchRatioStateful(pcm []float64) float64 {
	channels := e.channels
	if channels <= 0 || len(pcm) < channels {
		return 1
	}
	n := (len(pcm) / channels) / 2
	if n < 8 {
		return e.maxPitchRatio
	}
	down := ensureFloat64Slice(&e.scratch.pitchDown, n)
	hpPrefix := ensureFloat64Slice(&e.scratch.pitchHPPrefix, n+1)
	downmixAndDown2HP(pcm, channels, down, hpPrefix, &e.maxPitchDownState)

	const (
		analysisSize  = 720
		analysisFrame = 480
		analysisHist  = 240
	)
	if len(e.maxPitchInmem) < analysisSize {
		e.maxPitchInmem = make([]float64, analysisSize)
	}
	if e.maxPitchMemFill < analysisHist || e.maxPitchMemFill > analysisSize {
		e.maxPitchMemFill = analysisHist
	}

	pos := 0
	for pos < n {
		need := analysisSize - e.maxPitchMemFill
		if need <= 0 {
			copy(e.maxPitchInmem[:analysisHist], e.maxPitchInmem[analysisFrame:analysisSize])
			e.maxPitchMemFill = analysisHist
			e.maxPitchHPEnerAccum = 0
			need = analysisSize - e.maxPitchMemFill
		}
		take := n - pos
		if take > need {
			take = need
		}
		copy(e.maxPitchInmem[e.maxPitchMemFill:e.maxPitchMemFill+take], down[pos:pos+take])
		e.maxPitchHPEnerAccum += hpPrefix[pos+take] - hpPrefix[pos]
		e.maxPitchMemFill += take
		pos += take

		if e.maxPitchMemFill == analysisSize {
			e.maxPitchRatio = maxPitchRatioFromDownsampled(e.maxPitchInmem[:analysisFrame], e.maxPitchHPEnerAccum, &e.scratch)
			copy(e.maxPitchInmem[:analysisHist], e.maxPitchInmem[analysisFrame:analysisSize])
			e.maxPitchMemFill = analysisHist
			e.maxPitchHPEnerAccum = 0
		}
	}
	if e.maxPitchRatio < 0 {
		return 0
	}
	if e.maxPitchRatio > 1 {
		return 1
	}
	return e.maxPitchRatio
}

// downmixAndDown2HP mirrors the analysis downmix path used to derive
// max_pitch_ratio: downmix to mono, downsample by 2, and track hp energy.
func downmixAndDown2HP(pcm []float64, channels int, down []float64, hpPrefix []float64, state *[3]float64) {
	s0, s1, s2 := state[0], state[1], state[2]
	hpPrefix[0] = 0
	for i := 0; i < len(down); i++ {
		idx0 := (2 * i) * channels
		idx1 := idx0 + channels

		in0 := pcm[idx0]
		in1 := pcm[idx1]
		if channels == 2 {
			in0 = 0.5 * (pcm[idx0] + pcm[idx0+1])
			in1 = 0.5 * (pcm[idx1] + pcm[idx1+1])
		}

		y := in0 - s0
		x := 0.6074371 * y
		out0 := s0 + x
		s0 = in0 + x

		y = in1 - s1
		x = 0.15063 * y
		out := out0 + s1 + x
		s1 = in1 + x

		y = -in1 - s2
		x = 0.15063 * y
		outHP := out0 + s2 + x
		s2 = -in1 + x

		down[i] = 0.5 * out
		hpPrefix[i+1] = hpPrefix[i] + outHP*outHP
	}
	state[0], state[1], state[2] = s0, s1, s2
}

func maxPitchRatioFromDownsampled(down []float64, hpEner float64, scratch *encoderScratch) float64 {
	n := len(down)
	if n < 8 {
		return 1
	}
	// Apply Hann window using incremental oscillator.
	if n > 1 {
		ang := 2.0 * math.Pi / float64(n-1)
		cosStep := math.Cos(ang)
		sinStep := math.Sin(ang)
		cosCurr := 1.0
		sinCurr := 0.0
		for i := 0; i < n; i++ {
			w := 0.5 - 0.5*cosCurr
			down[i] *= w
			nextCos := cosCurr*cosStep - sinCurr*sinStep
			sinCurr = cosCurr*sinStep + sinCurr*cosStep
			cosCurr = nextCos
		}
	}

	half := n / 2
	splitBin := (64*n + 240) / 480
	if splitBin < 1 {
		splitBin = 1
	}
	if splitBin > half {
		splitBin = half
	}

	fftIn := ensureComplex64Slice(&scratch.pitchFFTIn, n)
	fftOut := ensureComplex64Slice(&scratch.pitchFFTOut, n)
	fftTmp := ensureKissCpxSlice(&scratch.pitchFFTTmp, n)
	for i := 0; i < n; i++ {
		fftIn[i] = complex(float32(down[i]), 0)
	}
	kissFFT32To(fftOut, fftIn, fftTmp)

	var below, above float64
	for k := 0; k < half; k++ {
		re := float64(real(fftOut[k]))
		im := float64(imag(fftOut[k]))
		p := re*re + im*im
		if k < splitBin {
			below += p
		} else {
			above += p
		}
	}
	above += hpEner * (1.0 / (60.0 * 60.0))

	if above > below && above > 1e-20 {
		r := below / above
		if r < 0 {
			return 0
		}
		if r > 1 {
			return 1
		}
		return r
	}
	return 1
}

func pitchDownsample(x []float64, xLP []float64, length, channels, factor int) {
	if length <= 0 || factor <= 0 || len(xLP) < length {
		return
	}
	offset := factor / 2
	if offset < 1 {
		offset = 1
	}
	for i := 1; i < length; i++ {
		idx := factor * i
		xLP[i] = 0.25*x[idx-offset] + 0.25*x[idx+offset] + 0.5*x[idx]
	}
	xLP[0] = 0.25*x[offset] + 0.5*x[0]
	if channels == 2 {
		chStride := len(x) / 2
		x1 := x[chStride:]
		for i := 1; i < length; i++ {
			idx := factor * i
			xLP[i] += 0.25*x1[idx-offset] + 0.25*x1[idx+offset] + 0.5*x1[idx]
		}
		xLP[0] += 0.25*x1[offset] + 0.5*x1[0]
	}

	// Compute 5 autocorrelation lags using ASM-backed inner products.
	// ac[k] = innerProduct(lp[0:length-k], lp[k:length]) for k=0..4.
	var ac [5]float64
	lp := xLP[:length]
	if length > 4 {
		ac[0], ac[1] = prefilterDualInnerProd(lp, lp, lp[1:], length-1)
		// ac[0] misses the last element's self-product; add it.
		ac[0] += lp[length-1] * lp[length-1]
		ac2partial, ac3 := prefilterDualInnerProd(lp, lp[2:], lp[3:], length-3)
		// ac[2] needs length-2 terms but dualInnerProd used length-3; add the missing term.
		ac[2] = ac2partial + lp[length-3]*lp[length-1]
		ac[3] = ac3
		ac[4] = prefilterInnerProd(lp, lp[4:], length-4)
	} else {
		for i := 0; i < length; i++ {
			for lag := 0; lag <= 4 && i+lag < length; lag++ {
				ac[lag] += lp[i] * lp[i+lag]
			}
		}
	}

	ac[0] *= 1.0001
	for i := 1; i <= 4; i++ {
		f := 0.008 * float64(i)
		ac[i] -= ac[i] * f * f
	}

	lpc := lpcFromAutocorr(ac)
	tmp := 1.0
	for i := 0; i < 4; i++ {
		tmp *= 0.9
		lpc[i] *= tmp
	}
	c1 := 0.8
	lpc2 := [5]float64{
		lpc[0] + 0.8,
		lpc[1] + c1*lpc[0],
		lpc[2] + c1*lpc[1],
		lpc[3] + c1*lpc[2],
		c1 * lpc[3],
	}
	celtFIR5(xLP, lpc2)
}

func pitchSearch(xLP []float64, y []float64, length, maxPitch int, scratch *encoderScratch) int {
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

	prefilterPitchXcorr(xLP4, yLP4, xcorr, length>>2, maxPitch>>2)
	bestPitch := [2]int{0, 0}
	findBestPitch(xcorr, yLP4, length>>2, maxPitch>>2, &bestPitch)

	for i := 0; i < maxPitch>>1; i++ {
		xcorr[i] = 0
		if util.Abs(i-2*bestPitch[0]) > 2 && util.Abs(i-2*bestPitch[1]) > 2 {
			continue
		}
		sum := prefilterInnerProd(xLP, y[i:], length>>1)
		if sum < -1 {
			sum = -1
		}
		xcorr[i] = sum
	}
	findBestPitch(xcorr, y, length>>1, maxPitch>>1, &bestPitch)

	offset := 0
	if bestPitch[0] > 0 && bestPitch[0] < (maxPitch>>1)-1 {
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

func findBestPitch(xcorr []float64, y []float64, length, maxPitch int, bestPitch *[2]int) {
	Syy := 1.0
	bestNum := [2]float64{-1, -1}
	bestDen := [2]float64{0, 0}
	bestPitch[0] = 0
	bestPitch[1] = 1
	for j := 0; j < length; j++ {
		Syy += y[j] * y[j]
	}
	for i := 0; i < maxPitch; i++ {
		if xcorr[i] > 0 {
			xcorr16 := xcorr[i] * 1e-12
			num := xcorr16 * xcorr16
			if num*bestDen[1] > bestNum[1]*Syy {
				if num*bestDen[0] > bestNum[0]*Syy {
					bestNum[1] = bestNum[0]
					bestDen[1] = bestDen[0]
					bestPitch[1] = bestPitch[0]
					bestNum[0] = num
					bestDen[0] = Syy
					bestPitch[0] = i
				} else {
					bestNum[1] = num
					bestDen[1] = Syy
					bestPitch[1] = i
				}
			}
		}
		Syy += y[i+length]*y[i+length] - y[i]*y[i]
		if Syy < 1 {
			Syy = 1
		}
	}
}

func removeDoubling(x []float64, maxPeriod, minPeriod, N int, T0 *int, prevPeriod int, prevGain float64, scratch *encoderScratch) float64 {
	minPeriod0 := minPeriod
	maxPeriod >>= 1
	minPeriod >>= 1
	*T0 >>= 1
	prevPeriod >>= 1
	N >>= 1
	if maxPeriod <= 0 || N <= 0 {
		return 0
	}

	xBase := x
	if *T0 >= maxPeriod {
		*T0 = maxPeriod - 1
	}
	T0val := *T0
	x0 := xBase[maxPeriod:]
	xx, xy := prefilterDualInnerProd(x0, x0, xBase[maxPeriod-T0val:maxPeriod-T0val+N], N)

	yyLookup := ensureFloat64Slice(&scratch.prefilterYYLookup, maxPeriod+1)
	yy := xx
	yyLookup[0] = xx
	for i := 1; i <= maxPeriod; i++ {
		v1 := xBase[maxPeriod-i]
		v2 := xBase[maxPeriod+N-i]
		yy += v1*v1 - v2*v2
		if yy < 0 {
			yy = 0
		}
		yyLookup[i] = yy
	}

	yy = yyLookup[T0val]
	bestXY := xy
	bestYY := yy
	g := computePitchGain(xy, xx, yy)
	g0 := g
	T := T0val

	for k := 2; k <= 15; k++ {
		T1 := (2*T0val + k) / (2 * k)
		if T1 < minPeriod {
			break
		}
		var T1b int
		if k == 2 {
			if T1+T0val > maxPeriod {
				T1b = T0val
			} else {
				T1b = T0val + T1
			}
		} else {
			T1b = (2*secondCheck[k]*T0val + k) / (2 * k)
		}
		xy1, xy2 := prefilterDualInnerProd(x0, xBase[maxPeriod-T1:maxPeriod-T1+N], xBase[maxPeriod-T1b:maxPeriod-T1b+N], N)
		xy = 0.5 * (xy1 + xy2)
		yy = 0.5 * (yyLookup[T1] + yyLookup[T1b])
		g1 := computePitchGain(xy, xx, yy)
		cont := 0.0
		if util.Abs(T1-prevPeriod) <= 1 {
			cont = prevGain
		} else if util.Abs(T1-prevPeriod) <= 2 && 5*k*k < T0val {
			cont = 0.5 * prevGain
		}
		thresh := math.Max(0.3, 0.7*g0-cont)
		if T1 < 3*minPeriod {
			thresh = math.Max(0.4, 0.85*g0-cont)
		} else if T1 < 2*minPeriod {
			thresh = math.Max(0.5, 0.9*g0-cont)
		}
		if g1 > thresh {
			bestXY = xy
			bestYY = yy
			T = T1
			g = g1
		}
	}

	if bestXY < 0 {
		bestXY = 0
	}
	pg := g
	if bestYY > bestXY {
		pg = bestXY / (bestYY + 1)
		if pg > g {
			pg = g
		}
	}

	var xcorr [3]float64
	for k := 0; k < 3; k++ {
		lag := T + k - 1
		xcorr[k] = prefilterInnerProd(x0, xBase[maxPeriod-lag:maxPeriod-lag+N], N)
	}
	offset := 0
	if (xcorr[2] - xcorr[0]) > 0.7*(xcorr[1]-xcorr[0]) {
		offset = 1
	} else if (xcorr[0] - xcorr[2]) > 0.7*(xcorr[1]-xcorr[2]) {
		offset = -1
	}
	*T0 = 2*T + offset
	if *T0 < minPeriod0 {
		*T0 = minPeriod0
	}
	return pg
}

func computePitchGain(xy, xx, yy float64) float64 {
	if xy == 0 || xx == 0 || yy == 0 {
		return 0
	}
	return xy / math.Sqrt(1+xx*yy)
}

func celtFIR5(x []float64, num [5]float64) {
	mem0, mem1, mem2, mem3, mem4 := 0.0, 0.0, 0.0, 0.0, 0.0
	for i := 0; i < len(x); i++ {
		sum := x[i] + num[0]*mem0 + num[1]*mem1 + num[2]*mem2 + num[3]*mem3 + num[4]*mem4
		mem4 = mem3
		mem3 = mem2
		mem2 = mem1
		mem1 = mem0
		mem0 = x[i]
		x[i] = sum
	}
}

func lpcFromAutocorr(ac [5]float64) [4]float64 {
	var lpc [4]float64
	if ac[0] == 0 {
		return lpc
	}
	err := ac[0]
	for i := 0; i < 4; i++ {
		r := -ac[i+1]
		for j := 0; j < i; j++ {
			r -= lpc[j] * ac[i-j]
		}
		if err != 0 {
			r /= err
		} else {
			r = 0
		}
		lpc[i] = r
		for j := 0; j < i/2; j++ {
			tmp := lpc[j]
			lpc[j] += r * lpc[i-1-j]
			lpc[i-1-j] += r * tmp
		}
		if i%2 == 1 {
			lpc[i/2] += r * lpc[i/2]
		}
		err *= 1 - r*r
		if err <= 0 {
			break
		}
	}
	return lpc
}

var secondCheck = [16]int{0, 0, 3, 2, 3, 2, 5, 2, 3, 2, 3, 2, 5, 2, 3, 2}

// prefilterInnerProd uses float32 accumulation to match libopus float-path
// numerics in pitch/pre-filter analysis.
func prefilterInnerProd(x, y []float64, length int) float64 {
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

// prefilterDualInnerProd computes two float32-accumulated dot products.
func prefilterDualInnerProd(x, y1, y2 []float64, length int) (float64, float64) {
	if length <= 0 {
		return 0, 0
	}
	_ = x[length-1]
	_ = y1[length-1]
	_ = y2[length-1]
	sum1 := float32(0)
	sum2 := float32(0)
	for i := 0; i < length; i++ {
		xi := float32(x[i])
		sum1 += xi * float32(y1[i])
		sum2 += xi * float32(y2[i])
	}
	return float64(sum1), float64(sum2)
}

// prefilterPitchXcorr computes lagged correlations with float32 accumulation.
// Keeping the prefilter pitch path consistently float32 reduces tie-break drift
// versus libopus on tonal sweep inputs.
func prefilterPitchXcorr(x, y, xcorr []float64, length, maxPitch int) {
	if length <= 0 || maxPitch <= 0 {
		return
	}
	_ = x[length-1]
	_ = xcorr[maxPitch-1]
	_ = y[maxPitch+length-2]
	for i := 0; i < maxPitch; i++ {
		sum := float32(0)
		for j := 0; j < length; j++ {
			sum += float32(x[j]) * float32(y[i+j])
		}
		xcorr[i] = float64(sum)
	}
}
