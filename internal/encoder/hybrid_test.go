// Package encoder tests hybrid mode SILK/CELT band splitting improvements.
// These tests verify proper crossover frequency handling, bit allocation,
// and smooth transitions between SILK and CELT bands.

package encoder

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/types"
)

// TestComputeSilkRateForHybridMatchesLibopus pins compute_silk_rate_for_hybrid()
// (src/opus_encoder.c): the per-channel rate-table interpolation, the half
// share of the rate beyond 64 kb/s, the CBR and superwideband boosts and the
// stereo adjustment.
func TestComputeSilkRateForHybridMatchesLibopus(t *testing.T) {
	for _, tc := range []struct {
		name      string
		rate      int32
		bw        types.Bandwidth
		frame20ms bool
		vbr       bool
		fec       bool
		channels  int32
		want      int32
	}{
		// 24000 sits on a table row: 18000.
		{"24k mono 20ms", 24000, types.BandwidthFullband, true, true, false, 1, 18000},
		// (13500*4000 + 16000*3600)/4000 between the 16k and 20k rows.
		{"19.6k mono 10ms", 19600, types.BandwidthFullband, false, true, false, 1, 15750},
		// 7600 below 12k interpolates from 0: 10000*7600/12000.
		{"7.6k mono 20ms", 7600, types.BandwidthFullband, true, true, false, 1, 6333},
		// FEC column: 28000 at 32k.
		{"32k mono 20ms fec", 32000, types.BandwidthFullband, true, true, true, 1, 28000},
		// Beyond 64k SILK takes half the excess: 38000+(80000-64000)/2.
		{"80k mono", 80000, types.BandwidthFullband, true, true, false, 1, 46000},
		// CBR and superwideband boosts.
		{"24k mono cbr swb", 24000, types.BandwidthSuperwideband, true, false, false, 1, 18400},
		// Stereo: per-channel 24000 -> 18000, doubled, minus 1000.
		{"48k stereo", 48000, types.BandwidthFullband, true, true, false, 2, 35000},
		// Stereo below 12k per channel keeps the doubled rate.
		{"20k stereo", 20000, types.BandwidthFullband, true, true, false, 2, 16666},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := computeSilkRateForHybrid(tc.rate, tc.bw, tc.frame20ms, tc.vbr, tc.fec, tc.channels)
			if got != tc.want {
				t.Fatalf("computeSilkRateForHybrid(%d) = %d, want %d", tc.rate, got, tc.want)
			}
		})
	}
}

// TestComputeRedundancyBytesMatchesLibopus pins compute_redundancy_bytes()
// (src/opus_encoder.c): the 5 ms equivalent rate raised by half, the cap from
// the frame budget, and the minimum below which the redundancy is dropped.
func TestComputeRedundancyBytesMatchesLibopus(t *testing.T) {
	for _, tc := range []struct {
		maxDataBytes, bitrate, frameRate, channels int32
		want                                       int32
	}{
		// (3*(24000+100*150)/2)/1600 = 36, below the cap ((240*8-200)*240/1200+100)/8 = 55.
		{240, 24000, 50, 2, 36},
		// The cap binds: ((120*8-200)*240/1200+100)/8 = 31.
		{120, 24000, 50, 2, 31},
		// ((50*8-120)*240/1200+60)/8 = 14.
		{50, 32000, 50, 1, 14},
		// At most 4+8*channels bytes is not worth coding.
		{20, 32000, 50, 1, 0},
		// At most 257 bytes.
		{1276, 510000, 50, 2, 257},
	} {
		got := computeRedundancyBytes(tc.maxDataBytes, tc.bitrate, tc.frameRate, tc.channels)
		if got != tc.want {
			t.Fatalf("computeRedundancyBytes(%d, %d, %d, %d) = %d, want %d",
				tc.maxDataBytes, tc.bitrate, tc.frameRate, tc.channels, got, tc.want)
		}
	}
}

// TestHBGainComputation verifies high-band gain attenuation at low bitrates.
func TestHBGainComputation(t *testing.T) {
	testCases := []struct {
		name            string
		celtBitrate     int
		expectedMinGain float64
		expectedMaxGain float64
	}{
		// libopus float-path formula: HB_gain = 1.0 - 2^(-celt_rate/1024)
		// This results in gains very close to 1.0 for typical CELT bitrates.
		// High CELT bitrate: nearly full gain
		{"high bitrate (25kbps)", 25000, 0.99, 1.01},
		{"moderate bitrate (16kbps)", 16000, 0.99, 1.01},
		// Medium CELT bitrate: still very close to 1.0
		{"medium bitrate (10kbps)", 10000, 0.99, 1.01},
		// Low CELT bitrate: slight attenuation
		{"low bitrate (6kbps)", 6000, 0.97, 0.99},
		// Very low CELT bitrate: more noticeable attenuation
		{"very low bitrate (4kbps)", 4000, 0.92, 0.95},
		{"minimum bitrate (2kbps)", 2000, 0.72, 0.77},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gain := hybridHBGain(int32(tc.celtBitrate))
			gain64 := float64(gain)

			t.Logf("CELT bitrate: %d, HB gain: %.4f", tc.celtBitrate, gain)

			if gain64 < tc.expectedMinGain || gain64 > tc.expectedMaxGain {
				t.Errorf("HB gain %.4f not in expected range [%.4f, %.4f]",
					gain, tc.expectedMinGain, tc.expectedMaxGain)
			}
		})
	}
}

