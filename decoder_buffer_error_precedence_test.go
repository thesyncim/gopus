package gopus

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	internalenc "github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
	"github.com/thesyncim/gopus/types"
	"io"
	"math"
	"strings"
	"testing"
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

const (
	cngRateHistoryFrameSize = 960
	cngRateHistorySteps     = 17
)

type cngRateHistoryStep struct {
	packet     []byte
	pcm        []float32
	finalRange uint32
	pitch      int32
}

type cngRateHistoryCase struct {
	direction int
	amplitude int
	steps     []cngRateHistoryStep
}

var (
	cngRateHistoryHelper libopustest.HelperCache
	cngRateHistorySink   uint32
)

func readCNGRateHistoryU32(t *testing.T, r *bytes.Reader, field string) uint32 {
	t.Helper()
	var value uint32
	if err := binary.Read(r, binary.LittleEndian, &value); err != nil {
		t.Fatalf("read CNG rate-history %s: %v", field, err)
	}
	return value
}

func runCNGRateHistoryOracle(t *testing.T) []cngRateHistoryCase {
	t.Helper()
	path, err := cngRateHistoryHelper.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:       "SILK CNG rate-history transition",
			OutputBase:  "gopus_libopus_decoder_cng_rate_history",
			SourceFile:  "libopus_decoder_cng_rate_history.c",
			CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
			RefIncludes: []string{"", "include", "src", "silk", "celt"},
			Libs:        []string{"-lm"},
			DeadStrip:   true,
		})
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "SILK CNG rate-history transition", err)
		return nil
	}
	output, err := libopustest.RunHelper(path, nil)
	if err != nil {
		t.Fatalf("CNG rate-history oracle: %v", err)
	}
	r := bytes.NewReader(output)
	magic := make([]byte, 4)
	if _, err := io.ReadFull(r, magic); err != nil || string(magic) != "GCRH" {
		t.Fatalf("CNG rate-history oracle magic=%q err=%v", magic, err)
	}
	caseCount := readCNGRateHistoryU32(t, r, "case count")
	if caseCount != 8 {
		t.Fatalf("CNG rate-history oracle cases=%d, want 8", caseCount)
	}
	cases := make([]cngRateHistoryCase, int(caseCount))
	pcmBytes := make([]byte, cngRateHistoryFrameSize*4)
	for caseIndex := range cases {
		tc := &cases[caseIndex]
		tc.direction = int(readCNGRateHistoryU32(t, r, "direction"))
		tc.amplitude = int(readCNGRateHistoryU32(t, r, "amplitude"))
		stepCount := readCNGRateHistoryU32(t, r, "step count")
		if tc.direction > 1 || tc.amplitude > 3 || stepCount != cngRateHistorySteps {
			t.Fatalf("CNG sequence %d metadata=%d/%d/%d", caseIndex, tc.direction, tc.amplitude, stepCount)
		}
		tc.steps = make([]cngRateHistoryStep, int(stepCount))
		for stepIndex := range tc.steps {
			step := &tc.steps[stepIndex]
			packetSize := readCNGRateHistoryU32(t, r, "packet size")
			samples := readCNGRateHistoryU32(t, r, "decoded sample count")
			step.finalRange = readCNGRateHistoryU32(t, r, "final range")
			step.pitch = int32(readCNGRateHistoryU32(t, r, "pitch"))
			if packetSize > 1000 || samples != cngRateHistoryFrameSize {
				t.Fatalf("CNG sequence %d step %d packet/sample counts=%d/%d", caseIndex, stepIndex, packetSize, samples)
			}
			step.packet = make([]byte, int(packetSize))
			if _, err := io.ReadFull(r, step.packet); err != nil {
				t.Fatalf("CNG sequence %d step %d packet: %v", caseIndex, stepIndex, err)
			}
			if _, err := io.ReadFull(r, pcmBytes); err != nil {
				t.Fatalf("CNG sequence %d step %d PCM: %v", caseIndex, stepIndex, err)
			}
			step.pcm = make([]float32, cngRateHistoryFrameSize)
			for sample := range step.pcm {
				step.pcm[sample] = math.Float32frombits(binary.LittleEndian.Uint32(pcmBytes[sample*4:]))
			}
		}
	}
	if r.Len() != 0 {
		t.Fatalf("CNG rate-history oracle has %d trailing bytes", r.Len())
	}
	return cases
}

func assertCNGRateHistorySequence(t *testing.T, dec *Decoder, tc cngRateHistoryCase, pcm []float32) {
	t.Helper()
	dec.Reset()
	firstBandwidth, secondBandwidth := BandwidthWideband, BandwidthNarrowband
	if tc.direction == 1 {
		firstBandwidth, secondBandwidth = secondBandwidth, firstBandwidth
	}
	for stepIndex, step := range tc.steps {
		if stepIndex < 7 {
			if len(step.packet) == 0 {
				t.Fatalf("sequence %s step %d has no C-encoded packet", cngRateHistoryCaseName(tc), stepIndex)
			}
			wantBandwidth := firstBandwidth
			if stepIndex == 6 {
				wantBandwidth = secondBandwidth
			}
			info := ParseTOC(step.packet[0])
			if info.Mode != ModeSILK || info.Bandwidth != wantBandwidth {
				t.Fatalf("sequence %s step %d C packet mode/bandwidth=%v/%v, want SILK/%v", cngRateHistoryCaseName(tc), stepIndex, info.Mode, info.Bandwidth, wantBandwidth)
			}
		} else if len(step.packet) != 0 {
			t.Fatalf("sequence %s loss step %d contains a packet", cngRateHistoryCaseName(tc), stepIndex)
		}

		n, err := dec.Decode(step.packet, pcm)
		if err != nil || n != cngRateHistoryFrameSize {
			t.Fatalf("sequence %s step %d Decode=(%d,%v), want (%d,nil)", cngRateHistoryCaseName(tc), stepIndex, n, err, cngRateHistoryFrameSize)
		}
		if got := dec.FinalRange(); got != step.finalRange {
			t.Fatalf("sequence %s step %d range=%08x, want %08x", cngRateHistoryCaseName(tc), stepIndex, got, step.finalRange)
		}
		if got := int32(dec.Pitch()); got != step.pitch {
			t.Fatalf("sequence %s step %d pitch=%d, want %d", cngRateHistoryCaseName(tc), stepIndex, got, step.pitch)
		}
		for sample := range step.pcm {
			if got, want := math.Float32bits(pcm[sample]), math.Float32bits(step.pcm[sample]); got != want {
				t.Fatalf("sequence %s step %d PCM[%d]=%08x, want %08x", cngRateHistoryCaseName(tc), stepIndex, sample, got, want)
			}
		}
	}
}

func cngRateHistoryCaseName(tc cngRateHistoryCase) string {
	direction := "wideband-to-narrowband"
	if tc.direction == 1 {
		direction = "narrowband-to-wideband"
	}
	return fmt.Sprintf("%s-amplitude-%d", direction, tc.amplitude)
}

func TestSILKCNGRateChangeRetainsExcitationMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := runCNGRateHistoryOracle(t)
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]float32, cngRateHistoryFrameSize)
	for _, tc := range cases {
		t.Run(cngRateHistoryCaseName(tc), func(t *testing.T) {
			assertCNGRateHistorySequence(t, dec, tc, pcm)
		})
	}
}

func TestSILKCNGRateChangeWarmZeroAllocs(t *testing.T) {
	cases := runCNGRateHistoryOracle(t)
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]float32, cngRateHistoryFrameSize)
	var runErr error
	run := func() {
		for _, tc := range cases {
			dec.Reset()
			for stepIndex, step := range tc.steps {
				n, err := dec.Decode(step.packet, pcm)
				if err != nil || n != cngRateHistoryFrameSize {
					runErr = fmt.Errorf("sequence %s step %d Decode=(%d,%v)", cngRateHistoryCaseName(tc), stepIndex, n, err)
					return
				}
				cngRateHistorySink ^= math.Float32bits(pcm[(stepIndex*47)%len(pcm)]) ^ dec.FinalRange()
			}
		}
	}
	for range 3 {
		run()
		if runErr != nil {
			t.Fatal(runErr)
		}
	}
	if got := testing.AllocsPerRun(10, run); got != 0 {
		t.Fatalf("warm SILK CNG rate-change decode allocated %g times per sequence batch", got)
	}
	if runErr != nil {
		t.Fatal(runErr)
	}
}

func TestDecodeCode3OverflowPaddingMatchesParsePacket(t *testing.T) {
	// Code-3 packet with padding flag (0x40), 1 frame, and two 0xFF padding
	// length bytes (254+254 > len(data)).
	malformed := []byte{0x03, 0x41, 0xFF, 0xFF}
	if _, err := ParsePacket(malformed); !errors.Is(err, ErrInvalidPacket) {
		t.Fatalf("ParsePacket err = %v, want ErrInvalidPacket", err)
	}
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	out := make([]float32, 960)
	if _, err := dec.Decode(malformed, out); !errors.Is(err, ErrInvalidPacket) {
		t.Fatalf("Decode err = %v, want ErrInvalidPacket", err)
	}
}

func TestDecoder_BandwidthAndLastPacketDuration(t *testing.T) {
	dec := newMonoTestDecoder(t)
	if got := dec.LastPacketDuration(); got != 0 {
		t.Fatalf("LastPacketDuration() before decode=%d want=0", got)
	}
	n := decodeMinimalHybrid20ms(t, dec)
	if n != 960 {
		t.Fatalf("Decode returned %d samples, want 960", n)
	}

	if got := dec.Bandwidth(); got != BandwidthFullband {
		t.Fatalf("Bandwidth()=%v want=%v", got, BandwidthFullband)
	}
	if got := dec.LastPacketDuration(); got != 960 {
		t.Fatalf("LastPacketDuration()=%d want=960", got)
	}
	dec.Reset()
	if got := dec.LastPacketDuration(); got != 0 {
		t.Fatalf("LastPacketDuration() after Reset=%d want=0", got)
	}
}

func TestDecoder_InDTX(t *testing.T) {
	dec := newMonoTestDecoder(t)

	if dec.InDTX() {
		t.Fatal("InDTX()=true before any packet")
	}

	dec.lastDataLen = 2
	if !dec.InDTX() {
		t.Fatal("InDTX()=false for 2-byte packet length")
	}

	dec.lastDataLen = 3
	if dec.InDTX() {
		t.Fatal("InDTX()=true for 3-byte packet length")
	}
}

func TestDecoder_SetGainBounds(t *testing.T) {
	dec := newMonoTestDecoder(t)

	if err := dec.SetGain(-32769); err != ErrInvalidGain {
		t.Fatalf("SetGain(-32769) error=%v want=%v", err, ErrInvalidGain)
	}
	if err := dec.SetGain(32768); err != ErrInvalidGain {
		t.Fatalf("SetGain(32768) error=%v want=%v", err, ErrInvalidGain)
	}

	for _, gain := range []int{-32768, 0, 256, 32767} {
		if err := dec.SetGain(gain); err != nil {
			t.Fatalf("SetGain(%d) unexpected error: %v", gain, err)
		}
		if got := dec.Gain(); got != gain {
			t.Fatalf("Gain()=%d want=%d", got, gain)
		}
	}
}

func TestDecoder_ComplexityControl(t *testing.T) {
	dec := newMonoTestDecoder(t)
	if got := dec.Complexity(); got != 0 {
		t.Fatalf("Complexity() default=%d want 0", got)
	}

	if err := dec.SetComplexity(7); err != nil {
		t.Fatalf("SetComplexity(7) error: %v", err)
	}
	if got := dec.Complexity(); got != 7 {
		t.Fatalf("Complexity()=%d want 7", got)
	}
	if got := dec.celtDecoder.Complexity(); got != 7 {
		t.Fatalf("CELT Complexity()=%d want 7", got)
	}
	if got := dec.hybridDecoder.Complexity(); got != 7 {
		t.Fatalf("Hybrid Complexity()=%d want 7", got)
	}

	if err := dec.SetComplexity(11); err != ErrInvalidComplexity {
		t.Fatalf("SetComplexity(11) error=%v want %v", err, ErrInvalidComplexity)
	}
	if got := dec.Complexity(); got != 7 {
		t.Fatalf("invalid SetComplexity changed setting to %d", got)
	}

	dec.Reset()
	if got := dec.Complexity(); got != 7 {
		t.Fatalf("Reset changed Complexity() to %d", got)
	}
}

func TestDecoder_PhaseInversionControl(t *testing.T) {
	mono := newMonoTestDecoder(t)
	if !mono.PhaseInversionDisabled() {
		t.Fatal("mono PhaseInversionDisabled()=false want true")
	}
	mono.SetPhaseInversionDisabled(false)
	if mono.PhaseInversionDisabled() {
		t.Fatal("mono PhaseInversionDisabled()=true after Set(false)")
	}
	mono.Reset()
	if mono.PhaseInversionDisabled() {
		t.Fatal("mono Reset changed phase inversion control")
	}

	stereo, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder stereo error: %v", err)
	}
	if stereo.PhaseInversionDisabled() {
		t.Fatal("stereo PhaseInversionDisabled()=true want false")
	}
	stereo.SetPhaseInversionDisabled(true)
	if !stereo.PhaseInversionDisabled() {
		t.Fatal("stereo PhaseInversionDisabled()=false after Set(true)")
	}
	stereo.Reset()
	if !stereo.PhaseInversionDisabled() {
		t.Fatal("stereo Reset changed phase inversion control")
	}
}

func TestDecoder_IgnoreExtensions(t *testing.T) {
	assertIgnoreExtensionsControls(t, newMonoTestDecoder(t))
}

func TestDecoder_OptionalExtensionControls(t *testing.T) {
	dec := newMonoTestDecoder(t)

	assertOptionalDecoderControls(t, dec)
	if osce, ok := any(dec).(extraOSCEBWEControl); ok {
		if extsupport.OSCEBWERuntime {
			assertWorkingOSCEBWEControl(t, osce)
		} else {
			t.Fatal("non-OSCE-runtime build unexpectedly exposes OSCE BWE control")
		}
	} else if extsupport.OSCEBWERuntime {
		t.Fatal("OSCE runtime build does not expose OSCE BWE control")
	}
}

func TestDecoder_GainAppliedToDecodeOutput(t *testing.T) {
	packet := minimalHybridTestPacket20ms()

	base, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder base error: %v", err)
	}
	withGain, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder withGain error: %v", err)
	}
	if err := withGain.SetGain(256); err != nil {
		t.Fatalf("SetGain(+1dB) error: %v", err)
	}

	pcmBase := make([]float32, 960)
	pcmGain := make([]float32, 960)

	nBase, err := base.Decode(packet, pcmBase)
	if err != nil {
		t.Fatalf("base Decode error: %v", err)
	}
	nGain, err := withGain.Decode(packet, pcmGain)
	if err != nil {
		t.Fatalf("withGain Decode error: %v", err)
	}
	if nBase != nGain {
		t.Fatalf("decode sample mismatch: base=%d gain=%d", nBase, nGain)
	}

	rms := func(x []float32) float64 {
		if len(x) == 0 {
			return 0
		}
		var sum float64
		for _, v := range x {
			f := float64(v)
			sum += f * f
		}
		return math.Sqrt(sum / float64(len(x)))
	}

	baseRMS := rms(pcmBase[:nBase])
	gainRMS := rms(pcmGain[:nGain])
	if baseRMS == 0 || gainRMS == 0 {
		t.Fatalf("unexpected zero RMS: base=%.8f gain=%.8f", baseRMS, gainRMS)
	}

	gotRatio := gainRMS / baseRMS
	wantRatio := float64(decodeGainLinear(256))
	if celtIntegerPLCActive {
		// src/opus_decoder.c:opus_decode_frame clamps gained opus_res to
		// +/-32767 before RES2FLOAT divides by 2^23. This packet reaches
		// that clamp, so its RMS does not follow an unclipped linear gain.
		const limit = float32(32767.0 / 8388608.0)
		wantPCM := make([]float32, nBase)
		for i, sample := range pcmBase[:nBase] {
			wantPCM[i] = max(-limit, min(limit, sample*float32(wantRatio)))
		}
		wantRatio = rms(wantPCM) / baseRMS
	}
	if math.Abs(gotRatio-wantRatio) > 0.02 {
		t.Fatalf("gain RMS ratio=%.6f want≈%.6f (tol=0.02)", gotRatio, wantRatio)
	}
}

func TestDecoder_PitchGetter(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	if got := dec.Pitch(); got != 0 {
		t.Fatalf("Pitch()=%d want=0 before decode", got)
	}

	celtEnc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder CELT error: %v", err)
	}
	if err := celtEnc.SetMode(EncoderModeCELT); err != nil {
		t.Fatalf("SetMode(CELT) error: %v", err)
	}
	packet := make([]byte, 4000)
	n, err := celtEnc.Encode(generateSineWave(48000, 440, 960), packet)
	if err != nil {
		t.Fatalf("CELT Encode error: %v", err)
	}
	pcm := make([]float32, 960)
	if _, err := dec.Decode(packet[:n], pcm); err != nil {
		t.Fatalf("CELT Decode error: %v", err)
	}

	got := dec.Pitch()
	want := dec.celtDecoder.PostfilterPeriod()
	if got != want {
		t.Fatalf("CELT Pitch()=%d want=%d", got, want)
	}
	if got < 0 {
		t.Fatalf("Pitch() should not be negative: %d", got)
	}

	dec.Reset()
	silkEnc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder SILK error: %v", err)
	}
	if err := silkEnc.SetMode(EncoderModeSILK); err != nil {
		t.Fatalf("SetMode(SILK) error: %v", err)
	}
	if err := silkEnc.SetBandwidth(BandwidthWideband); err != nil {
		t.Fatalf("SetBandwidth(WB) error: %v", err)
	}
	if err := silkEnc.SetSignal(SignalVoice); err != nil {
		t.Fatalf("SetSignal(Voice) error: %v", err)
	}
	n, err = silkEnc.Encode(generateSineWave(48000, 200, 960), packet)
	if err != nil {
		t.Fatalf("SILK Encode error: %v", err)
	}
	if _, err := dec.Decode(packet[:n], pcm); err != nil {
		t.Fatalf("SILK Decode error: %v", err)
	}
	want = 0
	if dec.silkDecoder.GetLastSignalType() == 2 {
		want = dec.silkDecoder.GetLagPrev() * 3
		if want <= 0 {
			t.Fatalf("SILK lagPrev=%d produced invalid pitch", dec.silkDecoder.GetLagPrev())
		}
	}
	if got := dec.Pitch(); got != want {
		t.Fatalf("SILK Pitch()=%d want=%d", got, want)
	}

	for _, tc := range []struct {
		bandwidth Bandwidth
		want      int
	}{
		{BandwidthNarrowband, 6},
		{BandwidthMediumband, 4},
		{BandwidthWideband, 3},
		{BandwidthSuperwideband, 3},
		{BandwidthFullband, 3},
	} {
		if got := silkPitchScale(tc.bandwidth); got != tc.want {
			t.Fatalf("silkPitchScale(%v)=%d want=%d", tc.bandwidth, got, tc.want)
		}
	}
}

func TestDecoderCTL_Defaults(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)

	// OPUS_GET_COMPLEXITY default = 0
	if got := dec.Complexity(); got != 0 {
		t.Errorf("Complexity() default = %d, want 0 (libopus: st->complexity=0 at init)", got)
	}

	// OPUS_GET_GAIN default = 0
	if got := dec.Gain(); got != 0 {
		t.Errorf("Gain() default = %d, want 0 (libopus: decode_gain=0 via OPUS_CLEAR)", got)
	}

	// OPUS_GET_LAST_PACKET_DURATION default = 0 (no packet decoded yet)
	if got := dec.LastPacketDuration(); got != 0 {
		t.Errorf("LastPacketDuration() default = %d, want 0", got)
	}

	// OPUS_GET_FINAL_RANGE default = 0 (lastDataLen=0 → FinalRange returns 0)
	if got := dec.FinalRange(); got != 0 {
		t.Errorf("FinalRange() default = %d, want 0", got)
	}

	// OPUS_GET_PITCH default = 0 (prev_mode=0, not MODE_CELT_ONLY, silk signal
	// type not VOICED)
	if got := dec.Pitch(); got != 0 {
		t.Errorf("Pitch() default = %d, want 0", got)
	}

	// OPUS_GET_PHASE_INVERSION_DISABLED: mono decoder always has phase
	// inversion disabled in gopus (libopus enforces mono constraint in CELT).
	// Stereo default is false.
	stereo := mustNewTestDecoder(t, 48000, 2)
	if stereo.PhaseInversionDisabled() {
		t.Errorf("stereo PhaseInversionDisabled() default = true, want false")
	}

	// OPUS_GET_IGNORE_EXTENSIONS default = false (OPUS_CLEAR zeroes the struct)
	if dec.IgnoreExtensions() {
		t.Errorf("IgnoreExtensions() default = true, want false")
	}

	// OPUS_GET_SAMPLE_RATE
	for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
		d := mustNewTestDecoder(t, rate, 1)
		if got := d.SampleRate(); got != rate {
			t.Errorf("SampleRate() = %d, want %d", got, rate)
		}
	}

	// Channels is not a CTL but a constructor parameter.
	if got := dec.Channels(); got != 1 {
		t.Errorf("Channels() = %d, want 1", got)
	}
}

