package lpcnetplc

import (
	"math"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/internal/dnnblob"
)

const (
	analysisLPCOrder     = 16
	analysisPreemphasis  = 0.85
	analysisOverlapSize  = 160
	analysisTrainingOff  = 80
	analysisWindow5ms    = 4
	analysisWindowSize   = FrameSize + analysisOverlapSize
	analysisFreqSize     = analysisWindowSize/2 + 1
	analysisPitchBufSize = PitchMaxPeriod + 2*FrameSize
)

type analysisScratch struct {
	frame       [FrameSize]float32
	window      [analysisWindowSize]float32
	alignedIn   [FrameSize]float32
	spectrum    [analysisFreqSize]complex64
	fftIn       [analysisWindowSize]complex64
	fftOut      [analysisWindowSize]complex64
	fftScratch  [analysisWindowSize]celt.KissCpx
	bandEnergy  [NumBands]float32
	logBands    [NumBands]float32
	pitchXCorr  [pitchXcorrFeatures]float32
	pitchEner   [pitchXcorrFeatures]float32
	lpcInput    [FrameSize + analysisLPCOrder]float32
	lpcEx       [NumBands]float32
	lpcTmp      [NumBands]float32
	lpcInterp   [analysisFreqSize]float32
	inverseSpec [analysisFreqSize]complex64
	inverseReal [analysisWindowSize]float32
	burgIn      [FrameSize]float32
	burgBands   [NumBands]float32
	burgLog     [NumBands]float32
	burgTemp    [2 * NumBands]float32
	burgAF      [analysisLPCOrder]float64
	burgFirst   [analysisLPCOrder]float64
	burgLast    [analysisLPCOrder]float64
	burgCAF     [analysisLPCOrder + 1]float64
	burgCAB     [analysisLPCOrder + 1]float64
}

// Analysis mirrors the retained libopus LPCNet encoder-analysis state used by
// the PLC/DRED concealment front end.
type Analysis struct {
	pitch         PitchDNN
	analysisMem   [analysisOverlapSize]float32
	memPreemph    float32
	prevIF        [pitchIFMaxFreq]complex64
	ifFeatures    [pitchIFFeatures]float32
	xcorrFeatures [pitchXcorrFeatures]float32
	dnnPitch      float32
	pitchMem      [analysisLPCOrder]float32
	pitchFilt     float32
	excBuf        [analysisPitchBufSize]float32
	lpBuf         [analysisPitchBufSize]float32
	lpMem         [4]float32
	lpc           [analysisLPCOrder]float32
	features      [NumTotalFeatures]float32
	scratch       analysisScratch
}

// SetModel binds the shared pitch model family used by libopus LPCNet
// analysis. The retained analysis state is reset after a successful bind.
func (a *Analysis) SetModel(blob *dnnblob.Blob) error {
	if err := a.pitch.SetModel(blob); err != nil {
		a.Reset()
		return err
	}
	a.Reset()
	return nil
}

// Loaded reports whether the analysis runtime currently has the pitch model
// family required by libopus.
func (a *Analysis) Loaded() bool {
	return a != nil && a.pitch.Loaded()
}

// Reset clears the retained analysis state but preserves the bound model.
func (a *Analysis) Reset() {
	if a == nil {
		return
	}
	a.analysisMem = [analysisOverlapSize]float32{}
	a.memPreemph = 0
	a.prevIF = [pitchIFMaxFreq]complex64{}
	a.ifFeatures = [pitchIFFeatures]float32{}
	a.xcorrFeatures = [pitchXcorrFeatures]float32{}
	a.dnnPitch = 0
	a.pitchMem = [analysisLPCOrder]float32{}
	a.pitchFilt = 0
	a.excBuf = [analysisPitchBufSize]float32{}
	a.lpBuf = [analysisPitchBufSize]float32{}
	a.lpMem = [4]float32{}
	a.lpc = [analysisLPCOrder]float32{}
	a.features = [NumTotalFeatures]float32{}
	a.pitch.Reset()
}

// ComputeSingleFrameFeaturesFloat mirrors libopus
// lpcnet_compute_single_frame_features_float(). It retains encoder-analysis
// state across calls and writes one 36-float feature vector into dst.
func (a *Analysis) ComputeSingleFrameFeaturesFloat(dst, pcm []float32) int {
	if a == nil || !a.Loaded() || len(dst) < NumTotalFeatures || len(pcm) < FrameSize {
		return 0
	}
	copy(a.scratch.frame[:], pcm[:FrameSize])
	preemphasisInPlace(a.scratch.frame[:], &a.memPreemph, analysisPreemphasis)
	a.computeFrameFeatures(a.scratch.frame[:])
	copy(dst[:NumTotalFeatures], a.features[:])
	return NumTotalFeatures
}

