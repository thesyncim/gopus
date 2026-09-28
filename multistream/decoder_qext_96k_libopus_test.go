//go:build gopus_qext && !gopus_dred && !gopus_osce

package multistream

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

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
