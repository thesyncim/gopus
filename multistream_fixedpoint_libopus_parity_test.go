//go:build gopus_fixed_point

package gopus

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/fixedpoint"
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
	binPath, err := multistreamFixedDecodeHelperPath()
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

func multistreamFixedDecodeHelperPath() (string, error) {
	return multistreamFixedRefdecodeHelper.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:      "multistream fixed reference decode",
			OutputBase: "gopus_libopus_refdecode_multistream_fixed",
			SourceFile: "libopus_refdecode_multistream.c",
			CFlags:     []string{"-O3", "-DNDEBUG"},
			DeadStrip:  true,
		})
	})
}

func decodeLibopusMultistreamFixedInt24WithPhase(sampleRate, channels, streams, coupled, frameSize int, phaseInversionDisabled bool, mapping []byte, packets [][]byte) ([]int32, error) {
	binPath, err := multistreamFixedDecodeHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayloadVersion(
		"GMSI", 5,
		uint32(sampleRate),
		0,
		libopusRefdecodeMSFormatInt24,
		1,
		uint32(channels),
		uint32(streams),
		uint32(coupled),
		uint32(frameSize),
		uint32(len(packets)),
		uint32(len(mapping)),
		0,
	)
	phaseDisabled := uint32(0)
	if phaseInversionDisabled {
		phaseDisabled = 1
	}
	payload.U32(phaseDisabled)
	payload.Raw(mapping)
	for _, packet := range packets {
		payload.U32(uint32(len(packet)))
		payload.Raw(packet)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "multistream fixed phase-control reference decode", "GMSO")
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

	for _, gainQ8 := range []int{512, -512, 2048, -2048, 8192, -8192, 32767, -32768} {
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

func TestMultistreamFixedCELTPLCMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate, channels, streams, coupled, frameSize = 48000, 2, 1, 1, 960
	mapping := []byte{0, 1}
	packet := encodeAPIRateCELTPacketFrameSizeVariant(t, channels, frameSize, 128000, 73)
	if toc := ParseTOC(packet[0]); toc.Mode != ModeCELT {
		t.Fatalf("packet mode = %v, want CELT", toc.Mode)
	}
	const consecutiveLosses = 60
	packets := make([][]byte, consecutiveLosses+2)
	packets[0] = packet
	for i := 1; i <= consecutiveLosses; i++ {
		packets[i] = nil
	}
	packets[len(packets)-1] = packet

	want16, err := decodeLibopusMultistreamFixedInt16(sampleRate, channels, streams, coupled, frameSize, mapping, packets)
	if err != nil {
		libopustest.HelperUnavailable(t, "multistream fixed CELT PLC int16", err)
		return
	}
	want24, err := decodeLibopusMultistreamFixedInt24(sampleRate, channels, streams, coupled, frameSize, mapping, packets)
	if err != nil {
		libopustest.HelperUnavailable(t, "multistream fixed CELT PLC int24", err)
		return
	}

	dec16, err := NewMultistreamDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatal(err)
	}
	dec24, err := NewMultistreamDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatal(err)
	}
	got16 := make([]int32, 0, len(want16))
	got24 := make([]int32, 0, len(want24))
	out16 := make([]int16, frameSize*channels)
	out24 := make([]int32, frameSize*channels)
	for i, pkt := range packets {
		if n, err := dec16.DecodeInt16(pkt, out16); err != nil || n != frameSize {
			t.Fatalf("DecodeInt16 packet %d returned samples=%d, err=%v; want %d", i, n, err, frameSize)
		}
		got16 = append(got16, int16ToInt32(out16)...)
		if n, err := dec24.DecodeInt24(pkt, out24); err != nil || n != frameSize {
			t.Fatalf("DecodeInt24 packet %d returned samples=%d, err=%v; want %d", i, n, err, frameSize)
		}
		got24 = append(got24, out24...)
	}
	assertFixedExact(t, "CELT PLC int16", got16, int16ToInt32(want16))
	assertFixedExact(t, "CELT PLC int24", got24, want24)

	if allocs := testing.AllocsPerRun(100, func() {
		if n, err := dec16.DecodeInt16(nil, out16); err != nil || n != frameSize {
			t.Fatalf("warm CELT PLC DecodeInt16 returned samples=%d, err=%v; want %d", n, err, frameSize)
		}
	}); allocs != 0 {
		t.Fatalf("warm CELT PLC DecodeInt16 allocations=%g want 0", allocs)
	}
	if allocs := testing.AllocsPerRun(100, func() {
		if n, err := dec24.DecodeInt24(nil, out24); err != nil || n != frameSize {
			t.Fatalf("warm CELT PLC DecodeInt24 returned samples=%d, err=%v; want %d", n, err, frameSize)
		}
	}); allocs != 0 {
		t.Fatalf("warm CELT PLC DecodeInt24 allocations=%g want 0", allocs)
	}
}