// BurgCepstralAnalysis mirrors libopus burg_cepstral_analysis(). It writes the
// 36-float Burg cepstrum used by the first-loss PLC analysis loop.
func (a *Analysis) BurgCepstralAnalysis(dst, pcm []float32) int {
	if a == nil || len(dst) < 2*NumBands || len(pcm) < FrameSize {
		return 0
	}
	a.computeBurgCepstrum(dst[:NumBands], pcm[:FrameSize/2], FrameSize/2, analysisLPCOrder)
	a.computeBurgCepstrum(dst[NumBands:2*NumBands], pcm[FrameSize/2:FrameSize], FrameSize/2, analysisLPCOrder)
	for i := 0; i < NumBands; i++ {
		c0 := dst[i]
		c1 := dst[NumBands+i]
		dst[i] = .5 * (c0 + c1)
		dst[NumBands+i] = c0 - c1
	}
	return 2 * NumBands
}

// PrimeHistoryFramesFloat replays a run of normalized 10 ms PCM frames through
// the retained analysis frontend so first-loss CELT entry can start from the
// same recent 16 kHz history window used for PLC state priming.
func (a *Analysis) PrimeHistoryFramesFloat(history []float32) int {
	if a == nil || !a.Loaded() || len(history) < FrameSize {
		return 0
	}
	var features [NumTotalFeatures]float32
	total := 0
	for offset := 0; offset+FrameSize <= len(history); offset += FrameSize {
		frame := history[offset : offset+FrameSize]
		for i := 0; i < FrameSize; i++ {
			a.scratch.frame[i] = 32768 * frame[i]
		}
		if n := a.ComputeSingleFrameFeaturesFloat(features[:], a.scratch.frame[:]); n != NumTotalFeatures {
			return total
		}
		total += FrameSize
	}
	return total
}

