//go:build gopus_fixedpoint

package gopus_test

import (
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/internal/libopustest"
)

// celtFixedTestSignal builds a deterministic float32 [-1,1) signal mixing tones
// with noise so the CELT encoder exercises real band shapes.
func celtFixedTestSignal(n int, seed int64, transient bool) []float32 {
	rng := rand.New(rand.NewSource(seed))
	out := make([]float32, n)
	for i := range out {
		t := float64(i)
		v := 0.30*math.Sin(2*math.Pi*440.0*t/48000.0) +
			0.15*math.Sin(2*math.Pi*1200.0*t/48000.0) +
			0.05*(rng.Float64()*2-1)
		if transient && i >= n/2 && i < n/2+80 {
			v += 0.6
		}
		out[i] = float32(v)
	}
	return out
}

// celtFixedFullbandTOC builds the CELT-only fullband TOC byte for a given 48 kHz
// per-channel frame size. Configs 28..31 are CELT fullband at 2.5/5/10/20 ms.
func celtFixedFullbandTOC(frameSize48 int, stereo bool) (byte, bool) {
	var config int
	switch frameSize48 {
	case 120:
		config = 28
	case 240:
		config = 29
	case 480:
		config = 30
	case 960:
		config = 31
	default:
		return 0, false
	}
	toc := byte(config<<3) | 0x00 // code 0 (single frame)
	if stereo {
		toc |= 0x04
	}
	return toc, true
}

// TestDecoderFixedPointCELTParity validates that, under -tags gopus_fixedpoint,
// the public Decoder.DecodeInt16 and Decoder.DecodeInt24 of CELT-only packets
// are bit-exact with the libopus FIXED_POINT celt_decode_with_ec sequence
// decode (one decoder, real cross-frame state). The public int16/int24 paths
// route CELT-only frames directly to the integer internal/fixedpoint.CELTDecoder
// output -- no float32 round-trip -- so they reproduce libopus' RES2INT16 /
// RES2INT24 conversions exactly:
//
//	int16 sample = SAT16(PSHR32(opus_res, 8))   (RES2INT16)
//	int24 sample = opus_res                       (RES2INT24, ENABLE_RES24)
//
// The libopus reference is the int16 sequence; the int24 output is checked for
// the same RES2INT16 relationship (SAT16(PSHR32(int24, 8)) == int16 reference),
// which pins the int24 path to libopus' int24 semantics.
func TestDecoderFixedPointCELTParity(t *testing.T) {
	libopustest.RequireOracle(t)

	type tc struct {
		name        string
		channels    int
		frameSize48 int
		bitrate     int
		transient   bool
	}
	cases := []tc{
		{"mono_960_64k", 1, 960, 64000, false},
		{"mono_960_96k", 1, 960, 96000, false},
		{"mono_960_32k", 1, 960, 32000, false},
		{"mono_transient_960_96k", 1, 960, 96000, true},
		{"mono_480_64k", 1, 480, 64000, false},
		{"mono_240_48k", 1, 240, 48000, false},
		{"stereo_960_128k", 2, 960, 128000, false},
		{"stereo_transient_960_128k", 2, 960, 128000, true},
		{"stereo_480_96k", 2, 480, 96000, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			channels := c.channels
			enc := celt.NewEncoder(channels)
			enc.SetBitrate(c.bitrate)

			toc, ok := celtFixedFullbandTOC(c.frameSize48, channels == 2)
			if !ok {
				t.Fatalf("no TOC for frame size %d", c.frameSize48)
			}

			const frames = 6
			var celtPayloads [][]byte
			var opusPackets [][]byte
			for frame := 0; frame < frames; frame++ {
				transient := c.transient && frame%2 == 1
				mono := celtFixedTestSignal(c.frameSize48, int64(0x5e1d+frame*7)+int64(c.frameSize48), transient)
				pcm := make([]float32, c.frameSize48*channels)
				for i := 0; i < c.frameSize48; i++ {
					pcm[i*channels] = mono[i]
					if channels == 2 {
						pcm[i*channels+1] = mono[i] * 0.8
					}
				}
				payload, err := enc.EncodeFrame(pcm, c.frameSize48)
				if err != nil {
					t.Fatalf("frame %d: encode: %v", frame, err)
				}
				if len(payload) <= 1 {
					continue // degenerate/silence packet uses the PLC path (out of scope)
				}
				// EncodeFrame returns a slice aliasing the encoder's reused range
				// coder buffer, so copy it before the next frame overwrites it.
				payloadCopy := append([]byte(nil), payload...)
				celtPayloads = append(celtPayloads, payloadCopy)
				pkt := make([]byte, len(payloadCopy)+1)
				pkt[0] = toc
				copy(pkt[1:], payloadCopy)
				opusPackets = append(opusPackets, pkt)
			}
			if len(celtPayloads) == 0 {
				t.Fatalf("no packets produced")
			}

			// libopus FIXED_POINT reference: decode the CELT payloads through one
			// fixed-point CELT decoder (48 kHz output, fullband).
			ref, err := libopustest.ProbeCELTFixedDecodeSeq(celtPayloads, channels, c.frameSize48, 0, 21, 48000)
			if err != nil {
				libopustest.HelperUnavailable(t, "celt fixed decode seq", err)
				return
			}

			dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("new decoder: %v", err)
			}

			for p, pkt := range opusPackets {
				out := make([]int16, c.frameSize48*channels)
				n, err := dec.DecodeInt16(pkt, out)
				if err != nil {
					t.Fatalf("packet %d: DecodeInt16: %v", p, err)
				}
				if n != c.frameSize48 {
					t.Fatalf("packet %d: decoded %d samples, want %d", p, n, c.frameSize48)
				}
				want := ref[p]
				for i := 0; i < c.frameSize48*channels; i++ {
					if out[i] != want[i] {
						t.Fatalf("packet %d sample %d: gopus=%d libopus=%d", p, i, out[i], want[i])
					}
				}
			}

			// int24: a fresh decoder (independent cross-frame state) decodes the
			// same packets. Each int24 sample is the opus_res value, so applying
			// RES2INT16 (SAT16(PSHR32(a, 8))) must reproduce the libopus int16
			// reference exactly.
			dec24, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("new int24 decoder: %v", err)
			}
			for p, pkt := range opusPackets {
				out := make([]int32, c.frameSize48*channels)
				n, err := dec24.DecodeInt24(pkt, out)
				if err != nil {
					t.Fatalf("packet %d: DecodeInt24: %v", p, err)
				}
				if n != c.frameSize48 {
					t.Fatalf("packet %d: DecodeInt24 decoded %d samples, want %d", p, n, c.frameSize48)
				}
				want := ref[p]
				for i := 0; i < c.frameSize48*channels; i++ {
					got16 := res2int16FromInt24(out[i])
					if got16 != want[i] {
						t.Fatalf("packet %d sample %d: RES2INT16(int24)=%d (int24=%d) libopus int16=%d",
							p, i, got16, out[i], want[i])
					}
				}
			}
		})
	}
}

// res2int16FromInt24 mirrors libopus RES2INT16(a) = SAT16(PSHR32(a, 8)) for the
// ENABLE_RES24 build, applied to an int24 (opus_res) sample. PSHR32 rounds:
// (a + (1<<7)) >> 8.
func res2int16FromInt24(a int32) int16 {
	v := (a + (1 << 7)) >> 8
	if v > 32767 {
		return 32767
	}
	if v < -32768 {
		return -32768
	}
	return int16(v)
}