// ---------------------------------------------------------------------------
// CTL round-trip: SET then GET must return the value just set.
// ---------------------------------------------------------------------------

// TestDecoderCTL_GainRoundTrip verifies OPUS_SET_GAIN / OPUS_GET_GAIN
// round-trips the full valid range boundary values.
//
// C ref: opus_decoder_ctl OPUS_SET_GAIN_REQUEST – range [-32768, 32767].
func TestDecoderCTL_GainRoundTrip(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)

	for _, gain := range []int{-32768, -1, 0, 1, 256, 32767} {
		if err := dec.SetGain(gain); err != nil {
			t.Fatalf("SetGain(%d) unexpected error: %v", gain, err)
		}
		if got := dec.Gain(); got != gain {
			t.Errorf("Gain() = %d after SetGain(%d), want %d", got, gain, gain)
		}
	}
}

// TestDecoderCTL_GainBoundaryReject verifies out-of-range gain returns an error
// and does not change the stored value.
//
// C ref: opus_decoder_ctl OPUS_SET_GAIN_REQUEST – "if (value<-32768 || value>32767) goto bad_arg"
func TestDecoderCTL_GainBoundaryReject(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	if err := dec.SetGain(0); err != nil {
		t.Fatalf("SetGain(0) error: %v", err)
	}

	for _, gain := range []int{-32769, 32768, -100000, 100000} {
		err := dec.SetGain(gain)
		if err == nil {
			t.Errorf("SetGain(%d) should return error, got nil", gain)
		}
		// Value must not have changed.
		if got := dec.Gain(); got != 0 {
			t.Errorf("SetGain(%d) changed Gain() to %d, want 0", gain, got)
		}
	}
}

// TestDecoderCTL_ComplexityRoundTrip verifies OPUS_SET_COMPLEXITY /
// OPUS_GET_COMPLEXITY round-trips all valid values [0,10].
//
// C ref: opus_decoder_ctl OPUS_SET_COMPLEXITY_REQUEST – "if(value<0 || value>10) goto bad_arg"
func TestDecoderCTL_ComplexityRoundTrip(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)

	for c := 0; c <= 10; c++ {
		if err := dec.SetComplexity(c); err != nil {
			t.Fatalf("SetComplexity(%d) error: %v", c, err)
		}
		if got := dec.Complexity(); got != c {
			t.Errorf("Complexity() = %d after SetComplexity(%d)", got, c)
		}
	}
}

// TestDecoderCTL_ComplexityBoundaryReject verifies out-of-range complexity is
// rejected.
//
// C ref: opus_decoder_ctl OPUS_SET_COMPLEXITY_REQUEST – "if(value<0 || value>10) goto bad_arg"
func TestDecoderCTL_ComplexityBoundaryReject(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	if err := dec.SetComplexity(5); err != nil {
		t.Fatalf("SetComplexity(5) error: %v", err)
	}

	for _, c := range []int{-1, 11, 100} {
		err := dec.SetComplexity(c)
		if err == nil {
			t.Errorf("SetComplexity(%d) should return error, got nil", c)
		}
		if got := dec.Complexity(); got != 5 {
			t.Errorf("invalid SetComplexity(%d) changed Complexity() to %d, want 5", c, got)
		}
	}
}

// TestDecoderCTL_PhaseInversionDisabledRoundTrip verifies
// OPUS_SET/GET_PHASE_INVERSION_DISABLED on a stereo decoder.
//
// C ref: opus_decoder_ctl OPUS_SET_PHASE_INVERSION_DISABLED_REQUEST –
//
//	"if(value<0 || value>1) goto bad_arg"
func TestDecoderCTL_PhaseInversionDisabledRoundTrip(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 2)

	dec.SetPhaseInversionDisabled(true)
	if !dec.PhaseInversionDisabled() {
		t.Error("PhaseInversionDisabled() = false after Set(true), want true")
	}

	dec.SetPhaseInversionDisabled(false)
	if dec.PhaseInversionDisabled() {
		t.Error("PhaseInversionDisabled() = true after Set(false), want false")
	}
}

// TestDecoderCTL_IgnoreExtensionsRoundTrip verifies
// OPUS_SET/GET_IGNORE_EXTENSIONS round-trip.
//
// C ref: opus_decoder_ctl OPUS_SET_IGNORE_EXTENSIONS_REQUEST –
//
//	"if(value<0 || value>1) goto bad_arg"
func TestDecoderCTL_IgnoreExtensionsRoundTrip(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)

	dec.SetIgnoreExtensions(true)
	if !dec.IgnoreExtensions() {
		t.Error("IgnoreExtensions() = false after Set(true), want true")
	}

	dec.SetIgnoreExtensions(false)
	if dec.IgnoreExtensions() {
		t.Error("IgnoreExtensions() = true after Set(false), want false")
	}
}

// TestDecoderCTL_LastPacketDurationAfterDecode verifies
// OPUS_GET_LAST_PACKET_DURATION equals the decoded sample count after a real
// packet.
//
// C ref: opus_decode_native – "st->last_packet_duration = nb_samples"
func TestDecoderCTL_LastPacketDurationAfterDecode(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]float32, 960)
	n, err := dec.Decode(minimalHybridTestPacket20ms(), pcm)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if got := dec.LastPacketDuration(); got != n {
		t.Errorf("LastPacketDuration() = %d after Decode returning %d, want equal", got, n)
	}
}

// TestDecoderCTL_BandwidthAfterDecode verifies OPUS_GET_BANDWIDTH returns the
// bandwidth of the last decoded packet.
//
// C ref: opus_decode_native – "st->bandwidth = packet_bandwidth"
func TestDecoderCTL_BandwidthAfterDecode(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	// minimalHybridTestPacket20ms is Hybrid Fullband (config 15).
	if _, err := dec.Decode(minimalHybridTestPacket20ms(), make([]float32, 960)); err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if got := dec.Bandwidth(); got != BandwidthFullband {
		t.Errorf("Bandwidth() = %v after Hybrid-FB decode, want BandwidthFullband", got)
	}
}

// TestDecoderCTL_SampleRateAllRates verifies OPUS_GET_SAMPLE_RATE returns the
// configured API rate for all valid sample rates.
//
// C ref: opus_decoder_ctl OPUS_GET_SAMPLE_RATE_REQUEST – "*value = st->Fs"
func TestDecoderCTL_SampleRateAllRates(t *testing.T) {
	for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
		dec := mustNewTestDecoder(t, rate, 1)
		if got := dec.SampleRate(); got != rate {
			t.Errorf("SampleRate() = %d, want %d", got, rate)
		}
	}
}

// TestDecoderCTL_FinalRangeZeroBeforeDecode verifies FinalRange returns 0
// before any packet has been decoded.
//
// C ref: opus_decoder.c FinalRange – "if lastDataLen <= 1 return 0"
func TestDecoderCTL_FinalRangeZeroBeforeDecode(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	if got := dec.FinalRange(); got != 0 {
		t.Errorf("FinalRange() = %d before any decode, want 0", got)
	}
}

// TestDecoderCTL_FinalRangeNonZeroAfterDecode verifies FinalRange returns a
// non-zero value after successfully decoding a real packet.
//
// C ref: opus_decoder.c – "st->rangeFinal" is set from the range coder state
//
//	after opus_decode_frame (celt_decoder_ctl OPUS_GET_FINAL_RANGE).
func TestDecoderCTL_FinalRangeNonZeroAfterDecode(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]float32, 960)
	if _, err := dec.Decode(minimalHybridTestPacket20ms(), pcm); err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if got := dec.FinalRange(); got == 0 {
		t.Error("FinalRange() = 0 after decoding real packet, want non-zero")
	}
}

// TestDecoderCTL_ResetPreservesComplexity verifies that Reset() does not clear
// the complexity setting (matching libopus: opus_decoder_ctl OPUS_RESET_STATE
// uses OPUS_CLEAR over the RESET_START range, which excludes complexity from
// its reset because st->complexity is inside the preserved region).
//
// C ref: opus_decoder.c OPUS_RESET_STATE case – OPUS_CLEAR starts at
//
//	&st->OPUS_DECODER_RESET_START, which is after st->complexity.
func TestDecoderCTL_ResetPreservesComplexity(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	if err := dec.SetComplexity(7); err != nil {
		t.Fatalf("SetComplexity(7) error: %v", err)
	}
	dec.Reset()
	if got := dec.Complexity(); got != 7 {
		t.Errorf("Complexity() = %d after Reset(), want 7 (complexity is preserved by libopus reset)", got)
	}
}

// TestDecoderCTL_ResetPreservesGain verifies that Reset() preserves the
// decode gain, matching libopus OPUS_RESET_STATE behavior.
//
// C ref: opus_decoder.c struct layout – decode_gain is at line 71, before
//
//	"OPUS_DECODER_RESET_START stream_channels" (line 80).  OPUS_RESET_STATE
//	clears only from stream_channels onward, so decode_gain survives reset.
//	Complexity (line 72) is likewise preserved (see TestDecoderCTL_ResetPreservesComplexity).
func TestDecoderCTL_ResetPreservesGain(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	if err := dec.SetGain(256); err != nil {
		t.Fatalf("SetGain(256) error: %v", err)
	}
	dec.Reset()
	if got := dec.Gain(); got != 256 {
		t.Errorf("Gain() = %d after Reset(), want 256 (libopus preserves decode_gain across OPUS_RESET_STATE)", got)
	}
}

// TestDecoderCTL_ResetClearsLastPacketDuration verifies Reset() zeroes
// LastPacketDuration.
func TestDecoderCTL_ResetClearsLastPacketDuration(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	if _, err := dec.Decode(minimalHybridTestPacket20ms(), make([]float32, 960)); err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if dec.LastPacketDuration() == 0 {
		t.Fatal("LastPacketDuration() = 0 after valid decode – precondition failed")
	}
	dec.Reset()
	if got := dec.LastPacketDuration(); got != 0 {
		t.Errorf("LastPacketDuration() = %d after Reset(), want 0", got)
	}
}

// ---------------------------------------------------------------------------
// Error-code equivalence for invalid packets.
// ---------------------------------------------------------------------------
// libopus maps packet parsing failures to OPUS_INVALID_PACKET (-4) and buffer
// sizing failures to OPUS_BUFFER_TOO_SMALL (-2).  gopus uses ErrInvalidPacket
// and ErrBufferTooSmall.  The tests below feed known-bad packets and confirm
// the gopus error matches the libopus classification.

// TestDecodeErrorCode_ZeroLengthPacket verifies that an empty (zero-length)
// byte slice returns ErrInvalidPacket.
//
// C ref: opus_decode_native – non-nil data with len==0 takes the PLC branch,
//
//	but when called from opus_decode_float with data!=nil it would have
//	attempted get_nb_samples first; however gopus (like libopus float path)
//	treats an empty non-nil slice as a zero-length data → PLC, not an error.
//	A truly absent packet (nil) is PLC.  A single-byte (TOC-only) packet with
//	no frame data is handled by opus_packet_parse_impl returning count≥0, and
//	the single frame has zero bytes, which is valid for the CELT/SILK layer.
//
// gopus mirrors libopus: empty non-nil data decodes as PLC (no error).
func TestDecodeErrorCode_ZeroLengthPacket(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]float32, 960)

	// nil data → PLC, not error
	_, err := dec.Decode(nil, pcm)
	if err != nil {
		t.Errorf("Decode(nil) error = %v, want nil (PLC)", err)
	}

	// empty non-nil data → PLC (matches libopus len==0 branch)
	_, err = dec.Decode([]byte{}, pcm)
	if err != nil {
		t.Errorf("Decode([]byte{}) error = %v, want nil (PLC)", err)
	}
}

// TestDecodeErrorCode_TruncatedCode3 verifies that a code-3 packet that is
// only 1 byte (missing the frame-count byte) is rejected.
//
// C ref: opus_packet_parse_impl – code==3 requires at least 2 bytes; returns
//
//	OPUS_INVALID_PACKET when len<2.
func TestDecodeErrorCode_TruncatedCode3(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]float32, 5760)

	// One-byte code-3 packet (missing frame-count byte).
	_, err := dec.Decode([]byte{GenerateTOC(16, false, 3)}, pcm)
	if err == nil {
		t.Error("Decode(truncated code-3) = nil, want error")
	}
}

// TestDecodeErrorCode_Code3ZeroFrames verifies that M=0 in a code-3 packet
// is rejected.
//
// C ref: opus_packet_parse_impl – "if (count==0) return OPUS_INVALID_PACKET"
// gopus: ErrInvalidFrameCount ("M > 48" message covers M==0 via m==0 check)
func TestDecodeErrorCode_Code3ZeroFrames(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]float32, 5760)

	// Code-3 packet with M=0 in the frame-count byte.
	pkt := []byte{GenerateTOC(16, false, 3), 0x00}
	_, err := dec.Decode(pkt, pcm)
	if err == nil {
		t.Error("Decode(code-3 M=0) = nil, want error")
	}
}

// TestDecodeErrorCode_Code3ExcessiveFrames verifies M>48 is rejected.
//
// C ref: opus_packet_parse_impl – if (count > 48) return OPUS_INVALID_PACKET
func TestDecodeErrorCode_Code3ExcessiveFrames(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]float32, 5760)

	// Code-3 packet with M=49 (0x31 = 0b0110001 = CBR, no-padding, 49 frames).
	pkt := []byte{GenerateTOC(16, false, 3), 49}
	_, err := dec.Decode(pkt, pcm)
	if err == nil {
		t.Error("Decode(code-3 M=49) = nil, want error")
	}
}

// TestDecodeErrorCode_TotalDurationOver120ms verifies that a packet whose
// total duration exceeds 120ms is rejected.
//
// C ref: opus_packet_get_nb_samples – "if (samples*25 > Fs*3) return
//
//	OPUS_INVALID_PACKET"
func TestDecodeErrorCode_TotalDurationOver120ms(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	// Config 31 = CELT FB 20ms; code 3 with M=7 → 140ms > 120ms.
	pkt := []byte{GenerateTOC(31, false, 3), 0x07}
	pcm := make([]float32, 7000)

	_, err := dec.Decode(pkt, pcm)
	if err == nil {
		t.Error("Decode(>120ms) = nil, want error")
	}
}

// TestDecodeErrorCode_BufferTooSmall verifies OPUS_BUFFER_TOO_SMALL when the
// output PCM buffer is shorter than the packet requires.
//
// C ref: opus_decode_native – "if (count*packet_frame_size > frame_size)
//
//	return OPUS_BUFFER_TOO_SMALL"
func TestDecodeErrorCode_BufferTooSmall(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	// minimalHybridTestPacket20ms() is 960 samples; give only 480.
	pcm := make([]float32, 480)
	_, err := dec.Decode(minimalHybridTestPacket20ms(), pcm)
	if err != ErrBufferTooSmall {
		t.Errorf("Decode with short buffer = %v, want ErrBufferTooSmall", err)
	}
}

// TestDecodeErrorCode_Code1OddPayload verifies that a code-1 packet with an
// odd payload length is rejected (two equal-length frames require an even
// total payload).
//
// C ref: opus_packet_parse_impl code==1 branch – "if (framesize & 1)
//
//	return OPUS_INVALID_PACKET"
func TestDecodeErrorCode_Code1OddPayload(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]float32, 960*2)

	// Config 16 (CELT NB 2.5ms), code 1, 3 payload bytes (odd).
	pkt := []byte{GenerateTOC(16, false, 1), 0xAA, 0xBB, 0xCC}
	_, err := dec.Decode(pkt, pcm)
	if err == nil {
		t.Error("Decode(code-1 odd payload) = nil, want error")
	}
}

// TestDecodeErrorCode_Code2ShortPacket verifies that a code-2 packet with
// insufficient bytes for the first frame length is rejected.
//
// C ref: opus_packet_parse_impl code==2 branch checks parsed frame1Len
//
//	against the remaining packet bytes.
func TestDecodeErrorCode_Code2ShortPacket(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]float32, 960*2)

	// TOC byte only, no frame-length byte for code-2.
	pkt := []byte{GenerateTOC(16, false, 2)}
	_, err := dec.Decode(pkt, pcm)
	if err == nil {
		t.Error("Decode(code-2 truncated) = nil, want error")
	}
}

// TestDecodeErrorCode_Code2Frame1TooLarge verifies that a code-2 packet
// where the encoded first-frame length exceeds the packet remainder is
// rejected.
//
// C ref: opus_packet_parse_impl code==2 – frame2 length becomes negative →
//
//	OPUS_INVALID_PACKET.
func TestDecodeErrorCode_Code2Frame1TooLarge(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]float32, 960*2)

	// TOC (code-2) + frame1-length byte 200 + 3 bytes payload (200 > 3).
	pkt := []byte{GenerateTOC(16, false, 2), 200, 0xAA, 0xBB, 0xCC}
	_, err := dec.Decode(pkt, pcm)
	if err == nil {
		t.Error("Decode(code-2 frame1 too large) = nil, want error")
	}
}

// TestDecodeErrorCode_Code3CBRUneven verifies that a CBR code-3 packet whose
// total frame bytes are not divisible by M is rejected.
//
// C ref: opus_packet_parse_impl code==3 CBR branch –
//
//	"if (framesize % count) return OPUS_INVALID_PACKET"
func TestDecodeErrorCode_Code3CBRUneven(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]float32, 960*3)

	// Code-3, CBR, M=3, no padding: 2.5ms CELT NB config, payload 5 bytes
	// (5 % 3 != 0 → invalid).
	frameCountByte := byte(3) // CBR (bit7=0), no padding (bit6=0), M=3
	pkt := append([]byte{GenerateTOC(16, false, 3), frameCountByte}, make([]byte, 5)...)
	_, err := dec.Decode(pkt, pcm)
	if err == nil {
		t.Error("Decode(code-3 CBR uneven) = nil, want error")
	}
}

// TestDecodeErrorCode_Int16BufferTooSmall verifies ErrBufferTooSmall is
// returned from DecodeInt16 when the output int16 buffer is short.
func TestDecodeErrorCode_Int16BufferTooSmall(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]int16, 480)
	_, err := dec.DecodeInt16(minimalHybridTestPacket20ms(), pcm)
	if err != ErrBufferTooSmall {
		t.Errorf("DecodeInt16 with short buffer = %v, want ErrBufferTooSmall", err)
	}
}

// TestDecodeErrorCode_InvalidPacketPropagatesFromInt16 verifies that
// ErrInvalidPacket is returned from DecodeInt16 for a malformed packet.
func TestDecodeErrorCode_InvalidPacketPropagatesFromInt16(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]int16, 5760)
	// Code-3 M=7 with 20ms config → >120ms
	_, err := dec.DecodeInt16([]byte{GenerateTOC(31, false, 3), 0x07}, pcm)
	if err == nil {
		t.Error("DecodeInt16(>120ms) = nil, want error")
	}
}

func buildSingleFramePacketWithExtensionsForTest(t *testing.T, packet []byte, extensions []packetExtensionData) []byte {
	t.Helper()

	if len(packet) < 2 {
		t.Fatal("packet too short for extension test")
	}
	dst := make([]byte, len(packet)+16)
	n, err := buildPacketWithOptions(packet[0]&0xFC, [][]byte{packet[1:]}, dst, 0, false, extensions, false)
	if err != nil {
		t.Fatalf("buildPacketWithOptions: %v", err)
	}
	return dst[:n]
}

func buildMalformedSingleFrameExtensionPacketForTest(t *testing.T, packet []byte) []byte {
	t.Helper()

	if len(packet) < 2 {
		t.Fatal("packet too short for extension test")
	}
	out := make([]byte, 0, len(packet)+4)
	out = append(out, (packet[0]&0xFC)|0x03)
	out = append(out, 0x41) // one CBR frame with padding
	out = append(out, 0x02) // total padding bytes = 2
	out = append(out, packet[1:]...)
	out = append(out, 0xFF, 0xFF) // invalid long extension: truncated lacing payload
	return out
}

