package celt

import (
	"math"
)

const (
	combFilterMinPeriod = 15
	combFilterMaxPeriod = 1024
	combFilterHistory   = combFilterMaxPeriod + 2
	// Matches libopus DEC_PITCH_BUF_SIZE used by celt_decode_lost().
	plcDecodeBufferSize = 2048
)

var combFilterGains = [3][3]float64{
	{0.3066406250, 0.2170410156, 0.1296386719},
	{0.4638671875, 0.2680664062, 0.0000000000},
	{0.7998046875, 0.1000976562, 0.0000000000},
}

func fma32(a, b, c float32) float32 {
	return float32(math.FMA(float64(a), float64(b), float64(c)))
}

func (d *Decoder) resetPostfilterState() {
	d.postfilterPeriod = 0
	d.postfilterGain = 0
	d.postfilterTapset = 0
	d.postfilterPeriodOld = 0
	d.postfilterGainOld = 0
	d.postfilterTapsetOld = 0

	for i := range d.postfilterMem {
		d.postfilterMem[i] = 0
	}
}

func sanitizePostfilterParams(t0, t1 int, g0, g1 float64, tap0, tap1 int) (int, int, int, int) {
	if t0 < combFilterMinPeriod || t0 > combFilterMaxPeriod {
		t0 = t1
	}
	if t1 < combFilterMinPeriod || t1 > combFilterMaxPeriod {
		t1 = t0
	}
	if t0 < combFilterMinPeriod {
		t0 = combFilterMinPeriod
	}
	if t1 < combFilterMinPeriod {
		t1 = combFilterMinPeriod
	}

	if tap0 < 0 || tap0 >= len(combFilterGains) {
		tap0 = tap1
	}
	if tap1 < 0 || tap1 >= len(combFilterGains) {
		tap1 = tap0
	}
	if tap0 < 0 || tap0 >= len(combFilterGains) {
		tap0 = 0
	}
	if tap1 < 0 || tap1 >= len(combFilterGains) {
		tap1 = 0
	}

	if g0 == 0 {
		t0 = t1
	}
	if g1 == 0 {
		t1 = t0
	}

	return t0, t1, tap0, tap1
}

func (d *Decoder) updatePostfilterHistory(samples []float64, frameSize int, history int) {
	if frameSize <= 0 || history <= 0 {
		return
	}
	if d.channels <= 1 {
		hist := d.postfilterMem[:history]
		if frameSize >= history {
			copy(hist, samples[frameSize-history:frameSize])
			return
		}
		copy(hist, hist[frameSize:])
		copy(hist[history-frameSize:], samples[:frameSize])
		return
	}
	if d.channels == 2 {
		histL := d.postfilterMem[:history]
		histR := d.postfilterMem[history : 2*history]
		if frameSize >= history {
			src := (frameSize - history) * 2
			DeinterleaveStereoInto(samples[src:src+history*2], histL, histR)
			return
		}
		copy(histL, histL[frameSize:])
		copy(histR, histR[frameSize:])
		dst := history - frameSize
		DeinterleaveStereoInto(samples[:frameSize*2], histL[dst:], histR[dst:])
		return
	}

	channels := d.channels
	for ch := 0; ch < channels; ch++ {
		hist := d.postfilterMem[ch*history : (ch+1)*history]
		if frameSize >= history {
			src := (frameSize-history)*channels + ch
			for i := 0; i < history; i++ {
				hist[i] = samples[src]
				src += channels
			}
			continue
		}
		copy(hist, hist[frameSize:])
		src := ch
		dst := history - frameSize
		for i := 0; i < frameSize; i++ {
			hist[dst+i] = samples[src]
			src += channels
		}
	}
}

