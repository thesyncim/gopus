package silk

import (
	"math/rand"
	"testing"
)

func TestNoiseShapeQuantizerDelDec24States4Pred16SpecializationsMatchGeneric(t *testing.T) {
	rng := rand.New(rand.NewSource(0x24341604))
	r32 := func(amp int32) int32 { return rng.Int31n(2*amp+1) - amp }
	r16 := func(amp int32) int16 { return int16(rng.Int31n(2*amp+1) - amp) }

	cases := [...]struct {
		name       string
		signalType int
	}{
		{name: "voiced", signalType: typeVoiced},
		{name: "unvoiced", signalType: typeUnvoiced},
	}

	for _, tc := range cases {
		for trial := range 200 {
			lengths := [...]int{1, 16, 40, maxSubFrameLength}
			length := lengths[trial%len(lengths)]
			lag := 60 + rng.Intn(120)
			if tc.signalType != typeVoiced && trial%3 == 0 {
				lag = 0
			}
			decisionDelayActive := 1 + rng.Intn(decisionDelay-1)
			frameOffset := decisionDelayActive + rng.Intn(4)
			subfr := rng.Intn(maxNbSubfr)

			var statesGeneric [maxDelDecStates]nsqDelDecState
			for k := range maxDelDecStates {
				st := &statesGeneric[k]
				st.lfARQ14 = r32(1 << 20)
				st.diffQ14 = r32(1 << 20)
				st.seed = int32(rng.Uint32())
				st.seedInit = int32(rng.Uint32())
				st.rdQ10 = r32(1 << 24)
				for i := range st.sLPCQ14 {
					st.sLPCQ14[i] = r32(1 << 22)
				}
				for i := range st.randState {
					st.randState[i] = int32(rng.Uint32())
					st.qQ10[i] = r32(1 << 14)
					st.xqQ14[i] = r32(1 << 22)
					st.predQ15[i] = r32(1 << 22)
					st.shapeQ14[i] = r32(1 << 22)
				}
				for i := range st.sAR2Q14 {
					st.sAR2Q14[i] = r32(1 << 22)
				}
			}
			statesSpecial := statesGeneric

			nsqGeneric := NewNSQState()
			nsqGeneric.sLTPShpBufIdx = ltpMemLength
			nsqGeneric.sLTPBufIdx = ltpMemLength
			for i := range nsqGeneric.sLTPShpQ14 {
				nsqGeneric.sLTPShpQ14[i] = r32(1 << 22)
			}
			nsqSpecial := nsqGeneric.Clone()

			xQ10 := make([]int32, length)
			for i := range xQ10 {
				xQ10[i] = r32(1 << 19)
			}
			sLTPGeneric := make([]int32, ltpMemLength+maxFrameLengthNSQ)
			for i := range sLTPGeneric {
				sLTPGeneric[i] = r32(1 << 22)
			}
			sLTPSpecial := append([]int32(nil), sLTPGeneric...)

			pulsesGeneric := make([]int8, frameOffset+length+decisionDelay)
			pulsesSpecial := make([]int8, len(pulsesGeneric))
			xqGeneric := make([]int16, len(pulsesGeneric))
			xqSpecial := make([]int16, len(pulsesGeneric))
			delayedGainGeneric := make([]int32, decisionDelay)
			for i := range delayedGainGeneric {
				delayedGainGeneric[i] = 1 << 10
			}
			delayedGainSpecial := append([]int32(nil), delayedGainGeneric...)

			var aQ12 [maxLPCOrder]int16
			for i := range aQ12 {
				aQ12[i] = r16(4096)
			}
			var bQ14 [ltpOrderConst]int16
			for i := range bQ14 {
				bQ14[i] = r16(8192)
			}
			var arQ13 [maxShapeLpcOrder]int16
			for i := range arQ13 {
				arQ13[i] = r16(8192)
			}

			harmShapeFIRPackedQ14 := r32(1 << 14)
			tiltQ14 := r32(1 << 14)
			lfShpQ14 := r32(1 << 20)
			gainQ16 := int32(1<<16 + rng.Int31n(1<<20))
			lambdaQ10 := int32(rng.Int31n(60000))
			offsetQ10 := [...]int{offsetVLQ10, offsetVHQ10}[rng.Intn(2)]
			warpingQ16 := int(r32(1 << 14))
			smplGeneric := rng.Intn(decisionDelay)
			smplSpecial := smplGeneric

			noiseShapeQuantizerDelDecGeneric(nsqGeneric, statesGeneric[:], tc.signalType, xQ10, pulsesGeneric, xqGeneric,
				sLTPGeneric, delayedGainGeneric, aQ12[:], bQ14[:], arQ13[:], lag, harmShapeFIRPackedQ14, tiltQ14, lfShpQ14,
				gainQ16, lambdaQ10, offsetQ10, length, subfr, maxShapeLpcOrder, maxLPCOrder, warpingQ16, maxDelDecStates,
				&smplGeneric, decisionDelayActive, frameOffset)
			if tc.signalType == typeVoiced {
				noiseShapeQuantizerDelDec24States4Pred16(nsqSpecial, statesSpecial[:], xQ10, pulsesSpecial, xqSpecial,
					sLTPSpecial, delayedGainSpecial, aQ12[:], bQ14[:], arQ13[:], lag, harmShapeFIRPackedQ14, tiltQ14, lfShpQ14,
					gainQ16, lambdaQ10, offsetQ10, length, subfr, warpingQ16, &smplSpecial, decisionDelayActive, frameOffset)
			} else {
				noiseShapeQuantizerDelDecUnvoiced24States4Pred16(nsqSpecial, statesSpecial[:], xQ10, pulsesSpecial, xqSpecial,
					sLTPSpecial, delayedGainSpecial, aQ12[:], arQ13[:], lag, harmShapeFIRPackedQ14, tiltQ14, lfShpQ14,
					gainQ16, lambdaQ10, offsetQ10, length, subfr, warpingQ16, &smplSpecial, decisionDelayActive, frameOffset)
			}

			if smplSpecial != smplGeneric {
				t.Fatalf("%s trial %d smplBufIdx: got %d want %d", tc.name, trial, smplSpecial, smplGeneric)
			}
			if nsqSpecial.sLTPShpBufIdx != nsqGeneric.sLTPShpBufIdx || nsqSpecial.sLTPBufIdx != nsqGeneric.sLTPBufIdx {
				t.Fatalf("%s trial %d NSQ indices got shp=%d ltp=%d want shp=%d ltp=%d", tc.name, trial,
					nsqSpecial.sLTPShpBufIdx, nsqSpecial.sLTPBufIdx, nsqGeneric.sLTPShpBufIdx, nsqGeneric.sLTPBufIdx)
			}
			if statesSpecial != statesGeneric {
				t.Fatalf("%s trial %d delayed-decision state mismatch", tc.name, trial)
			}
			for i := range nsqGeneric.sLTPShpQ14 {
				if nsqSpecial.sLTPShpQ14[i] != nsqGeneric.sLTPShpQ14[i] {
					t.Fatalf("%s trial %d sLTPShpQ14[%d]: got %d want %d", tc.name, trial, i, nsqSpecial.sLTPShpQ14[i], nsqGeneric.sLTPShpQ14[i])
				}
			}
			for i := range sLTPGeneric {
				if sLTPSpecial[i] != sLTPGeneric[i] {
					t.Fatalf("%s trial %d sLTPQ15[%d]: got %d want %d", tc.name, trial, i, sLTPSpecial[i], sLTPGeneric[i])
				}
			}
			for i := range delayedGainGeneric {
				if delayedGainSpecial[i] != delayedGainGeneric[i] {
					t.Fatalf("%s trial %d delayedGainQ10[%d]: got %d want %d", tc.name, trial, i, delayedGainSpecial[i], delayedGainGeneric[i])
				}
			}
			for i := range pulsesGeneric {
				if pulsesSpecial[i] != pulsesGeneric[i] {
					t.Fatalf("%s trial %d pulses[%d]: got %d want %d", tc.name, trial, i, pulsesSpecial[i], pulsesGeneric[i])
				}
				if xqSpecial[i] != xqGeneric[i] {
					t.Fatalf("%s trial %d xq[%d]: got %d want %d", tc.name, trial, i, xqSpecial[i], xqGeneric[i])
				}
			}
		}
	}
}
