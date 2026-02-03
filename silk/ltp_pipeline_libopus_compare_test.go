//go:build cgo_libopus

package silk

import (
	"math"
	"testing"
)

func TestLTPQuantizationPipelineMatchesLibopus(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)
	config := GetBandwidthConfig(BandwidthWideband)
	numSubframes := 4
	subfrLen := config.SubframeSamples
	frameSamples := numSubframes * subfrLen

	pcm := make([]float32, frameSamples)
	for i := range pcm {
		tm := float64(i) / float64(config.SampleRate)
		pcm[i] = float32(
			0.6*math.Sin(2*math.Pi*220*tm) +
				0.3*math.Sin(2*math.Pi*440*tm) +
				0.1*math.Sin(2*math.Pi*880*tm),
		)
	}

	enc.updateShapeBuffer(pcm, frameSamples)

	residual64, residual32, resStart, _ := enc.computePitchResidual(numSubframes)
	if len(residual32) == 0 {
		t.Fatalf("missing pitch residual")
	}

	searchThres1 := float64(enc.pitchEstimationThresholdQ16) / 65536.0
	thrhld := 0.6 - 0.004*float64(enc.pitchEstimationLPCOrder) - 0.1*200.0/256.0
	if thrhld < 0 {
		thrhld = 0
	}
	if thrhld > 1 {
		thrhld = 1
	}

	pitchLags := enc.detectPitch(residual32, numSubframes, searchThres1, thrhld)
	if len(pitchLags) != numSubframes {
		t.Fatalf("unexpected pitch lag count: %d", len(pitchLags))
	}

	ltpCoeffs, ltpIndices, perIndex, predGainQ7 := enc.analyzeLTPQuantized(residual64, resStart, pitchLags, numSubframes, subfrLen)

	xxLib, xXLib := libopusFindLTP(residual32, resStart, pitchLags, subfrLen, numSubframes)
	if len(xxLib) == 0 || len(xXLib) == 0 {
		t.Fatalf("libopus findLTP failed")
	}
	libRes := libopusQuantLTP(xxLib, xXLib, subfrLen, numSubframes, 0)

	if int8(perIndex) != libRes.PerIndex {
		t.Fatalf("PER index mismatch: go=%d lib=%d", perIndex, libRes.PerIndex)
	}
	if predGainQ7 != libRes.PredGainQ7 {
		t.Fatalf("predGainQ7 mismatch: go=%d lib=%d", predGainQ7, libRes.PredGainQ7)
	}
	for i := 0; i < numSubframes; i++ {
		if ltpIndices[i] != libRes.LTPIndex[i] {
			t.Fatalf("LTP index[%d] mismatch: go=%d lib=%d", i, ltpIndices[i], libRes.LTPIndex[i])
		}
	}
	for i := 0; i < numSubframes*ltpOrderConst; i++ {
		goQ14 := int16(ltpCoeffs[i/ltpOrderConst][i%ltpOrderConst]) << 7
		if goQ14 != libRes.BQ14[i] {
			t.Fatalf("LTP coeff[%d] mismatch: go=%d lib=%d", i, goQ14, libRes.BQ14[i])
		}
	}
}