func (d *Decoder) updatePLCDecodeHistory(samples []float64, frameSize int, history int) {
	if frameSize <= 0 || history <= 0 {
		return
	}
	if len(d.plcDecodeMem) != history*d.channels {
		d.plcDecodeMem = make([]float64, history*d.channels)
	}
	if d.channels <= 1 {
		hist := d.plcDecodeMem[:history]
		if frameSize >= history {
			copy(hist, samples[frameSize-history:frameSize])
			return
		}
		copy(hist, hist[frameSize:])
		copy(hist[history-frameSize:], samples[:frameSize])
		return
	}
	if d.channels == 2 {
		histL := d.plcDecodeMem[:history]
		histR := d.plcDecodeMem[history : 2*history]
		if frameSize >= history {
			src := (frameSize - history) * 2
			DeinterleaveStereoInto(samples[src:src+history*2], histL, histR)
			return
		}
		copy(histL, histL[frameSize:])
		copy(histR, histR[frameSize:])
		dst := history - frameSize
		DeinterleaveStereoInto(samples[:frameSize*2], histL[dst:], histR[dst:])
		return
	}

	channels := d.channels
	for ch := 0; ch < channels; ch++ {
		hist := d.plcDecodeMem[ch*history : (ch+1)*history]
		if frameSize >= history {
			src := (frameSize-history)*channels + ch
			for i := 0; i < history; i++ {
				hist[i] = samples[src]
				src += channels
			}
			continue
		}
		copy(hist, hist[frameSize:])
		src := ch
		dst := history - frameSize
		for i := 0; i < frameSize; i++ {
			hist[dst+i] = samples[src]
			src += channels
		}
	}
}

func updatePlanarHistory(hist, samples []float64, frameSize, history int) {
	if frameSize >= history {
		copy(hist, samples[frameSize-history:frameSize])
		return
	}
	copy(hist, hist[frameSize:])
	copy(hist[history-frameSize:], samples[:frameSize])
}

func updateMonoHistoryFromFloat32(hist []float64, samples []float32, frameSize, history int) {
	if frameSize <= 0 || history <= 0 {
		return
	}
	if frameSize >= history {
		copyFloat32ToFloat64(hist[:history], samples[frameSize-history:frameSize])
		return
	}
	copy(hist, hist[frameSize:])
	copyFloat32ToFloat64(hist[history-frameSize:history], samples[:frameSize])
}

func (d *Decoder) updatePostfilterHistoryStereoPlanar(left, right []float64, frameSize int, history int) {
	if frameSize <= 0 || history <= 0 {
		return
	}
	histL := d.postfilterMem[:history]
	histR := d.postfilterMem[history : 2*history]
	updatePlanarHistory(histL, left, frameSize, history)
	updatePlanarHistory(histR, right, frameSize, history)
}

func (d *Decoder) updatePLCDecodeHistoryStereoPlanar(left, right []float64, frameSize int, history int) {
	if frameSize <= 0 || history <= 0 {
		return
	}
	if len(d.plcDecodeMem) != history*d.channels {
		d.plcDecodeMem = make([]float64, history*d.channels)
	}
	histL := d.plcDecodeMem[:history]
	histR := d.plcDecodeMem[history : 2*history]
	updatePlanarHistory(histL, left, frameSize, history)
	updatePlanarHistory(histR, right, frameSize, history)
}

func (d *Decoder) updatePostfilterHistoryMonoFromFloat32(samples []float32, frameSize int, history int) {
	if frameSize <= 0 || history <= 0 {
		return
	}
	updateMonoHistoryFromFloat32(d.postfilterMem[:history], samples, frameSize, history)
}

func (d *Decoder) updatePLCDecodeHistoryMonoFromFloat32(samples []float32, frameSize int, history int) {
	if frameSize <= 0 || history <= 0 {
		return
	}
	if len(d.plcDecodeMem) != history*d.channels {
		d.plcDecodeMem = make([]float64, history*d.channels)
	}
	updateMonoHistoryFromFloat32(d.plcDecodeMem[:history], samples, frameSize, history)
}