func TestMultistreamFixedCELTLongPLCMixedOutputsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate, channels, streams, coupled = 48000, 2, 1, 1
	const packetFrameSize, plcFrameSize = 960, 5760
	mapping := []byte{0, 1}
	packet := encodeAPIRateCELTPacketFrameSizeVariant(t, channels, packetFrameSize, 128000, 107)
	if toc := ParseTOC(packet[0]); toc.Mode != ModeCELT {
		t.Fatalf("packet mode = %v, want CELT", toc.Mode)
	}
	packets := [][]byte{packet, nil, nil, packet}

	// The same C decoder handles a 20 ms packet, two requested 120 ms PLC
	// intervals, then recovery. Int24 exposes its opus_res samples, which also
	// provide exact expected values for the mixed int16 and fixed-build float
	// API calls below.
	want24, err := decodeLibopusMultistreamFixedInt24(sampleRate, channels, streams, coupled, plcFrameSize, mapping, packets)
	if err != nil {
		libopustest.HelperUnavailable(t, "multistream fixed long CELT PLC int24", err)
		return
	}
	packetSamples := packetFrameSize * channels
	plcSamples := plcFrameSize * channels
	wantTotal := packetSamples*2 + plcSamples*2
	if len(want24) != wantTotal {
		t.Fatalf("selected C decoded %d samples, want %d", len(want24), wantTotal)
	}

	dec, err := NewMultistreamDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatal(err)
	}
	packet16 := make([]int16, packetSamples)
	if n, err := dec.DecodeInt16(packet, packet16); err != nil || n != packetFrameSize {
		t.Fatalf("initial DecodeInt16 returned samples=%d, err=%v; want %d", n, err, packetFrameSize)
	}
	assertFixedExact(t, "mixed initial int16", int16ToInt32(packet16), convertResToInt16(want24[:packetSamples]))

	floatPLC := make([]float32, plcSamples)
	if n, err := dec.Decode(nil, floatPLC); err != nil || n != plcFrameSize {
		t.Fatalf("120 ms float PLC returned samples=%d, err=%v; want %d", n, err, plcFrameSize)
	}
	assertResAsFloat32(t, "120 ms float PLC", floatPLC, want24[packetSamples:packetSamples+plcSamples])

	int24PLC := make([]int32, plcSamples)
	if n, err := dec.DecodeInt24(nil, int24PLC); err != nil || n != plcFrameSize {
		t.Fatalf("120 ms int24 PLC returned samples=%d, err=%v; want %d", n, err, plcFrameSize)
	}
	int24Start := packetSamples + plcSamples
	assertFixedExact(t, "120 ms int24 PLC", int24PLC, want24[int24Start:int24Start+plcSamples])

	recovery := make([]int16, packetSamples)
	if n, err := dec.DecodeInt16(packet, recovery); err != nil || n != packetFrameSize {
		t.Fatalf("recovery DecodeInt16 returned samples=%d, err=%v; want %d", n, err, packetFrameSize)
	}
	assertFixedExact(t, "mixed recovery int16", int16ToInt32(recovery), convertResToInt16(want24[wantTotal-packetSamples:]))
}

func TestMultistreamFixedCELTPLCInvalidDurationDoesNotAdvance(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate, outputChannels, streams, coupled, frameSize = 48000, 2, 1, 1, 120
	mapping := []byte{0, 1}
	packet := buildMultistreamPacket(t, [][]byte{
		encodeAPIRateCELTPacketFrameSizeVariant(t, 1, frameSize, 96000, 239),
	})
	want24, err := decodeLibopusMultistreamFixedInt24(sampleRate, outputChannels, streams, coupled, frameSize, mapping, [][]byte{packet, nil})
	if err != nil {
		libopustest.HelperUnavailable(t, "fixed CELT invalid duration reference", err)
		return
	}
	dec, err := NewMultistreamDecoder(sampleRate, outputChannels, streams, coupled, mapping)
	if err != nil {
		t.Fatal(err)
	}
	seed := make([]int32, frameSize*outputChannels)
	if n, err := dec.DecodeInt24(packet, seed); err != nil || n != frameSize {
		t.Fatalf("seed DecodeInt24 returned samples=%d err=%v, want %d", n, err, frameSize)
	}
	if _, handled, err := dec.dec.DecodePLCToResFixed(frameSize + 1); err != nil || handled {
		t.Fatalf("invalid direct PLC preflight handled=%v err=%v, want unhandled without error", handled, err)
	}
	invalid := make([]int32, (frameSize+1)*outputChannels)
	if n, err := dec.DecodeInt24(nil, invalid); err == nil || n != 0 {
		t.Fatalf("invalid public PLC returned samples=%d err=%v, want frame-size error", n, err)
	}
	valid := make([]int32, frameSize*outputChannels)
	if n, err := dec.DecodeInt24(nil, valid); err != nil || n != frameSize {
		t.Fatalf("valid PLC after rejected request returned samples=%d err=%v, want %d", n, err, frameSize)
	}
	assertFixedExact(t, "PLC after invalid duration", valid, want24[frameSize*outputChannels:])
}