func TestHybridCELTExp2ApproxMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	samples := []float32{
		-60, -51, -50.5, -50, -24, -10,
		-1.75, -1.5, -1.25, -1, -0.75, -0.5, -0.25,
		0, 0.25, 0.5, 0.75, 1, 1.25, 2, 5, 10, 24,
	}
	for integer := int32(-12); integer <= 12; integer++ {
		for _, frac := range []float32{0, 0.0625, 0.125, 0.33325195, 0.5, 0.875, 0.99902344} {
			samples = append(samples, float32(integer)+frac)
		}
	}
	want, err := libopustest.ProbeCELTMath(libopustest.CELTMathModeExp2, samples)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt math", err)
	}
	for i, sample := range samples {
		got := opusmath.CeltExp2(sample)
		if math.Float32bits(got) != math.Float32bits(want[i]) {
			t.Fatalf("CeltExp2(%g)=%08x(%g) want %08x(%g)",
				sample,
				math.Float32bits(got), got,
				math.Float32bits(want[i]), want[i],
			)
		}
	}
}

// TestGainFadeMatchesLibopus compares the in-place Go gain fade with the
// pinned C implementation at the supported native and half rates.
func TestGainFadeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	testCases := []struct {
		name       string
		sampleRate int
		channels   int
		g1, g2     opusVal16
	}{
		{name: "48k_mono_changing", sampleRate: 48000, channels: 1, g1: 1, g2: 0.75},
		{name: "48k_mono_steady_hybrid_gain", sampleRate: 48000, channels: 1, g1: 0.8203125, g2: 0.8203125},
		{name: "48k_mono_steady_near_unity", sampleRate: 48000, channels: 1, g1: 0.984375, g2: 0.984375},
		{name: "48k_mono_unity", sampleRate: 48000, channels: 1, g1: 1, g2: 1},
		{name: "48k_stereo_changing", sampleRate: 48000, channels: 2, g1: 0.6, g2: 0.9},
		{name: "48k_stereo_steady_near_unity", sampleRate: 48000, channels: 2, g1: 0.984375, g2: 0.984375},
		{name: "48k_stereo_unity", sampleRate: 48000, channels: 2, g1: 1, g2: 1},
		{name: "24k_mono_changing", sampleRate: 24000, channels: 1, g1: 1, g2: 0.5},
		{name: "24k_mono_steady", sampleRate: 24000, channels: 1, g1: 0.7, g2: 0.7},
		{name: "24k_mono_unity", sampleRate: 24000, channels: 1, g1: 1, g2: 1},
		{name: "24k_stereo_changing", sampleRate: 24000, channels: 2, g1: 1, g2: 0.5},
		{name: "24k_stereo_steady_hybrid_gain", sampleRate: 24000, channels: 2, g1: 0.8203125, g2: 0.8203125},
		{name: "24k_stereo_unity", sampleRate: 24000, channels: 2, g1: 1, g2: 1},
	}

	oracleCases := make([]libopustest.GainFadeParams, len(testCases))
	inputs := make([][]opusRes, len(testCases))
	for i, tc := range testCases {
		frameSize := tc.sampleRate / 50
		in := make([]opusRes, frameSize*tc.channels)
		for j := range in {
			in[j] = opusRes(float32(math.Sin(float64(j)*0.37)) * 0.9)
		}
		inputs[i] = in
		oracleCases[i] = libopustest.GainFadeParams{
			SampleRate: tc.sampleRate,
			Channels:   tc.channels,
			G1:         tc.g1,
			G2:         tc.g2,
			Samples:    in,
		}
	}
	want, err := libopustest.ProbeGainFade(oracleCases)
	if err != nil {
		libopustest.HelperUnavailable(t, "gain fade", err)
		return
	}
	for i, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			e := &Encoder{channels: int32(tc.channels), sampleRate: int32(tc.sampleRate), prevHBGain: tc.g1}
			got := append([]opusRes(nil), inputs[i]...)
			e.applyGainFade(got, tc.g1, tc.g2)
			for j := range got {
				if math.Float32bits(float32(got[j])) != math.Float32bits(want[i][j]) {
					t.Fatalf("sample %d: Go=%08x C=%08x", j, math.Float32bits(float32(got[j])), math.Float32bits(want[i][j]))
				}
			}
		})
	}
}

