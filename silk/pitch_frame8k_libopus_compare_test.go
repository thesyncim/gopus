//go:build cgo_libopus

package silk

import (
	"math"
	"testing"

	cgowrap "github.com/thesyncim/gopus/celt/cgo_test"
)

func TestPitchFrame8kMatchesLibopus(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)
	fsKHz := 16
	numSubfr := 4
	frameLen := (peLTPMemLengthMS + numSubfr*peSubfrLengthMS) * fsKHz

	signal := make([]float32, frameLen)
	for i := range signal {
		tm := float64(i) / float64(fsKHz*1000)
		signal[i] = float32(
			0.6*math.Sin(2*math.Pi*220*tm) +
				0.3*math.Sin(2*math.Pi*440*tm) +
				0.1*math.Sin(2*math.Pi*660*tm),
		)
	}

	frameFix := ensureInt16Slice(&enc.scratchFrame16Fix, frameLen)
	floatToInt16SliceScaled(frameFix, signal, 1.0)

	// Our 8kHz path
	frame8Fix := ensureInt16Slice(&enc.scratchFrame8Fix, frameLen*8/16)
	var st [2]int32
	outLen := resamplerDown2(&st, frame8Fix, frameFix)
	frame8Fix = frame8Fix[:outLen]
	frame8Go := ensureFloat32Slice(&enc.scratchFrame8kHz, len(frame8Fix))
	int16ToFloat32Slice(frame8Go, frame8Fix)

	// Libopus 8kHz path
	frame8Lib := cgowrap.SilkPitchFrame8k(signal, fsKHz, numSubfr)

	if len(frame8Go) != len(frame8Lib) {
		t.Fatalf("length mismatch: go=%d lib=%d", len(frame8Go), len(frame8Lib))
	}
	maxDiff := float32(0)
	maxIdx := -1
	for i := range frame8Go {
		diff := frame8Go[i] - frame8Lib[i]
		if diff < 0 {
			diff = -diff
		}
		if diff > maxDiff {
			maxDiff = diff
			maxIdx = i
		}
	}
	if maxDiff > 0 {
		t.Fatalf("frame8k mismatch maxDiff=%g at %d: go=%g lib=%g", maxDiff, maxIdx, frame8Go[maxIdx], frame8Lib[maxIdx])
	}
}
