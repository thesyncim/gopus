//go:build cgo_libopus

package silk

import (
	"math"
	"testing"
)

func generatePitchTraceSignal(samples int, fs int) []float32 {
	signal := make([]float32, samples)
	var seed uint32 = 1
	for i := 0; i < samples; i++ {
		tm := float64(i) / float64(fs)
		base := 0.45*math.Sin(2*math.Pi*220*tm) + 0.2*math.Sin(2*math.Pi*330*tm)
		seed = seed*1664525 + 1013904223
		noise := 0.05 * (float64((seed>>9)&0x3FF)/512.0 - 1.0)
		signal[i] = float32(base + noise)
	}
	return signal
}

func generateVoicedPitchTraceSignal(samples int, fs int) []float32 {
	signal := make([]float32, samples)
	phase := 0.0
	for i := 0; i < samples; i++ {
		tm := float64(i) / float64(fs)
		// Light vibrato to avoid exact periodic repetition.
		f0 := 200.0 + 12.0*math.Sin(2*math.Pi*0.5*tm)
		phase += 2 * math.Pi * f0 / float64(fs)
		base := 0.8*math.Sin(phase) + 0.25*math.Sin(2*phase) + 0.1*math.Sin(3*phase)
		signal[i] = float32(base)
	}
	return signal
}

func TestPitchMultiFrameTraceAgainstLibopus(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)
	config := GetBandwidthConfig(BandwidthWideband)
	numSubfr := 4
	subfrLen := config.SubframeSamples
	frameSamples := numSubfr * subfrLen
	fsKHz := config.SampleRate / 1000
	frameLen := (peLTPMemLengthMS + numSubfr*peSubfrLengthMS) * fsKHz
	frames := 50

	signal := generatePitchTraceSignal(frames*frameSamples, config.SampleRate)

	complexity := enc.pitchEstimationComplexity
	if complexity < 0 {
		complexity = 0
	} else if complexity > 2 {
		complexity = 2
	}
	searchThres1 := float64(enc.pitchEstimationThresholdQ16) / 65536.0

	libPrevLag := 0
	libLTPCorr := float32(0)

	var lagMismatch int
	var lagIndexMismatch int
	var contourMismatch int
	var ltpCorrMismatch int

	for frame := 0; frame < frames; frame++ {
		start := frame * frameSamples
		end := start + frameSamples
		pcm := signal[start:end]

		enc.updateShapeBuffer(pcm, frameSamples)
		_, residual32, _, _ := enc.computePitchResidual(numSubfr)
		if len(residual32) < frameLen {
			t.Fatalf("residual too short frame %d: %d < %d", frame, len(residual32), frameLen)
		}

		speechActivityQ8 := 200
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
		}
		if thrhld > 1 {
			thrhld = 1
		}

		goPitchLags, lagIdx, contourIdx := enc.detectPitch(residual32[:frameLen], numSubfr, searchThres1, thrhld)
		goParams := pitchEncodeParams{}
		if enc.pitchState.ltpCorr > 0 {
			goParams = enc.preparePitchLags(append([]int(nil), goPitchLags...), numSubfr, lagIdx, contourIdx)
		}

		lib := libopusPitchAnalysis(residual32[:frameLen], fsKHz, numSubfr, complexity, searchThres1, thrhld, libPrevLag, libLTPCorr)

		mismatch := false
		for i := 0; i < numSubfr; i++ {
			if goPitchLags[i] != lib.Pitch[i] {
				lagMismatch++
				mismatch = true
				break
			}
		}
		if goParams.lagIdx != int(lib.LagIndex) {
			lagIndexMismatch++
			mismatch = true
		}
		if goParams.contourIdx != int(lib.ContourIndex) {
			contourMismatch++
			mismatch = true
		}
		if diff := math.Abs(float64(enc.pitchState.ltpCorr) - float64(lib.LTPCorr)); diff > 5e-2 {
			ltpCorrMismatch++
			mismatch = true
		}

		if mismatch {
			t.Logf("frame %d: goLags=%v libLags=%v", frame, goPitchLags, lib.Pitch[:numSubfr])
			t.Logf("  lagIdx go=%d lib=%d contour go=%d lib=%d ltpCorr go=%.4f lib=%.4f prevLag(lib)=%d",
				goParams.lagIdx, lib.LagIndex, goParams.contourIdx, lib.ContourIndex, enc.pitchState.ltpCorr, lib.LTPCorr, libPrevLag)
		}

		if lib.Voiced {
			libPrevLag = lib.Pitch[numSubfr-1]
		} else {
			libPrevLag = 0
		}
		libLTPCorr = lib.LTPCorr
		enc.isPreviousFrameVoiced = enc.pitchState.ltpCorr > 0
	}

	t.Logf("pitch lag mismatches: %d/%d", lagMismatch, frames)
	t.Logf("lag index mismatches: %d/%d", lagIndexMismatch, frames)
	t.Logf("contour mismatches: %d/%d", contourMismatch, frames)
	t.Logf("ltpCorr mismatches: %d/%d", ltpCorrMismatch, frames)
}