func (d *Decoder) commitPostfilterStateNoGain(lm int, newPeriod int, newGain float64, newTapset int) {
	d.postfilterPeriodOld = d.postfilterPeriod
	d.postfilterGainOld = d.postfilterGain
	d.postfilterTapsetOld = d.postfilterTapset
	d.postfilterPeriod = newPeriod
	d.postfilterGain = newGain
	d.postfilterTapset = newTapset
	if lm != 0 {
		d.postfilterPeriodOld = d.postfilterPeriod
		d.postfilterGainOld = d.postfilterGain
		d.postfilterTapsetOld = d.postfilterTapset
	}
}

func (d *Decoder) applyPostfilterNoGainMonoFromFloat32(samples []float32, frameSize, lm int, newPeriod int, newGain float64, newTapset int) {
	if frameSize <= 0 {
		return
	}
	history := combFilterHistory
	if len(d.postfilterMem) != history*d.channels {
		d.postfilterMem = make([]float64, history*d.channels)
	}
	d.updatePostfilterHistoryMonoFromFloat32(samples, frameSize, history)
	d.updatePLCDecodeHistoryMonoFromFloat32(samples, frameSize, plcDecodeBufferSize)
	d.commitPostfilterStateNoGain(lm, newPeriod, newGain, newTapset)
}

func applyPostfilterChannelInPlace(samples, hist, scratch []float64, frameSize, history, lm int, t0, t1, t1b, t2 int, g0, g1, g2 float64, tap0, tap1, tap1b, tap2 int, window, windowSq []float64) {
	buf := scratch[:history+frameSize]
	copy(buf, hist)
	copy(buf[history:], samples[:frameSize])

	shortMdctSize := frameSize >> uint(lm)
	if shortMdctSize <= 0 || shortMdctSize > frameSize {
		shortMdctSize = frameSize
	}

	combFilterWithSquare(buf, history, t0, t1, shortMdctSize, g0, g1, tap0, tap1, window, windowSq, Overlap)
	if lm != 0 && shortMdctSize < frameSize {
		combFilterWithSquare(buf, history+shortMdctSize, t1b, t2, frameSize-shortMdctSize, g1, g2, tap1b, tap2, window, windowSq, Overlap)
	}

	copy(samples[:frameSize], buf[history:history+frameSize])
	copy(hist, buf[frameSize:frameSize+history])
}

func (d *Decoder) applyPostfilterStereoPlanar(left, right []float64, frameSize, lm int, newPeriod int, newGain float64, newTapset int) {
	if len(left) < frameSize || len(right) < frameSize || frameSize <= 0 {
		return
	}

	history := combFilterHistory
	if len(d.postfilterMem) != history*2 {
		d.postfilterMem = make([]float64, history*2)
	}
	if d.postfilterGainOld == 0 && d.postfilterGain == 0 && newGain == 0 {
		d.updatePostfilterHistoryStereoPlanar(left, right, frameSize, history)
		d.updatePLCDecodeHistoryStereoPlanar(left, right, frameSize, plcDecodeBufferSize)
		d.commitPostfilterStateNoGain(lm, newPeriod, newGain, newTapset)
		return
	}

	needed := history + frameSize
	if cap(d.postfilterScratch) < needed {
		d.postfilterScratch = make([]float64, needed)
	}

	t0 := d.postfilterPeriodOld
	t1 := d.postfilterPeriod
	g0 := d.postfilterGainOld
	g1 := d.postfilterGain
	tap0 := d.postfilterTapsetOld
	tap1 := d.postfilterTapset
	t2 := newPeriod
	g2 := newGain
	tap2 := newTapset

	t0, t1, tap0, tap1 = sanitizePostfilterParams(t0, t1, g0, g1, tap0, tap1)
	t1b, t2, tap1b, tap2 := sanitizePostfilterParams(t1, t2, g1, g2, tap1, tap2)

	window := GetWindowBuffer(Overlap)
	windowSq := GetWindowSquareBuffer(Overlap)
	histL := d.postfilterMem[:history]
	histR := d.postfilterMem[history : 2*history]
	applyPostfilterChannelInPlace(left, histL, d.postfilterScratch, frameSize, history, lm, t0, t1, t1b, t2, g0, g1, g2, tap0, tap1, tap1b, tap2, window, windowSq)
	applyPostfilterChannelInPlace(right, histR, d.postfilterScratch, frameSize, history, lm, t0, t1, t1b, t2, g0, g1, g2, tap0, tap1, tap1b, tap2, window, windowSq)

	d.updatePLCDecodeHistoryStereoPlanar(left, right, frameSize, plcDecodeBufferSize)
	d.postfilterPeriodOld = d.postfilterPeriod
	d.postfilterGainOld = d.postfilterGain
	d.postfilterTapsetOld = d.postfilterTapset
	d.postfilterPeriod = newPeriod
	d.postfilterGain = newGain
	d.postfilterTapset = newTapset
	if lm != 0 {
		d.postfilterPeriodOld = d.postfilterPeriod
		d.postfilterGainOld = d.postfilterGain
		d.postfilterTapsetOld = d.postfilterTapset
	}
}