func TestDecoderDecodeValidUnknownExtensionMatchesBasePacket(t *testing.T) {
	base := minimalHybridTestPacket20ms()
	extended := buildSingleFramePacketWithExtensionsForTest(t, base, []packetExtensionData{
		{ID: 60, Frame: 0, Data: []byte{0x42}},
	})

	baseDec := newMonoTestDecoder(t)
	extDec := newMonoTestDecoder(t)

	basePCM := make([]float32, 960)
	extPCM := make([]float32, 960)

	baseN, err := baseDec.Decode(base, basePCM)
	if err != nil {
		t.Fatalf("Decode(base): %v", err)
	}
	extN, err := extDec.Decode(extended, extPCM)
	if err != nil {
		t.Fatalf("Decode(extended): %v", err)
	}
	if extN != baseN {
		t.Fatalf("Decode sample count=%d want %d", extN, baseN)
	}
	for i := range basePCM {
		if extPCM[i] != basePCM[i] {
			t.Fatalf("sample[%d]=%v want %v", i, extPCM[i], basePCM[i])
		}
	}
}

func TestDecoderOpaquePaddingRemainsDecodableInDefaultBuild(t *testing.T) {
	base := minimalHybridTestPacket20ms()
	malformed := buildMalformedSingleFrameExtensionPacketForTest(t, base)

	baseDec := newMonoTestDecoder(t)
	basePCM := make([]float32, 960)
	baseN, err := baseDec.Decode(base, basePCM)
	if err != nil {
		t.Fatalf("Decode(base): %v", err)
	}

	for _, ignore := range []bool{false, true} {
		dec := newMonoTestDecoder(t)
		dec.SetIgnoreExtensions(ignore)
		gotPCM := make([]float32, 960)
		gotN, err := dec.Decode(malformed, gotPCM)
		if err != nil {
			t.Fatalf("Decode(malformed, ignore=%v): %v", ignore, err)
		}
		if gotN != baseN {
			t.Fatalf("Decode sample count=%d want %d (ignore=%v)", gotN, baseN, ignore)
		}
		for i := range gotN {
			if gotPCM[i] != basePCM[i] {
				t.Fatalf("sample[%d]=%v want %v (ignore=%v)", i, gotPCM[i], basePCM[i], ignore)
			}
		}
	}
}

func makeValidCELTPacketForExtensionTest(t *testing.T) []byte {
	t.Helper()

	enc := internalenc.NewEncoder(48000, 2)
	enc.SetMode(internalenc.ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(256000)

	pcm := make([]float32, 960*2)
	for i := range 960 {
		phase := 2 * math.Pi * 997 * float64(i) / 48000.0
		pcm[2*i] = float32(0.45 * math.Sin(phase))
		pcm[2*i+1] = float32(0.35 * math.Sin(phase+0.37))
	}

	packet, err := enc.Encode(pcm, 960)
	if err != nil {
		t.Fatalf("Encode(CELT): %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("Encode(CELT) returned empty packet")
	}
	return packet
}

func TestDecoderCELTOpaquePaddingRemainsDecodable(t *testing.T) {
	base := makeValidCELTPacketForExtensionTest(t)
	malformed := buildMalformedSingleFrameExtensionPacketForTest(t, base)

	for _, ignore := range []bool{false, true} {
		baseDec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
		if err != nil {
			t.Fatalf("NewDecoder(base): %v", err)
		}
		baseDec.SetIgnoreExtensions(ignore)
		basePCM := make([]float32, 960*2)
		baseN, err := baseDec.Decode(base, basePCM)
		if err != nil {
			t.Fatalf("Decode(base, ignore=%v): %v", ignore, err)
		}

		extDec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
		if err != nil {
			t.Fatalf("NewDecoder(extended): %v", err)
		}
		extDec.SetIgnoreExtensions(ignore)
		extPCM := make([]float32, 960*2)
		extN, err := extDec.Decode(malformed, extPCM)
		if err != nil {
			t.Fatalf("Decode(malformed, ignore=%v): %v", ignore, err)
		}

		if extN != baseN {
			t.Fatalf("Decode sample count=%d want %d (ignore=%v)", extN, baseN, ignore)
		}
		for i := 0; i < extN*2; i++ {
			if math.IsNaN(float64(extPCM[i])) || math.IsInf(float64(extPCM[i]), 0) {
				t.Fatalf("sample[%d]=%v invalid (ignore=%v)", i, extPCM[i], ignore)
			}
		}
	}
}

func TestDecoderCELTExtraQEXTExtensionMatchesBasePacket(t *testing.T) {
	if SupportsOptionalExtension(OptionalExtensionQEXT) {
		t.Skip("test covers the default non-QEXT build")
	}

	base := makeValidCELTPacketForExtensionTest(t)
	extended := buildSingleFramePacketWithExtensionsForTest(t, base, []packetExtensionData{
		{ID: qextPacketExtensionID, Frame: 0, Data: []byte{0x29, 0x17, 0xA4, 0x53}},
	})

	for _, ignore := range []bool{false, true} {
		baseDec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
		if err != nil {
			t.Fatalf("NewDecoder(base): %v", err)
		}
		baseDec.SetIgnoreExtensions(ignore)
		basePCM := make([]float32, 960*2)
		baseN, err := baseDec.Decode(base, basePCM)
		if err != nil {
			t.Fatalf("Decode(base, ignore=%v): %v", ignore, err)
		}

		extDec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
		if err != nil {
			t.Fatalf("NewDecoder(extended): %v", err)
		}
		extDec.SetIgnoreExtensions(ignore)
		extPCM := make([]float32, 960*2)
		extN, err := extDec.Decode(extended, extPCM)
		if err != nil {
			t.Fatalf("Decode(extended, ignore=%v): %v", ignore, err)
		}

		if extN != baseN {
			t.Fatalf("Decode sample count=%d want %d (ignore=%v)", extN, baseN, ignore)
		}
		for i := 0; i < extN*2; i++ {
			if extPCM[i] != basePCM[i] {
				t.Fatalf("sample[%d]=%v want %v (ignore=%v)", i, extPCM[i], basePCM[i], ignore)
			}
		}
	}
}

func mustNewTestEncoder(t *testing.T, sampleRate, channels int, application Application) *Encoder {
	t.Helper()

	enc, err := NewEncoder(EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: application})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	return enc
}

func mustNewTestDecoder(t *testing.T, sampleRate, channels int) *Decoder {
	t.Helper()

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	return dec
}

func mustNewDefaultMultistreamEncoder(t *testing.T, sampleRate, channels int, application Application) *MultistreamEncoder {
	t.Helper()

	enc, err := NewMultistreamEncoderDefault(sampleRate, channels, application)
	if err != nil {
		t.Fatalf("NewMultistreamEncoderDefault error: %v", err)
	}
	return enc
}

func mustNewDefaultMultistreamDecoder(t *testing.T, sampleRate, channels int) *MultistreamDecoder {
	t.Helper()

	dec, err := NewMultistreamDecoderDefault(sampleRate, channels)
	if err != nil {
		t.Fatalf("NewMultistreamDecoderDefault error: %v", err)
	}
	return dec
}

func newMonoTestDecoder(t *testing.T) *Decoder {
	t.Helper()

	return mustNewTestDecoder(t, 48000, 1)
}

func decodeMinimalHybrid20ms(t *testing.T, dec *Decoder) int {
	t.Helper()

	pcm := make([]float32, 960)
	n, err := dec.Decode(minimalHybridTestPacket20ms(), pcm)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	return n
}

type plcInt16LossStep struct {
	packet []byte // nil → PLC
}

// plcInt16LossPattern groups a named sequence of loss steps (built relative to
// a concrete packet once the mode is known).
type plcInt16LossPattern struct {
	name  string
	build func(pkt []byte) []plcInt16LossStep
}

// plcInt16LossPatterns returns the five canonical loss patterns used in the
// parity matrix.  All patterns produce at least one PLC step.
func plcInt16LossPatterns(pkt []byte) []plcInt16LossPattern {
	return []plcInt16LossPattern{
		{
			name: "single",
			// one good packet then one lost frame then one recovery
			build: func(p []byte) []plcInt16LossStep {
				return []plcInt16LossStep{{packet: p}, {packet: nil}, {packet: p}}
			},
		},
		{
			name: "burst",
			// one good packet then three consecutive losses then recovery
			build: func(p []byte) []plcInt16LossStep {
				return []plcInt16LossStep{
					{packet: p},
					{packet: nil},
					{packet: nil},
					{packet: nil},
					{packet: p},
				}
			},
		},
		{
			name: "periodic",
			// loss on every third frame over 9 total frames (3 losses, 6 good)
			build: func(p []byte) []plcInt16LossStep {
				steps := make([]plcInt16LossStep, 0, 9)
				for i := range 9 {
					if (i+1)%3 == 0 {
						steps = append(steps, plcInt16LossStep{packet: nil})
					} else {
						steps = append(steps, plcInt16LossStep{packet: p})
					}
				}
				return steps
			},
		},
		{
			name: "leading",
			// cold PLC: loss before any good packet (libopus returns silence)
			build: func(p []byte) []plcInt16LossStep {
				return []plcInt16LossStep{{packet: nil}, {packet: p}}
			},
		},
		{
			name: "trailing",
			// good packet then two trailing losses, no recovery
			build: func(p []byte) []plcInt16LossStep {
				return []plcInt16LossStep{
					{packet: p},
					{packet: nil},
					{packet: nil},
				}
			},
		},
	}
}

// plcInt16ModeCases returns the three canonical mode test cases.
func plcInt16ModeCases(t *testing.T, channels int) []struct {
	mode   string
	packet []byte
} {
	t.Helper()
	return []struct {
		mode   string
		packet []byte
	}{
		{"silk", encodeAPIRateSILKPacket(t, channels)},
		{"celt", encodeAPIRateCELTPacket(t, channels)},
		{"hybrid", encodeAPIRateHybridPacket(t, channels)},
	}
}

// plcInt16DecodeSteps converts our loss-step slice to libopusAPIRateDecodeStep.
func plcInt16DecodeSteps(steps []plcInt16LossStep) []libopusAPIRateDecodeStep {
	out := make([]libopusAPIRateDecodeStep, len(steps))
	for i, s := range steps {
		out[i] = libopusAPIRateDecodeStep{packet: s.packet}
	}
	return out
}

// Fixed CELT and Hybrid expose different C conversions at the int16 and float
// public boundaries. Compare each Go boundary with the matching selected-C
// boundary on the same packet and PLC sequence (celt/arch.h: RES2INT16 and
// RES2FLOAT), instead of asserting an invalid cross-format round trip.
func assertPLCFormatsMatchSelectedC(t *testing.T, label string, sampleRate, channels, frameSize int, steps []plcInt16LossStep) {
	t.Helper()
	libopustest.RequireOracle(t)
	libSteps := plcInt16DecodeSteps(steps)
	wantF, err := decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, frameSize, libSteps)
	if err != nil {
		libopustest.HelperUnavailable(t, "float PLC reference decode", err)
	}
	want16, err := decodeWithLibopusReferenceAPIRateInt16Steps(sampleRate, channels, frameSize, libSteps)
	if err != nil {
		libopustest.HelperUnavailable(t, "int16 PLC reference decode", err)
	}
	decF := mustNewTestDecoder(t, sampleRate, channels)
	dec16 := mustNewTestDecoder(t, sampleRate, channels)
	bufF := make([]float32, frameSize*channels)
	buf16 := make([]int16, frameSize*channels)
	gotF := make([]float32, 0, len(wantF))
	got16 := make([]int16, 0, len(want16))
	for i, step := range steps {
		nF, err := decF.Decode(step.packet, bufF)
		if err != nil {
			t.Fatalf("Decode step[%d]: %v", i, err)
		}
		n16, err := dec16.DecodeInt16(step.packet, buf16)
		if err != nil {
			t.Fatalf("DecodeInt16 step[%d]: %v", i, err)
		}
		if nF != n16 {
			t.Fatalf("step[%d] sample count mismatch: float32=%d int16=%d", i, nF, n16)
		}
		gotF = append(gotF, bufF[:nF*channels]...)
		got16 = append(got16, buf16[:n16*channels]...)
	}
	assertAPIRateQualityFloat32PLC(t, gotF, wantF, sampleRate, channels, true, label+" float PLC vs libopus")
	assertAPIRateQualityInt16PLC(t, got16, want16, sampleRate, channels, true, label+" int16 PLC vs libopus")
	assertAPIRateFloat32BitsExact(t, gotF, wantF, label+" float PLC vs libopus")
	assertAPIRateInt16Exact(t, got16, want16, label+" int16 PLC vs libopus")
}

// TestDecodeInt16PLCModeChannelLossMatrixMatchesLibopus asserts that
// DecodeInt16(nil,...) matches libopus opus_decode(NULL,...) across
// mode {SILK,CELT,Hybrid} × channels {1,2} × loss-pattern {single, burst,
// periodic, leading, trailing} at the 48 kHz API rate.
//
// Quality diagnostics accompany an exact selected-C int16 comparison for each
// packet and loss sequence.
func TestDecodeInt16PLCModeChannelLossMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate = 48000

	for _, channels := range []int{1, 2} {
		for _, mc := range plcInt16ModeCases(t, channels) {
			pkt := mc.packet
			frameSize, err := packetSamplesAtRate(pkt, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate %s ch%d: %v", mc.mode, channels, err)
			}

			for _, pat := range plcInt16LossPatterns(pkt) {
				steps := pat.build(pkt)
				name := mc.mode + "_ch" + itoaSmall(channels) + "_" + pat.name

				t.Run(name, func(t *testing.T) {
					// Oracle: libopus int16.
					libSteps := plcInt16DecodeSteps(steps)
					want, err := decodeWithLibopusReferenceAPIRateInt16Steps(sampleRate, channels, frameSize, libSteps)
					if err != nil {
						libopustest.HelperUnavailable(t, "int16 PLC matrix reference decode", err)
					}

					// gopus DecodeInt16.
					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					got := make([]int16, 0, len(want))
					buf := make([]int16, frameSize*channels)
					for i, step := range steps {
						n, err := dec.DecodeInt16(step.packet, buf)
						if err != nil {
							t.Fatalf("DecodeInt16 step[%d]: %v", i, err)
						}
						got = append(got, buf[:n*channels]...)
					}

					if len(got) != len(want) {
						t.Fatalf("sample count mismatch: got %d want %d", len(got), len(want))
					}

					// Any PLC step makes this PLC-dominated; use the PLC bar so
					// opus_compare's psychoacoustic Q is not the gate on concealed audio.
					hasPLC := false
					for _, step := range steps {
						if step.packet == nil {
							hasPLC = true
							break
						}
					}
					assertAPIRateQualityInt16PLC(t, got, want, sampleRate, channels, hasPLC, name+" int16 PLC vs libopus")
					assertAPIRateInt16Exact(t, got, want, name+" int16 PLC vs libopus")
				})
			}
		}
	}
}

// TestDecodeInt16PLCEqualsFloat32PLCQuantized asserts the float-build int16 PLC
// path is exactly the float32 PLC path quantized by the int16 converter. Fixed
// CELT and Hybrid instead compare both public output formats exactly to their
// matching selected-C API on the same packet-loss sequence.
//
// In the float build, opus_decode and opus_decode_float share the same PLC
// output before FLOAT2INT16 conversion (opus_decoder.c).
//
// The test uses two fresh independent decoders with the same warm-up sequence so
// both decoders hold identical state before the PLC step. The float32 PLC output
// is converted to int16 using float32ToInt16NoSoftClip, the same function that
// DecodeInt16 uses internally, so the tested outputs match sample for sample in
// each selected float build.
func TestDecodeInt16PLCEqualsFloat32PLCQuantized(t *testing.T) {
	const sampleRate = 48000

	for _, channels := range []int{1, 2} {
		for _, mc := range plcInt16ModeCases(t, channels) {
			pkt := mc.packet
			frameSize, err := packetSamplesAtRate(pkt, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate %s ch%d: %v", mc.mode, channels, err)
			}

			for _, pat := range plcInt16LossPatterns(pkt) {
				steps := pat.build(pkt)
				name := mc.mode + "_ch" + itoaSmall(channels) + "_" + pat.name

				t.Run(name, func(t *testing.T) {
					if (celtIntegerPLCActive && mc.mode == "celt") || (hybridIntegerPLCActive && mc.mode == "hybrid") {
						assertPLCFormatsMatchSelectedC(t, name, sampleRate, channels, frameSize, steps)
						return
					}
					decF := mustNewTestDecoder(t, sampleRate, channels)
					dec16 := mustNewTestDecoder(t, sampleRate, channels)

					// wantBuf is used to convert the float32 PLC output to int16
					// using the same function as DecodeInt16 (float32ToInt16NoSoftClip).
					bufF := make([]float32, frameSize*channels)
					wantBuf := make([]int16, frameSize*channels)
					buf16 := make([]int16, frameSize*channels)

					for i, step := range steps {
						nF, err := decF.Decode(step.packet, bufF)
						if err != nil {
							t.Fatalf("Decode step[%d]: %v", i, err)
						}
						n16, err := dec16.DecodeInt16(step.packet, buf16)
						if err != nil {
							t.Fatalf("DecodeInt16 step[%d]: %v", i, err)
						}
						if nF != n16 {
							t.Fatalf("step[%d] sample count mismatch: float32=%d int16=%d", i, nF, n16)
						}

						if step.packet != nil {
							// Good packet: no assertion required (covered elsewhere).
							continue
						}

						// PLC frame: int16 must equal float32ToInt16NoSoftClip(float32 PLC).
						// This uses the same quantization path as DecodeInt16 internally.
						float32ToInt16NoSoftClip(wantBuf, bufF, nF, channels)
						for j := 0; j < n16*channels; j++ {
							if buf16[j] != wantBuf[j] {
								t.Fatalf("step[%d] sample[%d]: DecodeInt16=%d want quantize(Decode=%g)=%d",
									i, j, buf16[j], bufF[j], wantBuf[j])
							}
						}
					}
				})
			}
		}
	}
}

// TestDecodeInt16PLCLeadingColdMatchesLibopus verifies cold PLC
// (nil before any good packet) matches libopus int16 for all three modes.
// This is a focused sub-test of the matrix to make regressions easy to locate.
func TestDecodeInt16PLCLeadingColdMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate = 48000
		channels   = 1
	)

	for _, mc := range plcInt16ModeCases(t, channels) {
		pkt := mc.packet
		frameSize, err := packetSamplesAtRate(pkt, sampleRate)
		if err != nil {
			t.Fatalf("packetSamplesAtRate %s: %v", mc.mode, err)
		}

		t.Run(mc.mode, func(t *testing.T) {
			// Cold PLC: the oracle sequences nil, then the packet.
			libSteps := []libopusAPIRateDecodeStep{{packet: nil}, {packet: pkt}}
			want, err := decodeWithLibopusReferenceAPIRateInt16Steps(sampleRate, channels, frameSize, libSteps)
			if err != nil {
				libopustest.HelperUnavailable(t, "int16 cold PLC reference decode", err)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			got := make([]int16, 0, len(want))
			buf := make([]int16, frameSize*channels)
			for _, p := range [][]byte{nil, pkt} {
				n, err := dec.DecodeInt16(p, buf)
				if err != nil {
					t.Fatalf("DecodeInt16: %v", err)
				}
				got = append(got, buf[:n*channels]...)
			}
			assertAPIRateQualityInt16PLC(t, got, want, sampleRate, channels, true, mc.mode+" cold int16 PLC")
			assertAPIRateInt16Exact(t, got, want, mc.mode+" cold int16 PLC")
		})
	}
}

