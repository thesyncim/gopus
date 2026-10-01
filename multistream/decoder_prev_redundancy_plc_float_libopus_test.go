//go:build !gopus_fixed_point

package multistream

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestFloatPLCUsesCELTAfterRedundancySequence(t *testing.T) {
	packets := encodePrevRedundancyPLCSequence(t)
	sequence := [][]byte{packets[0], packets[1], packets[2], nil, nil, packets[3]}
	want, err := decodeWithLibopusReferencePackets(1, prevRedundancySampleRate, 1, 1, 0,
		prevRedundancyFrameSize, []byte{0}, nil, sequence)
	if err != nil {
		libopustest.HelperUnavailable(t, "float CELT redundancy PLC sequence", err)
	}

	dec, err := NewDecoder(prevRedundancySampleRate, 1, 1, 0, []byte{0})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]float32, 0, len(want))
	for frame, packet := range sequence {
		out, err := dec.DecodeToFloat32(packet, prevRedundancyFrameSize)
		if err != nil {
			t.Fatalf("frame %d DecodeToFloat32: %v", frame, err)
		}
		if frame == 2 {
			assertPreviousRedundancySelectsCELT(t, dec)
		}
		if frame == 3 {
			assertPLCConsumedPreviousRedundancy(t, dec)
		}
		got = append(got, out...)
	}
	if len(got) != len(want) {
		t.Fatalf("float sample count Go=%d C=%d", len(got), len(want))
	}
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("sample %d Go=%08x C=%08x (%g vs %g)", i,
				math.Float32bits(got[i]), math.Float32bits(want[i]), got[i], want[i])
		}
	}

	allocDec, err := NewDecoder(prevRedundancySampleRate, 1, 1, 0, []byte{0})
	if err != nil {
		t.Fatal(err)
	}
	output := make([]float32, prevRedundancyFrameSize)
	decodeCycle := func() {
		for frame, packet := range sequence {
			if n, err := allocDec.DecodeIntoFloat32(packet, output, prevRedundancyFrameSize); err != nil || n != prevRedundancyFrameSize {
				t.Fatalf("allocation cycle frame %d DecodeIntoFloat32=(%d,%v)", frame, n, err)
			}
		}
	}
	decodeCycle()
	if allocs := testing.AllocsPerRun(10, decodeCycle); allocs != 0 {
		t.Fatalf("warm CELT redundancy PLC allocations=%g, want 0", allocs)
	}
}
