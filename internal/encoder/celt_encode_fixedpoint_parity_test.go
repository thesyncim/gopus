//go:build gopus_fixedpoint

package encoder

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/types"
)

// TestPublicCELTEncodeFixedByteExact drives the PUBLIC Encoder API in CELT-only
// mode under the gopus_fixedpoint build and asserts the produced packet payload
// is byte-for-byte identical to the FIXED_POINT celt_encode_with_ec reference run
// on the exact int16 PCM the integer encoder consumed. It exercises mono and
// stereo, every CELT frame size (LM 0..3), a spread of bitrates and complexities,
// and CBR/CVBR/VBR rate control through the real public dispatch (sample
// conversion + routing + Opus packet assembly).
func TestPublicCELTEncodeFixedByteExact(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		shortMdctSize = 120
		start         = 0
		end           = 21
	)

	next := func(state *uint32) uint32 {
		s := *state
		s ^= s << 13
		s ^= s >> 17
		s ^= s << 5
		*state = s
		return s
	}

	type kase struct {
		lm         int
		channels   int
		bitrate    int
		complexity int
		mode       BitrateMode
		transient  bool
	}
	var cases []kase
	for _, ch := range []int{1, 2} {
		for _, lm := range []int{0, 1, 2, 3} {
			for _, br := range []int{32000, 64000, 96000, 256000} {
				for _, cx := range []int{0, 5, 10} {
					for _, m := range []BitrateMode{ModeCBR, ModeCVBR, ModeVBR} {
						for _, tr := range []bool{false, true} {
							cases = append(cases, kase{lm: lm, channels: ch, bitrate: br, complexity: cx, mode: m, transient: tr})
						}
					}
				}
			}
		}
	}

	for _, c := range cases {
		c := c
		name := fmt.Sprintf("ch%d/lm%d/br%d/cx%d/%v/tr=%v", c.channels, c.lm, c.bitrate, c.complexity, c.mode, c.transient)
		t.Run(name, func(t *testing.T) {
			frameSize := shortMdctSize << c.lm

			// Build float input in [-1,1). The public API quantizes to int16 via
			// FLOAT2INT16 before handing the frame to the integer encoder, so we
			// validate against the exact int16 captured by LastFixedCELTInput16.
			state := uint32(0xC0FFEE + c.lm*131 + c.bitrate + c.complexity*7 + int(c.mode)*97)
			if c.transient {
				state ^= 0x33333333
			}
			pcm := make([]float32, c.channels*frameSize)
			for i := range pcm {
				v := int32(next(&state))
				s := float32(v>>16) / 32768.0 * 0.25
				if c.transient && i > len(pcm)/2 && i < len(pcm)/2+40 {
					s = float32(v>>16) / 32768.0
				}
				if s >= 1 {
					s = 0.9999
				}
				if s < -1 {
					s = -1
				}
				pcm[i] = s
			}

			enc := NewEncoder(48000, c.channels)
			enc.SetMode(ModeCELT)
			enc.SetLowDelay(true)
			enc.SetBandwidth(types.BandwidthFullband)
			enc.SetComplexity(c.complexity)
			enc.SetBitrate(c.bitrate)
			enc.SetBitrateMode(c.mode)

			packet, err := enc.Encode(pcm, frameSize)
			if err != nil {
				t.Fatalf("public Encode: %v", err)
			}
			if !enc.fixedCELTUsed {
				t.Fatalf("frame was not routed through the integer CELT encoder")
			}
			if len(packet) < 1 {
				t.Fatalf("empty packet")
			}
			got := packet[1:] // strip TOC byte; single un-padded CELT frame

			consumed := enc.LastFixedCELTInput16()
			if len(consumed) != c.channels*frameSize {
				t.Fatalf("LastFixedCELTInput16 len=%d want %d", len(consumed), c.channels*frameSize)
			}
			pcm16 := append([]int16(nil), consumed...)

			vbr := c.mode != ModeCBR
			cvbr := c.mode == ModeCVBR
			// The public single-frame path passes maxPayloadBytes==0, so the
			// integer encoder uses celtPacketSizeCap-1 as the output buffer cap.
			nbCompressedBytes := celtPacketSizeCap - 1

			want, err := libopustest.ProbeCELTFixedEncodeExt(pcm16, c.channels, frameSize, start, end,
				c.bitrate, c.complexity, nbCompressedBytes, vbr, cvbr, false, nil)
			if err != nil {
				libopustest.HelperUnavailable(t, "celt fixed encode ext", err)
				return
			}

			if !bytes.Equal(got, want) {
				n := len(got)
				if len(want) < n {
					n = len(want)
				}
				diff := -1
				for i := 0; i < n; i++ {
					if got[i] != want[i] {
						diff = i
						break
					}
				}
				t.Fatalf("public CELT packet mismatch: got %d bytes, want %d bytes, first diff at %d\n got=% x\nwant=% x",
					len(got), len(want), diff, got, want)
			}
		})
	}
}