// TestDecodeInt16PLCBurstMatchesLibopus verifies a 3-frame burst loss matches
// libopus int16 for all three modes and both channel counts.
func TestDecodeInt16PLCBurstMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate = 48000

	for _, channels := range []int{1, 2} {
		for _, mc := range plcInt16ModeCases(t, channels) {
			pkt := mc.packet
			frameSize, err := packetSamplesAtRate(pkt, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate %s ch%d: %v", mc.mode, channels, err)
			}

			t.Run(mc.mode+"_ch"+itoaSmall(channels), func(t *testing.T) {
				// Warm up then 3-frame burst loss then recovery.
				libSteps := []libopusAPIRateDecodeStep{
					{packet: pkt},
					{packet: nil},
					{packet: nil},
					{packet: nil},
					{packet: pkt},
				}
				want, err := decodeWithLibopusReferenceAPIRateInt16Steps(sampleRate, channels, frameSize, libSteps)
				if err != nil {
					libopustest.HelperUnavailable(t, "int16 burst PLC reference decode", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				got := make([]int16, 0, len(want))
				buf := make([]int16, frameSize*channels)
				for i, step := range libSteps {
					n, err := dec.DecodeInt16(step.packet, buf)
					if err != nil {
						t.Fatalf("DecodeInt16 step[%d]: %v", i, err)
					}
					got = append(got, buf[:n*channels]...)
				}
				assertAPIRateQualityInt16PLC(t, got, want, sampleRate, channels, true, mc.mode+" burst int16 PLC")
				assertAPIRateInt16Exact(t, got, want, mc.mode+" burst int16 PLC")
			})
		}
	}
}

// TestDecodeInt16PLCPeriodicDecaysMatchesLibopus exercises the periodic loss
// pattern (loss every 3rd frame) and verifies energy behaviour matches the
// documented PLC decay while also asserting libopus int16 parity.
func TestDecodeInt16PLCPeriodicDecaysMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate = 48000
		channels   = 1
	)
	pkt := encodeAPIRateSILKPacket(t, channels) // Use SILK for voiced PLC energy
	frameSize, err := packetSamplesAtRate(pkt, sampleRate)
	if err != nil {
		t.Fatalf("packetSamplesAtRate: %v", err)
	}

	// Encode enough packets to warm up PLC state.
	const warmup = 4
	warmupPkt := encodeAPIRateSILKPacketFrameSize(t, channels, frameSize)

	// Sequence: warmup good packets + 9 periodic-loss frames.
	libSteps := make([]libopusAPIRateDecodeStep, 0, warmup+9)
	for range warmup {
		libSteps = append(libSteps, libopusAPIRateDecodeStep{packet: warmupPkt})
	}
	for i := range 9 {
		if (i+1)%3 == 0 {
			libSteps = append(libSteps, libopusAPIRateDecodeStep{packet: nil})
		} else {
			libSteps = append(libSteps, libopusAPIRateDecodeStep{packet: pkt})
		}
	}

	want, err := decodeWithLibopusReferenceAPIRateInt16Steps(sampleRate, channels, frameSize, libSteps)
	if err != nil {
		libopustest.HelperUnavailable(t, "int16 periodic PLC reference decode", err)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	got := make([]int16, 0, len(want))
	buf := make([]int16, frameSize*channels)
	for i, step := range libSteps {
		n, err := dec.DecodeInt16(step.packet, buf)
		if err != nil {
			t.Fatalf("DecodeInt16 step[%d]: %v", i, err)
		}
		got = append(got, buf[:n*channels]...)
	}

	assertAPIRateQualityInt16PLC(t, got, want, sampleRate, channels, true, "SILK periodic int16 PLC")
	assertAPIRateInt16Exact(t, got, want, "SILK periodic int16 PLC")
}

// TestDecodeInt16PLCTrailingMatchesLibopus verifies trailing loss (loss at end
// with no recovery packet) for all three modes.
func TestDecodeInt16PLCTrailingMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate = 48000

	for _, channels := range []int{1, 2} {
		for _, mc := range plcInt16ModeCases(t, channels) {
			pkt := mc.packet
			frameSize, err := packetSamplesAtRate(pkt, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate %s ch%d: %v", mc.mode, channels, err)
			}

			t.Run(mc.mode+"_ch"+itoaSmall(channels), func(t *testing.T) {
				libSteps := []libopusAPIRateDecodeStep{
					{packet: pkt},
					{packet: nil},
					{packet: nil},
				}
				want, err := decodeWithLibopusReferenceAPIRateInt16Steps(sampleRate, channels, frameSize, libSteps)
				if err != nil {
					libopustest.HelperUnavailable(t, "int16 trailing PLC reference decode", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				got := make([]int16, 0, len(want))
				buf := make([]int16, frameSize*channels)
				for i, step := range libSteps {
					n, err := dec.DecodeInt16(step.packet, buf)
					if err != nil {
						t.Fatalf("DecodeInt16 step[%d]: %v", i, err)
					}
					got = append(got, buf[:n*channels]...)
				}
				assertAPIRateQualityInt16PLC(t, got, want, sampleRate, channels, true, mc.mode+" trailing int16 PLC")
				assertAPIRateInt16Exact(t, got, want, mc.mode+" trailing int16 PLC")
			})
		}
	}
}

// TestDecodeInt16PLCSubRateMatchesLibopus verifies int16 PLC parity at sub-48 kHz
// API rates for each mode. Sub-rate decodes use the corr/RMS bar (opus_compare N/A).
func TestDecodeInt16PLCSubRateMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const channels = 1

	for _, mc := range plcInt16ModeCases(t, channels) {
		pkt := mc.packet
		for _, sampleRate := range []int{8000, 16000, 24000} {
			frameSize, err := packetSamplesAtRate(pkt, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate %s %dHz: %v", mc.mode, sampleRate, err)
			}

			t.Run(mc.mode+"_"+itoaSmall(sampleRate)+"hz", func(t *testing.T) {
				libSteps := []libopusAPIRateDecodeStep{{packet: pkt}, {packet: nil}}
				want, err := decodeWithLibopusReferenceAPIRateInt16Steps(sampleRate, channels, frameSize, libSteps)
				if err != nil {
					libopustest.HelperUnavailable(t, "int16 sub-rate PLC reference decode", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				got := make([]int16, 0, len(want))
				buf := make([]int16, frameSize*channels)
				for i, step := range libSteps {
					n, err := dec.DecodeInt16(step.packet, buf)
					if err != nil {
						t.Fatalf("DecodeInt16 step[%d]: %v", i, err)
					}
					got = append(got, buf[:n*channels]...)
				}
				assertAPIRateQualityInt16PLC(t, got, want, sampleRate, channels, true, mc.mode+" sub-rate int16 PLC")
				assertAPIRateInt16Exact(t, got, want, mc.mode+" sub-rate int16 PLC")
			})
		}
	}
}

// plcInt16DecoderReset resets a decoder and verifies that no PLC energy leaks
// across a Reset boundary.  This tests the warm-state reset invariant.
func TestDecodeInt16PLCResetBoundaryNoLeak(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
	)
	pkt := encodeAPIRateCELTPacket(t, channels)
	frameSize, err := packetSamplesAtRate(pkt, sampleRate)
	if err != nil {
		t.Fatalf("packetSamplesAtRate: %v", err)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	buf := make([]int16, frameSize*channels)

	// Warm up.
	for range 3 {
		if _, err := dec.DecodeInt16(pkt, buf); err != nil {
			t.Fatalf("warm-up DecodeInt16: %v", err)
		}
	}

	// Reset then cold PLC: should be silence.
	dec.Reset()
	n, err := dec.DecodeInt16(nil, buf)
	if err != nil {
		t.Fatalf("post-reset DecodeInt16(nil): %v", err)
	}
	for _, v := range buf[:n*channels] {
		if v != 0 {
			t.Fatalf("post-reset cold PLC: non-zero sample %d (want silence)", v)
		}
	}
}

// TestDecodeInt16PLCSelfConsistencyWarmupN asserts the float-build
// float-path-quantized identity for warm-up sequences of length 1, 3, and 6
// before the first PLC. Fixed CELT and Hybrid compare both output formats
// exactly with selected C. Each warmup length exercises a different decoder state.
//
// The float32 PLC output is converted to int16 using float32ToInt16NoSoftClip,
// the same function float-build DecodeInt16 uses, ensuring exact equality for
// each tested sequence in the selected build.
func TestDecodeInt16PLCSelfConsistencyWarmupN(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
	)

	for _, mc := range plcInt16ModeCases(t, channels) {
		pkt := mc.packet
		frameSize, err := packetSamplesAtRate(pkt, sampleRate)
		if err != nil {
			t.Fatalf("packetSamplesAtRate %s: %v", mc.mode, err)
		}

		for _, warmup := range []int{1, 3, 6} {
			name := mc.mode + "_warmup" + itoaSmall(warmup)
			t.Run(name, func(t *testing.T) {
				if (celtIntegerPLCActive && mc.mode == "celt") || (hybridIntegerPLCActive && mc.mode == "hybrid") {
					steps := make([]plcInt16LossStep, warmup+1)
					for i := range warmup {
						steps[i].packet = pkt
					}
					assertPLCFormatsMatchSelectedC(t, name, sampleRate, channels, frameSize, steps)
					return
				}
				decF := mustNewTestDecoder(t, sampleRate, channels)
				dec16 := mustNewTestDecoder(t, sampleRate, channels)

				bufF := make([]float32, frameSize*channels)
				buf16 := make([]int16, frameSize*channels)
				wantBuf := make([]int16, frameSize*channels)

				// Warm up both decoders identically.
				for i := range warmup {
					if _, err := decF.Decode(pkt, bufF); err != nil {
						t.Fatalf("warmup Decode step %d: %v", i, err)
					}
					if _, err := dec16.DecodeInt16(pkt, buf16); err != nil {
						t.Fatalf("warmup DecodeInt16 step %d: %v", i, err)
					}
				}

				// PLC frame.
				nF, err := decF.Decode(nil, bufF)
				if err != nil {
					t.Fatalf("Decode(nil): %v", err)
				}
				n16, err := dec16.DecodeInt16(nil, buf16)
				if err != nil {
					t.Fatalf("DecodeInt16(nil): %v", err)
				}
				if nF != n16 {
					t.Fatalf("sample count mismatch: float32=%d int16=%d", nF, n16)
				}

				// Convert float32 PLC output using the same path as DecodeInt16.
				float32ToInt16NoSoftClip(wantBuf, bufF, nF, channels)
				for j := 0; j < n16*channels; j++ {
					if buf16[j] != wantBuf[j] {
						t.Fatalf("sample[%d]: DecodeInt16=%d want quantize(Decode=%g)=%d",
							j, buf16[j], bufF[j], wantBuf[j])
					}
				}
			})
		}
	}
}

// TestDecodeInt16PLCSingleSampleEnergy verifies that a SILK single-loss PLC
// frame produces non-zero output (the PLC is active, not silence) at the
// int16 level.  Complements the float energy test in TestSILKPLCIIRFirstLossOutputNonSilentMatchesLibopus.
func TestDecodeInt16PLCSingleSampleEnergy(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
	)
	pkt := encodeAPIRateSILKPacket(t, channels)
	frameSize, err := packetSamplesAtRate(pkt, sampleRate)
	if err != nil {
		t.Fatalf("packetSamplesAtRate: %v", err)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	buf := make([]int16, frameSize*channels)

	// Warm up with 4 packets.
	for i := range 4 {
		if _, err := dec.DecodeInt16(pkt, buf); err != nil {
			t.Fatalf("warm-up step %d: %v", i, err)
		}
	}

	// PLC frame.
	n, err := dec.DecodeInt16(nil, buf)
	if err != nil {
		t.Fatalf("DecodeInt16(nil): %v", err)
	}

	var energy float64
	for _, v := range buf[:n*channels] {
		energy += float64(v) * float64(v)
	}
	energy /= float64(n * channels)
	if energy < 1 {
		t.Fatalf("SILK int16 PLC frame is silent: mean-square energy=%.2e, expected non-zero speech extrapolation", energy)
	}

	// Silence assertion: the next loss frame should have ≤ energy than the first.
	buf2 := make([]int16, frameSize*channels)
	if _, err := dec.DecodeInt16(nil, buf2); err != nil {
		t.Fatalf("second DecodeInt16(nil): %v", err)
	}
	var energy2 float64
	for _, v := range buf2[:n*channels] {
		energy2 += float64(v) * float64(v)
	}
	energy2 /= float64(n * channels)
	if energy2 >= energy*1.1 {
		// Allow 10 % tolerance for level variation but flag gross non-decay.
		t.Fatalf("SILK int16 PLC burst energy did not decay: frame1=%.2e frame2=%.2e", energy, energy2)
	}
}

const libopusRefdecodeSingleFormatInt24 = uint32(2)

// decodeWithLibopusReferenceAPIRateInt24 drives libopus opus_decode24() through
// the refdecode_single C helper for a sequence of packets, mirroring the pattern
// used by decodeWithLibopusReferenceAPIRateInt16.
func decodeWithLibopusReferenceAPIRateInt24(sampleRate, channels, frameSize int, packets [][]byte) ([]int32, error) {
	steps := make([]libopusAPIRateDecodeStep, len(packets))
	for i, pkt := range packets {
		steps[i] = libopusAPIRateDecodeStep{packet: pkt}
	}
	return decodeWithLibopusReferenceAPIRateInt24Steps(sampleRate, channels, frameSize, steps)
}

func decodeWithLibopusReferenceAPIRateInt24Steps(sampleRate, channels, frameSize int, steps []libopusAPIRateDecodeStep) ([]int32, error) {
	binPath, err := getLibopusAPIRateRefdecodeHelperPath()
	if err != nil {
		return nil, err
	}
	// Protocol version 5: magic GOSI + version + format + sample_rate + gain +
	// channels + frame_size + packet_count.
	payload := libopustest.NewOraclePayloadVersion("GOSI", 5,
		libopusRefdecodeSingleFormatInt24,
		uint32(sampleRate),
		0, // gain
		uint32(channels),
		uint32(frameSize),
		uint32(len(steps)),
	)
	for _, step := range steps {
		if step.fec {
			payload.U32(1)
		} else {
			payload.U32(0)
		}
		payload.U32(uint32(len(step.packet)))
		payload.Raw(step.packet)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "api-rate reference decode int24", "GOSO")
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 4)
	decoded := make([]int32, nSamples)
	for i := range decoded {
		decoded[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return decoded, nil
}

// int32Int24ToFloat32 converts an int32 slice of right-justified 24-bit samples
// to float32 by dividing by 8388608 (= 2^23), matching libopus INT24TORES for
// float builds: (1.f/32768.f/256.f) * a.
func int32Int24ToFloat32(in []int32) []float32 {
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(v) / 8388608.0
	}
	return out
}

// assertInt24ParitySelectedCExact keeps the quality diagnostic and requires
// exact int24 samples from the selected libopus build.
func assertInt24ParitySelectedCExact(t *testing.T, got, want []int32, sampleRate, channels int, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s sample[%d]=%d want %d", label, i, got[i], want[i])
		}
	}
	assertAPIRateQualityFloat32(t, int32Int24ToFloat32(got), int32Int24ToFloat32(want), sampleRate, channels, label)
}

// TestDecodeInt24SILKAPIRatePCMMatchesLibopus verifies that Decoder.DecodeInt24
// produces bit-exact output vs the selected libopus opus_decode24() reference
// for the tested SILK packets and API rates.
func TestDecodeInt24SILKAPIRatePCMMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateSILKPacket(t, channels)
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				frameSize, err := packetSamplesAtRate(packet, sampleRate)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}

				sequence := [][]byte{packet}
				want, err := decodeWithLibopusReferenceAPIRateInt24(sampleRate, channels, frameSize, sequence)
				if err != nil {
					libopustest.HelperUnavailable(t, "api-rate SILK reference decode int24", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				got := make([]int32, frameSize*channels)
				n, err := dec.DecodeInt24(packet, got)
				if err != nil {
					t.Fatalf("DecodeInt24: %v", err)
				}
				if n != frameSize {
					t.Fatalf("DecodeInt24 samples=%d want %d", n, frameSize)
				}
				got = got[:n*channels]
				// SILK output is bit-exact for this selected-reference case.
				if len(got) != len(want) {
					t.Fatalf("DecodeInt24 len=%d want %d", len(got), len(want))
				}
				for i := range got {
					if got[i] != want[i] {
						t.Fatalf("DecodeInt24[%d]=%d want %d", i, got[i], want[i])
					}
				}
			})
		}
	}
}

// TestDecodeInt24CELTAPIRatePCMMatchesLibopus compares public CELT int24
// output sample-for-sample with the selected libopus opus_decode24 build.
func TestDecodeInt24CELTAPIRatePCMMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateCELTPacket(t, channels)
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				frameSize, err := packetSamplesAtRate(packet, sampleRate)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}

				sequence := [][]byte{packet}
				want, err := decodeWithLibopusReferenceAPIRateInt24(sampleRate, channels, frameSize, sequence)
				if err != nil {
					libopustest.HelperUnavailable(t, "api-rate CELT reference decode int24", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				got := make([]int32, frameSize*channels)
				n, err := dec.DecodeInt24(packet, got)
				if err != nil {
					t.Fatalf("DecodeInt24: %v", err)
				}
				if n != frameSize {
					t.Fatalf("DecodeInt24 samples=%d want %d", n, frameSize)
				}
				got = got[:n*channels]
				assertInt24ParitySelectedCExact(t, got, want, sampleRate, channels, "CELT int24 decode")
			})
		}
	}
}

// TestDecodeInt24HybridAPIRatePCMMatchesLibopus verifies Decoder.DecodeInt24
// for Hybrid (SILK+CELT) packets vs libopus opus_decode24().
func TestDecodeInt24HybridAPIRatePCMMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateHybridPacket(t, channels)
		for _, sampleRate := range []int{16000, 24000, 48000} {
			t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				frameSize, err := packetSamplesAtRate(packet, sampleRate)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}

				sequence := [][]byte{packet}
				want, err := decodeWithLibopusReferenceAPIRateInt24(sampleRate, channels, frameSize, sequence)
				if err != nil {
					libopustest.HelperUnavailable(t, "api-rate Hybrid reference decode int24", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				got := make([]int32, frameSize*channels)
				n, err := dec.DecodeInt24(packet, got)
				if err != nil {
					t.Fatalf("DecodeInt24: %v", err)
				}
				if n != frameSize {
					t.Fatalf("DecodeInt24 samples=%d want %d", n, frameSize)
				}
				got = got[:n*channels]
				assertInt24ParitySelectedCExact(t, got, want, sampleRate, channels, "Hybrid int24 decode")
			})
		}
	}
}

// TestDecodeInt24TracksFloat32Decode verifies that Decoder.DecodeInt24 output
// is consistent with Decoder.Decode (float32) output via the int24 conversion,
// confirming that DecodeInt24 shares the same decode path and differs only in
// the final sample format conversion.
func TestDecodeInt24TracksFloat32Decode(t *testing.T) {
	// This test asserts that DecodeInt24 derives its samples from the same
	// float32 decode as Decode (default build). Under -tags gopus_fixed_point,
	// DecodeInt24 routes CELT-only frames directly to the integer decoder's
	// libopus-exact opus_res int24, which is produced by a different (fixed)
	// decode than the float path, so the "tracks float" relationship no longer
	// holds. The int24 path's correctness is covered there by
	// TestDecoderFixedPointCELTParity instead.
	if decodeInt24TracksFloat32Skip {
		t.Skip("DecodeInt24 routes CELT directly to the integer decoder under gopus_fixed_point")
	}
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateCELTPacket(t, channels)
		const sampleRate = 48000
		frameSize, err := packetSamplesAtRate(packet, sampleRate)
		if err != nil {
			t.Fatalf("packetSamplesAtRate: %v", err)
		}
		t.Run("ch_"+itoaSmall(channels), func(t *testing.T) {
			decF := mustNewTestDecoder(t, sampleRate, channels)
			pcmF := make([]float32, frameSize*channels)
			nF, err := decF.Decode(packet, pcmF)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}

			dec24 := mustNewTestDecoder(t, sampleRate, channels)
			pcm24 := make([]int32, frameSize*channels)
			n24, err := dec24.DecodeInt24(packet, pcm24)
			if err != nil {
				t.Fatalf("DecodeInt24: %v", err)
			}

			if nF != n24 {
				t.Fatalf("sample count mismatch: float32=%d int24=%d", nF, n24)
			}
			for i := 0; i < nF*channels; i++ {
				want := float32ToInt24(pcmF[i])
				if pcm24[i] != want {
					t.Fatalf("pcm24[%d]=%d want float32ToInt24(%g)=%d", i, pcm24[i], pcmF[i], want)
				}
			}
		})
	}
}