func (d *Decoder) applyPostfilter(samples []float64, frameSize, lm int, newPeriod int, newGain float64, newTapset int) {
	if len(samples) == 0 || frameSize <= 0 {
		return
	}
	if d.channels <= 0 {
		return
	}

	if lm < 0 {
		lm = 0
	}

	history := combFilterHistory
	if len(d.postfilterMem) != history*d.channels {
		d.postfilterMem = make([]float64, history*d.channels)
	}
	if d.postfilterGainOld == 0 && d.postfilterGain == 0 && newGain == 0 {
		d.updatePostfilterHistory(samples, frameSize, history)
		d.updatePLCDecodeHistory(samples, frameSize, plcDecodeBufferSize)
		d.commitPostfilterStateNoGain(lm, newPeriod, newGain, newTapset)
		return
	}
	needed := history + frameSize
	if needed < 0 {
		needed = 0
	}
	if cap(d.postfilterScratch) < needed {
		d.postfilterScratch = make([]float64, needed)
	}

	t0 := d.postfilterPeriodOld
	t1 := d.postfilterPeriod
	g0 := d.postfilterGainOld
	g1 := d.postfilterGain
	tap0 := d.postfilterTapsetOld
	tap1 := d.postfilterTapset
	t2 := newPeriod
	g2 := newGain
	tap2 := newTapset

	t0, t1, tap0, tap1 = sanitizePostfilterParams(t0, t1, g0, g1, tap0, tap1)
	t1b, t2, tap1b, tap2 := sanitizePostfilterParams(t1, t2, g1, g2, tap1, tap2)

	shortMdctSize := frameSize >> uint(lm)
	if shortMdctSize <= 0 || shortMdctSize > frameSize {
		shortMdctSize = frameSize
	}

	window := GetWindowBuffer(Overlap)
	windowSq := GetWindowSquareBuffer(Overlap)

	if d.channels == 1 {
		hist := d.postfilterMem[:history]
		buf := d.postfilterScratch[:needed]
		copy(buf, hist)
		copy(buf[history:], samples[:frameSize])

		combFilterWithSquare(buf, history, t0, t1, shortMdctSize, g0, g1, tap0, tap1, window, windowSq, Overlap)
		if lm != 0 && shortMdctSize < frameSize {
			combFilterWithSquare(buf, history+shortMdctSize, t1b, t2, frameSize-shortMdctSize, g1, g2, tap1b, tap2, window, windowSq, Overlap)
		}

		copy(samples[:frameSize], buf[history:history+frameSize])
		copy(hist, buf[frameSize:])
	} else {
		channels := d.channels
		for ch := 0; ch < channels; ch++ {
			hist := d.postfilterMem[ch*history : (ch+1)*history]
			buf := d.postfilterScratch[:needed]
			copy(buf, hist)

			j := ch
			i := 0
			for ; i+3 < frameSize; i += 4 {
				buf[history+i] = samples[j]
				j += channels
				buf[history+i+1] = samples[j]
				j += channels
				buf[history+i+2] = samples[j]
				j += channels
				buf[history+i+3] = samples[j]
				j += channels
			}
			for ; i < frameSize; i++ {
				buf[history+i] = samples[j]
				j += channels
			}

			combFilterWithSquare(buf, history, t0, t1, shortMdctSize, g0, g1, tap0, tap1, window, windowSq, Overlap)
			if lm != 0 && shortMdctSize < frameSize {
				combFilterWithSquare(buf, history+shortMdctSize, t1b, t2, frameSize-shortMdctSize, g1, g2, tap1b, tap2, window, windowSq, Overlap)
			}

			j = ch
			i = 0
			for ; i+3 < frameSize; i += 4 {
				samples[j] = buf[history+i]
				j += channels
				samples[j] = buf[history+i+1]
				j += channels
				samples[j] = buf[history+i+2]
				j += channels
				samples[j] = buf[history+i+3]
				j += channels
			}
			for ; i < frameSize; i++ {
				samples[j] = buf[history+i]
				j += channels
			}

			copy(hist, buf[frameSize:])
		}
	}

	d.updatePLCDecodeHistory(samples, frameSize, plcDecodeBufferSize)
	d.postfilterPeriodOld = d.postfilterPeriod
	d.postfilterGainOld = d.postfilterGain
	d.postfilterTapsetOld = d.postfilterTapset
	d.postfilterPeriod = newPeriod
	d.postfilterGain = newGain
	d.postfilterTapset = newTapset
	if lm != 0 {
		d.postfilterPeriodOld = d.postfilterPeriod
		d.postfilterGainOld = d.postfilterGain
		d.postfilterTapsetOld = d.postfilterTapset
	}
}

