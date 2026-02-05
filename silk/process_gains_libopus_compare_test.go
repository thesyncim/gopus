//go:build cgo_libopus

package silk

import (
	"testing"

	cgowrap "github.com/thesyncim/gopus/celt/cgo_test"
)

func TestApplyGainProcessingAndQuantAgainstLibopus(t *testing.T) {
	const nbSubfr = 4
	const subfrLength = 80

	state := uint32(7)
	next := func() uint32 {
		state = state*1664525 + 1013904223
		return state
	}

	for i := 0; i < 500; i++ {
		signalType := typeUnvoiced
		if (next() & 1) == 0 {
			signalType = typeVoiced
		}
		conditional := (next() & 1) == 0
		lastGainIn := int8(next() % nLevelsQGain)
		predGainQ7 := int32(next() % (35 * 128))
		inputTiltQ15 := int(int32(next()%65536) - 32768)
		snrDBQ7 := int(10*128 + next()%(35*128))
		speechQ8 := int(next() % 256)
		nStates := int(1 + next()%4)

		gainsIn := make([]float32, nbSubfr)
		resNrgIn := make([]float32, nbSubfr)
		for k := 0; k < nbSubfr; k++ {
			gainsIn[k] = 1.0 + float32(next()%30000)
			resNrgIn[k] = float32(next()%2000000000) / 16.0
		}

		gainsGo := append([]float32(nil), gainsIn...)
		resNrgGo := make([]float64, nbSubfr)
		for k := 0; k < nbSubfr; k++ {
			resNrgGo[k] = float64(resNrgIn[k])
		}
		quantOffsetGo := applyGainProcessing(gainsGo, resNrgGo, predGainQ7, snrDBQ7, signalType, inputTiltQ15, subfrLength)

		gainsQ16Go := make([]int32, nbSubfr)
		for k := 0; k < nbSubfr; k++ {
			gainsQ16Go[k] = int32(gainsGo[k] * 65536.0)
		}
		unqGo := append([]int32(nil), gainsQ16Go...)
		indicesGo := make([]int8, nbSubfr)
		prevOutGo := silkGainsQuantInto(indicesGo, gainsQ16Go, lastGainIn, conditional, nbSubfr)

		snap, ok := cgowrap.SilkProcessGainsFLP(
			gainsIn, resNrgIn, nbSubfr, subfrLength, signalType,
			float32(predGainQ7)/128.0, inputTiltQ15, snrDBQ7, speechQ8,
			nStates, lastGainIn, conditional,
		)
		if !ok {
			t.Fatalf("case %d: failed to run libopus process_gains wrapper", i)
		}

		if quantOffsetGo != snap.QuantOffsetType {
			t.Fatalf("case %d: quantOffset mismatch: go=%d lib=%d signalType=%d predGainQ7=%d inputTilt=%d", i, quantOffsetGo, snap.QuantOffsetType, signalType, predGainQ7, inputTiltQ15)
		}

		for k := 0; k < nbSubfr; k++ {
			if indicesGo[k] != snap.GainsIndices[k] {
				t.Fatalf("case %d subfr %d: gain index mismatch: go=%d lib=%d unqGo=%v unqLib=%v", i, k, indicesGo[k], snap.GainsIndices[k], unqGo, snap.GainsUnqQ16)
			}
		}
		if prevOutGo != snap.LastGainIndexOut {
			t.Fatalf("case %d: lastGain mismatch: go=%d lib=%d indicesGo=%v indicesLib=%v unqGo=%v unqLib=%v", i, prevOutGo, snap.LastGainIndexOut, indicesGo, snap.GainsIndices, unqGo, snap.GainsUnqQ16)
		}
	}
}
