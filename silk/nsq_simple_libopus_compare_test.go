//go:build cgo_libopus

package silk

import (
	"testing"

	cgowrap "github.com/thesyncim/gopus/celt/cgo_test"
)

func TestNSQSimpleMatchesLibopus(t *testing.T) {
	const (
		frameLength   = 320
		subfrLength   = 80
		nbSubfr       = 4
		ltpMem        = 320
		predLPCOrder  = 16
		shapeLPCOrder = 16
		lambdaQ10     = 1024
		ltpScaleQ14   = 16384
		quantOffset   = 0
		nlsfInterpQ2  = 2
		seed          = 1
	)

	cases := []struct {
		name       string
		signalType int
		constGain  bool
		zeroShp    bool
		zeroLTP    bool
		fixedLag   int
	}{
		{name: "voiced", signalType: typeVoiced},
		{name: "voiced_const_gain", signalType: typeVoiced, constGain: true},
		{name: "voiced_zero_shaping", signalType: typeVoiced, zeroShp: true},
		{name: "voiced_zero_ltp", signalType: typeVoiced, zeroLTP: true},
		{name: "unvoiced", signalType: typeUnvoiced},
		{name: "voiced_low_lag", signalType: typeVoiced, fixedLag: 32},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rng := nsqLCG{state: 42}

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

			// Run Go NSQ
			nsq := NewNSQState()
			nsqInit := nsq.Clone()

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
				LTPMemLength:           ltpMem,
				PredLPCOrder:           predLPCOrder,
				ShapeLPCOrder:          shapeLPCOrder,
				WarpingQ16:             0,
				NStatesDelayedDecision: 1,
				Seed:                   seed,
			}

			pulsesGo, _ := NoiseShapeQuantize(nsq, x16, params)

			// Run libopus NSQ
			pulsesLib, xqLib, finalState := cgowrap.SilkNSQSimpleWithState(
				frameLength, subfrLength, nbSubfr, ltpMem,
				predLPCOrder, shapeLPCOrder,
				tc.signalType, quantOffset, nlsfInterpQ2, seed,
				x16, predCoefQ12, ltpCoefQ14, arShpQ13,
				harmShapeGainQ14, tiltQ14, lfShpQ14, gainsQ16, pitchL,
				lambdaQ10, ltpScaleQ14,
				nsqInit.xq[:], nsqInit.sLTPShpQ14[:], nsqInit.sLPCQ14[:], nsqInit.sAR2Q14[:],
				nsqInit.sLFARShpQ14, nsqInit.sDiffShpQ14,
				nsqInit.lagPrev, nsqInit.sLTPBufIdx, nsqInit.sLTPShpBufIdx,
				nsqInit.randSeed, nsqInit.prevGainQ16, nsqInit.rewhiteFlag,
			)
			_ = xqLib

			if len(pulsesGo) != len(pulsesLib) {
				t.Fatalf("pulse length mismatch: go=%d lib=%d", len(pulsesGo), len(pulsesLib))
			}

			mismatchCount := 0
			firstMismatch := -1
			for i := 0; i < frameLength; i++ {
				if pulsesGo[i] != pulsesLib[i] {
					mismatchCount++
					if firstMismatch < 0 {
						firstMismatch = i
					}
				}
			}
			if mismatchCount > 0 {
				t.Fatalf("pulse mismatch: %d/%d samples differ (first at %d: go=%d lib=%d)",
					mismatchCount, frameLength, firstMismatch, pulsesGo[firstMismatch], pulsesLib[firstMismatch])
			}

			// Check final state
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
			if nsq.prevGainQ16 != finalState.PrevGainQ16 {
				t.Fatalf("final state prevGainQ16 mismatch: go=%d lib=%d", nsq.prevGainQ16, finalState.PrevGainQ16)
			}

			t.Logf("PASS: %d pulses match exactly", frameLength)
		})
	}
}