func combFilter(buf []float64, start int, t0, t1, n int, g0, g1 float64, tapset0, tapset1 int, window []float64, overlap int) {
	combFilterWithSquare(buf, start, t0, t1, n, g0, g1, tapset0, tapset1, window, nil, overlap)
}

func combFilterWithSquare(buf []float64, start int, t0, t1, n int, g0, g1 float64, tapset0, tapset1 int, window, windowSq []float64, overlap int) {
	if n <= 0 {
		return
	}
	if g0 == 0 && g1 == 0 {
		return
	}

	// Clamp periods to valid range, matching libopus:
	// T0 = IMAX(T0, COMBFILTER_MINPERIOD);
	// T1 = IMAX(T1, COMBFILTER_MINPERIOD);
	if t0 < combFilterMinPeriod {
		t0 = combFilterMinPeriod
	}
	if t1 < combFilterMinPeriod {
		t1 = combFilterMinPeriod
	}

	if overlap > n {
		overlap = n
	}
	if overlap > len(window) {
		overlap = len(window)
	}
	if windowSq != nil && overlap > len(windowSq) {
		overlap = len(windowSq)
	}

	if tapset0 < 0 || tapset0 >= len(combFilterGains) {
		tapset0 = 0
	}
	if tapset1 < 0 || tapset1 >= len(combFilterGains) {
		tapset1 = 0
	}

	g00 := g0 * combFilterGains[tapset0][0]
	g01 := g0 * combFilterGains[tapset0][1]
	g02 := g0 * combFilterGains[tapset0][2]
	g10 := g1 * combFilterGains[tapset1][0]
	g11 := g1 * combFilterGains[tapset1][1]
	g12 := g1 * combFilterGains[tapset1][2]

	x1 := buf[start-t1+1]
	x2 := buf[start-t1]
	x3 := buf[start-t1-1]
	x4 := buf[start-t1-2]

	if g0 == g1 && t0 == t1 && tapset0 == tapset1 {
		overlap = 0
	}

	for i := 0; i < overlap; i++ {
		var f float64
		if windowSq != nil {
			f = windowSq[i]
		} else {
			w := window[i]
			f = w * w
		}
		oneMinus := 1.0 - f
		idx := start + i
		x0 := buf[idx-t1+2]
		res := (oneMinus*g00)*buf[idx-t0] +
			(oneMinus*g01)*(buf[idx-t0-1]+buf[idx-t0+1]) +
			(oneMinus*g02)*(buf[idx-t0-2]+buf[idx-t0+2]) +
			(f*g10)*x2 +
			(f*g11)*(x3+x1) +
			(f*g12)*(x4+x0)
		buf[idx] += res
		x4 = x3
		x3 = x2
		x2 = x1
		x1 = x0
	}

	if g1 == 0 {
		return
	}

	i := overlap
	x4 = buf[start+i-t1-2]
	x3 = buf[start+i-t1-1]
	x2 = buf[start+i-t1]
	x1 = buf[start+i-t1+1]
	for ; i < n; i++ {
		idx := start + i
		x0 := buf[idx-t1+2]
		res := g10*x2 + g11*(x3+x1) + g12*(x4+x0)
		buf[idx] += res
		x4 = x3
		x3 = x2
		x2 = x1
		x1 = x0
	}
}

