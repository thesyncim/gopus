//go:build gopus_qext

package gopus

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/thesyncim/gopus/internal/benchutil"
	"github.com/thesyncim/gopus/internal/celt"
	internalenc "github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/types"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func TestDecode96kScratchBoundedOnLargeOutputBuffer(t *testing.T) {
	cfg := DefaultDecoderConfig(96000, 2)
	cfg.MaxPacketSamples = 11520
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder(96k): %v", err)
	}
	initialCap := cap(dec.scratchPCM)

	// Oversized packet must fail before touching scratchPCM.
	hugeBuf16 := make([]int16, 100000)
	if _, err := dec.DecodeInt16(make([]byte, cfg.MaxPacketBytes+1), hugeBuf16); err != ErrPacketTooLarge {
		t.Fatalf("DecodeInt16 oversized packet err = %v, want ErrPacketTooLarge", err)
	}
	if cap(dec.scratchPCM) != initialCap {
		t.Fatalf("DecodeInt16 grew scratchPCM cap from %d to %d on rejected packet", initialCap, cap(dec.scratchPCM))
	}

	// Valid packet with oversized caller buffer must cap scratchPCM at maxPacketSamples*channels.
	// Code-0 silence frame (TOC=0xf8 = CELT FB 20ms mono).
	if _, err := dec.DecodeInt16([]byte{0xf8}, hugeBuf16); err != nil {
		t.Fatalf("DecodeInt16(96k) failed: %v", err)
	}
	if cap(dec.scratchPCM) != initialCap {
		t.Fatalf("DecodeInt16 grew scratchPCM cap from %d to %d for oversized pcm buffer", initialCap, cap(dec.scratchPCM))
	}

	hugeBuf24 := make([]int32, 100000)
	if _, err := dec.DecodeInt24([]byte{0xf8}, hugeBuf24); err != nil {
		t.Fatalf("DecodeInt24(96k) failed: %v", err)
	}
	if cap(dec.scratchPCM) != initialCap {
		t.Fatalf("DecodeInt24 grew scratchPCM cap from %d to %d for oversized pcm buffer", initialCap, cap(dec.scratchPCM))
	}

	// Invalid PLC buffer length (not a multiple of 2.5 ms) must not grow scratchPCM.
	if _, err := dec.DecodeInt16(nil, make([]int16, 100002)); err != ErrInvalidFrameSize {
		t.Fatalf("DecodeInt16 invalid PLC len err = %v, want ErrInvalidFrameSize", err)
	}
	if cap(dec.scratchPCM) != initialCap {
		t.Fatalf("DecodeInt16 invalid PLC grew scratchPCM cap from %d to %d", initialCap, cap(dec.scratchPCM))
	}
}

func TestDecodeMalformedFramingPrecedesSmallOutput96k(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		for _, format := range []uint32{
			libopustest.DecodeDiffFormatFloat32,
			libopustest.DecodeDiffFormatInt16,
			libopustest.DecodeDiffFormatInt24,
		} {
			formatName := [...]string{"float32", "int16", "int24"}[format]
			t.Run(fmt.Sprintf("%dch/%s", channels, formatName), func(t *testing.T) {
				packet := encodeAPIRateCELTPacketFrameSize(t, channels, 960)
				assertDecodeMalformedFramingPrecedesSmallOutput(t, 96000, channels, format, packet, 1920)
			})
		}
	}
}

func TestDecodeEmptyAndPartialChannelBuffersPrecedePacketParsing96k(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		for _, format := range []uint32{
			libopustest.DecodeDiffFormatFloat32,
			libopustest.DecodeDiffFormatInt16,
			libopustest.DecodeDiffFormatInt24,
		} {
			formatName := [...]string{"float32", "int16", "int24"}[format]
			t.Run(fmt.Sprintf("%dch/%s", channels, formatName), func(t *testing.T) {
				packet := encodeAPIRateCELTPacketFrameSize(t, channels, 960)
				assertDecodeEmptyAndPartialChannelBuffers(
					t, 96000, channels, format, packet, 1920)
			})
		}
	}
}

func TestDecodeWithFEC96kValidatesPacketAndUsesRequestedPLCSize(t *testing.T) {
	const (
		channels   = 1
		frameSize  = 1920
		maxSamples = 5760
	)
	libopustest.RequireOracle(t)
	input := make([]float32, frameSize*2)
	for i := range input {
		input[i] = float32(0.3*math.Sin(2*math.Pi*6000*float64(i)/96000) +
			0.25*math.Sin(2*math.Pi*30000*float64(i)/96000))
	}
	encoded, err := libopustest.ProbeQEXTEncode96k(libopustest.QEXTEncode96kParams{
		Channels: channels, FrameSize: frameSize, Bitrate: 256000,
		Complexity: 10, MaxPacketSize: 8000, PCM: input, FrameCount: 2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "native 96 kHz QEXT encoder", err)
		return
	}
	if len(encoded.Packets) != 2 || len(encoded.Packets[0]) <= 1 || len(encoded.Packets[1]) <= 1 {
		t.Fatalf("QEXT encoder packets have lengths %v, want two non-DTX packets", packetLengths(encoded.Packets))
	}

	malformed := []byte{0x01, 0x00}
	packets := [][]byte{encoded.Packets[0], malformed, encoded.Packets[1], encoded.Packets[1], nil}
	decodeFEC := []bool{false, true, true, false, true}
	want, err := probeDecodeWithFEC96kOracle(libopustest.QEXTDecode96kParams{
		SampleFormat: libopustest.QEXTDecode96kFormatFloat32,
		Channels:     channels,
		SampleRate:   96000,
		MaxFrameSize: maxSamples,
		Packets:      packets,
		DecodeFEC:    decodeFEC,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "native 96 kHz QEXT FEC decoder", err)
		return
	}
	wantStatus := []int32{frameSize, -4, maxSamples, frameSize, maxSamples}
	if len(want.Status) != len(wantStatus) {
		t.Fatalf("C returned statuses %v, want %v", want.Status, wantStatus)
	}
	for i := range wantStatus {
		if want.Status[i] != wantStatus[i] {
			t.Fatalf("C step%d status=%d, want %d", i, want.Status[i], wantStatus[i])
		}
	}
	if want.FinalRanges[1] != want.FinalRanges[0] {
		t.Fatalf("C malformed FEC call changed final range: before=%08x after=%08x", want.FinalRanges[0], want.FinalRanges[1])
	}

	dec, err := NewDecoder(DefaultDecoderConfig(96000, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	pcm := make([]float32, maxSamples)
	pcmOffset := 0
	previousRange := uint32(0)
	previousDuration := 0
	previousDTX := false
	for step, packet := range packets {
		for i := range pcm {
			pcm[i] = float32(i) + 0.375
		}
		before := append([]float32(nil), pcm...)
		var n int
		var decodeErr error
		if decodeFEC[step] {
			n, decodeErr = dec.DecodeWithFEC(packet, pcm, true)
		} else {
			n, decodeErr = dec.Decode(packet, pcm)
		}
		if want.Status[step] < 0 {
			if n != 0 || decodeErr != ErrInvalidPacket {
				t.Fatalf("step%d decode=(%d,%v), C=%d; want malformed-packet rejection", step, n, decodeErr, want.Status[step])
			}
			if got := dec.FinalRange(); got != previousRange {
				t.Fatalf("step%d failed call changed final range to %08x, preceding range %08x", step, got, previousRange)
			}
			if got := dec.LastPacketDuration(); got != previousDuration {
				t.Fatalf("step%d failed call changed duration to %d, preceding duration %d", step, got, previousDuration)
			}
			if got := dec.InDTX(); got != previousDTX {
				t.Fatalf("step%d failed call changed DTX state to %v, preceding state %v", step, got, previousDTX)
			}
			for i := range pcm {
				if pcm[i] != before[i] {
					t.Fatalf("step%d failed call wrote PCM sample %d", step, i)
				}
			}
			continue
		}
		if decodeErr != nil || int32(n) != want.Status[step] {
			t.Fatalf("step%d decode=(%d,%v), C=%d", step, n, decodeErr, want.Status[step])
		}
		if got := dec.FinalRange(); got != want.FinalRanges[step] {
			t.Fatalf("step%d final range=%08x C=%08x", step, got, want.FinalRanges[step])
		}
		if got := dec.LastPacketDuration(); got != n {
			t.Fatalf("step%d duration=%d, decode returned %d", step, got, n)
		}
		count := n * channels
		if pcmOffset+count > len(want.PCM) {
			t.Fatalf("step%d C PCM ended at %d, need %d", step, len(want.PCM), pcmOffset+count)
		}
		for i := 0; i < count; i++ {
			if got, expected := math.Float32bits(pcm[i]), math.Float32bits(want.PCM[pcmOffset+i]); got != expected {
				t.Fatalf("step%d sample[%d]=%08x C=%08x", step, i, got, expected)
			}
		}
		pcmOffset += count
		previousRange = want.FinalRanges[step]
		previousDuration = n
		previousDTX = dec.InDTX()
	}
	if pcmOffset != len(want.PCM) {
		t.Fatalf("compared %d PCM samples, C returned %d", pcmOffset, len(want.PCM))
	}

	allocDec, err := NewDecoder(DefaultDecoderConfig(96000, channels))
	if err != nil {
		t.Fatalf("NewDecoder for allocation check: %v", err)
	}
	allocPCM := make([]float32, maxSamples)
	allocN, allocErr := allocDec.DecodeWithFEC(encoded.Packets[1], allocPCM, true)
	if allocErr != nil || allocN != maxSamples {
		t.Fatalf("warm FEC decode=(%d,%v), want (%d,nil)", allocN, allocErr, maxSamples)
	}
	allocs := testing.AllocsPerRun(20, func() {
		allocN, allocErr = allocDec.DecodeWithFEC(encoded.Packets[1], allocPCM, true)
	})
	if allocErr != nil || allocN != maxSamples {
		t.Fatalf("measured FEC decode=(%d,%v), want (%d,nil)", allocN, allocErr, maxSamples)
	}
	if allocs != 0 {
		t.Fatalf("warmed 96 kHz CELT FEC decode allocations=%v, want 0", allocs)
	}
}

func TestDecodeWithFEC96kInvalidDurationPrecedesMalformedFraming(t *testing.T) {
	libopustest.RequireOracle(t)
	malformed := []byte{0x01, 0x00}
	want, err := probeDecodeWithFEC96kOracle(libopustest.QEXTDecode96kParams{
		SampleFormat: libopustest.QEXTDecode96kFormatFloat32,
		Channels:     1,
		SampleRate:   96000,
		MaxFrameSize: 241,
		Packets:      [][]byte{malformed},
		DecodeFEC:    []bool{true},
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "native 96 kHz QEXT FEC decoder", err)
		return
	}
	if len(want.Status) != 1 || want.Status[0] != -1 {
		t.Fatalf("C status=%v, want [-1] for invalid FEC duration", want.Status)
	}
	if len(want.PCM) != 0 || want.FinalRanges[0] != 0 {
		t.Fatalf("C invalid-duration result emitted %d PCM samples or changed range to %08x", len(want.PCM), want.FinalRanges[0])
	}

	dec, err := NewDecoder(DefaultDecoderConfig(96000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	pcm := make([]float32, 241)
	for i := range pcm {
		pcm[i] = float32(i) + 0.25
	}
	before := append([]float32(nil), pcm...)
	if n, err := dec.DecodeWithFEC(malformed, pcm, true); n != 0 || err != ErrInvalidFrameSize {
		t.Fatalf("DecodeWithFEC invalid duration=(%d,%v), want (0,%v)", n, err, ErrInvalidFrameSize)
	}
	if dec.LastPacketDuration() != 0 || dec.FinalRange() != want.FinalRanges[0] {
		t.Fatalf("invalid-duration call changed decoder history: duration=%d range=%08x", dec.LastPacketDuration(), dec.FinalRange())
	}
	for i := range pcm {
		if pcm[i] != before[i] {
			t.Fatalf("invalid-duration call wrote PCM sample %d", i)
		}
	}

	// MaxPacketBytes is a Go decoder configuration limit that libopus does not
	// expose. It continues to take precedence over framing and duration parsing.
	cfg := DefaultDecoderConfig(96000, 1)
	cfg.MaxPacketBytes = 1
	limited, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder with MaxPacketBytes=1: %v", err)
	}
	if n, err := limited.DecodeWithFEC(malformed, pcm, true); n != 0 || err != ErrPacketTooLarge {
		t.Fatalf("DecodeWithFEC over MaxPacketBytes=(%d,%v), want (0,%v)", n, err, ErrPacketTooLarge)
	}
}

func TestDecodeWithFEC96kSuppliedSILKAndHybridPacketsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name      string
		mode      EncoderMode
		wantMode  Mode
		bandwidth Bandwidth
		bitrate   int
		withLBRR  bool
	}{
		{name: "silk", mode: EncoderModeSILK, wantMode: ModeSILK, bandwidth: BandwidthWideband, bitrate: 16000},
		{name: "hybrid", mode: EncoderModeHybrid, wantMode: ModeHybrid, bandwidth: BandwidthFullband, bitrate: 64000, withLBRR: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var seed, recovery []byte
			if tc.withLBRR {
				seed, recovery = encodeAPIRateFECSequence(t, tc.mode, tc.wantMode, tc.bandwidth, tc.bitrate, 1, 960)
			} else {
				seed = encodeAPIRateSILKPacket(t, 1)
				recovery = encodeAPIRateSILKPacket(t, 1)
			}
			packets := [][]byte{seed, recovery, recovery}
			decodeFEC := []bool{false, true, false}
			want, err := probeDecodeWithFEC96kOracle(libopustest.QEXTDecode96kParams{
				SampleFormat: libopustest.QEXTDecode96kFormatFloat32,
				Channels:     1,
				SampleRate:   96000,
				MaxFrameSize: 1920,
				Packets:      packets,
				DecodeFEC:    decodeFEC,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "native 96 kHz QEXT FEC decoder", err)
				return
			}
			if len(want.Status) != len(packets) || want.Status[0] != 1920 || want.Status[1] != 1920 || want.Status[2] != 1920 {
				t.Fatalf("C statuses=%v, want [1920 1920 1920]", want.Status)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(96000, 1))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			pcm := make([]float32, 1920)
			pcmOffset := 0
			for step, packet := range packets {
				var n int
				if decodeFEC[step] {
					n, err = dec.DecodeWithFEC(packet, pcm, true)
				} else {
					n, err = dec.Decode(packet, pcm)
				}
				if err != nil || int32(n) != want.Status[step] {
					t.Fatalf("step%d decode=(%d,%v), C=%d", step, n, err, want.Status[step])
				}
				if got := dec.FinalRange(); got != want.FinalRanges[step] {
					t.Fatalf("step%d final range=%08x C=%08x", step, got, want.FinalRanges[step])
				}
				for i := 0; i < n; i++ {
					if got, expected := math.Float32bits(pcm[i]), math.Float32bits(want.PCM[pcmOffset+i]); got != expected {
						t.Fatalf("step%d sample[%d]=%08x C=%08x", step, i, got, expected)
					}
				}
				pcmOffset += n
			}
			if pcmOffset != len(want.PCM) {
				t.Fatalf("compared %d PCM samples, C returned %d", pcmOffset, len(want.PCM))
			}
			if tc.withLBRR {
				allocDec, err := NewDecoder(DefaultDecoderConfig(96000, 1))
				if err != nil {
					t.Fatalf("NewDecoder for allocation check: %v", err)
				}
				allocPCM := make([]float32, 1920)
				if _, err := allocDec.Decode(seed, allocPCM); err != nil {
					t.Fatalf("warm seed decode: %v", err)
				}
				allocN, allocErr := allocDec.DecodeWithFEC(recovery, allocPCM, true)
				if allocErr != nil || allocN != 1920 {
					t.Fatalf("warm Hybrid FEC decode=(%d,%v), want (1920,nil)", allocN, allocErr)
				}
				allocs := testing.AllocsPerRun(20, func() {
					allocN, allocErr = allocDec.DecodeWithFEC(recovery, allocPCM, true)
				})
				if allocErr != nil || allocN != 1920 {
					t.Fatalf("measured Hybrid FEC decode=(%d,%v), want (1920,nil)", allocN, allocErr)
				}
				if allocs != 0 {
					t.Fatalf("warmed 96 kHz Hybrid FEC allocations=%v, want 0", allocs)
				}
			}
		})
	}
}

func packetLengths(packets [][]byte) []int {
	lens := make([]int, len(packets))
	for i := range packets {
		lens[i] = len(packets[i])
	}
	return lens
}

func TestDecodeLibopusQEXTPacketFinalRangeMatchesLibopus(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			pcm32 := make([]float32, 960*channels)
			for i := 0; i < 960; i++ {
				phase := 2 * math.Pi * 997 * float64(i) / 48000.0
				left := 0.45 * math.Sin(phase)
				pcm32[i*channels] = float32(left)
				if channels == 2 {
					pcm32[i*channels+1] = float32(0.35 * math.Sin(phase+0.37))
				}
			}

			packet, wantRange := encodeLibopusQEXTPacketWithFinalRange(t, opusDemo, channels, pcm32, false)
			if wantRange == 0 {
				t.Fatal("libopus packet final range is zero")
			}
			_, frames, padding, nbFrames, err := parsePacketFramesAndPadding(packet)
			if err != nil {
				t.Fatalf("parsePacketFramesAndPadding: %v", err)
			}
			if len(frames) != 1 {
				t.Fatalf("frame count=%d want 1", len(frames))
			}
			ext, ok, err := findPacketExtension(padding, nbFrames, qextPacketExtensionID)
			if err != nil {
				t.Fatalf("findPacketExtension: %v", err)
			}
			if !ok || len(ext.Data) == 0 {
				t.Fatal("libopus packet missing QEXT payload")
			}

			dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			pcm := make([]float32, 960*channels)
			n, err := dec.Decode(packet, pcm)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if n != 960 {
				t.Fatalf("Decode samples=%d want 960", n)
			}
			if got := dec.FinalRange(); got != wantRange {
				t.Fatalf("FinalRange()=0x%08x want libopus 0x%08x", got, wantRange)
			}
		})
	}
}

func TestDecodeLibopusQEXTPacketIgnoreExtensionsMatchesInactiveCELT(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			pcm32 := make([]float32, 960*channels)
			for i := 0; i < 960; i++ {
				phase := 2 * math.Pi * 997 * float64(i) / 48000.0
				left := 0.45 * math.Sin(phase)
				pcm32[i*channels] = float32(left)
				if channels == 2 {
					pcm32[i*channels+1] = float32(0.35 * math.Sin(phase+0.37))
				}
			}

			packet := encodeLibopusQEXTPacket(t, opusDemo, channels, pcm32, false)
			info, frames, padding, nbFrames, err := parsePacketFramesAndPadding(packet)
			if err != nil {
				t.Fatalf("parsePacketFramesAndPadding: %v", err)
			}
			if info.TOC.Mode != ModeCELT || len(frames) != 1 {
				t.Fatalf("packet mode=%v frames=%d want CELT single frame", info.TOC.Mode, len(frames))
			}
			ext, ok, err := findPacketExtension(padding, nbFrames, qextPacketExtensionID)
			if err != nil {
				t.Fatalf("findPacketExtension: %v", err)
			}
			if !ok || len(ext.Data) == 0 {
				t.Fatal("libopus packet missing QEXT payload")
			}

			wantDec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder(want): %v", err)
			}
			want := make([]float32, 960*channels)
			wantPacket := qextSingleFramePacketForTest(t, packet[0]&0xfc, frames[0], nil)
			wantN, err := wantDec.Decode(wantPacket, want)
			if err != nil {
				t.Fatalf("Decode(inactive QEXT packet): %v", err)
			}

			gotDec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder(got): %v", err)
			}
			gotDec.SetIgnoreExtensions(true)
			got := make([]float32, 960*channels)
			gotN, err := gotDec.Decode(packet, got)
			if err != nil {
				t.Fatalf("Decode(ignore extensions): %v", err)
			}
			if gotN != wantN {
				t.Fatalf("Decode samples=%d want %d", gotN, wantN)
			}
			if gotRange, wantRange := gotDec.FinalRange(), wantDec.FinalRange(); gotRange != wantRange {
				t.Fatalf("FinalRange()=0x%08x want inactive CELT range 0x%08x", gotRange, wantRange)
			}
			for i := 0; i < gotN*channels; i++ {
				if got[i] != want[i] {
					t.Fatalf("sample[%d]=%v want inactive CELT %v", i, got[i], want[i])
				}
			}
		})
	}
}

