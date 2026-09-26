//go:build gopus_fixed_point

package gopus

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func xorshift32Pub(state *uint32) uint32 {
	s := *state
	s ^= s << 13
	s ^= s >> 17
	s ^= s << 5
	*state = s
	return s
}

// genPCMInt16Pub builds nframes of interleaved int16 PCM with periodic transient
// spikes, matching the fixedpoint oracle generator's character.
func genPCMInt16Pub(seed uint32, channels, frameSize, nframes int, transient bool) []int16 {
	state := seed
	pcm := make([]int16, channels*frameSize*nframes)
	perFrame := channels * frameSize
	for f := 0; f < nframes; f++ {
		for i := 0; i < perFrame; i++ {
			v := int32(xorshift32Pub(&state))
			s := int16(v >> 19)
			if transient && f%2 == 1 && i > perFrame/2 && i < perFrame/2+40 {
				s = int16(v >> 12)
			}
			pcm[f*perFrame+i] = s
		}
	}
	return pcm
}

// TestPublicEncodeFixedCELTLibopusParity validates the gopus_fixed_point public
// encode seam: a CELT-mode (ApplicationRestrictedCelt, full-band 48 kHz) frame
// routed through Encoder.Encode produces a byte-exact CELT packet versus the
// FIXED_POINT libopus celt_encode_with_ec reference.
//
// The public path applies opus_encoder-level dc_reject + LSB quantization before
// the integer CELT encoder, which the bare celt_encode_with_ec reference does
// not. To compare against libopus the test feeds the reference the exact int16
// frame the integer encoder consumed (captured via LastFixedCELTInput16), so both
// encoders operate on identical samples; only the gopus plumbing (config mapping,
// VBR/CBR routing, TOC/packet wrapping) is under test here.
func TestPublicEncodeFixedCELTLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		start   = 0
		end     = 21
		nframes = 5
	)

	type kase struct {
		channels   int
		lm         int
		bitrate    int
		complexity int
		mode       BitrateMode
		transient  bool
	}
	var cases []kase
	for _, ch := range []int{1, 2} {
		for _, lm := range []int{0, 2, 3} { // 120, 480, 960 samples
			for _, br := range []int{32000, 64000, 128000} {
				for _, cx := range []int{0, 5, 10} {
					for _, m := range []BitrateMode{BitrateModeVBR, BitrateModeCBR, BitrateModeCVBR} {
						cases = append(cases, kase{channels: ch, lm: lm, bitrate: br,
							complexity: cx, mode: m, transient: lm == 3})
					}
				}
			}
		}
	}

	for _, c := range cases {
		frameSize := 120 << c.lm
		seed := uint32(0x51EED + c.lm*131 + c.bitrate + c.complexity*7 + c.channels*1009 + int(c.mode)*97)
		if c.transient {
			seed ^= 0x33333333
		}
		pcm16 := genPCMInt16Pub(seed, c.channels, frameSize, nframes, c.transient)

		enc, err := NewEncoder(EncoderConfig{
			SampleRate:  48000,
			Channels:    c.channels,
			Application: ApplicationRestrictedCelt,
		})
		if err != nil {
			t.Fatalf("NewEncoder: %v", err)
		}
		if err := enc.SetComplexity(c.complexity); err != nil {
			t.Fatalf("SetComplexity: %v", err)
		}
		if err := enc.SetBitrate(c.bitrate); err != nil {
			t.Fatalf("SetBitrate: %v", err)
		}
		if err := enc.SetBitrateMode(c.mode); err != nil {
			t.Fatalf("SetBitrateMode: %v", err)
		}
		if err := enc.SetFrameSize(frameSize); err != nil {
			t.Fatalf("SetFrameSize: %v", err)
		}
		// The standalone C CELT reference fixes its coded channels and end
		// band; use the same public controls before capturing the inner input.
		if err := enc.SetForceChannels(c.channels); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
		if err := enc.SetBandwidth(BandwidthFullband); err != nil {
			t.Fatalf("SetBandwidth: %v", err)
		}

		// Drive every frame through the public encoder, capturing the int16 the
		// integer CELT encoder consumed and the CELT payload (packet minus TOC).
		perFrame := c.channels * frameSize
		fed := make([]int16, 0, perFrame*nframes)
		payloads := make([][]byte, nframes)
		f32 := make([]float32, perFrame)
		out := make([]byte, 4000)
		for f := 0; f < nframes; f++ {
			for i := 0; i < perFrame; i++ {
				f32[i] = float32(pcm16[f*perFrame+i]) / 32768.0
			}
			n, err := enc.Encode(f32, out)
			if err != nil {
				t.Fatalf("Encode frame %d: %v", f, err)
			}
			in16 := enc.enc.LastFixedCELTInput16()
			if len(in16) != perFrame {
				t.Fatalf("ch=%d lm=%d frame=%d: integer CELT path not taken (LastFixedCELTInput16 len=%d want %d)",
					c.channels, c.lm, f, len(in16), perFrame)
			}
			fed = append(fed, in16...)
			if n < 1 {
				t.Fatalf("Encode frame %d: short packet %d", f, n)
			}
			payloads[f] = append([]byte(nil), out[1:n]...)
		}

		// The public CBR packet reserves its TOC byte before calling CELT.
		// opus_encoder.c computes (bitrate_to_bits(bitrate, Fs, frameSize)+4)/8
		// packet bytes; the standalone CELT oracle receives the remaining bytes.
		// VBR/CVBR use the full CELT output cap.
		maxBytes := 1275
		if c.mode == BitrateModeCBR {
			maxBytes = (c.bitrate*frameSize/48000+4)/8 - 1
		}
		vbr := c.mode != BitrateModeCBR
		constrained := c.mode == BitrateModeCVBR

		want, err := libopustest.ProbeCELTFixedEncodeSeq(fed, c.channels, frameSize, start, end,
			c.bitrate, c.complexity, vbr, constrained, maxBytes, nframes)
		if err != nil {
			libopustest.HelperUnavailable(t, "celt fixed encode seq", err)
			return
		}

		for f := 0; f < nframes; f++ {
			label := fmt.Sprintf("ch=%d lm=%d br=%d cx=%d mode=%v frame=%d",
				c.channels, c.lm, c.bitrate, c.complexity, c.mode, f)
			got := payloads[f]
			if !bytes.Equal(got, want[f]) {
				mismatch := 0
				for mismatch < min(len(got), len(want[f])) && got[mismatch] == want[f][mismatch] {
					mismatch++
				}
				t.Errorf("%s: CELT payload mismatch at byte %d (gopus len=%d libopus len=%d)",
					label, mismatch, len(got), len(want[f]))
				break
			}
		}
	}
}
