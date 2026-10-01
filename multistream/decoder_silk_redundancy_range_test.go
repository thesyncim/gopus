package multistream

import (
	"encoding/hex"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func testSILKRedundancyTwoFramePacket(t *testing.T) []byte {
	t.Helper()
	frame, err := hex.DecodeString("e1456147e59e3d5223c014e14994368219fc6faa569563dfa98b7b2419fc5daa569563dfa98b7b247d2d5cf1629eaaf9e297034c0c8e")
	if err != nil {
		t.Fatal(err)
	}
	packet := make([]byte, 1+2*len(frame))
	packet[0] = 0x05 // coupled SILK, code 1: two equal-size frames
	copy(packet[1:], frame)
	copy(packet[1+len(frame):], frame)
	return packet
}

func TestMultistreamSILKRedundancyFinalRangeMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	packet := testSILKRedundancyTwoFramePacket(t)
	cases := []libopustest.DecodeDiffCase{
		{Packet: packet, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 960},
		{Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 960},
		{Packet: packet, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 960},
	}
	want, err := libopustest.ProbeDecodeSequence(48000, 2, cases)
	if err != nil {
		t.Fatalf("selected C decoder: %v", err)
	}
	decoder, err := NewDecoder(48000, 2, 1, 1, []byte{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := decoder.FinalRange(); got != 0 {
		t.Fatalf("new decoder final range=%08x, want 0", got)
	}
	for i, decodeCase := range cases {
		got, err := decoder.DecodeToFloat32(decodeCase.Packet, 960)
		if err != nil {
			t.Fatalf("step %d decode: %v", i, err)
		}
		if want[i].Code != 960 || len(got) != 1920 {
			t.Fatalf("step %d samples Go=%d C=%d", i, len(got)/2, want[i].Code)
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
		if gotRange := decoder.FinalRange(); gotRange != want[i].FinalRange {
			t.Fatalf("step %d final range Go=%08x C=%08x", i, gotRange, want[i].FinalRange)
		}
	}
	decoder.Reset()
	if got := decoder.FinalRange(); got != 0 {
		t.Fatalf("reset decoder final range=%08x, want 0", got)
	}
	if _, err := decoder.DecodeToFloat32(packet, 960); err != nil {
		t.Fatalf("post-reset decode: %v", err)
	}
	if got := decoder.FinalRange(); got != want[0].FinalRange {
		t.Fatalf("post-reset final range Go=%08x C=%08x", got, want[0].FinalRange)
	}
}