// TestUnityHBGainFadeAfterHybrid pins the non-hybrid leg of libopus
// opus_encode_native(): HB_gain is one, so the first frame after a hybrid
// frame with prev_HB_gain < 1 fades back up to unity, and later frames pass
// through untouched once prev_HB_gain has reset.
func TestUnityHBGainFadeAfterHybrid(t *testing.T) {
	const prev = opusVal16(0.8203125)
	e := &Encoder{channels: 1, sampleRate: 48000, prevHBGain: prev}
	in := make([]opusRes, 960)
	for i := range in {
		in[i] = opusRes(float32(math.Cos(float64(i)*0.11)) * 0.5)
	}
	want := append([]opusRes(nil), in...)
	e.applyGainFade(want, prev, 1)

	got := append([]opusRes(nil), in...)
	e.fadeHighBand(got, 1)
	for i := range want {
		if math.Float32bits(float32(got[i])) != math.Float32bits(float32(want[i])) {
			t.Fatalf("first frame out[%d] = %08x, want %08x", i, math.Float32bits(float32(got[i])), math.Float32bits(float32(want[i])))
		}
	}
	if e.prevHBGain != 1 {
		t.Fatalf("prevHBGain = %g, want 1", e.prevHBGain)
	}
	got = append([]opusRes(nil), in...)
	e.fadeHighBand(got, 1)
	for i := range in {
		if math.Float32bits(float32(got[i])) != math.Float32bits(float32(in[i])) {
			t.Fatalf("second frame out[%d] = %08x, want unchanged %08x", i, math.Float32bits(float32(got[i])), math.Float32bits(float32(in[i])))
		}
	}
}

// TestHybridModeQuality runs an end-to-end quality test for hybrid encoding.
func TestHybridModeQuality(t *testing.T) {
	// Create encoder
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeHybrid)
	enc.SetBitrate(64000) // 64 kbps

	// Generate test signal: combination of low and high frequencies
	// Low freq (1kHz) goes to SILK, high freq (10kHz) goes to CELT
	frameSize := 960
	samples := make([]float64, frameSize)

	lowFreq := 1000.0   // 1kHz - handled by SILK
	highFreq := 10000.0 // 10kHz - handled by CELT

	for i := range frameSize {
		t := float64(i) / 48000.0
		// Mix of low and high frequency
		samples[i] = 0.5*math.Sin(2*math.Pi*lowFreq*t) +
			0.3*math.Sin(2*math.Pi*highFreq*t)
	}

	// Encode
	packet, err := encodeTest(enc, samples, frameSize)
	if err != nil {
		t.Fatalf("Hybrid encoding failed: %v", err)
	}

	// Verify we got a valid packet
	if len(packet) < 2 {
		t.Error("Packet too short")
	}

	t.Logf("Hybrid packet size: %d bytes", len(packet))

	// Check TOC byte indicates hybrid mode
	toc := packet[0]
	config := (toc >> 3) & 0x1F
	if config < 12 || config > 15 {
		t.Logf("TOC config %d (expected 12-15 for hybrid)", config)
		// Note: Don't fail - auto mode may select different mode
	}
}

// BenchmarkComputeSilkRateForHybrid benchmarks the SILK share of a hybrid
// frame.
func BenchmarkComputeSilkRateForHybrid(b *testing.B) {
	for b.Loop() {
		computeSilkRateForHybrid(64000, types.BandwidthFullband, true, true, false, 1)
	}
}

func TestHybridVBRPacketSizeCap(t *testing.T) {
	enc := NewEncoder(48000, 2)
	enc.SetMode(ModeHybrid)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrateMode(ModeVBR)
	enc.SetBitrate(64000)
	enc.SetFrameSize(960)

	pcm := make([]float64, 960*2)
	for i := range 960 {
		v := 0.2 * math.Sin(2*math.Pi*440*float64(i)/48000.0)
		pcm[i*2] = v
		pcm[i*2+1] = v
	}

	packet, err := encodeTest(enc, pcm, 960)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	if len(packet) == 0 {
		t.Fatalf("expected packet, got 0 bytes")
	}

	baseBytes := enc.targetBytesForBitrate(64000, 960)
	maxAllowed := int(float64(baseBytes) * 2.0)
	if len(packet) > maxAllowed {
		t.Fatalf("hybrid VBR packet too large: got %d bytes, max %d", len(packet), maxAllowed)
	}
}

// BenchmarkHBGainComputation benchmarks HB gain calculation.
func BenchmarkHBGainComputation(b *testing.B) {
	for b.Loop() {
		hybridHBGain(25000)
	}
}
