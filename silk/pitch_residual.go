package silk

import "math"

func autocorrelationF32(out, in []float32, length, order int) {
	if length <= 0 || order <= 0 {
		return
	}
	_ = in[length-1]
	_ = out[order-1]
	for k := 0; k < order; k++ {
		cnt := length - k
		out[k] = float32(innerProductF32(in[:cnt], in[k:k+cnt], cnt))
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
	// Match libopus silk_max_float(C[0][1], 1e-9f):
	// compare against float32 literal, then use that exact value in double domain.
	minDen := float64(float32(1e-9))
	for k := 0; k < order; k++ {
		den := C[0][1]
		if den < minDen {
			den = minDen
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
	// BCE hints: ensure all slice accesses in the unrolled loops are in-bounds.
	_ = rLPC[length-1]
	_ = s[length-1]
	_ = predCoef[order-1]
	switch order {
	case 6:
		// Cache coefficients in locals to avoid repeated slice indexing.
		a0, a1, a2, a3, a4, a5 := predCoef[0], predCoef[1], predCoef[2], predCoef[3], predCoef[4], predCoef[5]
		for ix := 6; ix < length; ix++ {
			lpcPred := s[ix-1]*a0 + s[ix-2]*a1 + s[ix-3]*a2 +
				s[ix-4]*a3 + s[ix-5]*a4 + s[ix-6]*a5
			rLPC[ix] = s[ix] - lpcPred
		}
	case 8:
		// Cache coefficients in locals to avoid repeated slice indexing.
		b0, b1, b2, b3 := predCoef[0], predCoef[1], predCoef[2], predCoef[3]
		b4, b5, b6, b7 := predCoef[4], predCoef[5], predCoef[6], predCoef[7]
		for ix := 8; ix < length; ix++ {
			lpcPred := s[ix-1]*b0 + s[ix-2]*b1 + s[ix-3]*b2 + s[ix-4]*b3 +
				s[ix-5]*b4 + s[ix-6]*b5 + s[ix-7]*b6 + s[ix-8]*b7
			rLPC[ix] = s[ix] - lpcPred
		}
	case 10:
		// Cache coefficients in locals to avoid repeated slice indexing.
		q0, q1, q2, q3, q4 := predCoef[0], predCoef[1], predCoef[2], predCoef[3], predCoef[4]
		q5, q6, q7, q8, q9 := predCoef[5], predCoef[6], predCoef[7], predCoef[8], predCoef[9]
		for ix := 10; ix < length; ix++ {
			lpcPred := s[ix-1]*q0 + s[ix-2]*q1 + s[ix-3]*q2 + s[ix-4]*q3 + s[ix-5]*q4 +
				s[ix-6]*q5 + s[ix-7]*q6 + s[ix-8]*q7 + s[ix-9]*q8 + s[ix-10]*q9
			rLPC[ix] = s[ix] - lpcPred
		}
	case 12:
		// Cache coefficients in locals to avoid repeated slice indexing.
		r0, r1, r2, r3 := predCoef[0], predCoef[1], predCoef[2], predCoef[3]
		r4, r5, r6, r7 := predCoef[4], predCoef[5], predCoef[6], predCoef[7]
		r8, r9, r10, r11 := predCoef[8], predCoef[9], predCoef[10], predCoef[11]
		for ix := 12; ix < length; ix++ {
			lpcPred := s[ix-1]*r0 + s[ix-2]*r1 + s[ix-3]*r2 + s[ix-4]*r3 +
				s[ix-5]*r4 + s[ix-6]*r5 + s[ix-7]*r6 + s[ix-8]*r7 +
				s[ix-9]*r8 + s[ix-10]*r9 + s[ix-11]*r10 + s[ix-12]*r11
			rLPC[ix] = s[ix] - lpcPred
		}
	case 16:
		// Cache coefficients in locals to avoid repeated slice indexing.
		p0, p1, p2, p3 := predCoef[0], predCoef[1], predCoef[2], predCoef[3]
		p4, p5, p6, p7 := predCoef[4], predCoef[5], predCoef[6], predCoef[7]
		p8, p9, p10, p11 := predCoef[8], predCoef[9], predCoef[10], predCoef[11]
		p12, p13, p14, p15 := predCoef[12], predCoef[13], predCoef[14], predCoef[15]
		for ix := 16; ix < length; ix++ {
			lpcPred := s[ix-1]*p0 + s[ix-2]*p1 + s[ix-3]*p2 + s[ix-4]*p3 +
				s[ix-5]*p4 + s[ix-6]*p5 + s[ix-7]*p6 + s[ix-8]*p7 +
				s[ix-9]*p8 + s[ix-10]*p9 + s[ix-11]*p10 + s[ix-12]*p11 +
				s[ix-13]*p12 + s[ix-14]*p13 + s[ix-15]*p14 + s[ix-16]*p15
			rLPC[ix] = s[ix] - lpcPred
		}
	default:
		for ix := order; ix < length; ix++ {
			var lpcPred float32
			for k := 0; k < order; k++ {
				lpcPred += s[ix-k-1] * predCoef[k]
			}
			rLPC[ix] = s[ix] - lpcPred
		}
	}
	for i := 0; i < order; i++ {
		rLPC[i] = 0
	}
}

func applySineWindowFLP32(pxWin, px []float32, winType, length int) {
	if length == 0 || length&3 != 0 {
		return
	}
	// Match libopus: freq = PI / (length + 1) where PI = 3.1415926536f (float32).
	// Compute in float32 to match C float / float precision.
	const piF32 = float32(3.1415926536)
	freq := piF32 / float32(length+1)
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
	// Split into two loops to eliminate per-sample bounds check.
	copyLen := needed
	if copyLen > len(src) {
		copyLen = len(src)
	}
	if copyLen > 0 {
		_ = src[copyLen-1]    // BCE hint
		_ = input32[needed-1] // BCE hint
		for i := 0; i < copyLen; i++ {
			input32[i] = src[i] * silkSampleScale
		}
	}
	for i := copyLen; i < needed; i++ {
		input32[i] = 0
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
	// libopus: psEncCtrl->predGain = auto_corr[0] / silk_max_float(res_nrg, 1.0f)
	// This is silk_float / silk_float = float32 division.
	resNrgClamped := resNrg
	if resNrgClamped < 1.0 {
		resNrgClamped = 1.0
	}
	predGainF32 := autoCorr[0] / resNrgClamped
	e.lastLPCGain = float64(predGainF32)

	a := ensureFloat32Slice(&e.scratchPitchA32, order)
	for i := range a {
		a[i] = 0
	}
	k2aF32(a, refl, order)
	bwexpanderF32(a, order, float32(findPitchBandwidthExpansion))

	residual32 := ensureFloat32Slice(&e.scratchPitchRes32, needed)
	lpcAnalysisFilterF32(residual32, a, input32, needed, order)
	residual := ensureFloat64Slice(&e.scratchLtpRes, needed)
	// 4x unrolled float32->float64 conversion.
	i := 0
	for ; i < needed-3; i += 4 {
		residual[i+0] = float64(residual32[i+0])
		residual[i+1] = float64(residual32[i+1])
		residual[i+2] = float64(residual32[i+2])
		residual[i+3] = float64(residual32[i+3])
	}
	for ; i < needed; i++ {
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