// TestDecodeInt24SliceMatchesDecodeInt24 verifies that DecodeInt24Slice returns
// the same samples as DecodeInt24 with a pre-allocated buffer.
func TestDecodeInt24SliceMatchesDecodeInt24(t *testing.T) {
	packet := encodeAPIRateCELTPacket(t, 1)
	const sampleRate = 48000
	frameSize, err := packetSamplesAtRate(packet, sampleRate)
	if err != nil {
		t.Fatalf("packetSamplesAtRate: %v", err)
	}

	dec1 := mustNewTestDecoder(t, sampleRate, 1)
	buf := make([]int32, frameSize)
	n, err := dec1.DecodeInt24(packet, buf)
	if err != nil {
		t.Fatalf("DecodeInt24: %v", err)
	}
	want := buf[:n]

	dec2 := mustNewTestDecoder(t, sampleRate, 1)
	got, err := dec2.DecodeInt24Slice(packet)
	if err != nil {
		t.Fatalf("DecodeInt24Slice: %v", err)
	}

	if len(got) != len(want) {
		t.Fatalf("DecodeInt24Slice len=%d want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("sample[%d]=%d want %d", i, got[i], want[i])
		}
	}
}

// TestDecodeInt24InvalidFrameSize verifies that DecodeInt24 returns
// ErrBufferTooSmall when the output buffer is too small.
func TestDecodeInt24InvalidFrameSize(t *testing.T) {
	packet := encodeAPIRateCELTPacket(t, 1)
	dec := mustNewTestDecoder(t, 48000, 1)

	if _, err := dec.DecodeInt24(packet, nil); err != ErrBufferTooSmall {
		t.Fatalf("DecodeInt24(nil pcm) error=%v want %v", err, ErrBufferTooSmall)
	}
	if _, err := dec.DecodeInt24(packet, make([]int32, 1)); err != ErrBufferTooSmall {
		t.Fatalf("DecodeInt24(short pcm) error=%v want %v", err, ErrBufferTooSmall)
	}
}

// TestDecodeInt24PLCMatchesLibopus verifies that Decoder.DecodeInt24 with
// a nil packet (PLC) matches libopus opus_decode24(NULL, ...) within the
// trusted near-exact bar.
//
// Under -tags gopus_fixed_point the CELT loss is concealed by the integer
// celt_decode_lost (different from the float concealment this float-oracle bar
// expects); that path is gated bit-exact by TestDecoderFixedPointCELTPLCParity.
func TestDecodeInt24PLCMatchesLibopus(t *testing.T) {
	if celtIntegerPLCActive {
		t.Skip("CELT PLC routes to the integer decoder under gopus_fixed_point; see TestDecoderFixedPointCELTPLCParity")
	}
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateCELTPacket(t, channels)
		const sampleRate = 48000
		frameSize, err := packetSamplesAtRate(packet, sampleRate)
		if err != nil {
			t.Fatalf("packetSamplesAtRate: %v", err)
		}
		t.Run("ch_"+itoaSmall(channels), func(t *testing.T) {
			sequence := [][]byte{packet, nil}
			want, err := decodeWithLibopusReferenceAPIRateInt24(sampleRate, channels, frameSize, sequence)
			if err != nil {
				libopustest.HelperUnavailable(t, "api-rate CELT reference decode int24 PLC", err)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			got := make([]int32, 0, len(want))
			buf := make([]int32, frameSize*channels)
			for i, pkt := range sequence {
				n, err := dec.DecodeInt24(pkt, buf)
				if err != nil {
					t.Fatalf("DecodeInt24 seq[%d]: %v", i, err)
				}
				got = append(got, buf[:n*channels]...)
			}
			assertInt24ParitySelectedCExact(t, got, want, sampleRate, channels, "CELT int24 PLC decode")
		})
	}
}

const (
	cbrAppAudio          = uint32(0)
	cbrAppVoIP           = uint32(1)
	cbrAppRestrictedSILK = uint32(2)
	cbrAppRestrictedCELT = uint32(3)
)

// libopus bandwidth constants (opus_defines.h), used by the CBR helper.
const (
	cbrBWNarrowband    = uint32(1101)
	cbrBWMediumband    = uint32(1102)
	cbrBWWideband      = uint32(1103)
	cbrBWSuperwideband = uint32(1104)
	cbrBWFullband      = uint32(1105)
)

var libopusCBREncodeHelper libopustest.HelperCache

func getLibopusCBREncodeHelperPath() (string, error) {
	return libopusCBREncodeHelper.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:      "cbr encode packets",
			OutputBase: "gopus_libopus_cbr_encode_packets",
			SourceFile: "libopus_cbr_encode_packets.c",
			CFlags:     []string{"-O3", "-DNDEBUG"},
		})
	})
}

type cbrEncodeConfig struct {
	app        uint32
	bandwidth  uint32
	channels   int
	bitrate    int
	frameSize  int // samples per frame at 48 kHz
	complexity int
	numFrames  int
}

// encodeLibopusCBRPackets drives libopus opus_encode_float() in CBR mode over a
// sequence of frames, returning the encoded packets. The encoder always runs at
// 48 kHz (gopus internal rate).
func encodeLibopusCBRPackets(cfg cbrEncodeConfig, pcm []float32) ([][]byte, error) {
	binPath, err := getLibopusCBREncodeHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayloadVersion("GCBR", 1,
		cfg.app,
		cfg.bandwidth,
		uint32(cfg.channels),
		uint32(cfg.bitrate),
		uint32(cfg.frameSize),
		uint32(cfg.complexity),
		uint32(cfg.numFrames),
	)
	payload.Float32s(pcm...)
	reader, version, err := libopustest.NewOracleReaderVersion("cbr encode", "GCBO", mustRunHelper(binPath, payload.Bytes()))
	if err != nil {
		return nil, err
	}
	if version != 2 {
		return nil, fmt.Errorf("cbr encode helper version=%d want 2", version)
	}
	versionLen := int(reader.U32())
	if versionLen <= 0 || versionLen > 128 {
		return nil, fmt.Errorf("invalid cbr encode libopus version length %d", versionLen)
	}
	if got := string(reader.Bytes(versionLen)); !strings.Contains(got, libopustooling.DefaultVersion) {
		return nil, fmt.Errorf("cbr encode helper reports libopus version %q, want %s", got, libopustooling.DefaultVersion)
	}
	archMask, buildFeatures, selectedArch := reader.U32(), reader.U32(), reader.U32()
	if selectedArch > archMask {
		return nil, fmt.Errorf("cbr encode helper selected architecture %d above OPUS_ARCHMASK %d", selectedArch, archMask)
	}
	identity, err := libopustest.ResolvePublicAPIReferenceIdentity()
	if err != nil {
		return nil, err
	}
	switch {
	case strings.HasSuffix(string(identity.Variant), "scalar"):
		if archMask != 0 || buildFeatures != 0 || selectedArch != 0 {
			return nil, fmt.Errorf("scalar CBR reference emitted SIMD metadata: arch_mask=%d features=0x%x selected_arch=%d", archMask, buildFeatures, selectedArch)
		}
	case strings.HasSuffix(string(identity.Variant), "simd"):
		if buildFeatures == 0 {
			return nil, fmt.Errorf("SIMD CBR reference emitted no generated SIMD build features")
		}
	default:
		return nil, fmt.Errorf("unsupported paired CBR reference variant %q", identity.Variant)
	}
	count := reader.Count(cfg.numFrames)
	packets := make([][]byte, 0, count)
	for range count {
		n := int(reader.U32())
		_ = reader.U32() // OPUS_GET_FINAL_RANGE
		packets = append(packets, append([]byte(nil), reader.Bytes(n)...))
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return packets, nil
}

func TestDecodeLibopusCBROracleOutputV2(t *testing.T) {
	libopustest.RequireOracle(t)
	cfg := cbrEncodeConfig{
		app:        cbrAppAudio,
		bandwidth:  cbrBWWideband,
		channels:   1,
		bitrate:    32000,
		frameSize:  480,
		complexity: 1,
		numFrames:  2,
	}
	packets, err := encodeLibopusCBRPackets(cfg, configEdgesPCM(cfg.frameSize, cfg.channels, cfg.numFrames))
	if err != nil {
		libopustest.HelperUnavailable(t, "cbr encode", err)
		return
	}
	if len(packets) != cfg.numFrames {
		t.Fatalf("CBR oracle returned %d packets, want %d", len(packets), cfg.numFrames)
	}
	for frame, packet := range packets {
		if len(packet) == 0 {
			t.Fatalf("CBR oracle returned empty packet at frame %d", frame)
		}
	}
}

func mustRunHelper(binPath string, input []byte) []byte {
	out, err := libopustest.RunHelper(binPath, input)
	if err != nil {
		panic(err)
	}
	return out
}

// configEdgesPCM produces a deterministic, seeded multi-tone + envelope signal
// suitable for exercising every coding mode. It is band-limited softly so SILK,
// Hybrid, and CELT all encode meaningful content.
func configEdgesPCM(frameSize, channels, numFrames int) []float32 {
	total := frameSize * channels * numFrames
	pcm := make([]float32, total)
	const sampleRate = 48000
	for f := range numFrames {
		for i := range frameSize {
			n := f*frameSize + i
			tt := float64(n) / float64(sampleRate)
			env := 0.7 + 0.3*math.Sin(2*math.Pi*0.9*tt)
			s := 0.0
			s += 0.30 * math.Sin(2*math.Pi*180*tt)
			s += 0.18 * math.Sin(2*math.Pi*523*tt+0.3)
			s += 0.10 * math.Sin(2*math.Pi*1200*tt+0.7)
			s += 0.05 * math.Sin(2*math.Pi*3300*tt+1.1)
			v := float32(env * s)
			for ch := range channels {
				// Slight inter-channel decorrelation for stereo coverage.
				pcm[(f*frameSize+i)*channels+ch] = v * (1.0 - 0.07*float32(ch))
			}
		}
	}
	return pcm
}

// decodeGopusFloat32Sequence decodes a packet sequence through a single gopus
// decoder instance (stateful), returning interleaved float32 PCM.
func decodeGopusFloat32Sequence(t *testing.T, sampleRate, channels, frameSize int, packets [][]byte) []float32 {
	t.Helper()
	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	out := make([]float32, 0, frameSize*channels*len(packets))
	buf := make([]float32, frameSize*channels)
	for i, pkt := range packets {
		n, err := dec.Decode(pkt, buf)
		if err != nil {
			t.Fatalf("Decode packet %d: %v", i, err)
		}
		out = append(out, buf[:n*channels]...)
	}
	return out
}

// TestDecodeLibopusCBRConfigEdgesMatchesLibopus exercises the gopus decoder on
// CBR libopus-encoded streams across the bandwidth × frame-size × channels grid.
// CBR is a configuration the gopus encoder does not pick by default, so this
// covers decode of libopus output the encoder-driven gates never produce.
func TestDecodeLibopusCBRConfigEdgesMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate = 48000
	const numFrames = 6

	type bwCase struct {
		name string
		bw   uint32
		// frame sizes valid for the encoder bandwidth; libopus clamps SILK to
		// >=10 ms, so NB/MB/WB SILK use >=480 here, while FB CELT covers short
		// frames too.
		frameSizes []int
	}
	bwCases := []bwCase{
		{"nb", cbrBWNarrowband, []int{480, 960, 1920}},
		{"mb", cbrBWMediumband, []int{480, 960, 1920}},
		{"wb", cbrBWWideband, []int{480, 960, 1920, 2880}},
		{"swb", cbrBWSuperwideband, []int{480, 960, 1920}},
		{"fb", cbrBWFullband, []int{120, 240, 480, 960, 1920, 2880}},
	}

	for _, channels := range []int{1, 2} {
		for _, bc := range bwCases {
			for _, frameSize := range bc.frameSizes {
				name := fmt.Sprintf("ch%d_%s_fs%d", channels, bc.name, frameSize)
				t.Run(name, func(t *testing.T) {
					// Choose a moderate per-bandwidth bitrate so CBR fits the
					// frame budget at all sizes.
					bitrate := 48000
					if channels == 2 {
						bitrate = 96000
					}
					cfg := cbrEncodeConfig{
						app:        cbrAppAudio,
						bandwidth:  bc.bw,
						channels:   channels,
						bitrate:    bitrate,
						frameSize:  frameSize,
						complexity: 10,
						numFrames:  numFrames,
					}
					pcm := configEdgesPCM(frameSize, channels, numFrames)
					packets, err := encodeLibopusCBRPackets(cfg, pcm)
					if err != nil {
						libopustest.HelperUnavailable(t, "cbr encode", err)
					}
					if len(packets) == 0 {
						t.Fatalf("libopus produced no packets")
					}

					want, err := decodeWithLibopusReferenceAPIRateFloat32(sampleRate, channels, frameSize, packets)
					if err != nil {
						libopustest.HelperUnavailable(t, "cbr reference decode", err)
					}
					got := decodeGopusFloat32Sequence(t, sampleRate, channels, frameSize, packets)
					assertSelectedPublicAPIRateFloat32(t, got, want, sampleRate, channels, name)
				})
			}
		}
	}
}

// TestDecodeLibopusBoundaryBitratesMatchesLibopus exercises the extreme bitrate
// edges: the 6 kbps minimum and 510 kbps maximum, across mode-selecting
// bandwidths and frame sizes. The libopus encoder picks SILK/Hybrid/CELT
// internally at these rates; the gopus decoder must track its output.
func TestDecodeLibopusBoundaryBitratesMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate = 48000
	const numFrames = 6

	type edge struct {
		name      string
		bw        uint32
		bitrate   int
		channels  int
		frameSize int
	}
	edges := []edge{
		// 6 kbps minimum: libopus forces SILK/NB-class at the floor.
		{"min6k_nb_mono_20ms", cbrBWNarrowband, 6000, 1, 960},
		{"min6k_wb_mono_20ms", cbrBWWideband, 6000, 1, 960},
		{"min6k_wb_mono_60ms", cbrBWWideband, 6000, 1, 2880},
		// 510 kbps maximum: libopus forces CELT/FB at the ceiling.
		{"max510k_fb_mono_20ms", cbrBWFullband, 510000, 1, 960},
		{"max510k_fb_stereo_20ms", cbrBWFullband, 510000, 2, 960},
		{"max510k_fb_stereo_5ms", cbrBWFullband, 510000, 2, 240},
		{"max510k_fb_stereo_2_5ms", cbrBWFullband, 510000, 2, 120},
		// Mode-selection boundary region: ~16-24 kbps WB toggles SILK<->Hybrid.
		{"bound16k_wb_mono_20ms", cbrBWWideband, 16000, 1, 960},
		{"bound24k_swb_stereo_20ms", cbrBWSuperwideband, 24000, 2, 960},
		{"bound64k_fb_stereo_20ms", cbrBWFullband, 64000, 2, 960},
	}

	for _, e := range edges {
		t.Run(e.name, func(t *testing.T) {
			cfg := cbrEncodeConfig{
				app:        cbrAppAudio,
				bandwidth:  e.bw,
				channels:   e.channels,
				bitrate:    e.bitrate,
				frameSize:  e.frameSize,
				complexity: 10,
				numFrames:  numFrames,
			}
			pcm := configEdgesPCM(e.frameSize, e.channels, numFrames)
			packets, err := encodeLibopusCBRPackets(cfg, pcm)
			if err != nil {
				libopustest.HelperUnavailable(t, "cbr encode", err)
			}
			if len(packets) == 0 {
				t.Fatalf("libopus produced no packets")
			}
			want, err := decodeWithLibopusReferenceAPIRateFloat32(sampleRate, e.channels, e.frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "cbr reference decode", err)
			}
			got := decodeGopusFloat32Sequence(t, sampleRate, e.channels, e.frameSize, packets)
			assertSelectedPublicAPIRateFloat32(t, got, want, sampleRate, e.channels, e.name)
		})
	}
}

// transitionSegment describes one libopus-encoded run whose packets are
// concatenated into a single switching stream.
type transitionSegment struct {
	cfg    cbrEncodeConfig
	frames int
}

// buildTransitionStream encodes each segment as a separate libopus CBR run and
// concatenates the packets into one stateful stream. All segments must share the
// channel count (a single decoder instance is used end-to-end). Returns the
// packets and the max frame size across segments (the decoder output buffer
// size). The PCM is phase-continuous across segments so the switch points carry
// real signal.
func buildTransitionStream(t *testing.T, channels int, segments []transitionSegment) (packets [][]byte, maxFrameSize int) {
	t.Helper()
	globalFrame := 0
	for _, seg := range segments {
		if seg.cfg.channels != channels {
			t.Fatalf("segment channels=%d want %d", seg.cfg.channels, channels)
		}
		if seg.cfg.frameSize > maxFrameSize {
			maxFrameSize = seg.cfg.frameSize
		}
		cfg := seg.cfg
		cfg.numFrames = seg.frames
		// Generate phase-continuous PCM offset by the global frame index so the
		// signal flows across the switch boundary.
		pcm := configEdgesPCMOffset(cfg.frameSize, channels, seg.frames, globalFrame*cfg.frameSize)
		segPackets, err := encodeLibopusCBRPackets(cfg, pcm)
		if err != nil {
			libopustest.HelperUnavailable(t, "transition cbr encode", err)
		}
		packets = append(packets, segPackets...)
		globalFrame += seg.frames
	}
	return packets, maxFrameSize
}

// configEdgesPCMOffset is configEdgesPCM with a sample offset so that
// concatenated segments form a continuous waveform.
func configEdgesPCMOffset(frameSize, channels, numFrames, sampleOffset int) []float32 {
	total := frameSize * channels * numFrames
	pcm := make([]float32, total)
	const sampleRate = 48000
	for f := range numFrames {
		for i := range frameSize {
			n := sampleOffset + f*frameSize + i
			tt := float64(n) / float64(sampleRate)
			env := 0.7 + 0.3*math.Sin(2*math.Pi*0.9*tt)
			s := 0.0
			s += 0.30 * math.Sin(2*math.Pi*180*tt)
			s += 0.18 * math.Sin(2*math.Pi*523*tt+0.3)
			s += 0.10 * math.Sin(2*math.Pi*1200*tt+0.7)
			s += 0.05 * math.Sin(2*math.Pi*3300*tt+1.1)
			v := float32(env * s)
			for ch := range channels {
				pcm[(f*frameSize+i)*channels+ch] = v * (1.0 - 0.07*float32(ch))
			}
		}
	}
	return pcm
}

