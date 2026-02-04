package silk

import "math"

// noiseShapeAnalysis computes noise shaping parameters, gains, and sparseness-based
// quantization offset selection. This is a float port of the remaining pieces in
// silk_noise_shape_analysis_FLP.c.
func (e *Encoder) noiseShapeAnalysis(
	pcm []float32,
	pitchRes []float64,
	pitchResStart int,
	signalType int,
	speechActivityQ8 int,
	lpcPredGain float64,
	pitchLags []int,
	quantOffset int,
	numSubframes int,
	subframeSamples int,
) (*NoiseShapeParams, []float32, int) {
	if e.noiseShapeState == nil {
		e.noiseShapeState = NewNoiseShapeState()
	}

	fsKHz := e.sampleRate / 1000
	if fsKHz < 8 {
		fsKHz = 8
	}

	quantOffsetType := quantOffset
	if signalType == typeVoiced {
		// For voiced, start at 0; process_gains may override.
		quantOffsetType = 0
	} else {
		if offset, ok := computeSparsenessQuantOffset(pitchRes, pitchResStart, fsKHz, numSubframes); ok {
			quantOffsetType = offset
		}
	}

	inputQualityBandsQ15 := [4]int{-1, -1, -1, -1}
	if e.speechActivitySet {
		inputQualityBandsQ15 = e.inputQualityBandsQ15
	}

	// Compute average input quality from first two bands (matches libopus psEncCtrl->input_quality)
	var inputQuality float32
	if inputQualityBandsQ15[0] >= 0 {
		inputQuality = 0.5 * (float32(inputQualityBandsQ15[0]) + float32(inputQualityBandsQ15[1])) / 32768.0
	} else {
		inputQuality = float32(speechActivityQ8) / 256.0
	}

	// SNR adjustment for gain tweaking and coding quality (matching libopus SNR_adj_dB)
	snrDB := float64(e.snrDBQ7) / 128.0
	SNRAdjDB := snrDB
	b := 1.0 - float64(speechActivityQ8)/256.0
	// Initial estimate for coding quality to match recursive dependency in libopus
	initialCodingQuality := Sigmoid(0.25 * (float32(snrDB) - 20.0))
	if !e.useCBR {
		SNRAdjDB -= bgSNRDecrDB * float64(initialCodingQuality) * (0.5 + 0.5*float64(inputQuality)) * b * b
	}
	if signalType == typeVoiced {
		SNRAdjDB += harmSNRIncrDB * float64(e.ltpCorr)
	} else {
		SNRAdjDB += (-0.4*snrDB + 6.0) * (1.0 - float64(inputQuality))
	}

	params := e.noiseShapeState.ComputeNoiseShapeParams(
		signalType,
		speechActivityQ8,
		e.ltpCorr,
		pitchLags,
		snrDB,
		quantOffsetType,
		inputQualityBandsQ15,
		numSubframes,
		fsKHz,
	)

	gains, arShpQ13 := e.computeShapingARAndGains(
		pcm,
		numSubframes,
		subframeSamples,
		lpcPredGain,
		SNRAdjDB,
		signalType,
		speechActivityQ8,
		params.CodingQuality,
		params.InputQuality,
	)
	params.ARShpQ13 = arShpQ13

	return params, gains, quantOffsetType
}

func computeSparsenessQuantOffset(pitchRes []float64, start, fsKHz, numSubframes int) (int, bool) {
	if fsKHz <= 0 || numSubframes <= 0 {
		return 0, false
	}
	if len(pitchRes) == 0 {
		return 0, false
	}

	nSamples := 2 * fsKHz
	if nSamples <= 0 {
		return 0, false
	}

	nSegs := (subFrameLengthMs * numSubframes) / 2
	if nSegs <= 0 {
		return 0, false
	}

	if start < 0 {
		start = 0
	}
	if start >= len(pitchRes) {
		return 0, false
	}

	pitchResFrame := pitchRes[start:]
	maxSegs := len(pitchResFrame) / nSamples
	if maxSegs <= 0 {
		return 0, false
	}
	if nSegs > maxSegs {
		nSegs = maxSegs
	}

	energyVariation := 0.0
	logEnergyPrev := 0.0
	for k := 0; k < nSegs; k++ {
		seg := pitchResFrame[k*nSamples : (k+1)*nSamples]
		nrg := float64(nSamples) + energyF64(seg, nSamples)
		logEnergy := math.Log2(nrg)
		if k > 0 {
			energyVariation += math.Abs(logEnergy - logEnergyPrev)
		}
		logEnergyPrev = logEnergy
	}

	threshold := energyVariationThresholdQntOffset * float64(nSegs-1)
	if energyVariation > threshold {
		return 0, true
	}
	return 1, true
}

