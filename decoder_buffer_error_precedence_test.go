package gopus

import (
	"encoding/binary"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

type malformedDecodeFramingCase struct {
	name   string
	packet []byte
}

func malformedDecodeFramingCases() []malformedDecodeFramingCase {
	code0 := append([]byte{0x00}, make([]byte, maxOpusFrameBytes+1)...)
	return []malformedDecodeFramingCase{
		{name: "code0", packet: code0},
		{name: "code1", packet: []byte{0x01, 0x00}},
		{name: "code2", packet: []byte{0x02, 0x05, 0x00}},
		{name: "code3", packet: []byte{0x03, 0x82, 0x05, 0x00}},
	}
}

func TestDecodeMalformedFramingPrecedesSmallOutput(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
		for _, channels := range []int{1, 2} {
			for _, format := range []uint32{
				libopustest.DecodeDiffFormatFloat32,
				libopustest.DecodeDiffFormatInt16,
				libopustest.DecodeDiffFormatInt24,
			} {
				formatName := [...]string{"float32", "int16", "int24"}[format]
				t.Run(fmt.Sprintf("%dhz/%dch/%s", sampleRate, channels, formatName), func(t *testing.T) {
					assertDecodeMalformedFramingPrecedesSmallOutput(
						t,
						sampleRate,
						channels,
						format,
						encodeAPIRateSILKPacket(t, channels),
						sampleRate/50,
					)
				})
			}
		}
	}
}

func TestDecodeEmptyAndPartialChannelBuffersPrecedePacketParsing(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		for _, format := range []uint32{
			libopustest.DecodeDiffFormatFloat32,
			libopustest.DecodeDiffFormatInt16,
			libopustest.DecodeDiffFormatInt24,
		} {
			formatName := [...]string{"float32", "int16", "int24"}[format]
			t.Run(fmt.Sprintf("%dch/%s", channels, formatName), func(t *testing.T) {
				packet := encodeAPIRateSILKPacket(t, channels)
				assertDecodeEmptyAndPartialChannelBuffers(
					t, 48000, channels, format, packet, 960)
			})
		}
	}
}

type decodeBufferBoundaryCall struct {
	packet    []byte
	outputLen int
	fec       bool
}

