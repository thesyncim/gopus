//go:build cgo_libopus

package silk

import (
	"math"
	"testing"
)

func generateWidebandTestSignal(samples int) []float32 {
	signal := make([]float32, samples)
	freqs := []float64{220, 440, 880, 1320}
	amp := 0.2
	for i := 0; i < samples; i++ {
		tm := float64(i) / 16000.0
		var v float64
		for _, f := range freqs {
			v += amp * math.Sin(2*math.Pi*f*tm)
		}
		signal[i] = float32(v)
	}
	return signal
}

// TestLTPQuantizationTraceAgainstLibopus compares LTP quantization inputs/outputs
// against libopus for a multi-frame wideband signal. This is a diagnostic trace.
func TestLTPQuantizationTraceAgainstLibopus(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)
	config := GetBandwidthConfig(BandwidthWideband)
	numSubframes := 4
	subfrLen := config.SubframeSamples
	frameSamples := numSubframes * subfrLen
	frames := 50

	signal := generateWidebandTestSignal(frames * frameSamples)

	var mismatchFrames int
	for frame := 0; frame < frames; frame++ {
		start := frame * frameSamples
		end := start + frameSamples
		pcm := signal[start:end]

		enc.updateShapeBuffer(pcm, frameSamples)

		residual64, residual32, resStart, _ := enc.computePitchResidual(numSubframes)
		if len(residual32) == 0 {
			t.Fatalf("missing residual frame %d", frame)
		}

		searchThres1 := float64(enc.pitchEstimationThresholdQ16) / 65536.0
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

		pitchLags, _, _ := enc.detectPitch(residual32, numSubframes, searchThres1, thrhld)
		if len(pitchLags) != numSubframes {
			t.Fatalf("unexpected pitch lag count frame %d: %d", frame, len(pitchLags))
		}

		sumLogGainQ7 := enc.sumLogGainQ7
		_, goIdx, goPer, goPredGain := enc.analyzeLTPQuantized(
			residual64, resStart, pitchLags, numSubframes, subfrLen,
		)

		xxLib, xXLib := libopusFindLTP(residual32, resStart, pitchLags, subfrLen, numSubframes)
		if len(xxLib) == 0 || len(xXLib) == 0 {
			t.Fatalf("libopus findLTP failed at frame %d", frame)
		}
		libRes := libopusQuantLTP(xxLib, xXLib, subfrLen, numSubframes, sumLogGainQ7)

		mismatch := false
		if int8(goPer) != libRes.PerIndex || goPredGain != libRes.PredGainQ7 {
			mismatch = true
		}
		for i := 0; i < numSubframes; i++ {
			if goIdx[i] != libRes.LTPIndex[i] {
				mismatch = true
				break
			}
		}

		if mismatch {
			mismatchFrames++
			maxXXDiff := 0.0
			maxXDiff := 0.0
			var xxGo [maxNbSubfr * ltpOrderConst * ltpOrderConst]float64
			var xXGo [maxNbSubfr * ltpOrderConst]float64
			findLTPFLP(xxGo[:], xXGo[:], residual64, resStart, pitchLags, subfrLen, numSubframes)
			for i := 0; i < numSubframes*ltpOrderConst*ltpOrderConst; i++ {
				diff := math.Abs(xxGo[i] - float64(xxLib[i]))
				if diff > maxXXDiff {
					maxXXDiff = diff
				}
			}
			for i := 0; i < numSubframes*ltpOrderConst; i++ {
				diff := math.Abs(xXGo[i] - float64(xXLib[i]))
				if diff > maxXDiff {
					maxXDiff = diff
				}
			}
			t.Logf("frame %d: PER go=%d lib=%d predGainQ7 go=%d lib=%d max|XX|=%.3g max|xX|=%.3g",
				frame, goPer, libRes.PerIndex, goPredGain, libRes.PredGainQ7, maxXXDiff, maxXDiff)
			t.Logf("  ltpIdx go=%v lib=%v", goIdx[:numSubframes], libRes.LTPIndex[:numSubframes])
			t.Logf("  pitchLags=%v sumLogGainQ7=%d", pitchLags, sumLogGainQ7)
		}
		enc.isPreviousFrameVoiced = enc.pitchState.ltpCorr > 0
	}

	t.Logf("LTP quant mismatches: %d/%d", mismatchFrames, frames)
	_ = enc
}
