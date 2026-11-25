package multistream

import (
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/types"
	"math"
	"testing"
)

func TestDecodeMalformedNeverPanics(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"single-toc-byte", []byte{0x00}},
		{"two-bytes-code0", []byte{0x00, 0x10}},
		{"code1-odd-tail", []byte{0x01, 0xaa, 0xbb, 0xcc}},
		{"code2-truncated-len", []byte{0x02, 0xff}},
		{"code3-no-count", []byte{0x03}},
		{"code3-zero-frames", []byte{0x03, 0x00}},
		{"code3-toomany-frames", []byte{0x03, 0x80 | 49}},
		{"code3-padding-runaway", []byte{0x03, 0x40 | 1, 0xff, 0xff, 0xff, 0xff}},
		{"code3-vbr-truncated", []byte{0x03, 0x80 | 3, 0xff}},
		{"all-ones", []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}},
		{"self-delim-len-overflow", []byte{0x00, 0xff, 0xff, 0x00, 0x10}},
	}

	for _, channels := range []int{1, 2, 6} {
		dec, err := NewDecoderDefault(48000, channels)
		if err != nil {
			t.Fatalf("NewDecoderDefault(%d): %v", channels, err)
		}
		for _, tc := range cases {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("ch%d/%s: PANIC: %v", channels, tc.name, r)
					}
				}()
				// Non-empty malformed packets must error (empty == PLC, handled
				// separately below); we only require no-panic here.
				_, _ = dec.DecodeToFloat32(tc.data, robustFrameSize)
				_, _ = dec.DecodeToInt16(tc.data, robustFrameSize)
			}()
		}
	}
}

// TestDecodeEmptyIsPLC verifies that a nil OR zero-length packet performs PLC
// (libopus do_plc semantics) and returns exactly frameSize*channels samples,
// not an error.
func TestDecodeEmptyIsPLC(t *testing.T) {
	for _, channels := range []int{1, 2, 6, 8} {
		dec, err := NewDecoderDefault(48000, channels)
		if err != nil {
			t.Fatalf("NewDecoderDefault(%d): %v", channels, err)
		}
		for _, data := range [][]byte{nil, {}} {
			out, err := dec.DecodeToFloat32(data, robustFrameSize)
			if err != nil {
				t.Fatalf("ch%d: empty-packet PLC returned error: %v", channels, err)
			}
			if len(out) != robustFrameSize*channels {
				t.Fatalf("ch%d: PLC out len %d, want %d", channels, len(out), robustFrameSize*channels)
			}
			out16, err := dec.DecodeToInt16(data, robustFrameSize)
			if err != nil {
				t.Fatalf("ch%d: empty-packet int16 PLC returned error: %v", channels, err)
			}
			if len(out16) != robustFrameSize*channels {
				t.Fatalf("ch%d: int16 PLC out len %d, want %d", channels, len(out16), robustFrameSize*channels)
			}
		}
	}
}

// TestNewDecoderRejectsBadLayouts pins the constructor validation contract: bad
// channel/stream/coupled counts and malformed mapping tables must each return
// the documented error rather than constructing an unusable decoder.
func TestNewDecoderRejectsBadLayouts(t *testing.T) {
	cases := []struct {
		name                       string
		channels, streams, coupled int
		mapping                    []byte
		wantErr                    error
	}{
		{"channels-zero", 0, 1, 0, []byte{}, ErrInvalidChannels},
		{"channels-over-255", 256, 1, 0, make([]byte, 256), ErrInvalidChannels},
		{"streams-zero", 2, 0, 0, []byte{0, 1}, ErrInvalidStreams},
		{"streams-over-255", 2, 256, 0, []byte{0, 1}, ErrInvalidStreams},
		{"coupled-negative", 2, 1, -1, []byte{0, 1}, ErrInvalidCoupledStreams},
		{"coupled-over-streams", 2, 1, 2, []byte{0, 1}, ErrInvalidCoupledStreams},
		{"streams-plus-coupled-over-255", 4, 200, 100, make([]byte, 4), ErrTooManyChannels},
		{"mapping-too-short", 2, 1, 1, []byte{0}, ErrInvalidMapping},
		{"mapping-too-long", 2, 1, 1, []byte{0, 1, 2}, ErrInvalidMapping},
		{"mapping-value-out-of-range", 2, 1, 0, []byte{0, 5}, ErrInvalidMapping},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewDecoder(48000, tc.channels, tc.streams, tc.coupled, tc.mapping)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("got error %v, want wrapping %v", err, tc.wantErr)
			}
		})
	}
}

// TestNewDecoderAcceptsValidEdgeLayouts confirms the boundary layouts the
// constructor must accept: a 255-value silent channel, the maximal-coupling
// case, and an all-silent mapping.
func TestNewDecoderAcceptsValidEdgeLayouts(t *testing.T) {
	cases := []struct {
		name                       string
		channels, streams, coupled int
		mapping                    []byte
	}{
		{"silent-channel-255", 3, 1, 1, []byte{0, 1, 255}},
		{"all-silent", 2, 1, 0, []byte{255, 255}},
		{"max-mapping-value", 3, 2, 1, []byte{0, 1, 2}}, // 2 == 2*M = first mono stream
		{"mono", 1, 1, 0, []byte{0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dec, err := NewDecoder(48000, tc.channels, tc.streams, tc.coupled, tc.mapping)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// A silent/edge layout must still PLC-conceal an empty packet to the
			// right shape without touching a non-existent stream.
			out, err := dec.DecodeToFloat32(nil, robustFrameSize)
			if err != nil {
				t.Fatalf("PLC decode error: %v", err)
			}
			if len(out) != robustFrameSize*tc.channels {
				t.Fatalf("PLC out len %d, want %d", len(out), robustFrameSize*tc.channels)
			}
		})
	}
}

// TestConstructorCopiesMapping verifies the documented buffer-ownership
// contract: NewDecoder copies the mapping so a later caller mutation cannot
// corrupt the decoder's routing.
func TestConstructorCopiesMapping(t *testing.T) {
	mapping := []byte{0, 1, 255}
	dec, err := NewDecoder(48000, 3, 1, 1, mapping)
	if err != nil {
		t.Fatal(err)
	}
	// Mutate the caller's slice to an out-of-range value after construction.
	mapping[2] = 200
	// The decoder must still conceal cleanly (its internal copy is unchanged).
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("PANIC after caller mutated mapping: %v", r)
		}
	}()
	if _, err := dec.DecodeToFloat32(nil, robustFrameSize); err != nil {
		t.Fatalf("decode error after mapping mutation: %v", err)
	}
}

// TestParseMultistreamPacketBadStreamCount checks the framing parser rejects a
// zero/negative stream count and a truncated multi-stream packet without panic.
func TestParseMultistreamPacketBadStreamCount(t *testing.T) {
	if _, err := parseMultistreamPacket([]byte{0x00}, 0); !errors.Is(err, ErrInvalidStreamCount) {
		t.Fatalf("zero streams: got %v, want ErrInvalidStreamCount", err)
	}
	if _, err := parseMultistreamPacket([]byte{0x00}, -3); !errors.Is(err, ErrInvalidStreamCount) {
		t.Fatalf("negative streams: got %v, want ErrInvalidStreamCount", err)
	}
	// Two streams but only enough bytes for part of the first self-delimited one.
	if _, err := parseMultistreamPacket([]byte{0x00, 0xff}, 2); err == nil {
		t.Fatalf("truncated 2-stream packet: expected error, got nil")
	}
}

