//go:build cgo_libopus

package silk

import (
	"math"
	"testing"
)

func updatePitchAnalysisBuffer(enc *Encoder, framePCM []float32) {
	pitchBufFrameLen := len(framePCM)
	if pitchBufFrameLen == 0 || len(enc.pitchAnalysisBuf) == 0 {
		return
	}
	if len(enc.pitchAnalysisBuf) > pitchBufFrameLen {
		copy(enc.pitchAnalysisBuf, enc.pitchAnalysisBuf[pitchBufFrameLen:])
	}
	start := len(enc.pitchAnalysisBuf) - pitchBufFrameLen
	if start < 0 {
		start = 0
		pitchBufFrameLen = len(enc.pitchAnalysisBuf)
	}
	copy(enc.pitchAnalysisBuf[start:], framePCM[:pitchBufFrameLen])
}

func generateNLSFTraceSignal(samples int, fs int) []float32 {
	signal := make([]float32, samples)
	freqs := []float64{440, 1000, 2000}
	amp := 0.3
	for i := 0; i < samples; i++ {
		tm := float64(i) / float64(fs)
		val := 0.0
		for _, f := range freqs {
			val += amp * math.Sin(2*math.Pi*f*tm)
		}
		signal[i] = float32(val)
	}
	return signal
}

func f64SliceFromF32(x []float32) []float64 {
	out := make([]float64, len(x))
	for i, v := range x {
		out[i] = float64(v)
	}
	return out
}

