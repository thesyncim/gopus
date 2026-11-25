//go:build gopus_qext && !gopus_dred && !gopus_osce

package multistream

import (
	"github.com/thesyncim/gopus/internal/libopustest"
	"math"
	"testing"
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

func native96kMultistreamPCM(channels, frames int) []float32 {
	pcm := make([]float32, 1920*frames*channels)
	for i := 0; i < 1920*frames; i++ {
		t := float64(i) / 96000
		left := 0.30*math.Sin(2*math.Pi*6000*t) + 0.25*math.Sin(2*math.Pi*30000*t)
		pcm[i*channels] = float32(left)
		if channels == 2 {
			right := 0.27*math.Sin(2*math.Pi*7400*t+0.21) + 0.18*math.Sin(2*math.Pi*28000*t+0.63)
			pcm[i*channels+1] = float32(right)
		}
	}
	return pcm
}

func TestQEXTMultistreamDecoderNative96kMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(map[int]string{1: "mono", 2: "stereo"}[channels], func(t *testing.T) {
			const frameSize = 1920
			encoded, err := libopustest.ProbeQEXTEncode96k(libopustest.QEXTEncode96kParams{
				Channels: channels, FrameSize: frameSize, Bitrate: 320000,
				Complexity: 10, VBR: false, MaxPacketSize: 4000,
				PCM: native96kMultistreamPCM(channels, 3), FrameCount: 3,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "native 96 kHz QEXT encode", err)
			}
			if len(encoded.Packets) != 3 {
				t.Fatalf("native 96 kHz encoder returned %d packets, want 3", len(encoded.Packets))
			}
			sequence := [][]byte{encoded.Packets[0], encoded.Packets[1], nil, encoded.Packets[2]}

			coupled := 0
			mapping := []byte{0}
			if channels == 2 {
				coupled = 1
				mapping = []byte{0, 1}
			}
			wantMultistreamFloat, err := decodeWithLibopusReferencePackets(
				1, 96000, channels, 1, coupled, frameSize, mapping, nil, sequence,
			)
			if err != nil {
				libopustest.HelperUnavailable(t, "native 96 kHz multistream decode", err)
			}
			wantDirectFloat, err := probeMultistreamNative96kReference(libopustest.QEXTDecode96kParams{
				SampleFormat: libopustest.QEXTDecode96kFormatFloat32,
				Channels:     channels, SampleRate: 96000, MaxFrameSize: frameSize,
				Packets: sequence,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "native 96 kHz decoder range", err)
			}
			if len(wantDirectFloat.FinalRanges) != len(sequence) {
				t.Fatalf("direct C final ranges=%d, want %d", len(wantDirectFloat.FinalRanges), len(sequence))
			}
			for i := range wantMultistreamFloat {
				if gotBits, wantBits := math.Float32bits(wantMultistreamFloat[i]), math.Float32bits(wantDirectFloat.PCM[i]); gotBits != wantBits {
					t.Fatalf("C multistream/direct PCM[%d]=%08x/%08x", i, gotBits, wantBits)
				}
			}

			dec, err := NewDecoder(96000, channels, 1, coupled, mapping)
			if err != nil {
				t.Fatalf("NewDecoder(96000): %v", err)
			}
			got := make([]float32, 0, len(wantMultistreamFloat))
			for frame, packet := range sequence {
				pcm, err := dec.DecodeToFloat32(packet, frameSize)
				if err != nil {
					t.Fatalf("frame %d DecodeToFloat32: %v", frame, err)
				}
				if len(pcm) != frameSize*channels {
					t.Fatalf("frame %d samples=%d, want %d", frame, len(pcm), frameSize*channels)
				}
				got = append(got, pcm...)
				if gotRange, wantRange := dec.FinalRange(), wantDirectFloat.FinalRanges[frame]; gotRange != wantRange {
					t.Fatalf("frame %d final range=%08x, want C %08x", frame, gotRange, wantRange)
				}
			}
			for i := range got {
				if gotBits, wantBits := math.Float32bits(got[i]), math.Float32bits(wantMultistreamFloat[i]); gotBits != wantBits {
					t.Fatalf("PCM[%d]=%08x, want C multistream %08x", i, gotBits, wantBits)
				}
			}

			wantMultistreamInt16, err := decodeWithLibopusReferencePacketsInt16Gain(
				1, 96000, channels, 1, coupled, frameSize, 0, mapping, nil, sequence,
			)
			if err != nil {
				libopustest.HelperUnavailable(t, "native 96 kHz multistream int16 decode", err)
			}
			wantDirectInt16, err := probeMultistreamNative96kReference(libopustest.QEXTDecode96kParams{
				SampleFormat: libopustest.QEXTDecode96kFormatInt16,
				Channels:     channels, SampleRate: 96000, MaxFrameSize: frameSize, Packets: sequence,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "native 96 kHz int16 decoder range", err)
			}
			decInt16, err := NewDecoder(96000, channels, 1, coupled, mapping)
			if err != nil {
				t.Fatalf("NewDecoder(96000) for int16: %v", err)
			}
			gotInt16 := make([]int16, 0, len(wantMultistreamInt16))
			for frame, packet := range sequence {
				pcm, err := decInt16.DecodeToInt16(packet, frameSize)
				if err != nil {
					t.Fatalf("frame %d DecodeToInt16: %v", frame, err)
				}
				gotInt16 = append(gotInt16, pcm...)
				if gotRange, wantRange := decInt16.FinalRange(), wantDirectInt16.FinalRanges[frame]; gotRange != wantRange {
					t.Fatalf("int16 frame %d final range=%08x, want C %08x", frame, gotRange, wantRange)
				}
			}
			for i := range gotInt16 {
				if gotInt16[i] != wantMultistreamInt16[i] {
					t.Fatalf("int16 PCM[%d]=%d, want C multistream %d", i, gotInt16[i], wantMultistreamInt16[i])
				}
			}

			wantMultistreamInt24, err := decodeWithLibopusReferencePacketsInt24Gain(
				1, 96000, channels, 1, coupled, frameSize, 0, mapping, nil, sequence,
			)
			if err != nil {
				libopustest.HelperUnavailable(t, "native 96 kHz multistream int24 decode", err)
			}
			wantDirectInt24, err := probeMultistreamNative96kReference(libopustest.QEXTDecode96kParams{
				SampleFormat: libopustest.QEXTDecode96kFormatInt24,
				Channels:     channels, SampleRate: 96000, MaxFrameSize: frameSize, Packets: sequence,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "native 96 kHz int24 decoder range", err)
			}
			decInt24, err := NewDecoder(96000, channels, 1, coupled, mapping)
			if err != nil {
				t.Fatalf("NewDecoder(96000) for int24: %v", err)
			}
			gotInt24 := make([]int32, 0, len(wantMultistreamInt24))
			for frame, packet := range sequence {
				pcm, err := decInt24.DecodeToInt24(packet, frameSize)
				if err != nil {
					t.Fatalf("frame %d DecodeToInt24: %v", frame, err)
				}
				gotInt24 = append(gotInt24, pcm...)
				if gotRange, wantRange := decInt24.FinalRange(), wantDirectInt24.FinalRanges[frame]; gotRange != wantRange {
					t.Fatalf("int24 frame %d final range=%08x, want C %08x", frame, gotRange, wantRange)
				}
			}
			for i := range gotInt24 {
				if gotInt24[i] != wantMultistreamInt24[i] {
					t.Fatalf("int24 PCM[%d]=%d, want C multistream %d", i, gotInt24[i], wantMultistreamInt24[i])
				}
			}

			allocDec, err := NewDecoder(96000, channels, 1, coupled, mapping)
			if err != nil {
				t.Fatalf("NewDecoder(96000) for allocation check: %v", err)
			}
			callerBuffer := make([]float32, frameSize*channels)
			decodeInto := func() {
				if _, err := allocDec.DecodeIntoFloat32(encoded.Packets[0], callerBuffer, frameSize); err != nil {
					panic(err)
				}
			}
			decodeInto()
			decodeInto()
			if allocs := testing.AllocsPerRun(20, decodeInto); allocs != 0 {
				t.Fatalf("warmed caller-buffer decode allocations=%g, want 0", allocs)
			}
		})
	}
}
