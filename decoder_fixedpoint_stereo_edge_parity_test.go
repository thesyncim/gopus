//go:build gopus_fixed_point

package gopus

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// decodeFixedStereoEdge decodes a step sequence (nil == lost frame) through the
// public DecodeInt16 / DecodeInt24 path and the FIXED_POINT opus_decode /
// opus_decode24 oracle, then asserts bit-exact int16 AND int24 output (amd64;
// within the documented arm64 1-ULP CELT drift budget). It returns the int16
// decoder so the caller can inspect the integer redundancy / transition counters.
func decodeFixedStereoEdge(t *testing.T, sampleRate, channels, frameSize int, steps [][]byte) *Decoder {
	t.Helper()

	refInt16, err := decodeWithLibopusFixedInt16(sampleRate, channels, frameSize, steps)
	if err != nil {
		libopustest.HelperUnavailable(t, "fixed reference decode int16", err)
		return nil
	}
	refInt24, err := decodeWithLibopusFixedInt24(sampleRate, channels, frameSize, steps)
	if err != nil {
		libopustest.HelperUnavailable(t, "fixed reference decode int24", err)
		return nil
	}

	dec16, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder int16: %v", err)
	}
	dec24, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder int24: %v", err)
	}

	var got16, got24 []int32
	for p, pkt := range steps {
		o16 := make([]int16, frameSize*channels)
		if _, err := dec16.DecodeInt16(pkt, o16); err != nil {
			t.Fatalf("step %d DecodeInt16: %v", p, err)
		}
		got16 = append(got16, int16ToInt32(o16)...)
		o24 := make([]int32, frameSize*channels)
		if _, err := dec24.DecodeInt24(pkt, o24); err != nil {
			t.Fatalf("step %d DecodeInt24: %v", p, err)
		}
		got24 = append(got24, o24...)
	}

	assertFixedExact(t, "int16", got16, int16ToInt32(refInt16))
	assertFixedExact(t, "int24", got24, refInt24)
	return dec16
}

// TestDecoderFixedPointStereoCELTRecoveryParity hardens the STEREO CELT
// packet-loss recovery edge: a multi-frame loss burst followed by SEVERAL good
// received frames so the integer celt_decode_with_ec coarse-energy prediction
// safety block (loss_duration != 0) and the post-filter / decode_mem recovery
// are exercised under the integer path across the full recovery tail (not just
// the first frame after the loss). Bit-exact int16 + int24 vs the FIXED_POINT
// opus_decode / opus_decode24 oracle.
func TestDecoderFixedPointStereoCELTRecoveryParity(t *testing.T) {
	libopustest.RequireOracle(t)

	type tc struct {
		name      string
		frameSize int
		frames    int
		lossAt    map[int]bool
	}
	cases := []tc{
		{"stereo_960_burst_recover", 960, 10, map[int]bool{2: true, 3: true, 4: true}},
		{"stereo_480_burst_recover", 480, 12, map[int]bool{3: true, 4: true, 5: true}},
		{"stereo_240_burst_recover", 240, 12, map[int]bool{4: true, 5: true}},
		{"stereo_960_single_recover", 960, 8, map[int]bool{3: true}},
	}

	const sampleRate = 48000
	const channels = 2
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			encoded := encodeFixedCELTSequence(t, channels, c.frameSize, c.frames)
			steps := make([][]byte, c.frames)
			for i := 0; i < c.frames; i++ {
				if c.lossAt[i] {
					steps[i] = nil
				} else {
					steps[i] = encoded[i]
				}
			}
			decodeFixedStereoEdge(t, sampleRate, channels, c.frameSize, steps)
		})
	}
}