// TestDecodeLibopusModeTransitionsMatchesLibopus exercises within-stream
// transitions: the gopus decoder must track libopus across SILK<->Hybrid<->CELT
// mode switches, NB<->WB<->SWB<->FB bandwidth steps, and frame-size changes
// across consecutive packets. Each segment is a separate libopus CBR encode;
// concatenating their packets produces a real switching stream decoded through a
// single stateful decoder on both sides.
func TestDecodeLibopusModeTransitionsMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate = 48000

	const fs20 = 960 // 20 ms at 48 kHz
	seg := func(app, bw uint32, channels, bitrate, frameSize, frames int) transitionSegment {
		return transitionSegment{
			cfg: cbrEncodeConfig{
				app:        app,
				bandwidth:  bw,
				channels:   channels,
				bitrate:    bitrate,
				frameSize:  frameSize,
				complexity: 10,
			},
			frames: frames,
		}
	}

	type tcase struct {
		name     string
		channels int
		segments []transitionSegment
	}
	cases := []tcase{
		{
			// SILK -> Hybrid -> CELT mode walk (mono).
			name:     "silk_hybrid_celt_mono",
			channels: 1,
			segments: []transitionSegment{
				seg(cbrAppVoIP, cbrBWWideband, 1, 16000, fs20, 3),  // SILK WB
				seg(cbrAppAudio, cbrBWFullband, 1, 64000, fs20, 3), // Hybrid/CELT FB
				seg(cbrAppRestrictedCELT, cbrBWFullband, 1, 96000, fs20, 3),
			},
		},
		{
			// NB -> WB -> SWB -> FB bandwidth steps (mono).
			name:     "bandwidth_steps_mono",
			channels: 1,
			segments: []transitionSegment{
				seg(cbrAppAudio, cbrBWNarrowband, 1, 16000, fs20, 3),
				seg(cbrAppAudio, cbrBWWideband, 1, 32000, fs20, 3),
				seg(cbrAppAudio, cbrBWSuperwideband, 1, 48000, fs20, 3),
				seg(cbrAppAudio, cbrBWFullband, 1, 96000, fs20, 3),
			},
		},
		{
			// Frame-size changes across consecutive frames (FB CELT, mono):
			// 2.5 -> 5 -> 10 -> 20 -> 40 -> 60 ms.
			name:     "frame_size_walk_mono",
			channels: 1,
			segments: []transitionSegment{
				seg(cbrAppRestrictedCELT, cbrBWFullband, 1, 96000, 120, 4),  // 2.5 ms
				seg(cbrAppRestrictedCELT, cbrBWFullband, 1, 96000, 240, 4),  // 5 ms
				seg(cbrAppRestrictedCELT, cbrBWFullband, 1, 96000, 480, 4),  // 10 ms
				seg(cbrAppRestrictedCELT, cbrBWFullband, 1, 96000, 960, 4),  // 20 ms
				seg(cbrAppRestrictedCELT, cbrBWFullband, 1, 96000, 1920, 2), // 40 ms
				seg(cbrAppRestrictedCELT, cbrBWFullband, 1, 96000, 2880, 2), // 60 ms
			},
		},
		{
			// Stereo SILK -> CELT bandwidth+mode walk.
			name:     "silk_celt_stereo",
			channels: 2,
			segments: []transitionSegment{
				seg(cbrAppVoIP, cbrBWWideband, 2, 32000, fs20, 3),   // SILK WB stereo
				seg(cbrAppAudio, cbrBWFullband, 2, 128000, fs20, 3), // CELT/Hybrid FB stereo
				seg(cbrAppRestrictedCELT, cbrBWFullband, 2, 192000, fs20, 3),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			packets, maxFrameSize := buildTransitionStream(t, tc.channels, tc.segments)
			if len(packets) == 0 {
				t.Fatalf("no packets produced")
			}
			// Decode through a single libopus decoder (stateful, max frame size).
			steps := make([]libopusAPIRateDecodeStep, len(packets))
			for i, pkt := range packets {
				steps[i] = libopusAPIRateDecodeStep{packet: pkt}
			}
			wantI16, err := decodeWithLibopusReferenceAPIRateInt16VariableSteps(sampleRate, tc.channels, maxFrameSize, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "transition reference decode", err)
			}
			// Decode through a single gopus decoder (stateful).
			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, tc.channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			gotI16 := make([]int16, 0, len(wantI16))
			buf := make([]int16, maxFrameSize*tc.channels)
			for i, pkt := range packets {
				n, err := dec.DecodeInt16(pkt, buf)
				if err != nil {
					t.Fatalf("DecodeInt16 packet %d: %v", i, err)
				}
				gotI16 = append(gotI16, buf[:n*tc.channels]...)
			}
			assertAPIRateQualityInt16(t, gotI16, wantI16, sampleRate, tc.channels, tc.name)
			assertAPIRateInt16Exact(t, gotI16, wantI16, tc.name)
		})
	}
}

// TestDecodeLibopusRestrictedAppEdgesMatchesLibopus exercises decode of streams
// from the restricted-SILK and restricted-CELT applications, which lock the
// encoder into a single coding mode the auto encoder would not always pick.
func TestDecodeLibopusRestrictedAppEdgesMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate = 48000
	const numFrames = 6

	type rc struct {
		name      string
		app       uint32
		bw        uint32
		channels  int
		bitrate   int
		frameSize int
	}
	cases := []rc{
		{"rsilk_nb_mono_20ms", cbrAppRestrictedSILK, cbrBWNarrowband, 1, 24000, 960},
		{"rsilk_wb_stereo_20ms", cbrAppRestrictedSILK, cbrBWWideband, 2, 48000, 960},
		{"rsilk_mb_mono_40ms", cbrAppRestrictedSILK, cbrBWMediumband, 1, 24000, 1920},
		{"rcelt_fb_mono_10ms", cbrAppRestrictedCELT, cbrBWFullband, 1, 64000, 480},
		{"rcelt_fb_stereo_20ms", cbrAppRestrictedCELT, cbrBWFullband, 2, 128000, 960},
		{"rcelt_fb_mono_2_5ms", cbrAppRestrictedCELT, cbrBWFullband, 1, 96000, 120},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := cbrEncodeConfig{
				app:        c.app,
				bandwidth:  c.bw,
				channels:   c.channels,
				bitrate:    c.bitrate,
				frameSize:  c.frameSize,
				complexity: 10,
				numFrames:  numFrames,
			}
			pcm := configEdgesPCM(c.frameSize, c.channels, numFrames)
			packets, err := encodeLibopusCBRPackets(cfg, pcm)
			if err != nil {
				libopustest.HelperUnavailable(t, "cbr encode", err)
			}
			if len(packets) == 0 {
				t.Fatalf("libopus produced no packets")
			}
			want, err := decodeWithLibopusReferenceAPIRateFloat32(sampleRate, c.channels, c.frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "cbr reference decode", err)
			}
			got := decodeGopusFloat32Sequence(t, sampleRate, c.channels, c.frameSize, packets)
			assertSelectedPublicAPIRateFloat32(t, got, want, sampleRate, c.channels, c.name)
		})
	}
}

func encodeSILKVoicedPLCTestSequence(t *testing.T, channels int) [][]byte {
	t.Helper()
	const (
		sampleRate = 48000
		frameSize  = 960
		bitrate    = 24000
		numFrames  = 8
	)
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationRestrictedSilk,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetMode(EncoderModeSILK); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthWideband); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(bitrate * channels); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetSignal(SignalVoice); err != nil {
		t.Fatalf("SetSignal: %v", err)
	}

	packets := make([][]byte, 0, numFrames)
	for frameIndex := range numFrames {
		pcm := make([]float32, frameSize*channels)
		for i := range frameSize {
			tm := float64(frameIndex*frameSize+i) / sampleRate
			s := 0.35*float32(math.Sin(2*math.Pi*220*tm)) +
				0.15*float32(math.Sin(2*math.Pi*440*tm+0.21)) +
				0.08*float32(math.Sin(2*math.Pi*880*tm+0.43))
			for ch := range channels {
				pcm[i*channels+ch] = s
			}
		}
		packet, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("Encode frame %d: %v", frameIndex, err)
		}
		if len(packet) == 0 {
			t.Fatalf("Encode frame %d produced no packet", frameIndex)
		}
		toc := ParseTOC(packet[0])
		if toc.Mode != ModeSILK {
			t.Fatalf("Encode frame %d mode=%v want SILK", frameIndex, toc.Mode)
		}
		packets = append(packets, append([]byte(nil), packet...))
	}
	return packets
}

// TestSILKPLCIIRFirstLossOutputNonSilentMatchesLibopus verifies that the first
// concealed frame after a voiced SILK sequence is non-silent and (with oracle)
// matches libopus samples. Covers the lossCnt=0 branch in silk_PLC_conceal()
// where harmAttQ15_0=0.99 and randScaleQ14 is initialised.
func TestSILKPLCIIRFirstLossOutputNonSilentMatchesLibopus(t *testing.T) {
	const channels = 1
	packets := encodeSILKVoicedPLCTestSequence(t, channels)

	dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	const frameSize = 960
	buf := make([]float32, frameSize*channels)

	// Warm up: decode first 4 packets normally.
	for i := range 4 {
		if _, err := dec.Decode(packets[i], buf); err != nil {
			t.Fatalf("Decode packet %d: %v", i, err)
		}
	}

	// Simulate loss of packet 4.
	n, err := dec.Decode(nil, buf)
	if err != nil {
		t.Fatalf("Decode(nil) PLC: %v", err)
	}
	if n != frameSize {
		t.Fatalf("Decode(nil) samples=%d want %d", n, frameSize)
	}

	// First concealed frame should be non-silent (PLC reproduces speech).
	energy := float64(0)
	for _, s := range buf[:n*channels] {
		energy += float64(s) * float64(s)
	}
	energy /= float64(n * channels)
	if energy < 1e-6 {
		t.Fatalf("first PLC frame RMS²=%.2e, expected non-silent speech extrapolation", energy)
	}

	// With oracle: verify exact samples and retain the quality diagnostic.
	if !libopustest.OracleEnabled() {
		return
	}
	steps := []libopusAPIRateDecodeStep{
		{packet: packets[0]},
		{packet: packets[1]},
		{packet: packets[2]},
		{packet: packets[3]},
		{}, // nil packet → PLC
	}
	want, err := decodeWithLibopusReferenceAPIRateFloat32Steps(48000, channels, frameSize, steps)
	if err != nil {
		libopustest.HelperUnavailable(t, "SILK PLC IIR first-loss reference", err)
	}
	// Compare only the PLC frame (last frameSize*channels samples).
	wantPLC := want[4*frameSize*channels:]
	got, err := NewDecoder(DefaultDecoderConfig(48000, channels))
	if err != nil {
		t.Fatalf("oracle compare: NewDecoder: %v", err)
	}
	gotBuf := make([]float32, frameSize*channels*5)
	for i := range 4 {
		if _, err := got.Decode(packets[i], gotBuf[i*frameSize:(i+1)*frameSize]); err != nil {
			t.Fatalf("oracle compare: Decode packet %d: %v", i, err)
		}
	}
	gotPLC := gotBuf[4*frameSize : 5*frameSize]
	if _, err := got.Decode(nil, gotPLC); err != nil {
		t.Fatalf("oracle compare: Decode(nil) PLC: %v", err)
	}

	assertAPIRateQualityFloat32PLC(t, gotPLC, wantPLC, 48000, channels, true,
		"SILK PLC IIR first-loss mono")
	assertAPIRateFloat32BitsExact(t, gotPLC, wantPLC, "SILK PLC IIR first-loss mono")
}

// TestSILKPLCIIRMultiLossEnergyDecaysMatchesLibopus verifies that energy
// decreases monotonically across a consecutive 3-frame loss run. This exercises
// the harmGainQ15 attenuation chain: frame0→harmAttQ15_0(0.99), frame1+→
// harmAttQ15_1(0.95). Reference: silk/PLC.c:353-355.
func TestSILKPLCIIRMultiLossEnergyDecaysMatchesLibopus(t *testing.T) {
	const channels = 1
	packets := encodeSILKVoicedPLCTestSequence(t, channels)

	const frameSize = 960
	energies := make([]float64, 3)

	for lossIdx := range 3 {
		dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
		if err != nil {
			t.Fatalf("NewDecoder (loss %d): %v", lossIdx, err)
		}
		buf := make([]float32, frameSize*channels)
		// Always warm up with 4 packets.
		for i := range 4 {
			if _, err := dec.Decode(packets[i], buf); err != nil {
				t.Fatalf("Decode packet %d (loss %d): %v", i, lossIdx, err)
			}
		}
		// Generate lossIdx+1 consecutive PLC frames and record the last one.
		var lostBuf []float32
		for k := 0; k <= lossIdx; k++ {
			lostBuf = make([]float32, frameSize*channels)
			if _, err := dec.Decode(nil, lostBuf); err != nil {
				t.Fatalf("Decode(nil) loss %d frame %d: %v", lossIdx, k, err)
			}
		}
		e := float64(0)
		for _, s := range lostBuf {
			e += float64(s) * float64(s)
		}
		energies[lossIdx] = e / float64(frameSize*channels)
	}

	// Energy must be strictly decreasing across the first three loss frames.
	for i := 1; i < len(energies); i++ {
		if energies[i] >= energies[i-1] {
			t.Fatalf("PLC energy did not decay: loss[%d]=%.4e >= loss[%d]=%.4e",
				i, energies[i], i-1, energies[i-1])
		}
	}

	// With oracle: compare each PLC frame against libopus.
	if !libopustest.OracleEnabled() {
		return
	}
	// Build steps: 4 good + 3 losses.
	steps := make([]libopusAPIRateDecodeStep, 7)
	for i := range 4 {
		steps[i] = libopusAPIRateDecodeStep{packet: packets[i]}
	}
	// steps[4..6] are nil (PLC).
	want, err := decodeWithLibopusReferenceAPIRateFloat32Steps(48000, channels, frameSize, steps)
	if err != nil {
		libopustest.HelperUnavailable(t, "SILK PLC IIR multi-loss reference", err)
	}
	// Decode with gopus and compare.
	dec2, err := NewDecoder(DefaultDecoderConfig(48000, channels))
	if err != nil {
		t.Fatalf("oracle compare: NewDecoder: %v", err)
	}
	got := make([]float32, 0, len(want))
	buf2 := make([]float32, frameSize*channels)
	for i := range 4 {
		n, err := dec2.Decode(packets[i], buf2)
		if err != nil {
			t.Fatalf("oracle compare: Decode packet %d: %v", i, err)
		}
		got = append(got, buf2[:n*channels]...)
	}
	for k := range 3 {
		n, err := dec2.Decode(nil, buf2)
		if err != nil {
			t.Fatalf("oracle compare: Decode(nil) loss %d: %v", k, err)
		}
		got = append(got, buf2[:n*channels]...)
	}
	// PLC frames dominate the comparison; use PLC quality bar.
	assertAPIRateQualityFloat32PLC(t, got, want, 48000, channels, true,
		"SILK PLC IIR multi-loss mono")
	assertAPIRateFloat32BitsExact(t, got, want, "SILK PLC IIR multi-loss mono")
}

// TestSILKPLCIIRVoicedUnvoicedTransitionNoPanic verifies that the full-stack
// decode path does not panic when a SILK voiced frame is followed by a PLC loss.
// This exercises the voiced→PLC-concealment transition in the decoder path.
func TestSILKPLCIIRVoicedUnvoicedTransitionNoPanic(t *testing.T) {
	for _, channels := range []int{1, 2} {
		t.Run("ch_"+itoaSmall(channels), func(t *testing.T) {
			packets := encodeSILKVoicedPLCTestSequence(t, channels)
			const frameSize = 960
			dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			buf := make([]float32, frameSize*channels)

			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Decode(nil) panic on voiced→PLC transition: %v", r)
				}
			}()

			for i := range 5 {
				if _, err := dec.Decode(packets[i], buf); err != nil {
					t.Fatalf("Decode packet %d: %v", i, err)
				}
			}
			n, err := dec.Decode(nil, buf)
			if err != nil {
				t.Fatalf("Decode(nil) PLC: %v", err)
			}
			if n != frameSize {
				t.Fatalf("Decode(nil) samples=%d want %d", n, frameSize)
			}
		})
	}
}

// TestSILKPLCIIRStereoMultiLossEnergyDecays verifies the stereo PLC IIR energy
// decay chain mirrors the mono case. libopus processes left/right channels
// independently through the same silk_PLC_conceal path.
func TestSILKPLCIIRStereoMultiLossEnergyDecays(t *testing.T) {
	const channels = 2
	packets := encodeSILKVoicedPLCTestSequence(t, channels)

	const frameSize = 960
	var prevEnergy = math.MaxFloat64
	dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	buf := make([]float32, frameSize*channels)
	for i := range 4 {
		if _, err := dec.Decode(packets[i], buf); err != nil {
			t.Fatalf("Decode packet %d: %v", i, err)
		}
	}

	for k := range 3 {
		lostBuf := make([]float32, frameSize*channels)
		if _, err := dec.Decode(nil, lostBuf); err != nil {
			t.Fatalf("Decode(nil) loss %d: %v", k, err)
		}
		e := float64(0)
		for _, s := range lostBuf {
			e += float64(s) * float64(s)
		}
		e /= float64(frameSize * channels)
		if k > 0 && e >= prevEnergy {
			t.Fatalf("stereo PLC energy did not decay at loss %d: %.4e >= %.4e", k, e, prevEnergy)
		}
		prevEnergy = e
	}
}

func TestDecodeMalformedVBRPreservesSelectedLibopusState(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		packet := append([]byte(nil), encodeAPIRateSILKPacket(t, channels)...)
		body := packet[1:]
		makeBad := func(padding, oversizedLast bool) []byte {
			header := []byte{packet[0]&^3 | 3, 0x83}
			if padding {
				header[1] |= 0x40
				header = append(header, 3)
			}
			var length [2]byte
			n := encodeFrameLength(length[:], len(body))
			header = append(header, length[:n]...)
			if oversizedLast {
				header = append(header, 0)
			} else {
				header = append(header, 2)
			}
			header = append(header, body...)
			if oversizedLast {
				header = append(header, make([]byte, maxOpusFrameBytes+1)...)
			}
			if padding {
				header = append(header, 0, 0, 0)
			}
			return header
		}
		for _, format := range []uint32{libopustest.DecodeDiffFormatFloat32, libopustest.DecodeDiffFormatInt16, libopustest.DecodeDiffFormatInt24} {
			for _, padded := range []bool{false, true} {
				for _, oversized := range []bool{false, true} {
					t.Run(fmt.Sprintf("ch%d/format%d/padded%t/oversized%t", channels, format, padded, oversized), func(t *testing.T) {
						bad := makeBad(padded, oversized)
						steps := []libopustest.DecodeDiffCase{
							{Packet: packet, Format: format, FrameSize: 960},
							{Packet: bad, Format: format, FrameSize: 2880},
							{Packet: packet, Format: format, FrameSize: 960},
							{Format: format, FrameSize: 960},
							{Packet: packet, Format: format, FrameSize: 960},
						}
						want, err := libopustest.ProbeDecodeSequence(48000, channels, steps)
						if err != nil {
							t.Fatal(err)
						}
						if want[1].Code != -4 || want[1].FinalRange != want[0].FinalRange {
							t.Fatalf("C malformed packet status=%d range=%08x, preceding=%08x", want[1].Code, want[1].FinalRange, want[0].FinalRange)
						}
						cfg := DefaultDecoderConfig(48000, channels)
						cfg.MaxPacketBytes = 2 * maxOpusFrameBytes
						dec, err := NewDecoder(cfg)
						if err != nil {
							t.Fatal(err)
						}
						f32 := make([]float32, 2880*channels)
						i16 := make([]int16, len(f32))
						i24 := make([]int32, len(f32))
						decode := func(step libopustest.DecodeDiffCase) (int, error) {
							n := int(step.FrameSize) * channels
							switch format {
							case libopustest.DecodeDiffFormatInt16:
								return dec.DecodeInt16(step.Packet, i16[:n])
							case libopustest.DecodeDiffFormatInt24:
								return dec.DecodeInt24(step.Packet, i24[:n])
							default:
								return dec.Decode(step.Packet, f32[:n])
							}
						}
						for i, step := range steps {
							n, err := decode(step)
							if want[i].Code < 0 {
								if n != 0 || err != ErrInvalidPacket {
									t.Fatalf("step%d decode=(%d,%v), C=%d", i, n, err, want[i].Code)
								}
							} else if err != nil || int32(n) != want[i].Code {
								t.Fatalf("step%d decode=(%d,%v), C=%d", i, n, err, want[i].Code)
							}
							if dec.FinalRange() != want[i].FinalRange {
								t.Fatalf("step%d range=%08x C=%08x", i, dec.FinalRange(), want[i].FinalRange)
							}
							for j := range n * channels {
								var got, expected uint32
								switch format {
								case libopustest.DecodeDiffFormatInt16:
									got, expected = uint32(uint16(i16[j])), uint32(binary.LittleEndian.Uint16(want[i].PCM[j*2:]))
								case libopustest.DecodeDiffFormatInt24:
									got, expected = uint32(i24[j]), binary.LittleEndian.Uint32(want[i].PCM[j*4:])
								default:
									got, expected = math.Float32bits(f32[j]), binary.LittleEndian.Uint32(want[i].PCM[j*4:])
								}
								if got != expected {
									t.Fatalf("step%d sample%d raw=%08x C=%08x", i, j, got, expected)
								}
							}
						}
						if allocs := testing.AllocsPerRun(20, func() {
							if n, err := decode(steps[1]); n != 0 || err != ErrInvalidPacket {
								t.Fatalf("malformed decode=(%d,%v)", n, err)
							}
						}); allocs != 0 {
							t.Fatalf("warm malformed decode allocations=%g, want0", allocs)
						}
					})
				}
			}
		}
	}
}