func (e *Encoder) computeShapingARAndGains(
	pcm []float32,
	numSubframes int,
	subframeSamples int,
	lpcPredGain float64,
	SNRAdjDB float64,
	signalType int,
	speechActivityQ8 int,
	codingQuality float32,
	inputQuality float32,
) ([]float32, []int16) {
	gains := ensureFloat32Slice(&e.scratchGains, numSubframes)
	arShpQ13 := ensureInt16Slice(&e.scratchArShpQ13, numSubframes*maxShapeLpcOrder)
	for i := range arShpQ13 {
		arShpQ13[i] = 0
	}
	if numSubframes == 0 || subframeSamples <= 0 || len(pcm) == 0 {
		for i := range gains {
			gains[i] = 1.0
		}
		return gains, arShpQ13
	}

	shapeOrder := e.shapingLPCOrder
	if shapeOrder <= 0 {
		shapeOrder = e.lpcOrder
	}
	if shapeOrder > maxShapeLpcOrder {
		shapeOrder = maxShapeLpcOrder
	}
	if shapeOrder < 2 {
		shapeOrder = 2
	}
	if shapeOrder&1 != 0 {
		shapeOrder--
	}

	fsKHz := e.sampleRate / 1000
	if fsKHz < 1 {
		fsKHz = 1
	}

	laShape := e.laShape
	if laShape < 0 {
		laShape = 0
	}

	frameSamples := numSubframes * subframeSamples
	if frameSamples > len(pcm) {
		frameSamples = len(pcm)
	}
	if frameSamples <= 0 {
		for i := range gains {
			gains[i] = 1.0
		}
		return gains, arShpQ13
	}

	shapeWinLength := subframeSamples + 2*laShape
	if shapeWinLength <= 0 {
		for i := range gains {
			gains[i] = 1.0
		}
		return gains, arShpQ13
	}

	xLen := frameSamples + 2*laShape
	xBuf := ensureFloat64Slice(&e.scratchShapeInput, xLen)
	for i := range xBuf {
		xBuf[i] = 0
	}

	// Populate xBuf from the SILK analysis buffer (x_buf in libopus).
	// libopus noise shaping uses x_ptr = x - la_shape, where x points to x_frame
	// (x_buf + ltp_mem). Align our window to that same origin.
	src := e.inputBuffer
	start := (ltpMemLengthMs*fsKHz - laShape)
	if start < 0 {
		start = 0
	}
	for i := 0; i < xLen; i++ {
		srcIdx := start + i
		if srcIdx < len(src) {
			xBuf[i] = float64(src[srcIdx]) * silkSampleScale
		} else {
			xBuf[i] = 0
		}
	}

	// Bandwidth expansion and warping.
	strength := findPitchWhiteNoiseFraction * lpcPredGain
	BWExp := bandwidthExpansion / (1.0 + strength*strength)
	warping := float64(e.warpingQ16)/65536.0 + 0.01*float64(codingQuality)

	flatPart := fsKHz * 3
	slopePart := (shapeWinLength - flatPart) / 2
	if slopePart < 0 {
		slopePart = 0
	}

	win := ensureFloat64Slice(&e.scratchShapeWindow, shapeWinLength)
	autoCorr := ensureFloat64Slice(&e.scratchShapeAutoCorr, shapeOrder+1)
	rc := ensureFloat64Slice(&e.scratchShapeRc, shapeOrder+1)
	ar := ensureFloat64Slice(&e.scratchShapeAr, shapeOrder)

	for k := 0; k < numSubframes; k++ {
		offset := k * subframeSamples
		segment := xBuf[offset : offset+shapeWinLength]

		if slopePart > 0 && slopePart*2+flatPart == shapeWinLength {
			applySineWindowFLP(win[:slopePart], segment[:slopePart], 1, slopePart)
			copy(win[slopePart:slopePart+flatPart], segment[slopePart:slopePart+flatPart])
			applySineWindowFLP(win[slopePart+flatPart:], segment[slopePart+flatPart:], 2, slopePart)
		} else {
			copy(win, segment)
		}

		if e.warpingQ16 > 0 {
			warpedAutocorrelationFLP(autoCorr, rc, win, warping, shapeWinLength, shapeOrder)
		} else {
			autocorrelationFLP(autoCorr, win, shapeWinLength, shapeOrder+1)
		}

		autoCorr[0] += autoCorr[0]*shapeWhiteNoiseFraction + 1.0

		nrg := schurFLP(rc, autoCorr, shapeOrder)
		for i := range ar {
			ar[i] = 0
		}
		k2aFLP(ar, rc, shapeOrder)

		g := 0.0
		if nrg > 0 {
			g = math.Sqrt(nrg)
		}
		if e.warpingQ16 > 0 {
			g *= warpedGain(ar, warping, shapeOrder)
		}

		bwexpanderFLP(ar, shapeOrder, BWExp)
		if e.warpingQ16 > 0 {
			warpedTrue2MonicCoefs(ar, warping, shapeCoefLimit, shapeOrder)
		} else {
			limitCoefs(ar, shapeCoefLimit, shapeOrder)
		}

		gains[k] = float32(g)
		base := k * maxShapeLpcOrder
		for i := 0; i < shapeOrder; i++ {
			arShpQ13[base+i] = floatToInt16(ar[i] * 8192.0)
		}
		for i := shapeOrder; i < maxShapeLpcOrder; i++ {
			arShpQ13[base+i] = 0
		}
	}

	gainMult := math.Pow(2.0, -0.16*SNRAdjDB)
	gainAdd := math.Pow(2.0, 0.16*float64(minQGainDb))
	for k := 0; k < numSubframes; k++ {
		gains[k] = float32(float64(gains[k])*gainMult + gainAdd)
	}

	return gains, arShpQ13
}