func (a *Analysis) computeFrameFeatures(in []float32) {
	copy(a.scratch.alignedIn[:analysisTrainingOff], a.analysisMem[analysisOverlapSize-analysisTrainingOff:])
	a.frameAnalysis(a.scratch.spectrum[:], a.scratch.bandEnergy[:], in)

	a.ifFeatures[0] = clampUnit((1.0 / 64.0) * (10*log10f(1e-15+real(a.scratch.spectrum[0])*real(a.scratch.spectrum[0])) - 6))
	for i := 1; i < pitchIFMaxFreq; i++ {
		prod := mulConj(a.scratch.spectrum[i], a.prevIF[i])
		norm := float32(1.0 / math.Sqrt(float64(1e-15+real(prod)*real(prod)+imag(prod)*imag(prod))))
		prod *= complex(norm, 0)
		a.ifFeatures[3*i-2] = real(prod)
		a.ifFeatures[3*i-1] = imag(prod)
		energy := real(a.scratch.spectrum[i])*real(a.scratch.spectrum[i]) + imag(a.scratch.spectrum[i])*imag(a.scratch.spectrum[i])
		a.ifFeatures[3*i] = clampUnit((1.0 / 64.0) * (10*log10f(1e-15+energy) - 6))
	}
	copy(a.prevIF[:], a.scratch.spectrum[:pitchIFMaxFreq])

	logMax := float32(-2)
	follow := float32(-2)
	for i := 0; i < NumBands; i++ {
		v := log10f(1e-2 + a.scratch.bandEnergy[i])
		v = maxF32(logMax-8, maxF32(follow-2.5, v))
		a.scratch.logBands[i] = v
		logMax = maxF32(logMax, v)
		follow = maxF32(follow-2.5, v)
	}
	dctTransform(a.features[:NumBands], a.scratch.logBands[:])
	a.features[0] -= 4
	lpcFromCepstrum(a.lpc[:], a.features[:NumBands], &a.scratch)
	for i := 0; i < analysisLPCOrder; i++ {
		a.features[NumBands+2+i] = a.lpc[i]
	}

	copy(a.excBuf[:PitchMaxPeriod], a.excBuf[FrameSize:FrameSize+PitchMaxPeriod])
	copy(a.lpBuf[:PitchMaxPeriod], a.lpBuf[FrameSize:FrameSize+PitchMaxPeriod])
	copy(a.scratch.alignedIn[analysisTrainingOff:], in[:FrameSize-analysisTrainingOff])
	copy(a.scratch.lpcInput[:analysisLPCOrder], a.pitchMem[:])
	copy(a.scratch.lpcInput[analysisLPCOrder:], a.scratch.alignedIn[:])
	copy(a.pitchMem[:], a.scratch.alignedIn[FrameSize-analysisLPCOrder:])
	celtFIRFloat(a.scratch.lpcInput[:], a.lpc[:], a.lpBuf[PitchMaxPeriod:PitchMaxPeriod+FrameSize])
	for i := 0; i < FrameSize; i++ {
		a.excBuf[PitchMaxPeriod+i] = a.lpBuf[PitchMaxPeriod+i] + 0.7*a.pitchFilt
		a.pitchFilt = a.lpBuf[PitchMaxPeriod+i]
	}
	biquadInPlace(a.lpBuf[PitchMaxPeriod:PitchMaxPeriod+FrameSize], a.lpMem[:2])

	buf := a.excBuf[:]
	pitchXCorrFloat(a.scratch.pitchXCorr[:], buf[PitchMaxPeriod:PitchMaxPeriod+FrameSize], buf[:PitchMaxPeriod+FrameSize], FrameSize, pitchXcorrFeatures)
	ener0 := innerProdFloat(buf[PitchMaxPeriod:PitchMaxPeriod+FrameSize], buf[PitchMaxPeriod:PitchMaxPeriod+FrameSize], FrameSize)
	ener1 := innerProdFloat(buf[:FrameSize], buf[:FrameSize], FrameSize)
	for i := 0; i < pitchXcorrFeatures; i++ {
		ener := 1 + ener0 + ener1
		a.xcorrFeatures[i] = 2 * a.scratch.pitchXCorr[i]
		a.scratch.pitchEner[i] = ener
		ener1 += buf[i+FrameSize]*buf[i+FrameSize] - buf[i]*buf[i]
	}
	for i := 0; i < pitchXcorrFeatures; i++ {
		a.xcorrFeatures[i] /= a.scratch.pitchEner[i]
	}

	a.dnnPitch = a.pitch.Compute(a.ifFeatures[:], a.xcorrFeatures[:])
	pitch := int(math.Floor(.5 + float64(PitchMaxPeriod)/math.Pow(2, (1.0/60.0)*float64((a.dnnPitch+1.5)*60))))
	xx := innerProdFloat(a.lpBuf[PitchMaxPeriod:PitchMaxPeriod+FrameSize], a.lpBuf[PitchMaxPeriod:PitchMaxPeriod+FrameSize], FrameSize)
	yy := innerProdFloat(a.lpBuf[PitchMaxPeriod-pitch:PitchMaxPeriod-pitch+FrameSize], a.lpBuf[PitchMaxPeriod-pitch:PitchMaxPeriod-pitch+FrameSize], FrameSize)
	xy := innerProdFloat(a.lpBuf[PitchMaxPeriod:PitchMaxPeriod+FrameSize], a.lpBuf[PitchMaxPeriod-pitch:PitchMaxPeriod-pitch+FrameSize], FrameSize)
	frameCorr := xy / float32(math.Sqrt(float64(1+xx*yy)))
	frameCorr = float32(math.Log(float64(1+float32(math.Exp(float64(5*frameCorr))))) / math.Log(1+math.Exp(5)))
	a.features[NumBands] = a.dnnPitch
	a.features[NumBands+1] = frameCorr - .5
}

