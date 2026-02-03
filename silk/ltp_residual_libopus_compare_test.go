//go:build cgo_libopus

package silk

import (
	"math"
	"testing"
)

func TestLTPResidualTraceAgainstLibopus(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)
	config := GetBandwidthConfig(BandwidthWideband)
	numSubfr := 4
	subfrLen := config.SubframeSamples
	frameSamples := numSubfr * subfrLen
	frames := 30

	signal := generateNLSFTraceSignal(frames*frameSamples, config.SampleRate)

	fsKHz := config.SampleRate / 1000
	ltpMemSamples := ltpMemLengthMs * fsKHz
	preLen := enc.lpcOrder

	var mismatches int
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

			pitchLags, _, _ = enc.detectPitch(residual32, numSubfr, searchThres1, thrhld)
			enc.ltpCorr = float32(enc.pitchState.ltpCorr)
			if enc.ltpCorr > 1.0 {
				enc.ltpCorr = 1.0
			}

			if enc.ltpCorr > 0 {
				signalType = typeVoiced
				ltpCoeffs, _, _, _ = enc.analyzeLTPQuantized(residual, resStart, pitchLags, numSubfr, subfrLen)
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
		_ = noiseParams

		pitchBuf := enc.inputBuffer
		frameStart := ltpMemSamples
		if frameStart+frameSamples > len(pitchBuf) {
			if len(pitchBuf) > frameSamples {
				frameStart = len(pitchBuf) - frameSamples
			} else {
				frameStart = 0
			}
		}

		ltpResGo := enc.buildLTPResidual(pitchBuf, frameStart, gains, pitchLags, ltpCoeffs, numSubfr, subfrLen, signalType)

		xStart := frameStart - preLen
		if xStart < 0 {
			xStart = 0
		}
		xScaledFull := make([]float32, len(pitchBuf))
		for i := range pitchBuf {
			xScaledFull[i] = float32(floatToInt16Round(pitchBuf[i] * float32(silkSampleScale)))
		}
		x := xScaledFull[xStart:]
		invGains := make([]float32, numSubfr)
		for i := range invGains {
			if i < len(gains) && gains[i] > 0 {
				invGains[i] = 1.0 / gains[i]
			} else {
				invGains[i] = 1.0
			}
		}
		b := make([]float32, numSubfr*ltpOrderConst)
		for k := 0; k < numSubfr; k++ {
			for j := 0; j < ltpOrderConst; j++ {
				b[k*ltpOrderConst+j] = float32(ltpCoeffs[k][j]) / 128.0
			}
		}
		pitchLagsUse := make([]int, numSubfr)
		copy(pitchLagsUse, pitchLags)

		ltpResLib := libopusLTPAnalysisFilter(x, b, pitchLagsUse, invGains, subfrLen, numSubfr, preLen)
		if len(ltpResLib) != len(ltpResGo) {
			t.Fatalf("frame %d ltpRes length mismatch: go=%d lib=%d", frame, len(ltpResGo), len(ltpResLib))
		}
		var maxDiff float64
		for i := range ltpResGo {
			diff := math.Abs(float64(ltpResGo[i]) - float64(ltpResLib[i]))
			if diff > maxDiff {
				maxDiff = diff
			}
		}
		if maxDiff > 1e-4 {
			mismatches++
			if mismatches <= 3 {
				t.Logf("frame %d ltpRes max diff %.6g (voiced=%v)", frame, maxDiff, signalType == typeVoiced)
			}
		}

		enc.haveEncoded = true
		enc.isPreviousFrameVoiced = signalType == typeVoiced
	}

	t.Logf("LTP residual mismatches: %d/%d", mismatches, frames)
}