func TestPitchMultiFrameVoicedTraceAgainstLibopus(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)
	config := GetBandwidthConfig(BandwidthWideband)
	numSubfr := 4
	subfrLen := config.SubframeSamples
	frameSamples := numSubfr * subfrLen
	fsKHz := config.SampleRate / 1000
	frameLen := (peLTPMemLengthMS + numSubfr*peSubfrLengthMS) * fsKHz
	frames := 30
	warmupFrames := 3

	signal := generateVoicedPitchTraceSignal(frames*frameSamples, config.SampleRate)

	complexity := enc.pitchEstimationComplexity
	if complexity < 0 {
		complexity = 0
	} else if complexity > 2 {
		complexity = 2
	}
	searchThres1 := float64(enc.pitchEstimationThresholdQ16) / 65536.0

	libPrevLag := 0
	libLTPCorr := float32(0)

	for frame := 0; frame < frames; frame++ {
		start := frame * frameSamples
		end := start + frameSamples
		pcm := signal[start:end]

		enc.updateShapeBuffer(pcm, frameSamples)
		_, residual32, _, _ := enc.computePitchResidual(numSubfr)
		if len(residual32) < frameLen {
			t.Fatalf("residual too short frame %d: %d < %d", frame, len(residual32), frameLen)
		}

		speechActivityQ8 := 255
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
		}
		if thrhld > 1 {
			thrhld = 1
		}

		goPitchLags, lagIdx, contourIdx := enc.detectPitch(residual32[:frameLen], numSubfr, searchThres1, thrhld)
		lib := libopusPitchAnalysis(residual32[:frameLen], fsKHz, numSubfr, complexity, searchThres1, thrhld, libPrevLag, libLTPCorr)

		if lib.Voiced {
			libPrevLag = lib.Pitch[numSubfr-1]
		} else {
			libPrevLag = 0
		}
		libLTPCorr = lib.LTPCorr
		enc.isPreviousFrameVoiced = enc.pitchState.ltpCorr > 0

		if frame < warmupFrames {
			continue
		}

		if !lib.Voiced || enc.pitchState.ltpCorr <= 0 {
			t.Fatalf("frame %d: expected voiced pitch (go=%v lib=%v)", frame, enc.pitchState.ltpCorr > 0, lib.Voiced)
		}

		goParams := enc.preparePitchLags(append([]int(nil), goPitchLags...), numSubfr, lagIdx, contourIdx)
		for i := 0; i < numSubfr; i++ {
			if goPitchLags[i] != lib.Pitch[i] {
				t.Fatalf("frame %d pitchLags[%d] mismatch: go=%d lib=%d",
					frame, i, goPitchLags[i], lib.Pitch[i])
			}
		}
		if goParams.lagIdx != int(lib.LagIndex) {
			t.Fatalf("frame %d lagIndex mismatch: go=%d lib=%d", frame, goParams.lagIdx, lib.LagIndex)
		}
		if goParams.contourIdx != int(lib.ContourIndex) {
			t.Fatalf("frame %d contour mismatch: go=%d lib=%d", frame, goParams.contourIdx, lib.ContourIndex)
		}
		if diff := math.Abs(float64(enc.pitchState.ltpCorr) - float64(lib.LTPCorr)); diff > 5e-2 {
			t.Fatalf("frame %d ltpCorr mismatch: go=%g lib=%g diff=%g", frame, enc.pitchState.ltpCorr, lib.LTPCorr, diff)
		}
	}
}
