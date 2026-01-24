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

func (d *Decoder) applyPostfilter(samples []float64, frameSize, lm int) {
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

	t0 := d.postfilterPeriodOld
	t1 := d.postfilterPeriod
	g0 := d.postfilterGainOld
	g1 := d.postfilterGain
	tap0 := d.postfilterTapsetOld
	tap1 := d.postfilterTapset

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

	if g0 == 0 {
		t0 = t1
	}
	if g1 == 0 {
		t1 = t0
	}

	shortMdctSize := frameSize >> uint(lm)
	if shortMdctSize <= 0 || shortMdctSize > frameSize {
		shortMdctSize = frameSize
	}

	window := GetWindowBuffer(Overlap)

	for ch := 0; ch < d.channels; ch++ {
		hist := d.postfilterMem[ch*history : (ch+1)*history]
		buf := make([]float64, history+frameSize)
		copy(buf, hist)

		if d.channels == 1 {
			copy(buf[history:], samples)
		} else {
			for i := 0; i < frameSize; i++ {
				buf[history+i] = samples[i*d.channels+ch]
			}
		}

		combFilter(buf, history, t0, t1, shortMdctSize, g0, g1, tap0, tap1, window, Overlap)
		if lm != 0 && shortMdctSize < frameSize {
			combFilter(buf, history+shortMdctSize, t1, t1, frameSize-shortMdctSize, g1, g1, tap1, tap1, window, Overlap)
		}

		if d.channels == 1 {
			copy(samples, buf[history:history+frameSize])
		} else {
			for i := 0; i < frameSize; i++ {
				samples[i*d.channels+ch] = buf[history+i]
			}
		}

		copy(hist, buf[frameSize:])
	}

	d.postfilterPeriodOld = d.postfilterPeriod
	d.postfilterGainOld = d.postfilterGain
	d.postfilterTapsetOld = d.postfilterTapset
}

func combFilter(buf []float64, start int, t0, t1, n int, g0, g1 float64, tapset0, tapset1 int, window []float64, overlap int) {
	if n <= 0 {
		return
	}
	if g0 == 0 && g1 == 0 {
		return
	}

	if overlap > n {
		overlap = n
	}
	if overlap > len(window) {
		overlap = len(window)
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
		f := window[i] * window[i]
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
