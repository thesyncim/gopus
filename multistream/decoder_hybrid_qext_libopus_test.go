//go:build gopus_qext && !gopus_dred && !gopus_osce

package multistream

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const hybridQEXTFrameSize = 960

func selectedHybridQEXTSequence(t *testing.T, format uint32) ([][]byte, []libopustest.DecodeDiffResult) {
	t.Helper()
	pcm := make([]float32, hybridQEXTFrameSize)
	for i := range pcm {
		time := float64(i) / 48000
		pcm[i] = float32(0.28*math.Sin(2*math.Pi*173*time) +
			0.17*math.Sin(2*math.Pi*347*time+0.13) +
			0.09*math.Sin(2*math.Pi*521*time+0.29))
	}
	encoded, err := libopustest.ProbeEncodeDiff(libopustest.EncodeDiffParams{
		SampleRate:    48000,
		Channels:      1,
		Application:   2049,
		ForceMode:     1001,
		Bandwidth:     1105,
		Bitrate:       32000,
		Complexity:    10,
		Signal:        3001,
		VBR:           true,
		ForceChannels: 1,
		FrameSize:     hybridQEXTFrameSize,
		FrameCount:    1,
		PCM:           pcm,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "selected C Hybrid QEXT packet", err)
		return nil, nil
	}
	if len(encoded) != 1 || len(encoded[0].Packet) < 2 {
		t.Fatalf("C encoder returned %d records or a short packet", len(encoded))
	}
	base := encoded[0].Packet
	parsed, err := parseOpusPacket(base, false)
	if err != nil {
		t.Fatalf("parse selected C Hybrid packet: %v", err)
	}
	if len(parsed.frames) != 1 || parseStreamTOC(base[0]).mode != streamModeHybrid {
		t.Fatalf("selected C packet is not one Hybrid frame: TOC=%+v frames=%d", parseStreamTOC(base[0]), len(parsed.frames))
	}
	qext := make([]byte, 32)
	packet := make([]byte, len(base)+len(qext)+128)
	n, err := buildOpusPacketFromFramesAndExtensions(
		parsed.tocBase,
		parsed.frames,
		[]packetExtensionData{{ID: qextPacketExtensionID, Frame: 0, Data: qext}},
		false,
		packet,
	)
	if err != nil {
		t.Fatalf("build Hybrid QEXT packet: %v", err)
	}
	packet = packet[:n]
	malformed := append([]byte(nil), packet...)
	malformed[1] &= 0xc0 // Code-3 packets require a nonzero frame count.
	packets := [][]byte{packet, nil, malformed, base, packet}
	cases := make([]libopustest.DecodeDiffCase, len(packets))
	for i, input := range packets {
		cases[i] = libopustest.DecodeDiffCase{
			Packet:    input,
			Format:    format,
			FrameSize: hybridQEXTFrameSize,
		}
	}
	want, err := libopustest.ProbeDecodeSequence(48000, 1, cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "selected C Hybrid QEXT decode sequence", err)
		return nil, nil
	}
	if len(want) != len(cases) {
		t.Fatalf("selected C returned %d sequence records, want %d", len(want), len(cases))
	}
	if want[2].Code >= 0 {
		t.Fatalf("malformed code-3 packet returned C status %d", want[2].Code)
	}
	return packets, want
}

func TestHybridQEXTPayloadMatchesSelectedLibopus(t *testing.T) {
	packets, want := selectedHybridQEXTSequence(t, libopustest.DecodeDiffFormatFloat32)
	decoder, err := NewDecoder(48000, 1, 1, 0, []byte{0})
	if err != nil {
		t.Fatal(err)
	}
	for i, packet := range packets {
		got, err := decoder.DecodeToFloat32(packet, hybridQEXTFrameSize)
		if want[i].Code < 0 {
			if err == nil {
				t.Fatalf("step %d malformed packet returned %d samples without an error", i, len(got))
			}
			if gotRange := decoder.FinalRange(); gotRange != want[i].FinalRange {
				t.Fatalf("step %d malformed final range Go=%08x C=%08x", i, gotRange, want[i].FinalRange)
			}
			continue
		}
		if err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		pcm := want[i].Float32()
		if int32(len(got)) != want[i].Code || len(got) != len(pcm) {
			t.Fatalf("step %d sample count Go=%d C=%d", i, len(got), want[i].Code)
		}
		for sample := range got {
			if math.Float32bits(got[sample]) != math.Float32bits(pcm[sample]) {
				t.Fatalf("step %d sample %d Go=%08x C=%08x", i, sample, math.Float32bits(got[sample]), math.Float32bits(pcm[sample]))
			}
		}
		if gotRange := decoder.FinalRange(); gotRange != want[i].FinalRange {
			t.Fatalf("step %d final range Go=%08x C=%08x", i, gotRange, want[i].FinalRange)
		}
	}
}

func TestHybridQEXTDecodeIntoWarmZeroAllocs(t *testing.T) {
	packets, _ := selectedHybridQEXTSequence(t, libopustest.DecodeDiffFormatFloat32)
	decoder, err := NewDecoder(48000, 1, 1, 0, []byte{0})
	if err != nil {
		t.Fatal(err)
	}
	output := make([]float32, hybridQEXTFrameSize)
	sequence := func() bool {
		for _, index := range []int{0, 3} {
			n, err := decoder.DecodeIntoFloat32(packets[index], output, hybridQEXTFrameSize)
			if err != nil || n != hybridQEXTFrameSize {
				return false
			}
		}
		return true
	}
	for range 3 {
		if !sequence() {
			t.Fatal("warm Hybrid QEXT decode sequence failed")
		}
	}
	failed := false
	allocs := testing.AllocsPerRun(20, func() {
		if !sequence() {
			failed = true
		}
	})
	if failed {
		t.Fatal("Hybrid QEXT decode sequence failed during allocation measurement")
	}
	if allocs != 0 {
		t.Fatalf("warm Hybrid QEXT DecodeIntoFloat32 allocs/op=%.2f, want 0", allocs)
	}
}