// combFilterWithInput applies the comb filter using a separate input buffer.
// This matches libopus comb_filter(y, x, ...) behavior where x is the source
// and y is the destination.
func combFilterWithInput(dst, src []float64, start int, t0, t1, n int, g0, g1 float64, tapset0, tapset1 int, window []float64, overlap int) {
	if n <= 0 {
		return
	}
	if g0 == 0 && g1 == 0 {
		copy(dst[start:start+n], src[start:start+n])
		return
	}

	if t0 < combFilterMinPeriod {
		t0 = combFilterMinPeriod
	}
	if t1 < combFilterMinPeriod {
		t1 = combFilterMinPeriod
	}

	if window == nil {
		overlap = 0
	}
	if overlap > n {
		overlap = n
	}
	if window != nil && overlap > len(window) {
		overlap = len(window)
	}

	if tapset0 < 0 || tapset0 >= len(combFilterGains) {
		tapset0 = 0
	}
	if tapset1 < 0 || tapset1 >= len(combFilterGains) {
		tapset1 = 0
	}

	copy(dst[start:start+n], src[start:start+n])

	g00 := g0 * combFilterGains[tapset0][0]
	g01 := g0 * combFilterGains[tapset0][1]
	g02 := g0 * combFilterGains[tapset0][2]
	g10 := g1 * combFilterGains[tapset1][0]
	g11 := g1 * combFilterGains[tapset1][1]
	g12 := g1 * combFilterGains[tapset1][2]

	x1 := src[start-t1+1]
	x2 := src[start-t1]
	x3 := src[start-t1-1]
	x4 := src[start-t1-2]

	if g0 == g1 && t0 == t1 && tapset0 == tapset1 {
		overlap = 0
	}

	for i := 0; i < overlap; i++ {
		w := window[i]
		f := w * w
		oneMinus := 1.0 - f
		idx := start + i
		x0 := src[idx-t1+2]
		res := (oneMinus*g00)*src[idx-t0] +
			(oneMinus*g01)*(src[idx-t0-1]+src[idx-t0+1]) +
			(oneMinus*g02)*(src[idx-t0-2]+src[idx-t0+2]) +
			(f*g10)*x2 +
			(f*g11)*(x3+x1) +
			(f*g12)*(x4+x0)
		dst[idx] += res
		x4 = x3
		x3 = x2
		x2 = x1
		x1 = x0
	}

	if g1 == 0 {
		return
	}

	i := overlap
	x4 = src[start+i-t1-2]
	x3 = src[start+i-t1-1]
	x2 = src[start+i-t1]
	x1 = src[start+i-t1+1]
	for ; i < n; i++ {
		idx := start + i
		x0 := src[idx-t1+2]
		res := g10*x2 + g11*(x3+x1) + g12*(x4+x0)
		dst[idx] += res
		x4 = x3
		x3 = x2
		x2 = x1
		x1 = x0
	}
}