func (a *Analysis) computeBurgCepstrum(dst, pcm []float32, length, order int) {
	for i := 0; i < length-1; i++ {
		a.scratch.burgIn[i] = pcm[i+1] - analysisPreemphasis*pcm[i]
	}
	g := burgAnalysis(a.scratch.burgTemp[:analysisLPCOrder], a.scratch.burgIn[:length-1], 1e-3, length-1, 1, order, &a.scratch)
	g /= float32(length - 2*(order-1))
	clear(a.scratch.window[:])
	a.scratch.window[0] = 1
	for i := 0; i < order; i++ {
		a.scratch.window[i+1] = -a.scratch.burgTemp[i] * float32(math.Pow(.995, float64(i+1)))
	}
	for i := 0; i < analysisWindowSize; i++ {
		a.scratch.fftIn[i] = complex(a.scratch.window[i], 0)
	}
	celt.KissFFT32ToWithScratch(a.scratch.fftOut[:], a.scratch.fftIn[:], a.scratch.fftScratch[:])
	scale := float32(1.0 / analysisWindowSize)
	for i := 0; i < analysisFreqSize; i++ {
		a.scratch.spectrum[i] = a.scratch.fftOut[i] * complex(scale, 0)
	}
	computeBandEnergyInverse(a.scratch.burgBands[:], a.scratch.spectrum[:])
	logMax := float32(-2)
	follow := float32(-2)
	gainScale := .45 * g * (1.0 / float32(analysisWindowSize*analysisWindowSize*analysisWindowSize))
	for i := 0; i < NumBands; i++ {
		a.scratch.burgBands[i] *= gainScale
		v := log10f(1e-2 + a.scratch.burgBands[i])
		v = maxF32(logMax-8, maxF32(follow-2.5, v))
		a.scratch.burgLog[i] = v
		logMax = maxF32(logMax, v)
		follow = maxF32(follow-2.5, v)
	}
	dctTransform(dst[:NumBands], a.scratch.burgLog[:])
	dst[0] -= 4
}

func (a *Analysis) frameAnalysis(spectrum []complex64, ex, in []float32) {
	copy(a.scratch.window[:analysisOverlapSize], a.analysisMem[:])
	copy(a.scratch.window[analysisOverlapSize:], in[:FrameSize])
	copy(a.analysisMem[:], in[FrameSize-analysisOverlapSize:])
	applyAnalysisWindow(a.scratch.window[:])
	for i := 0; i < analysisWindowSize; i++ {
		a.scratch.fftIn[i] = complex(a.scratch.window[i], 0)
	}
	celt.KissFFT32ToWithScratch(a.scratch.fftOut[:], a.scratch.fftIn[:], a.scratch.fftScratch[:])
	scale := float32(1.0 / analysisWindowSize)
	for i := 0; i < analysisFreqSize; i++ {
		spectrum[i] = a.scratch.fftOut[i] * complex(scale, 0)
	}
	computeBandEnergy(ex, spectrum)
}

func preemphasisInPlace(x []float32, mem *float32, coef float32) {
	if len(x) == 0 || mem == nil {
		return
	}
	m := *mem
	for i := range x {
		y := x[i] + m
		m = -coef * x[i]
		x[i] = y
	}
	*mem = m
}

func applyAnalysisWindow(x []float32) {
	for i := 0; i < analysisOverlapSize; i++ {
		x[i] *= analysisHalfWindow[i]
		x[analysisWindowSize-1-i] *= analysisHalfWindow[i]
	}
}

func computeBandEnergy(bandE []float32, spectrum []complex64) {
	var sum [NumBands]float32
	for i := 0; i < NumBands-1; i++ {
		bandSize := (analysisBandEdges[i+1] - analysisBandEdges[i]) * analysisWindow5ms
		for j := 0; j < bandSize; j++ {
			frac := float32(j) / float32(bandSize)
			idx := analysisBandEdges[i]*analysisWindow5ms + j
			re := real(spectrum[idx])
			im := imag(spectrum[idx])
			tmp := re*re + im*im
			sum[i] += (1 - frac) * tmp
			sum[i+1] += frac * tmp
		}
	}
	sum[0] *= 2
	sum[NumBands-1] *= 2
	copy(bandE[:NumBands], sum[:])
}

func computeBandEnergyInverse(bandE []float32, spectrum []complex64) {
	var sum [NumBands]float32
	for i := 0; i < NumBands-1; i++ {
		bandSize := (analysisBandEdges[i+1] - analysisBandEdges[i]) * analysisWindow5ms
		for j := 0; j < bandSize; j++ {
			frac := float32(j) / float32(bandSize)
			idx := analysisBandEdges[i]*analysisWindow5ms + j
			re := real(spectrum[idx])
			im := imag(spectrum[idx])
			tmp := 1 / (re*re + im*im + 1e-9)
			sum[i] += (1 - frac) * tmp
			sum[i+1] += frac * tmp
		}
	}
	sum[0] *= 2
	sum[NumBands-1] *= 2
	copy(bandE[:NumBands], sum[:])
}

