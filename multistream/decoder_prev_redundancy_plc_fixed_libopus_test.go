//go:build gopus_fixed_point

package multistream

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestFixedPLCUsesCELTAfterRedundancySequence(t *testing.T) {
	packets := encodePrevRedundancyPLCSequence(t)
	sequence := [][]byte{packets[0], packets[1], packets[2], nil, nil, packets[3]}
	want, err := decodeWithLibopusReferencePacketsInt24Gain(1, prevRedundancySampleRate, 1, 1, 0,
		prevRedundancyFrameSize, 0, []byte{0}, nil, sequence)
	if err != nil {
		libopustest.HelperUnavailable(t, "fixed CELT redundancy PLC sequence", err)
	}

	dec, err := NewDecoder(prevRedundancySampleRate, 1, 1, 0, []byte{0})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]int32, 0, len(want))
	for frame, packet := range sequence {
		out, err := dec.DecodeToInt24(packet, prevRedundancyFrameSize)
		if err != nil {
			t.Fatalf("frame %d DecodeToInt24: %v", frame, err)
		}
		if frame == 2 {
			assertPreviousRedundancySelectsCELT(t, dec)
			st := dec.decoders[0].(*streamState)
			if !st.fixedHybridRedundant || st.fixedHybridRedundantToSilk || len(st.fixedHybridRedundantData) <= 1 {
				t.Fatalf("frame 2 fixed redundancy state active=%v toSilk=%v bytes=%d",
					st.fixedHybridRedundant, st.fixedHybridRedundantToSilk, len(st.fixedHybridRedundantData))
			}
		}
		if frame == 3 {
			assertPLCConsumedPreviousRedundancy(t, dec)
		}
		got = append(got, out...)
	}
	if len(got) != len(want) {
		t.Fatalf("int24 sample count Go=%d C=%d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("sample %d Go=%d C=%d", i, got[i], want[i])
		}
	}

	allocDec, err := NewDecoder(prevRedundancySampleRate, 1, 1, 0, []byte{0})
	if err != nil {
		t.Fatal(err)
	}
	decodeCycle := func() {
		for frame, packet := range sequence {
			if _, handled, err := allocDec.DecodeToResFixed(packet, prevRedundancyFrameSize); err != nil || !handled {
				t.Fatalf("allocation cycle frame %d DecodeToResFixed=(handled=%v, err=%v)", frame, handled, err)
			}
		}
	}
	decodeCycle()
	if allocs := testing.AllocsPerRun(10, decodeCycle); allocs != 0 {
		t.Fatalf("warm CELT redundancy PLC allocations=%g, want 0", allocs)
	}
}