// TestDecoderFixedPointStereoCELTTransitionParity gates the STEREO CELT<->SILK
// (here CELT->Hybrid) transition smooth_fade under the integer path: a CELT
// frame followed by Hybrid frames triggers the integer 5 ms CELT PLC
// pcm_transition decode and the integer opus_res smooth_fade crossfade. The
// stereo transition crossfade interleaves both channels, which the mono gate
// does not cover.
func TestDecoderFixedPointStereoCELTTransitionParity(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate = 48000
	const channels = 2
	for _, frameSize := range []int{960, 480} {
		t.Run("stereo_"+itoaSmall(frameSize), func(t *testing.T) {
			phase := 0.0
			celtPkt := encodeFixedSingleModePacket(t, channels, frameSize, EncoderModeCELT, phase)
			phase += float64(frameSize)
			hyb1 := encodeFixedSingleModePacket(t, channels, frameSize, EncoderModeHybrid, phase)
			phase += float64(frameSize)
			hyb2 := encodeFixedSingleModePacket(t, channels, frameSize, EncoderModeHybrid, phase)
			if toc := ParseTOC(celtPkt[0]); toc.Mode != ModeCELT {
				t.Skipf("first packet mode %v, want CELT", toc.Mode)
			}
			if toc := ParseTOC(hyb1[0]); toc.Mode != ModeHybrid {
				t.Skipf("second packet mode %v, want Hybrid", toc.Mode)
			}
			steps := [][]byte{celtPkt, hyb1, hyb2}
			dec := decodeFixedStereoEdge(t, sampleRate, channels, frameSize, steps)
			if dec == nil {
				return
			}
			if dec.fixedTransitionApplied == 0 {
				t.Fatalf("stereo stream did not exercise the integer transition crossfade")
			}
			t.Logf("integer transition applied=%d, redundancy applied=%d",
				dec.fixedTransitionApplied, dec.fixedRedundancyApplied)
		})
	}
}

// TestDecoderFixedPointStereoRedundancyBothDirectionsParity gates STEREO Hybrid
// CELT redundancy in BOTH directions on the integer path:
//   - SILK->CELT redundancy: a Hybrid (SILK lowband) frame followed by a CELT
//     frame carries a redundant CELT frame whose tail is smooth_faded into the
//     SILK-derived output (fixedApplyRedundancySilkToCelt).
//   - CELT->SILK redundancy: a CELT frame followed by a Hybrid frame prefills
//     the Hybrid frame's head from the redundant CELT decode
//     (fixedApplyRedundancyCeltToSilk).
//
// Mode-switch streams (Hybrid<->CELT) on a single encoder produce both. The
// stereo crossfade interleaves both channels. Bit-exact int16 + int24 vs the
// FIXED_POINT oracle.
func TestDecoderFixedPointStereoRedundancyBothDirectionsParity(t *testing.T) {
	libopustest.RequireOracle(t)

	type tc struct {
		name      string
		frameSize int
		modes     []EncoderMode
	}
	cases := []tc{
		{
			name:      "stereo_hybrid_celt_hybrid_960",
			frameSize: 960,
			modes:     []EncoderMode{EncoderModeHybrid, EncoderModeHybrid, EncoderModeCELT, EncoderModeHybrid, EncoderModeHybrid},
		},
		{
			name:      "stereo_alternating_960",
			frameSize: 960,
			modes:     []EncoderMode{EncoderModeHybrid, EncoderModeCELT, EncoderModeHybrid, EncoderModeCELT, EncoderModeHybrid, EncoderModeHybrid},
		},
		{
			name:      "stereo_alternating_480",
			frameSize: 480,
			modes:     []EncoderMode{EncoderModeHybrid, EncoderModeHybrid, EncoderModeCELT, EncoderModeCELT, EncoderModeHybrid, EncoderModeHybrid},
		},
	}

	const sampleRate = 48000
	const channels = 2
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			packets := encodeFixedModeSwitchSequence(t, channels, c.frameSize, c.modes)
			dec := decodeFixedStereoEdge(t, sampleRate, channels, c.frameSize, packets)
			if dec == nil {
				return
			}
			if dec.fixedRedundancyApplied == 0 && dec.fixedTransitionApplied == 0 {
				t.Fatalf("stereo stream did not exercise the integer redundancy/transition path "+
					"(redundancy=%d transition=%d)", dec.fixedRedundancyApplied, dec.fixedTransitionApplied)
			}
			t.Logf("integer redundancy applied=%d, transition applied=%d",
				dec.fixedRedundancyApplied, dec.fixedTransitionApplied)
		})
	}
}