func lpcAnalysisFilterFLP32(rLPC, predCoef, s []float32, length, order int) {
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

func energyF32To64(x []float32, length int) float64 {
	var energy float64
	for i := 0; i < length; i++ {
		v := float64(x[i])
		energy += v * v
	}
	return energy
}

func computeInterpIdxFloat32(x []float32, prevNLSF, curNLSF []int16, resNrg float32, subfrLen, order int) (int, [4]float32) {
	interpIdx := 4
	resNrg2nd := float64(math.MaxFloat32)
	resNrg64 := float64(resNrg)
	analyzeLen := 2 * subfrLen
	if analyzeLen > len(x) {
		analyzeLen = len(x)
	}
	var energies [4]float32
	if analyzeLen <= 0 {
		return interpIdx, energies
	}
	lpcRes := make([]float32, analyzeLen)
	interpNLSF := make([]int16, order)
	lpcTmpQ12 := make([]int16, order)
	lpcTmpF32 := make([]float32, order)
	subfrEnergyLen := subfrLen - order
	if subfrEnergyLen < 0 {
		subfrEnergyLen = 0
	}
	for k := 3; k >= 0; k-- {
		for i := 0; i < order; i++ {
			diff := int32(curNLSF[i]) - int32(prevNLSF[i])
			interpNLSF[i] = int16(int32(prevNLSF[i]) + (int32(k)*diff >> 2))
		}
		silkNLSF2A(lpcTmpQ12, interpNLSF, order)
		for i := 0; i < order; i++ {
			lpcTmpF32[i] = float32(lpcTmpQ12[i]) / 4096.0
		}
		lpcAnalysisFilterFLP32(lpcRes, lpcTmpF32, x, analyzeLen, order)
		resNrgInterp := energyF32To64(lpcRes[order:], subfrEnergyLen)
		if order+subfrLen < analyzeLen {
			resNrgInterp += energyF32To64(lpcRes[order+subfrLen:], subfrEnergyLen)
		}
		energies[k] = float32(resNrgInterp)
		if resNrgInterp < resNrg64 {
			resNrg64 = resNrgInterp
			interpIdx = k
		} else if resNrgInterp > resNrg2nd {
			break
		}
		resNrg2nd = resNrgInterp
	}
	return interpIdx, energies
}

func TestNLSFInterpolationTraceAgainstLibopus(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)
	config := GetBandwidthConfig(BandwidthWideband)
	numSubfr := 4
	subfrLen := config.SubframeSamples
	frameSamples := numSubfr * subfrLen
	frames := 30

	signal := generateNLSFTraceSignal(frames*frameSamples, config.SampleRate)

	var mismatch int
	for frame := 0; frame < frames; frame++ {
		start := frame * frameSamples
		end := start + frameSamples
		pcm := signal[start:end]

		signalType := typeUnvoiced
		quantOffset := 0
		speechActivityQ8 := 200

		framePCM := enc.updateShapeBuffer(pcm, frameSamples)
		updatePitchAnalysisBuffer(enc, framePCM)

		residual, residual32, resStart, _ := enc.computePitchResidual(numSubfr)

		var pitchLags []int
		var ltpCoeffs LTPCoeffsArray
		predGainQ7 := int32(0)

		if signalType != typeNoVoiceActivity {
			searchThres1 := float64(enc.pitchEstimationThresholdQ16) / 65536.0
			prevSignalType := 0
			if enc.isPreviousFrameVoiced {
				prevSignalType = 2
			}
			thrhld := 0.6 - 0.004*float64(enc.pitchEstimationLPCOrder) -
				0.1*float64(speechActivityQ8)/256.0 -
				0.15*float64(prevSignalType>>1) -
				0.1*float64(enc.inputTiltQ15)/32768.0
			if thrhld < 0 {
				thrhld = 0
			} else if thrhld > 1 {
				thrhld = 1
			}

			pitchLags, _, _ := enc.detectPitch(residual32, numSubfr, searchThres1, thrhld)
			enc.ltpCorr = float32(enc.pitchState.ltpCorr)
			if enc.ltpCorr > 1.0 {
				enc.ltpCorr = 1.0
			}

			if enc.ltpCorr > 0 {
				signalType = typeVoiced
				ltpCoeffs, _, _, predGainQ7 = enc.analyzeLTPQuantized(residual, resStart, pitchLags, numSubfr, subfrLen)
			} else {
				signalType = typeUnvoiced
				enc.sumLogGainQ7 = 0
			}
		}

		noiseParams, gains, _ := enc.noiseShapeAnalysis(
			framePCM,
			residual,
			resStart,
			signalType,
			speechActivityQ8,
			enc.lastLPCGain,
			pitchLags,
			quantOffset,
			numSubfr,
			subfrLen,
		)

		fsKHz := config.SampleRate / 1000
		ltpMemSamples := ltpMemLengthMs * fsKHz
		pitchBuf := enc.inputBuffer
		frameStart := ltpMemSamples
		if frameStart+frameSamples > len(pitchBuf) {
			if len(pitchBuf) > frameSamples {
				frameStart = len(pitchBuf) - frameSamples
			} else {
				frameStart = 0
			}
		}

		ltpRes := enc.buildLTPResidual(pitchBuf, frameStart, gains, pitchLags, ltpCoeffs, numSubfr, subfrLen, signalType)
		codingQuality := float32(0.0)
		if noiseParams != nil {
			codingQuality = noiseParams.CodingQuality
		}
		minInvGainVal := computeMinInvGain(predGainQ7, codingQuality, !enc.haveEncoded)

		_, lsfQ15, interpIdx := enc.computeLPCAndNLSFWithInterp(ltpRes, numSubfr, subfrLen, minInvGainVal)

		useInterp := enc.complexity >= 4 && enc.haveEncoded && numSubfr == maxNbSubfr
		lib := libopusFindLPCInterp(ltpRes, numSubfr, subfrLen, enc.lpcOrder, useInterp, !enc.haveEncoded, enc.prevLSFQ15, float32(minInvGainVal))
		if interpIdx != lib.InterpQ2 {
			mismatch++
			if mismatch <= 5 {
				t.Logf("frame %d interp mismatch: go=%d lib=%d voiced=%v",
					frame, interpIdx, lib.InterpQ2, signalType == typeVoiced)
			}
		}

		stage1Idx, residuals, interpIdx := enc.quantizeLSFWithInterp(lsfQ15, enc.bandwidth, signalType, speechActivityQ8, numSubfr, interpIdx)
		lsfQ15 = enc.decodeQuantizedNLSF(stage1Idx, residuals, enc.bandwidth)
		copy(enc.prevLSFQ15, lsfQ15)
		enc.haveEncoded = true
		enc.isPreviousFrameVoiced = signalType == typeVoiced
	}

	t.Logf("NLSF interp mismatches: %d/%d", mismatch, frames)
}

