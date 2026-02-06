//go:build cgo_libopus

package silk

import (
	"math"
	"testing"

	cgowrap "github.com/thesyncim/gopus/celt/cgo_test"
)

type stage2EvalGoResult struct {
	CCmaxNew float32
	CBimax   int
}

func stage2EvalGo(frame []float32, fsKHz, nbSubfr, complexity, d int) stage2EvalGoResult {
	if len(frame) == 0 || nbSubfr <= 0 {
		return stage2EvalGoResult{}
	}
	frame8kHz := stage2Frame8kGo(frame, fsKHz, nbSubfr)
	if len(frame8kHz) == 0 {
		return stage2EvalGoResult{}
	}

	nbCbkSearch := peNbCbksStage2
	if fsKHz == 8 && complexity > 0 {
		nbCbkSearch = peNbCbksStage2Ext
	}

	sfLength8kHz := peSubfrLengthMS * 8
	targetStart := peLTPMemLengthMS * 8

	best := float32(-1000.0)
	cbimax := 0
	for j := 0; j < nbCbkSearch; j++ {
		cc := float32(0)
		for k := 0; k < nbSubfr; k++ {
			targetIdx := targetStart + k*sfLength8kHz
			if targetIdx+sfLength8kHz > len(frame8kHz) {
				break
			}
			target := frame8kHz[targetIdx : targetIdx+sfLength8kHz]
			energyTmp := energyFLP(target) + 1.0
			lag := d + int(pitchCBLagsStage2[k][j])
			basisIdx := targetIdx - lag
			if basisIdx < 0 || basisIdx+sfLength8kHz > len(frame8kHz) {
				continue
			}
			basis := frame8kHz[basisIdx : basisIdx+sfLength8kHz]
			crossCorr := innerProductFLP(basis, target, sfLength8kHz)
			if crossCorr > 0 {
				energy := energyFLP(basis)
				cc += float32(2.0 * crossCorr / (energy + energyTmp))
			}
		}
		if cc > best {
			best = cc
			cbimax = j
		}
	}

	return stage2EvalGoResult{CCmaxNew: best, CBimax: cbimax}
}

func stage2Frame8kGo(frame []float32, fsKHz, nbSubfr int) []float32 {
	if len(frame) == 0 || nbSubfr <= 0 {
		return nil
	}
	frameLen := (peLTPMemLengthMS + nbSubfr*peSubfrLengthMS) * fsKHz
	if frameLen > len(frame) {
		frameLen = len(frame)
	}
	frameFix := make([]int16, frameLen)
	floatToInt16SliceScaled(frameFix, frame[:frameLen], 1.0)

	frameLen8k := (peLTPMemLengthMS + nbSubfr*peSubfrLengthMS) * 8
	var frame8Fix []int16
	switch fsKHz {
	case 16:
		frame8Fix = make([]int16, frameLen8k)
		var st [2]int32
		outLen := resamplerDown2(&st, frame8Fix, frameFix)
		frame8Fix = frame8Fix[:outLen]
	case 12:
		frame8Fix = make([]int16, frameLen8k)
		var st [6]int32
		scratch := make([]int32, frameLen+4)
		outLen := resamplerDown2_3(&st, frame8Fix, frameFix, scratch)
		frame8Fix = frame8Fix[:outLen]
	default:
		frame8Fix = frameFix
	}

	frame8kHz := make([]float32, len(frame8Fix))
	int16ToFloat32Slice(frame8kHz, frame8Fix)
	if len(frame8kHz) > frameLen8k {
		frame8kHz = frame8kHz[:frameLen8k]
	}
	return frame8kHz
}