// TestSetProjectionDemixingMatrixValidation pins the demixing-matrix validation:
// a wrong-length matrix and a non-identity mapping must be rejected, an empty
// matrix clears projection, and a correctly-sized matrix is accepted.
func TestSetProjectionDemixingMatrixValidation(t *testing.T) {
	channels := 4
	enc, err := NewProjectionEncoder(48000, channels)
	if err != nil {
		t.Fatalf("NewProjectionEncoder: %v", err)
	}
	streams, coupled := enc.Streams(), enc.CoupledStreams()
	good := enc.GetDemixingMatrix()

	// Correct length on a trivial-mapping decoder: accepted.
	dec, err := NewProjectionDecoder(48000, channels, streams, coupled, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := dec.SetProjectionDemixingMatrix(good); err != nil {
		t.Fatalf("valid matrix rejected: %v", err)
	}
	// Empty clears it.
	if err := dec.SetProjectionDemixingMatrix(nil); err != nil {
		t.Fatalf("clearing matrix errored: %v", err)
	}

	// Wrong length: rejected.
	if err := dec.SetProjectionDemixingMatrix(good[:len(good)-2]); !errors.Is(err, ErrInvalidProjectionMatrix) {
		t.Fatalf("short matrix: got %v, want ErrInvalidProjectionMatrix", err)
	}
	if err := dec.SetProjectionDemixingMatrix(append(append([]byte(nil), good...), 0, 0)); !errors.Is(err, ErrInvalidProjectionMatrix) {
		t.Fatalf("long matrix: got %v, want ErrInvalidProjectionMatrix", err)
	}

	// Non-identity mapping: a projection matrix requires trivial mapping.
	badMap := make([]byte, channels)
	for i := range badMap {
		badMap[i] = byte(channels - 1 - i) // reversed, non-identity
	}
	ndec, err := NewDecoder(48000, channels, streams, coupled, badMap)
	if err != nil {
		t.Fatal(err)
	}
	if err := ndec.SetProjectionDemixingMatrix(good); !errors.Is(err, ErrInvalidProjectionMatrix) {
		t.Fatalf("non-identity mapping: got %v, want ErrInvalidProjectionMatrix", err)
	}
}

// TestDurationMismatchRejected builds a 2-stream multistream packet whose two
// elementary streams have different frame durations and asserts the decoder
// rejects it with ErrDurationMismatch rather than mis-decoding.
func TestDurationMismatchRejected(t *testing.T) {
	// Two CELT TOC bytes with different frame sizes: config 16 (2.5 ms) vs
	// config 19 (20 ms), both mono code 0 with a 1-byte payload. The first
	// stream is reframed self-delimited; the second uses standard framing.
	stream0 := []byte{16 << 3, 0x00}             // 2.5 ms
	stream1 := []byte{19 << 3, 0x00}             // 20 ms
	sd0, err := makeSelfDelimitedPacket(stream0) // first stream is self-delimited
	if err != nil {
		t.Fatalf("self-delimit stream0: %v", err)
	}
	packet := append(append([]byte(nil), sd0...), stream1...)

	dec, err := NewDecoder(48000, 2, 2, 0, []byte{0, 1}) // 2 mono streams
	if err != nil {
		t.Fatal(err)
	}
	_, derr := dec.DecodeToFloat32(packet, robustFrameSize)
	if !errors.Is(derr, ErrDurationMismatch) {
		t.Fatalf("got %v, want ErrDurationMismatch", derr)
	}
}

func TestMultistreamPLCFrameSizeValidationMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate = 48000
		channels   = 2
		frameSize  = 960
		badPLCSize = 121 // 2.5 ms is 120 samples at 48 kHz.
		shortCap   = 961 // Packet duration fits although this is not a PLC quantum.
	)
	pcm := make([]float32, frameSize*channels)
	for sample := range frameSize {
		phase := 2 * math.Pi * 440 * float64(sample) / sampleRate
		pcm[2*sample] = float32(0.35 * math.Sin(phase))
		pcm[2*sample+1] = float32(0.29 * math.Sin(phase+0.2))
	}
	encoded, err := libopustest.ProbeEncodeDiff(libopustest.EncodeDiffParams{
		SampleRate: sampleRate, Channels: channels,
		Application:   libopustest.EncodeDiffApplicationAudio,
		ForceMode:     libopustest.EncodeDiffForceModeCELTOnly,
		Bandwidth:     libopustest.EncodeDiffBandwidthFullband,
		Bitrate:       64000,
		Complexity:    10,
		Signal:        libopustest.EncodeDiffSignalMusic,
		VBR:           true,
		ForceChannels: channels,
		FrameSize:     frameSize,
		FrameCount:    1,
		PCM:           pcm,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "PLC frame-size C packet", err)
	}
	if len(encoded) != 1 {
		t.Fatalf("selected C encoder returned %d records, want 1", len(encoded))
	}
	if encoded[0].Ret <= 0 {
		t.Fatalf("selected C encoder packet status=%d, want positive", encoded[0].Ret)
	}
	packet := encoded[0].Packet

	cases := []libopustest.DecodeDiffCase{
		{Packet: packet, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: frameSize},
		{Format: libopustest.DecodeDiffFormatFloat32, FrameSize: badPLCSize},
		{Format: libopustest.DecodeDiffFormatInt16, FrameSize: badPLCSize},
		{Format: libopustest.DecodeDiffFormatInt24, FrameSize: badPLCSize},
		{Packet: packet, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: shortCap},
	}
	want, err := libopustest.ProbeDecodeSequence(sampleRate, channels, cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "PLC frame-size C sequence", err)
	}
	control, err := libopustest.ProbeDecodeSequence(sampleRate, channels, []libopustest.DecodeDiffCase{
		{Packet: packet, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: frameSize},
		{Packet: packet, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: shortCap},
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "PLC frame-size C control", err)
	}
	if len(want) != len(cases) || len(control) != 2 {
		t.Fatalf("C sequence lengths got=%d control=%d", len(want), len(control))
	}
	if want[0].Code != frameSize || want[4].Code != frameSize || control[1].Code != frameSize {
		t.Fatalf("C valid decode statuses got=%d/%d control=%d", want[0].Code, want[4].Code, control[1].Code)
	}
	for i := 1; i <= 3; i++ {
		if want[i].Code != -1 {
			t.Fatalf("C invalid PLC format %d status=%d, want OPUS_BAD_ARG (-1)", cases[i].Format, want[i].Code)
		}
		if want[i].FinalRange != want[0].FinalRange {
			t.Fatalf("C invalid PLC call %d changed range %08x to %08x", i, want[0].FinalRange, want[i].FinalRange)
		}
	}
	if want[4].Code != control[1].Code || want[4].FinalRange != control[1].FinalRange {
		t.Fatal("C invalid PLC calls changed state before the recovery packet")
	}
	assertMSDecodeFloatFrameAware(t, want[4].Float32(), control[1].Float32(), channels, frameSize, nil, "C PLC recovery")

	dec, err := NewDecoderDefault(sampleRate, channels)
	if err != nil {
		t.Fatalf("NewDecoderDefault: %v", err)
	}
	if _, err := dec.DecodeToFloat32(packet, frameSize); err != nil {
		t.Fatalf("prime DecodeToFloat32: %v", err)
	}
	if got := dec.FinalRange(); got != want[0].FinalRange {
		t.Fatalf("prime range Go=%08x C=%08x", got, want[0].FinalRange)
	}

	output := make([]float32, badPLCSize*channels)
	for i := range output {
		output[i] = float32(i) + 0.25
	}
	sentinel := append([]float32(nil), output...)
	if n, err := dec.DecodeIntoFloat32(nil, output, badPLCSize); n != 0 || !errors.Is(err, ErrInvalidPacket) {
		t.Fatalf("DecodeIntoFloat32 invalid PLC=(%d,%v), want (0,ErrInvalidPacket)", n, err)
	}
	for i := range output {
		if output[i] != sentinel[i] {
			t.Fatalf("failed DecodeIntoFloat32 wrote output[%d]=%g, want %g", i, output[i], sentinel[i])
		}
	}
	if _, err := dec.DecodeToFloat32(nil, badPLCSize); !errors.Is(err, ErrInvalidPacket) {
		t.Fatalf("DecodeToFloat32 invalid PLC error=%v, want ErrInvalidPacket", err)
	}
	if _, err := dec.DecodeToInt16(nil, badPLCSize); !errors.Is(err, ErrInvalidPacket) {
		t.Fatalf("DecodeToInt16 invalid PLC error=%v, want ErrInvalidPacket", err)
	}
	if _, err := dec.DecodeToInt24(nil, badPLCSize); !errors.Is(err, ErrInvalidPacket) {
		t.Fatalf("DecodeToInt24 invalid PLC error=%v, want ErrInvalidPacket", err)
	}
	if got := dec.FinalRange(); got != want[0].FinalRange {
		t.Fatalf("invalid PLC calls changed range %08x to %08x", want[0].FinalRange, got)
	}

	partial := make([]float32, shortCap*channels)
	for i := range partial {
		partial[i] = float32(i) + 0.5
	}
	partialSentinel := append([]float32(nil), partial...)
	if n, err := dec.DecodeIntoFloat32(packet, partial, shortCap); err != nil || n != frameSize {
		t.Fatalf("DecodeIntoFloat32 packet fits short capacity=(%d,%v), want (%d,nil)", n, err, frameSize)
	}
	assertMSDecodeFloatFrameAware(t, partial[:frameSize*channels], want[4].Float32(), channels, frameSize, nil, "Go PLC recovery")
	for i := frameSize * channels; i < len(partial); i++ {
		if partial[i] != partialSentinel[i] {
			t.Fatalf("DecodeIntoFloat32 wrote beyond packet duration at output[%d]", i)
		}
	}
	if got := dec.FinalRange(); got != want[4].FinalRange {
		t.Fatalf("recovery range Go=%08x C=%08x", got, want[4].FinalRange)
	}
}