func warpedAutocorrelationFLP(out, state []float64, in []float64, warping float64, length, order int) {
	if order&1 != 0 {
		order--
	}
	if order <= 0 {
		return
	}
	for i := 0; i <= order && i < len(state); i++ {
		state[i] = 0
	}
	for i := 0; i <= order && i < len(out); i++ {
		out[i] = 0
	}

	for n := 0; n < length; n++ {
		tmp1 := in[n]
		for i := 0; i < order; i += 2 {
			tmp2 := state[i] + warping*state[i+1] - warping*tmp1
			state[i] = tmp1
			out[i] += state[0] * tmp1
			tmp1 = state[i+1] + warping*state[i+2] - warping*tmp2
			state[i+1] = tmp2
			out[i+1] += state[0] * tmp2
		}
		state[order] = tmp1
		out[order] += state[0] * tmp1
	}
}

func warpedGain(coefs []float64, lambda float64, order int) float64 {
	lambda = -lambda
	if order <= 0 {
		return 1.0
	}
	gain := coefs[order-1]
	for i := order - 2; i >= 0; i-- {
		gain = lambda*gain + coefs[i]
	}
	return 1.0 / (1.0 - lambda*gain)
}

func warpedTrue2MonicCoefs(coefs []float64, lambda, limit float64, order int) {
	if order <= 0 {
		return
	}
	for i := order - 1; i > 0; i-- {
		coefs[i-1] -= lambda * coefs[i]
	}
	gain := (1.0 - lambda*lambda) / (1.0 + lambda*coefs[0])
	for i := 0; i < order; i++ {
		coefs[i] *= gain
	}

	for iter := 0; iter < 10; iter++ {
		maxabs := -1.0
		ind := 0
		for i := 0; i < order; i++ {
			tmp := math.Abs(coefs[i])
			if tmp > maxabs {
				maxabs = tmp
				ind = i
			}
		}
		if maxabs <= limit {
			return
		}

		for i := 1; i < order; i++ {
			coefs[i-1] += lambda * coefs[i]
		}
		gain = 1.0 / gain
		for i := 0; i < order; i++ {
			coefs[i] *= gain
		}

		chirp := 0.99 - (0.8+0.1*float64(iter))*(maxabs-limit)/(maxabs*float64(ind+1))
		bwexpanderFLP(coefs, order, chirp)

		for i := order - 1; i > 0; i-- {
			coefs[i-1] -= lambda * coefs[i]
		}
		gain = (1.0 - lambda*lambda) / (1.0 + lambda*coefs[0])
		for i := 0; i < order; i++ {
			coefs[i] *= gain
		}
	}
}

func limitCoefs(coefs []float64, limit float64, order int) {
	if order <= 0 {
		return
	}
	for iter := 0; iter < 10; iter++ {
		maxabs := -1.0
		ind := 0
		for i := 0; i < order; i++ {
			tmp := math.Abs(coefs[i])
			if tmp > maxabs {
				maxabs = tmp
				ind = i
			}
		}
		if maxabs <= limit {
			return
		}
		chirp := 0.99 - (0.8+0.1*float64(iter))*(maxabs-limit)/(maxabs*float64(ind+1))
		bwexpanderFLP(coefs, order, chirp)
	}
}

func floatToInt16(x float64) int16 {
	return float64ToInt16Round(x)
}