func TestDecodeLibopusQEXTOpaquePaddingMatchesInactiveCELT(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			pcm32 := make([]float32, 960*channels)
			for i := 0; i < 960; i++ {
				phase := 2 * math.Pi * 997 * float64(i) / 48000.0
				left := 0.45 * math.Sin(phase)
				pcm32[i*channels] = float32(left)
				if channels == 2 {
					pcm32[i*channels+1] = float32(0.35 * math.Sin(phase+0.37))
				}
			}

			packet := encodeLibopusQEXTPacket(t, opusDemo, channels, pcm32, false)
			info, frames, _, _, err := parsePacketFramesAndPadding(packet)
			if err != nil {
				t.Fatalf("parsePacketFramesAndPadding: %v", err)
			}
			if info.TOC.Mode != ModeCELT || len(frames) != 1 {
				t.Fatalf("packet mode=%v frames=%d want CELT single frame", info.TOC.Mode, len(frames))
			}

			malformed := make([]byte, len(packet)+8)
			n, err := buildPacketFromFramesAndPadding(packet[0]&^byte(0x03), frames, []byte{0xFF, 0xFF}, malformed, false)
			if err != nil {
				t.Fatalf("build malformed CELT QEXT padding packet: %v", err)
			}
			malformed = malformed[:n]

			wantDec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder(want): %v", err)
			}
			want := make([]float32, 960*channels)
			wantPacket := qextSingleFramePacketForTest(t, packet[0]&0xfc, frames[0], nil)
			wantN, err := wantDec.Decode(wantPacket, want)
			if err != nil {
				t.Fatalf("Decode(inactive QEXT packet): %v", err)
			}

			for _, ignore := range []bool{false, true} {
				gotDec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
				if err != nil {
					t.Fatalf("NewDecoder(got): %v", err)
				}
				gotDec.SetIgnoreExtensions(ignore)
				got := make([]float32, 960*channels)
				gotN, err := gotDec.Decode(malformed, got)
				if err != nil {
					t.Fatalf("Decode(malformed opaque padding, ignore=%v): %v", ignore, err)
				}
				if gotN != wantN {
					t.Fatalf("Decode samples=%d want %d (ignore=%v)", gotN, wantN, ignore)
				}
				if gotRange, wantRange := gotDec.FinalRange(), wantDec.FinalRange(); gotRange != wantRange {
					t.Fatalf("FinalRange()=0x%08x want inactive CELT range 0x%08x (ignore=%v)", gotRange, wantRange, ignore)
				}
				for i := 0; i < gotN*channels; i++ {
					if got[i] != want[i] {
						t.Fatalf("sample[%d]=%v want inactive CELT %v (ignore=%v)", i, got[i], want[i], ignore)
					}
				}
			}
		})
	}
}

func TestDecodeLibopusQEXTIgnoreExtensionsToggleSequenceMatchesExplicitPayloads(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	type transitionFrame struct {
		packet      []byte
		rawFrame    []byte
		qextPayload []byte
		tocBase     byte
		frameSize   int
		ignore      bool
	}

	newSine := func(channels int, freq float64, rightPhase float64, rightGain float64) []float32 {
		pcm := make([]float32, 960*channels)
		for i := 0; i < 960; i++ {
			phase := 2 * math.Pi * freq * float64(i) / 48000.0
			pcm[i*channels] = float32(0.45 * math.Sin(phase))
			if channels == 2 {
				pcm[i*channels+1] = float32(rightGain * math.Sin(phase+rightPhase))
			}
		}
		return pcm
	}

	plans := []struct {
		channels int
		pcm      []float32
		ignore   bool
	}{
		{1, newSine(1, 320.0, 0, 0), false},
		{2, newSine(2, 640.0, 0.37, 0.35), true},
		{1, newSine(1, 800.0, 0, 0), false},
	}

	sequence := make([]transitionFrame, 0, len(plans))
	for i, tc := range plans {
		packet := encodeLibopusQEXTPacket(t, opusDemo, tc.channels, tc.pcm, false)
		if len(packet) == 0 {
			t.Fatalf("encodeLibopusQEXTPacket[%d] empty", i)
		}
		info, frames, padding, nbFrames, err := parsePacketFramesAndPadding(packet)
		if err != nil {
			t.Fatalf("parsePacketFramesAndPadding[%d]: %v", i, err)
		}
		if len(frames) != 1 {
			t.Fatalf("frame count[%d]=%d want 1", i, len(frames))
		}
		ext, ok, err := findPacketExtension(padding, nbFrames, qextPacketExtensionID)
		if err != nil {
			t.Fatalf("findPacketExtension[%d]: %v", i, err)
		}
		if !ok || len(ext.Data) == 0 {
			t.Fatalf("packet[%d] missing QEXT extension payload", i)
		}
		sequence = append(sequence, transitionFrame{
			packet:      packet,
			rawFrame:    frames[0],
			qextPayload: ext.Data,
			tocBase:     packet[0] & 0xfc,
			frameSize:   info.TOC.FrameSize,
			ignore:      tc.ignore,
		})
	}

	wantDec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder(want): %v", err)
	}
	gotDec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder(got): %v", err)
	}

	for i, tc := range sequence {
		payload := tc.qextPayload
		if tc.ignore {
			payload = nil
		}
		want := make([]float32, 960*2)
		wantPacket := qextSingleFramePacketForTest(t, tc.tocBase, tc.rawFrame, payload)
		wantN, err := wantDec.Decode(wantPacket, want)
		if err != nil {
			t.Fatalf("Decode(explicit payload)[%d]: %v", i, err)
		}

		gotDec.SetIgnoreExtensions(tc.ignore)
		got := make([]float32, 960*2)
		gotN, err := gotDec.Decode(tc.packet, got)
		if err != nil {
			t.Fatalf("Decode[%d] ignore=%v: %v", i, tc.ignore, err)
		}
		if gotN != wantN {
			t.Fatalf("Decode[%d] samples=%d want %d", i, gotN, wantN)
		}
		if gotRange, wantRange := gotDec.FinalRange(), wantDec.FinalRange(); gotRange != wantRange {
			t.Fatalf("Decode[%d] FinalRange()=0x%08x want explicit payload range 0x%08x", i, gotRange, wantRange)
		}
		for j := 0; j < gotN*2; j++ {
			if got[j] != want[j] {
				t.Fatalf("Decode[%d] sample[%d]=%v want %v", i, j, got[j], want[j])
			}
		}
	}
}

func TestDecodeLibopusQEXTMultiFramePacketMatchesExplicitPayloads(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			packet, frames := makeLibopusQEXTMultiFramePacketForTest(t, opusDemo, channels)

			wantDec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder(want): %v", err)
			}
			want := make([]float32, frames[0].frameSize*channels*len(frames))
			wantOffset := 0
			for i, frame := range frames {
				wantPacket := qextSingleFramePacketForTest(t, packet[0]&0xfc, frame.rawFrame, frame.qextPayload)
				n, err := wantDec.Decode(wantPacket, want[wantOffset*channels:])
				if err != nil {
					t.Fatalf("Decode(explicit payload)[%d]: %v", i, err)
				}
				wantOffset += n
			}

			gotCfg := DefaultDecoderConfig(48000, channels)
			gotCfg.MaxPacketBytes = len(packet)
			gotDec, err := NewDecoder(gotCfg)
			if err != nil {
				t.Fatalf("NewDecoder(got): %v", err)
			}
			got := make([]float32, len(want))
			gotN, err := gotDec.Decode(packet, got)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if gotN != wantOffset {
				t.Fatalf("Decode samples=%d want %d", gotN, wantOffset)
			}
			if gotRange, wantRange := gotDec.FinalRange(), wantDec.FinalRange(); gotRange != wantRange {
				t.Fatalf("FinalRange()=0x%08x want 0x%08x", gotRange, wantRange)
			}
			for i := 0; i < gotN*channels; i++ {
				if got[i] != want[i] {
					t.Fatalf("sample[%d]=%v want %v", i, got[i], want[i])
				}
			}
		})
	}
}

func TestDecodeLibopusQEXTMultiFrameIgnoreExtensionsMatchesInactivePayloads(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			packet, frames := makeLibopusQEXTMultiFramePacketForTest(t, opusDemo, channels)

			wantDec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder(want): %v", err)
			}
			want := make([]float32, frames[0].frameSize*channels*len(frames))
			wantOffset := 0
			for i, frame := range frames {
				wantPacket := qextSingleFramePacketForTest(t, packet[0]&0xfc, frame.rawFrame, nil)
				n, err := wantDec.Decode(wantPacket, want[wantOffset*channels:])
				if err != nil {
					t.Fatalf("Decode(inactive QEXT packet)[%d]: %v", i, err)
				}
				wantOffset += n
			}

			gotCfg := DefaultDecoderConfig(48000, channels)
			gotCfg.MaxPacketBytes = len(packet)
			gotDec, err := NewDecoder(gotCfg)
			if err != nil {
				t.Fatalf("NewDecoder(got): %v", err)
			}
			gotDec.SetIgnoreExtensions(true)
			got := make([]float32, len(want))
			gotN, err := gotDec.Decode(packet, got)
			if err != nil {
				t.Fatalf("Decode(ignore extensions): %v", err)
			}
			if gotN != wantOffset {
				t.Fatalf("Decode samples=%d want %d", gotN, wantOffset)
			}
			if gotRange, wantRange := gotDec.FinalRange(), wantDec.FinalRange(); gotRange != wantRange {
				t.Fatalf("FinalRange()=0x%08x want inactive payload range 0x%08x", gotRange, wantRange)
			}
			for i := 0; i < gotN*channels; i++ {
				if got[i] != want[i] {
					t.Fatalf("sample[%d]=%v want inactive payload %v", i, got[i], want[i])
				}
			}
		})
	}
}

func TestDecodeGopusQEXTPacketMatchesLibopus(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			enc := internalenc.NewEncoder(48000, channels)
			enc.SetMode(internalenc.ModeCELT)
			enc.SetBandwidth(types.BandwidthFullband)
			enc.SetBitrate(256000)
			enc.SetQEXT(true)

			pcm := make([]float32, 960*channels)
			for i := 0; i < 960; i++ {
				phase := 2 * math.Pi * 997 * float64(i) / 48000.0
				left := 0.45 * math.Sin(phase)
				pcm[i*channels] = float32(left)
				if channels == 2 {
					right := 0.35 * math.Sin(phase+0.37)
					pcm[i*channels+1] = float32(right)
				}
			}

			packet, err := enc.Encode(pcm, 960)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if len(packet) == 0 {
				t.Fatal("Encode returned empty packet")
			}
			info, frames, padding, nbFrames, err := parsePacketFramesAndPadding(packet)
			if err != nil {
				t.Fatalf("parsePacketFramesAndPadding: %v", err)
			}
			if len(frames) != 1 {
				t.Fatalf("frame count=%d want 1", len(frames))
			}
			if info.Padding == 0 {
				t.Fatal("encoded packet missing extension padding")
			}
			ext, ok, err := findPacketExtension(padding, nbFrames, qextPacketExtensionID)
			if err != nil {
				t.Fatalf("findPacketExtension: %v", err)
			}
			if !ok || len(ext.Data) == 0 {
				t.Fatal("encoded packet missing QEXT payload")
			}

			refPacket := encodeLibopusQEXTPacket(t, opusDemo, channels, pcm, false)
			_, refFrames, refPadding, refNBFrames, err := parsePacketFramesAndPadding(refPacket)
			if err != nil {
				t.Fatalf("parsePacketFramesAndPadding(ref): %v", err)
			}
			refExt, refOK, err := findPacketExtension(refPadding, refNBFrames, qextPacketExtensionID)
			if err != nil {
				t.Fatalf("findPacketExtension(ref): %v", err)
			}
			if !refOK || len(refExt.Data) == 0 {
				t.Fatal("libopus packet missing QEXT payload")
			}

			dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			got := make([]float32, 960*channels)
			n, err := dec.decodeOpusFrameIntoWithQEXT(got, frames[0], info.TOC.FrameSize, info.TOC.FrameSize, info.TOC.Mode, info.TOC.Bandwidth, info.TOC.Stereo, ext.Data)
			if err != nil {
				t.Fatalf("decodeOpusFrameIntoWithQEXT: %v", err)
			}
			if n != 960 {
				t.Fatalf("Decode samples=%d want 960", n)
			}
			withoutQEXT := make([]float32, len(got))
			decNoQEXT, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder(no qext): %v", err)
			}
			nNoQEXT, err := decNoQEXT.decodeOpusFrameIntoWithQEXT(withoutQEXT, frames[0], info.TOC.FrameSize, info.TOC.FrameSize, info.TOC.Mode, info.TOC.Bandwidth, info.TOC.Stereo, nil)
			if err != nil {
				t.Fatalf("decodeOpusFrameIntoWithQEXT(nil): %v", err)
			}
			if nNoQEXT != n {
				t.Fatalf("Decode(no qext) samples=%d want %d", nNoQEXT, n)
			}

			tmpDir := t.TempDir()
			bitstreamPath := filepath.Join(tmpDir, "qext.bit")
			outputPath := filepath.Join(tmpDir, "qext.raw")
			if err := benchutil.WriteRepeatedOpusDemoBitstream(bitstreamPath, [][]byte{packet}, 1); err != nil {
				t.Fatalf("WriteRepeatedOpusDemoBitstream: %v", err)
			}

			cmd := exec.Command(opusDemo, "-d", "48000", fmt.Sprint(channels), bitstreamPath, outputPath)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("opus_demo decode failed: %v (%s)", err, bytes.TrimSpace(out))
			}

			refData, err := os.ReadFile(outputPath)
			if err != nil {
				t.Fatalf("read opus_demo output: %v", err)
			}
			if len(refData) < len(got)*2 {
				t.Fatalf("opus_demo output bytes=%d want at least %d", len(refData), len(got)*2)
			}
			refData = refData[:len(got)*2]

			var (
				maxDiff     float64
				signalPower float64
				errorPower  float64
				deltaNoQEXT float64
			)
			for i := range got {
				ref := float32(int16(binary.LittleEndian.Uint16(refData[i*2:]))) / 32768.0
				quantized := float32(float32ToInt16(got[i])) / 32768.0
				diff := math.Abs(float64(quantized - ref))
				if diff > maxDiff {
					maxDiff = diff
				}
				s := float64(ref)
				signalPower += s * s
				errorPower += diff * diff
				delta := math.Abs(float64(quantized - withoutQEXT[i]))
				if delta > deltaNoQEXT {
					deltaNoQEXT = delta
				}
			}

			snr := 200.0
			if errorPower > 0 {
				snr = 10 * math.Log10(signalPower/errorPower)
			}
			if maxDiff > 2.0/32768.0 {
				packetDiff := firstDiffByte(packet, refPacket)
				frameDiff := -1
				if len(refFrames) == 1 {
					frameDiff = firstDiffByte(frames[0], refFrames[0])
				}
				extDiff := firstDiffByte(ext.Data, refExt.Data)
				t.Logf("packet lengths: gopus=%d libopus=%d", len(packet), len(refPacket))
				t.Logf("frame lengths: gopus=%d libopus=%d", len(frames[0]), len(refFrames[0]))
				t.Logf("qext lengths: gopus=%d libopus=%d", len(ext.Data), len(refExt.Data))
				t.Logf("first diff: packet=%d frame=%d qext=%d", packetDiff, frameDiff, extDiff)
				if packetDiff >= 0 && packetDiff < len(packet) && packetDiff < len(refPacket) {
					t.Logf("packet bytes at diff: gopus=%02x libopus=%02x", packet[packetDiff], refPacket[packetDiff])
				}
				if extDiff >= 0 && extDiff < len(ext.Data) && extDiff < len(refExt.Data) {
					t.Logf("qext bytes at diff: gopus=%02x libopus=%02x", ext.Data[extDiff], refExt.Data[extDiff])
				}
				t.Fatalf("max diff too high: got %.2e want <= %.2e (delta vs no-qext decode %.2e)", maxDiff, 2.0/32768.0, deltaNoQEXT)
			}
			if snr < 60 {
				t.Fatalf("snr too low: got %.1f dB want >= 60 dB (delta vs no-qext decode %.2e)", snr, deltaNoQEXT)
			}
		})
	}
}

