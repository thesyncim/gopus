//go:build gopus_fixed_point

package multistream

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/fixedpoint"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestFixedMultistreamHybridLowRateMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	for _, channels := range []int{1, 2} {
		packets := fixedLowRateHybridPackets(t, channels)
		for _, sampleRate := range []int{8000, 12000} {
			t.Run(fmt.Sprintf("rate%d/ch%d", sampleRate, channels), func(t *testing.T) {
				frameSize := sampleRate / 50
				cases := []libopustest.DecodeDiffCase{
					{Packet: packets[0], Format: libopustest.DecodeDiffFormatInt16, FrameSize: uint32(frameSize)},
					{Packet: packets[1], Format: libopustest.DecodeDiffFormatInt16, FrameSize: uint32(frameSize)},
					{Format: libopustest.DecodeDiffFormatInt16, FrameSize: uint32(frameSize)},
					{Packet: packets[2], Format: libopustest.DecodeDiffFormatInt16, FrameSize: uint32(frameSize)},
					{Format: libopustest.DecodeDiffFormatInt16, FrameSize: uint32(frameSize)},
				}
				want, err := libopustest.ProbeDecodeSequence(sampleRate, channels, cases)
				if err != nil {
					t.Fatalf("selected FIXED_POINT decoder: %v", err)
				}

				mapping := []byte{0}
				coupled := 0
				if channels == 2 {
					mapping = []byte{0, 1}
					coupled = 1
				}

				// Assert that the public call is handled by the fixed integer path,
				// then compare its RES conversion and entropy range to selected C.
				resDecoder, err := NewDecoder(sampleRate, channels, 1, coupled, mapping)
				if err != nil {
					t.Fatalf("NewDecoder for opus_res: %v", err)
				}
				for i, decodeCase := range cases {
					res, handled, err := resDecoder.DecodeToResFixed(decodeCase.Packet, frameSize)
					if err != nil {
						t.Fatalf("case %d DecodeToResFixed: %v", i, err)
					}
					if !handled {
						t.Fatalf("case %d mode=%d was declined by fixed decode", i, parseStreamTOC(packets[min(i, len(packets)-1)][0]).mode)
					}
					got := make([]int16, len(res))
					for sample, value := range res {
						got[sample] = fixedpoint.Res2Int16(value)
					}
					wantPCM := want[i].Int16()
					if want[i].Code != int32(frameSize) || len(got) != len(wantPCM) {
						t.Fatalf("case %d samples Go=%d C=%d status=%d", i, len(got), len(wantPCM), want[i].Code)
					}
					for sample := range got {
						if got[sample] != wantPCM[sample] {
							t.Fatalf("case %d sample %d: Go=%d selected C=%d", i, sample, got[sample], wantPCM[sample])
						}
					}
					if gotRange := resDecoder.FinalRange(); gotRange != want[i].FinalRange {
						t.Fatalf("case %d final range Go=%08x selected C=%08x", i, gotRange, want[i].FinalRange)
					}
				}

				floatCases := append([]libopustest.DecodeDiffCase(nil), cases...)
				for i := range floatCases {
					floatCases[i].Format = libopustest.DecodeDiffFormatFloat32
				}
				wantFloat, err := libopustest.ProbeDecodeSequence(sampleRate, channels, floatCases)
				if err != nil {
					t.Fatalf("selected FIXED_POINT float decoder: %v", err)
				}
				floatDecoder, err := NewDecoder(sampleRate, channels, 1, coupled, mapping)
				if err != nil {
					t.Fatalf("NewDecoder for float output: %v", err)
				}
				for i, decodeCase := range cases {
					got, err := floatDecoder.DecodeToFloat32(decodeCase.Packet, frameSize)
					if err != nil {
						t.Fatalf("case %d DecodeToFloat32: %v", i, err)
					}
					wantPCM := wantFloat[i].Float32()
					if wantFloat[i].Code != int32(frameSize) || len(got) != len(wantPCM) {
						t.Fatalf("case %d float samples Go=%d C=%d status=%d", i, len(got), len(wantPCM), wantFloat[i].Code)
					}
					for sample := range got {
						if math.Float32bits(got[sample]) != math.Float32bits(wantPCM[sample]) {
							t.Fatalf("case %d float sample %d: Go=%08x selected C=%08x", i, sample, math.Float32bits(got[sample]), math.Float32bits(wantPCM[sample]))
						}
					}
					if gotRange := floatDecoder.FinalRange(); gotRange != wantFloat[i].FinalRange {
						t.Fatalf("case %d float final range Go=%08x selected C=%08x", i, gotRange, wantFloat[i].FinalRange)
					}
				}

				int24Cases := append([]libopustest.DecodeDiffCase(nil), cases...)
				for i := range int24Cases {
					int24Cases[i].Format = libopustest.DecodeDiffFormatInt24
				}
				wantInt24, err := libopustest.ProbeDecodeSequence(sampleRate, channels, int24Cases)
				if err != nil {
					t.Fatalf("selected FIXED_POINT int24 decoder: %v", err)
				}
				int24Decoder, err := NewDecoder(sampleRate, channels, 1, coupled, mapping)
				if err != nil {
					t.Fatalf("NewDecoder for int24 output: %v", err)
				}
				for i, decodeCase := range cases {
					got, err := int24Decoder.DecodeToInt24(decodeCase.Packet, frameSize)
					if err != nil {
						t.Fatalf("case %d DecodeToInt24: %v", i, err)
					}
					wantPCM := wantInt24[i].Int24()
					if wantInt24[i].Code != int32(frameSize) || len(got) != len(wantPCM) {
						t.Fatalf("case %d int24 samples Go=%d C=%d status=%d", i, len(got), len(wantPCM), wantInt24[i].Code)
					}
					for sample := range got {
						if got[sample] != wantPCM[sample] {
							t.Fatalf("case %d int24 sample %d: Go=%d selected C=%d", i, sample, got[sample], wantPCM[sample])
						}
					}
					if gotRange := int24Decoder.FinalRange(); gotRange != wantInt24[i].FinalRange {
						t.Fatalf("case %d int24 final range Go=%08x selected C=%08x", i, gotRange, wantInt24[i].FinalRange)
					}
				}

				allocDecoder, err := NewDecoder(sampleRate, channels, 1, coupled, mapping)
				if err != nil {
					t.Fatalf("NewDecoder for allocation check: %v", err)
				}
				if _, handled, err := allocDecoder.DecodeToResFixed(packets[0], frameSize); err != nil || !handled {
					t.Fatalf("warm received DecodeToResFixed handled=%v err=%v", handled, err)
				}
				if _, handled, err := allocDecoder.DecodeToResFixed(nil, frameSize); err != nil || !handled {
					t.Fatalf("warm PLC DecodeToResFixed handled=%v err=%v", handled, err)
				}
				var allocErr error
				allocs := testing.AllocsPerRun(10, func() {
					if _, handled, err := allocDecoder.DecodeToResFixed(packets[0], frameSize); err != nil || !handled {
						allocErr = fmt.Errorf("received DecodeToResFixed handled=%v err=%v", handled, err)
						return
					}
					if _, handled, err := allocDecoder.DecodeToResFixed(nil, frameSize); err != nil || !handled {
						allocErr = fmt.Errorf("PLC DecodeToResFixed handled=%v err=%v", handled, err)
						return
					}
				})
				if allocErr != nil {
					t.Fatal(allocErr)
				}
				if allocs != 0 {
					t.Fatalf("warmed low-rate Hybrid receive+PLC cycle allocated %.2f objects, want 0", allocs)
				}
			})
		}
	}
}