const robustFrameSize = 960

// robustDecodeNoPanic decodes data through dec via both the float32 and int16
// paths inside a panic recover, asserting crash-safety and the output-shape
// invariants. A nil dec (constructor rejected the layout) is a no-op.
func robustDecodeNoPanic(t *testing.T, label string, dec *Decoder, channels int, data []byte) {
	t.Helper()
	if dec == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("PANIC %s (len=%d): %v\n% x", label, len(data), r, data)
		}
	}()

	out32, err32 := dec.DecodeToFloat32(data, robustFrameSize)
	if err32 == nil {
		if len(out32)%channels != 0 {
			t.Fatalf("%s: float out len %d not a multiple of channels %d", label, len(out32), channels)
		}
		if n := len(out32) / channels; n < 0 || n > robustFrameSize {
			t.Fatalf("%s: float decoded %d samples outside [0,%d]", label, n, robustFrameSize)
		}
	}

	out16, err16 := dec.DecodeToInt16(data, robustFrameSize)
	if err16 == nil {
		if len(out16)%channels != 0 {
			t.Fatalf("%s: int16 out len %d not a multiple of channels %d", label, len(out16), channels)
		}
		if n := len(out16) / channels; n < 0 || n > robustFrameSize {
			t.Fatalf("%s: int16 decoded %d samples outside [0,%d]", label, n, robustFrameSize)
		}
	}

	// Accept/reject and decoded-sample-count must agree across the two output
	// formats on the SAME fresh-decoder state (both first calls). A divergence
	// would mean one path validates the packet differently from the other.
	if (err32 == nil) != (err16 == nil) {
		t.Fatalf("%s: float/int16 accept mismatch (float err=%v, int16 err=%v)\n% x", label, err32, err16, data)
	}
	if err32 == nil && err16 == nil && len(out32) != len(out16) {
		t.Fatalf("%s: float/int16 sample-count mismatch (%d vs %d)\n% x", label, len(out32), len(out16), data)
	}
}

// surroundSeedPacket encodes one valid family-1 surround packet for the given
// channel count via the gopus encoder (no oracle), for use as a fuzz seed.
func surroundSeedPacket(tb testing.TB, channels int) []byte {
	tb.Helper()
	enc, err := NewEncoderDefault(48000, channels)
	if err != nil {
		tb.Fatalf("NewEncoderDefault(%d): %v", channels, err)
	}
	enc.SetBitrate(channels * 48000)
	pcm := make([]float32, robustFrameSize*channels)
	for i := range pcm {
		pcm[i] = float32((i*7+channels)%193-96) / 110
	}
	pkt, err := encodePacket(enc, pcm, robustFrameSize)
	if err != nil {
		tb.Fatalf("surround encode(%d ch): %v", channels, err)
	}
	return pkt
}

// projectionSeed bundles a valid family-3 packet with the layout it needs.
type projectionSeed struct {
	channels int
	streams  int
	coupled  int
	demixing []byte
	packet   []byte
}

// projectionSeedPacket encodes one valid family-3 projection packet for the
// given (ambisonics) channel count via the gopus projection encoder (no oracle).
func projectionSeedPacket(tb testing.TB, channels int) projectionSeed {
	tb.Helper()
	enc, err := NewProjectionEncoder(48000, channels)
	if err != nil {
		tb.Fatalf("NewProjectionEncoder(%d): %v", channels, err)
	}
	enc.SetBitrate(channels * 16000)
	dm := enc.GetDemixingMatrix()
	pcm := make([]float32, robustFrameSize*channels)
	for i := range pcm {
		pcm[i] = float32((i*5+channels)%107-53) / 90
	}
	pkt, err := encodePacket(enc, pcm, robustFrameSize)
	if err != nil {
		tb.Fatalf("projection encode(%d ch): %v", channels, err)
	}
	return projectionSeed{
		channels: channels,
		streams:  enc.Streams(),
		coupled:  enc.CoupledStreams(),
		demixing: dm,
		packet:   pkt,
	}
}

// surroundLayouts are the family-1 channel counts (1..8) selected by the fuzzer.
var surroundLayouts = []int{1, 2, 3, 4, 5, 6, 7, 8}

// FuzzMultistreamSurroundDecode fuzzes the family-1 surround decode path: the
// fuzzer supplies an arbitrary packet plus a selector byte that picks one of the
// 1..8-channel default layouts. The decoder must never panic and must honour the
// output-shape invariants for any bytes.
func FuzzMultistreamSurroundDecode(f *testing.F) {
	// Seed with valid packets for every supported channel count, plus the
	// degenerate empty/short packets that drive the PLC and rejection paths.
	for sel, ch := range surroundLayouts {
		f.Add(surroundSeedPacket(f, ch), uint8(sel))
		f.Add([]byte(nil), uint8(sel))
		f.Add([]byte{0x00}, uint8(sel))
		f.Add([]byte{0xff, 0xff, 0xff}, uint8(sel))
	}

	f.Fuzz(func(t *testing.T, data []byte, sel uint8) {
		channels := surroundLayouts[int(sel)%len(surroundLayouts)]
		dec, err := NewDecoderDefault(48000, channels)
		if err != nil {
			t.Fatalf("NewDecoderDefault(%d): %v", channels, err)
		}
		robustDecodeNoPanic(t, fmt.Sprintf("surround/ch%d", channels), dec, channels, data)
	})
}

// FuzzMultistreamDiscreteDecode fuzzes arbitrary family-255 (discrete) layouts:
// the leading fuzz bytes are interpreted as channels/streams/coupled and a
// per-output-channel mapping table, so the fuzzer can drive every NewDecoder
// validation branch (bad stream/coupled counts, oversized mapping values, silent
// channels) as well as the decode of whatever remains as the packet. The
// constructor must reject every invalid layout cleanly, and any layout it
// accepts must decode arbitrary trailing bytes without panicking.
func FuzzMultistreamDiscreteDecode(f *testing.F) {
	// Seed: a couple of hand-built layout headers + a real surround packet body,
	// and a maximal-fanout layout that stresses the mapping scatter.
	f.Add([]byte{6, 4, 2, 0, 4, 1, 2, 3, 5}, surroundSeedPacket(f, 6))
	f.Add([]byte{2, 1, 1, 0, 1}, surroundSeedPacket(f, 2))
	f.Add([]byte{1, 1, 0, 0}, surroundSeedPacket(f, 1))
	f.Add([]byte{8, 5, 3, 0, 6, 1, 2, 3, 4, 5, 7}, []byte{})
	f.Add([]byte{4, 2, 2, 255, 255, 0, 1}, []byte{0xfc})

	f.Fuzz(func(t *testing.T, header, body []byte) {
		// Need at least channels, streams, coupled.
		if len(header) < 3 {
			return
		}
		channels := int(header[0])
		streams := int(header[1])
		coupled := int(header[2])
		// Clamp the fuzzed layout to the parameter ranges NewDecoder accepts so
		// the constructor exercises its validation rather than allocating an
		// absurd number of stream decoders (255 channels * full Opus state is
		// pointlessly expensive for a per-input fuzz iteration).
		if channels < 1 || channels > 8 || streams < 1 || streams > 8 {
			return
		}
		rest := header[3:]
		if len(rest) < channels {
			return
		}
		mapping := append([]byte(nil), rest[:channels]...)
		// The remaining header bytes after the mapping table join the body as
		// the packet payload, so a single fuzz input can carry both layout and
		// packet.
		packet := append(append([]byte(nil), rest[channels:]...), body...)

		dec, err := NewDecoder(48000, channels, streams, coupled, mapping)
		if err != nil {
			return // invalid layout rejected cleanly — the contract we want.
		}
		label := fmt.Sprintf("discrete/ch%d/st%d/cp%d/map%v", channels, streams, coupled, mapping)
		robustDecodeNoPanic(t, label, dec, channels, packet)
	})
}

