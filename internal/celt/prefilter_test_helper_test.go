package celt

func pitchDownsample(x []float64, xLP []float32, length, channels, factor int) {
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
				xLP[i] = v
				idx += 2
			}
			xLP[0] = firQuarter*float32(x[1]) + firHalf*float32(x[0])
		} else if channels == 2 {
			chStride := len(x) / 2
			x0 := x[:chStride]
			x1 := x[chStride:]
			idx := 2
			for i := 1; i < length; i++ {
				v0 := firQuarter*float32(x0[idx-1]) + firQuarter*float32(x0[idx+1]) + firHalf*float32(x0[idx])
				v1 := firQuarter*float32(x1[idx-1]) + firQuarter*float32(x1[idx+1]) + firHalf*float32(x1[idx])
				xLP[i] = v0
				xLP[i] += v1
				idx += 2
			}
			v0 := firQuarter*float32(x0[1]) + firHalf*float32(x0[0])
			v1 := firQuarter*float32(x1[1]) + firHalf*float32(x1[0])
			xLP[0] = v0
			xLP[0] += v1
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
			xLP[i] = v
		}
		xLP[0] = firQuarter*float32(x[offset]) + firHalf*float32(x[0])
		if channels == 2 {
			chStride := len(x) / 2
			x1 := x[chStride:]
			for i := 1; i < length; i++ {
				idx := factor * i
				v := firQuarter*float32(x1[idx-offset]) +
					firQuarter*float32(x1[idx+offset]) +
					firHalf*float32(x1[idx])
				xLP[i] += v
			}
			v := firQuarter*float32(x1[offset]) + firHalf*float32(x1[0])
			xLP[0] += v
		}
	}

	var ac [5]float32
	pitchAutocorr5F32(xLP[:length], length, &ac)
	applyCELTAutocorrNoiseAndLagWindow32(ac[:], 4)

	lpc := lpcFromAutocorr32(ac)
	tmp := float32(1.0)
	for i := 0; i < 4; i++ {
		tmp *= float32(0.9)
		lpc[i] *= tmp
	}
	c1 := float32(0.8)
	lpc2 := [5]float32{
		lpc[0] + float32(0.8),
		lpc[1] + c1*lpc[0],
		lpc[2] + c1*lpc[1],
		lpc[3] + c1*lpc[2],
		c1 * lpc[3],
	}
	celtFIR5F32(xLP, lpc2)
}

func findBestPitch(xcorr []float64, y []float64, length, maxPitch int, bestPitch *[2]int) {
	xcorr32 := make([]float32, maxPitch)
	y32 := make([]float32, length+maxPitch)
	for i := range xcorr32 {
		xcorr32[i] = float32(xcorr[i])
	}
	for i := range y32 {
		y32[i] = float32(y[i])
	}
	findBestPitchF32(xcorr32, y32, length, maxPitch, bestPitch)
}

func findBestPitchInRanges(xcorr []float64, y []float64, length int, ranges [2]pitchSearchRange, bestPitch *[2]int) {
	maxPitch := len(xcorr)
	xcorr32 := make([]float32, maxPitch)
	y32 := make([]float32, len(y))
	for i := range xcorr32 {
		xcorr32[i] = float32(xcorr[i])
	}
	for i := range y32 {
		y32[i] = float32(y[i])
	}
	findBestPitchInRangesF32(xcorr32, y32, length, ranges, bestPitch)
}