func TestPitchAnalysisMatchesLibopus(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)
	fsKHz := 16
	numSubfr := 4
	frameLen := (peLTPMemLengthMS + numSubfr*peSubfrLengthMS) * fsKHz
	laShape := laShapeMs * fsKHz
	bufLen := frameLen + laShape

	signal := make([]float32, bufLen)
	for i := range signal {
		tm := float64(i) / float64(fsKHz*1000)
		signal[i] = float32(
			0.6*math.Sin(2*math.Pi*220*tm) +
				0.3*math.Sin(2*math.Pi*440*tm) +
				0.1*math.Sin(2*math.Pi*660*tm),
		)
	}
	enc.inputBuffer = signal
	enc.pitchState.prevLag = 0
	enc.pitchState.ltpCorr = 0

	_, residual32, _, _ := enc.computePitchResidual(numSubfr)
	if len(residual32) < frameLen {
		t.Fatalf("residual too short: %d < %d", len(residual32), frameLen)
	}

	complexity := enc.pitchEstimationComplexity
	if complexity < 0 {
		complexity = 0
	} else if complexity > 2 {
		complexity = 2
	}

	searchThres1 := 0.8 - 0.5*float64(complexity)/2.0
	searchThres2 := 0.4 - 0.25*float64(complexity)/2.0

	pitchLags, lagIdx, contourIdx := enc.detectPitch(residual32, numSubfr, searchThres1, searchThres2)
	if len(pitchLags) != numSubfr {
		t.Fatalf("expected %d pitch lags, got %d", numSubfr, len(pitchLags))
	}

	pitchParams := enc.preparePitchLags(pitchLags, numSubfr, lagIdx, contourIdx)

	lib := libopusPitchAnalysis(residual32[:frameLen], fsKHz, numSubfr, complexity, searchThres1, searchThres2, 0, 0)
	if !lib.Voiced {
		t.Fatalf("libopus pitch analysis returned unvoiced")
	}
	stage2d35 := cgowrap.SilkPitchStage2Eval(residual32[:frameLen], fsKHz, numSubfr, complexity, 35)
	stage2d36 := cgowrap.SilkPitchStage2Eval(residual32[:frameLen], fsKHz, numSubfr, complexity, 36)

	contribLib := cgowrap.SilkPitchStage2Contrib(residual32[:frameLen], fsKHz, numSubfr, complexity, 35, stage2d35.CBimax)
	contribGo := stage2ContribGo(residual32[:frameLen], fsKHz, numSubfr, 35, stage2d35.CBimax)
	for i := 0; i < numSubfr; i++ {
		if contribGo.EnergyTmp[i] != contribLib.EnergyTmp[i] ||
			contribGo.Energy[i] != contribLib.Energy[i] ||
			contribGo.Xcorr[i] != contribLib.Xcorr[i] {
			t.Fatalf("stage2 contrib mismatch sf=%d: go tmp=%g energy=%g xcorr=%g | lib tmp=%g energy=%g xcorr=%g",
				i, contribGo.EnergyTmp[i], contribGo.Energy[i], contribGo.Xcorr[i],
				contribLib.EnergyTmp[i], contribLib.Energy[i], contribLib.Xcorr[i])
		}
	}
	if stage2d35.CCmaxNew != stage2d35.CCmaxNew || stage2d36.CCmaxNew != stage2d36.CCmaxNew {
		t.Fatalf("stage2 CCmaxNew is NaN")
	}
	stage2go35 := stage2EvalGo(residual32[:frameLen], fsKHz, numSubfr, complexity, 35)
	stage2go36 := stage2EvalGo(residual32[:frameLen], fsKHz, numSubfr, complexity, 36)
	t.Logf("go stage2: d=35 CCmaxNew=%.6f cbimax=%d; d=36 CCmaxNew=%.6f cbimax=%d",
		stage2go35.CCmaxNew, stage2go35.CBimax, stage2go36.CCmaxNew, stage2go36.CBimax)
	frame8Go := stage2Frame8kGo(residual32[:frameLen], fsKHz, numSubfr)
	frame8Lib := cgowrap.SilkPitchStage2Frame8kHz(residual32[:frameLen], fsKHz, numSubfr)
	t.Logf("frame8 len: go=%d lib=%d", len(frame8Go), len(frame8Lib))
	if len(frame8Go) != len(frame8Lib) {
		t.Fatalf("frame8 length mismatch: go=%d lib=%d", len(frame8Go), len(frame8Lib))
	}
	if len(frame8Go) > 0 {
		const maxAllowedDiff = float32(1.0 / 512.0)
		maxDiff := float32(0)
		maxIdx := -1
		for i := 0; i < len(frame8Go); i++ {
			diff := frame8Go[i] - frame8Lib[i]
			if diff < 0 {
				diff = -diff
			}
			if diff > maxDiff {
				maxDiff = diff
				maxIdx = i
			}
		}
		if maxIdx >= 0 {
			t.Logf("frame8k max diff=%.6f at %d (go=%.6f lib=%.6f)", maxDiff, maxIdx, frame8Go[maxIdx], frame8Lib[maxIdx])
		}
		if maxDiff > maxAllowedDiff {
			t.Fatalf("frame8k mismatch too large: maxDiff=%.6f allowed=%.6f idx=%d go=%.6f lib=%.6f",
				maxDiff, maxAllowedDiff, maxIdx, frame8Go[maxIdx], frame8Lib[maxIdx])
		}
	}

	for i := 0; i < numSubfr; i++ {
		if pitchLags[i] != lib.Pitch[i] {
			t.Fatalf("pitchLags[%d] mismatch: go=%d lib=%d", i, pitchLags[i], lib.Pitch[i])
		}
	}
	if pitchParams.lagIdx != int(lib.LagIndex) {
		t.Fatalf("lagIndex mismatch: go=%d lib=%d", pitchParams.lagIdx, lib.LagIndex)
	}
	if pitchParams.contourIdx != int(lib.ContourIndex) {
		t.Fatalf("contourIndex mismatch: go=%d lib=%d", pitchParams.contourIdx, lib.ContourIndex)
	}
	if diff := math.Abs(float64(enc.pitchState.ltpCorr) - float64(lib.LTPCorr)); diff > 5e-2 {
		t.Fatalf("ltpCorr mismatch: go=%g lib=%g diff=%g", enc.pitchState.ltpCorr, lib.LTPCorr, diff)
	}
}