func TestDecodeLibopusQEXTPacketMatchesLibopus(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			pcm32 := make([]float32, 960*channels)
			for i := 0; i < 960; i++ {
				phase := 2 * math.Pi * 997 * float64(i) / 48000.0
				left := 0.45 * math.Sin(phase)
				pcm32[i*channels] = float32(left)
				if channels == 2 {
					pcm32[i*channels+1] = float32(0.35 * math.Sin(phase+0.37))
				}
			}

			packet := encodeLibopusQEXTPacket(t, opusDemo, channels, pcm32, false)
			outputPath := filepath.Join(t.TempDir(), "qext.raw")

			info, frames, padding, nbFrames, err := parsePacketFramesAndPadding(packet)
			if err != nil {
				t.Fatalf("parsePacketFramesAndPadding: %v", err)
			}
			if len(frames) != 1 {
				t.Fatalf("frame count=%d want 1", len(frames))
			}
			ext, ok, err := findPacketExtension(padding, nbFrames, qextPacketExtensionID)
			if err != nil {
				t.Fatalf("findPacketExtension: %v", err)
			}
			if !ok || len(ext.Data) == 0 {
				t.Fatal("libopus packet missing QEXT payload")
			}

			dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			got := make([]float32, 960*channels)
			n, err := dec.decodeOpusFrameIntoWithQEXT(got, frames[0], info.TOC.FrameSize, info.TOC.FrameSize, info.TOC.Mode, info.TOC.Bandwidth, info.TOC.Stereo, ext.Data)
			if err != nil {
				t.Fatalf("decodeOpusFrameIntoWithQEXT: %v", err)
			}
			if n != 960 {
				t.Fatalf("Decode samples=%d want 960", n)
			}
			withoutQEXT := make([]float32, len(got))
			decNoQEXT, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder(no qext): %v", err)
			}
			nNoQEXT, err := decNoQEXT.decodeOpusFrameIntoWithQEXT(withoutQEXT, frames[0], info.TOC.FrameSize, info.TOC.FrameSize, info.TOC.Mode, info.TOC.Bandwidth, info.TOC.Stereo, nil)
			if err != nil {
				t.Fatalf("decodeOpusFrameIntoWithQEXT(nil): %v", err)
			}
			if nNoQEXT != n {
				t.Fatalf("Decode(no qext) samples=%d want %d", nNoQEXT, n)
			}

			bitstreamPath := filepath.Join(t.TempDir(), "qext.bit")
			if err := benchutil.WriteRepeatedOpusDemoBitstream(bitstreamPath, [][]byte{packet}, 1); err != nil {
				t.Fatalf("WriteRepeatedOpusDemoBitstream: %v", err)
			}
			cmd := exec.Command(opusDemo, "-d", "48000", fmt.Sprint(channels), bitstreamPath, outputPath)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("opus_demo decode failed: %v (%s)", err, bytes.TrimSpace(out))
			}

			refData, err := os.ReadFile(outputPath)
			if err != nil {
				t.Fatalf("read opus_demo output: %v", err)
			}
			if len(refData) < len(got)*2 {
				t.Fatalf("opus_demo output bytes=%d want at least %d", len(refData), len(got)*2)
			}
			refData = refData[:len(got)*2]

			var (
				maxDiff        float64
				baseMaxDiff    float64
				signalPower    float64
				errorPower     float64
				baseErrorPower float64
				deltaNoQEXT    float64
				maxDiffIdx     int
				baseMaxDiffIdx int
			)
			for i := range got {
				ref := float32(int16(binary.LittleEndian.Uint16(refData[i*2:]))) / 32768.0
				quantized := float32(float32ToInt16(got[i])) / 32768.0
				diff := math.Abs(float64(quantized - ref))
				if diff > maxDiff {
					maxDiff = diff
					maxDiffIdx = i
				}
				baseQuantized := float32(float32ToInt16(withoutQEXT[i])) / 32768.0
				baseDiff := math.Abs(float64(baseQuantized - ref))
				if baseDiff > baseMaxDiff {
					baseMaxDiff = baseDiff
					baseMaxDiffIdx = i
				}
				s := float64(ref)
				signalPower += s * s
				errorPower += diff * diff
				baseErrorPower += baseDiff * baseDiff
				delta := math.Abs(float64(quantized - withoutQEXT[i]))
				if delta > deltaNoQEXT {
					deltaNoQEXT = delta
				}
			}

			snr := 200.0
			if errorPower > 0 {
				snr = 10 * math.Log10(signalPower/errorPower)
			}
			baseSNR := 200.0
			if baseErrorPower > 0 {
				baseSNR = 10 * math.Log10(signalPower/baseErrorPower)
			}
			if maxDiff > 2.0/32768.0 {
				refWorst := float32(int16(binary.LittleEndian.Uint16(refData[maxDiffIdx*2:]))) / 32768.0
				gotWorst := float32(float32ToInt16(got[maxDiffIdx])) / 32768.0
				baseWorst := float32(float32ToInt16(withoutQEXT[maxDiffIdx])) / 32768.0
				t.Logf("worst sample idx=%d frame=%d ch=%d got=%.8f ref=%.8f base=%.8f", maxDiffIdx, maxDiffIdx/channels, maxDiffIdx%channels, gotWorst, refWorst, baseWorst)
				refBaseWorst := float32(int16(binary.LittleEndian.Uint16(refData[baseMaxDiffIdx*2:]))) / 32768.0
				gotBaseWorst := float32(float32ToInt16(got[baseMaxDiffIdx])) / 32768.0
				baseQuantWorst := float32(float32ToInt16(withoutQEXT[baseMaxDiffIdx])) / 32768.0
				t.Logf("base worst idx=%d frame=%d ch=%d got=%.8f ref=%.8f base=%.8f", baseMaxDiffIdx, baseMaxDiffIdx/channels, baseMaxDiffIdx%channels, gotBaseWorst, refBaseWorst, baseQuantWorst)
				t.Fatalf("max diff too high: got %.2e want <= %.2e (delta vs no-qext decode %.2e, base max diff %.2e, base snr %.1f dB)", maxDiff, 2.0/32768.0, deltaNoQEXT, baseMaxDiff, baseSNR)
			}
			if snr < 60 {
				t.Fatalf("snr too low: got %.1f dB want >= 60 dB (delta vs no-qext decode %.2e)", snr, deltaNoQEXT)
			}
		})
	}
}

func TestDecodeLibopusQEXTPacketCELTFloat32FastPathMatchesFloat64(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			pcm32 := make([]float32, 960*channels)
			for i := 0; i < 960; i++ {
				phase := 2 * math.Pi * 997 * float64(i) / 48000.0
				left := 0.45 * math.Sin(phase)
				pcm32[i*channels] = float32(left)
				if channels == 2 {
					pcm32[i*channels+1] = float32(0.35 * math.Sin(phase+0.37))
				}
			}

			packet := encodeLibopusQEXTPacket(t, opusDemo, channels, pcm32, false)
			info, frames, padding, nbFrames, err := parsePacketFramesAndPadding(packet)
			if err != nil {
				t.Fatalf("parsePacketFramesAndPadding: %v", err)
			}
			if len(frames) != 1 {
				t.Fatalf("frame count=%d want 1", len(frames))
			}
			ext, ok, err := findPacketExtension(padding, nbFrames, qextPacketExtensionID)
			if err != nil {
				t.Fatalf("findPacketExtension: %v", err)
			}
			if !ok || len(ext.Data) == 0 {
				t.Fatal("libopus packet missing QEXT payload")
			}

			float64Dec := celt.NewDecoder(channels)
			float64Dec.SetBandwidth(celt.BandwidthFromOpusConfig(int(info.TOC.Bandwidth)))
			float64Dec.SetQEXTPayload(ext.Data)
			want, err := float64Dec.DecodeFrameWithPacketStereo(frames[0], info.TOC.FrameSize, info.TOC.Stereo)
			if err != nil {
				t.Fatalf("DecodeFrameWithPacketStereo: %v", err)
			}

			float32Dec := celt.NewDecoder(channels)
			float32Dec.SetBandwidth(celt.BandwidthFromOpusConfig(int(info.TOC.Bandwidth)))
			float32Dec.SetQEXTPayload(ext.Data)
			got := make([]float32, len(want))
			if err := float32Dec.DecodeFrameWithPacketStereoToFloat32(frames[0], info.TOC.FrameSize, info.TOC.Stereo, got); err != nil {
				t.Fatalf("DecodeFrameWithPacketStereoToFloat32: %v", err)
			}

			var (
				maxDiff    float64
				maxDiffIdx int
			)
			for i := range got {
				diff := math.Abs(float64(got[i] - float32(want[i])))
				if diff > maxDiff {
					maxDiff = diff
					maxDiffIdx = i
				}
			}
			if maxDiff != 0 {
				t.Fatalf("max diff %.2e at idx=%d frame=%d ch=%d", maxDiff, maxDiffIdx, maxDiffIdx/channels, maxDiffIdx%channels)
			}
		})
	}
}

func TestDecodeLibopusQEXTChannelTransitionSequenceMatchesLibopus(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	type transitionFrame struct {
		packet      []byte
		rawFrame    []byte
		qextPayload []byte
		frameSize   int
		stereo      bool
	}

	newSine := func(channels int, freq float64, rightPhase float64, rightGain float64) []float32 {
		pcm := make([]float32, 960*channels)
		for i := 0; i < 960; i++ {
			phase := 2 * math.Pi * freq * float64(i) / 48000.0
			pcm[i*channels] = float32(0.45 * math.Sin(phase))
			if channels == 2 {
				pcm[i*channels+1] = float32(rightGain * math.Sin(phase+rightPhase))
			}
		}
		return pcm
	}

	plans := []struct {
		channels int
		pcm      []float32
	}{
		{1, newSine(1, 320.0, 0, 0)},
		{2, newSine(2, 640.0, 0.37, 0.35)},
		{1, newSine(1, 800.0, 0.12, 0)},
	}

	sequence := make([]transitionFrame, 0, len(plans))
	packets := make([][]byte, 0, len(plans))
	for i, tc := range plans {
		packet := encodeLibopusQEXTPacket(t, opusDemo, tc.channels, tc.pcm, false)
		if len(packet) == 0 {
			t.Fatalf("encodeLibopusQEXTPacket[%d] empty", i)
		}
		info, frames, padding, nbFrames, err := parsePacketFramesAndPadding(packet)
		if err != nil {
			t.Fatalf("parsePacketFramesAndPadding[%d]: %v", i, err)
		}
		if len(frames) != 1 {
			t.Fatalf("frame count[%d]=%d want 1", i, len(frames))
		}
		ext, ok, err := findPacketExtension(padding, nbFrames, qextPacketExtensionID)
		if err != nil {
			t.Fatalf("findPacketExtension[%d]: %v", i, err)
		}
		if !ok || len(ext.Data) == 0 {
			t.Fatalf("packet[%d] missing QEXT extension payload", i)
		}
		sequence = append(sequence, transitionFrame{
			packet:      packet,
			rawFrame:    frames[0],
			qextPayload: ext.Data,
			frameSize:   info.TOC.FrameSize,
			stereo:      info.TOC.Stereo,
		})
		packets = append(packets, packet)
	}

	celtDec := celt.NewDecoder(2)
	got := make([]float32, 960*2*len(sequence))
	gotSamples := 0
	for i, tc := range sequence {
		celtDec.SetQEXTPayload(tc.qextPayload)
		decoded, err := celtDec.DecodeFrameWithPacketStereo(tc.rawFrame, tc.frameSize, tc.stereo)
		if err != nil {
			t.Fatalf("decode frame[%d]: %v", i, err)
		}
		gotLen := len(decoded)
		if gotLen != tc.frameSize*2 {
			t.Fatalf("decoded len[%d]=%d want %d", i, gotLen, tc.frameSize*2)
		}
		start := gotSamples * 2
		copy(got[start:start+gotLen], decoded)
		gotSamples += tc.frameSize
	}

	tmpDir := t.TempDir()
	bitstreamPath := filepath.Join(tmpDir, "transition.bit")
	outputPath := filepath.Join(tmpDir, "transition.raw")
	if err := benchutil.WriteRepeatedOpusDemoBitstream(bitstreamPath, packets, 1); err != nil {
		t.Fatalf("WriteRepeatedOpusDemoBitstream: %v", err)
	}
	cmd := exec.Command(opusDemo, "-d", "48000", "2", bitstreamPath, outputPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("opus_demo decode failed: %v (%s)", err, bytes.TrimSpace(out))
	}

	refData, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read opus_demo output: %v", err)
	}
	gotBytes := gotSamples * 2 * 2
	if len(refData) < gotBytes {
		t.Fatalf("opus_demo output bytes=%d want at least %d", len(refData), gotBytes)
	}
	refData = refData[:gotBytes]

	var maxDiff float64
	var maxDiffIdx int
	for i := 0; i < gotBytes/2; i++ {
		ref := float32(int16(binary.LittleEndian.Uint16(refData[i*2:]))) / 32768.0
		q := float32(float32ToInt16(float32(got[i]))) / 32768.0
		diff := math.Abs(float64(q - ref))
		if diff > maxDiff {
			maxDiff = diff
			maxDiffIdx = i
		}
	}
	if maxDiff > 2.0/32768.0 {
		t.Fatalf("max diff too high: got %.2e want <= %.2e at sample=%d", maxDiff, 2.0/32768.0, maxDiffIdx)
	}
}

func TestDecodeLibopusQEXTPacketWrapperMatchesDirectCELT(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			pcm32 := make([]float32, 960*channels)
			for i := 0; i < 960; i++ {
				phase := 2 * math.Pi * 997 * float64(i) / 48000.0
				left := 0.45 * math.Sin(phase)
				pcm32[i*channels] = float32(left)
				if channels == 2 {
					pcm32[i*channels+1] = float32(0.35 * math.Sin(phase+0.37))
				}
			}

			packet := encodeLibopusQEXTPacket(t, opusDemo, channels, pcm32, false)
			info, frames, padding, nbFrames, err := parsePacketFramesAndPadding(packet)
			if err != nil {
				t.Fatalf("parsePacketFramesAndPadding: %v", err)
			}
			if len(frames) != 1 {
				t.Fatalf("frame count=%d want 1", len(frames))
			}
			ext, ok, err := findPacketExtension(padding, nbFrames, qextPacketExtensionID)
			if err != nil {
				t.Fatalf("findPacketExtension: %v", err)
			}
			if !ok || len(ext.Data) == 0 {
				t.Fatal("libopus packet missing QEXT payload")
			}

			dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			got := make([]float32, 960*channels)
			n, err := dec.decodeOpusFrameIntoWithQEXT(got, frames[0], info.TOC.FrameSize, info.TOC.FrameSize, info.TOC.Mode, info.TOC.Bandwidth, info.TOC.Stereo, ext.Data)
			if err != nil {
				t.Fatalf("decodeOpusFrameIntoWithQEXT: %v", err)
			}
			if n != 960 {
				t.Fatalf("Decode samples=%d want 960", n)
			}

			direct := celt.NewDecoder(channels)
			direct.SetBandwidth(celt.BandwidthFromOpusConfig(int(info.TOC.Bandwidth)))
			direct.SetQEXTPayload(ext.Data)
			want := make([]float32, 960*channels)
			if err := direct.DecodeFrameWithPacketStereoToFloat32(frames[0], info.TOC.FrameSize, info.TOC.Stereo, want); err != nil {
				t.Fatalf("DecodeFrameWithPacketStereoToFloat32: %v", err)
			}

			var (
				maxDiff    float64
				maxDiffIdx int
			)
			for i := range got {
				diff := math.Abs(float64(got[i] - want[i]))
				if diff > maxDiff {
					maxDiff = diff
					maxDiffIdx = i
				}
			}
			if maxDiff != 0 {
				t.Fatalf("max diff %.2e at idx=%d frame=%d ch=%d", maxDiff, maxDiffIdx, maxDiffIdx/channels, maxDiffIdx%channels)
			}
		})
	}
}

func TestDecodeStereoLibopusQEXTPacketToMonoMatchesLibopus(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	pcm32 := make([]float32, 960*2)
	for i := 0; i < 960; i++ {
		phase := 2 * math.Pi * 997 * float64(i) / 48000.0
		pcm32[i*2] = float32(0.45 * math.Sin(phase))
		pcm32[i*2+1] = float32(0.35 * math.Sin(phase+0.37))
	}

	packet := encodeLibopusQEXTPacket(t, opusDemo, 2, pcm32, false)

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	got := make([]float32, 960)
	n, err := dec.Decode(packet, got)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if n != 960 {
		t.Fatalf("Decode samples=%d want 960", n)
	}

	tmpDir := t.TempDir()
	bitstreamPath := filepath.Join(tmpDir, "stereo_qext.bit")
	outputPath := filepath.Join(tmpDir, "stereo_qext_mono.raw")
	if err := benchutil.WriteRepeatedOpusDemoBitstream(bitstreamPath, [][]byte{packet}, 1); err != nil {
		t.Fatalf("WriteRepeatedOpusDemoBitstream: %v", err)
	}
	cmd := exec.Command(opusDemo, "-d", "48000", "1", bitstreamPath, outputPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("opus_demo decode failed: %v (%s)", err, bytes.TrimSpace(out))
	}

	refData, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read opus_demo output: %v", err)
	}
	if len(refData) < len(got)*2 {
		t.Fatalf("opus_demo output bytes=%d want at least %d", len(refData), len(got)*2)
	}
	refData = refData[:len(got)*2]

	maxDiff := 0.0
	for i := range got {
		ref := float32(int16(binary.LittleEndian.Uint16(refData[i*2:]))) / 32768.0
		quantized := float32(float32ToInt16(got[i])) / 32768.0
		diff := math.Abs(float64(quantized - ref))
		if diff > maxDiff {
			maxDiff = diff
		}
	}
	if maxDiff > 2.0/32768.0 {
		t.Fatalf("max diff too high: got %.2e want <= %.2e", maxDiff, 2.0/32768.0)
	}
}

func TestDecodeLibopusRestrictedCELTPacketMatchesLibopus(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			pcm32 := make([]float32, 960*channels)
			for i := 0; i < 960; i++ {
				phase := 2 * math.Pi * 997 * float64(i) / 48000.0
				left := 0.45 * math.Sin(phase)
				pcm32[i*channels] = float32(left)
				if channels == 2 {
					pcm32[i*channels+1] = float32(0.35 * math.Sin(phase+0.37))
				}
			}

			packet := encodeLibopusPacket(t, opusDemo, channels, pcm32, false, false)
			outputPath := filepath.Join(t.TempDir(), "restricted.raw")

			info, frames, _, _, err := parsePacketFramesAndPadding(packet)
			if err != nil {
				t.Fatalf("parsePacketFramesAndPadding: %v", err)
			}
			if len(frames) != 1 {
				t.Fatalf("frame count=%d want 1", len(frames))
			}

			dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			got := make([]float32, 960*channels)
			n, err := dec.decodeOpusFrameInto(got, frames[0], info.TOC.FrameSize, info.TOC.FrameSize, info.TOC.Mode, info.TOC.Bandwidth, info.TOC.Stereo)
			if err != nil {
				t.Fatalf("decodeOpusFrameInto: %v", err)
			}
			if n != 960 {
				t.Fatalf("Decode samples=%d want 960", n)
			}

			bitstreamPath := filepath.Join(t.TempDir(), "restricted.bit")
			if err := benchutil.WriteRepeatedOpusDemoBitstream(bitstreamPath, [][]byte{packet}, 1); err != nil {
				t.Fatalf("WriteRepeatedOpusDemoBitstream: %v", err)
			}
			cmd := exec.Command(opusDemo, "-d", "48000", fmt.Sprint(channels), bitstreamPath, outputPath)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("opus_demo decode failed: %v (%s)", err, bytes.TrimSpace(out))
			}

			refData, err := os.ReadFile(outputPath)
			if err != nil {
				t.Fatalf("read opus_demo output: %v", err)
			}
			if len(refData) < len(got)*2 {
				t.Fatalf("opus_demo output bytes=%d want at least %d", len(refData), len(got)*2)
			}
			refData = refData[:len(got)*2]

			var maxDiff float64
			for i := range got {
				ref := float32(int16(binary.LittleEndian.Uint16(refData[i*2:]))) / 32768.0
				quantized := float32(float32ToInt16(got[i])) / 32768.0
				diff := math.Abs(float64(quantized - ref))
				if diff > maxDiff {
					maxDiff = diff
				}
			}
			if maxDiff > 2.0/32768.0 {
				t.Fatalf("max diff too high: got %.2e want <= %.2e", maxDiff, 2.0/32768.0)
			}
		})
	}
}

func TestQEXTDiscardedBandsMatchSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, hybridMode := range []bool{false, true} {
		for _, codedChannels := range []int{1, 2} {
			var base []byte
			if hybridMode {
				base = encodeAPIRateHybridPacketFrameSize(t, codedChannels, 960)
			} else {
				base = encodeAPIRateCELTPacketFrameSize(t, codedChannels, 960)
			}
			if base[0]&3 != 0 {
				t.Fatalf("expected one-frame packet, TOC=%02x", base[0])
			}
			for _, length := range []int{16, 32, 64, 128} {
				padding := append([]byte{248}, bytes.Repeat([]byte{255}, length)...)
				packet := append([]byte{base[0] | 3, 0x41, byte(len(padding))}, base[1:]...)
				packet = append(packet, padding...)
				for _, rate := range []int{48000, 96000} {
					for _, channels := range []int{1, 2} {
						t.Run(fmt.Sprintf("hybrid%t/coded%d/fs%d/api%d/bytes%d", hybridMode, codedChannels, rate, channels, length), func(t *testing.T) {
							assertDecoderSequenceFormatsMatchSelectedLibopus(t, rate, channels, [][]byte{packet, base, nil, base})
						})
					}
				}
			}
		}
	}
}