func TestMultistreamFixedCELTPLCValidDurationsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate, outputChannels, streams, coupled, seedFrameSize = 48000, 2, 1, 1, 120
	mapping := []byte{0, 1}
	packet := buildMultistreamPacket(t, [][]byte{
		encodeAPIRateCELTPacketFrameSizeVariant(t, 1, seedFrameSize, 96000, 211),
	})
	if toc := ParseTOC(packet[0]); toc.Mode != ModeCELT || toc.Stereo {
		t.Fatalf("seed packet mode/stereo=%v/%v, want mono-coded CELT", toc.Mode, toc.Stereo)
	}

	for lossFrameSize := sampleRate / 400; lossFrameSize <= sampleRate*3/25; lossFrameSize += sampleRate / 400 {
		t.Run(fmt.Sprintf("loss_%d_samples", lossFrameSize), func(t *testing.T) {
			packets := [][]byte{packet, nil}
			want24, err := decodeLibopusMultistreamFixedInt24(sampleRate, outputChannels, streams, coupled, lossFrameSize, mapping, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed CELT PLC duration matrix int24", err)
				return
			}
			seedSamples := seedFrameSize * outputChannels
			if len(want24) != seedSamples+lossFrameSize*outputChannels {
				t.Fatalf("selected C emitted %d samples, want seed+loss=%d", len(want24), seedSamples+lossFrameSize*outputChannels)
			}

			dec24, err := NewMultistreamDecoder(sampleRate, outputChannels, streams, coupled, mapping)
			if err != nil {
				t.Fatal(err)
			}
			out24 := make([]int32, lossFrameSize*outputChannels)
			if n, err := dec24.DecodeInt24(packet, out24); err != nil || n != seedFrameSize {
				t.Fatalf("seed DecodeInt24 returned samples=%d err=%v, want %d", n, err, seedFrameSize)
			}
			if n, err := dec24.DecodeInt24(nil, out24); err != nil || n != lossFrameSize {
				t.Fatalf("PLC DecodeInt24 returned samples=%d err=%v, want %d", n, err, lossFrameSize)
			}
			assertFixedExact(t, "valid-duration int24 PLC", out24, want24[seedSamples:])

			dec16, err := NewMultistreamDecoder(sampleRate, outputChannels, streams, coupled, mapping)
			if err != nil {
				t.Fatal(err)
			}
			out16 := make([]int16, lossFrameSize*outputChannels)
			if n, err := dec16.DecodeInt16(packet, out16); err != nil || n != seedFrameSize {
				t.Fatalf("seed DecodeInt16 returned samples=%d err=%v, want %d", n, err, seedFrameSize)
			}
			if n, err := dec16.DecodeInt16(nil, out16); err != nil || n != lossFrameSize {
				t.Fatalf("PLC DecodeInt16 returned samples=%d err=%v, want %d", n, err, lossFrameSize)
			}
			assertFixedExact(t, "valid-duration int16 PLC", int16ToInt32(out16), convertResToInt16(want24[seedSamples:]))
		})
	}
}

