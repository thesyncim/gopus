//go:build gopus_fixed_point

// Package testvectors exercises the fixed-point public Opus encoder against
// selected FIXED_POINT libopus 1.6.1. Forced CELT streams compare complete
// packets and final ranges on identical raw float input. The same frames also
// compare their inner CELT payloads against selected C on exact opus_res Q8.
// SILK and Hybrid coverage below reports its current resampler boundary.
package testvectors

import (
	"bytes"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/types"
)

// xorshiftNext is a small deterministic PRNG for generating test PCM.
func xorshiftNext(state *uint32) uint32 {
	s := *state
	s ^= s << 13
	s ^= s >> 17
	s ^= s << 5
	*state = s
	return s
}

// celtFixedFrame holds one public packet and its exact inner Q8 controls.
type celtFixedFrame struct {
	packet       []byte
	rangeV       uint32
	raw          []float32
	inner        libopustest.CELTFixedQ8Frame
	innerBitrate int
	lsbDepth     int
}

// driveFixedCELT drives the PUBLIC encoder.Encoder in forced CELT-only mode and
// captures the raw float input and Q8 frame before the next encode reuses them.
func driveFixedCELT(t *testing.T, channels, lm, bitrate int, mode encoder.BitrateMode, numFrames int) []celtFixedFrame {
	t.Helper()
	const shortMdctSize = 120
	const complexity = 10
	frameSize := shortMdctSize << lm

	enc := encoder.NewEncoder(48000, channels)
	enc.SetMode(encoder.ModeCELT)
	enc.SetLowDelay(true)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetComplexity(complexity)
	enc.SetBitrate(bitrate)
	enc.SetBitrateMode(mode)
	// Pin the coded channel count on both public encoders.
	enc.SetForceChannels(channels)

	state := uint32(0xC0FFEE + lm*131 + bitrate + int(mode)*97 + channels*7)
	frames := make([]celtFixedFrame, 0, numFrames)
	for f := 0; f < numFrames; f++ {
		pcm := make([]float32, channels*frameSize)
		for i := range pcm {
			v := int32(xorshiftNext(&state))
			s := float32(v>>16) / 32768.0 * 0.25
			// A transient burst mid-stream stresses transient analysis.
			if f == 3 && i > len(pcm)/2 && i < len(pcm)/2+40 {
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
		pkt, err := enc.Encode(pcm, frameSize)
		if err != nil {
			t.Fatalf("frame %d: public Encode: %v", f, err)
		}
		if len(pkt) < 1 {
			t.Fatalf("frame %d: empty packet", f)
		}
		inQ8 := enc.LastFixedCELTInputQ8()
		if len(inQ8) != channels*frameSize {
			t.Fatalf("frame %d: LastFixedCELTInputQ8 len=%d want %d",
				f, len(inQ8), channels*frameSize)
		}
		bitrateUsed, maxBytes, lsbDepth := enc.LastFixedCELTControls()
		analysis := enc.LastFixedCELTAnalysis()
		frames = append(frames, celtFixedFrame{
			packet: append([]byte(nil), pkt...), rangeV: enc.FinalRange(),
			raw: append([]float32(nil), pcm...),
			inner: libopustest.CELTFixedQ8Frame{
				PCM: append([]int32(nil), inQ8...), MaxBytes: maxBytes,
				Analysis: libopustest.CELTFixedQ8Analysis{
					Valid: analysis.Valid, Tonality: analysis.Tonality,
					TonalitySlope: analysis.TonalitySlope, Noisiness: analysis.NoisySpeech,
					Activity: analysis.Activity, MusicProb: analysis.MusicProb,
					MusicProbMin: analysis.MusicProbMin, MusicProbMax: analysis.MusicProbMax,
					Bandwidth: analysis.BandwidthIndex, ActivityProbability: analysis.VADProb,
					MaxPitchRatio: analysis.MaxPitchRatio, LeakBoost: analysis.LeakBoost,
				},
			},
			innerBitrate: bitrateUsed, lsbDepth: lsbDepth,
		})
	}
	return frames
}

// TestOpusEncodeFixedCELTByteExact compares every complete public packet and
// final range against the same raw float input in selected fixed C. It also
// compares the inner payload and range on identical Q8 input and analysis.
func TestOpusEncodeFixedCELTByteExact(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierParity)
	libopustest.RequireOracle(t)

	const (
		numFrames     = 6
		complexity    = 10
		celtStart     = 0
		celtEnd       = 21
		shortMdctSize = 120
	)

	type kase struct {
		lm       int
		channels int
		bitrate  int
		mode     encoder.BitrateMode
	}
	var cases []kase
	for _, ch := range []int{1, 2} {
		for _, lm := range []int{0, 1, 2, 3} {
			for _, br := range []int{32000, 64000, 128000} {
				for _, m := range []encoder.BitrateMode{encoder.ModeCBR, encoder.ModeCVBR, encoder.ModeVBR} {
					cases = append(cases, kase{lm: lm, channels: ch, bitrate: br, mode: m})
				}
			}
		}
	}

	for _, c := range cases {
		c := c
		name := fmt.Sprintf("ch%d/lm%d/br%d/%v", c.channels, c.lm, c.bitrate, c.mode)
		t.Run(name, func(t *testing.T) {
			frameSize := shortMdctSize << c.lm
			frames := driveFixedCELT(t, c.channels, c.lm, c.bitrate, c.mode, numFrames)

			vbr := c.mode != encoder.ModeCBR
			cvbr := c.mode == encoder.ModeCVBR

			topFrames := make([]libopustest.OpusEncodeFixedMixedFrame, numFrames)
			innerFrames := make([]libopustest.CELTFixedQ8Frame, numFrames)
			innerBitrate, lsbDepth := frames[0].innerBitrate, frames[0].lsbDepth
			for _, fr := range frames {
				if fr.innerBitrate != innerBitrate || fr.lsbDepth != lsbDepth {
					t.Fatalf("inner controls changed across frames")
				}
			}
			for i, fr := range frames {
				topFrames[i] = libopustest.OpusEncodeFixedMixedFrame{Format: 1, FloatPCM: fr.raw}
				innerFrames[i] = fr.inner
			}
			topPackets, err := libopustest.ProbeOpusEncodeFixedMixedRecords(libopustest.OpusEncodeFixedParams{
				SampleRate:     48000,
				Channels:       c.channels,
				Application:    libopustest.OpusApplicationRestrictedLowDelay,
				MaxPacketBytes: 1276,
				ForceMode:      libopustest.OpusForceModeCELTOnly,
				Bandwidth:      libopustest.OpusBandwidthFullband,
				Bitrate:        c.bitrate,
				Complexity:     complexity,
				VBR:            vbr,
				VBRConstraint:  cvbr,
				ForceChannels:  c.channels,
				FrameSize:      frameSize,
				FrameCount:     numFrames,
			}, topFrames)
			if err != nil {
				libopustest.HelperUnavailable(t, "opus encode fixed", err)
				return
			}
			if len(topPackets) != len(frames) {
				t.Fatalf("packet count: gopus=%d FIXED opus_encode=%d", len(frames), len(topPackets))
			}

			wantInner, err := libopustest.ProbeCELTFixedRawQ8(libopustest.CELTFixedQ8Params{
				SampleRate: 48000, Channels: c.channels, FrameSize: frameSize,
				Start: celtStart, End: celtEnd, Bitrate: innerBitrate,
				Complexity: complexity, LSBDepth: lsbDepth, VBR: vbr,
				ConstrainedVBR: cvbr, Frames: innerFrames,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "celt fixed encode raw Q8", err)
				return
			}
			if len(wantInner) != len(frames) {
				t.Fatalf("inner CELT packet count: gopus=%d FIXED celt_encode=%d", len(frames), len(wantInner))
			}

			for f, fr := range frames {
				if topPackets[f].Status < 0 ||
					!bytes.Equal(fr.packet, topPackets[f].Packet) ||
					fr.rangeV != topPackets[f].FinalRange {
					reportOpusEncodeFixedDiff(t, f, fr.packet, topPackets[f].Packet)
					t.Fatalf("frame %d: public packet/range mismatch status=%d range=%08x/%08x",
						f, topPackets[f].Status, fr.rangeV, topPackets[f].FinalRange)
				}
				got := fr.packet[1:]
				want := wantInner[f]
				if !bytes.Equal(got, want.Packet) || fr.rangeV != want.FinalRange {
					reportOpusEncodeFixedDiff(t, f, got, want.Packet)
					t.Fatalf("frame %d: inner CELT payload/range mismatch (got %d bytes, want %d bytes; range=%08x/%08x)",
						f, len(got), len(want.Packet), fr.rangeV, want.FinalRange)
				}
			}
		})
	}
}

// TestOpusEncodeFixedCELTFloatInputSingleFrameByteExact exercises a separate
// one-frame float-input seed at each CELT duration. Both selected C entry points
// receive the same public float or inner opus_res Q8 frame as Go.
func TestOpusEncodeFixedCELTFloatInputSingleFrameByteExact(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierParity)
	libopustest.RequireOracle(t)

	for _, lm := range []int{0, 1, 2, 3} {
		lm := lm
		t.Run(fmt.Sprintf("lm%d", lm), func(t *testing.T) {
			frameSize := 120 << lm
			fr := driveFixedCELT(t, 1, lm, 64000, encoder.ModeVBR, 1)[0]
			top, err := libopustest.ProbeOpusEncodeFixedMixedRecords(libopustest.OpusEncodeFixedParams{
				SampleRate: 48000, Channels: 1,
				Application:    libopustest.OpusApplicationRestrictedLowDelay,
				MaxPacketBytes: 1276,
				ForceMode:      libopustest.OpusForceModeCELTOnly,
				Bandwidth:      libopustest.OpusBandwidthFullband,
				Bitrate:        64000, Complexity: 10, VBR: true, ForceChannels: 1,
				FrameSize: frameSize, FrameCount: 1,
			}, []libopustest.OpusEncodeFixedMixedFrame{{Format: 1, FloatPCM: fr.raw}})
			if err != nil {
				libopustest.HelperUnavailable(t, "opus encode fixed float input", err)
				return
			}
			if len(top) != 1 {
				t.Fatalf("lm%d: public C records=%d want 1", lm, len(top))
			}
			if top[0].Status < 0 || !bytes.Equal(fr.packet, top[0].Packet) || fr.rangeV != top[0].FinalRange {
				t.Fatalf("lm%d: public packet/range mismatch status=%d range=%08x/%08x", lm,
					top[0].Status, fr.rangeV, top[0].FinalRange)
			}
			inner, err := libopustest.ProbeCELTFixedRawQ8(libopustest.CELTFixedQ8Params{
				SampleRate: 48000, Channels: 1, FrameSize: frameSize,
				Start: 0, End: 21, Bitrate: fr.innerBitrate, Complexity: 10,
				LSBDepth: fr.lsbDepth, VBR: true,
				Frames: []libopustest.CELTFixedQ8Frame{fr.inner},
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "celt fixed encode raw Q8", err)
				return
			}
			if len(inner) != 1 {
				t.Fatalf("lm%d: inner C records=%d want 1", lm, len(inner))
			}
			if !bytes.Equal(fr.packet[1:], inner[0].Packet) || fr.rangeV != inner[0].FinalRange {
				t.Fatalf("lm%d: inner payload/range mismatch range=%08x/%08x", lm,
					fr.rangeV, inner[0].FinalRange)
			}
		})
	}
}