func TestDecodeHybridLibopusQEXTPacketMatchesLibopus(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			packet := makeHybridQEXTPacketForTest(t, opusDemo, channels)
			dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			got := make([]float32, 960*channels)
			n, err := dec.Decode(packet, got)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if n != 960 {
				t.Fatalf("Decode samples=%d want 960", n)
			}

			tmpDir := t.TempDir()
			bitstreamPath := filepath.Join(tmpDir, "hybrid_qext.bit")
			outputPath := filepath.Join(tmpDir, "hybrid_qext.raw")
			if err := benchutil.WriteRepeatedOpusDemoBitstream(bitstreamPath, [][]byte{packet}, 1); err != nil {
				t.Fatalf("WriteRepeatedOpusDemoBitstream: %v", err)
			}
			cmd := exec.Command(opusDemo, "-d", "48000", fmt.Sprint(channels), bitstreamPath, outputPath)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("opus_demo decode failed: %v (%s)", err, bytes.TrimSpace(out))
			}

			refData, err := os.ReadFile(outputPath)
			if err != nil {
				t.Fatalf("read opus_demo output: %v", err)
			}
			if len(refData) < len(got)*2 {
				t.Fatalf("opus_demo output bytes=%d want at least %d", len(refData), len(got)*2)
			}
			refData = refData[:len(got)*2]

			var (
				maxDiff    float64
				maxDiffIdx int
			)
			for i := range got {
				ref := float32(int16(binary.LittleEndian.Uint16(refData[i*2:]))) / 32768.0
				quantized := float32(float32ToInt16(got[i])) / 32768.0
				diff := math.Abs(float64(quantized - ref))
				if diff > maxDiff {
					maxDiff = diff
					maxDiffIdx = i
				}
			}
			if maxDiff > 2.0/32768.0 {
				t.Fatalf("max diff too high: got %.2e want <= %.2e at sample=%d frame=%d ch=%d", maxDiff, 2.0/32768.0, maxDiffIdx, maxDiffIdx/channels, maxDiffIdx%channels)
			}
		})
	}
}

func TestDecodeHybridLibopusQEXTPacketIgnoreExtensionsMatchesInactiveHybrid(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			packet := makeHybridQEXTPacketForTest(t, opusDemo, channels)
			info, frames, padding, nbFrames, err := parsePacketFramesAndPadding(packet)
			if err != nil {
				t.Fatalf("parsePacketFramesAndPadding: %v", err)
			}
			if info.TOC.Mode != ModeHybrid || len(frames) != 1 {
				t.Fatalf("packet mode=%v frames=%d want Hybrid single frame", info.TOC.Mode, len(frames))
			}
			ext, ok, err := findPacketExtension(padding, nbFrames, qextPacketExtensionID)
			if err != nil {
				t.Fatalf("findPacketExtension: %v", err)
			}
			if !ok || len(ext.Data) == 0 {
				t.Fatal("hybrid packet missing QEXT payload")
			}

			wantDec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder(want): %v", err)
			}
			want := make([]float32, 960*channels)
			wantPacket := qextSingleFramePacketForTest(t, packet[0]&0xfc, frames[0], nil)
			wantN, err := wantDec.Decode(wantPacket, want)
			if err != nil {
				t.Fatalf("Decode(inactive QEXT packet): %v", err)
			}

			gotDec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder(got): %v", err)
			}
			gotDec.SetIgnoreExtensions(true)
			got := make([]float32, 960*channels)
			gotN, err := gotDec.Decode(packet, got)
			if err != nil {
				t.Fatalf("Decode(ignore extensions): %v", err)
			}
			if gotN != wantN {
				t.Fatalf("Decode samples=%d want %d", gotN, wantN)
			}
			if gotRange, wantRange := gotDec.FinalRange(), wantDec.FinalRange(); gotRange != wantRange {
				t.Fatalf("FinalRange()=0x%08x want inactive Hybrid range 0x%08x", gotRange, wantRange)
			}
			for i := 0; i < gotN*channels; i++ {
				if got[i] != want[i] {
					t.Fatalf("sample[%d]=%v want inactive Hybrid %v", i, got[i], want[i])
				}
			}
		})
	}
}

func TestDecodeHybridLibopusQEXTOpaquePaddingMatchesInactiveHybrid(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			packet := makeHybridQEXTPacketForTest(t, opusDemo, channels)
			info, frames, _, _, err := parsePacketFramesAndPadding(packet)
			if err != nil {
				t.Fatalf("parsePacketFramesAndPadding: %v", err)
			}
			if info.TOC.Mode != ModeHybrid || len(frames) != 1 {
				t.Fatalf("packet mode=%v frames=%d want Hybrid single frame", info.TOC.Mode, len(frames))
			}

			malformed := make([]byte, len(packet)+8)
			n, err := buildPacketFromFramesAndPadding(packet[0]&^byte(0x03), frames, []byte{0xFF, 0xFF}, malformed, false)
			if err != nil {
				t.Fatalf("build malformed Hybrid QEXT padding packet: %v", err)
			}
			malformed = malformed[:n]

			wantDec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder(want): %v", err)
			}
			want := make([]float32, 960*channels)
			wantPacket := qextSingleFramePacketForTest(t, packet[0]&0xfc, frames[0], nil)
			wantN, err := wantDec.Decode(wantPacket, want)
			if err != nil {
				t.Fatalf("Decode(inactive QEXT packet): %v", err)
			}

			for _, ignore := range []bool{false, true} {
				gotDec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
				if err != nil {
					t.Fatalf("NewDecoder(got): %v", err)
				}
				gotDec.SetIgnoreExtensions(ignore)
				got := make([]float32, 960*channels)
				gotN, err := gotDec.Decode(malformed, got)
				if err != nil {
					t.Fatalf("Decode(malformed opaque padding, ignore=%v): %v", ignore, err)
				}
				if gotN != wantN {
					t.Fatalf("Decode samples=%d want %d (ignore=%v)", gotN, wantN, ignore)
				}
				if gotRange, wantRange := gotDec.FinalRange(), wantDec.FinalRange(); gotRange != wantRange {
					t.Fatalf("FinalRange()=0x%08x want inactive Hybrid range 0x%08x (ignore=%v)", gotRange, wantRange, ignore)
				}
				for i := 0; i < gotN*channels; i++ {
					if got[i] != want[i] {
						t.Fatalf("sample[%d]=%v want inactive Hybrid %v (ignore=%v)", i, got[i], want[i], ignore)
					}
				}
			}
		})
	}
}

func TestDecodeHybridLibopusQEXTIgnoreExtensionsToggleSequenceMatchesExplicitPayloads(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	type transitionFrame struct {
		packet      []byte
		rawFrame    []byte
		qextPayload []byte
		tocBase     byte
		frameSize   int
		ignore      bool
	}

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			ignores := []bool{false, true, false}
			sequence := make([]transitionFrame, 0, len(ignores))
			for i, ignore := range ignores {
				packet := makeHybridQEXTPacketForTest(t, opusDemo, channels)
				info, frames, padding, nbFrames, err := parsePacketFramesAndPadding(packet)
				if err != nil {
					t.Fatalf("parsePacketFramesAndPadding[%d]: %v", i, err)
				}
				if info.TOC.Mode != ModeHybrid || len(frames) != 1 {
					t.Fatalf("packet[%d] mode=%v frames=%d want Hybrid single frame", i, info.TOC.Mode, len(frames))
				}
				ext, ok, err := findPacketExtension(padding, nbFrames, qextPacketExtensionID)
				if err != nil {
					t.Fatalf("findPacketExtension[%d]: %v", i, err)
				}
				if !ok || len(ext.Data) == 0 {
					t.Fatalf("packet[%d] missing QEXT payload", i)
				}
				sequence = append(sequence, transitionFrame{
					packet:      packet,
					rawFrame:    frames[0],
					qextPayload: ext.Data,
					tocBase:     packet[0] & 0xfc,
					frameSize:   info.TOC.FrameSize,
					ignore:      ignore,
				})
			}

			wantDec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder(want): %v", err)
			}
			gotDec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder(got): %v", err)
			}
			for i, tc := range sequence {
				payload := tc.qextPayload
				if tc.ignore {
					payload = nil
				}
				want := make([]float32, tc.frameSize*channels)
				wantPacket := qextSingleFramePacketForTest(t, tc.tocBase, tc.rawFrame, payload)
				wantN, err := wantDec.Decode(wantPacket, want)
				if err != nil {
					t.Fatalf("Decode(explicit payload)[%d]: %v", i, err)
				}

				gotDec.SetIgnoreExtensions(tc.ignore)
				got := make([]float32, tc.frameSize*channels)
				gotN, err := gotDec.Decode(tc.packet, got)
				if err != nil {
					t.Fatalf("Decode[%d] ignore=%v: %v", i, tc.ignore, err)
				}
				if gotN != wantN {
					t.Fatalf("Decode[%d] samples=%d want %d", i, gotN, wantN)
				}
				if gotRange, wantRange := gotDec.FinalRange(), wantDec.FinalRange(); gotRange != wantRange {
					t.Fatalf("Decode[%d] FinalRange()=0x%08x want 0x%08x", i, gotRange, wantRange)
				}
				for j := 0; j < gotN*channels; j++ {
					if got[j] != want[j] {
						t.Fatalf("Decode[%d] sample[%d]=%v want %v", i, j, got[j], want[j])
					}
				}
			}
		})
	}
}

func TestQEXTNonFullbandHeaderMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		pcm := make([]float32, 960*channels)
		for i := range 960 {
			for channel := range channels {
				pcm[i*channels+channel] = float32(0.28*math.Sin(2*math.Pi*float64(173+97*channel)*float64(i)/48000) +
					0.13*math.Sin(2*math.Pi*float64(1773+423*channel)*float64(i)/48000+0.17))
			}
		}
		for _, config := range []struct{ mode, bandwidth int }{
			{1002, 1101}, {1002, 1103}, {1002, 1104}, {1002, 1105},
			{1001, 1104}, {1001, 1105},
		} {
			encoded, err := libopustest.ProbeEncodeDiff(libopustest.EncodeDiffParams{
				SampleRate: 48000, Channels: channels, Application: 2049,
				ForceMode: config.mode, Bandwidth: config.bandwidth, MaxBandwidth: config.bandwidth,
				Bitrate: 64000, Complexity: 10, Signal: 3002, VBR: true,
				ForceChannels: channels, FrameSize: 960, FrameCount: 1, PCM: pcm,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(encoded) != 1 || len(encoded[0].Packet) < 2 || encoded[0].Packet[0]&3 != 0 {
				t.Fatal("selected C encoder did not return one elementary frame")
			}
			base := encoded[0].Packet
			for _, fill := range []byte{0, 255} {
				padding := append([]byte{248}, bytes.Repeat([]byte{fill}, 128)...)
				packet := append([]byte{base[0] | 3, 0x41, byte(len(padding))}, base[1:]...)
				packet = append(packet, padding...)
				for _, rate := range []int{48000, 96000} {
					t.Run(fmt.Sprintf("mode%d/bw%d/ch%d/fill%d/fs%d", config.mode, config.bandwidth, channels, fill, rate), func(t *testing.T) {
						assertDecoderSequenceFormatsMatchSelectedLibopus(t, rate, channels, [][]byte{packet, base, nil, packet})
					})
				}
			}
		}
	}
}

func TestDecoderReset96kLastFrameSize(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(96000, 2))
	if err != nil {
		t.Fatalf("NewDecoder(96000): %v", err)
	}
	dec.Reset()
	if dec.lastFrameSize != 1920 {
		t.Fatalf("lastFrameSize after Reset() = %d, want 1920", dec.lastFrameSize)
	}
}

func firstOpusDemoPacket(path string) ([]byte, error) {
	packet, _, err := firstOpusDemoPacketWithFinalRange(path)
	return packet, err
}

func firstOpusDemoPacketWithFinalRange(path string) ([]byte, uint32, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	if len(data) < 8 {
		return nil, 0, os.ErrInvalid
	}
	n := int(binary.BigEndian.Uint32(data[:4]))
	if n < 0 || len(data) < 8+n {
		return nil, 0, os.ErrInvalid
	}
	finalRange := binary.BigEndian.Uint32(data[4:8])
	return append([]byte(nil), data[8:8+n]...), finalRange, nil
}

func encodeLibopusPacket(t *testing.T, opusDemo string, channels int, pcm32 []float32, cbr bool, qext bool) []byte {
	t.Helper()
	packet, _ := encodeLibopusPacketWithFinalRange(t, opusDemo, channels, pcm32, cbr, qext)
	return packet
}

func encodeLibopusPacketWithFinalRange(t *testing.T, opusDemo string, channels int, pcm32 []float32, cbr bool, qext bool) ([]byte, uint32) {
	t.Helper()
	return encodeLibopusPacketWithFinalRangeAtBitrate(t, opusDemo, channels, pcm32, cbr, qext, 256000)
}

func encodeLibopusPacketAtBitrate(t *testing.T, opusDemo string, channels int, pcm32 []float32, cbr bool, qext bool, bitrate int) []byte {
	t.Helper()
	packet, _ := encodeLibopusPacketWithFinalRangeAtBitrate(t, opusDemo, channels, pcm32, cbr, qext, bitrate)
	return packet
}

func encodeLibopusPacketWithFinalRangeAtBitrate(t *testing.T, opusDemo string, channels int, pcm32 []float32, cbr bool, qext bool, bitrate int) ([]byte, uint32) {
	t.Helper()

	tmpDir := t.TempDir()
	inputPath := filepath.Join(tmpDir, "qext.f32")
	bitstreamPath := filepath.Join(tmpDir, "qext.bit")
	if err := benchutil.WriteRepeatedRawFloat32(inputPath, pcm32, 1); err != nil {
		t.Fatalf("WriteRepeatedRawFloat32: %v", err)
	}

	args := []string{
		"-e", "restricted-celt", "48000", fmt.Sprint(channels), fmt.Sprint(bitrate),
		"-f32", "-complexity", "10", "-bandwidth", "FB", "-framesize", "20",
	}
	if qext {
		args = append(args, "-qext")
	}
	if cbr {
		args = append(args, "-cbr")
	}
	args = append(args, inputPath, bitstreamPath)
	cmd := exec.Command(opusDemo, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("opus_demo encode failed: %v (%s)", err, bytes.TrimSpace(out))
	}

	packet, finalRange, err := firstOpusDemoPacketWithFinalRange(bitstreamPath)
	if err != nil {
		t.Fatalf("firstOpusDemoPacketWithFinalRange: %v", err)
	}
	return packet, finalRange
}

func encodeLibopusQEXTPacket(t *testing.T, opusDemo string, channels int, pcm32 []float32, cbr bool) []byte {
	t.Helper()
	return encodeLibopusPacket(t, opusDemo, channels, pcm32, cbr, true)
}

func encodeLibopusQEXTPacketWithFinalRange(t *testing.T, opusDemo string, channels int, pcm32 []float32, cbr bool) ([]byte, uint32) {
	t.Helper()
	return encodeLibopusPacketWithFinalRange(t, opusDemo, channels, pcm32, cbr, true)
}

func firstDiffByte(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return n
	}
	return -1
}

type qextTestFrame struct {
	rawFrame    []byte
	qextPayload []byte
	frameSize   int
	mode        Mode
	bandwidth   Bandwidth
	stereo      bool
}

func makeQEXTSinePCMForTest(channels int, freq, phaseShift float64) []float32 {
	pcm := make([]float32, 960*channels)
	for i := 0; i < 960; i++ {
		phase := 2 * math.Pi * freq * float64(i) / 48000.0
		pcm[i*channels] = float32(0.43 * math.Sin(phase+phaseShift))
		if channels == 2 {
			pcm[i*channels+1] = float32(0.31 * math.Sin(phase+phaseShift+0.41))
		}
	}
	return pcm
}

func parseLibopusQEXTFrameForTest(t *testing.T, label string, packet []byte) qextTestFrame {
	t.Helper()
	info, frames, padding, nbFrames, err := parsePacketFramesAndPadding(packet)
	if err != nil {
		t.Fatalf("parsePacketFramesAndPadding(%s): %v", label, err)
	}
	if len(frames) != 1 {
		t.Fatalf("%s frames=%d want 1", label, len(frames))
	}
	ext, ok, err := findPacketExtension(padding, nbFrames, qextPacketExtensionID)
	if err != nil {
		t.Fatalf("findPacketExtension(%s): %v", label, err)
	}
	if !ok || len(ext.Data) == 0 {
		t.Fatalf("%s missing QEXT payload", label)
	}
	return qextTestFrame{
		rawFrame:    frames[0],
		qextPayload: ext.Data,
		frameSize:   info.TOC.FrameSize,
		mode:        info.TOC.Mode,
		bandwidth:   info.TOC.Bandwidth,
		stereo:      info.TOC.Stereo,
	}
}

func makeLibopusQEXTMultiFramePacketForTest(t *testing.T, opusDemo string, channels int) ([]byte, []qextTestFrame) {
	t.Helper()
	packetA := encodeLibopusQEXTPacket(t, opusDemo, channels, makeQEXTSinePCMForTest(channels, 997, 0.0), false)
	packetB := encodeLibopusQEXTPacket(t, opusDemo, channels, makeQEXTSinePCMForTest(channels, 1237, 0.23), false)
	frameA := parseLibopusQEXTFrameForTest(t, "frameA", packetA)
	frameB := parseLibopusQEXTFrameForTest(t, "frameB", packetB)
	if frameA.frameSize != frameB.frameSize || frameA.mode != frameB.mode || frameA.bandwidth != frameB.bandwidth || frameA.stereo != frameB.stereo {
		t.Fatalf("source frames are not repacketizable: A=%+v B=%+v", frameA, frameB)
	}

	rawFrames := [][]byte{frameA.rawFrame, frameB.rawFrame}
	packet := make([]byte, len(packetA)+len(packetB)+len(frameA.qextPayload)+len(frameB.qextPayload)+128)
	n, err := buildRepacketizedPacketWithOptions(
		packetA[0]&^byte(0x03),
		rawFrames,
		packet,
		0,
		false,
		[]packetExtensionData{
			{ID: qextPacketExtensionID, Frame: 0, Data: frameA.qextPayload},
			{ID: qextPacketExtensionID, Frame: 1, Data: frameB.qextPayload},
		},
	)
	if err != nil {
		t.Fatalf("build multi-frame QEXT packet: %v", err)
	}
	packet = packet[:n]

	info, builtFrames, padding, nbFrames, err := parsePacketFramesAndPadding(packet)
	if err != nil {
		t.Fatalf("parsePacketFramesAndPadding(built): %v", err)
	}
	if info.FrameCount != 2 || len(builtFrames) != 2 || nbFrames != 2 {
		t.Fatalf("built packet frame count info=%d frames=%d paddingFrames=%d want 2", info.FrameCount, len(builtFrames), nbFrames)
	}
	extensions, err := parsePacketExtensionList(padding, nbFrames)
	if err != nil {
		t.Fatalf("parsePacketExtensionList(built): %v", err)
	}
	if len(extensions) != 2 || extensions[0].Frame != 0 || extensions[1].Frame != 1 {
		t.Fatalf("built extensions=%+v want one QEXT payload per frame", extensions)
	}
	return packet, []qextTestFrame{frameA, frameB}
}

// qextSingleFramePacketForTest rebuilds one extracted CELT/Hybrid frame as a
// public packet, optionally attaching its explicit QEXT payload. Using the
// public decoder for both sides keeps packet-wrapper comparisons in the same
// coefficient domain under gopus_fixed_point and exercises normal packet
// framing rather than calling the float-only internal frame helper.
func qextSingleFramePacketForTest(t *testing.T, tocBase byte, frame, payload []byte) []byte {
	t.Helper()
	frames := [][]byte{frame}
	var extensions []packetExtensionData
	if len(payload) != 0 {
		extensions = []packetExtensionData{{ID: qextPacketExtensionID, Frame: 0, Data: payload}}
	}
	packet := make([]byte, len(frame)+len(payload)+16)
	n, err := buildRepacketizedPacketWithOptions(tocBase, frames, packet, 0, false, extensions)
	if err != nil {
		t.Fatalf("build explicit QEXT packet: %v", err)
	}
	return packet[:n]
}