func dctTransform(out, in []float32) {
	const scale = 0.3333333333333333
	for i := 0; i < NumBands; i++ {
		var sum float32
		for j := 0; j < NumBands; j++ {
			sum += in[j] * analysisDCTTable[j*NumBands+i]
		}
		out[i] = sum * scale
	}
}

func idctTransform(out, in []float32) {
	const scale = 0.3333333333333333
	for i := 0; i < NumBands; i++ {
		var sum float32
		for j := 0; j < NumBands; j++ {
			sum += in[j] * analysisDCTTable[i*NumBands+j]
		}
		out[i] = sum * scale
	}
}

func lpcFromCepstrum(lpc, cepstrum []float32, scratch *analysisScratch) float32 {
	copy(scratch.lpcTmp[:], cepstrum[:NumBands])
	scratch.lpcTmp[0] += 4
	idctTransform(scratch.lpcEx[:], scratch.lpcTmp[:])
	for i := 0; i < NumBands; i++ {
		scratch.lpcEx[i] = float32(math.Pow(10, float64(scratch.lpcEx[i]))) * analysisCompensation[i]
	}
	return lpcFromBands(lpc, scratch.lpcEx[:], scratch)
}

func lpcFromBands(lpc, ex []float32, scratch *analysisScratch) float32 {
	interpBandGain(scratch.lpcInterp[:], ex)
	scratch.lpcInterp[analysisFreqSize-1] = 0
	for i := 0; i < analysisFreqSize; i++ {
		scratch.inverseSpec[i] = complex(scratch.lpcInterp[i], 0)
	}
	inverseTransform(scratch.inverseReal[:], scratch.inverseSpec[:], scratch)
	var ac [analysisLPCOrder + 1]float32
	copy(ac[:], scratch.inverseReal[:analysisLPCOrder+1])
	ac[0] += ac[0]*1e-4 + float32(320.0/12.0/38.0)
	for i := 1; i <= analysisLPCOrder; i++ {
		ac[i] *= 1 - 6e-5*float32(i*i)
	}
	return lpcnLPC(lpc, ac[:], analysisLPCOrder)
}

func interpBandGain(dst, bandE []float32) {
	clear(dst[:analysisFreqSize])
	for i := 0; i < NumBands-1; i++ {
		bandSize := (analysisBandEdges[i+1] - analysisBandEdges[i]) * analysisWindow5ms
		for j := 0; j < bandSize; j++ {
			frac := float32(j) / float32(bandSize)
			dst[analysisBandEdges[i]*analysisWindow5ms+j] = (1-frac)*bandE[i] + frac*bandE[i+1]
		}
	}
}

func inverseTransform(out []float32, in []complex64, scratch *analysisScratch) {
	copy(scratch.fftIn[:analysisFreqSize], in[:analysisFreqSize])
	for i := analysisFreqSize; i < analysisWindowSize; i++ {
		v := scratch.fftIn[analysisWindowSize-i]
		scratch.fftIn[i] = complex(real(v), -imag(v))
	}
	celt.KissFFT32ToWithScratch(scratch.fftOut[:], scratch.fftIn[:], scratch.fftScratch[:])
	out[0] = real(scratch.fftOut[0])
	for i := 1; i < analysisWindowSize; i++ {
		out[i] = real(scratch.fftOut[analysisWindowSize-i])
	}
}

func lpcnLPC(lpc, ac []float32, order int) float32 {
	var rc [analysisLPCOrder]float32
	clear(lpc[:order])
	clear(rc[:order])
	err := ac[0]
	if ac[0] == 0 {
		return err
	}
	for i := 0; i < order; i++ {
		var rr float32
		for j := 0; j < i; j++ {
			rr += lpc[j] * ac[i-j]
		}
		rr += ac[i+1]
		r := -rr / err
		rc[i] = r
		lpc[i] = r
		for j := 0; j < (i+1)>>1; j++ {
			tmp1 := lpc[j]
			tmp2 := lpc[i-1-j]
			lpc[j] = tmp1 + r*tmp2
			lpc[i-1-j] = tmp2 + r*tmp1
		}
		err -= (r * r) * err
		if err < .001*ac[0] {
			break
		}
	}
	return err
}