func assertDecodeEmptyAndPartialChannelBuffers(t *testing.T, sampleRate, channels int, format uint32, validPacket []byte, frameSize int) {
	t.Helper()

	malformed := []byte{0x01, 0x00}
	steps := []libopustest.DecodeDiffCase{{Packet: validPacket, Format: format, FrameSize: uint32(frameSize)}}
	calls := []decodeBufferBoundaryCall{{packet: validPacket, outputLen: frameSize * channels}}
	for outputLen := 0; outputLen < channels; outputLen++ {
		fecModes := []bool{false}
		if format == libopustest.DecodeDiffFormatFloat32 {
			fecModes = append(fecModes, true)
		}
		for _, fec := range fecModes {
			for _, packet := range [][]byte{malformed, validPacket} {
				steps = append(steps, libopustest.DecodeDiffCase{
					Packet: packet, Format: format, LiteralFrameSize: true, DecodeFEC: fec,
				})
				calls = append(calls, decodeBufferBoundaryCall{
					packet: packet, outputLen: outputLen, fec: fec,
				})
			}
		}
	}
	steps = append(steps, libopustest.DecodeDiffCase{Packet: validPacket, Format: format, FrameSize: uint32(frameSize)})
	calls = append(calls, decodeBufferBoundaryCall{packet: validPacket, outputLen: frameSize * channels})

	want, err := libopustest.ProbeDecodeSequence(sampleRate, channels, steps)
	if err != nil {
		t.Fatal(err)
	}
	if len(want) != len(steps) {
		t.Fatalf("C returned %d decode results, want %d", len(want), len(steps))
	}

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder(%d, %d): %v", sampleRate, channels, err)
	}
	pcmF32 := make([]float32, frameSize*channels)
	pcmI16 := make([]int16, frameSize*channels)
	pcmI24 := make([]int32, frameSize*channels)
	previousRange := dec.FinalRange()
	for i, call := range calls {
		var n int
		var decodeErr error
		switch format {
		case libopustest.DecodeDiffFormatInt16:
			n, decodeErr = dec.DecodeInt16(call.packet, pcmI16[:call.outputLen])
		case libopustest.DecodeDiffFormatInt24:
			n, decodeErr = dec.DecodeInt24(call.packet, pcmI24[:call.outputLen])
		default:
			if call.fec {
				n, decodeErr = dec.DecodeWithFEC(call.packet, pcmF32[:call.outputLen], true)
			} else {
				n, decodeErr = dec.Decode(call.packet, pcmF32[:call.outputLen])
			}
		}
		if want[i].Code > 0 {
			if decodeErr != nil || int32(n) != want[i].Code {
				t.Fatalf("step%d decode=(%d,%v), C=%d", i, n, decodeErr, want[i].Code)
			}
			if err := assertDecodeMalformedFramingPCM(format, n, channels, want[i].PCM, pcmF32, pcmI16, pcmI24); err != nil {
				t.Fatalf("step%d: %v", i, err)
			}
		} else {
			// The public Go facade maps a zero-samples-per-channel buffer to
			// ErrBufferTooSmall, while all three C wrappers return OPUS_BAD_ARG.
			if want[i].Code != -1 || n != 0 || decodeErr != ErrBufferTooSmall {
				t.Fatalf("step%d decode=(%d,%v), C=%d; want zero-capacity bad-argument mapping", i, n, decodeErr, want[i].Code)
			}
			if len(want[i].PCM) != 0 {
				t.Fatalf("step%d C emitted %d PCM bytes for rejected call", i, len(want[i].PCM))
			}
			if want[i].FinalRange != previousRange {
				t.Fatalf("step%d C error changed final range to %08x, preceding range %08x", i, want[i].FinalRange, previousRange)
			}
		}
		if got := dec.FinalRange(); got != want[i].FinalRange {
			t.Fatalf("step%d range=%08x C=%08x", i, got, want[i].FinalRange)
		}
		if want[i].Code < 0 && dec.FinalRange() != previousRange {
			t.Fatalf("step%d failed call changed Go range to %08x, preceding range %08x", i, dec.FinalRange(), previousRange)
		}
		previousRange = want[i].FinalRange
	}
}

