//go:build gopus_fixedpoint

package encoder

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/types"
)

// TestPublicCELTEncodeFixedRateByteExact drives the PUBLIC Encoder API in
// CELT-only mode at the sub-48 kHz API sample rates (24000/16000/12000/8000 Hz)
// under the gopus_fixedpoint build and asserts the produced packet payload is
// byte-for-byte identical to the FIXED_POINT celt_encode_with_ec reference run
// (a CELT encoder created at the same API rate, so st->upsample matches). It
// exercises mono and stereo, the API-rate frame sizes that upsample to a valid
// 48 kHz core block (2.5/5/10/20 ms), a spread of bitrates and complexities, and
// CBR/CVBR/VBR rate control through the real public dispatch (sample conversion,
// bandwidth clamping/end-band selection, routing and Opus packet assembly).
func TestPublicCELTEncodeFixedRateByteExact(t *testing.T) {
	libopustest.RequireOracle(t)

	next := func(state *uint32) uint32 {
		s := *state
		s ^= s << 13
		s ^= s >> 17
		s ^= s << 5
		*state = s
		return s
	}

	// upsample factor for each API rate (resampling_factor).
	upsampleFor := map[int]int{24000: 2, 16000: 3, 12000: 4, 8000: 6}

	type kase struct {
		rate       int
		frameMS    int // 2.5ms encoded as 0 (handled below); use ms*10 to keep ints
		channels   int
		bitrate    int
		complexity int
		mode       BitrateMode
		transient  bool
	}

	// Frame durations expressed in tenths of a millisecond so 2.5 ms is exact.
	durTenthMS := []int{25, 50, 100, 200}

	var cases []kase
	for _, rate := range []int{24000, 16000, 12000, 8000} {
		for _, ch := range []int{1, 2} {
			for _, dur := range durTenthMS {
				for _, br := range []int{24000, 48000, 96000} {
					for _, cx := range []int{0, 5, 10} {
						for _, m := range []BitrateMode{ModeCBR, ModeCVBR, ModeVBR} {
							for _, tr := range []bool{false, true} {
								cases = append(cases, kase{
									rate: rate, frameMS: dur, channels: ch,
									bitrate: br, complexity: cx, mode: m, transient: tr,
								})
							}
						}
					}
				}
			}
		}
	}

	for _, c := range cases {
		c := c
		// API-rate frame size: rate * (dur/10) / 1000 == rate*dur/10000.
		frameSize := c.rate * c.frameMS / 10000
		if frameSize <= 0 {
			continue
		}
		// Skip frame sizes that don't upsample to a valid 48 kHz core block.
		core := frameSize * upsampleFor[c.rate]
		if core != 120 && core != 240 && core != 480 && core != 960 {
			continue
		}
		name := fmt.Sprintf("fs%d/ch%d/dur%d/br%d/cx%d/%v/tr=%v",
			c.rate, c.channels, c.frameMS, c.bitrate, c.complexity, c.mode, c.transient)
		t.Run(name, func(t *testing.T) {
			state := uint32(0xBADF00D + c.rate + c.frameMS*131 + c.bitrate + c.complexity*7 + int(c.mode)*97)
			if c.transient {
				state ^= 0x33333333
			}
			pcm := make([]float32, c.channels*frameSize)
			for i := range pcm {
				v := int32(next(&state))
				s := float32(v>>16) / 32768.0 * 0.25
				if c.transient && i > len(pcm)/2 && i < len(pcm)/2+8 {
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

			enc := NewEncoder(c.rate, c.channels)
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
				t.Fatalf("frame was not routed through the integer CELT encoder (rate=%d frameSize=%d bw=%v)",
					c.rate, frameSize, enc.effectiveBandwidth())
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
			end := celtFixedEndBand(enc.effectiveBandwidth())
			nbCompressedBytes := celtPacketSizeCap - 1

			want, err := libopustest.ProbeCELTFixedEncodeRate(pcm16, c.channels, frameSize, 0, end,
				c.bitrate, c.complexity, nbCompressedBytes, c.rate, vbr, cvbr, false, nil)
			if err != nil {
				libopustest.HelperUnavailable(t, "celt fixed encode rate", err)
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
				t.Fatalf("public CELT packet mismatch (rate=%d frameSize=%d end=%d): got %d bytes, want %d bytes, first diff at %d\n got=% x\nwant=% x",
					c.rate, frameSize, end, len(got), len(want), diff, got, want)
			}
		})
	}
}