// TestOpusEncodeFixedSILKHybridResamplerCaveat records packet differences for
// forced SILK and Hybrid streams on raw 48 kHz input. The SILK API-rate
// resampling path in this build remains float32, while selected fixed libopus
// uses integer resampling. The per-frame fixed SILK kernel has separate
// identical-input byte gates; this test asserts packet count and reports the
// public-wrapper difference without treating unmatched input as a codec oracle.
func TestOpusEncodeFixedSILKHybridResamplerCaveat(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierParity)
	libopustest.RequireOracle(t)

	const (
		fs         = 48000
		frameSize  = 960 // 20 ms
		numFrames  = 4
		complexity = 10
		bitrate    = 24000
	)

	type kase struct {
		name      string
		mode      encoder.Mode
		forceMode int
		bw        types.Bandwidth
		oracleBW  int
		channels  int
	}
	cases := []kase{
		{"silk_nb_mono", encoder.ModeSILK, libopustest.OpusForceModeSILKOnly, types.BandwidthNarrowband, libopustest.OpusBandwidthNarrowband, 1},
		{"silk_mb_mono", encoder.ModeSILK, libopustest.OpusForceModeSILKOnly, types.BandwidthMediumband, libopustest.OpusBandwidthMediumband, 1},
		{"silk_wb_mono", encoder.ModeSILK, libopustest.OpusForceModeSILKOnly, types.BandwidthWideband, libopustest.OpusBandwidthWideband, 1},
		{"silk_wb_stereo", encoder.ModeSILK, libopustest.OpusForceModeSILKOnly, types.BandwidthWideband, libopustest.OpusBandwidthWideband, 2},
		{"hybrid_swb_mono", encoder.ModeHybrid, libopustest.OpusForceModeHybrid, types.BandwidthSuperwideband, libopustest.OpusBandwidthSuperwideband, 1},
		{"hybrid_fb_mono", encoder.ModeHybrid, libopustest.OpusForceModeHybrid, types.BandwidthFullband, libopustest.OpusBandwidthFullband, 1},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			enc := encoder.NewEncoder(fs, c.channels)
			enc.SetMode(c.mode)
			enc.SetBandwidth(c.bw)
			enc.SetBitrate(bitrate)
			enc.SetBitrateMode(encoder.ModeCBR)
			enc.SetComplexity(complexity)
			enc.SetForceChannels(c.channels)

			gotPackets := make([][]byte, 0, numFrames)
			rawI16 := make([]int16, 0, numFrames*frameSize*c.channels)
			for f := 0; f < numFrames; f++ {
				pcm := make([]float32, c.channels*frameSize)
				for i := 0; i < frameSize; i++ {
					ti := float64(f*frameSize+i) / fs
					s := float32(0.3 * math.Sin(2*math.Pi*300*ti))
					for ch := 0; ch < c.channels; ch++ {
						pcm[i*c.channels+ch] = s
					}
				}
				pkt, err := enc.Encode(pcm, frameSize)
				if err != nil {
					t.Fatalf("frame %d: public Encode: %v", f, err)
				}
				if len(pkt) < 1 {
					t.Fatalf("frame %d: empty packet", f)
				}
				gotPackets = append(gotPackets, append([]byte(nil), pkt...))
				for _, v := range pcm {
					rawI16 = append(rawI16, opusmath.Float32ToInt16(v))
				}
			}

			want, err := libopustest.ProbeOpusEncodeFixed(libopustest.OpusEncodeFixedParams{
				SampleRate:    fs,
				Channels:      c.channels,
				ForceMode:     c.forceMode,
				Bandwidth:     c.oracleBW,
				Bitrate:       bitrate,
				Complexity:    complexity,
				VBR:           false,
				ForceChannels: c.channels,
				FrameSize:     frameSize,
				FrameCount:    numFrames,
				PCM:           rawI16,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "opus encode fixed", err)
				return
			}

			if len(want) != len(gotPackets) {
				t.Fatalf("packet count: gopus=%d FIXED opus_encode=%d", len(gotPackets), len(want))
			}

			diverged := 0
			for f := range want {
				if !bytes.Equal(gotPackets[f], want[f]) {
					diverged++
				}
			}
			t.Logf("packets=%d byte-divergent=%d (documented float API-rate resampler "+
				"caveat: gopus wrapper is float, libopus FIXED wrapper is integer; "+
				"per-frame SILK encode is byte-exact given identical input)",
				len(want), diverged)
		})
	}
}

// reportOpusEncodeFixedDiff logs the first byte that differs between got/want.
func reportOpusEncodeFixedDiff(t *testing.T, frame int, got, want []byte) {
	t.Helper()
	n := len(got)
	if len(want) < n {
		n = len(want)
	}
	first := -1
	for i := 0; i < n; i++ {
		if got[i] != want[i] {
			first = i
			break
		}
	}
	if first < 0 && len(got) != len(want) {
		first = n
	}
	t.Logf("frame %d firstByteDiff=%d len(got=%d want=%d)", frame, first, len(got), len(want))
	t.Logf("  got =% x", got)
	t.Logf("  want=% x", want)
}