func burgAnalysis(dst, x []float32, minInvGain float32, subfrLength, nbSubfr, order int, scratch *analysisScratch) float32 {
	totalLen := nbSubfr * subfrLength
	if totalLen > len(x) || order > analysisLPCOrder {
		clear(dst[:order])
		return 0
	}
	af := scratch.burgAF[:order]
	first := scratch.burgFirst[:order]
	last := scratch.burgLast[:order]
	caf := scratch.burgCAF[:order+1]
	cab := scratch.burgCAB[:order+1]
	for i := range af {
		af[i] = 0
		first[i] = 0
		last[i] = 0
	}
	for i := range caf {
		caf[i] = 0
		cab[i] = 0
	}

	c0 := energyFloat64(x[:totalLen])
	for s := 0; s < nbSubfr; s++ {
		xPtr := x[s*subfrLength:]
		for n := 1; n <= order; n++ {
			first[n-1] += innerProdFloat64(xPtr[:subfrLength-n], xPtr[n:subfrLength], subfrLength-n)
		}
	}
	copy(last, first)
	caf[0] = c0 + 1e-5*c0 + 1e-9
	cab[0] = caf[0]
	invGain := 1.0
	reachedMaxGain := false

	for n := 0; n < order; n++ {
		for s := 0; s < nbSubfr; s++ {
			xPtr := x[s*subfrLength:]
			tmp1 := float64(xPtr[n])
			tmp2 := float64(xPtr[subfrLength-n-1])
			for k := 0; k < n; k++ {
				first[k] -= float64(xPtr[n]) * float64(xPtr[n-k-1])
				last[k] -= float64(xPtr[subfrLength-n-1]) * float64(xPtr[subfrLength-n+k])
				atmp := af[k]
				tmp1 += float64(xPtr[n-k-1]) * atmp
				tmp2 += float64(xPtr[subfrLength-n+k]) * atmp
			}
			for k := 0; k <= n; k++ {
				caf[k] -= tmp1 * float64(xPtr[n-k])
				cab[k] -= tmp2 * float64(xPtr[subfrLength-n+k-1])
			}
		}
		tmp1 := first[n]
		tmp2 := last[n]
		for k := 0; k < n; k++ {
			atmp := af[k]
			tmp1 += last[n-k-1] * atmp
			tmp2 += first[n-k-1] * atmp
		}
		caf[n+1] = tmp1
		cab[n+1] = tmp2

		num := cab[n+1]
		nrgB := cab[0]
		nrgF := caf[0]
		for k := 0; k < n; k++ {
			atmp := af[k]
			num += cab[n-k] * atmp
			nrgB += cab[k+1] * atmp
			nrgF += caf[k+1] * atmp
		}
		rc := -2.0 * num / (nrgF + nrgB)
		tmp1 = invGain * (1.0 - rc*rc)
		if tmp1 <= float64(minInvGain) {
			rc = math.Sqrt(1.0 - float64(minInvGain)/invGain)
			if num > 0 {
				rc = -rc
			}
			invGain = float64(minInvGain)
			reachedMaxGain = true
		} else {
			invGain = tmp1
		}

		for k := 0; k < (n+1)>>1; k++ {
			tmp1 = af[k]
			tmp2 = af[n-k-1]
			af[k] = tmp1 + rc*tmp2
			af[n-k-1] = tmp2 + rc*tmp1
		}
		af[n] = rc
		if reachedMaxGain {
			for k := n + 1; k < order; k++ {
				af[k] = 0
			}
			break
		}
		for k := 0; k <= n+1; k++ {
			tmp1 = caf[k]
			caf[k] += rc * cab[n-k+1]
			cab[n-k+1] += rc * tmp1
		}
	}

	var nrgF float64
	if reachedMaxGain {
		for k := 0; k < order; k++ {
			dst[k] = float32(-af[k])
		}
		for s := 0; s < nbSubfr; s++ {
			c0 -= energyFloat64(x[s*subfrLength : s*subfrLength+order])
		}
		nrgF = c0 * invGain
	} else {
		nrgF = caf[0]
		tmp1 := 1.0
		for k := 0; k < order; k++ {
			atmp := af[k]
			nrgF += caf[k+1] * atmp
			tmp1 += atmp * atmp
			dst[k] = float32(-atmp)
		}
		nrgF -= 1e-5 * c0 * tmp1
	}
	if nrgF < 0 {
		return 0
	}
	return float32(nrgF)
}

func energyFloat64(x []float32) float64 {
	var sum float64
	for _, v := range x {
		sum += float64(v) * float64(v)
	}
	return sum
}