// TestDecoderFixedPointStereoLongPLCChurnParity gates a LONG (100+ frame) STEREO
// CELT stream with periodic packet loss so the integer decoder's cross-frame
// state (decode_mem, per-band energy histories, post-filter taps/gains,
// preemph memory, loss_duration / fade decay) is churned over many frames. Each
// periodic loss + recovery re-enters the coarse-energy prediction safety block
// from a different accumulated state, and any per-frame state divergence between
// the integer path and the FIXED_POINT oracle compounds and surfaces. Bit-exact
// int16 + int24 over the whole sequence.
func TestDecoderFixedPointStereoLongPLCChurnParity(t *testing.T) {
	libopustest.RequireOracle(t)

	type tc struct {
		name      string
		frameSize int
		frames    int
		// lossPeriod marks every lossPeriod-th frame (after warmup) as lost.
		lossPeriod int
		warmup     int
	}
	cases := []tc{
		{"stereo_960_120f_p13", 960, 120, 13, 4},
		{"stereo_480_160f_p11", 480, 160, 11, 4},
	}

	const sampleRate = 48000
	const channels = 2
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			encoded := encodeFixedCELTSequence(t, channels, c.frameSize, c.frames)
			steps := make([][]byte, c.frames)
			losses := 0
			for i := 0; i < c.frames; i++ {
				if i >= c.warmup && (i-c.warmup)%c.lossPeriod == 0 {
					steps[i] = nil
					losses++
				} else {
					steps[i] = encoded[i]
				}
			}
			if losses == 0 {
				t.Fatalf("no losses scheduled")
			}
			t.Logf("%d frames, %d periodic losses", c.frames, losses)
			decodeFixedStereoEdge(t, sampleRate, channels, c.frameSize, steps)
		})
	}
}

// TestDecoderFixedPointSubRateParity gates the integer int16 / int24 decode path
// at the sub-48k API output rates (24/16/12/8 kHz) for CELT, SILK and Hybrid
// streams. The packets are 48 kHz-domain frames; the public DecodeInt16 /
// DecodeInt24 decode them at the configured API rate (the integer CELT decoder
// runs at the core rate and decimates by 48000/Fs). Where the integer path
// declines a sub-48k case (it does for CELT-only loss frames, which decline so
// the float conversion conceals), the output still matches the FIXED_POINT
// oracle because the float fallback is itself FIXED_POINT-derived. Every step
// here is a RECEIVED frame, so the received CELT / Hybrid integer decimation and
// the inherently-integer SILK resampler are all exercised. Bit-exact int16 +
// int24 vs the FIXED_POINT opus_decode / opus_decode24 oracle.
func TestDecoderFixedPointSubRateParity(t *testing.T) {
	libopustest.RequireOracle(t)

	const coreFrame = 960 // 20 ms at 48 kHz
	const frames = 5

	subRates := []int{24000, 16000, 12000, 8000}

	for _, channels := range []int{1, 2} {
		// CELT-only.
		t.Run("celt_ch"+itoaSmall(channels), func(t *testing.T) {
			packets := encodeFixedCELTSequence(t, channels, coreFrame, frames)
			for _, sr := range subRates {
				frameSize, err := packetSamplesAtRate(packets[0], sr)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}
				t.Run("fs"+itoaSmall(sr), func(t *testing.T) {
					decodeFixedStereoEdge(t, sr, channels, frameSize, packets)
				})
			}
		})

		// SILK-only (wideband). The SILK decoder is inherently integer and its
		// int16 resampler reproduces the FIXED_POINT lowband at the API rate.
		t.Run("silk_ch"+itoaSmall(channels), func(t *testing.T) {
			packets := encodeFixedSILKSequence(t, channels, coreFrame, frames, BandwidthWideband)
			for _, pkt := range packets {
				if toc := ParseTOC(pkt[0]); toc.Mode != ModeSILK {
					t.Skipf("encoder produced mode %v, want SILK", toc.Mode)
				}
			}
			for _, sr := range subRates {
				frameSize, err := packetSamplesAtRate(packets[0], sr)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}
				t.Run("fs"+itoaSmall(sr), func(t *testing.T) {
					decodeFixedStereoEdge(t, sr, channels, frameSize, packets)
				})
			}
		})

		// Hybrid. The integer SILK lowband + integer CELT highband combine and
		// then decimate to the API rate.
		t.Run("hybrid_ch"+itoaSmall(channels), func(t *testing.T) {
			packets := make([][]byte, 0, frames)
			for f := 0; f < frames; f++ {
				pkt := encodeAPIRateHybridPacketFrameSizeVariant(t, channels, coreFrame, f)
				if toc := ParseTOC(pkt[0]); toc.Mode != ModeHybrid {
					t.Skipf("encoder produced mode %v, want Hybrid", toc.Mode)
				}
				packets = append(packets, append([]byte(nil), pkt...))
			}
			for _, sr := range subRates {
				frameSize, err := packetSamplesAtRate(packets[0], sr)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}
				t.Run("fs"+itoaSmall(sr), func(t *testing.T) {
					decodeFixedStereoEdge(t, sr, channels, frameSize, packets)
				})
			}
		})
	}
}
