package multistream

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestMultistreamHybridRedundancyFinalRangeMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	ref := projectionDecodeRef(t, 9, 480, 2, 256000)
	first, err := parseMultistreamPacket(ref.packets[0], ref.streams)
	if err != nil {
		t.Fatal(err)
	}
	second, err := parseMultistreamPacket(ref.packets[1], ref.streams)
	if err != nil {
		t.Fatal(err)
	}
	const stream = 4
	if len(first) <= stream || len(second) <= stream || parseStreamTOC(second[stream][0]).mode != streamModeHybrid {
		t.Fatal("selected projection packet does not contain the Hybrid stream")
	}
	channels := streamChannels(stream, ref.coupledStreams)
	if channels != 1 {
		t.Fatalf("Hybrid stream channels=%d, want 1", channels)
	}
	cases := []libopustest.DecodeDiffCase{
		{Packet: first[stream], Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 480},
		{Packet: second[stream], Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 480},
		{Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 480},
	}
	want, err := libopustest.ProbeDecodeSequence(48000, channels, cases)
	if err != nil {
		t.Fatalf("selected C decoder: %v", err)
	}
	decoder, err := NewDecoder(48000, channels, 1, 0, []byte{0})
	if err != nil {
		t.Fatal(err)
	}
	for i, decodeCase := range cases {
		got, err := decoder.DecodeToFloat32(decodeCase.Packet, 480)
		if err != nil {
			t.Fatalf("step %d decode: %v", i, err)
		}
		if want[i].Code != 480 || len(got) != 480 {
			t.Fatalf("step %d samples Go=%d C=%d", i, len(got), want[i].Code)
		}
		wantPCM := want[i].Float32()
		if len(wantPCM) != len(got) {
			t.Fatalf("step %d PCM length Go=%d C=%d", i, len(got), len(wantPCM))
		}
		for sample, value := range got {
			if math.Float32bits(value) != math.Float32bits(wantPCM[sample]) {
				t.Fatalf("step %d PCM sample %d Go=%08x C=%08x", i, sample, math.Float32bits(value), math.Float32bits(wantPCM[sample]))
			}
		}
		if i == 1 && decoder.decoders[0].(*streamState).hybridDec.FinalRange() == want[i].FinalRange {
			t.Fatal("Hybrid packet did not exercise a redundant range contribution")
		}
		if gotRange := decoder.FinalRange(); gotRange != want[i].FinalRange {
			t.Fatalf("step %d final range Go=%08x C=%08x", i, gotRange, want[i].FinalRange)
		}
	}
	decoder.Reset()
	if got := decoder.FinalRange(); got != 0 {
		t.Fatalf("reset decoder final range=%08x, want 0", got)
	}
}