func fixedLowRateHybridPackets(t *testing.T, channels int) [][]byte {
	t.Helper()
	const (
		frameSize  = 960
		frameCount = 3
	)
	pcm := make([]int16, frameSize*channels*frameCount)
	for i := range pcm {
		pcm[i] = int16((i*7919+channels*251+37)%30001 - 15000)
	}
	packets, err := libopustest.ProbeOpusEncodeFixed(libopustest.OpusEncodeFixedParams{
		SampleRate:    48000,
		Channels:      channels,
		Application:   libopustest.OpusApplicationAudio,
		ForceMode:     libopustest.OpusForceModeHybrid,
		Bandwidth:     libopustest.OpusBandwidthFullband,
		Bitrate:       64000 * channels,
		Complexity:    10,
		ForceChannels: channels,
		FrameSize:     frameSize,
		FrameCount:    frameCount,
		PCM:           pcm,
	})
	if err != nil {
		t.Fatalf("selected FIXED_POINT encoder: %v", err)
	}
	if len(packets) != frameCount {
		t.Fatalf("selected FIXED_POINT encoder returned %d packets, want %d", len(packets), frameCount)
	}
	for i, packet := range packets {
		config := packet[0] >> 3
		if config < 12 || config >= 16 {
			t.Fatalf("packet %d TOC=%02x is not Hybrid", i, packet[0])
		}
	}
	return packets
}