func TestMultistreamFixedCELTModeResetAndCodedChannelsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate, outputChannels, streams, coupled, frameSize = 48000, 2, 1, 1, 960
	mapping := []byte{0, 1}
	celtStart := buildMultistreamPacket(t, [][]byte{
		encodeAPIRateCELTPacketFrameSizeVariant(t, 2, frameSize, 128000, 223),
	})
	silk := buildMultistreamPacket(t, [][]byte{encodeAPIRateSILKPacketFrameSize(t, 2, frameSize)})
	celtRecovery := buildMultistreamPacket(t, [][]byte{
		encodeAPIRateCELTPacketFrameSizeVariant(t, 2, frameSize, 128000, 227),
	})
	packets := [][]byte{celtStart, silk, celtRecovery, celtRecovery}
	for i, packet := range packets {
		if len(packet) == 0 {
			t.Fatalf("packet %d is empty", i)
		}
	}
	if got := ParseTOC(celtStart[0]).Mode; got != ModeCELT {
		t.Fatalf("first packet mode=%v, want CELT", got)
	}
	if got := ParseTOC(silk[0]).Mode; got != ModeSILK {
		t.Fatalf("second packet mode=%v, want SILK", got)
	}
	if got := ParseTOC(celtRecovery[0]).Mode; got != ModeCELT {
		t.Fatalf("recovery packet mode=%v, want CELT", got)
	}

	for _, gainQ8 := range []int{0, 768, -768} {
		t.Run(fmt.Sprintf("gain_%d", gainQ8), func(t *testing.T) {
			want24, err := decodeLibopusMultistreamFixedInt24WithGain(sampleRate, outputChannels, streams, coupled, frameSize, gainQ8, mapping, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed CELT mode transition int24", err)
				return
			}
			dec, err := NewMultistreamDecoder(sampleRate, outputChannels, streams, coupled, mapping)
			if err != nil {
				t.Fatal(err)
			}
			if err := dec.SetGain(gainQ8); err != nil {
				t.Fatalf("SetGain(%d): %v", gainQ8, err)
			}
			got24 := make([]int32, 0, len(want24))
			frame := make([]int32, frameSize*outputChannels)
			for i, packet := range packets {
				if n, err := dec.DecodeInt24(packet, frame); err != nil || n != frameSize {
					t.Fatalf("packet %d DecodeInt24 returned samples=%d err=%v, want %d", i, n, err, frameSize)
				}
				got24 = append(got24, frame...)
			}
			assertFixedExact(t, "CELT/SILK/CELT mode reset", got24, want24)
		})
	}
}

func TestMultistreamFixedCELTAutoMonoCodedChannelsWithPLCMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate, outputChannels, streams, coupled, frameSize = 48000, 2, 1, 1, 960
	mapping := []byte{0, 1}
	stereoPacket := buildMultistreamPacket(t, [][]byte{
		encodeAPIRateCELTPacketFrameSizeVariant(t, 2, frameSize, 128000, 229),
	})
	autoMonoPacket := buildMultistreamPacket(t, [][]byte{encodeAutoMonoCELTPacket(t, frameSize, 233)})
	if toc := ParseTOC(autoMonoPacket[0]); toc.Mode != ModeCELT || toc.Stereo {
		t.Fatalf("low-rate stereo-input packet TOC mode/stereo=%v/%v, want mono-coded CELT", toc.Mode, toc.Stereo)
	}
	packets := [][]byte{stereoPacket, autoMonoPacket, nil, autoMonoPacket}
	want24, err := decodeLibopusMultistreamFixedInt24WithPhase(sampleRate, outputChannels, streams, coupled, frameSize, true, mapping, packets)
	if err != nil {
		libopustest.HelperUnavailable(t, "fixed auto-mono CELT and PLC reference", err)
		return
	}
	dec, err := NewMultistreamDecoder(sampleRate, outputChannels, streams, coupled, mapping)
	if err != nil {
		t.Fatal(err)
	}
	dec.SetPhaseInversionDisabled(true)
	got24 := make([]int32, 0, len(want24))
	frame := make([]int32, frameSize*outputChannels)
	for i, packet := range packets {
		if n, err := dec.DecodeInt24(packet, frame); err != nil || n != frameSize {
			t.Fatalf("packet %d DecodeInt24 returned samples=%d err=%v, want %d", i, n, err, frameSize)
		}
		got24 = append(got24, frame...)
	}
	assertFixedExact(t, "stereo input auto-mono CELT with PLC/recovery", got24, want24)
}

func encodeAutoMonoCELTPacket(t *testing.T, frameSize, variant int) []byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{SampleRate: sampleRate, Channels: 2, Application: ApplicationRestrictedCelt})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(6000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	pcm := make([]float32, frameSize*2)
	for i := 0; i < frameSize; i++ {
		tm := float64(variant*frameSize+i) / sampleRate
		pcm[i*2] = 0.23*float32(math.Sin(2*math.Pi*431*tm)) + 0.07*float32(math.Sin(2*math.Pi*1973*tm))
		pcm[i*2+1] = 0.17*float32(math.Sin(2*math.Pi*683*tm+0.2)) + 0.05*float32(math.Sin(2*math.Pi*2381*tm+0.1))
	}
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("EncodeFloat32: %v", err)
	}
	return packet
}

func convertResToInt16(res []int32) []int32 {
	pcm := make([]int32, len(res))
	for i, sample := range res {
		pcm[i] = int32(fixedpoint.Res2Int16(sample))
	}
	return pcm
}

func assertResAsFloat32(t *testing.T, name string, got []float32, res []int32) {
	t.Helper()
	if len(got) != len(res) {
		t.Fatalf("%s: got %d float samples, want %d", name, len(got), len(res))
	}
	const resToFloat = float32(1.0 / 8388608.0)
	for i := range got {
		want := float32(res[i]) * resToFloat
		if got[i] != want {
			t.Fatalf("%s sample %d: got %08x, want %08x", name, i, got[i], want)
		}
	}
}
