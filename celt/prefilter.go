package celt

import (
	"math"

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

	if channels == 1 {
		hist := e.prefilterMem[:maxPeriod]
		preCh := pre[:perChanLen]
		copy(preCh[:maxPeriod], hist)
		copy(preCh[maxPeriod:maxPeriod+frameSize], preemph[:frameSize])
	} else {
		histL := e.prefilterMem[:maxPeriod]
		histR := e.prefilterMem[maxPeriod : 2*maxPeriod]
		preL := pre[:perChanLen]
		preR := pre[perChanLen : 2*perChanLen]
		copy(preL[:maxPeriod], histL)
		copy(preR[:maxPeriod], histR)
		DeinterleaveStereoInto(preemph[:frameSize*2], preL[maxPeriod:maxPeriod+frameSize], preR[maxPeriod:maxPeriod+frameSize])
	}
	// Keep prefilter inputs at float32 precision to match libopus celt_sig path.
	// Prefilter history is already float32-quantized when persisted in prefilterMem.
	// Only round the newly appended frame samples.
	for ch := 0; ch < channels; ch++ {
		frame := pre[ch*perChanLen+maxPeriod : ch*perChanLen+maxPeriod+frameSize]
		roundFloat64ToFloat32(frame)
	}
	needStateRound := false

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
		before[ch] = absSum(preSub)
		if offset > 0 {
			combFilterWithInputF32(outCh, preCh, maxPeriod, prevPeriod, prevPeriod, offset, -e.prefilterGain, -e.prefilterGain, prevTapset, prevTapset, nil, 0)
		}
		combFilterWithInputF32(outCh, preCh, maxPeriod+offset, prevPeriod, pitchIndex, frameSize-offset, -e.prefilterGain, -gain1, prevTapset, tapset, window, overlap)
		outSub := outCh[maxPeriod : maxPeriod+frameSize]
		after[ch] = absSum(outSub)
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
			combFilterWithInputF32(outCh, preCh, maxPeriod+offset, prevPeriod, pitchIndex, overlap, -e.prefilterGain, -0, prevTapset, tapset, window, overlap)
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

	if channels == 1 {
		preCh := pre[:perChanLen]
		outCh := out[:perChanLen]
		mem := e.prefilterMem[:maxPeriod]
		if frameSize > maxPeriod {
			copy(mem, preCh[frameSize:frameSize+maxPeriod])
		} else {
			copy(mem, mem[frameSize:])
			copy(mem[maxPeriod-frameSize:], preCh[maxPeriod:maxPeriod+frameSize])
		}
		if needStateRound {
			roundFloat64ToFloat32(mem)
		}
		outSub2 := outCh[maxPeriod : maxPeriod+frameSize]
		copy(preemph[:frameSize], outSub2)
		if overlap > 0 && len(e.overlapBuffer) >= overlap && frameSize >= overlap {
			hist := e.overlapBuffer[:overlap]
			copy(hist, outSub2[frameSize-overlap:])
			if needStateRound {
				roundFloat64ToFloat32(hist)
			}
		}
	} else {
		preL := pre[:perChanLen]
		preR := pre[perChanLen : 2*perChanLen]
		outL := out[maxPeriod : maxPeriod+frameSize]
		outR := out[perChanLen+maxPeriod : perChanLen+maxPeriod+frameSize]
		memL := e.prefilterMem[:maxPeriod]
		memR := e.prefilterMem[maxPeriod : 2*maxPeriod]
		if frameSize > maxPeriod {
			copy(memL, preL[frameSize:frameSize+maxPeriod])
			copy(memR, preR[frameSize:frameSize+maxPeriod])
		} else {
			copy(memL, memL[frameSize:])
			copy(memL[maxPeriod-frameSize:], preL[maxPeriod:maxPeriod+frameSize])
			copy(memR, memR[frameSize:])
			copy(memR[maxPeriod-frameSize:], preR[maxPeriod:maxPeriod+frameSize])
		}
		if needStateRound {
			roundFloat64ToFloat32(memL)
			roundFloat64ToFloat32(memR)
		}
		InterleaveStereoInto(outL, outR, preemph[:frameSize*2])
		if overlap > 0 && len(e.overlapBuffer) >= channels*overlap && frameSize >= overlap {
			histL := e.overlapBuffer[:overlap]
			histR := e.overlapBuffer[overlap : 2*overlap]
			copy(histL, outL[frameSize-overlap:])
			copy(histR, outR[frameSize-overlap:])
			if needStateRound {
				roundFloat64ToFloat32(histL)
				roundFloat64ToFloat32(histR)
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

func pitchDownsample(x []float64, xLP []float64, length, channels, factor int) {
	if length <= 0 || factor <= 0 || len(xLP) < length {
		return
	}
	const (
		firQuarter = float32(0.25)
		firHalf    = float32(0.5)
	)
	handled := false
	if factor == 2 {
		if channels == 1 {
			idx := 2
			for i := 1; i < length; i++ {
				v := firQuarter*float32(x[idx-1]) + firQuarter*float32(x[idx+1]) + firHalf*float32(x[idx])
				xLP[i] = float64(v)
				idx += 2
			}
			xLP[0] = float64(firQuarter*float32(x[1]) + firHalf*float32(x[0]))
		} else if channels == 2 {
			chStride := len(x) / 2
			x0 := x[:chStride]
			x1 := x[chStride:]
			idx := 2
			for i := 1; i < length; i++ {
				v0 := firQuarter*float32(x0[idx-1]) + firQuarter*float32(x0[idx+1]) + firHalf*float32(x0[idx])
				v1 := firQuarter*float32(x1[idx-1]) + firQuarter*float32(x1[idx+1]) + firHalf*float32(x1[idx])
				xLP[i] = float64(v0 + v1)
				idx += 2
			}
			xLP[0] = float64(firQuarter*float32(x0[1]) + firHalf*float32(x0[0]) +
				firQuarter*float32(x1[1]) + firHalf*float32(x1[0]))
		}
		handled = true
	}
	if !handled {
		offset := factor / 2
		if offset < 1 {
			offset = 1
		}
		for i := 1; i < length; i++ {
			idx := factor * i
			v := firQuarter*float32(x[idx-offset]) +
				firQuarter*float32(x[idx+offset]) +
				firHalf*float32(x[idx])
			xLP[i] = float64(v)
		}
		xLP[0] = float64(firQuarter*float32(x[offset]) + firHalf*float32(x[0]))
		if channels == 2 {
			chStride := len(x) / 2
			x1 := x[chStride:]
			for i := 1; i < length; i++ {
				idx := factor * i
				v := firQuarter*float32(x1[idx-offset]) +
					firQuarter*float32(x1[idx+offset]) +
					firHalf*float32(x1[idx])
				xLP[i] = float64(float32(xLP[i]) + v)
			}
			xLP[0] = float64(float32(xLP[0]) + firQuarter*float32(x1[offset]) + firHalf*float32(x1[0]))
		}
	}

	// Match libopus _celt_autocorr() order for lag=4, overlap=0.
	// This preserves float-path accumulation behavior used by tone/pitch analysis.
	var ac [5]float64
	lp := xLP[:length]
	pitchAutocorr5(lp, length, &ac)

	ac[0] = float64(float32(ac[0]) * float32(1.0001))
	for i := 1; i <= 4; i++ {
		f := float32(0.008) * float32(i)
		ac[i] = float64(float32(ac[i]) - float32(ac[i])*f*f)
	}

	lpc := lpcFromAutocorr(ac)
	tmp := float32(1.0)
	for i := 0; i < 4; i++ {
		tmp *= float32(0.9)
		lpc[i] = float64(float32(lpc[i]) * tmp)
	}
	c1 := float32(0.8)
	lpc2 := [5]float64{
		float64(float32(lpc[0]) + float32(0.8)),
		float64(float32(lpc[1]) + c1*float32(lpc[0])),
		float64(float32(lpc[2]) + c1*float32(lpc[1])),
		float64(float32(lpc[3]) + c1*float32(lpc[2])),
		float64(c1 * float32(lpc[3])),
	}
	celtFIR5(xLP, lpc2)
}

func pitchSearch(xLP []float64, y []float64, length, maxPitch int, scratch *encoderScratch) int {
	if length <= 0 || maxPitch <= 0 {
		return 0
	}
	lag := length + maxPitch
	quarterLen := length >> 2
	quarterLag := lag >> 2
	quarterPitch := maxPitch >> 2
	halfLen := length >> 1
	halfPitch := maxPitch >> 1

	xLP4 := ensureFloat64Slice(&scratch.prefilterXLP4, quarterLen)
	yLP4 := ensureFloat64Slice(&scratch.prefilterYLP4, quarterLag)
	xcorr := ensureFloat64Slice(&scratch.prefilterXcorr, halfPitch)

	for j, idx := 0, 0; j < quarterLen; j, idx = j+1, idx+2 {
		xLP4[j] = xLP[idx]
	}
	for j, idx := 0, 0; j < quarterLag; j, idx = j+1, idx+2 {
		yLP4[j] = y[idx]
	}

	prefilterPitchXcorrFast(xLP4, yLP4, xcorr, quarterLen, quarterPitch)
	bestPitch := [2]int{0, 0}
	findBestPitch(xcorr, yLP4, quarterLen, quarterPitch, &bestPitch)

	ranges := pitchSearchFineRanges(bestPitch, halfPitch)
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
		n := r.hi - r.lo + 1
		prefilterPitchXcorrFast(xLP, y[r.lo:], xcorr[r.lo:], halfLen, n)
		for ; i <= r.hi; i++ {
			if xcorr[i] < -1 {
				xcorr[i] = -1
			}
			if xv := xcorr[i]; xv > 0 {
				xc := float32(xv)
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

func findBestPitch(xcorr []float64, y []float64, length, maxPitch int, bestPitch *[2]int) {
	Syy := float32(1)
	bestNum := [2]float32{-1, -1}
	bestDen := [2]float32{0, 0}
	bestPitch[0] = 0
	bestPitch[1] = 1
	_ = y[length+maxPitch-1] // BCE hint
	_ = xcorr[maxPitch-1]    // BCE hint
	for j := 0; j < length; j++ {
		yj := float32(y[j])
		Syy += yj * yj
	}
	const xcorrScale = float32(1e-12)
	for i := 0; i < maxPitch; i++ {
		if xv := xcorr[i]; xv > 0 {
			xc := float32(xv)
			xcorr16 := xc * xcorrScale
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
		yi := float32(y[i])
		yil := float32(y[i+length])
		Syy += yil*yil - yi*yi
		if Syy < 1 {
			Syy = 1
		}
	}
}

type pitchSearchRange struct {
	lo int
	hi int
}

const pitchSearchXcorrScale = float32(1e-12)

func normalizePitchSearchRanges(a, b pitchSearchRange) [2]pitchSearchRange {
	if a.hi < a.lo {
		a = pitchSearchRange{lo: 1, hi: 0}
	}
	if b.hi < b.lo {
		b = pitchSearchRange{lo: 1, hi: 0}
	}
	if a.hi < a.lo {
		return [2]pitchSearchRange{b, {lo: 1, hi: 0}}
	}
	if b.hi < b.lo {
		return [2]pitchSearchRange{a, {lo: 1, hi: 0}}
	}
	if b.lo < a.lo {
		a, b = b, a
	}
	if b.lo <= a.hi+1 {
		if b.hi > a.hi {
			a.hi = b.hi
		}
		return [2]pitchSearchRange{a, {lo: 1, hi: 0}}
	}
	return [2]pitchSearchRange{a, b}
}

func pitchSearchFineRanges(bestPitch [2]int, halfPitch int) [2]pitchSearchRange {
	p0 := 2 * bestPitch[0]
	p1 := 2 * bestPitch[1]
	l0 := max(0, p0-2)
	h0 := min(halfPitch-1, p0+2)
	l1 := max(0, p1-2)
	h1 := min(halfPitch-1, p1+2)
	return normalizePitchSearchRanges(
		pitchSearchRange{lo: l0, hi: h0},
		pitchSearchRange{lo: l1, hi: h1},
	)
}

func findBestPitchInRanges(xcorr []float64, y []float64, length int, ranges [2]pitchSearchRange, bestPitch *[2]int) {
	Syy := float32(1)
	bestNum := [2]float32{-1, -1}
	bestDen := [2]float32{0, 0}
	bestPitch[0] = 0
	bestPitch[1] = 1
	for j := 0; j < length; j++ {
		yj := float32(y[j])
		Syy += yj * yj
	}
	i := 0
	for _, r := range ranges {
		if r.hi < r.lo {
			continue
		}
		for ; i < r.lo; i++ {
			yi := float32(y[i])
			yil := float32(y[i+length])
			Syy += yil*yil - yi*yi
			if Syy < 1 {
				Syy = 1
			}
		}
		for ; i <= r.hi; i++ {
			if xv := xcorr[i]; xv > 0 {
				xc := float32(xv)
				xcorr16 := xc * pitchSearchXcorrScale
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
			yi := float32(y[i])
			yil := float32(y[i+length])
			Syy += yil*yil - yi*yi
			if Syy < 1 {
				Syy = 1
			}
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
	xx64, xy64 := prefilterDualInnerProd(x0, x0, xBase[maxPeriod-T0val:maxPeriod-T0val+N], N)
	xx := float32(xx64)
	xy := float32(xy64)

	yyLookup := ensureFloat32Slice(&scratch.prefilterYYLookup, maxPeriod+1)
	yy := xx
	yyLookup[0] = yy
	for i := 1; i <= maxPeriod; i++ {
		v1 := float32(xBase[maxPeriod-i])
		v2 := float32(xBase[maxPeriod+N-i])
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
		xy = float32(0.5) * (float32(xy1) + float32(xy2))
		yy = float32(0.5) * (yyLookup[T1] + yyLookup[T1b])
		g1 := computePitchGain(xy, xx, yy)
		cont := float32(0)
		if util.Abs(T1-prevPeriod) <= 1 {
			cont = float32(prevGain)
		} else if util.Abs(T1-prevPeriod) <= 2 && 5*k*k < T0val {
			cont = float32(0.5) * float32(prevGain)
		}
		thresh := maxFloat32(float32(0.3), float32(0.7)*g0-cont)
		if T1 < 3*minPeriod {
			thresh = maxFloat32(float32(0.4), float32(0.85)*g0-cont)
		} else if T1 < 2*minPeriod {
			thresh = maxFloat32(float32(0.5), float32(0.9)*g0-cont)
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
		pg = bestXY / (bestYY + float32(1))
		if pg > g {
			pg = g
		}
	}

	prev, mid, next := tripleInnerProd(
		x0,
		xBase[maxPeriod-(T-1):maxPeriod-(T-1)+N],
		xBase[maxPeriod-T:maxPeriod-T+N],
		xBase[maxPeriod-(T+1):maxPeriod-(T+1)+N],
		N,
	)
	xcorr := [3]float32{float32(prev), float32(mid), float32(next)}
	offset := 0
	if (xcorr[2] - xcorr[0]) > float32(0.7)*(xcorr[1]-xcorr[0]) {
		offset = 1
	} else if (xcorr[0] - xcorr[2]) > float32(0.7)*(xcorr[1]-xcorr[2]) {
		offset = -1
	}
	*T0 = 2*T + offset
	if *T0 < minPeriod0 {
		*T0 = minPeriod0
	}
	return float64(pg)
}

func maxFloat32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

func computePitchGain(xy, xx, yy float32) float32 {
	if xy == 0 || xx == 0 || yy == 0 {
		return 0
	}
	return xy / float32(math.Sqrt(float64(1+xx*yy)))
}

func celtFIR5(x []float64, num [5]float64) {
	n0 := float32(num[0])
	n1 := float32(num[1])
	n2 := float32(num[2])
	n3 := float32(num[3])
	n4 := float32(num[4])
	mem0 := float32(0)
	mem1 := float32(0)
	mem2 := float32(0)
	mem3 := float32(0)
	mem4 := float32(0)
	i := 0
	for ; i+1 < len(x); i += 2 {
		x0 := float32(x[i])
		sum0 := x0 + n0*mem0 + n1*mem1 + n2*mem2 + n3*mem3 + n4*mem4
		x1 := float32(x[i+1])
		sum1 := x1 + n0*x0 + n1*mem0 + n2*mem1 + n3*mem2 + n4*mem3
		x[i] = float64(sum0)
		x[i+1] = float64(sum1)
		mem4 = mem2
		mem3 = mem1
		mem2 = mem0
		mem1 = x0
		mem0 = x1
	}
	for ; i < len(x); i++ {
		xi := float32(x[i])
		sum := xi + n0*mem0 + n1*mem1 + n2*mem2 + n3*mem3 + n4*mem4
		mem4 = mem3
		mem3 = mem2
		mem2 = mem1
		mem1 = mem0
		mem0 = xi
		x[i] = float64(sum)
	}
}

func lpcFromAutocorr(ac [5]float64) [4]float64 {
	var lpc [4]float64
	if ac[0] <= 1e-10 {
		return lpc
	}
	var lpc32 [4]float32
	ac0 := float32(ac[0])
	err := ac0
	for i := 0; i < 4; i++ {
		rr := float32(0)
		for j := 0; j < i; j++ {
			rr += lpc32[j] * float32(ac[i-j])
		}
		rr += float32(ac[i+1])
		r := -rr / err
		lpc32[i] = r
		for j := 0; j < (i+1)>>1; j++ {
			tmp1 := lpc32[j]
			tmp2 := lpc32[i-1-j]
			lpc32[j] = tmp1 + r*tmp2
			lpc32[i-1-j] = tmp2 + r*tmp1
		}
		err = err - r*r*err
		if err <= float32(0.001)*ac0 {
			break
		}
	}
	for i := range lpc {
		lpc[i] = float64(lpc32[i])
	}
	return lpc
}

// prefilterInnerProd and prefilterDualInnerProd are implemented in:
//   prefilter_innerprod_asm.go + prefilter_innerprod_{arm64,amd64}.s  (SIMD path)
//   prefilter_innerprod_default.go                                     (Go fallback)

var secondCheck = [16]int{0, 0, 3, 2, 3, 2, 5, 2, 3, 2, 3, 2, 5, 2, 3, 2}
