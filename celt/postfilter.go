package celt

const (
	combFilterMinPeriod = 15
	combFilterMaxPeriod = 1024
	combFilterHistory   = combFilterMaxPeriod + 2
)

var combFilterGains = [3][3]float64{
	{0.3066406250, 0.2170410156, 0.1296386719},
	{0.4638671875, 0.2680664062, 0.0000000000},
	{0.7998046875, 0.1000976562, 0.0000000000},
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
