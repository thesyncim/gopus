//go:build cgo_libopus

package silk

import (
	"testing"

	cgowrap "github.com/thesyncim/gopus/celt/cgo_test"
)

type nsqLCG struct {
	state uint32
}

func (l *nsqLCG) next() uint32 {
	l.state = l.state*1664525 + 1013904223
	return l.state
}

func randRange(l *nsqLCG, min, max int32) int32 {
	if max <= min {
		return min
	}
	span := uint32(max - min + 1)
	return int32(l.next()%span) + min
}

func fillInt16(l *nsqLCG, dst []int16, min, max int32) {
	for i := range dst {
		dst[i] = int16(randRange(l, min, max))
	}
}

func fillInt32(l *nsqLCG, dst []int32, min, max int32) {
	for i := range dst {
		dst[i] = randRange(l, min, max)
	}
}

func fillInt(l *nsqLCG, dst []int, min, max int32) {
	for i := range dst {
		dst[i] = int(randRange(l, min, max))
	}
}

func TestNSQDelDecMatchesLibopus(t *testing.T) {
	const (
		frameLength   = 320
		subfrLength   = 80
		nbSubfr       = 4
		ltpMemLength  = 320
		predLPCOrder  = 16
		shapeLPCOrder = 16
		lambdaQ10     = 2048
		ltpScaleQ14   = 16384
		quantOffset   = 0
		nlsfInterpQ2  = 2
		seed          = 1
	)

	cases := []struct {
		name       string
		nStates    int
		warpingQ16 int
		signalType int
		constGain  bool
		zeroShp    bool
		zeroLTP    bool
		fixedLag   int
	}{
		{name: "voiced_nstates1", nStates: 1, warpingQ16: 0, signalType: typeVoiced},
		{name: "voiced_nstates2", nStates: 2, warpingQ16: 0, signalType: typeVoiced},
		{name: "voiced_low_lag_decdelay", nStates: 4, warpingQ16: 15728, signalType: typeVoiced, fixedLag: 32},
		{name: "voiced_const_gain", nStates: 1, warpingQ16: 0, signalType: typeVoiced, constGain: true},
		{name: "voiced_zero_shaping", nStates: 1, warpingQ16: 0, signalType: typeVoiced, zeroShp: true},
		{name: "voiced_zero_ltp", nStates: 1, warpingQ16: 0, signalType: typeVoiced, zeroLTP: true},
		{name: "unvoiced_warped", nStates: 1, warpingQ16: 8192, signalType: typeUnvoiced},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rng := nsqLCG{state: 1}
			x16 := make([]int16, frameLength)
			fillInt16(&rng, x16, -12000, 12000)

			predCoefQ12 := make([]int16, 2*maxLPCOrder)
			fillInt16(&rng, predCoefQ12, -3000, 3000)

			ltpCoefQ14 := make([]int16, nbSubfr*ltpOrderConst)
			if tc.signalType == typeVoiced && !tc.zeroLTP {
				fillInt16(&rng, ltpCoefQ14, -2000, 2000)
			}

			arShpQ13 := make([]int16, nbSubfr*maxShapeLpcOrder)
			if !tc.zeroShp {
				fillInt16(&rng, arShpQ13, -1500, 1500)
			}

			harmShapeGainQ14 := make([]int, nbSubfr)
			if !tc.zeroShp {
				fillInt(&rng, harmShapeGainQ14, 0, 3000)
			}

			tiltQ14 := make([]int, nbSubfr)
			if !tc.zeroShp {
				fillInt(&rng, tiltQ14, -1500, 1500)
			}

			lfShpQ14 := make([]int32, nbSubfr)
			if !tc.zeroShp {
				fillInt32(&rng, lfShpQ14, -800, 800)
			}

			gainsQ16 := make([]int32, nbSubfr)
			if tc.constGain {
				for i := range gainsQ16 {
					gainsQ16[i] = 40000
				}
			} else {
				for i := range gainsQ16 {
					gainsQ16[i] = 30000 + int32(rng.next()%20000)
					if gainsQ16[i] <= 0 {
						gainsQ16[i] = 1
					}
				}
			}

			pitchL := make([]int, nbSubfr)
			if tc.signalType == typeVoiced {
				for i := range pitchL {
					if tc.fixedLag > 0 {
						pitchL[i] = tc.fixedLag
					} else {
						pitchL[i] = 64 + int(rng.next()%8)
					}
				}
			}

			params := &NSQParams{
				SignalType:             tc.signalType,
				QuantOffsetType:        quantOffset,
				PredCoefQ12:            predCoefQ12,
				NLSFInterpCoefQ2:       nlsfInterpQ2,
				LTPCoefQ14:             ltpCoefQ14,
				ARShpQ13:               arShpQ13,
				HarmShapeGainQ14:       harmShapeGainQ14,
				TiltQ14:                tiltQ14,
				LFShpQ14:               lfShpQ14,
				GainsQ16:               gainsQ16,
				PitchL:                 pitchL,
				LambdaQ10:              lambdaQ10,
				LTPScaleQ14:            ltpScaleQ14,
				FrameLength:            frameLength,
				SubfrLength:            subfrLength,
				NbSubfr:                nbSubfr,
				LTPMemLength:           ltpMemLength,
				PredLPCOrder:           predLPCOrder,
				ShapeLPCOrder:          shapeLPCOrder,
				WarpingQ16:             tc.warpingQ16,
				NStatesDelayedDecision: tc.nStates,
				Seed:                   seed,
			}

			nsq := NewNSQState()
			nsqInit := nsq.Clone()
			debugSLTP := make([]int32, ltpMemLength+frameLength)
			debugSLTPRaw := make([]int16, ltpMemLength+frameLength)
			setNSQDelDecDebugSLTP(debugSLTP)
			setNSQDelDecDebugSLTPRaw(debugSLTPRaw)
			setNSQDelDecDebugScale(2, 252)
			pulsesGo, xqGo, seedGo := NoiseShapeQuantizeDelDec(nsq, x16, params)
			setNSQDelDecDebugSLTP(nil)
			setNSQDelDecDebugSLTPRaw(nil)
			scaleHit := nsqDelDecDebugScaleHit
			scaleInv := nsqDelDecDebugScaleInv
			scaleSLTP := nsqDelDecDebugScaleSLTP
			scaleOut := nsqDelDecDebugScaleOut
			scaleGain := nsqDelDecDebugScaleGain
			setNSQDelDecDebugScale(-1, -1)

			pulsesLib, xqLib, seedLib, sLTPLib, sLTPLibRaw, _, finalState := cgowrap.SilkNSQDelDecCaptureWithStateFinal(
				frameLength, subfrLength, nbSubfr, ltpMemLength,
				predLPCOrder, shapeLPCOrder, tc.warpingQ16, tc.nStates,
				tc.signalType, quantOffset, nlsfInterpQ2, seed,
				x16, predCoefQ12, ltpCoefQ14, arShpQ13,
				harmShapeGainQ14, tiltQ14, lfShpQ14, gainsQ16, pitchL,
				lambdaQ10, ltpScaleQ14,
				nsqInit.xq[:], nsqInit.sLTPShpQ14[:], nsqInit.sLPCQ14[:], nsqInit.sAR2Q14[:],
				nsqInit.sLFARShpQ14, nsqInit.sDiffShpQ14,
				nsqInit.lagPrev, nsqInit.sLTPBufIdx, nsqInit.sLTPShpBufIdx,
				nsqInit.randSeed, nsqInit.prevGainQ16, nsqInit.rewhiteFlag,
			)

			if len(pulsesGo) != len(pulsesLib) {
				t.Fatalf("pulse length mismatch: go=%d lib=%d", len(pulsesGo), len(pulsesLib))
			}
			if len(xqGo) != len(xqLib) {
				t.Fatalf("xq length mismatch: go=%d lib=%d", len(xqGo), len(xqLib))
			}

			for i := 0; i < frameLength; i++ {
				if pulsesGo[i] != pulsesLib[i] {
					t.Fatalf("pulse[%d] mismatch: go=%d lib=%d", i, pulsesGo[i], pulsesLib[i])
				}
			}

			if len(debugSLTP) != len(sLTPLib) {
				t.Fatalf("sLTP_Q15 length mismatch: go=%d lib=%d", len(debugSLTP), len(sLTPLib))
			}
			diffCount := 0
			firstIdx := -1
			lastIdx := -1
			diffMinus1 := 0
			diffPlus1 := 0
			diffOther := 0
			for i := 0; i < len(debugSLTP); i++ {
				if debugSLTP[i] != sLTPLib[i] {
					diffCount++
					if firstIdx == -1 {
						firstIdx = i
					}
					lastIdx = i
					diff := debugSLTP[i] - sLTPLib[i]
					if diff == -1 {
						diffMinus1++
					} else if diff == 1 {
						diffPlus1++
					} else {
						diffOther++
					}
				}
			}
			if diffCount > 0 {
				first := -1
				for i := 0; i < len(debugSLTP); i++ {
					if debugSLTP[i] != sLTPLib[i] {
						first = i
						break
					}
				}
				if first < 0 {
					t.Fatalf("sLTP_Q15 mismatch count=%d -1=%d +1=%d other=%d", diffCount, diffMinus1, diffPlus1, diffOther)
				}
				i := first
				lag0 := 0
				start0 := 0
				if len(pitchL) > 0 {
					lag0 = pitchL[0]
					start0 = ltpMemLength - lag0 - ltpOrderConst/2
				}
				lag2 := 0
				start2 := 0
				if len(pitchL) > 2 {
					lag2 = pitchL[2]
					start2 = ltpMemLength - lag2 - ltpOrderConst/2
				}
				rawGo := int16(0)
				rawLib := int16(0)
				if i < len(debugSLTPRaw) {
					rawGo = debugSLTPRaw[i]
				}
				if i < len(sLTPLibRaw) {
					rawLib = sLTPLibRaw[i]
				}
				gain0 := int32(0)
				if len(gainsQ16) > 0 {
					gain0 = gainsQ16[0]
				}
				gain2 := int32(0)
				if len(gainsQ16) > 2 {
					gain2 = gainsQ16[2]
				}
				invGain0 := int32(0)
				invGain0Lib := int32(0)
				if len(gainsQ16) > 0 {
					invGain0 = silk_INVERSE32_varQ(silk_max(gainsQ16[0], 1), 47)
					invGain0Lib = cgowrap.SilkInverse32VarQ(silk_max(gainsQ16[0], 1), 47)
					if tc.signalType == typeVoiced && start0 > 0 {
						invGain0 = silk_LSHIFT32(silk_SMULWB(invGain0, int32(ltpScaleQ14)), 2)
						invGain0Lib = silk_LSHIFT32(silk_SMULWB(invGain0Lib, int32(ltpScaleQ14)), 2)
					}
				}
				invGain2 := int32(0)
				invGain2Lib := int32(0)
				if len(gainsQ16) > 2 {
					invGain2 = silk_INVERSE32_varQ(silk_max(gainsQ16[2], 1), 47)
					invGain2Lib = cgowrap.SilkInverse32VarQ(silk_max(gainsQ16[2], 1), 47)
				}
				predGo0 := silk_SMULWB(invGain0, int32(rawGo))
				predLib0 := silk_SMULWB(invGain0Lib, int32(rawLib))
				predGo2 := silk_SMULWB(invGain2, int32(rawGo))
				predLib2 := silk_SMULWB(invGain2Lib, int32(rawLib))
				predLib2CGO := cgowrap.SilkSMULWB(invGain2Lib, int32(rawLib))
				invGainCheck := int32(0)
				if scaleGain != 0 {
					invGainCheck = silk_INVERSE32_varQ(scaleGain, 47)
				}
				t.Fatalf("sLTP_Q15 mismatch at %d: go=%d lib=%d raw_go=%d raw_lib=%d lag0=%d start0=%d gain0=%d invGain0=%d invGain0Lib=%d pred0_go=%d pred0_lib=%d lag2=%d start2=%d gain2=%d invGain2=%d invGain2Lib=%d pred2_go=%d pred2_lib=%d pred2_lib_cgo=%d diffStats(total=%d -1=%d +1=%d other=%d first=%d last=%d scale_hit=%t scale_gain=%d scale_inv=%d scale_inv_check=%d scale_sltp=%d scale_out=%d",
					i, debugSLTP[i], sLTPLib[i], rawGo, rawLib,
					lag0, start0, gain0, invGain0, invGain0Lib, predGo0, predLib0,
					lag2, start2, gain2, invGain2, invGain2Lib, predGo2, predLib2, predLib2CGO,
					diffCount, diffMinus1, diffPlus1, diffOther, firstIdx, lastIdx,
					scaleHit, scaleGain, scaleInv, invGainCheck, scaleSLTP, scaleOut)
			}
			maxDiff := 0
			maxIdx := -1
			for i := 0; i < frameLength; i++ {
				diff := int(xqGo[i]) - int(xqLib[i])
				if diff < 0 {
					diff = -diff
				}
				if diff > maxDiff {
					maxDiff = diff
					maxIdx = i
				}
			}
			if maxDiff > 2 {
				t.Fatalf("xq max diff %d at %d: go=%d lib=%d", maxDiff, maxIdx, xqGo[maxIdx], xqLib[maxIdx])
			}

			if seedGo != seedLib {
				t.Fatalf("seed mismatch: go=%d lib=%d", seedGo, seedLib)
			}

			if idx := firstInt16Mismatch(nsq.xq[:], finalState.XQ); idx >= 0 {
				t.Fatalf("final state xq mismatch at %d: go=%d lib=%d", idx, nsq.xq[idx], finalState.XQ[idx])
			}
			if idx := firstInt32Mismatch(nsq.sLTPShpQ14[:], finalState.SLTPShpQ14); idx >= 0 {
				t.Fatalf("final state sLTP_shp mismatch at %d: go=%d lib=%d", idx, nsq.sLTPShpQ14[idx], finalState.SLTPShpQ14[idx])
			}
			if idx := firstInt32Mismatch(nsq.sLPCQ14[:], finalState.SLPCQ14); idx >= 0 {
				t.Fatalf("final state sLPC mismatch at %d: go=%d lib=%d", idx, nsq.sLPCQ14[idx], finalState.SLPCQ14[idx])
			}
			if idx := firstInt32Mismatch(nsq.sAR2Q14[:], finalState.SAR2Q14); idx >= 0 {
				t.Fatalf("final state sAR2 mismatch at %d: go=%d lib=%d", idx, nsq.sAR2Q14[idx], finalState.SAR2Q14[idx])
			}
			if nsq.sLFARShpQ14 != finalState.LFARQ14 || nsq.sDiffShpQ14 != finalState.DiffQ14 {
				t.Fatalf("final state shaping scalar mismatch: lfAR go=%d lib=%d diff go=%d lib=%d",
					nsq.sLFARShpQ14, finalState.LFARQ14, nsq.sDiffShpQ14, finalState.DiffQ14)
			}
			if nsq.lagPrev != finalState.LagPrev || nsq.sLTPBufIdx != finalState.SLTPBufIdx || nsq.sLTPShpBufIdx != finalState.SLTPShpBufIdx {
				t.Fatalf("final state index mismatch: lagPrev go=%d lib=%d sLTPBufIdx go=%d lib=%d sLTPShpBufIdx go=%d lib=%d",
					nsq.lagPrev, finalState.LagPrev, nsq.sLTPBufIdx, finalState.SLTPBufIdx, nsq.sLTPShpBufIdx, finalState.SLTPShpBufIdx)
			}
			if nsq.randSeed != finalState.RandSeed || nsq.prevGainQ16 != finalState.PrevGainQ16 || nsq.rewhiteFlag != finalState.RewhiteFlag {
				t.Fatalf("final state flags mismatch: randSeed go=%d lib=%d prevGain go=%d lib=%d rewhite go=%d lib=%d",
					nsq.randSeed, finalState.RandSeed, nsq.prevGainQ16, finalState.PrevGainQ16, nsq.rewhiteFlag, finalState.RewhiteFlag)
			}
		})
	}
}