func TestNLSFInterpolationVoicedTraceAgainstLibopus(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)
	config := GetBandwidthConfig(BandwidthWideband)
	numSubfr := 4
	subfrLen := config.SubframeSamples
	frameSamples := numSubfr * subfrLen
	frames := 30

	signal := generateVoicedPitchTraceSignal(frames*frameSamples, config.SampleRate)

	var mismatch int
	for frame := 0; frame < frames; frame++ {
		start := frame * frameSamples
		end := start + frameSamples
		pcm := signal[start:end]

		signalType := typeUnvoiced
		quantOffset := 0
		speechActivityQ8 := 200

		framePCM := enc.updateShapeBuffer(pcm, frameSamples)
		updatePitchAnalysisBuffer(enc, framePCM)

		residual, residual32, resStart, _ := enc.computePitchResidual(numSubfr)

		var pitchLags []int
		var ltpCoeffs LTPCoeffsArray
		predGainQ7 := int32(0)

		if signalType != typeNoVoiceActivity {
			searchThres1 := float64(enc.pitchEstimationThresholdQ16) / 65536.0
			prevSignalType := 0
			if enc.isPreviousFrameVoiced {
				prevSignalType = 2
			}
			thrhld := 0.6 - 0.004*float64(enc.pitchEstimationLPCOrder) -
				0.1*float64(speechActivityQ8)/256.0 -
				0.15*float64(prevSignalType>>1) -
				0.1*float64(enc.inputTiltQ15)/32768.0
			if thrhld < 0 {
				thrhld = 0
			} else if thrhld > 1 {
				thrhld = 1
			}

			pitchLags, _, _ := enc.detectPitch(residual32, numSubfr, searchThres1, thrhld)
			enc.ltpCorr = float32(enc.pitchState.ltpCorr)
			if enc.ltpCorr > 1.0 {
				enc.ltpCorr = 1.0
			}

			if enc.ltpCorr > 0 {
				signalType = typeVoiced
				ltpCoeffs, _, _, predGainQ7 = enc.analyzeLTPQuantized(residual, resStart, pitchLags, numSubfr, subfrLen)
			} else {
				signalType = typeUnvoiced
				enc.sumLogGainQ7 = 0
			}
		}

		noiseParams, gains, _ := enc.noiseShapeAnalysis(
			framePCM,
			residual,
			resStart,
			signalType,
			speechActivityQ8,
			enc.lastLPCGain,
			pitchLags,
			quantOffset,
			numSubfr,
			subfrLen,
		)

		fsKHz := config.SampleRate / 1000
		ltpMemSamples := ltpMemLengthMs * fsKHz
		pitchBuf := enc.inputBuffer
		frameStart := ltpMemSamples
		if frameStart+frameSamples > len(pitchBuf) {
			if len(pitchBuf) > frameSamples {
				frameStart = len(pitchBuf) - frameSamples
			} else {
				frameStart = 0
			}
		}

		ltpRes := enc.buildLTPResidual(pitchBuf, frameStart, gains, pitchLags, ltpCoeffs, numSubfr, subfrLen, signalType)
		codingQuality := float32(0.0)
		if noiseParams != nil {
			codingQuality = noiseParams.CodingQuality
		}
		minInvGainVal := computeMinInvGain(predGainQ7, codingQuality, !enc.haveEncoded)

		_, lsfQ15, interpIdx := enc.computeLPCAndNLSFWithInterp(ltpRes, numSubfr, subfrLen, minInvGainVal)

		useInterp := enc.complexity >= 4 && enc.haveEncoded && numSubfr == maxNbSubfr
		lib := libopusFindLPCInterp(ltpRes, numSubfr, subfrLen, enc.lpcOrder, useInterp, !enc.haveEncoded, enc.prevLSFQ15, float32(minInvGainVal))
		if interpIdx != lib.InterpQ2 {
			mismatch++
			if mismatch <= 3 {
				subfrLenWithOrder := subfrLen + enc.lpcOrder
				libDebug := libopusFindLPCInterpDebug(ltpRes, numSubfr, subfrLen, enc.lpcOrder, useInterp, !enc.haveEncoded, enc.prevLSFQ15, float32(minInvGainVal))
				_, libResFull := libopusBurgModified(ltpRes, float32(minInvGainVal), subfrLenWithOrder, numSubfr, enc.lpcOrder)
				libResLast, libALast := float32(0), []float32(nil)
				halfOffset := (maxNbSubfr / 2) * subfrLenWithOrder
				if halfOffset < len(ltpRes) {
					libALast, libResLast = libopusBurgModified(ltpRes[halfOffset:], float32(minInvGainVal), subfrLenWithOrder, maxNbSubfr/2, enc.lpcOrder)
				}
				libRes := libResFull - libResLast

				lsfLastLib := make([]int16, enc.lpcOrder)
				if len(libALast) == enc.lpcOrder {
					lpcQ16 := make([]int32, enc.lpcOrder)
					for i := 0; i < enc.lpcOrder; i++ {
						lpcQ16[i] = float64ToInt32Round(float64(libALast[i]) * 65536.0)
					}
					silkA2NLSF(lsfLastLib, lpcQ16, enc.lpcOrder)
				}
				// Compute Go burg energies for comparison without disturbing encoder state.
				savedTotal, savedInvGain, savedNum := enc.lastTotalEnergy, enc.lastInvGain, enc.lastNumSamples
				_, goResFull := enc.burgModifiedFLPZeroAlloc(f64SliceFromF32(ltpRes), minInvGainVal, subfrLenWithOrder, numSubfr, enc.lpcOrder)
				_, goResLast := enc.burgModifiedFLPZeroAlloc(f64SliceFromF32(ltpRes[halfOffset:]), minInvGainVal, subfrLenWithOrder, maxNbSubfr/2, enc.lpcOrder)
				enc.lastTotalEnergy, enc.lastInvGain, enc.lastNumSamples = savedTotal, savedInvGain, savedNum
				goRes := goResFull - goResLast
				lsfLastGo := make([]int16, enc.lpcOrder)
				{
					savedTotal, savedInvGain, savedNum := enc.lastTotalEnergy, enc.lastInvGain, enc.lastNumSamples
					aLast, _ := enc.burgModifiedFLPZeroAlloc(f64SliceFromF32(ltpRes[halfOffset:]), minInvGainVal, subfrLenWithOrder, maxNbSubfr/2, enc.lpcOrder)
					enc.lastTotalEnergy, enc.lastInvGain, enc.lastNumSamples = savedTotal, savedInvGain, savedNum
					lpcQ16 := make([]int32, enc.lpcOrder)
					for i := 0; i < enc.lpcOrder; i++ {
						lpcQ16[i] = float64ToInt32Round(aLast[i] * 65536.0)
					}
					silkA2NLSF(lsfLastGo, lpcQ16, enc.lpcOrder)
				}
				interpIdxF32, energies := computeInterpIdxFloat32(ltpRes, enc.prevLSFQ15, lsfLastLib, libRes, subfrLenWithOrder, enc.lpcOrder)
				t.Logf("frame %d interp mismatch: go=%d lib=%d voiced=%v f32=%d energies=%v libRes=%.6f goRes=%.6f libE=%v",
					frame, interpIdx, lib.InterpQ2, signalType == typeVoiced, interpIdxF32, energies, libRes, goRes, libDebug.ResNrgInterp)
				if mismatch <= 1 {
					t.Logf("  lsfLast diff (lib-go) first5: %v",
						[]int16{int16(lsfLastLib[0] - lsfLastGo[0]), int16(lsfLastLib[1] - lsfLastGo[1]), int16(lsfLastLib[2] - lsfLastGo[2]), int16(lsfLastLib[3] - lsfLastGo[3]), int16(lsfLastLib[4] - lsfLastGo[4])})
					for _, k := range []int{3, 2} {
						nlsf0 := make([]int16, enc.lpcOrder)
						interpolateNLSF(nlsf0, enc.prevLSFQ15, lsfLastLib, k, enc.lpcOrder)
						goQ12 := make([]int16, enc.lpcOrder)
						silkNLSF2A(goQ12, nlsf0, enc.lpcOrder)
						libQ12 := libopusNLSF2A(nlsf0, enc.lpcOrder)
						maxDiff := int16(0)
						for i := 0; i < enc.lpcOrder; i++ {
							diff := goQ12[i] - libQ12[i]
							if diff < 0 {
								diff = -diff
							}
							if diff > maxDiff {
								maxDiff = diff
							}
						}
						pred := make([]float32, enc.lpcOrder)
						for i := 0; i < enc.lpcOrder; i++ {
							pred[i] = float32(goQ12[i]) / 4096.0
						}
						analyzeLen := 2 * subfrLenWithOrder
						if analyzeLen > len(ltpRes) {
							analyzeLen = len(ltpRes)
						}
						libRes := libopusLPCAnalysisFilter(ltpRes, pred, analyzeLen, enc.lpcOrder)
						goRes := make([]float32, analyzeLen)
						lpcAnalysisFilterFLP32(goRes, pred, ltpRes, analyzeLen, enc.lpcOrder)
						subfrEnergyLen := subfrLenWithOrder - enc.lpcOrder
						libEnergy := energyF32To64(libRes[enc.lpcOrder:], subfrEnergyLen) +
							energyF32To64(libRes[enc.lpcOrder+subfrLenWithOrder:], subfrEnergyLen)
						goEnergy := energyF32To64(goRes[enc.lpcOrder:], subfrEnergyLen) +
							energyF32To64(goRes[enc.lpcOrder+subfrLenWithOrder:], subfrEnergyLen)
						t.Logf("  NLSF2A k=%d max|diff|=%d libEnergy=%.6f goEnergy=%.6f", k, maxDiff, libEnergy, goEnergy)
					}
				}
			}
		}

		stage1Idx, residuals, interpIdx := enc.quantizeLSFWithInterp(lsfQ15, enc.bandwidth, signalType, speechActivityQ8, numSubfr, interpIdx)
		lsfQ15 = enc.decodeQuantizedNLSF(stage1Idx, residuals, enc.bandwidth)
		copy(enc.prevLSFQ15, lsfQ15)
		enc.haveEncoded = true
		enc.isPreviousFrameVoiced = signalType == typeVoiced
	}

	t.Logf("NLSF interp mismatches (voiced trace): %d/%d", mismatch, frames)
}