// combFilterWithInputF32 applies the comb filter using float32 arithmetic.
// Encoder prefilter parity with libopus float path is sensitive to this precision.
func combFilterWithInputF32(dst, src []float64, start int, t0, t1, n int, g0, g1 float64, tapset0, tapset1 int, window []float64, overlap int) {
	if n <= 0 {
		return
	}
	if g0 == 0 && g1 == 0 {
		copy(dst[start:start+n], src[start:start+n])
		return
	}

	if t0 < combFilterMinPeriod {
		t0 = combFilterMinPeriod
	}
	if t1 < combFilterMinPeriod {
		t1 = combFilterMinPeriod
	}

	if window == nil {
		overlap = 0
	}
	if overlap > n {
		overlap = n
	}
	if window != nil && overlap > len(window) {
		overlap = len(window)
	}

	if tapset0 < 0 || tapset0 >= len(combFilterGains) {
		tapset0 = 0
	}
	if tapset1 < 0 || tapset1 >= len(combFilterGains) {
		tapset1 = 0
	}

	g00 := float32(g0 * combFilterGains[tapset0][0])
	g01 := float32(g0 * combFilterGains[tapset0][1])
	g02 := float32(g0 * combFilterGains[tapset0][2])
	g10 := float32(g1 * combFilterGains[tapset1][0])
	g11 := float32(g1 * combFilterGains[tapset1][1])
	g12 := float32(g1 * combFilterGains[tapset1][2])

	srcFrame := src[start:]
	dstFrame := dst[start:]
	delay1 := src[start-t1-2:]
	x1 := float32(delay1[3])
	x2 := float32(delay1[2])
	x3 := float32(delay1[1])
	x4 := float32(delay1[0])
	var delay0 []float64

	if g0 == g1 && t0 == t1 && tapset0 == tapset1 {
		overlap = 0
	} else if overlap > 0 {
		delay0 = src[start-t0-2:]
	}

	i := 0
	for ; i < overlap; i++ {
		w := float32(window[i])
		// Match libopus overlap path: compute f = window[i]*window[i] as a
		// standalone rounded float32 multiply before (1-f), avoiding fused fmsub.
		f := noFMA32Mul(w, w)
		oneMinus := float32(1.0) - f
		x0 := float32(delay1[i+4])
		sum := float32(srcFrame[i]) +
			(oneMinus*g00)*float32(delay0[i+2]) +
			(oneMinus*g01)*(float32(delay0[i+1])+float32(delay0[i+3])) +
			(oneMinus*g02)*(float32(delay0[i])+float32(delay0[i+4])) +
			(f*g10)*x2 +
			(f*g11)*(x1+x3) +
			(f*g12)*(x0+x4)
		dstFrame[i] = float64(sum)

		x4 = x3
		x3 = x2
		x2 = x1
		x1 = x0
	}

	if g1 == 0 {
		if i < n {
			copy(dstFrame[i:n], srcFrame[i:n])
		}
		return
	}

	x4 = float32(delay1[i])
	x3 = float32(delay1[i+1])
	x2 = float32(delay1[i+2])
	x1 = float32(delay1[i+3])
	for ; i < n; i++ {
		x0 := float32(delay1[i+4])
		sum := float32(srcFrame[i]) +
			g10*x2 +
			g11*(x3+x1) +
			g12*(x4+x0)
		dstFrame[i] = float64(sum)

		x4 = x3
		x3 = x2
		x2 = x1
		x1 = x0
	}
}
