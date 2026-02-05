package silk

import "math"

func autocorrelationF32(out, in []float32, length, order int) {
	for k := 0; k < order; k++ {
		var sum float64
		for n := 0; n < length-k; n++ {
			sum += float64(in[n]) * float64(in[n+k])
		}
		out[k] = float32(sum)
	}
}

func schurF32(refl, autoCorr []float32, order int) float32 {
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
		C[k][0] = float64(autoCorr[k])
		C[k][1] = float64(autoCorr[k])
	}
	for k := 0; k < order; k++ {
		den := C[0][1]
		if den < 1e-9 {
			den = 1e-9
		}
		rc := -C[k+1][0] / den
		refl[k] = float32(rc)
		for n := 0; n < order-k; n++ {
			c1 := C[n+k+1][0]
			c2 := C[n][1]
			C[n+k+1][0] = c1 + c2*rc
			C[n][1] = c2 + c1*rc
		}
	}
	return float32(C[0][1])
}

func k2aF32(a, rc []float32, order int) {
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

func bwexpanderF32(ar []float32, order int, chirp float32) {
	cfac := chirp
	for i := 0; i < order-1; i++ {
		ar[i] *= cfac
		cfac *= chirp
	}
	if order > 0 {
		ar[order-1] *= cfac
	}
}

func lpcAnalysisFilterF32(rLPC, predCoef, s []float32, length, order int) {
	if order > length {
		return
	}
	for i := 0; i < order; i++ {
		rLPC[i] = 0
	}
	for ix := order; ix < length; ix++ {
		var lpcPred float32
		for k := 0; k < order; k++ {
			lpcPred += s[ix-k-1] * predCoef[k]
		}
		rLPC[ix] = s[ix] - lpcPred
	}
}

func applySineWindowFLP32(pxWin, px []float32, winType, length int) {
	if length == 0 || length&3 != 0 {
		return
	}
	freq := float32(math.Pi / float64(length+1))
	// Approximation of 2 * cos(f)
	c := float32(2.0) - freq*freq

	var S0, S1 float32
	if winType < 2 {
		S0 = 0
		S1 = freq
	} else {
		S0 = 1
		S1 = 0.5 * c
	}

	for k := 0; k < length; k += 4 {
		pxWin[k+0] = px[k+0] * 0.5 * (S0 + S1)
		pxWin[k+1] = px[k+1] * S1
		S0 = c*S1 - S0
		pxWin[k+2] = px[k+2] * 0.5 * (S1 + S0)
		pxWin[k+3] = px[k+3] * S0
		S1 = c*S0 - S1
	}
}

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

	// Use the SILK analysis buffer (x_buf in libopus). This already contains
	// LTP memory + LA_SHAPE lookahead + current frame. LA_PITCH is covered
	// by the LA_SHAPE region (LA_SHAPE >= LA_PITCH).
	input32 := ensureFloat32Slice(&e.scratchPitchInput32, needed)
	src := e.inputBuffer
	for i := 0; i < needed; i++ {
		if i < len(src) {
			input32[i] = src[i] * silkSampleScale
		} else {
			input32[i] = 0
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
		copy(residual32, input32)
		residual := ensureFloat64Slice(&e.scratchLtpRes, needed)
		for i := 0; i < needed; i++ {
			residual[i] = float64(residual32[i])
		}
		resStart := histLen - frameSamples
		if resStart < 0 {
			resStart = 0
		}
		return residual, residual32, resStart, frameSamples
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

	Wsig := ensureFloat32Slice(&e.scratchPitchWsig32, pitchWinLen)
	xBufPtr := input32[needed-pitchWinLen:]
	if laPitch > 0 {
		applySineWindowFLP32(Wsig[:laPitch], xBufPtr, 1, laPitch)
		middleLen := pitchWinLen - 2*laPitch
		if middleLen > 0 {
			copy(Wsig[laPitch:laPitch+middleLen], xBufPtr[laPitch:laPitch+middleLen])
		}
		applySineWindowFLP32(Wsig[pitchWinLen-laPitch:], xBufPtr[pitchWinLen-laPitch:], 2, laPitch)
	} else {
		copy(Wsig, xBufPtr)
	}

	autoCorr := ensureFloat32Slice(&e.scratchPitchAuto32, order+1)
	autocorrelationF32(autoCorr, Wsig, pitchWinLen, order+1)
	autoCorr[0] += autoCorr[0]*float32(findPitchWhiteNoiseFraction) + 1.0

	refl := ensureFloat32Slice(&e.scratchPitchRefl32, order)
	resNrg := schurF32(refl, autoCorr, order)

	// Prediction gain (matching libopus silk_find_pitch_lags_FLP)
	e.lastLPCGain = float64(autoCorr[0]) / math.Max(float64(resNrg), 1.0)

	a := ensureFloat32Slice(&e.scratchPitchA32, order)
	for i := range a {
		a[i] = 0
	}
	k2aF32(a, refl, order)
	bwexpanderF32(a, order, float32(findPitchBandwidthExpansion))

	residual32 := ensureFloat32Slice(&e.scratchPitchRes32, needed)
	lpcAnalysisFilterF32(residual32, a, input32, needed, order)
	residual := ensureFloat64Slice(&e.scratchLtpRes, needed)
	for i := 0; i < needed; i++ {
		residual[i] = float64(residual32[i])
	}

	resStart := histLen - frameSamples
	if resStart < 0 {
		resStart = 0
	}

	if e.trace != nil && e.trace.Pitch != nil {
		tr := e.trace.Pitch
		tr.BufLen = needed
		tr.LtpMemLen = ltpMemSamples
		tr.LaPitch = laPitch
		tr.FrameSamples = frameSamples
		tr.PitchWinLen = pitchWinLen
		tr.NbSubfr = numSubframes
		tr.SubfrLen = subframeSamples
		tr.FsKHz = fsKHz
		tr.ResStart = resStart
		tr.PredGain = e.lastLPCGain
		tr.LPCOrder = order
		tr.XBufLen = needed
		tr.XBufHash = hashFloat32Slice(input32)
		xFrameEnd := ltpMemSamples + frameSamples + laPitch
		if xFrameEnd > len(input32) {
			xFrameEnd = len(input32)
		}
		if xFrameEnd > ltpMemSamples {
			tr.XFrameLen = xFrameEnd - ltpMemSamples
			tr.XFrameHash = hashFloat32Slice(input32[ltpMemSamples:xFrameEnd])
		} else {
			tr.XFrameLen = 0
			tr.XFrameHash = 0
		}
		tr.ResidualLen = len(residual32)
		tr.ResidualHash = hashFloat32Slice(residual32)
		if tr.CaptureResidual {
			tr.Residual = append(tr.Residual[:0], residual32...)
		}
		if tr.CaptureXBuf {
			tr.XBuf = append(tr.XBuf[:0], input32...)
		}
	}

	if e.trace != nil && e.trace.LTP != nil {
		tr := e.trace.LTP
		tr.ResStart = resStart
		tr.NbSubfr = numSubframes
		tr.SubfrLen = subframeSamples
		tr.ResidualLen = len(residual32)
		tr.ResidualHash = hashFloat32Slice(residual32)
		tr.PitchLags = tr.PitchLags[:0]
		tr.LTPIndex = tr.LTPIndex[:0]
		tr.BQ14 = tr.BQ14[:0]
		tr.SumLogGainQ7In = 0
		tr.SumLogGainQ7Out = 0
		tr.PERIndex = 0
		tr.PredGainQ7 = 0
		tr.XXHash = 0
		tr.XxHash = 0
		tr.XXLen = 0
		tr.XxLen = 0
		tr.XX = tr.XX[:0]
		tr.Xx = tr.Xx[:0]
		if tr.CaptureResidual {
			tr.Residual = append(tr.Residual[:0], residual32...)
		}
	}

	return residual, residual32, resStart, frameSamples
}
