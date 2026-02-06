//go:build cgo_libopus

package silk

import (
	"math"
	"testing"

	cgowrap "github.com/thesyncim/gopus/celt/cgo_test"
)

func TestNoiseShapeAnalysisAgainstLibopus(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)
	enc.SetComplexity(10)
	enc.SetVADState(255, 32766, [4]int{32767, 32767, 32767, 32767})

	cfg := GetBandwidthConfig(BandwidthWideband)
	subfrLen := cfg.SubframeSamples
	numSubfr := 4
	frameSamples := subfrLen * numSubfr

	signal := make([]float32, frameSamples*2)
	for i := range signal {
		tm := float64(i) / float64(cfg.SampleRate)
		signal[i] = float32(
			0.55*math.Sin(2*math.Pi*220*tm) +
				0.30*math.Sin(2*math.Pi*440*tm) +
				0.10*math.Sin(2*math.Pi*880*tm),
		)
	}

	_ = enc.EncodeFrame(signal[:frameSamples], nil, true)

	pcm := enc.quantizePCMToInt16(signal[frameSamples:])
	framePCM := enc.updateShapeBuffer(pcm, frameSamples)

	residual64, residual32, resStart, _ := enc.computePitchResidual(numSubfr)

	signalType := typeUnvoiced
	quantOffset := 0
	speechActivityQ8 := 255

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
	if enc.ltpCorr > 0 {
		signalType = typeVoiced
	} else {
		signalType = typeUnvoiced
	}

	// Capture pre-call smoothing state so libopus sees identical inputs.
	harmSmthIn := enc.noiseShapeState.HarmShapeGainSmth
	tiltSmthIn := enc.noiseShapeState.TiltSmth

	paramsGo, gainsGo, quantOffsetGo := enc.noiseShapeAnalysis(
		framePCM,
		residual64,
		resStart,
		signalType,
		speechActivityQ8,
		enc.lastLPCGain,
		pitchLags,
		quantOffset,
		numSubfr,
		subfrLen,
	)

	fsKHz := cfg.SampleRate / 1000
	ltpMem := ltpMemLengthMs * fsKHz
	laShape := enc.laShape
	xStart := ltpMem - laShape
	xLen := frameSamples + 2*laShape
	if xStart < 0 {
		t.Fatalf("invalid xStart: %d", xStart)
	}
	xWithLA := make([]float32, xLen)
	for i := 0; i < xLen; i++ {
		srcIdx := xStart + i
		if srcIdx >= 0 && srcIdx < len(enc.inputBuffer) {
			xWithLA[i] = enc.inputBuffer[srcIdx] * silkSampleScale
		}
	}

	if resStart+frameSamples > len(residual32) {
		t.Fatalf("pitch residual too short: resStart=%d frame=%d len=%d", resStart, frameSamples, len(residual32))
	}
	pitchResFrame := append([]float32(nil), residual32[resStart:resStart+frameSamples]...)

	libSnap, ok := cgowrap.SilkNoiseShapeAnalysisFLP(
		xWithLA,
		pitchResFrame,
		laShape,
		fsKHz,
		numSubfr,
		subfrLen,
		enc.shapeWinLength,
		enc.shapingLPCOrder,
		enc.warpingQ16,
		enc.snrDBQ7,
		enc.useCBR,
		speechActivityQ8,
		signalType,
		quantOffset,
		enc.inputQualityBandsQ15,
		pitchLags,
		enc.ltpCorr,
		float32(enc.lastLPCGain),
		harmSmthIn,
		tiltSmthIn,
	)
	if !ok {
		t.Fatal("failed to run libopus noise_shape_analysis wrapper")
	}

	if quantOffsetGo != libSnap.QuantOffsetType {
		t.Fatalf("quantOffsetType mismatch: go=%d lib=%d", quantOffsetGo, libSnap.QuantOffsetType)
	}
	if len(gainsGo) != len(libSnap.Gains) {
		t.Fatalf("gains length mismatch: go=%d lib=%d", len(gainsGo), len(libSnap.Gains))
	}

	const gainTol = 1e-3
	const shapeTol = 1e-3
	for k := 0; k < numSubfr; k++ {
		if diff := math.Abs(float64(gainsGo[k] - libSnap.Gains[k])); diff > gainTol {
			t.Fatalf("gain[%d] mismatch: go=%.6f lib=%.6f diff=%.6f", k, gainsGo[k], libSnap.Gains[k], diff)
		}

		goTilt := float32(paramsGo.TiltQ14[k]) / 16384.0
		if diff := math.Abs(float64(goTilt - libSnap.Tilt[k])); diff > shapeTol {
			t.Fatalf("tilt[%d] mismatch: go=%.6f lib=%.6f diff=%.6f", k, goTilt, libSnap.Tilt[k], diff)
		}

		goHarm := float32(paramsGo.HarmShapeGainQ14[k]) / 16384.0
		if diff := math.Abs(float64(goHarm - libSnap.HarmShapeGain[k])); diff > shapeTol {
			t.Fatalf("harmShape[%d] mismatch: go=%.6f lib=%.6f diff=%.6f", k, goHarm, libSnap.HarmShapeGain[k], diff)
		}

		goLFMA := float32(int16(paramsGo.LFShpQ14[k]&0xFFFF)) / 16384.0
		goLFAR := float32(int16((paramsGo.LFShpQ14[k]>>16)&0xFFFF)) / 16384.0
		if diff := math.Abs(float64(goLFMA - libSnap.LFMAShp[k])); diff > shapeTol {
			t.Fatalf("lfMA[%d] mismatch: go=%.6f lib=%.6f diff=%.6f", k, goLFMA, libSnap.LFMAShp[k], diff)
		}
		if diff := math.Abs(float64(goLFAR - libSnap.LFARShp[k])); diff > shapeTol {
			t.Fatalf("lfAR[%d] mismatch: go=%.6f lib=%.6f diff=%.6f", k, goLFAR, libSnap.LFARShp[k], diff)
		}
	}
}