func stage2ContribGo(frame []float32, fsKHz, nbSubfr, d, cbimax int) cgowrap.Stage2ContribResult {
	frameLen := (peLTPMemLengthMS + nbSubfr*peSubfrLengthMS) * fsKHz
	frameFix := make([]int16, frameLen)
	floatToInt16SliceScaled(frameFix, frame[:frameLen], 1.0)

	var frame8Fix []int16
	if fsKHz == 16 {
		frame8Fix = make([]int16, frameLen*8/16)
		var st [2]int32
		outLen := resamplerDown2(&st, frame8Fix, frameFix)
		frame8Fix = frame8Fix[:outLen]
	} else if fsKHz == 12 {
		frame8Fix = make([]int16, frameLen*8/12)
		var st [6]int32
		scratch := make([]int32, frameLen+4)
		outLen := resamplerDown2_3(&st, frame8Fix, frameFix, scratch)
		frame8Fix = frame8Fix[:outLen]
	} else {
		frame8Fix = frameFix
	}

	frame8 := make([]float32, len(frame8Fix))
	int16ToFloat32Slice(frame8, frame8Fix)

	cbTable := pitchCBLagsStage2Slice
	if nbSubfr != peMaxNbSubfr {
		cbTable = pitchCBLagsStage210msSlice
	}

	sfLength8k := peSubfrLengthMS * 8
	targetStart := peLTPMemLengthMS * 8
	energyTmp := make([]float32, nbSubfr)
	energy := make([]float32, nbSubfr)
	xcorr := make([]float32, nbSubfr)

	for k := 0; k < nbSubfr; k++ {
		targetIdx := targetStart + k*sfLength8k
		target := frame8[targetIdx : targetIdx+sfLength8k]
		energyTmp[k] = float32(energyFLP(target) + 1.0)
		lag := d + int(cbTable[k][cbimax])
		basisIdx := targetIdx - lag
		basis := frame8[basisIdx : basisIdx+sfLength8k]
		xcorr[k] = float32(innerProductFLP(basis, target, sfLength8k))
		energy[k] = float32(energyFLP(basis))
	}

	return cgowrap.Stage2ContribResult{
		EnergyTmp: energyTmp,
		Energy:    energy,
		Xcorr:     xcorr,
	}
}