func TestDecodeMalformedRawCSequenceWitnessPreservesState(t *testing.T) {
	libopustest.RequireOracle(t)
	const channels = 1
	packet := encodeAPIRateSILKPacket(t, channels)
	bad := []byte{0x4b, 0x83, 0x02, 0x01, 0x00, 0x00}
	steps := []libopustest.DecodeDiffCase{
		{Packet: packet, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 960},
		{Packet: bad, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 2880},
		{Packet: packet, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 960},
		{Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 960},
		{Packet: packet, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 960},
	}
	want, err := libopustest.ProbeDecodeSequence(48000, channels, steps)
	if err != nil {
		t.Fatal(err)
	}
	if want[1].Code != -4 || want[1].FinalRange != want[0].FinalRange {
		t.Fatalf("C witness status=%d range=%08x, preceding=%08x", want[1].Code, want[1].FinalRange, want[0].FinalRange)
	}

	cfg := DefaultDecoderConfig(48000, channels)
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]float32, 2880*channels)
	for i, step := range steps {
		n, err := dec.Decode(step.Packet, pcm[:int(step.FrameSize)*channels])
		if want[i].Code < 0 {
			if n != 0 || err != ErrInvalidPacket {
				t.Fatalf("step%d decode=(%d,%v), C=%d", i, n, err, want[i].Code)
			}
		} else if err != nil || int32(n) != want[i].Code {
			t.Fatalf("step%d decode=(%d,%v), C=%d", i, n, err, want[i].Code)
		}
		if dec.FinalRange() != want[i].FinalRange {
			t.Fatalf("step%d range=%08x C=%08x", i, dec.FinalRange(), want[i].FinalRange)
		}
		for j := range n * channels {
			if got, expected := math.Float32bits(pcm[j]), binary.LittleEndian.Uint32(want[i].PCM[j*4:]); got != expected {
				t.Fatalf("step%d sample%d raw=%08x C=%08x", i, j, got, expected)
			}
		}
	}
}

func TestMonoHybridShortPLCBackgroundMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	packetHex := []string{
		"5cee4d1b5636a384d5974ea37cc01e037e8bff7a9a3416e1b58099326287f76046b0e3e06f8a922c0f9c4fd178c6bcc2e93cee1b6ecb1729adfeafd2e299b45c85263f0f78162486b50cb7b678bbe58284bb33ced74b818618aa4cb5dd22397e467975ba74187a4c836438e4f2f19cb63c96cb7445c3b1360f8e99c8b08875f0b3bf2b6ec2199a5d19db9cd95e5cc961980694f2a588f8b5a4c3f23a0d47588fffa56de233f46f1940b2abc601895f77978a9693e96f2fced293018429d7bf320cd073a8d4bda02c474c2fc20e0b80",
		"40822e685173fe9a9996eb0122e11bebfebeee6dd11997f75d87fe319556ab5da14072",
		"70822e0dfbd19557d6eb45b1ab3a3afa38f40cc6336e613f1b7866db563fa9bf0890a9925c5a91a1cf2d82c71b43639216c08b6eaf05fabc112229568c940d3f360e8bb1cce3ba3234c13ef63a92",
		"",
	}
	formats := []uint32{
		libopustest.DecodeDiffFormatInt24,
		libopustest.DecodeDiffFormatFloat32,
		libopustest.DecodeDiffFormatInt16,
		libopustest.DecodeDiffFormatInt24,
	}
	frameSizes := []uint32{5760, 5760, 5760, 240}
	cases := make([]libopustest.DecodeDiffCase, len(packetHex))
	for i, raw := range packetHex {
		packet, err := hex.DecodeString(raw)
		if err != nil {
			t.Fatal(err)
		}
		cases[i] = libopustest.DecodeDiffCase{Packet: packet, Format: formats[i], FrameSize: frameSizes[i]}
	}
	want, err := libopustest.ProbeDecodeSequence(48000, 2, cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "mono-to-stereo Hybrid short PLC", err)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatal(err)
	}
	const maxSamples = 5760 * 2
	floatPCM := make([]float32, maxSamples)
	int16PCM := make([]int16, maxSamples)
	int24PCM := make([]int32, maxSamples)
	for step, c := range cases {
		var n int
		switch c.Format {
		case libopustest.DecodeDiffFormatFloat32:
			n, err = dec.Decode(c.Packet, floatPCM[:int(c.FrameSize)*2])
		case libopustest.DecodeDiffFormatInt16:
			n, err = dec.DecodeInt16(c.Packet, int16PCM[:int(c.FrameSize)*2])
		default:
			n, err = dec.DecodeInt24(c.Packet, int24PCM[:int(c.FrameSize)*2])
		}
		if err != nil || int32(n) != want[step].Code {
			t.Fatalf("step%d decode=(%d,%v), C returned %d", step, n, err, want[step].Code)
		}
		if got := dec.FinalRange(); got != want[step].FinalRange {
			t.Fatalf("step%d range=%08x C=%08x", step, got, want[step].FinalRange)
		}
		for sample := 0; sample < n*2; sample++ {
			switch c.Format {
			case libopustest.DecodeDiffFormatFloat32:
				got := math.Float32bits(floatPCM[sample])
				want := binary.LittleEndian.Uint32(want[step].PCM[sample*4:])
				if got != want {
					t.Fatalf("step%d sample%d=%08x C=%08x", step, sample, got, want)
				}
			case libopustest.DecodeDiffFormatInt16:
				got := int16PCM[sample]
				want := int16(binary.LittleEndian.Uint16(want[step].PCM[sample*2:]))
				if got != want {
					t.Fatalf("step%d sample%d=%d C=%d", step, sample, got, want)
				}
			default:
				got := int24PCM[sample]
				want := int32(binary.LittleEndian.Uint32(want[step].PCM[sample*4:]))
				if got != want {
					t.Fatalf("step%d sample%d=%d C=%d", step, sample, got, want)
				}
			}
		}
	}
	decodeSequence := func() {
		dec.Reset()
		for step, c := range cases {
			var n int
			switch c.Format {
			case libopustest.DecodeDiffFormatFloat32:
				n, err = dec.Decode(c.Packet, floatPCM[:int(c.FrameSize)*2])
			case libopustest.DecodeDiffFormatInt16:
				n, err = dec.DecodeInt16(c.Packet, int16PCM[:int(c.FrameSize)*2])
			default:
				n, err = dec.DecodeInt24(c.Packet, int24PCM[:int(c.FrameSize)*2])
			}
			if err != nil || int32(n) != want[step].Code {
				panic("decode sequence changed during allocation check")
			}
		}
	}
	for range 3 {
		decodeSequence()
	}
	if allocs := testing.AllocsPerRun(10, decodeSequence); allocs != 0 {
		t.Fatalf("warm decode sequence allocations=%g", allocs)
	}
}

func TestMonoCELTToStereoEnergyHistoryMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	packetHex := []string{
		"c3029fd5d9e8c1cd16f2a29d8ec8d4f65e311779803222ac56befc97631a75c85ae23859c0cb53315a3b16cc85cb286d81a14af585a3810a2cb84ae20c51da3c43466c625da9f29f0815192755eee5bd46548fd8cfe354b535e000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000001f77be29c293286d04598eed16f65e645d80cf162c557cf16e8b316f69d8e107bf71f5b14521fd58786fd1178615472b04ec3ec808d3828e1fe6ac4370d347348d5e4251a4d100b6139a99fd5d9e8c1cd16f2a29d8ec8d4f65e311779803222ac56befc97631a75c85ae23859c0cb53315a3b16cc85cb286d81a14af585a3810a2cb84ae20c51da3c43466c625da9f29f0815192755eee5bd46548fd8cfe354b535e000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000001f77be29c293286d04598eed16f65e645d80cf162c557cf16e8b316f69d8e107bf71f5b14521fd58786fd1178615472b04ec3ec808d3828e1fe6ac4370d347348d5e4251a4d100b6139a9",
		"f47e0540971e6dfb0379e8868ecd4a9a820822b3a59547b25a61a29495cddfc3b3df03e1202ee2dc4289afb89d15f98f21bc3058291fdac32d01fbfeab9f8c29760a03e58192bff8b6f23029af1cbebf92bf50a78a4a7103b9f1ba2b96dbbd8e1c84a4ec087cdd2c43241a427c0000000000000000000000152e5b82dbe453852650da096955445bd9796955445bd97aabe78b74598aabe78b7459881f0c24f07c3093dc70c71c304d171445c003de3ef15b905d8a4a36bae0141d87e9ace16b87dfeee40ec135ffd00300000f946e9a8fd3d163151bafc50e924a4e9d",
	}
	cases := make([]libopustest.DecodeDiffCase, len(packetHex))
	for i, raw := range packetHex {
		packet, err := hex.DecodeString(raw)
		if err != nil {
			t.Fatal(err)
		}
		cases[i] = libopustest.DecodeDiffCase{
			Packet: packet, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 960,
		}
	}
	want, err := libopustest.ProbeDecodeSequence(8000, 2, cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "mono-to-stereo CELT sequence", err)
	}
	dec, err := NewDecoder(DefaultDecoderConfig(8000, 2))
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]float32, 960*2)
	wantCounts := [...]int32{40, 80}
	for step := range cases {
		if want[step].Code != wantCounts[step] {
			t.Fatalf("C step%d returned %d samples, want %d", step, want[step].Code, wantCounts[step])
		}
		n, err := dec.Decode(cases[step].Packet, pcm)
		if err != nil || int32(n) != want[step].Code {
			t.Fatalf("step%d decode=(%d,%v), C returned %d", step, n, err, want[step].Code)
		}
		if got, expected := dec.FinalRange(), want[step].FinalRange; got != expected {
			t.Fatalf("step%d range=%08x C=%08x", step, got, expected)
		}
		for sample, got := range pcm[:n*2] {
			if expected := binary.LittleEndian.Uint32(want[step].PCM[sample*4:]); math.Float32bits(got) != expected {
				t.Fatalf("step%d sample%d=%08x C=%08x", step, sample, math.Float32bits(got), expected)
			}
		}
	}
	decodeSequence := func() {
		dec.Reset()
		for step, c := range cases {
			n, err := dec.Decode(c.Packet, pcm)
			if err != nil || int32(n) != want[step].Code {
				panic("decode sequence changed during allocation check")
			}
		}
	}
	for range 3 {
		decodeSequence()
	}
	if allocs := testing.AllocsPerRun(10, decodeSequence); allocs != 0 {
		t.Fatalf("warm decode sequence allocations=%g", allocs)
	}
}

func TestMonoHybridPLCAfterRecoveryMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const seed = 364
	prime := mustDecodeHex(t,
		"f3069fd5d9e8c1cd16f2a29d8ec8d4f65e311779803222ac56befc97631a75c85ae23859c0cb53315a3b16cc85cb286d"+
			"81a14af585a3810a2cb84ae20c51da3c43466c625da9f29f0815192755eee5bd46548fd8cfe354b535e0000000000000"+
			"00000000000000000000000000000000000000000000000000000000000000000000000000000001f77be29c293286d0"+
			"4598eed16f65e645d80cf162c557cf16e8b316f69d8e107bf71f5b14521fd58786fd1178615472b04ec3ec808d3828e1"+
			"fe6ac4370d347348d5e4251a4d100b6139a99fd5d9e8c1cd16f2a29d8ec8d4f65e311779803222ac56befc97631a75c8"+
			"5ae23859c0cb53315a3b16cc85cb286d81a14af585a3810a2cb84ae20c51da3c43466c625da9f29f0815192755eee5bd"+
			"46548fd8cfe354b535e00000000000000000000000000000000000000000000000000000000000000000000000000000"+
			"0000000000000001f77be29c293286d04598eed16f65e645d80cf162c557cf16e8b316f69d8e107bf71f5b14521fd587"+
			"86fd1178615472b04ec3ec808d3828e1fe6ac4370d347348d5e4251a4d100b6139a99fd5d9e8c1cd16f2a29d8ec8d4f6"+
			"5e311779803222ac56befc97631a75c85ae23859c0cb53315a3b16cc85cb286d81a14af585a3810a2cb84ae20c51da3c"+
			"43466c625da9f29f0815192755eee5bd46548fd8cfe354b535e000000000000000000000000000000000000000000000"+
			"000000000000000000000000000000000000000000000001f77be29c293286d04598eed16f65e645d80cf162c557cf16"+
			"e8b316f69d8e107bf71f5b14521fd58786fd1178615472b04ec3ec808d3828e1fe6ac4370d347348d5e4251a4d100b61"+
			"39a99fd5d9e8c1cd16f2a29d8ec8d4f65e311779803222ac56befc97631a75c85ae23859c0cb53315a3b16cc85cb286d"+
			"81a14af585a3810a2cb84ae20c51da3c43466c625da9f29f0815192755eee5bd46548fd8cfe354b535e0000000000000"+
			"00000000000000000000000000000000000000000000000000000000000000000000000000000001f77be29c293286d0"+
			"4598eed16f65e645d80cf162c557cf16e8b316f69d8e107bf71f5b14521fd58786fd1178615472b04ec3ec808d3828e1"+
			"fe6ac4370d347348d5e4251a4d100b6139a99fd5d9e8c1cd16f2a29d8ec8d4f65e311779803222ac56befc97631a75c8"+
			"5ae23859c0cb53315a3b16cc85cb286d81a14af585a3810a2cb84ae20c51da3c43466c625da9f29f0815192755eee5bd"+
			"46548fd8cfe354b535e00000000000000000000000000000000000000000000000000000000000000000000000000000"+
			"0000000000000001f77be29c293286d04598eed16f65e645d80cf162c557cf16e8b316f69d8e107bf71f5b14521fd587"+
			"86fd1178615472b04ec3ec808d3828e1fe6ac4370d347348d5e4251a4d100b6139a99fd5d9e8c1cd16f2a29d8ec8d4f6"+
			"5e311779803222ac56befc97631a75c85ae23859c0cb53315a3b16cc85cb286d81a14af585a3810a2cb84ae20c51da3c"+
			"43466c625da9f29f0815192755eee5bd46548fd8cfe354b535e000000000000000000000000000000000000000000000"+
			"000000000000000000000000000000000000000000000001f77be29c293286d04598eed16f65e645d80cf162c557cf16"+
			"e8b316f69d8e107bf71f5b14521fd58786fd1178615472b04ec3ec808d3828e1fe6ac4370d347348d5e4251a4d100b61"+
			"39a9",
	)
	mutant := mustDecodeHex(t,
		"819ecc3c1abc39107aac7ce5ce18793519666ce6d2503fd70b3adf02a51812d57da03d547d792672cc9f7f808d0be1b4"+
			"cb109a8ff8a5c3d7442690fb306450df69e14aa80d030a7671a2c2da3496d21c540e38a611329fcf9e364a4a50bf8e0b"+
			"97187cd5c4e27a5e6d1420e967fa3dedcf2c0c9ebb89002f9ead28000000000000000000000000000000000000000000"+
			"000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"+
			"0000000000000000000000000000000000000000000000000000000000000000000000000000000000000036749914e1"+
			"499436820b31dda2decbc62a4a7cefdd97a77596e8b3157b56c7182e3b8fa6bd1487f550f61fa22e0512fd2fd5641acf"+
			"ae94416cfc213656ba83d23cb0a3da8bbeec13ee1fd52bf0234c173baba139c99e8dec9f5df96dcb21c68073d31d3056"+
			"bbf06a651845f7aa47b3bcee2f6f3bdbd242873470de6cda4a0605f7abcff78fcb09a158aab56423d075d6f55028996d"+
			"9533513151df8592b280dac28205816e8488d94b5d7cc90615a4530698cae50a3103df86d3290c40012d528fc0822827"+
			"f81a5cbec3bae89183a1bbbaeb7e92548400f8175afb855026afc6e99594504d6eb31c3534f6c35b87d1018c9ea5f770"+
			"503f20ab0e952059b294a8ed4d9e59817703c96683cff1eec8be4206f25706a463f268586a6c4e4d2dd15387c95a5da2"+
			"3022bbb472ef4ff9d8dd2f3e7b13a5825bea03d1291644ae6b8830462124a501e32490a5cea2e09dadfed26215a9a033"+
			"8511a5230231aac06e4406daa8acf7a72db28f2d675220ff1cdb3860e40c72741ab481f103e0fdfaeee27f3019a63626"+
			"b03d73724783707a98998b26c3fdc9",
	)
	recovery := mustDecodeHex(t,
		"f89ecc3c1abc39107aac7c6f0f14812ad56396763f3811fbbcdeb4484cc2341f57f4397aebf1ae9cd2861f0b00c65fd6"+
			"d1bc1ade33026844ea9e43cb87cd45b73eedcafdd10e92bae665c74952f8d9e7b0d52290a322b6e7f9f08c2015ec7760"+
			"e37b614827315ae6237d4ac567edabc589879eb69ae19d328617ed4e3a42650257780886508000000000000000000000"+
			"000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"+
			"000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"+
			"000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"+
			"000000000000000000000000000021762d14e1499436820b31dda2decbc315253e77eecbd3bacb74598abdab638c171d"+
			"c7d35e8a43faa87b0fd117028815c48573e2ee0ec143f26fc4896a9696ed5817e22fc65ce0ff2b1d5295f810d341739e"+
			"b5a139c9",
	)
	packets := [][]byte{prime, mutant, recovery, nil, prime}
	formats := [...]uint32{
		libopustest.DecodeDiffFormatInt16,
		libopustest.DecodeDiffFormatInt24,
		libopustest.DecodeDiffFormatFloat32,
		libopustest.DecodeDiffFormatInt16,
		libopustest.DecodeDiffFormatInt24,
	}
	frameSizes := [...]uint32{960, 960, 960, 40, 960}
	cases := make([]libopustest.DecodeDiffCase, len(packets))
	for i, packet := range packets {
		cases[i] = libopustest.DecodeDiffCase{Packet: packet, Format: formats[i], FrameSize: frameSizes[i]}
	}
	want, err := libopustest.ProbeDecodeSequence(8000, 2, cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "mono Hybrid PLC recovery", err)
	}
	dec, err := NewDecoder(DefaultDecoderConfig(8000, 2))
	if err != nil {
		t.Fatal(err)
	}
	const maxSamples = 960 * 2
	floatPCM := make([]float32, maxSamples)
	int16PCM := make([]int16, maxSamples)
	int24PCM := make([]int32, maxSamples)
	for step, c := range cases {
		var n int
		switch c.Format {
		case libopustest.DecodeDiffFormatFloat32:
			n, err = dec.Decode(c.Packet, floatPCM[:int(c.FrameSize)*2])
		case libopustest.DecodeDiffFormatInt16:
			n, err = dec.DecodeInt16(c.Packet, int16PCM[:int(c.FrameSize)*2])
		default:
			n, err = dec.DecodeInt24(c.Packet, int24PCM[:int(c.FrameSize)*2])
		}
		if err != nil || int32(n) != want[step].Code {
			t.Fatalf("step%d decode=(%d,%v), C returned %d", step, n, err, want[step].Code)
		}
		if got, expected := dec.FinalRange(), want[step].FinalRange; got != expected {
			t.Fatalf("step%d range=%08x C=%08x", step, got, expected)
		}
		for sample := 0; sample < n*2; sample++ {
			switch c.Format {
			case libopustest.DecodeDiffFormatFloat32:
				got := math.Float32bits(floatPCM[sample])
				if expected := binary.LittleEndian.Uint32(want[step].PCM[sample*4:]); got != expected {
					t.Fatalf("step%d sample%d=%08x C=%08x", step, sample, got, expected)
				}
			case libopustest.DecodeDiffFormatInt16:
				got := int16PCM[sample]
				if expected := int16(binary.LittleEndian.Uint16(want[step].PCM[sample*2:])); got != expected {
					t.Fatalf("step%d sample%d=%d C=%d", step, sample, got, expected)
				}
			default:
				got := int24PCM[sample]
				if expected := int32(binary.LittleEndian.Uint32(want[step].PCM[sample*4:])); got != expected {
					t.Fatalf("step%d sample%d=%d C=%d", step, sample, got, expected)
				}
			}
		}
	}
}

func assertDecoderSequenceFormatsMatchSelectedLibopus(t *testing.T, rate, channels int, packets [][]byte) {
	t.Helper()
	frameSize := rate / 50
	for format := uint32(0); format < 3; format++ {
		t.Run(fmt.Sprintf("format%d", format), func(t *testing.T) {
			cases := make([]libopustest.DecodeDiffCase, len(packets))
			for i, p := range packets {
				cases[i] = libopustest.DecodeDiffCase{Packet: p, Format: format, FrameSize: uint32(frameSize)}
			}
			want, err := libopustest.ProbeDecodeSequence(rate, channels, cases)
			if err != nil {
				t.Fatal(err)
			}
			d, err := NewDecoder(DefaultDecoderConfig(rate, channels))
			if err != nil {
				t.Fatal(err)
			}
			pcm := make([]float32, frameSize*channels)
			pcm16 := make([]int16, frameSize*channels)
			pcm24 := make([]int32, frameSize*channels)
			decode := func(p []byte) (int, error) {
				switch format {
				case 0:
					return d.Decode(p, pcm)
				case 1:
					return d.DecodeInt16(p, pcm16)
				default:
					return d.DecodeInt24(p, pcm24)
				}
			}
			for step, p := range packets {
				n, err := decode(p)
				if err != nil || n != int(want[step].Code) {
					t.Fatalf("step%d samples%d err%v C%d", step, n, err, want[step].Code)
				}
				if d.FinalRange() != want[step].FinalRange {
					t.Fatalf("step%d range%08x C%08x", step, d.FinalRange(), want[step].FinalRange)
				}
				switch format {
				case 0:
					for i, v := range want[step].Float32() {
						if math.Float32bits(pcm[i]) != math.Float32bits(v) {
							t.Fatalf("step%d sample%d=%08x C=%08x", step, i, math.Float32bits(pcm[i]), math.Float32bits(v))
						}
					}
				case 1:
					for i, v := range want[step].Int16() {
						if pcm16[i] != v {
							t.Fatalf("step%d sample%d=%d C=%d", step, i, pcm16[i], v)
						}
					}
				case 2:
					for i, v := range want[step].Int24() {
						if pcm24[i] != v {
							t.Fatalf("step%d sample%d=%d C=%d", step, i, pcm24[i], v)
						}
					}
				}
			}
			if allocs := testing.AllocsPerRun(10, func() {
				for _, p := range packets {
					if _, err := decode(p); err != nil {
						panic(err)
					}
				}
			}); allocs != 0 {
				t.Fatalf("warm allocations=%g", allocs)
			}
		})
	}
}

