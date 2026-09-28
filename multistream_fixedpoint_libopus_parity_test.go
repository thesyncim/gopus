//go:build gopus_fixed_point

package gopus

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

var multistreamFixedRefdecodeHelper libopustest.HelperCache

// runLibopusMultistreamFixedDecode drives the libopus multistream public API
// (opus_multistream_decode / opus_multistream_decode24) built against the
// fixed-point reference selected by the current Go build, including ENABLE_QEXT.
func runLibopusMultistreamFixedDecode(sampleRate, channels, streams, coupled, frameSize, sampleFormat int, mapping []byte, packets [][]byte) (*libopustest.OracleReader, error) {
	return runLibopusMultistreamFixedDecodeWithGain(sampleRate, channels, streams, coupled, frameSize, sampleFormat, 0, mapping, packets)
}

func runLibopusMultistreamFixedDecodeWithGain(sampleRate, channels, streams, coupled, frameSize, sampleFormat, gainQ8 int, mapping []byte, packets [][]byte) (*libopustest.OracleReader, error) {
	binPath, err := multistreamFixedRefdecodeHelper.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:      "multistream fixed reference decode",
			OutputBase: "gopus_libopus_refdecode_multistream_fixed",
			SourceFile: "libopus_refdecode_multistream.c",
			CFlags:     []string{"-O3", "-DNDEBUG"},
			DeadStrip:  true,
		})
	})
	if err != nil {
		return nil, err
	}

	payload := libopustest.NewOraclePayloadVersion(
		"GMSI", 4,
		uint32(sampleRate),
		uint32(gainQ8),
		uint32(sampleFormat),
		1,
		uint32(channels),
		uint32(streams),
		uint32(coupled),
		uint32(frameSize),
		uint32(len(packets)),
		uint32(len(mapping)),
		0,
	)
	payload.Raw(mapping)
	for _, packet := range packets {
		payload.U32(uint32(len(packet)))
		payload.Raw(packet)
	}
	return libopustest.RunOracle(binPath, payload.Bytes(), "multistream fixed reference decode", "GMSO")
}

func decodeLibopusMultistreamFixedInt16(sampleRate, channels, streams, coupled, frameSize int, mapping []byte, packets [][]byte) ([]int16, error) {
	return decodeLibopusMultistreamFixedInt16WithGain(sampleRate, channels, streams, coupled, frameSize, 0, mapping, packets)
}