func makeHybridQEXTPacketForTest(t *testing.T, opusDemo string, channels int) []byte {
	t.Helper()

	const frameSize = 960
	hybridPCM := make([]float32, frameSize*channels)
	qextPCM := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		tm := float64(i) / 48000.0
		left := 0.28*math.Sin(2*math.Pi*173*tm) +
			0.17*math.Sin(2*math.Pi*347*tm+0.13) +
			0.09*math.Sin(2*math.Pi*521*tm+0.29)
		hybridPCM[i*channels] = float32(left)
		qextPCM[i*channels] = float32(0.45 * math.Sin(2*math.Pi*997*tm))
		if channels == 2 {
			right := 0.22*math.Sin(2*math.Pi*211*tm+0.19) +
				0.15*math.Sin(2*math.Pi*389*tm+0.31)
			hybridPCM[i*channels+1] = float32(right)
			qextPCM[i*channels+1] = float32(0.35 * math.Sin(2*math.Pi*997*tm+0.37))
		}
	}

	tmpDir := t.TempDir()
	inputPath := filepath.Join(tmpDir, "hybrid.f32")
	bitstreamPath := filepath.Join(tmpDir, "hybrid.bit")
	if err := benchutil.WriteRepeatedRawFloat32(inputPath, hybridPCM, 1); err != nil {
		t.Fatalf("WriteRepeatedRawFloat32(Hybrid): %v", err)
	}
	args := []string{
		"-e", "audio", "48000", fmt.Sprint(channels), "32000",
		"-f32", "-complexity", "10", "-bandwidth", "FB", "-framesize", "20",
		inputPath, bitstreamPath,
	}
	cmd := exec.Command(opusDemo, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("opus_demo hybrid encode failed: %v (%s)", err, bytes.TrimSpace(out))
	}
	hybridPacket, err := firstOpusDemoPacket(bitstreamPath)
	if err != nil {
		t.Fatalf("firstOpusDemoPacket(Hybrid): %v", err)
	}
	info, frames, _, _, err := parsePacketFramesAndPadding(hybridPacket)
	if err != nil {
		t.Fatalf("parsePacketFramesAndPadding(Hybrid): %v", err)
	}
	if info.TOC.Mode != ModeHybrid || info.TOC.Bandwidth != BandwidthFullband || len(frames) != 1 {
		t.Fatalf("Hybrid packet mode=%v bandwidth=%v frames=%d", info.TOC.Mode, info.TOC.Bandwidth, len(frames))
	}

	qextPacket := encodeLibopusPacketAtBitrate(t, opusDemo, channels, qextPCM, false, true, 96000)
	_, _, padding, nbFrames, err := parsePacketFramesAndPadding(qextPacket)
	if err != nil {
		t.Fatalf("parsePacketFramesAndPadding(QEXT): %v", err)
	}
	ext, ok, err := findPacketExtension(padding, nbFrames, qextPacketExtensionID)
	if err != nil {
		t.Fatalf("findPacketExtension(QEXT): %v", err)
	}
	if !ok || len(ext.Data) == 0 {
		t.Fatal("libopus packet missing QEXT payload")
	}

	packet := make([]byte, len(hybridPacket)+len(ext.Data)+64)
	n, err := buildRepacketizedPacketWithOptions(
		hybridPacket[0]&^byte(0x03),
		frames,
		packet,
		0,
		false,
		[]packetExtensionData{{
			ID:    qextPacketExtensionID,
			Frame: 0,
			Data:  ext.Data,
		}},
	)
	if err != nil {
		t.Fatalf("build hybrid QEXT packet: %v", err)
	}
	packet = packet[:n]

	builtInfo, _, builtPadding, builtFrames, err := parsePacketFramesAndPadding(packet)
	if err != nil {
		t.Fatalf("parsePacketFramesAndPadding(built): %v", err)
	}
	if builtInfo.TOC.Mode != ModeHybrid {
		t.Fatalf("built packet mode=%v want Hybrid", builtInfo.TOC.Mode)
	}
	if _, ok, err := findPacketExtension(builtPadding, builtFrames, qextPacketExtensionID); err != nil || !ok {
		t.Fatalf("built packet missing QEXT extension ok=%v err=%v", ok, err)
	}
	return packet
}

func TestNative96kEncoderFrameSizeBoundariesMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		for _, size := range []int{240, 480, 960, 1920, 3840, 5760, 7680, 9600, 11520} {
			t.Run(fmt.Sprintf("ch%d/samples%d", channels, size), func(t *testing.T) {
				params := libopustest.EncodeDiffParams{
					SampleRate: 96000, Channels: channels,
					Application: libopustest.EncodeDiffApplicationAudio,
					Bitrate:     64000, Complexity: 0, Signal: libopustest.EncodeDiffSignalAuto,
					VBR: true, VBRConstraint: true, FrameSize: size, FrameCount: 2,
					PCM: make([]float32, 2*size*channels),
				}
				for i := range params.PCM {
					params.PCM[i] = float32((i*37)%8192-4096) / 32768
				}
				want, err := libopustest.ProbeEncodeDiff(params)
				if err != nil {
					libopustest.HelperUnavailable(t, "96 kHz frame boundary", err)
				}
				if len(want) != 2 {
					t.Fatalf("oracle frames=%d, want 2", len(want))
				}
				enc, err := NewEncoder(EncoderConfig{SampleRate: 96000, Channels: channels, Application: ApplicationAudio})
				if err != nil {
					t.Fatal(err)
				}
				for _, err := range []error{enc.SetBitrate(64000), enc.SetComplexity(0), enc.SetFrameSize(size)} {
					if err != nil {
						t.Fatal(err)
					}
				}
				enc.SetVBR(true)
				enc.SetVBRConstraint(true)
				out := make([]byte, 4000)
				for frame := range 2 {
					if frame == 1 {
						for _, invalid := range []int{size - 1, size + 1} {
							bad := params
							bad.FrameSize, bad.FrameCount = invalid, 1
							bad.PCM = make([]float32, invalid*channels)
							records, err := libopustest.ProbeEncodeDiff(bad)
							if err != nil {
								t.Fatal(err)
							}
							if len(records) != 1 || records[0].Ret != -1 {
								t.Fatalf("libopus frame size %d: records=%+v, want OPUS_BAD_ARG", invalid, records)
							}
							rng := enc.FinalRange()
							if err := enc.SetFrameSize(invalid); err != ErrInvalidFrameSize {
								t.Fatalf("SetFrameSize(%d)=%v, want ErrInvalidFrameSize", invalid, err)
							}
							if enc.FrameSize() != size || enc.FinalRange() != rng {
								t.Fatal("rejected frame size changes encoder state")
							}
						}
					}
					pcm := params.PCM[frame*size*channels : (frame+1)*size*channels]
					n, err := enc.Encode(pcm, out)
					if err != nil || n != want[frame].Ret || !bytes.Equal(out[:n], want[frame].Packet) || enc.FinalRange() != want[frame].FinalRange {
						t.Fatalf("frame%d: Go bytes=%d range=%08x error=%v; C bytes=%d range=%08x", frame, n, enc.FinalRange(), err, want[frame].Ret, want[frame].FinalRange)
					}
				}
			})
		}
	}
}

type hd96kPacketLayout struct {
	toc         byte
	code        int
	countByte   byte
	hasPadding  bool
	nbFrames    int
	paddingLen  int // decoded total padding (= qext_bytes for the native path)
	padFieldLen int // bytes consumed by the padding-length field
	main        []byte
	extIDByte   byte // first byte of the extension region (0xF8 for QEXT)
	extID       int  // extIDByte >> 1
	qextPayload []byte
}

func parseHD96kLayout(t *testing.T, pkt []byte) hd96kPacketLayout {
	t.Helper()
	if len(pkt) < 2 {
		t.Fatalf("packet too short: %d", len(pkt))
	}
	var l hd96kPacketLayout
	l.toc = pkt[0]
	l.code = int(pkt[0] & 0x03)
	if l.code != 3 {
		// Code-0 (no QEXT) packet: main is everything after TOC.
		l.main = pkt[1:]
		return l
	}
	l.countByte = pkt[1]
	l.hasPadding = l.countByte&0x40 != 0
	l.nbFrames = int(l.countByte & 0x3F)
	offset := 2
	if l.hasPadding {
		start := offset
		for {
			if offset >= len(pkt) {
				t.Fatalf("padding overran packet")
			}
			b := int(pkt[offset])
			offset++
			if b == 255 {
				l.paddingLen += 254
			} else {
				l.paddingLen += b
				break
			}
		}
		l.padFieldLen = offset - start
	}
	end := len(pkt) - l.paddingLen
	if end < offset {
		t.Fatalf("bad framing: dataStart=%d end=%d", offset, end)
	}
	l.main = pkt[offset:end]
	if l.paddingLen > 0 {
		ext := pkt[end:]
		l.extIDByte = ext[0]
		l.extID = int(ext[0] >> 1)
		l.qextPayload = ext[1:]
	}
	return l
}

// TestHD96kFramingByteParityMatchesLibopus drives the public 96 kHz QEXT encode
// path and checks the assembled Opus packet framing against the libopus
// --enable-qext oracle: TOC byte, frame-count byte, padding-length field, the
// extension-ID byte (0xF8) and the byte offsets/lengths of the main and QEXT
// payload regions must all match exactly.
func TestHD96kFramingByteParityMatchesLibopus(t *testing.T) {
	const frameSize = 1920
	const bitrate = 256000

	for _, ch := range []int{1, 2} {
		ch := ch
		t.Run(map[int]string{1: "mono", 2: "stereo"}[ch], func(t *testing.T) {
			pcm := hd96kParitySine(ch, frameSize)

			res, err := libopustest.ProbeQEXTEncode96k(libopustest.QEXTEncode96kParams{
				Channels:      ch,
				FrameSize:     frameSize,
				Bitrate:       bitrate,
				Complexity:    10,
				VBR:           false,
				MaxPacketSize: 8000,
				PCM:           pcm,
				FrameCount:    1,
			})
			if err != nil {
				t.Fatalf("ProbeQEXTEncode96k: %v", err)
			}
			ref := parseHD96kLayout(t, res.Packets[0])

			enc, err := NewEncoder(EncoderConfig{
				SampleRate:  96000,
				Channels:    ch,
				Application: ApplicationRestrictedCelt,
			})
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			if err := enc.SetBitrate(bitrate); err != nil {
				t.Fatalf("SetBitrate: %v", err)
			}
			if err := enc.SetBitrateMode(BitrateModeCBR); err != nil {
				t.Fatalf("SetBitrateMode: %v", err)
			}
			if err := enc.SetComplexity(10); err != nil {
				t.Fatalf("SetComplexity: %v", err)
			}
			if err := enc.SetQEXT(true); err != nil {
				t.Fatalf("SetQEXT: %v", err)
			}

			out, err := enc.EncodeFloat32(pcm)
			if err != nil {
				t.Fatalf("EncodeFloat32: %v", err)
			}
			got := parseHD96kLayout(t, out)

			// TOC byte (config 31, stereo bit, code 3) must be identical.
			if got.toc != ref.toc {
				t.Errorf("TOC byte: got 0x%02x want 0x%02x", got.toc, ref.toc)
			}
			if got.code != ref.code {
				t.Errorf("frame code: got %d want %d", got.code, ref.code)
			}
			// Frame-count byte (padding flag | 1 frame) must match.
			if got.countByte != ref.countByte {
				t.Errorf("frame-count byte: got 0x%02x want 0x%02x", got.countByte, ref.countByte)
			}
			if got.hasPadding != ref.hasPadding {
				t.Errorf("padding flag: got %v want %v", got.hasPadding, ref.hasPadding)
			}
			if got.nbFrames != ref.nbFrames {
				t.Errorf("frame count: got %d want %d", got.nbFrames, ref.nbFrames)
			}
			// Padding-length field (qext_bytes) must be byte-identical.
			if got.paddingLen != ref.paddingLen {
				t.Errorf("padding length (qext_bytes): got %d want %d", got.paddingLen, ref.paddingLen)
			}
			if got.padFieldLen != ref.padFieldLen {
				t.Errorf("padding-length field bytes: got %d want %d", got.padFieldLen, ref.padFieldLen)
			}
			// Main CELT payload byte budget must match.
			if len(got.main) != len(ref.main) {
				t.Errorf("main payload length: got %d want %d", len(got.main), len(ref.main))
			}
			// Extension-ID byte (QEXT_EXTENSION_ID<<1 = 0xF8) must match.
			if got.extIDByte != ref.extIDByte {
				t.Errorf("extension-ID byte: got 0x%02x want 0x%02x", got.extIDByte, ref.extIDByte)
			}
			if got.extID != qextPacketExtensionID {
				t.Errorf("extension ID: got %d want %d", got.extID, qextPacketExtensionID)
			}
			// QEXT payload byte count must match.
			if len(got.qextPayload) != len(ref.qextPayload) {
				t.Errorf("QEXT payload length: got %d want %d", len(got.qextPayload), len(ref.qextPayload))
			}
			// Total packet length must match.
			if len(out) != len(res.Packets[0]) {
				t.Errorf("total packet length: got %d want %d", len(out), len(res.Packets[0]))
			}

			t.Logf("ch=%d framing: toc=0x%02x count=0x%02x qext_bytes=%d main=%d qext=%d total=%d",
				ch, got.toc, got.countByte, got.paddingLen, len(got.main), len(got.qextPayload), len(out))

			// Report how far the full-packet bytes match (diagnostic for the
			// upstream comb / band-data residual; framing above is the contract).
			refPkt := res.Packets[0]
			firstDiff := firstByteDiff(out, refPkt)
			if firstDiff < 0 {
				t.Logf("ch=%d: FULL PACKET byte-exact (%d bytes)", ch, len(out))
			} else {
				t.Logf("ch=%d: full packet diverges at byte %d (framing exact; main-payload residual)", ch, firstDiff)
			}
		})
	}
}

// TestHD96kFramingExtensionRoundtrip checks that the gopus native 96 kHz QEXT
// packet decodes through the gopus decoder without error and that the QEXT
// extension is correctly recovered from the padding region (framing is
// self-consistent on the decode side).
func TestHD96kFramingExtensionRoundtrip(t *testing.T) {
	const frameSize = 1920
	const bitrate = 256000

	for _, ch := range []int{1, 2} {
		ch := ch
		t.Run(fmt.Sprintf("%dch", ch), func(t *testing.T) {
			pcm := hd96kParitySine(ch, frameSize)

			enc, err := NewEncoder(EncoderConfig{
				SampleRate:  96000,
				Channels:    ch,
				Application: ApplicationRestrictedCelt,
			})
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			_ = enc.SetBitrate(bitrate)
			_ = enc.SetBitrateMode(BitrateModeCBR)
			_ = enc.SetComplexity(10)
			if err := enc.SetQEXT(true); err != nil {
				t.Fatalf("SetQEXT: %v", err)
			}
			out, err := enc.EncodeFloat32(pcm)
			if err != nil {
				t.Fatalf("EncodeFloat32: %v", err)
			}

			// The QEXT extension must be recoverable from the padding region.
			l := parseHD96kLayout(t, out)
			if l.code != 3 {
				t.Fatalf("expected code-3 packet, got code %d", l.code)
			}
			if l.extID != qextPacketExtensionID {
				t.Fatalf("QEXT extension ID not recovered: got %d", l.extID)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(96000, ch))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			pcmOut := make([]float32, frameSize*ch)
			n, err := dec.Decode(out, pcmOut)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if n != frameSize {
				t.Errorf("decoded %d samples want %d", n, frameSize)
			}
		})
	}
}

const (
	rootHD96kInputMagic  = "G96M"
	rootHD96kOutputMagic = "G96O"
)

var rootHD96kModeHelper libopustest.HelperCache

type rootHD96kRecord struct {
	packet  []byte
	range32 uint32
}

func buildRootHD96kModeHelper() (string, error) {
	return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
		Label:       "native 96 kHz encoder mode sequence",
		OutputBase:  "gopus_libopus_encoder_96k_mode_sequence",
		SourceFile:  "libopus_encoder_96k_mode_sequence.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"src", "celt", "silk", "silk/float"},
		DeadStrip:   true,
	})
}

func encodeRootHD96kWithLibopus(t *testing.T, qext bool, budgets []int, frames [][]float32) []rootHD96kRecord {
	t.Helper()
	if len(budgets) != len(frames) {
		t.Fatalf("oracle input has %d budgets and %d frames", len(budgets), len(frames))
	}
	if len(frames) == 0 {
		t.Fatal("oracle input has no frames")
	}
	frameSize := len(frames[0])
	qextFlag := uint32(0)
	if qext {
		qextFlag = 1
	}
	payload := libopustest.NewOraclePayloadVersion(rootHD96kInputMagic, 1,
		uint32(frameSize), uint32(len(frames)), qextFlag, 10, 24)
	for i, frame := range frames {
		if len(frame) != frameSize {
			t.Fatalf("frame %d has %d samples, want %d", i, len(frame), frameSize)
		}
		payload.U32(uint32(budgets[i]))
		payload.U32(256000)
		payload.Float32s(frame...)
	}
	bin, err := rootHD96kModeHelper.Path(buildRootHD96kModeHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "native 96 kHz mode sequence", err)
		return nil
	}
	reader, err := libopustest.RunOracleVersion(bin, payload.Bytes(), "native 96 kHz mode sequence", rootHD96kOutputMagic, 1)
	if err != nil {
		t.Fatalf("selected libopus encoder: %v", err)
	}
	count := reader.Count(len(frames))
	if count != len(frames) {
		t.Fatalf("selected C returned %d frames, want %d: %v", count, len(frames), reader.Err())
	}
	records := make([]rootHD96kRecord, count)
	for i := range records {
		n := int(reader.U32())
		records[i].range32 = reader.U32()
		if n < 1 || n > budgets[i] {
			t.Fatalf("selected C packet %d has length %d for budget %d", i, n, budgets[i])
		}
		records[i].packet = append([]byte(nil), reader.Bytes(n)...)
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return records
}