// projectionLayouts are the projection (family-3) channel counts seeded.
var projectionLayouts = []int{4, 9, 16}

// FuzzProjectionDecode fuzzes the family-3 projection decode path, including the
// demixing-matrix matmul. The fuzzer supplies an arbitrary packet plus a
// selector picking one of the seeded ambisonics orders (FOA/SOA/TOA), each with
// its real demixing matrix. A malformed packet must not drive the demix matmul
// out of bounds, and the empty/short paths must conceal cleanly.
func FuzzProjectionDecode(f *testing.F) {
	seeds := make([]projectionSeed, 0, len(projectionLayouts))
	for sel, ch := range projectionLayouts {
		s := projectionSeedPacket(f, ch)
		seeds = append(seeds, s)
		f.Add(s.packet, uint8(sel))
		f.Add([]byte(nil), uint8(sel))
		f.Add([]byte{0x00}, uint8(sel))
	}

	f.Fuzz(func(t *testing.T, data []byte, sel uint8) {
		s := seeds[int(sel)%len(seeds)]
		dec, err := NewProjectionDecoder(48000, s.channels, s.streams, s.coupled, s.demixing)
		if err != nil {
			t.Fatalf("NewProjectionDecoder(ch%d): %v", s.channels, err)
		}
		robustDecodeNoPanic(t, fmt.Sprintf("projection/ch%d", s.channels), dec, s.channels, data)
	})
}

// FuzzProjectionDemixingMatrix fuzzes Decoder.SetProjectionDemixingMatrix with
// arbitrary matrix bytes on a real projection layout, then decodes a valid
// packet. The matrix-length / identity-mapping validation must reject every
// malformed matrix, and any accepted matrix must drive the demix matmul without
// reading out of bounds.
func FuzzProjectionDemixingMatrix(f *testing.F) {
	s := projectionSeedPacket(f, 4)
	f.Add(s.demixing)
	f.Add([]byte(nil))
	f.Add(make([]byte, len(s.demixing)-1)) // wrong length -> reject
	f.Add(make([]byte, len(s.demixing)+1)) // wrong length -> reject

	f.Fuzz(func(t *testing.T, matrix []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC SetProjectionDemixingMatrix (len=%d): %v\n% x", len(matrix), r, matrix)
			}
		}()
		// Build a fresh trivial-mapping projection layout (identity mapping is
		// required for projection; NewProjectionDecoder with a nil matrix gives
		// exactly that).
		dec, err := NewProjectionDecoder(48000, s.channels, s.streams, s.coupled, nil)
		if err != nil {
			t.Fatalf("NewProjectionDecoder: %v", err)
		}
		if err := dec.SetProjectionDemixingMatrix(matrix); err != nil {
			return // malformed matrix rejected cleanly.
		}
		robustDecodeNoPanic(t, "demix-matrix", dec, s.channels, s.packet)
	})
}

// FuzzMultistreamPacketParse fuzzes the low-level cross-stream framing helpers
// (parseMultistreamPacket / PacketDuration / PacketDurationAtRate) directly with
// arbitrary bytes and a fuzzed stream count, independent of any decoder state.
// These parse attacker-controlled self-delimited sub-packet lengths and must
// never panic or over-read.
func FuzzMultistreamPacketParse(f *testing.F) {
	f.Add(surroundSeedPacket(f, 6), uint8(4)) // 5.1 -> 4 streams
	f.Add(surroundSeedPacket(f, 2), uint8(1))
	f.Add([]byte(nil), uint8(1))
	f.Add([]byte{0x00}, uint8(0))
	f.Add([]byte{0xff, 0xff, 0xff, 0xff}, uint8(3))

	f.Fuzz(func(t *testing.T, data []byte, streamsByte uint8) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC packet-parse (streams=%d len=%d): %v\n% x", streamsByte, len(data), r, data)
			}
		}()
		// Cap the stream count: each parsed stream is a slice header, so a huge
		// count just bloats the result without exercising new logic.
		streams := int(streamsByte%16) + 1

		packets, err := parseMultistreamPacket(data, streams)
		if err == nil {
			if len(packets) != streams {
				t.Fatalf("parse returned %d packets for %d streams\n% x", len(packets), streams, data)
			}
			// PacketDuration re-parses + validates cross-stream timing; it must
			// likewise never panic on whatever framing parse accepted.
			_, _ = PacketDuration(data, streams)
			_, _ = PacketDurationAtRate(data, streams, 48000)
		}

		// Stream-count zero must always be rejected, never panic.
		if _, err := parseMultistreamPacket(data, 0); err == nil {
			t.Fatalf("parse accepted zero streams\n% x", data)
		}
	})
}

func TestCELTActualRotationPacketsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name   string
		packet string
	}{
		{
			name:   "n48_k5_b4",
			packet: "f07f137d7ff28e19b21aca710a241a44818a6b51b98c23211c3682a673bad7cb89e030fd8c3a6464",
		},
		{
			name:   "n18_k2_b2",
			packet: "f07eadd49cb81c906978f65c278eef6446fef8303c98637011f8d4002f741650983d3f37b2db3b67d1458444635d3fdcd459182d26c9d4b48ade0c41e5cffc8a59bc8af875ada322090eb9caae135e5812c8ade61192a8977454b9d43efff1b66bb6e352be3ea21757e89fed0b149e0e75b596d636176c776a1c64ed3b",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packet, err := hex.DecodeString(tc.packet)
			if err != nil {
				t.Fatal(err)
			}
			if mode := streamModeOfPacket(packet); mode != streamModeCELT {
				t.Fatalf("packet mode=%d want CELT", mode)
			}
			const frameSize = 480
			want := decodeTransitionSequenceWithLibopus(t, 48000, 1, 0, frameSize, []transitionDecodeStep{{packet: packet, frameSize: frameSize}})
			if len(want) != 1 {
				t.Fatalf("C step count=%d want 1", len(want))
			}
			if want[0].samples != frameSize {
				t.Fatalf("C sample count=%d want %d", want[0].samples, frameSize)
			}
			dec := newStreamDecoder(48000, 1)
			got, err := dec.Decode(packet, frameSize)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != frameSize || dec.FinalRange() != want[0].finalRange {
				t.Fatalf("Go length/range=(%d,%08x), C=(%d,%08x)", len(got), dec.FinalRange(), want[0].samples, want[0].finalRange)
			}
			assertTransitionStagePCMExact(t, got, want[0].pcm, tc.name)
		})
	}
}

func TestMultistreamModeTransitionsWarmZeroAllocs(t *testing.T) {
	libopustest.RequireOracle(t)
	spec, ref := encodeTransitionGainRegressionInput(t)
	_, _, previous, next := firstTransitionStreamPackets(t, ref.packets)
	if previous.mode != streamModeCELT || next.mode != streamModeHybrid {
		t.Fatalf("transition modes=%d→%d, want CELT→Hybrid", previous.mode, next.mode)
	}
	dec, err := NewDecoder(transitionStageSampleRate, spec.channels, ref.streams, ref.coupledStreams, ref.mapping)
	if err != nil {
		t.Fatal(err)
	}
	if err := dec.SetGain(transitionStageGainQ8); err != nil {
		t.Fatal(err)
	}
	output := make([]float32, spec.frameSize*spec.channels)
	decode := func() {
		for _, packet := range ref.packets {
			if n, err := dec.DecodeIntoFloat32(packet, output, spec.frameSize); err != nil || n != spec.frameSize {
				t.Fatalf("DecodeIntoFloat32=%d, %v", n, err)
			}
		}
	}
	decode()
	decode()
	if allocs := testing.AllocsPerRun(20, decode); allocs != 0 {
		t.Fatalf("warm mode-transition allocations=%g, want 0", allocs)
	}
}