func decodeLibopusMultistreamFixedInt16WithGain(sampleRate, channels, streams, coupled, frameSize, gainQ8 int, mapping []byte, packets [][]byte) ([]int16, error) {
	reader, err := runLibopusMultistreamFixedDecodeWithGain(sampleRate, channels, streams, coupled, frameSize, libopusRefdecodeMSFormatInt16, gainQ8, mapping, packets)
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 2)
	out := make([]int16, nSamples)
	for i := range out {
		out[i] = reader.I16()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func decodeLibopusMultistreamFixedInt24(sampleRate, channels, streams, coupled, frameSize int, mapping []byte, packets [][]byte) ([]int32, error) {
	return decodeLibopusMultistreamFixedInt24WithGain(sampleRate, channels, streams, coupled, frameSize, 0, mapping, packets)
}

func decodeLibopusMultistreamFixedInt24WithGain(sampleRate, channels, streams, coupled, frameSize, gainQ8 int, mapping []byte, packets [][]byte) ([]int32, error) {
	reader, err := runLibopusMultistreamFixedDecodeWithGain(sampleRate, channels, streams, coupled, frameSize, libopusRefdecodeMSFormatInt24, gainQ8, mapping, packets)
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 4)
	out := make([]int32, nSamples)
	for i := range out {
		out[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

const libopusRefdecodeMSFormatInt16 = 1

// packSelfDelimitedLength encodes an Opus self-delimited frame length (RFC 6716
// Appendix B): one byte when < 252, otherwise two bytes.
func packSelfDelimitedLength(dst []byte, n int) []byte {
	if n < 252 {
		return append(dst, byte(n))
	}
	return append(dst, byte(252+(n-252)&0x3), byte((n-252)>>2))
}

// buildMultistreamPacket concatenates per-stream single-frame (code 0) Opus
// packets into one multistream packet: the first N-1 streams use self-delimited
// framing (TOC, length, frame data) and the last stream uses standard framing
// (TOC, frame data), matching opus_multistream_packet framing.
func buildMultistreamPacket(t *testing.T, streamPackets [][]byte) []byte {
	t.Helper()
	var out []byte
	for i, pkt := range streamPackets {
		if len(pkt) < 1 {
			t.Fatalf("stream %d: empty packet", i)
		}
		if (pkt[0] & 0x03) != 0 {
			t.Fatalf("stream %d: expected code-0 (single frame) packet, toc=%#x", i, pkt[0])
		}
		toc := pkt[0]
		frame := pkt[1:]
		if i < len(streamPackets)-1 {
			out = append(out, toc)
			out = packSelfDelimitedLength(out, len(frame))
			out = append(out, frame...)
		} else {
			out = append(out, toc)
			out = append(out, frame...)
		}
	}
	return out
}

// TestMultistreamDecodeFixedPointParity gates that, under -tags gopus_fixed_point,
// MultistreamDecoder.DecodeInt16 / DecodeInt24 of CELT-only multistream packets
// are bit-exact with the libopus FIXED_POINT opus_multistream_decode /
// opus_multistream_decode24 reference. Each elementary stream is routed through
// the integer opus_res path and the surround channel mapping is applied in the
// integer domain (RES2INT16 / RES2INT24), matching
// opus_multistream_decode_native built FIXED_POINT (no soft clip).
//
// Layouts cover mono streams, coupled (stereo) streams, a 5.1-style
// 4-stream/2-coupled surround mapping, and Hybrid streams (a Hybrid stereo
// coupled stream and a coupled layout mixing Hybrid streams), multi-frame.
// Bit-exact on every architecture: the integer decode has no fused-multiply-add,
// so there is no per-arch float drift.
func TestMultistreamDecodeFixedPointParity(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate  = 48000
		frameSize48 = 960
		frames      = 4
	)

	type layout struct {
		name     string
		channels int
		streams  int
		coupled  int
		mapping  []byte
		// streamChans gives the channel count of each stream (2 for coupled).
		streamChans []int
		// streamMode selects the per-stream packet mode (ModeCELT default, or
		// ModeHybrid). nil means all CELT.
		streamMode []Mode
	}
	layouts := []layout{
		{"mono_2streams", 2, 2, 0, []byte{0, 1}, []int{1, 1}, nil},
		{"stereo_coupled", 2, 1, 1, []byte{0, 1}, []int{2}, nil},
		{"quad_2coupled", 4, 2, 2, []byte{0, 1, 2, 3}, []int{2, 2}, nil},
		{"surround51", 6, 4, 2, []byte{0, 4, 1, 2, 3, 5}, []int{2, 2, 1, 1}, nil},
		{"hybrid_stereo_coupled", 2, 1, 1, []byte{0, 1}, []int{2}, []Mode{ModeHybrid}},
		{"hybrid_mono_2streams", 2, 2, 0, []byte{0, 1}, []int{1, 1}, []Mode{ModeHybrid, ModeHybrid}},
		{"hybrid_quad_2coupled", 4, 2, 2, []byte{0, 1, 2, 3}, []int{2, 2}, []Mode{ModeHybrid, ModeHybrid}},
		{"mixed_hybrid_celt_coupled", 4, 2, 2, []byte{0, 1, 2, 3}, []int{2, 2}, []Mode{ModeHybrid, ModeCELT}},
	}

	for _, lo := range layouts {
		t.Run(lo.name, func(t *testing.T) {
			// Build `frames` multistream packets, each composed of one frame per
			// stream (distinct payloads per frame so cross-frame integer CELT/Hybrid
			// state is exercised).
			msPackets := make([][]byte, 0, frames)
			for f := 0; f < frames; f++ {
				streamPackets := make([][]byte, lo.streams)
				for s := 0; s < lo.streams; s++ {
					ch := lo.streamChans[s]
					mode := ModeCELT
					if lo.streamMode != nil {
						mode = lo.streamMode[s]
					}
					var pkt []byte
					if mode == ModeHybrid {
						pkt = encodeAPIRateHybridPacketFrameSizeVariant(t, ch, frameSize48, f*8+s+1)
					} else {
						pkt = encodeAPIRateCELTPacketFrameSizeVariant(t, ch, frameSize48, 128000, f*8+s+1)
					}
					if toc := ParseTOC(pkt[0]); toc.Mode != mode {
						t.Skipf("stream %d frame %d: encoder produced mode %v, want %v", s, f, toc.Mode, mode)
					}
					streamPackets[s] = pkt
				}
				msPackets = append(msPackets, buildMultistreamPacket(t, streamPackets))
			}

			refInt16, err := decodeLibopusMultistreamFixedInt16(sampleRate, lo.channels, lo.streams, lo.coupled, frameSize48, lo.mapping, msPackets)
			if err != nil {
				libopustest.HelperUnavailable(t, "multistream fixed reference decode int16", err)
				return
			}
			refInt24, err := decodeLibopusMultistreamFixedInt24(sampleRate, lo.channels, lo.streams, lo.coupled, frameSize48, lo.mapping, msPackets)
			if err != nil {
				libopustest.HelperUnavailable(t, "multistream fixed reference decode int24", err)
				return
			}

			dec16, err := NewMultistreamDecoder(sampleRate, lo.channels, lo.streams, lo.coupled, lo.mapping)
			if err != nil {
				t.Fatalf("NewMultistreamDecoder int16: %v", err)
			}
			dec24, err := NewMultistreamDecoder(sampleRate, lo.channels, lo.streams, lo.coupled, lo.mapping)
			if err != nil {
				t.Fatalf("NewMultistreamDecoder int24: %v", err)
			}

			var got16, got24 []int32
			for p, pkt := range msPackets {
				o16 := make([]int16, frameSize48*lo.channels)
				if _, err := dec16.DecodeInt16(pkt, o16); err != nil {
					t.Fatalf("packet %d DecodeInt16: %v", p, err)
				}
				got16 = append(got16, int16ToInt32(o16)...)
				o24 := make([]int32, frameSize48*lo.channels)
				if _, err := dec24.DecodeInt24(pkt, o24); err != nil {
					t.Fatalf("packet %d DecodeInt24: %v", p, err)
				}
				got24 = append(got24, o24...)
			}

			assertFixedExact(t, "int16", got16, int16ToInt32(refInt16))
			assertFixedExact(t, "int24", got24, refInt24)

			// Reset must rewind the integer CELT state alongside the float
			// decoder state. Replaying the same sequence must reproduce the
			// selected FIXED_POINT stream after reset.
			dec16.Reset()
			dec24.Reset()
			var replay16, replay24 []int32
			for p, pkt := range msPackets {
				o16 := make([]int16, frameSize48*lo.channels)
				if _, err := dec16.DecodeInt16(pkt, o16); err != nil {
					t.Fatalf("reset replay packet %d DecodeInt16: %v", p, err)
				}
				replay16 = append(replay16, int16ToInt32(o16)...)
				o24 := make([]int32, frameSize48*lo.channels)
				if _, err := dec24.DecodeInt24(pkt, o24); err != nil {
					t.Fatalf("reset replay packet %d DecodeInt24: %v", p, err)
				}
				replay24 = append(replay24, o24...)
			}
			assertFixedExact(t, "reset replay int16", replay16, int16ToInt32(refInt16))
			assertFixedExact(t, "reset replay int24", replay24, refInt24)
		})
	}
}

func TestMultistreamDecodeFixedPointGainMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate, channels, streams, coupled, frameSize = 48000, 2, 1, 1, 960
	mapping := []byte{0, 1}
	packets := make([][]byte, 4)
	for i := range packets {
		packets[i] = encodeAPIRateCELTPacketFrameSizeVariant(t, channels, frameSize, 128000, i+41)
		if toc := ParseTOC(packets[i][0]); toc.Mode != ModeCELT {
			t.Fatalf("packet %d mode = %v, want CELT", i, toc.Mode)
		}
	}

	for _, gainQ8 := range []int{512, -512, 2048, -2048, 32767, -32768} {
		t.Run(fmt.Sprintf("gain_%d", gainQ8), func(t *testing.T) {
			want16, err := decodeLibopusMultistreamFixedInt16WithGain(sampleRate, channels, streams, coupled, frameSize, gainQ8, mapping, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "multistream fixed int16 decode with gain", err)
				return
			}
			want24, err := decodeLibopusMultistreamFixedInt24WithGain(sampleRate, channels, streams, coupled, frameSize, gainQ8, mapping, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "multistream fixed int24 decode with gain", err)
				return
			}

			dec16, err := NewMultistreamDecoder(sampleRate, channels, streams, coupled, mapping)
			if err != nil {
				t.Fatal(err)
			}
			if err := dec16.SetGain(gainQ8); err != nil {
				t.Fatalf("SetGain(%d): %v", gainQ8, err)
			}
			got16 := make([]int32, 0, len(want16))
			out16 := make([]int16, frameSize*channels)
			for i, packet := range packets {
				if n, err := dec16.DecodeInt16(packet, out16); err != nil || n != frameSize {
					t.Fatalf("DecodeInt16 packet %d: samples=%d err=%v", i, n, err)
				}
				got16 = append(got16, int16ToInt32(out16)...)
			}
			assertFixedExact(t, "gained int16", got16, int16ToInt32(want16))
			if allocs := testing.AllocsPerRun(100, func() {
				if n, err := dec16.DecodeInt16(packets[0], out16); err != nil || n != frameSize {
					t.Fatalf("warm gained DecodeInt16: samples=%d err=%v", n, err)
				}
			}); allocs != 0 {
				t.Fatalf("warm gained DecodeInt16 allocations=%g want 0", allocs)
			}

			dec24, err := NewMultistreamDecoder(sampleRate, channels, streams, coupled, mapping)
			if err != nil {
				t.Fatal(err)
			}
			if err := dec24.SetGain(gainQ8); err != nil {
				t.Fatalf("SetGain(%d): %v", gainQ8, err)
			}
			got24 := make([]int32, 0, len(want24))
			out24 := make([]int32, frameSize*channels)
			for i, packet := range packets {
				if n, err := dec24.DecodeInt24(packet, out24); err != nil || n != frameSize {
					t.Fatalf("DecodeInt24 packet %d: samples=%d err=%v", i, n, err)
				}
				got24 = append(got24, out24...)
			}
			assertFixedExact(t, "gained int24", got24, want24)
			if allocs := testing.AllocsPerRun(100, func() {
				if n, err := dec24.DecodeInt24(packets[0], out24); err != nil || n != frameSize {
					t.Fatalf("warm gained DecodeInt24: samples=%d err=%v", n, err)
				}
			}); allocs != 0 {
				t.Fatalf("warm gained DecodeInt24 allocations=%g want 0", allocs)
			}
		})
	}
}
