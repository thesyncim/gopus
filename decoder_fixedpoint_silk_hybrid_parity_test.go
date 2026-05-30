//go:build gopus_fixedpoint

package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

var fixedRefdecodeHelper libopustest.HelperCache

// getFixedRefdecodeHelperPath builds the libopus_refdecode_single.c full-pipeline
// opus_decode / opus_decode24 helper against the FIXED_POINT reference tree
// (--enable-fixed-point, ENABLE_RES24), so the int16/int24 output is the libopus
// FIXED_POINT opus_decode result rather than the float build.
func getFixedRefdecodeHelperPath() (string, error) {
	return fixedRefdecodeHelper.CHelperPath(libopustest.CHelperConfig{
		Label:       "fixed-point reference decode",
		OutputBase:  "gopus_libopus_refdecode_fixed",
		SourceFile:  "libopus_refdecode_single.c",
		FixedRef:    true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{libopustest.FixedRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

// decodeWithLibopusFixedInt16 drives FIXED_POINT opus_decode() for a sequence of
// packets through the refdecode_single helper.
func decodeWithLibopusFixedInt16(sampleRate, channels, frameSize int, packets [][]byte) ([]int16, error) {
	binPath, err := getFixedRefdecodeHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayloadVersion("GOSI", 5,
		libopusRefdecodeSingleFormatInt16, uint32(sampleRate), 0,
		uint32(channels), uint32(frameSize), uint32(len(packets)))
	for _, pkt := range packets {
		payload.U32(0) // decode_fec
		payload.U32(uint32(len(pkt)))
		payload.Raw(pkt)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "fixed reference decode int16", "GOSO")
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 2)
	decoded := make([]int16, nSamples)
	for i := range decoded {
		decoded[i] = reader.I16()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return decoded, nil
}

// decodeWithLibopusFixedInt24 drives FIXED_POINT opus_decode24() for a sequence
// of packets through the refdecode_single helper.
func decodeWithLibopusFixedInt24(sampleRate, channels, frameSize int, packets [][]byte) ([]int32, error) {
	binPath, err := getFixedRefdecodeHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayloadVersion("GOSI", 5,
		libopusRefdecodeSingleFormatInt24, uint32(sampleRate), 0,
		uint32(channels), uint32(frameSize), uint32(len(packets)))
	for _, pkt := range packets {
		payload.U32(0) // decode_fec
		payload.U32(uint32(len(pkt)))
		payload.Raw(pkt)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "fixed reference decode int24", "GOSO")
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

// encodeFixedSILKSequence encodes a multi-frame SILK-only stream at the given
// bandwidth so the decode exercises cross-frame SILK + resampler state.
func encodeFixedSILKSequence(t *testing.T, channels, frameSize, frames int, bw Bandwidth) [][]byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationRestrictedSilk,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(bw); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(24000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetInBandFEC(InBandFECDisabled); err != nil {
		t.Fatalf("SetInBandFEC: %v", err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}

	var packets [][]byte
	phase := 0.0
	for f := 0; f < frames; f++ {
		pcm := make([]float32, frameSize*channels)
		for i := 0; i < frameSize; i++ {
			tm := (phase + float64(i)) / sampleRate
			pcm[i*channels] = 0.22 * float32(math.Sin(2*math.Pi*440*tm))
			if channels == 2 {
				pcm[i*channels+1] = 0.18 * float32(math.Sin(2*math.Pi*660*tm))
			}
		}
		phase += float64(frameSize)
		pkt, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("frame %d Encode: %v", f, err)
		}
		packets = append(packets, append([]byte(nil), pkt...))
	}
	return packets
}

// TestDecoderFixedPointSILKParity gates that the public DecodeInt16 / DecodeInt24
// of SILK-only packets is bit-exact with the libopus FIXED_POINT opus_decode /
// opus_decode24 reference. gopus' silk.Decoder is inherently integer (int16
// native samples + the int16 silk_resampler), and the int16/int24 output of a
// SILK-only frame round-trips through float32 without loss, so the existing
// public path is already FIXED_POINT-exact (subject to the documented per-arch
// 1-ULP budget).
func TestDecoderFixedPointSILKParity(t *testing.T) {
	libopustest.RequireOracle(t)

	type tc struct {
		name      string
		channels  int
		frameSize int
		bw        Bandwidth
	}
	cases := []tc{
		{"mono_wb_960", 1, 960, BandwidthWideband},
		{"stereo_wb_960", 2, 960, BandwidthWideband},
		{"mono_wb_480", 1, 480, BandwidthWideband},
		{"mono_mb_960", 1, 960, BandwidthMediumband},
		{"mono_nb_960", 1, 960, BandwidthNarrowband},
	}

	const sampleRate = 48000
	const frames = 5
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			packets := encodeFixedSILKSequence(t, c.channels, c.frameSize, frames, c.bw)
			for _, pkt := range packets {
				if toc := ParseTOC(pkt[0]); toc.Mode != ModeSILK {
					t.Skipf("encoder produced mode %v, want SILK", toc.Mode)
				}
			}

			refInt16, err := decodeWithLibopusFixedInt16(sampleRate, c.channels, c.frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int16", err)
				return
			}
			refInt24, err := decodeWithLibopusFixedInt24(sampleRate, c.channels, c.frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int24", err)
				return
			}

			dec16, err := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
			if err != nil {
				t.Fatalf("NewDecoder int16: %v", err)
			}
			dec24, err := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
			if err != nil {
				t.Fatalf("NewDecoder int24: %v", err)
			}

			var got16 []int32
			var got24 []int32
			for p, pkt := range packets {
				o16 := make([]int16, c.frameSize*c.channels)
				if _, err := dec16.DecodeInt16(pkt, o16); err != nil {
					t.Fatalf("packet %d DecodeInt16: %v", p, err)
				}
				got16 = append(got16, int16ToInt32(o16)...)
				o24 := make([]int32, c.frameSize*c.channels)
				if _, err := dec24.DecodeInt24(pkt, o24); err != nil {
					t.Fatalf("packet %d DecodeInt24: %v", p, err)
				}
				got24 = append(got24, o24...)
			}

			assertFixedExact(t, "int16", got16, int16ToInt32(refInt16))
			assertFixedExact(t, "int24", got24, refInt24)
		})
	}
}

// TestDecoderFixedPointHybridParity gates that the public DecodeInt16 /
// DecodeInt24 of Hybrid packets is bit-exact with the libopus FIXED_POINT
// opus_decode / opus_decode24 reference. The integer SILK lowband
// (INT16TORES of the resampled int16 SILK output) is combined with the integer
// FIXED_POINT CELT highband (bands 17-21) via internal/fixedpoint.CELTDecoder
// driven from the shared range decoder with celt_accum, mirroring
// opus_decode_frame (start_band=17, celt_accum=1). Multi-frame packets exercise
// the cross-frame integer CELT state (decode_mem, energy histories, post-filter,
// preemph). Bit-exact on amd64; subject to the documented per-arch 1-ULP CELT
// drift budget on arm64.
func TestDecoderFixedPointHybridParity(t *testing.T) {
	libopustest.RequireOracle(t)

	type tc struct {
		name      string
		channels  int
		frameSize int
		frames    int
	}
	cases := []tc{
		{"mono_960", 1, 960, 1},
		{"stereo_960", 2, 960, 1},
		{"mono_480", 1, 480, 1},
		{"mono_960_multi", 1, 960, 4},
		{"stereo_960_multi", 2, 960, 4},
		{"mono_480_multi", 1, 480, 6},
	}

	const sampleRate = 48000
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			packets := make([][]byte, 0, c.frames)
			for f := 0; f < c.frames; f++ {
				pkt := encodeAPIRateHybridPacketFrameSize(t, c.channels, c.frameSize)
				if toc := ParseTOC(pkt[0]); toc.Mode != ModeHybrid {
					t.Skipf("encoder produced mode %v, want Hybrid", toc.Mode)
				}
				packets = append(packets, pkt)
			}

			refInt16, err := decodeWithLibopusFixedInt16(sampleRate, c.channels, c.frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int16", err)
				return
			}
			refInt24, err := decodeWithLibopusFixedInt24(sampleRate, c.channels, c.frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int24", err)
				return
			}

			dec16, _ := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
			dec24, _ := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
			var got16, got24 []int32
			for p, pkt := range packets {
				o16 := make([]int16, c.frameSize*c.channels)
				if _, err := dec16.DecodeInt16(pkt, o16); err != nil {
					t.Fatalf("packet %d DecodeInt16: %v", p, err)
				}
				got16 = append(got16, int16ToInt32(o16)...)
				o24 := make([]int32, c.frameSize*c.channels)
				if _, err := dec24.DecodeInt24(pkt, o24); err != nil {
					t.Fatalf("packet %d DecodeInt24: %v", p, err)
				}
				got24 = append(got24, o24...)
			}

			assertFixedExact(t, "int16", got16, int16ToInt32(refInt16))
			assertFixedExact(t, "int24", got24, refInt24)
		})
	}
}

func int16ToInt32(in []int16) []int32 {
	out := make([]int32, len(in))
	for i, v := range in {
		out[i] = int32(v)
	}
	return out
}

func divergence(got, want []int32) (diffs int, maxAbs int64, firstIdx int) {
	firstIdx = -1
	for i := range got {
		d := int64(got[i]) - int64(want[i])
		if d < 0 {
			d = -d
		}
		if d != 0 {
			diffs++
			if firstIdx < 0 {
				firstIdx = i
			}
		}
		if d > maxAbs {
			maxAbs = d
		}
	}
	return
}

// assertFixedExact requires bit-exact equality on amd64 and tolerates the
// documented darwin/arm64 1-ULP CELT drift budget on arm64.
func assertFixedExact(t *testing.T, label string, got, want []int32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: length got=%d want=%d", label, len(got), len(want))
	}
	diffs, maxAbs, firstIdx := divergence(got, want)
	if diffs == 0 {
		t.Logf("%s: bit-exact (%d samples)", label, len(got))
		return
	}
	t.Errorf("%s: %d/%d samples differ, maxAbs=%d, first at %d: gopus=%d libopus=%d",
		label, diffs, len(got), maxAbs, firstIdx, got[firstIdx], want[firstIdx])
}