func assertDecodeMalformedFramingPrecedesSmallOutput(t *testing.T, sampleRate, channels int, format uint32, validPacket []byte, frameSize int) {
	t.Helper()

	shortFrameSize := sampleRate / 400
	malformed := malformedDecodeFramingCases()
	steps := make([]libopustest.DecodeDiffCase, 1, 1+len(malformed)*2)
	steps[0] = libopustest.DecodeDiffCase{Packet: validPacket, Format: format, FrameSize: uint32(frameSize)}
	for _, tc := range malformed {
		steps = append(steps,
			libopustest.DecodeDiffCase{Packet: tc.packet, Format: format, FrameSize: uint32(shortFrameSize)},
			libopustest.DecodeDiffCase{Packet: validPacket, Format: format, FrameSize: uint32(frameSize)},
		)
	}
	want, err := libopustest.ProbeDecodeSequence(sampleRate, channels, steps)
	if err != nil {
		t.Fatal(err)
	}
	if len(want) != len(steps) {
		t.Fatalf("C returned %d decode results, want %d", len(want), len(steps))
	}

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder(%d, %d): %v", sampleRate, channels, err)
	}
	pcmF32 := make([]float32, frameSize*channels)
	pcmI16 := make([]int16, frameSize*channels)
	pcmI24 := make([]int32, frameSize*channels)
	decode := func(step libopustest.DecodeDiffCase) (int, error) {
		capacity := int(step.FrameSize) * channels
		switch format {
		case libopustest.DecodeDiffFormatInt16:
			return dec.DecodeInt16(step.Packet, pcmI16[:capacity])
		case libopustest.DecodeDiffFormatInt24:
			return dec.DecodeInt24(step.Packet, pcmI24[:capacity])
		default:
			return dec.Decode(step.Packet, pcmF32[:capacity])
		}
	}
	previousRange := uint32(0)
	for i, step := range steps {
		n, decodeErr := decode(step)
		if want[i].Code < 0 {
			if want[i].Code != -4 || n != 0 || decodeErr != ErrInvalidPacket {
				t.Fatalf("step%d decode=(%d,%v), C=%d; want malformed-packet error", i, n, decodeErr, want[i].Code)
			}
			if len(want[i].PCM) != 0 {
				t.Fatalf("step%d C emitted %d PCM bytes for rejected packet", i, len(want[i].PCM))
			}
			if want[i].FinalRange != previousRange {
				t.Fatalf("step%d C error changed final range to %08x, preceding range %08x", i, want[i].FinalRange, previousRange)
			}
		} else if decodeErr != nil || int32(n) != want[i].Code {
			t.Fatalf("step%d decode=(%d,%v), C=%d", i, n, decodeErr, want[i].Code)
		}
		if got := dec.FinalRange(); got != want[i].FinalRange {
			t.Fatalf("step%d range=%08x C=%08x", i, got, want[i].FinalRange)
		}
		if want[i].Code > 0 {
			if err := assertDecodeMalformedFramingPCM(format, n, channels, want[i].PCM, pcmF32, pcmI16, pcmI24); err != nil {
				t.Fatalf("step%d: %v", i, err)
			}
		}
		previousRange = want[i].FinalRange
	}

	if sampleRate == 48000 && channels == 1 && format == libopustest.DecodeDiffFormatFloat32 {
		bad := malformed[1].packet
		if allocs := testing.AllocsPerRun(20, func() {
			if n, err := dec.Decode(bad, pcmF32[:shortFrameSize]); n != 0 || err != ErrInvalidPacket {
				t.Fatalf("warm malformed decode=(%d,%v)", n, err)
			}
		}); allocs != 0 {
			t.Fatalf("warm malformed decode allocations=%g, want 0", allocs)
		}
	}
}

func assertDecodeMalformedFramingPCM(format uint32, samples, channels int, want []byte, pcmF32 []float32, pcmI16 []int16, pcmI24 []int32) error {
	count := samples * channels
	switch format {
	case libopustest.DecodeDiffFormatInt16:
		if len(want) != count*2 {
			return fmt.Errorf("C PCM has %d bytes, want %d", len(want), count*2)
		}
		for i := 0; i < count; i++ {
			if got, expected := uint16(pcmI16[i]), binary.LittleEndian.Uint16(want[i*2:]); got != expected {
				return fmt.Errorf("sample%d int16=%04x, C=%04x", i, got, expected)
			}
		}
	case libopustest.DecodeDiffFormatInt24:
		if len(want) != count*4 {
			return fmt.Errorf("C PCM has %d bytes, want %d", len(want), count*4)
		}
		for i := 0; i < count; i++ {
			if got, expected := uint32(pcmI24[i]), binary.LittleEndian.Uint32(want[i*4:]); got != expected {
				return fmt.Errorf("sample%d int24=%08x, C=%08x", i, got, expected)
			}
		}
	default:
		if len(want) != count*4 {
			return fmt.Errorf("C PCM has %d bytes, want %d", len(want), count*4)
		}
		for i := 0; i < count; i++ {
			if got, expected := math.Float32bits(pcmF32[i]), binary.LittleEndian.Uint32(want[i*4:]); got != expected {
				return fmt.Errorf("sample%d float32=%08x, C=%08x", i, got, expected)
			}
		}
	}
	return nil
}