func innerProdFloat64(x, y []float32, n int) float64 {
	var sum float64
	for i := 0; i < n; i++ {
		sum += float64(x[i]) * float64(y[i])
	}
	return sum
}

func celtFIRFloat(inWithHistory, coeffs, out []float32) {
	for i := 0; i < FrameSize; i++ {
		sum := inWithHistory[analysisLPCOrder+i]
		for j := 0; j < analysisLPCOrder; j++ {
			sum += coeffs[analysisLPCOrder-j-1] * inWithHistory[i+j]
		}
		out[i] = sum
	}
}

func biquadInPlace(y, mem []float32) {
	const (
		b0 = -0.84946
		b1 = 1.0
		a0 = -1.54220
		a1 = 0.70781
	)
	mem0 := mem[0]
	mem1 := mem[1]
	for i := range y {
		xi := y[i]
		yi := xi + mem0
		mem00 := mem0
		mem0 = (b0-a0)*xi + mem1 - a0*mem0
		mem1 = (b1-a1)*xi + 1e-30 - a1*mem00
		y[i] = yi
	}
	mem[0] = mem0
	mem[1] = mem1
}

func pitchXCorrFloat(dst, x, y []float32, length, maxPitch int) {
	for i := 0; i < maxPitch; i++ {
		var sum float32
		for j := 0; j < length; j++ {
			sum += x[j] * y[i+j]
		}
		dst[i] = sum
	}
}

func innerProdFloat(x, y []float32, length int) float32 {
	var sum float32
	for i := 0; i < length; i++ {
		sum += x[i] * y[i]
	}
	return sum
}

func log10f(x float32) float32 {
	return 0.3010299957 * celtLog2Approx(x)
}

func clampUnit(x float32) float32 {
	if x < -1 {
		return -1
	}
	if x > 1 {
		return 1
	}
	return x
}

func mulConj(a, b complex64) complex64 {
	return complex(real(a)*real(b)+imag(a)*imag(b), imag(a)*real(b)-real(a)*imag(b))
}

var analysisBandEdges = [...]int{
	0, 1, 2, 3, 4, 5, 6, 7, 8, 10, 12, 14, 16, 20, 24, 28, 34, 40,
}

var analysisCompensation = [...]float32{
	0.8, 1, 1, 1, 1, 1, 1, 1, 0.666667, 0.5, 0.5, 0.5, 0.333333, 0.25, 0.25, 0.2, 0.166667, 0.173913,
}

func celtLog2Approx(x float32) float32 {
	bits := math.Float32bits(x)
	integer := int32(bits>>23) - 127
	bitsInt := int32(bits)
	bitsInt -= int32(uint32(integer) << 23)
	bits = uint32(bitsInt)

	rangeIdx := (bits >> 20) & 0x7
	f := math.Float32frombits(bits)
	f = f*analysisLog2XNormCoeff[rangeIdx] - 1.0625
	f = analysisLog2CoeffA0 + f*(analysisLog2CoeffA1+f*(analysisLog2CoeffA2+f*(analysisLog2CoeffA3+f*analysisLog2CoeffA4)))
	return float32(integer) + f + analysisLog2YNormCoeff[rangeIdx]
}

var analysisLog2XNormCoeff = [8]float32{
	1.0000000000000000000000000000,
	8.88888895511627197265625e-01,
	8.00000000000000000000000e-01,
	7.27272748947143554687500e-01,
	6.66666686534881591796875e-01,
	6.15384638309478759765625e-01,
	5.71428596973419189453125e-01,
	5.33333361148834228515625e-01,
}

var analysisLog2YNormCoeff = [8]float32{
	0.0000000000000000000000000000,
	1.699250042438507080078125e-01,
	3.219280838966369628906250e-01,
	4.594316184520721435546875e-01,
	5.849624872207641601562500e-01,
	7.004396915435791015625000e-01,
	8.073549270629882812500000e-01,
	9.068905711174011230468750e-01,
}

const (
	analysisLog2CoeffA0 float32 = 8.74628424644470214843750000e-02
	analysisLog2CoeffA1 float32 = 1.357829570770263671875000000000
	analysisLog2CoeffA2 float32 = -6.3897705078125000000000000e-01
	analysisLog2CoeffA3 float32 = 4.01971250772476196289062500e-01
	analysisLog2CoeffA4 float32 = -2.8415444493293762207031250e-01
)
