package silk

import "math"

func autocorrelationFLP(out, in []float64, length, order int) {
	for k := 0; k < order; k++ {
		sum := 0.0
		for n := 0; n < length-k; n++ {
			sum += in[n] * in[n+k]
		}
		out[k] = sum
	}
}

func schurFLP(refl, autoCorr []float64, order int) float64 {
	if order <= 0 {
		if len(autoCorr) > 0 {
			return autoCorr[0]
		}
		return 0
	}
	if order > maxShapeLpcOrder {
		order = maxShapeLpcOrder
	}
	if order >= len(autoCorr) {
		order = len(autoCorr) - 1
	}
	if order > len(refl) {
		order = len(refl)
	}
	if order <= 0 {
		if len(autoCorr) > 0 {
			return autoCorr[0]
		}
		return 0
	}
	var C [maxShapeLpcOrder + 1][2]float64
	for k := 0; k <= order; k++ {
		C[k][0] = autoCorr[k]
		C[k][1] = autoCorr[k]
	}
	for k := 0; k < order; k++ {
		rc := -C[k+1][0] / math.Max(C[0][1], 1e-9)
		refl[k] = rc
		for n := 0; n < order-k; n++ {
			c1 := C[n+k+1][0]
			c2 := C[n][1]
			C[n+k+1][0] = c1 + c2*rc
			C[n][1] = c2 + c1*rc
		}
	}
	return C[0][1]
}

func k2aFLP(a, rc []float64, order int) {
	for k := 0; k < order; k++ {
		rck := rc[k]
		for n := 0; n < (k+1)/2; n++ {
			tmp1 := a[n]
			tmp2 := a[k-n-1]
			a[n] = tmp1 + tmp2*rck
			a[k-n-1] = tmp2 + tmp1*rck
		}
		a[k] = -rck
	}
}

func bwexpanderFLP(ar []float64, order int, chirp float64) {
	cfac := chirp
	for i := 0; i < order-1; i++ {
		ar[i] *= cfac
		cfac *= chirp
	}
	if order > 0 {
		ar[order-1] *= cfac
	}
}

func (e *Encoder) computePitchResidual(numSubframes int) ([]float64, []float32, int, int) {
	config := GetBandwidthConfig(e.bandwidth)
	fsKHz := config.SampleRate / 1000
	subframeSamples := config.SubframeSamples
	frameSamples := numSubframes * subframeSamples
	if frameSamples <= 0 {
		return nil, nil, 0, 0
	}

	ltpMemSamples := ltpMemLengthMs * fsKHz
	histLen := ltpMemSamples + frameSamples
	laPitch := laPitchMs * fsKHz
	needed := histLen + laPitch

	pitchBuf := e.pitchAnalysisBuf
	if len(pitchBuf) > histLen {
		pitchBuf = pitchBuf[len(pitchBuf)-histLen:]
	}

	input := ensureFloat64Slice(&e.scratchLtpInput, needed)
	for i := range input {
		input[i] = 0
	}
	if len(pitchBuf) > 0 {
		offset := histLen - len(pitchBuf)
		if offset < 0 {
			offset = 0
		}
		maxCopy := len(input) - offset
		if maxCopy > len(pitchBuf) {
			maxCopy = len(pitchBuf)
		}
		for i := 0; i < maxCopy; i++ {
			input[offset+i] = float64(pitchBuf[i])
		}
	}

	order := e.pitchEstimationLPCOrder
	if order == 0 {
		order = e.lpcOrder
	}
	if order > maxFindPitchLpcOrder {
		order = maxFindPitchLpcOrder
	}
	if order <= 0 {
		residual32 := ensureFloat32Slice(&e.scratchPitchRes32, needed)
		for i := 0; i < needed; i++ {
			residual32[i] = float32(input[i])
		}
		resStart := histLen - frameSamples
		if resStart < 0 {
			resStart = 0
		}
		return input, residual32, resStart, frameSamples
	}

	pitchWinMs := findPitchLpcWinMs
	if numSubframes == 2 {
		pitchWinMs = findPitchLpcWinMs2SF
	}
	pitchWinLen := pitchWinMs * fsKHz
	if pitchWinLen > needed {
		pitchWinLen = needed
	}
	if laPitch*2 > pitchWinLen {
		laPitch = pitchWinLen / 2
	}

	Wsig := ensureFloat64Slice(&e.scratchPitchWsig, pitchWinLen)
	xBufPtr := input[needed-pitchWinLen:]
	if laPitch > 0 {
		applySineWindowFLP(Wsig[:laPitch], xBufPtr, 1, laPitch)
		middleLen := pitchWinLen - 2*laPitch
		if middleLen > 0 {
			copy(Wsig[laPitch:laPitch+middleLen], xBufPtr[laPitch:laPitch+middleLen])
		}
		applySineWindowFLP(Wsig[pitchWinLen-laPitch:], xBufPtr[pitchWinLen-laPitch:], 2, laPitch)
	} else {
		copy(Wsig, xBufPtr)
	}

	autoCorr := ensureFloat64Slice(&e.scratchPitchAuto, order+1)
	autocorrelationFLP(autoCorr, Wsig, pitchWinLen, order+1)
	autoCorr[0] += autoCorr[0]*findPitchWhiteNoiseFraction + 1.0

	refl := ensureFloat64Slice(&e.scratchPitchRefl, order)
	schurFLP(refl, autoCorr, order)

	a := ensureFloat64Slice(&e.scratchPitchA, order)
	for i := range a {
		a[i] = 0
	}
	k2aFLP(a, refl, order)
	bwexpanderFLP(a, order, findPitchBandwidthExpansion)

	residual := ensureFloat64Slice(&e.scratchLtpRes, needed)
	lpcAnalysisFilterFLP(residual, a, input, needed, order)

	residual32 := ensureFloat32Slice(&e.scratchPitchRes32, needed)
	for i := 0; i < needed; i++ {
		residual32[i] = float32(residual[i])
	}

	resStart := histLen - frameSamples
	if resStart < 0 {
		resStart = 0
	}

	return residual, residual32, resStart, frameSamples
}