var (
	transitionSequenceRefHelper       libopustest.HelperCache
	publicTransitionSequenceRefHelper libopustest.HelperCache
)

type transitionDecodeStep struct {
	packet    []byte
	frameSize int
}

type transitionDecodeResult struct {
	pcm        []float32
	samples    int
	finalRange uint32
}

// decodeTransitionSequenceWithLibopus uses the matched C decoder's v8 transport:
// each step has its own frame capacity, returned sample count, final range, and
// PCM span. The packet sequence preserves decoder history across transitions.
func decodeTransitionSequenceWithLibopus(t *testing.T, sampleRate, channels, gainQ8, maxFrameSize int, steps []transitionDecodeStep) []transitionDecodeResult {
	return decodeTransitionSequenceWithBuilder(
		t, &transitionSequenceRefHelper, buildMultistreamFloatShadowReferenceHelper,
		"single-stream float-shadow transition sequence reference", "gopus_transition_sequence_float_shadow_ref",
		sampleRate, channels, gainQ8, maxFrameSize, steps,
	)
}

func decodePublicTransitionSequenceWithLibopus(t *testing.T, sampleRate, channels, gainQ8, maxFrameSize int, steps []transitionDecodeStep) []transitionDecodeResult {
	return decodeTransitionSequenceWithBuilder(
		t, &publicTransitionSequenceRefHelper, buildMultistreamReferenceHelper,
		"single-stream public transition sequence reference", "gopus_transition_sequence_public_ref",
		sampleRate, channels, gainQ8, maxFrameSize, steps,
	)
}

func decodeTransitionSequenceWithBuilder(
	t *testing.T,
	helperCache *libopustest.HelperCache,
	buildHelper func(libopustest.CHelperConfig) (string, error),
	label, outputBase string,
	sampleRate, channels, gainQ8, maxFrameSize int,
	steps []transitionDecodeStep,
) []transitionDecodeResult {
	t.Helper()
	path, err := helperCache.Path(func() (string, error) {
		return buildHelper(libopustest.CHelperConfig{
			Label:      label,
			OutputBase: outputBase,
			SourceFile: "libopus_refdecode_single.c",
			CFlags:     []string{"-O3", "-DNDEBUG"},
			Libs:       []string{"-lm"},
		})
	})
	if err != nil {
		libopustest.HelperUnavailable(t, label, err)
	}
	payload := libopustest.NewOraclePayloadVersion(
		"GOSI", 8, 0, uint32(sampleRate), uint32(int32(gainQ8)),
		uint32(channels), uint32(maxFrameSize), uint32(len(steps)),
	)
	for i, step := range steps {
		if step.frameSize <= 0 || step.frameSize > maxFrameSize {
			t.Fatalf("step %d invalid frame capacity %d", i, step.frameSize)
		}
		payload.U32(0) // decode_fec
		payload.U32(uint32(step.frameSize))
		payload.U32(uint32(len(step.packet)))
		payload.Raw(step.packet)
	}
	reader, err := libopustest.RunOracleVersion(path, payload.Bytes(), label, "GOSO", 3)
	if err != nil {
		libopustest.HelperUnavailable(t, label, err)
	}
	pcmCount := reader.Count(-1)
	if pcmCount < 0 || pcmCount > len(steps)*maxFrameSize*channels {
		t.Fatalf("C PCM count %d exceeds step capacity %d", pcmCount, len(steps)*maxFrameSize*channels)
	}
	if reader.Remaining() != pcmCount*4+4+len(steps)*16 {
		t.Fatalf("C v8 output size=%d want %d", reader.Remaining(), pcmCount*4+4+len(steps)*16)
	}
	pcm := make([]float32, pcmCount)
	for i := range pcm {
		pcm[i] = reader.Float32()
	}
	if count := reader.Count(len(steps)); count != len(steps) {
		t.Fatalf("C step count=%d want %d", count, len(steps))
	}
	results := make([]transitionDecodeResult, len(steps))
	nextOffset := 0
	for i := range steps {
		status := reader.I32()
		samples := reader.Count(-1)
		finalRange := reader.U32()
		offset := reader.Count(-1)
		if status != 0 || samples < 0 || samples > steps[i].frameSize || offset != nextOffset || samples > (pcmCount-offset)/channels {
			t.Fatalf("C step %d record=(status=%d samples=%d range=%08x offset=%d), next offset=%d PCM count=%d",
				i, status, samples, finalRange, offset, nextOffset, pcmCount)
		}
		end := offset + samples*channels
		results[i] = transitionDecodeResult{pcm: pcm[offset:end], samples: samples, finalRange: finalRange}
		nextOffset = end
	}
	if nextOffset != pcmCount {
		t.Fatalf("C step PCM spans end at %d, total PCM=%d", nextOffset, pcmCount)
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return results
}

// TestTransitionFullSequenceMatchesLibopus checks complete transition and
// recovery frames in both CELT-boundary directions at every supported API rate.
// It compares each returned length, final range, and float bit against the
// matched libopus build, with no window-specific tolerance.
func TestTransitionFullSequenceMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize48 = 480
	modes := []encoder.Mode{encoder.ModeHybrid, encoder.ModeCELT, encoder.ModeHybrid, encoder.ModeCELT, encoder.ModeHybrid}
	for _, channels := range []int{1, 2} {
		packets := encodeModeSwitchSingleStreamPackets(t, channels, frameSize48, modes)
		for i, packet := range packets {
			wantMode := streamModeHybrid
			if modes[i] == encoder.ModeCELT {
				wantMode = streamModeCELT
			}
			if gotMode := streamModeOfPacket(packet); gotMode != wantMode {
				t.Fatalf("packet %d mode=%d want %d", i, gotMode, wantMode)
			}
		}
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			frameSize := frameSize48 * sampleRate / 48000
			steps := make([]transitionDecodeStep, len(packets))
			for i, packet := range packets {
				steps[i] = transitionDecodeStep{packet: packet, frameSize: frameSize}
			}
			for _, gainQ8 := range []int{0, 768, -768} {
				t.Run(fmt.Sprintf("ch%d/fs%d/g%d", channels, sampleRate, gainQ8), func(t *testing.T) {
					want := decodeTransitionSequenceWithLibopus(t, sampleRate, channels, gainQ8, frameSize, steps)
					dec := newStreamDecoder(sampleRate, channels)
					if err := dec.SetGain(gainQ8); err != nil {
						t.Fatalf("SetGain(%d): %v", gainQ8, err)
					}
					for i, step := range steps {
						if want[i].samples != frameSize {
							t.Fatalf("C frame %d returned %d samples/channel, want packet duration %d", i, want[i].samples, frameSize)
						}
						got, err := dec.Decode(step.packet, step.frameSize)
						if err != nil {
							t.Fatalf("Go frame %d: %v", i, err)
						}
						if len(got) != want[i].samples*channels {
							t.Fatalf("Go frame %d returned %d PCM samples, C %d", i, len(got), want[i].samples*channels)
						}
						if gotRange := dec.FinalRange(); gotRange != want[i].finalRange {
							t.Fatalf("frame %d final range Go=%08x C=%08x", i, gotRange, want[i].finalRange)
						}
						assertTransitionStagePCMExact(t, got, want[i].pcm, fmt.Sprintf("frame %d %v→%v", i, modes[max(0, i-1)], modes[i]))
					}
				})
			}
		}
	}
}

