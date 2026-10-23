package silk

import (
	"slices"
	"testing"
)

// LBRR and gain-search attempts share the converted frame input, as permitted
// by the const x argument of libopus silk/float/wrappers_FLP.c.
func TestNSQInputReadOnly(t *testing.T) {
	for _, tc := range []struct {
		name    string
		voiced  bool
		delayed bool
	}{
		{"single/unvoiced", false, false},
		{"single/voiced", true, false},
		{"delayed/unvoiced", false, true},
		{"delayed/voiced", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const frameLength = 320
			input := make([]int16, frameLength)
			for i := range input {
				input[i] = int16((i*7919)%20001 - 10000)
			}
			want := slices.Clone(input)
			params := &NSQParams{
				SignalType:             typeUnvoiced,
				PredCoefQ12:            make([]int16, 2*maxLPCOrder),
				NLSFInterpCoefQ2:       4,
				LTPCoefQ14:             make([]int16, 4*ltpOrderConst),
				ARShpQ13:               make([]int16, 4*maxShapeLpcOrder),
				HarmShapeGainQ14:       make([]int32, 4),
				TiltQ14:                make([]int32, 4),
				LFShpQ14:               make([]int32, 4),
				GainsQ16:               []int32{1 << 18, 1 << 18, 1 << 18, 1 << 18},
				PitchL:                 []int32{80, 80, 80, 80},
				LambdaQ10:              512,
				LTPScaleQ14:            12288,
				FrameLength:            frameLength,
				SubfrLength:            80,
				NbSubfr:                4,
				LTPMemLength:           320,
				PredLPCOrder:           maxLPCOrder,
				ShapeLPCOrder:          maxShapeLpcOrder,
				NStatesDelayedDecision: 4,
			}
			if tc.voiced {
				params.SignalType = typeVoiced
				for sf := range 4 {
					params.LTPCoefQ14[sf*ltpOrderConst+2] = 8192
				}
			}
			state := NewNSQState()
			for attempt := range 3 {
				if tc.delayed {
					NoiseShapeQuantizeDelDec(state, input, params)
				} else {
					NoiseShapeQuantize(state, input, params)
				}
				if !slices.Equal(input, want) {
					t.Fatalf("attempt %d modifies shared frame input", attempt)
				}
				for sf := range params.GainsQ16 {
					params.GainsQ16[sf] *= 2
				}
			}
		})
	}
}