func TestNewDecoder_ValidParams(t *testing.T) {
	tests := []struct {
		name       string
		sampleRate int
		channels   int
	}{
		{"48kHz_mono", 48000, 1},
		{"48kHz_stereo", 48000, 2},
		{"24kHz_mono", 24000, 1},
		{"24kHz_stereo", 24000, 2},
		{"16kHz_mono", 16000, 1},
		{"16kHz_stereo", 16000, 2},
		{"12kHz_mono", 12000, 1},
		{"12kHz_stereo", 12000, 2},
		{"8kHz_mono", 8000, 1},
		{"8kHz_stereo", 8000, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dec, err := NewDecoder(DefaultDecoderConfig(tt.sampleRate, tt.channels))
			if err != nil {
				t.Fatalf("NewDecoder(%d, %d) unexpected error: %v", tt.sampleRate, tt.channels, err)
			}
			if dec == nil {
				t.Fatal("NewDecoder returned nil decoder")
			}
			if dec.SampleRate() != tt.sampleRate {
				t.Errorf("SampleRate() = %d, want %d", dec.SampleRate(), tt.sampleRate)
			}
			if dec.Channels() != tt.channels {
				t.Errorf("Channels() = %d, want %d", dec.Channels(), tt.channels)
			}
		})
	}
}

func TestNewDecoder_InvalidSampleRate(t *testing.T) {
	// 96000 is only invalid without the gopus_qext build tag.
	invalidRates := []int{0, 1000, 7999, 8001, 44100, -1}
	if !extsupport.QEXT {
		invalidRates = append(invalidRates, 96000)
	}

	for _, rate := range invalidRates {
		t.Run(fmt.Sprintf("rate_%d", rate), func(t *testing.T) {
			dec, err := NewDecoder(DefaultDecoderConfig(rate, 1))
			if err != ErrInvalidSampleRate {
				t.Errorf("NewDecoder(%d, 1) error = %v, want ErrInvalidSampleRate", rate, err)
			}
			if dec != nil {
				t.Error("NewDecoder returned non-nil decoder on error")
			}
		})
	}
}

func TestNewDecoder_InvalidChannels(t *testing.T) {
	invalidChannels := []int{0, -1, 3, 4, 100}

	for _, ch := range invalidChannels {
		t.Run(fmt.Sprintf("channels_%d", ch), func(t *testing.T) {
			dec, err := NewDecoder(DefaultDecoderConfig(48000, ch))
			if err != ErrInvalidChannels {
				t.Errorf("NewDecoder(48000, %d) error = %v, want ErrInvalidChannels", ch, err)
			}
			if dec != nil {
				t.Error("NewDecoder returned non-nil decoder on error")
			}
		})
	}
}

func TestNewDecoderRejectsUnrepresentableMaxPacketSamples(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	sampleRates := []int{48000}
	if extsupport.QEXT {
		sampleRates = append(sampleRates, 96000)
	}
	for _, sampleRate := range sampleRates {
		t.Run(fmt.Sprintf("%dHz", sampleRate), func(t *testing.T) {
			dec, err := NewDecoder(DecoderConfig{
				SampleRate:       sampleRate,
				Channels:         2,
				MaxPacketSamples: maxInt,
			})
			if dec != nil {
				t.Fatal("NewDecoder returned a decoder for an unrepresentable sample cap")
			}
			if err != ErrInvalidMaxPacketSamples {
				t.Fatalf("NewDecoder error=%v, want %v", err, ErrInvalidMaxPacketSamples)
			}
		})
	}
}

func TestNewDecoder_DefaultMaxPacketLimits(t *testing.T) {
	dec, err := NewDecoder(DecoderConfig{
		SampleRate: 48000,
		Channels:   2,
	})
	if err != nil {
		t.Fatalf("NewDecoder() unexpected error: %v", err)
	}
	if dec.maxPacketSamples != defaultMaxPacketSamples {
		t.Fatalf("maxPacketSamples=%d want %d", dec.maxPacketSamples, defaultMaxPacketSamples)
	}
	if dec.maxPacketBytes != defaultMaxPacketBytes {
		t.Fatalf("maxPacketBytes=%d want %d", dec.maxPacketBytes, defaultMaxPacketBytes)
	}
}

func minimalHybridTestPacket20ms() []byte {
	// TOC byte: config=15 (Hybrid FB 20ms), mono, code 0
	// 15 << 3 | 0 << 2 | 0 = 0x78
	toc := byte(0x78)

	// Frame data: minimal valid hybrid data
	// These bytes are designed to produce valid (if near-silence) SILK+CELT decode
	frameData := []byte{
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
	}

	return append([]byte{toc}, frameData...)
}

// minimalHybridTestPacket20msStereo creates a test packet for Hybrid FB 20ms stereo.
// TOC byte (0x7C) indicates: config=15 (Hybrid FB 20ms), stereo, code 0.
func minimalHybridTestPacket20msStereo() []byte {
	// TOC byte: config=15 (Hybrid FB 20ms), stereo, code 0
	// 15 << 3 | 1 << 2 | 0 = 0x7C
	toc := byte(0x7C)

	// Frame data for stereo
	frameData := []byte{
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
	}

	return append([]byte{toc}, frameData...)
}

func TestDecoder_Decode_Float32(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	packet := minimalHybridTestPacket20ms()
	frameSize := 960 // 20ms at 48kHz

	pcmOut := make([]float32, frameSize)
	n, err := dec.Decode(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	if n != frameSize {
		t.Errorf("Decode returned %d samples, want %d", n, frameSize)
	}

	// Check that output buffer was written to (decoder ran without panic)
	t.Logf("Decoded %d samples successfully", n)
}

func TestDecoder_Decode_Int16(t *testing.T) {
	dec := newMonoTestDecoder(t)

	pcmOut := make([]int16, 960)
	n, err := dec.DecodeInt16(minimalHybridTestPacket20ms(), pcmOut)
	if err != nil {
		t.Fatalf("DecodeInt16 error: %v", err)
	}

	if n != 960 {
		t.Errorf("DecodeInt16 returned %d samples, want %d", n, 960)
	}
}

func TestDecodeFloatClearsSoftClipMemOnPacket(t *testing.T) {
	dec := newMonoTestDecoder(t)
	dec.softClipMem[0] = 0.25

	pcm := make([]float32, dec.maxPacketSamples*int(dec.channels))
	if _, err := dec.Decode(minimalHybridTestPacket20ms(), pcm); err != nil {
		t.Fatalf("Decode packet error: %v", err)
	}
	if dec.softClipMem[0] != 0 || dec.softClipMem[1] != 0 {
		t.Fatalf("Decode packet softClipMem=%v want zeroed", dec.softClipMem)
	}
}

func TestDecodeInt16PLCPreservesSoftClipMem(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	want := [2]float32{0.25, -0.125}
	dec.softClipMem = want

	pcm := make([]int16, dec.maxPacketSamples*int(dec.channels))
	if _, err := dec.DecodeInt16(nil, pcm); err != nil {
		t.Fatalf("DecodeInt16 PLC error: %v", err)
	}
	if dec.softClipMem != want {
		t.Fatalf("DecodeInt16 PLC softClipMem=%v want %v", dec.softClipMem, want)
	}
}

func TestDecoder_Decode_BufferTooSmall(t *testing.T) {
	dec := newMonoTestDecoder(t)

	// Buffer too small for float32
	pcmOut := make([]float32, 480)
	_, err := dec.Decode(minimalHybridTestPacket20ms(), pcmOut)
	if err != ErrBufferTooSmall {
		t.Errorf("Decode with small buffer: got %v, want ErrBufferTooSmall", err)
	}

	// Buffer too small for int16
	pcmOut16 := make([]int16, 480)
	_, err = dec.DecodeInt16(minimalHybridTestPacket20ms(), pcmOut16)
	if err != ErrBufferTooSmall {
		t.Errorf("DecodeInt16 with small buffer: got %v, want ErrBufferTooSmall", err)
	}
}

func TestDecodeRejectsCode3DurationOver120msBeforeLocalCap(t *testing.T) {
	cfg := DefaultDecoderConfig(48000, 1)
	cfg.MaxPacketSamples = 7000
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	pcm := make([]float32, cfg.MaxPacketSamples)
	_, err = dec.Decode([]byte{GenerateTOC(31, false, 3), 0x07}, pcm)
	if err != ErrInvalidPacket {
		t.Fatalf("Decode error=%v want %v", err, ErrInvalidPacket)
	}
}

func TestDecodeInt16RejectsCode3DurationOver120msBeforeLocalCap(t *testing.T) {
	cfg := DefaultDecoderConfig(48000, 1)
	cfg.MaxPacketSamples = 7000
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	pcm := make([]int16, cfg.MaxPacketSamples)
	_, err = dec.DecodeInt16([]byte{GenerateTOC(31, false, 3), 0x07}, pcm)
	if err != ErrInvalidPacket {
		t.Fatalf("DecodeInt16 error=%v want %v", err, ErrInvalidPacket)
	}
}

func TestDecodeValid120msPacketUsesBufferSizeAfterProtocolCheck(t *testing.T) {
	dec := newMonoTestDecoder(t)
	packet := append([]byte{GenerateTOC(16, false, 3), 0x30}, make([]byte, 48)...)

	_, err := dec.Decode(packet, make([]float32, 120))
	if err != ErrBufferTooSmall {
		t.Fatalf("Decode error=%v want %v", err, ErrBufferTooSmall)
	}
}

func TestDecoder_Decode_PLC(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// First decode a valid frame to set up state
	packet := minimalHybridTestPacket20ms()
	frameSize := 960

	pcmOut := make([]float32, frameSize)
	_, err = dec.Decode(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	// Now simulate packet loss by passing nil
	pcmPLC := make([]float32, frameSize)
	n, err := dec.Decode(nil, pcmPLC)
	if err != nil {
		t.Fatalf("Decode PLC error: %v", err)
	}

	if n != frameSize {
		t.Errorf("PLC returned %d samples, want %d", n, frameSize)
	}

	t.Logf("PLC produced %d samples", n)
}

func TestDecoder_DecodePLCUsesRequestedBufferFrameSize(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	packet := minimalHybridTestPacket20ms()
	if _, err := dec.Decode(packet, make([]float32, 960)); err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	for _, requested := range []int{960, 1920} {
		pcmPLC := make([]float32, requested)
		n, err := dec.Decode(nil, pcmPLC)
		if err != nil {
			t.Fatalf("Decode PLC request=%d: %v", requested, err)
		}
		if n != requested {
			t.Fatalf("PLC request=%d returned %d samples per channel", requested, n)
		}
	}
}

func TestDecoder_Reset(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	packet := minimalHybridTestPacket20ms()
	frameSize := 960

	// Decode a frame
	pcmOut := make([]float32, frameSize)
	_, err = dec.Decode(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	// Reset
	dec.Reset()

	// Decode again should work
	_, err = dec.Decode(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode after Reset error: %v", err)
	}
}

func TestDecoder_Stereo(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	packet := minimalHybridTestPacket20msStereo()
	frameSize := 960

	pcmOut := make([]float32, frameSize*2) // Stereo: 2 channels
	n, err := dec.Decode(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	if n != frameSize {
		t.Errorf("Decode returned %d samples per channel, want %d", n, frameSize)
	}
}

func TestDecoder_TOCParsing(t *testing.T) {
	// Test that the decoder correctly parses the TOC byte to determine frame size
	tests := []struct {
		name      string
		toc       byte
		frameSize int
	}{
		// Hybrid FB 20ms: config 15
		{"hybrid_fb_20ms", 0x78, 960},
		// Hybrid FB 10ms: config 14
		{"hybrid_fb_10ms", 0x70, 480},
		// SILK WB 20ms: config 9
		{"silk_wb_20ms", 0x48, 960},
		// CELT FB 20ms: config 31
		{"celt_fb_20ms", 0xF8, 960},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toc := ParseTOC(tt.toc)
			if toc.FrameSize != tt.frameSize {
				t.Errorf("TOC %02X: FrameSize = %d, want %d", tt.toc, toc.FrameSize, tt.frameSize)
			}
		})
	}
}

// TestDecode_ModeRouting verifies that packets are routed to the correct decoder
// based on their TOC mode field.
func TestDecode_ModeRouting(t *testing.T) {
	tests := []struct {
		name      string
		config    uint8 // TOC config (0-31)
		frameSize int   // Expected frame size at 48kHz
		mode      Mode  // Expected mode
	}{
		// SILK-only (configs 0-11)
		{"SILK NB 10ms", 0, 480, ModeSILK},
		{"SILK NB 20ms", 1, 960, ModeSILK},
		{"SILK NB 40ms", 2, 1920, ModeSILK},
		{"SILK NB 60ms", 3, 2880, ModeSILK},
		{"SILK MB 20ms", 5, 960, ModeSILK},
		{"SILK WB 20ms", 9, 960, ModeSILK},
		{"SILK WB 40ms", 10, 1920, ModeSILK},
		{"SILK WB 60ms", 11, 2880, ModeSILK},

		// Hybrid (configs 12-15)
		{"Hybrid SWB 10ms", 12, 480, ModeHybrid},
		{"Hybrid SWB 20ms", 13, 960, ModeHybrid},
		{"Hybrid FB 10ms", 14, 480, ModeHybrid},
		{"Hybrid FB 20ms", 15, 960, ModeHybrid},

		// CELT-only (configs 16-31)
		{"CELT NB 2.5ms", 16, 120, ModeCELT},
		{"CELT NB 5ms", 17, 240, ModeCELT},
		{"CELT NB 10ms", 18, 480, ModeCELT},
		{"CELT NB 20ms", 19, 960, ModeCELT},
		{"CELT FB 2.5ms", 28, 120, ModeCELT},
		{"CELT FB 5ms", 29, 240, ModeCELT},
		{"CELT FB 10ms", 30, 480, ModeCELT},
		{"CELT FB 20ms", 31, 960, ModeCELT},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Verify TOC parsing
			toc := ParseTOC(GenerateTOC(tt.config, false, 0))

			if toc.Mode != tt.mode {
				t.Errorf("Mode mismatch: got %v, want %v", toc.Mode, tt.mode)
			}
			if toc.FrameSize != tt.frameSize {
				t.Errorf("FrameSize mismatch: got %d, want %d", toc.FrameSize, tt.frameSize)
			}

			// Test decoder accepts the packet (may fail on decode but should not fail on routing)
			dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
			if err != nil {
				t.Fatalf("NewDecoder failed: %v", err)
			}

			// Create minimal valid packet (TOC + some data)
			packet := make([]byte, 100)
			packet[0] = GenerateTOC(tt.config, false, 0)
			// Fill with minimal valid data for range decoder
			for i := 1; i < len(packet); i++ {
				packet[i] = byte(i)
			}

			// Decode should not panic and should not return "hybrid: invalid frame size"
			pcm := make([]float32, tt.frameSize*2) // Extra buffer
			_, err = dec.Decode(packet, pcm)

			// For extended frame sizes, we expect decode to succeed (no routing error)
			// The decode may still fail for other reasons (invalid bitstream) but
			// should NOT fail with "hybrid: invalid frame size"
			if err != nil {
				errStr := err.Error()
				if strings.Contains(errStr, "hybrid: invalid frame size") {
					t.Errorf("Mode routing failed: SILK/CELT packet incorrectly routed to hybrid decoder: %v", err)
				}
				// Log other errors but don't fail - bitstream content may be invalid
				t.Logf("Decode error (non-routing): %v", err)
			}
		})
	}
}

// TestDecode_ExtendedFrameSizes verifies that extended frame sizes (CELT 2.5/5ms,
// SILK 40/60ms) are accepted without being rejected by the hybrid decoder.
func TestDecode_ExtendedFrameSizes(t *testing.T) {
	// Test that extended frame sizes don't trigger hybrid validation error
	extendedConfigs := []struct {
		name      string
		config    uint8
		frameSize int
	}{
		{"CELT 2.5ms", 28, 120}, // CELT FB 2.5ms
		{"CELT 5ms", 29, 240},   // CELT FB 5ms
		{"SILK 40ms", 10, 1920}, // SILK WB 40ms
		{"SILK 60ms", 11, 2880}, // SILK WB 60ms
	}

	for _, tt := range extendedConfigs {
		t.Run(tt.name, func(t *testing.T) {
			dec, _ := NewDecoder(DefaultDecoderConfig(48000, 1))

			packet := make([]byte, 100)
			packet[0] = GenerateTOC(tt.config, false, 0)
			for i := 1; i < len(packet); i++ {
				packet[i] = byte(i * 7) // Different pattern
			}

			pcm := make([]float32, tt.frameSize*2)
			_, err := dec.Decode(packet, pcm)

			// Critical: should NOT fail with hybrid frame size error
			if err != nil && strings.Contains(err.Error(), "hybrid: invalid frame size") {
				t.Errorf("Extended frame size incorrectly rejected as hybrid: %v", err)
			}
		})
	}
}

// TestDecode_PLC_ModeTracking verifies that PLC uses the last decoded mode,
// not defaulting to Hybrid mode.
func TestDecode_PLC_ModeTracking(t *testing.T) {
	dec, _ := NewDecoder(DefaultDecoderConfig(48000, 1))

	// First: decode a SILK packet to set mode
	silkPacket := make([]byte, 50)
	silkPacket[0] = GenerateTOC(9, false, 0) // SILK WB 20ms
	for i := 1; i < len(silkPacket); i++ {
		silkPacket[i] = byte(i)
	}

	pcm := make([]float32, 960*2)
	_, _ = dec.Decode(silkPacket, pcm)

	// PLC should use last mode (SILK)
	_, err := dec.Decode(nil, pcm)
	if err != nil && strings.Contains(err.Error(), "hybrid") {
		t.Errorf("PLC should use SILK mode, not hybrid: %v", err)
	}
}

func TestExtractFirstFramePayloadCode3VBROneFrameWithPadding(t *testing.T) {
	packet := append([]byte{0x43, 0xC1, 0x03}, []byte{0x4D, 0x66, 0xDD, 0x53, 0xE3}...)
	packet = append(packet, 0x00, 0x00, 0x00)

	firstFrameData, err := extractFirstFramePayload(packet, ParseTOC(packet[0]))
	if err != nil {
		t.Fatalf("extractFirstFramePayload error: %v", err)
	}

	want := []byte{0x4D, 0x66, 0xDD, 0x53, 0xE3}
	if len(firstFrameData) != len(want) {
		t.Fatalf("payload length=%d want=%d", len(firstFrameData), len(want))
	}
	for i := range want {
		if firstFrameData[i] != want[i] {
			t.Fatalf("payload[%d]=0x%02x want=0x%02x", i, firstFrameData[i], want[i])
		}
	}
}

func TestDecodeCode3VBROneFramePaddingRegression(t *testing.T) {
	packet := mustDecodeHex(t, "43c1064d66dd53e3b92d85ca64ec672fb6384f7b2dd2cb3164f5e17ae7b97e7a7e69544afe2e8880")

	cfg := DefaultDecoderConfig(48000, 1)
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	pcm := make([]float32, cfg.MaxPacketSamples)
	n, err := dec.Decode(packet, pcm)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if n <= 0 || n > cfg.MaxPacketSamples {
		t.Fatalf("Decode samples=%d outside (0,%d]", n, cfg.MaxPacketSamples)
	}
}