// TestTransitionPreviousCELTPLCStageMatchesLibopus isolates the CELT PLC audio
// entering CELT→Hybrid transitions. A public C NULL decode after the identical
// packet history requests exactly F5 samples/channel, matching the recursive
// opus_decode_frame(NULL) CELT branch before its outer crossfade.
func TestTransitionPreviousCELTPLCStageMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize48 = 480
	modes := []encoder.Mode{encoder.ModeHybrid, encoder.ModeCELT, encoder.ModeHybrid, encoder.ModeCELT, encoder.ModeHybrid}
	for _, channels := range []int{1, 2} {
		packets := encodeModeSwitchSingleStreamPackets(t, channels, frameSize48, modes)
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			frameSize := frameSize48 * sampleRate / 48000
			f5 := sampleRate / 200
			for _, gainQ8 := range []int{0, 768, -768} {
				for _, target := range []int{2, 4} {
					t.Run(fmt.Sprintf("ch%d/fs%d/g%d/target%d", channels, sampleRate, gainQ8, target), func(t *testing.T) {
						steps := make([]transitionDecodeStep, target+1)
						for i := range target {
							steps[i] = transitionDecodeStep{packet: packets[i], frameSize: frameSize}
						}
						steps[target] = transitionDecodeStep{frameSize: f5}
						want := decodeTransitionSequenceWithLibopus(t, sampleRate, channels, gainQ8, frameSize, steps)
						dec := newStreamDecoder(sampleRate, channels)
						if err := dec.SetGain(gainQ8); err != nil {
							t.Fatal(err)
						}
						for i := range target {
							got, err := dec.Decode(packets[i], frameSize)
							if err != nil {
								t.Fatalf("history frame %d: %v", i, err)
							}
							if want[i].samples != frameSize || len(got) != frameSize*channels || dec.FinalRange() != want[i].finalRange {
								t.Fatalf("history frame %d length/range Go=(%d,%08x) C=(%d,%08x)",
									i, len(got), dec.FinalRange(), want[i].samples*channels, want[i].finalRange)
							}
							assertTransitionStagePCMExact(t, got, want[i].pcm, fmt.Sprintf("history frame %d", i))
						}
						if dec.lastMode != streamModeCELT {
							t.Fatalf("previous mode=%d, want CELT", dec.lastMode)
						}
						got, err := dec.transitionPLCToFloat32(f5, int(dec.lastMode), int(dec.lastBandwidth), dec.lastPacketStereo)
						if err != nil {
							t.Fatal(err)
						}
						if want[target].samples != f5 || len(got) != f5*channels {
							t.Fatalf("F5 PLC length Go=%d C=%d want %d", len(got), want[target].samples*channels, f5*channels)
						}
						assertTransitionStagePCMExact(t, got, want[target].pcm, "full CELT PLC stage")
					})
				}
			}
		}
	}
}

const (
	transitionStageSampleRate = 48000
	transitionStageGainQ8     = 768
	transitionStageF10        = transitionStageSampleRate / 100
	transitionStageF5         = transitionStageF10 / 2
)

var transitionStageRefHelper libopustest.HelperCache

type transitionStageStep struct {
	packet    []byte
	frameSize int
}

func encodeTransitionGainRegressionInput(t *testing.T) (msDecodeFuzzSpec, *surroundEncodeRef) {
	t.Helper()
	const caseName = "stereo/fs480/br32000/vbrfalse/cfalse/g768/f32"
	var spec msDecodeFuzzSpec
	found := false
	for _, candidate := range buildSurroundDecodeFuzzSweep() {
		if candidate.name == caseName {
			spec = candidate
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("surround decode regression %q is missing", caseName)
	}
	pcm := seededMultichannelPCM(spec.seed, spec.channels, spec.frameSize, spec.frameCount)
	ref, err := encodeLibopusSurround(
		transitionStageSampleRate,
		spec.channels,
		spec.mappingFamily,
		2049,
		spec.bitrate,
		spec.vbr,
		spec.vbrConstraint,
		10,
		-1000,
		spec.frameSize,
		spec.frameCount,
		4000,
		pcm, false,
	)
	if err != nil {
		libopustest.HelperUnavailable(t, "multistream transition PLC input packets", err)
	}
	if ref.streams != 1 || ref.coupledStreams != 1 {
		t.Fatalf("stereo transition regression stream layout=(%d,%d), want (1,1)", ref.streams, ref.coupledStreams)
	}
	if len(ref.packets) < 2 {
		t.Fatalf("transition regression packets=%d, want at least 2", len(ref.packets))
	}
	return spec, ref
}

func firstTransitionStreamPackets(t *testing.T, packets [][]byte) ([]byte, []byte, streamTOC, streamTOC) {
	t.Helper()
	prevStreams, err := parseMultistreamPacket(packets[0], 1)
	if err != nil {
		t.Fatalf("parse preceding multistream packet: %v", err)
	}
	nextStreams, err := parseMultistreamPacket(packets[1], 1)
	if err != nil {
		t.Fatalf("parse transition multistream packet: %v", err)
	}
	if len(prevStreams) != 1 || len(prevStreams[0]) == 0 || len(nextStreams) != 1 || len(nextStreams[0]) == 0 {
		t.Fatalf("transition packets have elementary stream counts %d and %d", len(prevStreams), len(nextStreams))
	}
	prevPacket, nextPacket := prevStreams[0], nextStreams[0]
	prevTOC, nextTOC := parseStreamTOC(prevPacket[0]), parseStreamTOC(nextPacket[0])
	if (prevTOC.mode == streamModeCELT) == (nextTOC.mode == streamModeCELT) {
		t.Fatalf("first two packet modes do not cross the CELT boundary: %d -> %d", prevTOC.mode, nextTOC.mode)
	}
	return prevPacket, nextPacket, prevTOC, nextTOC
}

// decodeTransitionStageWithLibopus runs a packet followed by a public
// NULL-packet PLC request through the paired single-stream libopus decoder.
// The v7 helper supports a distinct output size for each step, so the PLC
// request uses the same F5 capacity as opus_decode_frame's recursive
// transition call. This is a candidate-equivalent stage probe; the separate
// full-transition comparison verifies the recursive transition context.
func decodeTransitionStageWithLibopus(t *testing.T, channels, gainQ8, maxFrameSize int, steps []transitionStageStep) []float32 {
	t.Helper()
	binPath, err := transitionStageRefHelper.Path(func() (string, error) {
		return buildMultistreamFloatShadowReferenceHelper(libopustest.CHelperConfig{
			Label:      "multistream transition PLC stage reference",
			OutputBase: "gopus_multistream_transition_plc_stage",
			SourceFile: "libopus_refdecode_single.c",
			CFlags:     []string{"-O3", "-DNDEBUG"},
			Libs:       []string{"-lm"},
		})
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "multistream transition PLC stage reference", err)
	}

	const sampleFormatFloat32 = uint32(0)
	payload := libopustest.NewOraclePayloadVersion(
		"GOSI",
		7,
		sampleFormatFloat32,
		transitionStageSampleRate,
		uint32(int32(gainQ8)),
		uint32(channels),
		uint32(maxFrameSize),
		uint32(len(steps)),
	)
	for _, step := range steps {
		if step.frameSize <= 0 || step.frameSize > maxFrameSize {
			t.Fatalf("invalid transition stage frame size %d (maximum %d)", step.frameSize, maxFrameSize)
		}
		payload.U32(0) // decode_fec
		payload.U32(uint32(step.frameSize))
		payload.U32(uint32(len(step.packet)))
		payload.Raw(step.packet)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "multistream transition PLC stage reference", "GOSO")
	if err != nil {
		libopustest.HelperUnavailable(t, "multistream transition PLC stage reference", err)
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 4)
	pcm := make([]float32, nSamples)
	for i := range pcm {
		pcm[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatalf("multistream transition PLC stage reference: %v", err)
	}
	return pcm
}

func assertTransitionStagePCMExact(t *testing.T, got, want []float32, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s sample count=%d want %d", label, len(got), len(want))
	}
	for i := range want {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("%s first difference at sample %d: Go=%08x (%g) C=%08x (%g)",
				label, i, math.Float32bits(got[i]), got[i], math.Float32bits(want[i]), want[i])
		}
	}
}