func TestRootNative96kModeBudgetSequenceMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const packetCapacity = 3825
	for _, frameSize := range []int{1920, 3840} {
		for _, qext := range []bool{false, true} {
			for _, prime := range []bool{false, true} {
				for _, lowBudget := range []int{16, 32} {
					name := fmt.Sprintf("frame%d/qext%t/primed%t/budget%d", frameSize, qext, prime, lowBudget)
					t.Run(name, func(t *testing.T) {
						budgets := []int{lowBudget, packetCapacity}
						if prime {
							budgets = append([]int{packetCapacity}, budgets...)
						}
						frames := makeRootHD96kPCM(frameSize, len(budgets))
						want := encodeRootHD96kWithLibopus(t, qext, budgets, frames)
						lowBudgetFrame := 0
						if prime {
							lowBudgetFrame = 1
						}
						if frameSize == 1920 && want[lowBudgetFrame].packet[0] != 0x48 {
							t.Fatalf("selected C low-budget frame TOC=%02x, want native 96 kHz SILK WB TOC 48", want[lowBudgetFrame].packet[0])
						}
						enc, err := NewEncoder(EncoderConfig{SampleRate: 96000, Channels: 1, Application: ApplicationAudio})
						if err != nil {
							t.Fatal(err)
						}
						if err := enc.SetBitrate(256000); err != nil {
							t.Fatal(err)
						}
						if err := enc.SetComplexity(10); err != nil {
							t.Fatal(err)
						}
						enc.SetVBR(true)
						enc.SetVBRConstraint(true)
						if err := enc.SetQEXT(qext); err != nil {
							t.Fatal(err)
						}
						if enc.FrameSize() != frameSize {
							if err := enc.SetFrameSize(frameSize); err != nil {
								t.Fatalf("SetFrameSize(%d): %v", frameSize, err)
							}
						}
						out := make([]byte, packetCapacity)
						for step, budget := range budgets {
							if err := enc.SetBitrate(256000); err != nil {
								t.Fatal(err)
							}
							n, err := enc.Encode(frames[step], out[:budget])
							if err != nil {
								t.Fatalf("step%d encode: %v", step, err)
							}
							if !bytes.Equal(out[:n], want[step].packet) {
								t.Fatalf("step%d budget%d packet Go=%x C=%x", step, budget, out[:n], want[step].packet)
							}
							if got := enc.FinalRange(); got != want[step].range32 {
								t.Fatalf("step%d range Go=%08x C=%08x", step, got, want[step].range32)
							}
						}
						encodeWarm := func() {
							for step, budget := range budgets {
								if _, err := enc.Encode(frames[step], out[:budget]); err != nil {
									panic(err)
								}
							}
						}
						if allocs := testing.AllocsPerRun(10, encodeWarm); allocs != 0 {
							t.Fatalf("warm allocations=%g", allocs)
						}
					})
				}
			}
		}
	}
}

func makeRootHD96kPCM(frameSize, frames int) [][]float32 {
	pcm := make([][]float32, frames)
	for frame := range frames {
		pcm[frame] = make([]float32, frameSize)
		for i := range frameSize {
			n := frame*frameSize + i
			x := 0.36*math.Sin(2*math.Pi*5300*float64(n)/96000) +
				0.16*math.Sin(2*math.Pi*11900*float64(n)/96000) +
				0.21*math.Sin(2*math.Pi*30000*float64(n)/96000)
			pcm[frame][i] = float32(int16(math.Round(x*32767))) / 32768
		}
	}
	return pcm
}

func hd96kParitySine(channels, frameSize int) []float32 {
	pcm := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		s := 0.4*math.Sin(2*math.Pi*6000*float64(i)/96000) +
			0.2*math.Sin(2*math.Pi*30000*float64(i)/96000)
		pcm[i*channels] = float32(s)
		if channels == 2 {
			pcm[i*channels+1] = float32(0.3 * s)
		}
	}
	return pcm
}

// refMainCELTPayload extracts the main CELT payload and the QEXT extension
// payload from a native 96 kHz code-3 reference Opus packet carrying a single
// CBR frame. Returns (mainPayload, qextPayload).
func refMainCELTPayload(t *testing.T, pkt []byte) (main, qext []byte) {
	t.Helper()
	if len(pkt) < 2 {
		t.Fatalf("ref packet too short: %d", len(pkt))
	}
	if pkt[0]&0x03 != 3 {
		t.Fatalf("ref packet not code 3: toc=0x%02x", pkt[0])
	}
	fc := pkt[1]
	vbr := fc&0x80 != 0
	hasPad := fc&0x40 != 0
	m := int(fc & 0x3f)
	if m != 1 {
		t.Fatalf("expected single frame, got m=%d", m)
	}
	if vbr {
		t.Fatalf("expected CBR ref packet")
	}
	offset := 2
	padding := 0
	if hasPad {
		for {
			if offset >= len(pkt) {
				t.Fatalf("padding overran packet")
			}
			b := int(pkt[offset])
			offset++
			if b == 255 {
				padding += 254
			} else {
				padding += b
				break
			}
		}
	}
	// Single CBR frame: the frame data runs from offset to len-padding.
	end := len(pkt) - padding
	if end < offset {
		t.Fatalf("bad framing: offset=%d end=%d", offset, end)
	}
	main = pkt[offset:end]

	if hasPad && padding > 0 {
		var payloads [maxRepacketizerFrames][]byte
		collectQEXTPacketExtensions(pkt[len(pkt)-padding:], m, qextPacketExtensionID, &payloads)
		qext = payloads[0]
	}
	return main, qext
}

// TestHD96kNativeEncodeMainPayloadParity compares the native 96 kHz CELT main
// payload and QEXT side payload for one fullband CBR frame against the selected
// libopus reference. It checks mono and stereo and fails at the first differing
// byte.
func TestHD96kNativeEncodeMainPayloadParity(t *testing.T) {
	const frameSize = 1920
	const bitrate = 256000
	for _, ch := range []int{1, 2} {
		ch := ch
		t.Run(map[int]string{1: "mono", 2: "stereo"}[ch], func(t *testing.T) {
			pcm := hd96kParitySine(ch, frameSize)

			res, err := libopustest.ProbeQEXTEncode96k(libopustest.QEXTEncode96kParams{
				Channels:      ch,
				FrameSize:     frameSize,
				Bitrate:       bitrate,
				Complexity:    10,
				VBR:           false,
				MaxPacketSize: 8000,
				PCM:           pcm,
				FrameCount:    1,
			})
			if err != nil {
				t.Fatalf("ProbeQEXTEncode96k: %v", err)
			}
			refMain, refQext := refMainCELTPayload(t, res.Packets[0])

			e := celt.NewEncoder(ch)
			// QEXT path: libopus copies raw PCM into the CELT buffer (no dc_reject,
			// no delay compensation). RESTRICTED_LOWDELAY only forces CELT-only; it
			// does NOT disable the prefilter, so leave CELT prediction at default.
			e.SetDCRejectEnabled(false)
			e.SetLSBQuantizationEnabled(false)
			e.SetDelayCompensationEnabled(false)
			e.SetVBR(false)
			e.SetComplexity(10)
			e.SetBitrate(bitrate)
			e.SetLSBDepth(24)
			e.SetQEXTEnabled(true)
			e.EnableHD96kMode()

			gotMain, err := e.EncodeFrame(pcm, frameSize)
			if err != nil {
				t.Fatalf("ch=%d: EncodeFrame: %v", ch, err)
			}
			gotQext := e.LastQEXTPayload()

			if len(gotMain) != len(refMain) {
				t.Logf("ch=%d main byte budget got=%d ref=%d", ch, len(gotMain), len(refMain))
			}
			diagFlags(t, "got", gotMain)
			diagFlags(t, "ref", refMain)

			firstMainDiff := firstByteDiff(gotMain, refMain)
			firstQextDiff := firstByteDiff(gotQext, refQext)
			// Both mono and stereo native-96k encode are byte-exact (main + QEXT);
			// any mismatch is a real correctness regression.
			if firstMainDiff >= 0 {
				dumpAround(t, "main", gotMain, refMain, firstMainDiff)
				t.Fatalf("ch=%d main payload diverges at byte %d (got len=%d ref len=%d)",
					ch, firstMainDiff, len(gotMain), len(refMain))
			}
			if firstQextDiff >= 0 {
				dumpAround(t, "qext", gotQext, refQext, firstQextDiff)
				t.Fatalf("ch=%d qext payload diverges at byte %d (got len=%d ref len=%d)",
					ch, firstQextDiff, len(gotQext), len(refQext))
			}
		})
	}
}

func diagFlags(t *testing.T, label string, payload []byte) {
	t.Helper()
	var d rangecoding.Decoder
	d.Init(payload)
	silence := d.DecodeBit(15)
	pf := d.DecodeBit(1)
	transient := d.DecodeBit(3)
	intra := d.DecodeBit(3)
	t.Logf("%s flags: silence=%d postfilter=%d transient=%d intra=%d tell=%d",
		label, silence, pf, transient, intra, d.Tell())
}

func dumpAround(t *testing.T, label string, got, ref []byte, at int) {
	t.Helper()
	lo := at - 4
	if lo < 0 {
		lo = 0
	}
	hiG := at + 8
	if hiG > len(got) {
		hiG = len(got)
	}
	hiR := at + 8
	if hiR > len(ref) {
		hiR = len(ref)
	}
	t.Logf("%s got[%d:%d]=% x", label, lo, hiG, got[lo:hiG])
	t.Logf("%s ref[%d:%d]=% x", label, lo, hiR, ref[lo:hiR])
}

func sin96kPCM(channels, frameSize int) []float32 {
	pcm := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		phase := 2 * math.Pi * 997 * float64(i) / 96000.0
		pcm[i*channels] = float32(0.45 * math.Sin(phase))
		if channels == 2 {
			pcm[i*channels+1] = float32(0.35 * math.Sin(phase+0.37))
		}
	}
	return pcm
}

// TestHD96kEncoderConstructorAcceptsRate verifies that NewEncoder at 96 kHz
// succeeds under gopus_qext, SampleRate() returns 96000, and FrameSize()
// returns 1920 (20ms at 96 kHz).
func TestHD96kEncoderConstructorAcceptsRate(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  96000,
		Channels:    1,
		Application: ApplicationAudio,
	})
	if err != nil {
		t.Fatalf("NewEncoder(96000) unexpected error: %v", err)
	}
	if enc == nil {
		t.Fatal("NewEncoder(96000) returned nil")
	}
	if got := enc.SampleRate(); got != 96000 {
		t.Errorf("SampleRate() = %d, want 96000", got)
	}
	if got := enc.FrameSize(); got != 1920 {
		t.Errorf("FrameSize() = %d, want 1920 (20ms at 96 kHz)", got)
	}
}

// TestHD96kEncoderStereoConstructor verifies stereo at 96 kHz.
func TestHD96kEncoderStereoConstructor(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  96000,
		Channels:    2,
		Application: ApplicationAudio,
	})
	if err != nil {
		t.Fatalf("NewEncoder(96000, 2) unexpected error: %v", err)
	}
	if enc.Channels() != 2 {
		t.Errorf("Channels() = %d, want 2", enc.Channels())
	}
}

// TestHD96kDecoderConstructorAcceptsRate verifies that NewDecoder at 96 kHz
// succeeds under gopus_qext, SampleRate() returns 96000.
func TestHD96kDecoderConstructorAcceptsRate(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(96000, 1))
	if err != nil {
		t.Fatalf("NewDecoder(96000) unexpected error: %v", err)
	}
	if dec == nil {
		t.Fatal("NewDecoder(96000) returned nil")
	}
	if got := dec.SampleRate(); got != 96000 {
		t.Errorf("SampleRate() = %d, want 96000", got)
	}
	if got := dec.Channels(); got != 1 {
		t.Errorf("Channels() = %d, want 1", got)
	}
}

// TestHD96kEncoderProducesValidPackets verifies that a 96 kHz encoder produces
// non-empty packets with a CELT TOC.
func TestHD96kEncoderProducesValidPackets(t *testing.T) {
	for _, channels := range []int{1, 2} {
		t.Run(itoaSmall(channels)+"ch", func(t *testing.T) {
			enc, err := NewEncoder(EncoderConfig{
				SampleRate:  96000,
				Channels:    channels,
				Application: ApplicationAudio,
			})
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			// 20ms at 96 kHz = 1920 samples
			pcm := sin96kPCM(channels, 1920)
			buf := make([]byte, 4000)
			n, err := enc.Encode(pcm, buf)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if n == 0 {
				t.Fatal("Encode returned empty packet")
			}
			pkt := buf[:n]
			// Verify it's a parseable Opus packet.
			toc := ParseTOC(pkt[0])
			if toc.Mode != ModeCELT {
				t.Errorf("expected CELT mode, got %v", toc.Mode)
			}
		})
	}
}

// TestHD96kRoundtripNonZeroAudio verifies that encoding a 96 kHz sine wave
// and decoding it at 96 kHz produces non-zero output samples.
func TestHD96kRoundtripNonZeroAudio(t *testing.T) {
	for _, channels := range []int{1, 2} {
		t.Run(itoaSmall(channels)+"ch", func(t *testing.T) {
			enc, err := NewEncoder(EncoderConfig{
				SampleRate:  96000,
				Channels:    channels,
				Application: ApplicationAudio,
			})
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			if err := enc.SetBitrate(256000); err != nil {
				t.Fatalf("SetBitrate: %v", err)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(96000, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}

			pcm96in := sin96kPCM(channels, 1920)
			buf := make([]byte, 4000)
			n, err := enc.Encode(pcm96in, buf)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}

			pcm96out := make([]float32, 1920*channels)
			nDecoded, err := dec.Decode(buf[:n], pcm96out)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			// Expect 1920 samples at 96 kHz (20ms).
			if nDecoded != 1920 {
				t.Errorf("Decode returned %d samples, want 1920", nDecoded)
			}
			// Verify non-zero output (sine wave encoded; should not be silence).
			nonZero := 0
			for _, s := range pcm96out[:nDecoded*channels] {
				if s != 0 {
					nonZero++
				}
			}
			if nonZero == 0 {
				t.Error("decoded 96 kHz audio is all-zero; expected non-zero for sine input")
			}
		})
	}
}

// TestHD96kDecoderLastPacketDurationIs96kSamples verifies that after decoding
// a standard CELT packet at 96 kHz, LastPacketDuration() returns the 96 kHz
// sample count (1920 for a 20ms packet).
func TestHD96kDecoderLastPacketDurationIs96kSamples(t *testing.T) {
	// Build a 48 kHz encoder to produce a standard CELT packet.
	enc48, err := NewEncoder(EncoderConfig{
		SampleRate:  48000,
		Channels:    1,
		Application: ApplicationAudio,
	})
	if err != nil {
		t.Fatalf("NewEncoder 48k: %v", err)
	}
	pcm48 := make([]float32, 960)
	for i := range pcm48 {
		pcm48[i] = float32(math.Sin(2 * math.Pi * 440 * float64(i) / 48000.0))
	}
	buf := make([]byte, 4000)
	n, err := enc48.Encode(pcm48, buf)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	packet := buf[:n]

	// Decode the 48 kHz packet with a 96 kHz decoder.
	dec96, err := NewDecoder(DefaultDecoderConfig(96000, 1))
	if err != nil {
		t.Fatalf("NewDecoder 96k: %v", err)
	}
	out96 := make([]float32, 1920)
	nDecoded, err := dec96.Decode(packet, out96)
	if err != nil {
		t.Fatalf("Decode 96k: %v", err)
	}
	if nDecoded != 1920 {
		t.Errorf("Decode 96k returned %d samples, want 1920 (20ms at 96 kHz)", nDecoded)
	}
	if got := dec96.LastPacketDuration(); got != 1920 {
		t.Errorf("LastPacketDuration() = %d, want 1920", got)
	}
}

// TestHD96kEncoderFrameSizeSetGet verifies SetFrameSize and FrameSize at 96 kHz.
// Frame sizes count samples per channel at 96 kHz.
func TestHD96kEncoderFrameSizeSetGet(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  96000,
		Channels:    1,
		Application: ApplicationAudio,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}

	// Default: 20ms = 1920 at 96 kHz.
	if got := enc.FrameSize(); got != 1920 {
		t.Errorf("default FrameSize() = %d, want 1920", got)
	}

	// Set to 40ms = 3840 at 96 kHz.
	if err := enc.SetFrameSize(3840); err != nil {
		t.Fatalf("SetFrameSize(3840): %v", err)
	}
	if got := enc.FrameSize(); got != 3840 {
		t.Errorf("FrameSize() after SetFrameSize(3840) = %d, want 3840", got)
	}

	// Reset back to 20ms.
	if err := enc.SetFrameSize(1920); err != nil {
		t.Fatalf("SetFrameSize(1920): %v", err)
	}
	if got := enc.FrameSize(); got != 1920 {
		t.Errorf("FrameSize() after SetFrameSize(1920) = %d, want 1920", got)
	}
}

// TestHD96kDecodeWrongBufferSizeErrors verifies ErrBufferTooSmall is returned
// when the output buffer is smaller than the 96 kHz frame size.
func TestHD96kDecodeWrongBufferSizeErrors(t *testing.T) {
	// Build a 48 kHz packet.
	enc48, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	pcm48 := make([]float32, 960)
	buf := make([]byte, 4000)
	n, err := enc48.Encode(pcm48, buf)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	packet := buf[:n]

	dec96, err := NewDecoder(DefaultDecoderConfig(96000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	// A 48 kHz packet decoded at 96 kHz needs 1920 samples.
	// Providing only 960 should fail.
	small := make([]float32, 960)
	_, err = dec96.Decode(packet, small)
	if err != ErrBufferTooSmall {
		t.Errorf("Decode with too-small buffer: got %v, want ErrBufferTooSmall", err)
	}
}

func TestMultistreamEncodeInt2496kLongFramesMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate     = 96000
		bitrate        = 256000
		complexity     = 9
		packetCapacity = 4000
	)
	frameSizes := []int{7680, 9600, 11520}
	layouts := []multistreamInt24CapacityLayout{
		{name: "explicit_mono", channels: 1, streams: 1, mapping: []byte{0}},
		{name: "default_mono", channels: 1, streams: 1, mapping: []byte{0}, useDefault: true},
		{name: "explicit_stereo", channels: 2, streams: 1, coupledStreams: 1, mapping: []byte{0, 1}},
		{name: "default_stereo", channels: 2, streams: 1, coupledStreams: 1, mapping: []byte{0, 1}, useDefault: true},
	}
	signals := []struct {
		name    string
		nonzero bool
	}{
		{name: "silence"},
		{name: "signed_24bit_low_bits", nonzero: true},
	}

	for _, signal := range signals {
		t.Run(signal.name, func(t *testing.T) {
			for _, layout := range layouts {
				t.Run(layout.name, func(t *testing.T) {
					for _, frameSize := range frameSizes {
						t.Run(fmt.Sprintf("%d_samples", frameSize), func(t *testing.T) {
							enc := newMultistreamInt24CapacityEncoder(t, layout)
							if err := enc.SetFrameSize(frameSize); err != nil {
								t.Fatalf("SetFrameSize(%d): %v", frameSize, err)
							}
							if err := enc.SetBitrate(bitrate); err != nil {
								t.Fatalf("SetBitrate(%d): %v", bitrate, err)
							}
							if err := enc.SetComplexity(complexity); err != nil {
								t.Fatalf("SetComplexity(%d): %v", complexity, err)
							}

							pcm := multistreamInt24CapacityPCM(frameSize, layout.channels, signal.nonzero)
							want, err := libopustest.ProbeMultistreamInt24Encode96k(libopustest.MultistreamInt24Encode96kParams{
								Channels:       layout.channels,
								Streams:        layout.streams,
								CoupledStreams: layout.coupledStreams,
								Mapping:        layout.mapping,
								FrameSize:      frameSize,
								PacketCapacity: packetCapacity,
								Bitrate:        bitrate,
								Complexity:     complexity,
								PCM:            pcm,
							})
							if err != nil {
								libopustest.HelperUnavailable(t, "selected QEXT multistream int24 encode", err)
							}

							packet := make([]byte, packetCapacity)
							n, err := enc.EncodeInt24(pcm, packet)
							if err != nil {
								t.Fatalf("EncodeInt24: %v", err)
							}
							if n != len(want.Packet) || !bytes.Equal(packet[:n], want.Packet) {
								t.Fatalf("packet differs from selected C: Go len=%d C len=%d first diff=%d", n, len(want.Packet), firstByteDiff(packet[:n], want.Packet))
							}
							if want.Samples != frameSize {
								t.Fatalf("selected C samples=%d want %d", want.Samples, frameSize)
							}
							if got := enc.FinalRange(); got != want.FinalRange {
								t.Fatalf("final range Go=%08x selected C=%08x", got, want.FinalRange)
							}

							// Warm the same caller-owned PCM, packet, and encoder state before
							// measuring the public long-frame int24 path.
							if _, err := enc.EncodeInt24(pcm, packet); err != nil {
								t.Fatalf("warm EncodeInt24: %v", err)
							}
							var encodeErr error
							allocs := testing.AllocsPerRun(30, func() {
								_, encodeErr = enc.EncodeInt24(pcm, packet)
							})
							if encodeErr != nil {
								t.Fatalf("EncodeInt24 during allocation measurement: %v", encodeErr)
							}
							if allocs != 0 {
								t.Fatalf("warmed 96 kHz %d-sample EncodeInt24 (%s) allocated %g times", frameSize, signal.name, allocs)
							}
						})
					}
				})
			}
		})
	}
}

