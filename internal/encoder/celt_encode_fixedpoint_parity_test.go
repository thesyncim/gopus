//go:build gopus_fixed_point

package encoder

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/types"
)

// TestPublicCELTEncodeFixedByteExact drives the PUBLIC Encoder API in CELT-only
// mode under the gopus_fixed_point build and asserts the produced packet payload
// is byte-for-byte identical to selected FIXED_POINT celt_encode_with_ec on the
// exact opus_res Q8 PCM and AnalysisInfo the integer encoder consumed. It exercises mono and
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

			// Build float input in [-1,1). The public fixed encoder retains its
			// Q8 fractional samples through the Opus delay and CELT front end.
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

			frame := fixedQ8OracleFrame(enc)
			if len(frame.PCM) != c.channels*frameSize {
				t.Fatalf("LastFixedCELTInputQ8 len=%d want %d", len(frame.PCM), c.channels*frameSize)
			}
			bitrate, _, lsbDepth := enc.LastFixedCELTControls()

			vbr := c.mode != ModeCBR
			cvbr := c.mode == ModeCVBR
			want, err := probePublicFixedCELTQ8(libopustest.CELTFixedQ8Params{
				SampleRate: 48000, Channels: c.channels,
				StreamChannels: int(enc.celtEncoder.StreamChannels()), FrameSize: frameSize,
				Start: start, End: end, Bitrate: bitrate, Complexity: c.complexity,
				LSBDepth: lsbDepth, VBR: vbr, ConstrainedVBR: cvbr,
				Frames: []libopustest.CELTFixedQ8Frame{frame},
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "celt fixed encode raw Q8", err)
				return
			}
			if len(want) != 1 {
				t.Fatalf("raw Q8 oracle records=%d want 1", len(want))
			}

			if !bytes.Equal(got, want[0].Packet) || enc.FinalRange() != want[0].FinalRange {
				n := len(got)
				if len(want[0].Packet) < n {
					n = len(want[0].Packet)
				}
				diff := -1
				for i := 0; i < n; i++ {
					if got[i] != want[0].Packet[i] {
						diff = i
						break
					}
				}
				t.Fatalf("public CELT packet mismatch: got %d bytes, want %d bytes, first diff at %d range=%08x/%08x\n got=% x\nwant=% x",
					len(got), len(want[0].Packet), diff, enc.FinalRange(), want[0].FinalRange, got, want[0].Packet)
			}

			// The raw CELT oracle receives the coded channel count selected by
			// the public encoder. Verify that count against an independent public
			// libopus encode for a low-rate stereo case where auto channel
			// selection can choose mono; this prevents the direct oracle from
			// merely repeating a production channel-control mistake.
			if c.channels == 2 && c.lm == 0 && c.bitrate == 32000 &&
				c.complexity == 0 && c.mode == ModeCBR && !c.transient {
				ref, err := probePublicFixedMixedRecords(libopustest.OpusEncodeFixedParams{
					SampleRate: 48000, Channels: c.channels,
					Application:    libopustest.OpusApplicationRestrictedLowDelay,
					MaxPacketBytes: maxSilkPacketBytes,
					ForceMode:      libopustest.OpusForceModeCELTOnly,
					Bandwidth:      libopustest.OpusBandwidthFullband,
					Bitrate:        c.bitrate,
					Complexity:     c.complexity,
					VBR:            false,
					VBRConstraint:  false,
					FrameSize:      frameSize,
					FrameCount:     1,
					LSBDepth:       24,
				}, []libopustest.OpusEncodeFixedMixedFrame{{Format: 1, FloatPCM: pcm, ForceMode: libopustest.OpusForceModeCELTOnly, Bandwidth: libopustest.OpusBandwidthFullband}})
				if err != nil {
					libopustest.HelperUnavailable(t, "public fixed Opus encode", err)
					return
				}
				if len(ref) != 1 {
					t.Fatalf("public fixed C records=%d, want one successful record", len(ref))
				}
				if ref[0].Status != 0 {
					t.Fatalf("public fixed C status=%d, want success", ref[0].Status)
				}
				codedChannels := 1
				if packet[0]&0x04 != 0 {
					codedChannels = 2
				}
				if int(enc.celtEncoder.StreamChannels()) != codedChannels {
					t.Fatalf("public TOC coded channels=%d, CELT control selected=%d", codedChannels, enc.celtEncoder.StreamChannels())
				}
				if !bytes.Equal(packet, ref[0].Packet) || enc.FinalRange() != ref[0].FinalRange {
					t.Fatalf("public fixed C packet mismatch: got %d bytes, want %d bytes, range=%08x/%08x\n got=% x\nwant=% x",
						len(packet), len(ref[0].Packet), enc.FinalRange(), ref[0].FinalRange, packet, ref[0].Packet)
				}
			}
		})
	}
}