// TestTransitionPLCStageGainMatchesLibopus uses the exact previous stream
// packet from the strict surround decode regression and compares the entire
// transition PLC buffer against a paired C decode of that packet followed by a
// 5 ms NULL-packet PLC request. Matching the preceding PCM and the complete
// PLC buffer checks this candidate-equivalent C stage; it does not by itself
// establish that the recursive transition call has identical surrounding
// decoder context. TestCELTTransitionPLCStageHasInnerAndOuterGainChecks keeps
// that full-transition comparison strict.
func TestTransitionPLCStageGainMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const channels = 2
	spec, ref := encodeTransitionGainRegressionInput(t)
	prevPacket, nextPacket, prevTOC, nextTOC := firstTransitionStreamPackets(t, ref.packets)
	prevDuration := getFrameDuration(prevPacket)
	nextDuration := getFrameDuration(nextPacket)
	if prevDuration != spec.frameSize || nextDuration != spec.frameSize {
		t.Fatalf("transition packet durations=(%d,%d), want both %d samples/channel", prevDuration, nextDuration, spec.frameSize)
	}
	if prevTOC.stereo != (channels == 2) || nextTOC.stereo != (channels == 2) {
		t.Fatalf("transition packet stereo flags=(%t,%t), want both true for %d-channel stream", prevTOC.stereo, nextTOC.stereo, channels)
	}
	t.Logf("strict transition cohort: prior(mode=%d bw=%d stereo=%t duration=%d), target(mode=%d bw=%d stereo=%t duration=%d), channels=%d",
		prevTOC.mode, prevTOC.bandwidth, prevTOC.stereo, prevDuration,
		nextTOC.mode, nextTOC.bandwidth, nextTOC.stereo, nextDuration, channels)

	for _, gainQ8 := range []int{0, transitionStageGainQ8} {
		t.Run(fmt.Sprintf("gain_%d", gainQ8), func(t *testing.T) {
			state := newStreamDecoder(transitionStageSampleRate, channels)
			if err := state.SetGain(gainQ8); err != nil {
				t.Fatalf("SetGain(%d): %v", gainQ8, err)
			}
			previous, err := state.Decode(prevPacket, spec.frameSize)
			if err != nil {
				t.Fatalf("decode preceding elementary packet: %v", err)
			}
			if int(state.lastMode) != prevTOC.mode || int(state.lastBandwidth) != prevTOC.bandwidth || state.lastPacketStereo != prevTOC.stereo {
				t.Fatalf("Go previous decode state=(mode=%d bw=%d stereo=%t), TOC=(mode=%d bw=%d stereo=%t)",
					state.lastMode, state.lastBandwidth, state.lastPacketStereo, prevTOC.mode, prevTOC.bandwidth, prevTOC.stereo)
			}
			if state.prevRedundancy {
				t.Fatal("previous stream packet reports redundancy; standalone NULL decode would not match the recursive transition call")
			}

			got, err := state.transitionPLCToFloat32(transitionStageF5, prevTOC.mode, prevTOC.bandwidth, prevTOC.stereo)
			if err != nil {
				t.Fatalf("decode Go transition PLC stage: %v", err)
			}
			refPCM := decodeTransitionStageWithLibopus(t, channels, gainQ8, spec.frameSize, []transitionStageStep{
				{packet: prevPacket, frameSize: spec.frameSize},
				{packet: nil, frameSize: transitionStageF5},
			})
			previousSamples := spec.frameSize * channels
			stageSamples := transitionStageF5 * channels
			if len(previous) != previousSamples || len(refPCM) != previousSamples+stageSamples {
				t.Fatalf("paired C/Go sequence lengths: preceding Go=%d, C=%d; want %d + %d samples (C previous packet + PLC step)",
					len(previous), len(refPCM), previousSamples, stageSamples)
			}
			assertTransitionStagePCMExact(t, previous, refPCM[:previousSamples], fmt.Sprintf("gain=%d preceding packet mode=%d", gainQ8, prevTOC.mode))
			assertTransitionStagePCMExact(t, got, refPCM[previousSamples:], fmt.Sprintf("gain=%d previous-mode=%d", gainQ8, prevTOC.mode))
		})
	}
}

// TestCELTTransitionPLCStageHasInnerAndOuterGainChecks each complete frame of a
// CELT boundary and its recovery sequence against the paired multistream C
// decoder. The recursive PLC stage applies gain before the crossfade, and the
// outer frame applies gain afterward.
func TestCELTTransitionPLCStageHasInnerAndOuterGainChecks(t *testing.T) {
	libopustest.RequireOracle(t)

	const channels = 2
	spec, ref := encodeTransitionGainRegressionInput(t)
	prevPacket, nextPacket, _, _ := firstTransitionStreamPackets(t, ref.packets)
	if len(prevPacket) == 0 || len(nextPacket) == 0 {
		t.Fatal("transition packets are empty")
	}
	for _, gainQ8 := range []int{0, transitionStageGainQ8, -transitionStageGainQ8} {
		t.Run(fmt.Sprintf("gain_%d", gainQ8), func(t *testing.T) {
			want, err := decodeWithLibopusReferencePacketsGain(
				1, transitionStageSampleRate, channels, ref.streams, ref.coupledStreams,
				spec.frameSize, gainQ8, ref.mapping, nil, ref.packets,
			)
			if err != nil {
				libopustest.HelperUnavailable(t, "multistream transition output reference", err)
			}
			perFrame := spec.frameSize * channels
			if len(want) != len(ref.packets)*perFrame {
				t.Fatalf("C decoded %d samples, want %d packets x %d samples", len(want), len(ref.packets), perFrame)
			}
			dec, err := NewDecoder(transitionStageSampleRate, channels, ref.streams, ref.coupledStreams, ref.mapping)
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			if err := dec.SetGain(gainQ8); err != nil {
				t.Fatalf("SetGain(%d): %v", gainQ8, err)
			}
			for i, packet := range ref.packets {
				got, err := dec.DecodeToFloat32(packet, spec.frameSize)
				if err != nil {
					t.Fatalf("frame %d Go decode: %v", i, err)
				}
				if len(got) != perFrame {
					t.Fatalf("frame %d Go decoded %d samples, want %d", i, len(got), perFrame)
				}
				assertTransitionStagePCMExact(t, got, want[i*perFrame:(i+1)*perFrame],
					fmt.Sprintf("frame %d with gain %d", i, gainQ8))
			}
		})
	}
}

// TestCELTTransitionFadeReplaysMatchedLibopus applies both multiplication
// orders to the actual transition PLC and main Hybrid buffers. The C frame
// selects the ordering used by src/opus_decoder.c smooth_fade on this build.
func TestCELTTransitionFadeReplaysMatchedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const channels = 2
	spec, ref := encodeTransitionGainRegressionInput(t)
	prevPacket, nextPacket, _, nextTOC := firstTransitionStreamPackets(t, ref.packets)
	if nextTOC.mode != streamModeHybrid {
		t.Fatalf("target mode=%d, want Hybrid", nextTOC.mode)
	}
	want := decodeTransitionSequenceWithLibopus(t, transitionStageSampleRate, channels, 0, spec.frameSize, []transitionDecodeStep{
		{packet: prevPacket, frameSize: spec.frameSize},
		{packet: nextPacket, frameSize: spec.frameSize},
	})
	perFrame := spec.frameSize * channels
	if len(want) != 2 {
		t.Fatalf("C returned %d steps, want two", len(want))
	}
	for i := range want {
		if want[i].samples != spec.frameSize || len(want[i].pcm) != perFrame {
			t.Fatalf("C frame %d returned %d samples and %d PCM values, want %d and %d",
				i, want[i].samples, len(want[i].pcm), spec.frameSize, perFrame)
		}
	}
	state := newStreamDecoder(transitionStageSampleRate, channels)
	if _, err := state.Decode(prevPacket, spec.frameSize); err != nil {
		t.Fatal(err)
	}
	parsed, err := parseOpusPacket(nextPacket, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.frames) != 1 {
		t.Fatalf("target has %d frames, want one", len(parsed.frames))
	}
	state.recordDecodeCall(spec.frameSize, len(nextPacket))
	ts := transitionState{
		active:           true,
		prevMode:         int(state.lastMode),
		prevBW:           int(state.lastBandwidth),
		prevStereo:       state.lastPacketStereo,
		pendingTransSize: transitionStageF5,
	}
	main, err := state.decodeHybridToFloat32(parsed.frames[0], spec.frameSize, nextTOC, &ts)
	if err != nil {
		t.Fatal(err)
	}
	if len(main) != perFrame || len(ts.pcm) != transitionStageF5*channels {
		t.Fatalf("actual main/transition buffers have lengths %d/%d", len(main), len(ts.pcm))
	}
	f2_5 := transitionStageF5 / 2
	window := celt.GetWindowBufferF32(f2_5)
	fused := append([]float32(nil), main...)
	rounded := append([]float32(nil), main...)
	copy(fused[:f2_5*channels], ts.pcm[:f2_5*channels])
	copy(rounded[:f2_5*channels], ts.pcm[:f2_5*channels])
	for i := range f2_5 {
		w := streamSmoothFadeMul(window[i], window[i])
		oneMinus := streamSmoothFadeSub(1, w)
		for ch := range channels {
			idx := (f2_5+i)*channels + ch
			other := streamSmoothFadeMul(oneMinus, ts.pcm[idx])
			fused[idx] = streamSmoothFadeMulAdd(w, main[idx], other)
			rounded[idx] = streamSmoothFadeMul(w, main[idx]) + other
		}
	}
	cFrame := want[1].pcm
	assertTransitionStagePCMExact(t, fused[:transitionStageF5*channels], cFrame[:transitionStageF5*channels], "actual-buffer fused transition window")
	roundedDifferences := 0
	for i := f2_5 * channels; i < transitionStageF5*channels; i++ {
		if math.Float32bits(rounded[i]) != math.Float32bits(cFrame[i]) {
			roundedDifferences++
		}
	}
	t.Logf("crossfade samples differing under separately rounded first product: %d", roundedDifferences)
}