type multistreamInt24CapacityLayout struct {
	name           string
	channels       int
	streams        int
	coupledStreams int
	mapping        []byte
	useDefault     bool
}

func multistreamInt24CapacityPCM(frameSize, channels int, nonzero bool) []int32 {
	pcm := make([]int32, frameSize*channels)
	if !nonzero {
		return pcm
	}
	for i := range pcm {
		// The odd step keeps every sample's low bit meaningful across the
		// signed 24-bit range without relying on a generated audio fixture.
		pcm[i] = int32((i*7919+12345)&0x7fffff) - 1<<22
	}
	return pcm
}

func newMultistreamInt24CapacityEncoder(t *testing.T, layout multistreamInt24CapacityLayout) *MultistreamEncoder {
	t.Helper()
	if layout.useDefault {
		enc, err := NewMultistreamEncoderDefault(96000, layout.channels, ApplicationAudio)
		if err != nil {
			t.Fatalf("NewMultistreamEncoderDefault(96000, %d): %v", layout.channels, err)
		}
		return enc
	}
	enc, err := NewMultistreamEncoder(96000, layout.channels, layout.streams, layout.coupledStreams, layout.mapping, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewMultistreamEncoder(96000, %d, %d, %d): %v", layout.channels, layout.streams, layout.coupledStreams, err)
	}
	return enc
}

func TestQEXTActiveStereoDecodeMatchesLibopus(t *testing.T) {
	packets := testQEXTStatefulPacketsWithSizeMatchLibopus(t, 960, 3, 2, 256000, BitrateModeCVBR, "-cvbr", true)
	compareQEXTDecodeSequenceWithLibopus(t, 2, 960, packets)

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]float32, 1920)
	for _, packet := range packets {
		if n, err := dec.Decode(packet, pcm); err != nil || n != 960 {
			t.Fatalf("warm active QEXT decode: samples=%d err=%v", n, err)
		}
	}
	var decoded int
	var decodeErr error
	allocs := testing.AllocsPerRun(20, func() {
		decoded, decodeErr = dec.Decode(packets[0], pcm)
	})
	if decoded != 960 || decodeErr != nil {
		t.Fatalf("measured active QEXT decode: samples=%d err=%v", decoded, decodeErr)
	}
	if allocs != 0 {
		t.Fatalf("warm active QEXT decode allocs/call=%g, want 0", allocs)
	}
}

func qextParseExtensionRegion(packet []byte) (tocCode, paddingLen, extPayloadBytes int, extIDPresent, ok bool) {
	if len(packet) < 2 {
		return
	}
	tocCode = int(packet[0] & 0x03)
	if tocCode != 3 {
		ok = true // code-0/1/2 packets: no extension region (QEXT absent)
		return
	}
	offset := 2
	for offset < len(packet) {
		b := int(packet[offset])
		offset++
		paddingLen += b
		if b != 255 {
			break
		}
	}
	if paddingLen <= 0 || len(packet)-paddingLen < 0 {
		return
	}
	ext := packet[len(packet)-paddingLen:]
	if len(ext) == 0 {
		return
	}
	const qextIDByteValue = 0xF8 // QEXT_EXTENSION_ID<<1 = 124<<1 = 0xF8
	extIDPresent = ext[0] == qextIDByteValue
	if extIDPresent {
		extPayloadBytes = paddingLen - 1 // one byte is the extension ID itself
	}
	ok = true
	return
}

// qextSinePCM returns a single-period 997 Hz sine for the given channels and
// frameSize at 48 kHz.  This matches the PCM used across decoder_qext_test.go
// so tests are comparable.
func qextSinePCM(channels, frameSize int) []float32 {
	pcm := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		phase := 2 * math.Pi * 997 * float64(i) / 48000.0
		pcm[i*channels] = float32(0.45 * math.Sin(phase))
		if channels == 2 {
			pcm[i*channels+1] = float32(0.35 * math.Sin(phase+0.37))
		}
	}
	return pcm
}

// TestQEXTCBRExtensionFramingByteParityMatchesLibopus compares complete CBR
// CELT QEXT packet bytes and final ranges with the paired libopus oracle.
//
// The underlying fix is in celt/celt_encoder.c lines 2543–2556: for CBR the
// QEXT reservation calls compute_vbr(tf2=min(1,2*tf)) to compute the natural
// VBR target rather than using the naive nbCompressedBytes−qextBytes/3
// shortcut.  This test gates that behaviour from regressing.
func TestQEXTCBRExtensionFramingByteParityMatchesLibopus(t *testing.T) {
	opusDemo, err := libopustest.PublicAPIOpusDemoPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "paired QEXT opus_demo", err)
		return
	}

	cases := []struct {
		channels int
		bitrate  int
		wantQEXT bool // QEXT is expected to activate at this config
	}{
		{1, 256000, true},
		{2, 256000, true},
		{1, 128000, true},
		{1, 96000, true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(fmt.Sprintf("%dch-%dk", tc.channels, tc.bitrate/1000), func(t *testing.T) {
			pcm := qextSinePCM(tc.channels, 960)

			refPacket, refRange := encodeLibopusPacketWithFinalRangeAtBitrate(t, opusDemo, tc.channels, pcm, true, true, tc.bitrate)
			if len(refPacket) == 0 {
				t.Fatal("libopus returned empty packet")
			}
			refCode, refPaddingLen, refExtBytes, refHasExt, refOK := qextParseExtensionRegion(refPacket)
			if !refOK {
				t.Fatalf("could not parse libopus packet: %x", refPacket[:min(16, len(refPacket))])
			}

			enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: tc.channels, Application: ApplicationRestrictedCelt})
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			if err := enc.SetBandwidth(BandwidthFullband); err != nil {
				t.Fatalf("SetBandwidth: %v", err)
			}
			if err := enc.SetBitrate(tc.bitrate); err != nil {
				t.Fatalf("SetBitrate: %v", err)
			}
			if err := enc.SetBitrateMode(BitrateModeCBR); err != nil {
				t.Fatalf("SetBitrateMode: %v", err)
			}
			if err := enc.SetComplexity(10); err != nil {
				t.Fatalf("SetComplexity: %v", err)
			}
			if err := enc.SetFrameSize(960); err != nil {
				t.Fatalf("SetFrameSize: %v", err)
			}
			if err := enc.SetQEXT(true); err != nil {
				t.Fatalf("SetQEXT: %v", err)
			}
			// opus_demo's -f32 input converts to signed 24-bit samples with
			// floor(.5 + sample*8388608) before calling opus_encode24.
			pcm24 := make([]int32, len(pcm))
			for i, sample := range pcm {
				pcm24[i] = int32(math.Floor(0.5 + float64(sample*8388608)))
			}
			out := make([]byte, 1276)
			n, err := enc.EncodeInt24(pcm24, out)
			if err != nil {
				t.Fatalf("gopus Encode: %v", err)
			}
			gopusPacket := out[:n]
			if len(gopusPacket) == 0 {
				t.Fatal("gopus returned empty packet")
			}
			if got := enc.FinalRange(); got != refRange {
				t.Errorf("final range: gopus=%08x libopus=%08x", got, refRange)
			}
			if !bytes.Equal(gopusPacket, refPacket) {
				t.Errorf("complete packet: first difference at byte %d, gopus len=%d libopus len=%d", firstDiffByte(gopusPacket, refPacket), len(gopusPacket), len(refPacket))
			}
			gopusCode, gopusPaddingLen, gopusExtBytes, gopusHasExt, gopusOK := qextParseExtensionRegion(gopusPacket)
			if !gopusOK {
				t.Fatalf("could not parse gopus packet: %x", gopusPacket[:min(16, len(gopusPacket))])
			}

			// TOC code must match.
			if gopusCode != refCode {
				t.Errorf("TOC code: gopus=%d ref=%d", gopusCode, refCode)
			}
			// QEXT extension presence must match.
			if gopusHasExt != refHasExt {
				t.Errorf("QEXT extID present: gopus=%v ref=%v", gopusHasExt, refHasExt)
			}
			if tc.wantQEXT && !refHasExt {
				t.Errorf("expected QEXT extension in libopus packet but not found")
			}
			if !refHasExt {
				return // both correctly omit QEXT
			}
			// Padding length must be byte-identical (encodes qext byte count).
			if gopusPaddingLen != refPaddingLen {
				t.Errorf("paddingLen: gopus=%d ref=%d (qext payload: gopus=%d ref=%d)",
					gopusPaddingLen, refPaddingLen, gopusExtBytes, refExtBytes)
			}
			// Extension payload byte count must match exactly.
			if gopusExtBytes != refExtBytes {
				t.Errorf("QEXT ext payload bytes: gopus=%d ref=%d", gopusExtBytes, refExtBytes)
			}
		})
	}
}

// TestQEXTExtensionAbsentBelowThresholdMatchesLibopus verifies that gopus
// omits the QEXT extension for configurations where the packet budget is too
// small to activate QEXT.  This reflects the packet-space threshold check:
// celt/celt_encoder.c lines 2539–2540: qext_bytes = 0 when
// (nbCompressedBytes − offset)*4/5 ≤ 20, and no extension is written.
//
// For 1-ch 64 kbps / 20 ms: nbCompressedBytes = 159, offset = 200,
// (159−200)*4/5 < 0 → qext_bytes = 0 → no QEXT extension.
// For 2-ch 128 kbps / 20 ms: nbCompressedBytes = 319, offset = 400, same logic.
func TestQEXTExtensionAbsentBelowThresholdMatchesLibopus(t *testing.T) {
	opusDemo, err := libopustest.PublicAPIOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	cases := []struct {
		channels int
		bitrate  int
	}{
		{1, 64000},  // 1ch 64kbps: nbCompressedBytes=159 < offset=200 → no QEXT
		{2, 128000}, // 2ch 128kbps: nbCompressedBytes=319 < offset=400 → no QEXT
	}

	for _, tc := range cases {
		tc := tc
		t.Run(fmt.Sprintf("%dch-%dk", tc.channels, tc.bitrate/1000), func(t *testing.T) {
			pcm := qextSinePCM(tc.channels, 960)

			// Use the correct bitrate for the oracle call, not the default 256kbps.
			refPacket := encodeLibopusPacketAtBitrate(t, opusDemo, tc.channels, pcm, true, true, tc.bitrate)
			_, _, _, refHasExt, _ := qextParseExtensionRegion(refPacket)
			if refHasExt {
				t.Skipf("libopus activated QEXT for %dch %dbps (expected absent); skip threshold test",
					tc.channels, tc.bitrate)
			}

			enc := internalenc.NewEncoder(48000, tc.channels)
			enc.SetMode(internalenc.ModeCELT)
			enc.SetBandwidth(types.BandwidthFullband)
			enc.SetBitrate(tc.bitrate)
			enc.SetBitrateMode(internalenc.ModeCBR)
			enc.SetComplexity(10)
			enc.SetQEXT(true)

			gopusPacket, err := enc.Encode(pcm, 960)
			if err != nil {
				t.Fatalf("gopus Encode: %v", err)
			}
			_, _, _, gopusHasExt, _ := qextParseExtensionRegion(gopusPacket)
			if gopusHasExt {
				t.Errorf("gopus added QEXT extension but libopus did not (packet: %x)",
					gopusPacket[:min(8, len(gopusPacket))])
			}
		})
	}
}

// TestQEXTCBRExtensionSizeExactMatchesLibopus verifies that the QEXT extension
// payload byte count from gopus matches the libopus oracle exactly for a
// representative CBR matrix (channel × bitrate).
func TestQEXTCBRExtensionSizeExactMatchesLibopus(t *testing.T) {
	opusDemo, err := libopustest.PublicAPIOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	type testCase struct {
		channels int
		bitrate  int
	}
	cases := []testCase{
		{1, 128000},
		{1, 256000},
		{2, 256000},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(fmt.Sprintf("%dch-%dk", tc.channels, tc.bitrate/1000), func(t *testing.T) {
			pcm := qextSinePCM(tc.channels, 960)

			refPacket := encodeLibopusPacketAtBitrate(t, opusDemo, tc.channels, pcm, true, true, tc.bitrate)
			_, refPaddingLen, refExtBytes, refHasExt, _ := qextParseExtensionRegion(refPacket)
			if !refHasExt {
				t.Skipf("libopus did not produce QEXT extension for %dch %dbps", tc.channels, tc.bitrate)
			}

			enc := internalenc.NewEncoder(48000, tc.channels)
			enc.SetMode(internalenc.ModeCELT)
			enc.SetBandwidth(types.BandwidthFullband)
			enc.SetBitrate(tc.bitrate)
			enc.SetBitrateMode(internalenc.ModeCBR)
			enc.SetComplexity(10)
			enc.SetQEXT(true)

			gopusPacket, err := enc.Encode(pcm, 960)
			if err != nil {
				t.Fatalf("gopus Encode: %v", err)
			}
			_, gopusPaddingLen, gopusExtBytes, gopusHasExt, _ := qextParseExtensionRegion(gopusPacket)
			if !gopusHasExt {
				t.Fatalf("gopus did not produce QEXT extension for %dch %dbps", tc.channels, tc.bitrate)
			}

			if gopusPaddingLen != refPaddingLen {
				t.Errorf("paddingLen mismatch: gopus=%d ref=%d", gopusPaddingLen, refPaddingLen)
			}
			if gopusExtBytes != refExtBytes {
				t.Errorf("QEXT payload bytes: gopus=%d ref=%d", gopusExtBytes, refExtBytes)
			}
		})
	}
}

// TestQEXTMultistreamEncoderProducesQEXTExtension verifies that the public
// gopus multistream encoder, when QEXT is enabled, produces packets that
// contain a QEXT extension and that a multistream decoder can decode them to
// audio without error.
func TestQEXTMultistreamEncoderProducesQEXTExtension(t *testing.T) {
	const sampleRate = 48000
	const frameSize = 960

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			streams := 1
			coupledStreams := 0
			mapping := []byte{0}
			if channels == 2 {
				coupledStreams = 1
				mapping = []byte{0, 1}
			}

			mse, err := NewMultistreamEncoder(sampleRate, channels, streams, coupledStreams, mapping, ApplicationRestrictedCelt)
			if err != nil {
				t.Fatalf("NewMultistreamEncoder: %v", err)
			}
			if err := mse.SetBitrate(256000); err != nil {
				t.Fatalf("SetBitrate: %v", err)
			}
			if err := mse.SetBitrateMode(BitrateModeCBR); err != nil {
				t.Fatalf("SetBitrateMode: %v", err)
			}
			if err := mse.SetQEXT(true); err != nil {
				t.Fatalf("SetQEXT: %v", err)
			}

			pcm := qextSinePCM(channels, frameSize)

			buf := make([]byte, 8192)
			n, err := mse.Encode(pcm, buf)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			packet := buf[:n]
			if len(packet) == 0 {
				t.Fatal("Encode returned empty packet")
			}

			// Verify QEXT extension is present in the encoded packet.
			_, _, _, hasExt, ok := qextParseExtensionRegion(packet)
			if !ok {
				t.Fatalf("qextParseExtensionRegion failed for packet: %x", packet[:min(8, len(packet))])
			}
			if !hasExt {
				t.Error("multistream encoder with QEXT enabled did not produce QEXT extension")
			}

			// Decode the packet and verify audio is non-empty.
			msd, err := NewMultistreamDecoder(sampleRate, channels, streams, coupledStreams, mapping)
			if err != nil {
				t.Fatalf("NewMultistreamDecoder: %v", err)
			}
			pcmOut := make([]float32, frameSize*channels)
			gotN, err := msd.Decode(packet, pcmOut)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if gotN != frameSize {
				t.Errorf("decoded %d samples want %d", gotN, frameSize)
			}

			// Decoded samples must be non-zero (we encoded a sine wave).
			nonZero := 0
			for _, s := range pcmOut[:gotN*channels] {
				if s != 0 {
					nonZero++
				}
			}
			if nonZero == 0 {
				t.Error("decoded audio is all-zero for a sine wave input")
			}
		})
	}
}