func firstInt16Mismatch(a, b []int16) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return n
	}
	return -1
}

func firstInt32Mismatch(a, b []int32) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return n
	}
	return -1
}

func TestRewhitenLTPMatchesLibopus(t *testing.T) {
	rng := nsqLCG{state: 7}

	const (
		frameLength  = 320
		ltpMemLength = 320
		order        = 16
	)

	xq := make([]int16, ltpMemLength+frameLength)
	fillInt16(&rng, xq, -20000, 20000)

	offsets := []int{0, 160}

	aQ12 := make([]int16, order)
	fillInt16(&rng, aQ12, -3000, 3000)

	for _, startIdx := range []int{0, 238} {
		if startIdx < 0 || startIdx >= ltpMemLength {
			t.Fatalf("invalid startIdx=%d", startIdx)
		}
		length := ltpMemLength - startIdx
		if length < order {
			t.Fatalf("invalid length for startIdx=%d", startIdx)
		}
		for _, offset := range offsets {
			if startIdx+offset+length > len(xq) {
				t.Fatalf("invalid offset=%d", offset)
			}
			sLTP := make([]int16, ltpMemLength+frameLength)
			rewhitenLTP(sLTP, xq, startIdx, offset, aQ12, length, order)

			libOut := cgowrap.SilkLPCAnalysisFilter(xq[startIdx+offset:], aQ12, length, order)

			for i := 0; i < length; i++ {
				got := sLTP[startIdx+i]
				want := libOut[i]
				if got != want {
					t.Fatalf("rewhiten mismatch (start=%d offset=%d) at %d: go=%d lib=%d", startIdx, offset, i, got, want)
				}
			}
		}
	}
}
