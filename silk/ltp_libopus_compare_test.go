//go:build cgo_libopus

package silk

import (
	"math"
	"testing"
)

func TestLTPQuantizationVsLibopus(t *testing.T) {
	fsKHz := 16
	subfrLen := subFrameLengthMs * fsKHz
	nbSubfr := 4
	ltpMem := ltpMemLengthMs * fsKHz
	frameLen := subfrLen * nbSubfr
	laPitch := laPitchMs * fsKHz
	needed := ltpMem + frameLen + laPitch

	residual64 := make([]float64, needed)
	residual32 := make([]float32, needed)
	for i := 0; i < needed; i++ {
		t0 := float64(i) / float64(fsKHz*1000)
		v := 0.35*math.Sin(2*math.Pi*300*t0) +
			0.2*math.Sin(2*math.Pi*620*t0) +
			0.03*float64((i%37)-18)/18.0
		residual64[i] = v
		residual32[i] = float32(v)
	}

	pitchLags := []int{80, 82, 85, 88}
	resStart := ltpMem

	var XX [maxNbSubfr * ltpOrderConst * ltpOrderConst]float64
	var xX [maxNbSubfr * ltpOrderConst]float64
	findLTPFLP(XX[:], xX[:], residual64, resStart, pitchLags, subfrLen, nbSubfr)

	xx32 := make([]float32, nbSubfr*ltpOrderConst*ltpOrderConst)
	xX32 := make([]float32, nbSubfr*ltpOrderConst)
	for i := 0; i < len(xx32); i++ {
		xx32[i] = float32(XX[i])
	}
	for i := 0; i < len(xX32); i++ {
		xX32[i] = float32(xX[i])
	}

	xxLib, xXLib := libopusFindLTP(residual32, resStart, pitchLags, subfrLen, nbSubfr)
	if len(xxLib) != len(xx32) || len(xXLib) != len(xX32) {
		t.Fatalf("libopus LTP sizes mismatch: XX %d vs %d, xX %d vs %d", len(xxLib), len(xx32), len(xXLib), len(xX32))
	}

	const tol = 1e-4
	for i := 0; i < len(xx32); i++ {
		if diff := math.Abs(float64(xxLib[i]) - float64(xx32[i])); diff > tol {
			t.Fatalf("XX[%d] mismatch: go=%g lib=%g diff=%g", i, xx32[i], xxLib[i], diff)
		}
	}
	for i := 0; i < len(xX32); i++ {
		if diff := math.Abs(float64(xXLib[i]) - float64(xX32[i])); diff > tol {
			t.Fatalf("xX[%d] mismatch: go=%g lib=%g diff=%g", i, xX32[i], xXLib[i], diff)
		}
	}

	xxLen := nbSubfr * ltpOrderConst * ltpOrderConst
	xXLen := nbSubfr * ltpOrderConst
	XXQ17 := make([]int32, xxLen)
	xXQ17 := make([]int32, xXLen)
	for i := 0; i < xxLen; i++ {
		XXQ17[i] = float64ToInt32(float64(xx32[i]) * ltpQuantScaleQ17)
	}
	for i := 0; i < xXLen; i++ {
		xXQ17[i] = float64ToInt32(float64(xX32[i]) * ltpQuantScaleQ17)
	}

	var bQ14 [maxNbSubfr * ltpOrderConst]int16
	var cbkIndex [maxNbSubfr]int8
	perIndex := int8(0)
	predGainQ7 := int32(0)
	sumLogGainQ7 := int32(0)
	silkQuantLTPGains(bQ14[:], cbkIndex[:], &perIndex, &sumLogGainQ7, &predGainQ7, XXQ17, xXQ17, subfrLen, nbSubfr)

	libRes := libopusQuantLTP(xx32, xX32, subfrLen, nbSubfr, 0)
	if perIndex != libRes.PerIndex {
		t.Fatalf("perIndex mismatch: go=%d lib=%d", perIndex, libRes.PerIndex)
	}
	if predGainQ7 != libRes.PredGainQ7 {
		t.Fatalf("predGainQ7 mismatch: go=%d lib=%d", predGainQ7, libRes.PredGainQ7)
	}
	if sumLogGainQ7 != libRes.SumLogGainQ7 {
		t.Fatalf("sumLogGainQ7 mismatch: go=%d lib=%d", sumLogGainQ7, libRes.SumLogGainQ7)
	}
	for i := 0; i < nbSubfr; i++ {
		if cbkIndex[i] != libRes.LTPIndex[i] {
			t.Fatalf("LTPIndex[%d] mismatch: go=%d lib=%d", i, cbkIndex[i], libRes.LTPIndex[i])
		}
	}
	for i := 0; i < nbSubfr*ltpOrderConst; i++ {
		if bQ14[i] != libRes.BQ14[i] {
			t.Fatalf("BQ14[%d] mismatch: go=%d lib=%d", i, bQ14[i], libRes.BQ14[i])
		}
	}
}