// TestQEXTMultistreamDecodeMatchesLibopusOracle verifies that the gopus
// multistream decoder applied to a libopus-encoded QEXT packet (from the oracle
// binary) produces audio matching the per-stream libopus oracle decode.
// This is the multistream QEXT decode parity test.
func TestQEXTMultistreamDecodeMatchesLibopusOracle(t *testing.T) {
	opusDemo, err := libopustest.PublicAPIOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	// Reuse the per-stream oracle helper from multistream/qext_decoder_test.go
	// approach: build a 2-stream multistream packet from two libopus QEXT
	// packets and decode with gopus multistream decoder.

	for _, coupledChannels := range []int{1, 2} {
		coupledChannels := coupledChannels
		t.Run(fmt.Sprintf("coupled%dch", coupledChannels), func(t *testing.T) {
			// Build a single-stream (coupledChannels channels) packet from the oracle.
			// Use CBR 256kbps to ensure QEXT activates for all channel counts and
			// the extension region starts with the 0xF8 QEXT ID byte (code-3 packet).
			pcm := qextSinePCM(coupledChannels, 960)
			refPacket := encodeLibopusPacketAtBitrate(t, opusDemo, coupledChannels, pcm, true, true, 256000)

			// Use findPacketExtension to confirm QEXT is present.
			_, _, padding, nbFrames, err := parsePacketFramesAndPadding(refPacket)
			if err != nil {
				t.Fatalf("parsePacketFramesAndPadding: %v", err)
			}
			_, hasExt, _ := findPacketExtension(padding, nbFrames, qextPacketExtensionID)
			if !hasExt {
				t.Skipf("libopus did not produce QEXT for %dch CBR 256kbps", coupledChannels)
			}

			// Multistream decode via gopus.
			streams := 1
			coupledStreams := 0
			mapping := []byte{0}
			if coupledChannels == 2 {
				coupledStreams = 1
				mapping = []byte{0, 1}
			}

			msd, err := NewMultistreamDecoder(48000, coupledChannels, streams, coupledStreams, mapping)
			if err != nil {
				t.Fatalf("NewMultistreamDecoder: %v", err)
			}
			outBuf := make([]float32, 960*coupledChannels)
			gotN, err := msd.Decode(refPacket, outBuf)
			if err != nil {
				t.Fatalf("MultistreamDecoder.Decode: %v", err)
			}
			if gotN != 960 {
				t.Errorf("decoded %d samples want 960", gotN)
			}

			// Single-stream reference decode for comparison.
			dec, err := NewDecoder(DefaultDecoderConfig(48000, coupledChannels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			wantBuf := make([]float32, 960*coupledChannels)
			n, err := dec.Decode(refPacket, wantBuf)
			if err != nil {
				t.Fatalf("Decoder.Decode: %v", err)
			}
			if n != gotN {
				t.Fatalf("sample count mismatch: ms=%d single=%d", gotN, n)
			}

			// Both paths must produce identical output for a single-stream packet.
			if !bytes.Equal(float32SliceToBytes(outBuf[:n*coupledChannels]),
				float32SliceToBytes(wantBuf[:n*coupledChannels])) {
				maxDiff := float32(0)
				for i := range outBuf[:n*coupledChannels] {
					d := outBuf[i] - wantBuf[i]
					if d < 0 {
						d = -d
					}
					if d > maxDiff {
						maxDiff = d
					}
				}
				t.Errorf("multistream vs single-stream decode diff: maxDiff=%e", maxDiff)
			}
		})
	}
}

// float32SliceToBytes reinterprets a []float32 as []byte for comparison.
func float32SliceToBytes(s []float32) []byte {
	if len(s) == 0 {
		return nil
	}
	// Use unsafe-free approach: compare via math.Float32bits.
	b := make([]byte, len(s)*4)
	for i, v := range s {
		bits := math.Float32bits(v)
		b[4*i] = byte(bits)
		b[4*i+1] = byte(bits >> 8)
		b[4*i+2] = byte(bits >> 16)
		b[4*i+3] = byte(bits >> 24)
	}
	return b
}

func qextSilencePacket(t *testing.T, codedChannels, extensionBytes int) []byte {
	t.Helper()
	toc := byte(31 << 3) // 20 ms full-band CELT
	if codedChannels == 2 {
		toc |= 4
	}
	packet := make([]byte, 4000)
	n, err := buildPacketWithOptions(toc, [][]byte{{0xff, 0xfe}}, packet, 0, false,
		[]packetExtensionData{{ID: qextPacketExtensionID, Frame: 0, Data: make([]byte, extensionBytes)}}, false)
	if err != nil {
		t.Fatal(err)
	}
	return packet[:n]
}

func compareQEXTDecodeSequenceWithLibopus(t *testing.T, outputChannels, frameSize int, packets [][]byte) {
	t.Helper()
	libopustest.RequireOracle(t)
	wantPCM, wantRanges, err := selectedQEXTDecodeSequenceReference(outputChannels, frameSize, packets)
	if err != nil {
		libopustest.HelperUnavailable(t, "selected QEXT decoder", err)
		return
	}
	samplesPerPacket := frameSize * outputChannels

	dec, err := NewDecoder(DefaultDecoderConfig(48000, outputChannels))
	if err != nil {
		t.Fatal(err)
	}
	gotPCM := make([]float32, samplesPerPacket)
	for i, packet := range packets {
		n, err := dec.Decode(packet, gotPCM)
		if err != nil || n != frameSize {
			t.Fatalf("Go frame %d samples=%d err=%v", i, n, err)
		}
		if got := dec.FinalRange(); got != wantRanges[i] {
			t.Errorf("frame %d range Go=%08x C=%08x", i, got, wantRanges[i])
		}
		for j, got := range gotPCM {
			want := wantPCM[i*samplesPerPacket+j]
			if math.Float32bits(got) != math.Float32bits(want) {
				t.Fatalf("frame %d PCM[%d] Go=%08x C=%08x", i, j, math.Float32bits(got), math.Float32bits(want))
			}
		}
	}
}

func TestQEXTReceivedSilencePacketMatchesLibopus(t *testing.T) {
	for _, codedChannels := range []int{1, 2} {
		for _, outputChannels := range []int{1, 2} {
			for _, extensionBytes := range []int{20, 64, 128} {
				name := fmt.Sprintf("coded%d_output%d_extension%d", codedChannels, outputChannels, extensionBytes)
				t.Run(name, func(t *testing.T) {
					packet := qextSilencePacket(t, codedChannels, extensionBytes)
					compareQEXTDecodeSequenceWithLibopus(t, outputChannels, 960, [][]byte{packet})
				})
			}
		}
	}
}

func TestQEXTReceivedSilenceRecoveryMatchesLibopus(t *testing.T) {
	for _, channels := range []int{1, 2} {
		t.Run(fmt.Sprintf("channels%d", channels), func(t *testing.T) {
			bitrate, mode, modeArg := 128000, BitrateModeVBR, ""
			expectExtension := true
			if channels == 2 {
				bitrate, mode, modeArg = 96000, BitrateModeCVBR, "-cvbr"
				expectExtension = false
			}
			packets := testQEXTStatefulPacketsWithSizeMatchLibopus(t, 960, 2, channels, bitrate, mode, modeArg, expectExtension)
			if len(packets) != 2 {
				t.Fatalf("C packet count %d, want 2", len(packets))
			}
			sequence := [][]byte{packets[0], qextSilencePacket(t, channels, 64), packets[1]}
			compareQEXTDecodeSequenceWithLibopus(t, channels, 960, sequence)
		})
	}
}

func TestQEXTStatefulCVBRPacketsMatchLibopus(t *testing.T) {
	testQEXTStatefulPacketsMatchLibopus(t, 96000, BitrateModeCVBR, "-cvbr")
}

func TestQEXTStatefulCVBR128kPacketsMatchLibopus(t *testing.T) {
	testQEXTStatefulPacketsMatchLibopus(t, 128000, BitrateModeCVBR, "-cvbr")
}

func TestQEXTStatefulVBRPacketsMatchLibopus(t *testing.T) {
	testQEXTStatefulPacketsMatchLibopus(t, 128000, BitrateModeVBR, "")
}

func TestQEXTStateful5msCubicPacketsMatchLibopus(t *testing.T) {
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 240, 2, 1, 128000, BitrateModeVBR, "", true)
}

func TestQEXTStateful5msCBRTargetTruncationMatchesLibopus(t *testing.T) {
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 240, 3, 1, 256000, BitrateModeCBR, "-cbr", true)
}

func TestQEXTStateful5msFinalisationPacketsMatchLibopus(t *testing.T) {
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 240, 3, 1, 128000, BitrateModeVBR, "", true)
}

func TestQEXTStatefulStereoFinalisationPacketsMatchLibopus(t *testing.T) {
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 960, 3, 2, 256000, BitrateModeCVBR, "-cvbr", true)
}

func TestQEXTStateful10msFixedStoragePacketsMatchLibopus(t *testing.T) {
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 480, 3, 1, 256000, BitrateModeVBR, "", true)
}

func TestQEXTStateful5msStereoThetaRDOPacketsMatchLibopus(t *testing.T) {
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 240, 3, 2, 128000, BitrateModeCVBR, "-cvbr", false)
}

func TestQEXTStateful40msCBRPaddingMatchesLibopus(t *testing.T) {
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 1920, 3, 1, 96000, BitrateModeCBR, "-cbr", true)
}

func TestQEXTStateful40msVBRSubframeBudgetMatchesLibopus(t *testing.T) {
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 1920, 3, 1, 128000, BitrateModeVBR, "", true)
}

func TestQEXTStateful40msVBRSubframeBudgetCapBoundaryMatchesLibopus(t *testing.T) {
	// The two-frame cap puts curr_max below and above the 254-byte divisor.
	for _, capBytes := range []int{510, 514} {
		t.Run(strconv.Itoa(capBytes), func(t *testing.T) {
			testQEXTStatefulPacketsWithSizeMatchLibopus(t, 1920, 3, 1, 128000, BitrateModeVBR, "", true, capBytes)
		})
	}
}

func TestQEXTStateful60msCBRPaddingMatchesLibopus(t *testing.T) {
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 2880, 3, 1, 96000, BitrateModeCBR, "-cbr", true)
}

func TestQEXTStateful40msCappedCBRPaddingMatchesLibopus(t *testing.T) {
	packets := testQEXTStatefulPacketsWithSizeMatchLibopus(t, 1920, 3, 1, 96000, BitrateModeCBR, "-cbr", true, 477)
	for frame, packet := range packets {
		if len(packet) != 477 {
			t.Fatalf("capped C frame %d length %d, want 477", frame, len(packet))
		}
	}
}

func TestQEXTStatefulPacketMatrixMatchesLibopus(t *testing.T) {
	rows := []struct {
		frameSize int
		bitrates  []int
	}{
		{240, []int{128000, 256000}},
		{480, []int{128000, 256000}},
		{960, []int{96000, 128000, 256000}},
		{1920, []int{96000, 128000}},
		{2880, []int{96000}},
	}
	modes := []struct {
		mode BitrateMode
		arg  string
	}{
		{BitrateModeCBR, "-cbr"},
		{BitrateModeCVBR, "-cvbr"},
		{BitrateModeVBR, ""},
	}
	configurations := 0
	for _, channels := range []int{1, 2} {
		for _, row := range rows {
			for _, bitrate := range row.bitrates {
				for _, mode := range modes {
					name := fmt.Sprintf("channels%d_frame%d_bitrate%d_mode%d", channels, row.frameSize, bitrate, mode.mode)
					t.Run(name, func(t *testing.T) {
						// Presence may vary by frame; every packet and decoded sample stays exact.
						packets := testQEXTStatefulPacketsWithSizeAndExtensionCheck(t, row.frameSize, 3, channels, bitrate, mode.mode, mode.arg, nil)
						compareQEXTDecodeSequenceWithLibopus(t, channels, row.frameSize, packets)
					})
					configurations++
				}
			}
		}
	}
	if configurations != 60 {
		t.Fatalf("QEXT matrix has %d configurations, want 60", configurations)
	}
}

func testQEXTStatefulPacketsMatchLibopus(t *testing.T, bitrate int, mode BitrateMode, modeArg string) {
	t.Helper()
	testQEXTStatefulPacketsWithSizeMatchLibopus(t, 960, 3, 1, bitrate, mode, modeArg, true)
}

func testQEXTStatefulPacketsWithSizeMatchLibopus(t *testing.T, frameSize, frames, channels, bitrate int, mode BitrateMode, modeArg string, expectExtension bool, caps ...int) [][]byte {
	t.Helper()
	return testQEXTStatefulPacketsWithSizeAndExtensionCheck(t, frameSize, frames, channels, bitrate, mode, modeArg, &expectExtension, caps...)
}

func testQEXTStatefulPacketsWithSizeAndExtensionCheck(t *testing.T, frameSize, frames, channels, bitrate int, mode BitrateMode, modeArg string, expectedExtension *bool, caps ...int) [][]byte {
	t.Helper()
	maxPayload := 1276
	if len(caps) > 0 {
		maxPayload = caps[0]
	}
	libopustest.RequireOracle(t)
	opusDemo, err := libopustest.PublicAPIOpusDemoPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "paired QEXT opus_demo", err)
		return nil
	}

	pcm := make([]float32, frameSize*frames*channels)
	state := uint32(0xadd44317)
	for i := range frameSize * frames {
		phase := 2 * math.Pi * (697*float64(i)/48000 + 101*float64(i*i)/(48000*48000))
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		noise := (float64(state&0xffff)/32768 - 1) * 0.08
		for c := range channels {
			pcm[i*channels+c] = float32(0.29*math.Sin(phase+float64(c)*0.43) + 0.16*math.Sin(phase*2.7+float64(c)*0.19) + noise)
		}
	}

	dir := t.TempDir()
	inputPath := filepath.Join(dir, "input.f32")
	bitstreamPath := filepath.Join(dir, "output.bit")
	if err := benchutil.WriteRepeatedRawFloat32(inputPath, pcm, 1); err != nil {
		t.Fatal(err)
	}
	args := []string{"-e", "restricted-celt", "48000", strconv.Itoa(channels), strconv.Itoa(bitrate),
		"-f32", "-complexity", "10", "-bandwidth", "FB", "-framesize", strconv.Itoa(frameSize / 48),
		"-max_payload", strconv.Itoa(maxPayload), "-qext"}
	if modeArg != "" {
		args = append(args, modeArg)
	}
	args = append(args, inputPath, bitstreamPath)
	if out, err := exec.Command(opusDemo, args...).CombinedOutput(); err != nil {
		t.Fatalf("libopus QEXT encode: %v (%s)", err, out)
	}
	bitstream, err := os.ReadFile(bitstreamPath)
	if err != nil {
		t.Fatal(err)
	}

	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: channels, Application: ApplicationRestrictedCelt})
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrateMode(mode); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetComplexity(10); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetQEXT(true); err != nil {
		t.Fatal(err)
	}
	pcm24 := make([]int32, frameSize*channels)
	packet := make([]byte, maxPayload)
	refPackets := make([][]byte, 0, frames)
	offset := 0
	for frame := range frames {
		if len(bitstream)-offset < 8 {
			t.Fatalf("libopus has fewer than %d packet records", frames)
		}
		refLen := int(binary.BigEndian.Uint32(bitstream[offset:]))
		refRange := binary.BigEndian.Uint32(bitstream[offset+4:])
		offset += 8
		if refLen <= 0 || refLen > len(bitstream)-offset {
			t.Fatalf("libopus frame %d has invalid length %d", frame, refLen)
		}
		refPacket := bitstream[offset : offset+refLen]
		offset += refLen
		refPackets = append(refPackets, append([]byte(nil), refPacket...))
		if frameSize > 960 {
			_, _, padding, packetFrames, err := parsePacketFramesAndPadding(refPacket)
			if err != nil {
				t.Fatalf("libopus frame %d framing: %v", frame, err)
			}
			_, present, err := findPacketExtension(padding, packetFrames, qextPacketExtensionID)
			if err != nil {
				t.Fatalf("libopus frame %d extension: %v", frame, err)
			}
			if expectedExtension != nil && present != *expectedExtension {
				t.Fatalf("libopus frame %d extension present=%t, want %t", frame, present, *expectedExtension)
			}
		} else if _, _, _, present, ok := qextParseExtensionRegion(refPacket); !ok {
			t.Fatalf("libopus frame %d extension region invalid", frame)
		} else if expectedExtension != nil && present != *expectedExtension {
			t.Fatalf("libopus frame %d extension present=%t, want %t", frame, present, *expectedExtension)
		}

		for i, sample := range pcm[frame*frameSize*channels : (frame+1)*frameSize*channels] {
			// opus_demo -f32 uses this signed-24 conversion before opus_encode24.
			pcm24[i] = int32(math.Floor(0.5 + float64(sample*8388608)))
		}
		gotLen, err := enc.EncodeInt24(pcm24, packet)
		if err != nil {
			t.Fatalf("Go frame %d encode: %v", frame, err)
		}
		if gotLen < 0 || gotLen > len(packet) {
			t.Fatalf("Go frame %d length %d exceeds caller cap %d", frame, gotLen, len(packet))
		}
		if gotLen != refLen || !bytes.Equal(packet[:gotLen], refPacket) {
			t.Errorf("frame %d packet: first difference %d, Go length %d, C length %d", frame, firstDiffByte(packet[:gotLen], refPacket), gotLen, refLen)
		}
		if gotRange := enc.FinalRange(); gotRange != refRange {
			t.Errorf("frame %d final range: Go %08x, C %08x", frame, gotRange, refRange)
		}
	}
	if !t.Failed() {
		t.Logf("%d complete QEXT packets and final ranges match paired libopus", frames)
	}
	return refPackets
}

func TestQEXTStatefulEncodeInt24SteadyAllocations(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationRestrictedCelt})
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrate(96000); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrateMode(BitrateModeCVBR); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetFrameSize(960); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetQEXT(true); err != nil {
		t.Fatal(err)
	}
	pcm := make([]int32, 960)
	for i := range pcm {
		pcm[i] = int32(math.Sin(2*math.Pi*997*float64(i)/48000) * 0.4 * 8388608)
	}
	packet := make([]byte, 1276)
	for range 4 {
		n, err := enc.EncodeInt24(pcm, packet)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, _, present, ok := qextParseExtensionRegion(packet[:n]); !ok || !present {
			t.Fatal("QEXT extension inactive during allocation warmup")
		}
	}
	var encodeErr error
	lastLen := 0
	allocs := testing.AllocsPerRun(100, func() {
		n, err := enc.EncodeInt24(pcm, packet)
		if err != nil && encodeErr == nil {
			encodeErr = err
		}
		lastLen = n
	})
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	if allocs != 0 {
		t.Fatalf("warmed QEXT EncodeInt24 allocated %.1f times", allocs)
	}
	if _, _, _, present, ok := qextParseExtensionRegion(packet[:lastLen]); !ok || !present {
		t.Fatal("QEXT extension inactive after allocation measurement")
	}
}

func TestQEXTStateful40msCBRCallerBufferSteadyAllocations(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationRestrictedCelt})
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrate(96000); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrateMode(BitrateModeCBR); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetComplexity(10); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetFrameSize(1920); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetQEXT(true); err != nil {
		t.Fatal(err)
	}
	pcm := make([]int32, 1920)
	for i := range pcm {
		pcm[i] = int32(math.Sin(2*math.Pi*697*float64(i)/48000) * 0.38 * 8388608)
	}
	packet := make([]byte, 1276)
	assertPacket := func(n int) {
		t.Helper()
		if n != 480 {
			t.Fatalf("40 ms CBR packet length %d, want 480", n)
		}
		_, _, padding, frames, err := parsePacketFramesAndPadding(packet[:n])
		if err != nil {
			t.Fatal(err)
		}
		_, present, err := findPacketExtension(padding, frames, qextPacketExtensionID)
		if err != nil || !present {
			t.Fatalf("active QEXT extension: present=%t err=%v", present, err)
		}
	}
	for range 6 {
		n, err := enc.EncodeInt24(pcm, packet)
		if err != nil {
			t.Fatal(err)
		}
		assertPacket(n)
	}
	var encodeErr error
	lastLen := 0
	allocs := testing.AllocsPerRun(100, func() {
		n, err := enc.EncodeInt24(pcm, packet)
		if err != nil && encodeErr == nil {
			encodeErr = err
		}
		lastLen = n
	})
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	if allocs != 0 {
		t.Fatalf("warmed 40 ms QEXT CBR EncodeInt24 allocated %.1f times", allocs)
	}
	assertPacket(lastLen)
}

var libopusQEXTDecodeSingleHelper libopustest.HelperCache

func buildLibopusQEXTDecodeSingleHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "qext stateful float decoder",
		OutputBase:  "gopus_libopus_qext_decode_single",
		SourceFile:  "libopus_refdecode_single.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		QEXTRef:     true,
		Libs:        []string{libopustest.QEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func TestQEXTStateful5msStereoWithoutExtensionDecodeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	packets := testQEXTStatefulPacketsWithSizeMatchLibopus(t, 240, 3, 2, 128000, BitrateModeCVBR, "-cvbr", false)
	if len(packets) != 3 {
		t.Fatalf("reference packet count %d, want 3", len(packets))
	}
	const samplesPerPacket = 240 * 2
	wantPCM, wantRanges, err := selectedQEXTDecodeSequenceReference(2, 240, packets)
	if err != nil {
		libopustest.HelperUnavailable(t, "selected QEXT decoder", err)
		return
	}
	if len(wantPCM) != 3*samplesPerPacket || len(wantRanges) != len(packets) {
		t.Fatalf("selected reference geometry PCM=%d ranges=%d", len(wantPCM), len(wantRanges))
	}
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatal(err)
	}
	gotPCM := make([]float32, samplesPerPacket)
	for i, packet := range packets {
		n, err := dec.Decode(packet, gotPCM)
		if err != nil || n != 240 {
			t.Fatalf("Go frame %d decode: samples=%d err=%v", i, n, err)
		}
		if got := dec.FinalRange(); got != wantRanges[i] {
			t.Errorf("frame %d range Go=%08x C=%08x", i, got, wantRanges[i])
		}
		for j, got := range gotPCM {
			want := wantPCM[i*samplesPerPacket+j]
			if math.Float32bits(got) != math.Float32bits(want) {
				t.Fatalf("frame %d PCM[%d] Go=%08x C=%08x", i, j, math.Float32bits(got), math.Float32bits(want))
			}
		}
	}
}
