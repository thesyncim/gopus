//go:build cgo_libopus

package silk

import (
	"math"
	"testing"

	cgowrap "github.com/thesyncim/gopus/celt/cgo_test"
)

func TestComputeResidualEnergiesAgainstLibopus(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)
	order := enc.lpcOrder
	const (
		numSubframes    = 4
		subframeSamples = 80
	)
	shift := subframeSamples + order

	state := uint32(11)
	next := func() uint32 {
		state = state*1664525 + 1013904223
		return state
	}

	for tc := 0; tc < 300; tc++ {
		ltpRes := make([]float32, numSubframes*shift)
		for i := range ltpRes {
			ltpRes[i] = (float32(int32(next()%65536)-32768) / 32768.0) * 0.8
		}

		predCoefQ12 := make([]int16, 2*maxLPCOrder)
		for i := 0; i < order; i++ {
			predCoefQ12[i] = int16(int32(next()%8000) - 4000)
			predCoefQ12[maxLPCOrder+i] = int16(int32(next()%8000) - 4000)
		}

		gains := make([]float32, numSubframes)
		for i := range gains {
			gains[i] = 1.0 + float32(next()%30000)
		}

		interpIdx := int(next() % 5)
		goRes := enc.computeResidualEnergies(ltpRes, predCoefQ12, interpIdx, gains, numSubframes, subframeSamples)

		a0 := make([]float32, order)
		a1 := make([]float32, order)
		for i := 0; i < order; i++ {
			a1[i] = float32(predCoefQ12[maxLPCOrder+i]) / 4096.0
		}
		if interpIdx < 4 {
			for i := 0; i < order; i++ {
				a0[i] = float32(predCoefQ12[i]) / 4096.0
			}
		} else {
			copy(a0, a1)
		}

		libRes, ok := cgowrap.SilkResidualEnergyFLP(ltpRes, a0, a1, gains, subframeSamples, numSubframes, order)
		if !ok {
			t.Fatalf("case %d: failed to run libopus residual energy wrapper", tc)
		}

		for i := 0; i < numSubframes; i++ {
			diff := math.Abs(float64(libRes[i]) - goRes[i])
			den := math.Abs(float64(libRes[i]))
			if den < 1.0 {
				den = 1.0
			}
			rel := diff / den
			if diff > 64.0 && rel > 1e-6 {
				t.Fatalf("case %d subfr %d: residual energy mismatch: go=%.6f lib=%.6f diff=%.6f rel=%.9f interpIdx=%d", tc, i, goRes[i], libRes[i], diff, rel, interpIdx)
			}
		}
	}
}
