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
// The selected C reference receives the exact opus_res Q8 input and per-frame
// analysis consumed by the integer CELT encoder. The test keeps C state across
// the same five-frame sequence and compares every complete payload and range.
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

		// Drive every frame through the public encoder, capturing the Q8 input,
		// analysis, and CELT payload before the next encode reuses scratch.
		perFrame := c.channels * frameSize
		fed := make([]libopustest.CELTFixedQ8Frame, nframes)
		payloads := make([][]byte, nframes)
		ranges := make([]uint32, nframes)
		f32 := make([]float32, perFrame)
		out := make([]byte, 4000)
		innerBitrate, lsbDepth := 0, 0
		for f := 0; f < nframes; f++ {
			for i := 0; i < perFrame; i++ {
				f32[i] = float32(pcm16[f*perFrame+i]) / 32768.0
			}
			n, err := enc.Encode(f32, out)
			if err != nil {
				t.Fatalf("Encode frame %d: %v", f, err)
			}
			input := enc.enc.LastFixedCELTInputQ8()
			if len(input) != perFrame {
				t.Fatalf("ch=%d lm=%d frame=%d: integer CELT Q8 input len=%d want %d",
					c.channels, c.lm, f, len(input), perFrame)
			}
			rate, maxBytes, depth := enc.enc.LastFixedCELTControls()
			if f == 0 {
				innerBitrate, lsbDepth = rate, depth
			} else if rate != innerBitrate || depth != lsbDepth {
				t.Fatalf("frame %d: inner controls changed bitrate=%d/%d depth=%d/%d", f, rate, innerBitrate, depth, lsbDepth)
			}
			analysis := enc.enc.LastFixedCELTAnalysis()
			fed[f] = libopustest.CELTFixedQ8Frame{
				PCM: append([]int32(nil), input...), MaxBytes: maxBytes,
				Analysis: libopustest.CELTFixedQ8Analysis{
					Valid: analysis.Valid, Tonality: analysis.Tonality,
					TonalitySlope: analysis.TonalitySlope, Noisiness: analysis.NoisySpeech,
					Activity: analysis.Activity, MusicProb: analysis.MusicProb,
					MusicProbMin: analysis.MusicProbMin, MusicProbMax: analysis.MusicProbMax,
					Bandwidth: analysis.BandwidthIndex, ActivityProbability: analysis.VADProb,
					MaxPitchRatio: analysis.MaxPitchRatio, LeakBoost: analysis.LeakBoost,
				},
			}
			if n < 1 {
				t.Fatalf("Encode frame %d: short packet %d", f, n)
			}
			payloads[f] = append([]byte(nil), out[1:n]...)
			ranges[f] = enc.FinalRange()
		}

		vbr := c.mode != BitrateModeCBR
		constrained := c.mode == BitrateModeCVBR

		want, err := libopustest.ProbeCELTFixedRawQ8(libopustest.CELTFixedQ8Params{
			SampleRate: 48000, Channels: c.channels, FrameSize: frameSize,
			Start: start, End: end, Bitrate: innerBitrate, Complexity: c.complexity,
			LSBDepth: lsbDepth, VBR: vbr, ConstrainedVBR: constrained, Frames: fed,
		})
		if err != nil {
			libopustest.HelperUnavailable(t, "celt fixed encode raw Q8", err)
			return
		}
		if len(want) != nframes {
			t.Fatalf("raw Q8 oracle records=%d want %d", len(want), nframes)
		}

		for f := 0; f < nframes; f++ {
			label := fmt.Sprintf("ch=%d lm=%d br=%d cx=%d mode=%v frame=%d",
				c.channels, c.lm, c.bitrate, c.complexity, c.mode, f)
			got := payloads[f]
			if !bytes.Equal(got, want[f].Packet) || ranges[f] != want[f].FinalRange {
				mismatch := 0
				for mismatch < min(len(got), len(want[f].Packet)) && got[mismatch] == want[f].Packet[mismatch] {
					mismatch++
				}
				t.Errorf("%s: CELT payload/range mismatch at byte %d (gopus len=%d libopus len=%d range=%08x/%08x)",
					label, mismatch, len(got), len(want[f].Packet), ranges[f], want[f].FinalRange)
				break
			}
		}
	}
}
