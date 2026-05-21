//go:build gopus_extra_controls

package multistream

import (
	"testing"

	"github.com/thesyncim/gopus/silk"
)

func TestStreamOSCEFloatToInt16MatchesLibopusScaleOutput(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   float32
		want int16
	}{
		{name: "positive clamp", in: 1.5, want: 32767},
		{name: "negative clamp", in: -1.5, want: -32767},
		{name: "negative full scale", in: -1.0, want: -32767},
		{name: "half tie to even", in: float32(0.5 / 32768.0), want: 0},
		{name: "one point five tie to even", in: float32(1.5 / 32768.0), want: 2},
		{name: "two point five tie to even", in: float32(2.5 / 32768.0), want: 2},
		{name: "negative one point five tie to even", in: float32(-1.5 / 32768.0), want: -2},
	} {
		if got := streamOSCEFloatToInt16(tc.in); got != tc.want {
			t.Fatalf("%s: streamOSCEFloatToInt16(%g)=%d want %d", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestStreamOSCELACEComplexityMode(t *testing.T) {
	for _, tc := range []struct {
		complexity int
		want       streamOSCELACEMode
	}{
		{complexity: 5, want: streamOSCELACEModeNone},
		{complexity: 6, want: streamOSCELACEModeLACE},
		{complexity: 7, want: streamOSCELACEModeNoLACE},
		{complexity: 10, want: streamOSCELACEModeNoLACE},
	} {
		if got := pickStreamOSCELACEMode(tc.complexity); got != tc.want {
			t.Fatalf("pickStreamOSCELACEMode(%d)=%v want %v", tc.complexity, got, tc.want)
		}
	}
}

func TestStreamOSCELACEOutputResetMatchesLibopusSequence(t *testing.T) {
	var state streamOSCEState
	state.laceResetFrames[0] = 2
	for i := 0; i < streamOSCELACEFrameSamples; i++ {
		state.laceApplyInF[i] = -0.25
		state.laceApplyOutF[i] = 0.75
	}
	state.applyOSCELACEOutputReset(0)
	if state.laceResetFrames[0] != 1 {
		t.Fatalf("after raw reset frame countdown=%d want 1", state.laceResetFrames[0])
	}
	for i := 0; i < streamOSCELACEFrameSamples; i++ {
		if got := state.laceApplyOutF[i]; got != state.laceApplyInF[i] {
			t.Fatalf("raw reset frame sample %d=%g want %g", i, got, state.laceApplyInF[i])
		}
		state.laceApplyOutF[i] = 0.75
	}
	state.applyOSCELACEOutputReset(0)
	if state.laceResetFrames[0] != 0 {
		t.Fatalf("after cross-fade reset frame countdown=%d want 0", state.laceResetFrames[0])
	}
	firstWant := streamOSCEWindow[0]*0.75 + (1.0-streamOSCEWindow[0])*(-0.25)
	if got := state.laceApplyOutF[0]; got != firstWant {
		t.Fatalf("cross-fade sample 0=%g want %g", got, firstWant)
	}
	if got := state.laceApplyOutF[159]; got == 0.75 || got == -0.25 {
		t.Fatalf("cross-fade sample 159=%g, want blended value", got)
	}
	if got := state.laceApplyOutF[160]; got != 0.75 {
		t.Fatalf("cross-fade touched trailing sample 160=%g want 0.75", got)
	}
}

func TestStreamOSCEInactiveMarkClearsNonSILKState(t *testing.T) {
	st := &streamState{
		channels:   2,
		sampleRate: 48000,
		streamOSCEFields: streamOSCEFields{
			osceState: &streamOSCEState{
				prevLACEActive: true,
				prevBWEActive:  true,
				laceMethod:     streamOSCELACEModeLACE,
			},
		},
	}
	st.osceState.laceResetFrames[0] = 2
	st.osceState.laceResetFrames[1] = 2

	st.markOSCEInactiveIfModeIneligible(streamTOC{mode: streamModeCELT, bandwidth: 4, stereo: true}, make([]float64, 960*2), 960)

	if st.osceState.prevLACEActive {
		t.Fatal("CELT transition left LACE active")
	}
	if st.osceState.prevBWEActive {
		t.Fatal("CELT transition left BWE active")
	}
	if st.osceState.laceMethod != streamOSCELACEModeNone {
		t.Fatalf("laceMethod=%v want none", st.osceState.laceMethod)
	}
}

func TestStreamOSCEInactiveMarkKeepsSILKWBState(t *testing.T) {
	st := &streamState{
		channels:   1,
		sampleRate: 48000,
		streamOSCEFields: streamOSCEFields{
			osceState: &streamOSCEState{
				prevLACEActive: true,
				prevBWEActive:  true,
				laceMethod:     streamOSCELACEModeLACE,
			},
		},
	}

	st.markOSCEInactiveIfModeIneligible(streamTOC{mode: streamModeSILK, bandwidth: 2}, make([]float64, 960), 960)

	if !st.osceState.prevLACEActive {
		t.Fatal("SILK WB transition unexpectedly cleared LACE")
	}
	if !st.osceState.prevBWEActive {
		t.Fatal("SILK WB transition unexpectedly cleared BWE")
	}
}

func TestStreamOSCEPLCSilkResetsLACEAndClearsInactiveBWE(t *testing.T) {
	st := &streamState{
		channels:   1,
		sampleRate: 48000,
		streamOSCEFields: streamOSCEFields{
			osceLACEEnabled: true,
			osceBWEEnabled:  true,
			osceState: &streamOSCEState{
				prevLACEActive: true,
				prevBWEActive:  true,
				laceMethod:     streamOSCELACEModeLACE,
			},
		},
	}

	st.applyOSCEPLCSilk(make([]float32, 960), 960, silk.BandwidthWideband, false)

	if st.osceState.prevLACEActive {
		t.Fatal("SILK WB PLC left LACE active")
	}
	if st.osceState.prevBWEActive {
		t.Fatal("SILK WB PLC without model left BWE active")
	}
}
