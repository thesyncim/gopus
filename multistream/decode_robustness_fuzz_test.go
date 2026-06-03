// decode_robustness_fuzz_test.go — oracle-free DECODE robustness fuzzing for the
// public multistream + projection decode entry points. Multistream decode is a
// WebRTC/RTP attack surface: arbitrary, attacker-controlled bytes reach the
// cross-stream self-delimited framing parser, the per-stream Opus decoders, the
// channel-mapping scatter, and the projection demixing matmul. None of that may
// panic or read out of bounds, and every malformed input must surface as a
// proper error.
//
// These are native Go fuzz targets (FuzzXxx), so they run standalone WITHOUT the
// libopus oracle and can be exercised with `go test -fuzz=Fuzz -fuzztime=...`.
// They assert only crash-safety and output-shape invariants on the gopus
// decoder; byte/sample PARITY against libopus on clean and malformed input is
// proven separately by the differential sweeps
// (ms_decode_differential_fuzz_test.go, projection_decode_robustness_fuzz_test.go),
// which these intentionally do not duplicate.
//
// Invariants enforced on every accepted decode:
//   - no panic / no out-of-bounds for ANY input (random or mutated),
//   - len(out) == frameSize_decoded * outputChannels for some
//     0 <= frameSize_decoded <= requested frameSize (so the result is always a
//     whole number of frames no larger than requested), and
//   - the int16 and float32 paths agree on that decoded sample count.

package multistream

import (
	"fmt"
	"testing"
)

// robustFrameSize is the request size used throughout (20 ms at 48 kHz). The
// decoder may legitimately return fewer samples (a shorter packet duration), so
// the invariant is an upper bound, not equality.
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
	pkt, err := enc.Encode(pcm, robustFrameSize)
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
	pkt, err := enc.Encode(pcm, robustFrameSize)
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
