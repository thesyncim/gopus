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

	if tapset < 0 {
		tapset = 0
	}
	if tapset >= len(combFilterGains) {
		tapset = len(combFilterGains) - 1
	}

	maxPeriod := combFilterMaxPeriod
	minPeriod := combFilterMinPeriod
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

	pitchIndex := minPeriod
	gain1 := 0.0
	qg := 0
	pfOn := false

	if enabled && toneishness > 0.99 {
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
		pitchIndex = pitchSearch(pitchBuf[maxPeriod>>1:], pitchBuf, frameSize, maxPitch, &e.scratch)
		pitchIndex = maxPeriod - pitchIndex
		gain1 = removeDoubling(pitchBuf, maxPeriod, minPeriod, frameSize, &pitchIndex, e.prefilterPeriod, e.prefilterGain, &e.scratch)
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
		for i := 0; i < frameSize; i++ {
			before[ch] += math.Abs(preCh[maxPeriod+i]) * (1.0 / 4096.0)
		}
		if offset > 0 {
			combFilterWithInput(outCh, preCh, maxPeriod, e.prefilterPeriod, e.prefilterPeriod, offset, -e.prefilterGain, -e.prefilterGain, e.prefilterTapset, e.prefilterTapset, nil, 0)
		}
		combFilterWithInput(outCh, preCh, maxPeriod+offset, e.prefilterPeriod, pitchIndex, frameSize-offset, -e.prefilterGain, -gain1, e.prefilterTapset, tapset, window, overlap)
		for i := 0; i < frameSize; i++ {
			after[ch] += math.Abs(outCh[maxPeriod+i]) * (1.0 / 4096.0)
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
			combFilterWithInput(outCh, preCh, maxPeriod+offset, e.prefilterPeriod, pitchIndex, overlap, -e.prefilterGain, -0, e.prefilterTapset, tapset, window, overlap)
		}
		gain1 = 0
		pfOn = false
		qg = 0
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
		for i := 0; i < frameSize; i++ {
			preemph[i*channels+ch] = outCh[maxPeriod+i]
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
	return result
}

// estimateMaxPitchRatio approximates libopus analysis->max_pitch_ratio by
// comparing low-frequency and high-frequency spectral energy after a 2x
// downsampling step (48 kHz -> 24 kHz) with a split at bin 64 (~3.2 kHz).
// It uses the same down2 all-pass structure as libopus analysis and adds the
// high-pass residual energy to the "above max pitch" side.
func estimateMaxPitchRatio(pcm []float64, channels int, scratch []float64) float64 {
	if channels <= 0 || len(pcm) < channels {
		return 1
	}
	monoLen := len(pcm) / channels
	n := monoLen / 2
	if n < 8 {
		return 1
	}

	var down []float64
	if len(scratch) >= n {
		down = scratch[:n]
	} else {
		down = make([]float64, n)
	}

	// Downmix and downsample by 2 (matching the 24 kHz analysis bandwidth).
	// This mirrors silk_resampler_down2_hp() from libopus analysis.c.
	var s0, s1, s2 float64
	var hpEner float64
	for i := 0; i < n; i++ {
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

		hpEner += outHP * outHP
		down[i] = 0.5 * out
	}

	// Apply a light Hann window to reduce spectral leakage.
	if n > 1 {
		inv := 1.0 / float64(n-1)
		for i := 0; i < n; i++ {
			w := 0.5 - 0.5*math.Cos(2.0*math.Pi*float64(i)*inv)
			down[i] *= w
		}
	}

	half := n / 2
	splitBin := 64
	if splitBin > half {
		splitBin = half
	}

	var below, above float64
	for k := 0; k < half; k++ {
		ang := -2.0 * math.Pi * float64(k) / float64(n)
		cosStep := math.Cos(ang)
		sinStep := math.Sin(ang)
		cosCurr := 1.0
		sinCurr := 0.0
		var re, im float64

		for t := 0; t < n; t++ {
			v := down[t]
			re += v * cosCurr
			im += v * sinCurr

			nextCos := cosCurr*cosStep - sinCurr*sinStep
			sinCurr = cosCurr*sinStep + sinCurr*cosStep
			cosCurr = nextCos
		}

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

	var ac [5]float64
	for lag := 0; lag <= 4; lag++ {
		sum := 0.0
		for i := 0; i < length-lag; i++ {
			sum += xLP[i] * xLP[i+lag]
		}
		ac[lag] = sum
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

	celtPitchXcorr(xLP4, yLP4, xcorr, length>>2, maxPitch>>2)
	bestPitch := [2]int{0, 0}
	findBestPitch(xcorr, yLP4, length>>2, maxPitch>>2, &bestPitch)

	for i := 0; i < maxPitch>>1; i++ {
		xcorr[i] = 0
		if util.Abs(i-2*bestPitch[0]) > 2 && util.Abs(i-2*bestPitch[1]) > 2 {
			continue
		}
		sum := celtInnerProd(xLP, y[i:], length>>1)
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

func celtPitchXcorr(x []float64, y []float64, xcorr []float64, length, maxPitch int) {
	if length <= 0 || maxPitch <= 0 {
		return
	}
	for i := 0; i < maxPitch; i++ {
		sum := 0.0
		for j := 0; j < length; j++ {
			sum += x[j] * y[i+j]
		}
		xcorr[i] = sum
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
	xx, xy := dualInnerProd(x0, x0, xBase[maxPeriod-T0val:maxPeriod-T0val+N], N)

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
		xy1, xy2 := dualInnerProd(x0, xBase[maxPeriod-T1:maxPeriod-T1+N], xBase[maxPeriod-T1b:maxPeriod-T1b+N], N)
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
		xcorr[k] = celtInnerProd(x0, xBase[maxPeriod-lag:maxPeriod-lag+N], N)
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

func dualInnerProd(x, y1, y2 []float64, length int) (float64, float64) {
	sum1 := 0.0
	sum2 := 0.0
	for i := 0; i < length; i++ {
		sum1 += x[i] * y1[i]
		sum2 += x[i] * y2[i]
	}
	return sum1, sum2
}

func celtInnerProd(x, y []float64, length int) float64 {
	sum := 0.0
	for i := 0; i < length; i++ {
		sum += x[i] * y[i]
	}
	return sum
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