var silkToCELTTransitionPackets = []struct {
	name      string
	frameSize int
	target    int
	packets   []string
}{
	{
		name:      "celt_silk_silk_celt",
		frameSize: 960,
		target:    3,
		packets: []string{
			"987e1a9df12c1f5762337294a37f",
			"0b0185a6d366f69adef15ecaf892e0",
			"08858cdb858cd482b2a5486aa940",
			"98024c8c2bb47c4e3fc402cf",
			"9817311653c9705554eaa1bf41",
		},
	},
	{
		name:      "silk_silk_celt",
		frameSize: 2880,
		target:    2,
		packets: []string{
			"18e127ad6528c2bb9d1b995b365bd8c074d634d0ac110cbe7ed96d9907ef2696b371e8c70379d79ef336e50f19",
			"1b4101e5c949891be191b90f13dcd161178a4073bbeb47a874359cff57f349986af44f9b7972538b35b000",
			"9b43057daac888d9d95e0b018c91f80ef203c25bd365748ce8b80a37497b959966f8a6d2dffa780000000000",
			"9b43044775c3919b5877da4031c3274794a87fc523d5570696dd2c4a6bf044532788267796e19a00000000",
			"18e5c580bf1bfc1f79ee5abb88c809a2d5316fd8ab1b61703f8ed3c3608d8055d0bb082520ba2ca26278eab5b6cbc01c",
		},
	},
}

func TestSILKToCELTTransitionPLCMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate = 48000
	for _, tc := range silkToCELTTransitionPackets {
		t.Run(tc.name, func(t *testing.T) {
			packets := make([][]byte, len(tc.packets))
			steps := make([]transitionDecodeStep, len(packets))
			for i, encoded := range tc.packets {
				packet, err := hex.DecodeString(encoded)
				if err != nil {
					t.Fatalf("packet %d: %v", i, err)
				}
				packets[i] = packet
				steps[i] = transitionDecodeStep{packet: packet, frameSize: tc.frameSize}
			}

			want := decodeTransitionSequenceWithLibopus(t, sampleRate, 1, 0, tc.frameSize, steps)
			dec := newStreamDecoder(sampleRate, 1)
			for i, packet := range packets {
				got, err := dec.Decode(packet, tc.frameSize)
				if err != nil {
					t.Fatalf("frame %d: %v", i, err)
				}
				if want[i].samples != tc.frameSize || len(got) != want[i].samples {
					t.Fatalf("frame %d length Go=%d C=%d want=%d", i, len(got), want[i].samples, tc.frameSize)
				}
				if gotRange := dec.FinalRange(); gotRange != want[i].finalRange {
					t.Fatalf("frame %d range Go=%08x C=%08x", i, gotRange, want[i].finalRange)
				}
				assertTransitionStagePCMExact(t, got, want[i].pcm, fmt.Sprintf("frame %d", i))
			}

			for _, plcSize := range []int{sampleRate / 200, sampleRate / 100} {
				t.Run(fmt.Sprintf("plc%d", plcSize), func(t *testing.T) {
					stageSteps := append([]transitionDecodeStep(nil), steps[:tc.target]...)
					stageSteps = append(stageSteps, transitionDecodeStep{frameSize: plcSize})
					stageWant := decodeTransitionSequenceWithLibopus(t, sampleRate, 1, 0, tc.frameSize, stageSteps)
					stageDec := newStreamDecoder(sampleRate, 1)
					for i := range tc.target {
						got, err := stageDec.Decode(packets[i], tc.frameSize)
						if err != nil {
							t.Fatalf("history frame %d: %v", i, err)
						}
						if stageWant[i].samples != tc.frameSize || len(got) != tc.frameSize || stageDec.FinalRange() != stageWant[i].finalRange {
							t.Fatalf("history frame %d length/range Go=(%d,%08x) C=(%d,%08x)",
								i, len(got), stageDec.FinalRange(), stageWant[i].samples, stageWant[i].finalRange)
						}
						assertTransitionStagePCMExact(t, got, stageWant[i].pcm, fmt.Sprintf("history frame %d", i))
					}
					if stageDec.lastMode != streamModeSILK {
						t.Fatalf("previous mode=%d want SILK", stageDec.lastMode)
					}
					got, err := stageDec.transitionPLCToFloat32(plcSize, int(stageDec.lastMode), int(stageDec.lastBandwidth), stageDec.lastPacketStereo)
					if err != nil {
						t.Fatal(err)
					}
					if stageWant[tc.target].samples != plcSize || len(got) != plcSize {
						t.Fatalf("PLC length Go=%d C=%d want=%d", len(got), stageWant[tc.target].samples, plcSize)
					}
					assertTransitionStagePCMExact(t, got, stageWant[tc.target].pcm, "full SILK PLC stage")
				})
			}
		})
	}
}

// TestSILKPLCDurationChangesMatchLibopus checks current loss-frame geometry
// against previous-good excitation geometry in both duration directions. The
// next good packet also checks the state carried out of concealment.
func TestSILKPLCDurationChangesMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate = 48000
	for _, channels := range []int{1, 2} {
		for _, bandwidth := range []types.Bandwidth{types.BandwidthNarrowband, types.BandwidthMediumband, types.BandwidthWideband} {
			for _, sizes := range [][2]int{{960, 480}, {480, 960}, {480, 720}} {
				goodSize, lossSize := sizes[0], sizes[1]
				t.Run(fmt.Sprintf("ch%d/bw%d/good%d/loss%d", channels, bandwidth, goodSize, lossSize), func(t *testing.T) {
					packets := encodeModeSwitchSingleStreamPacketsWithSILKBandwidth(
						t, channels, goodSize, []encoder.Mode{encoder.ModeSILK, encoder.ModeSILK}, bandwidth)
					for i, packet := range packets {
						if got := streamModeOfPacket(packet); got != streamModeSILK {
							t.Fatalf("packet %d mode=%d want SILK", i, got)
						}
					}
					steps := []transitionDecodeStep{
						{packet: packets[0], frameSize: goodSize},
						{frameSize: lossSize},
						{packet: packets[1], frameSize: goodSize},
					}
					want := decodeTransitionSequenceWithLibopus(t, sampleRate, channels, 0, max(goodSize, lossSize), steps)
					dec := newStreamDecoder(sampleRate, channels)
					for i, step := range steps {
						got, err := dec.Decode(step.packet, step.frameSize)
						if err != nil {
							t.Fatalf("step %d: %v", i, err)
						}
						if want[i].samples != step.frameSize || len(got) != want[i].samples*channels {
							t.Fatalf("step %d length Go=%d C=%d want=%d", i, len(got), want[i].samples*channels, step.frameSize*channels)
						}
						if gotRange := dec.FinalRange(); gotRange != want[i].finalRange {
							t.Fatalf("step %d range Go=%08x C=%08x", i, gotRange, want[i].finalRange)
						}
						assertTransitionStagePCMExact(t, got, want[i].pcm, fmt.Sprintf("step %d", i))
					}
				})
			}
		}
	}
}
