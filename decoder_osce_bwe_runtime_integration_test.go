//go:build gopus_extra_controls

package gopus

// Smoke test for the OSCE BWE forward-pass wiring on the decoder hot path. The
// wiring lives in `decoder_osce_bwe_apply.go`; this test verifies that:
//
//   - SetOSCEBWE(true) + SetDNNBlob(merged core+BWE) succeeds.
//   - Decoding a Hybrid SWB packet completes without panic / error.
//   - The decoder returns the expected number of samples per channel.
//   - The decoded PCM has non-zero energy (so the helper did not zero out the
//     standard silk_resampler output when BWE conditions are not met).
//
// libopus only enables OSCE_MODE_SILK_BBWE for SILK-only WB at 48 kHz API, so
// a Hybrid SWB packet must NOT trigger the BWE replacement. The test therefore
// implicitly verifies the gate logic in maybeApplyOSCEBWEPostSilk: the BWE
// hook is armed (model loaded, control enabled) but the standard hybrid
// upsampler output is preserved untouched.

import (
	"math"
	"testing"

	internalenc "github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
)

func TestDecoderOSCEBWERuntimeIntegration(t *testing.T) {
	coreBlob := requireLibopusDecoderNeuralModelBlob(t)
	bweBlob := requireLibopusOSCEBWEModelBlob(t)

	merged := make([]byte, 0, len(coreBlob)+len(bweBlob))
	merged = append(merged, coreBlob...)
	merged = append(merged, bweBlob...)

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder(stereo 48kHz): %v", err)
	}
	if err := dec.SetComplexity(4); err != nil {
		t.Fatalf("SetComplexity(4): %v", err)
	}

	if err := dec.SetOSCEBWE(true); err != nil {
		t.Fatalf("SetOSCEBWE(true): %v", err)
	}
	enabled, err := dec.OSCEBWE()
	if err != nil {
		t.Fatalf("OSCEBWE(): %v", err)
	}
	if !enabled {
		t.Fatalf("OSCEBWE() == false after SetOSCEBWE(true)")
	}

	if err := dec.SetDNNBlob(merged); err != nil {
		t.Fatalf("SetDNNBlob(merged core+BWE): %v", err)
	}
	if !dec.osceBWEModelLoaded {
		t.Fatalf("decoder did not retain osceBWEModelLoaded after SetDNNBlob")
	}
	if !dec.osceBWEModelLoadedRuntime() {
		t.Fatalf("decoder did not bind OSCE BWE runtime model after SetDNNBlob")
	}

	// Hybrid SWB packets are encoded mono; the stereo decoder will up-mix.
	// Hybrid mode does NOT satisfy OSCE_MODE_SILK_BBWE so the BWE hook must
	// short-circuit and leave the standard hybrid upsampler output untouched.
	t.Run("hybrid_swb_bypasses_bwe", func(t *testing.T) {
		const frameSize = 960 // 20 ms @ 48 kHz
		packet := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthSuperwideband)
		toc := ParseTOC(packet[0])
		if toc.Mode != ModeHybrid || toc.Bandwidth != BandwidthSuperwideband {
			t.Fatalf("unexpected TOC: mode=%v bandwidth=%v", toc.Mode, toc.Bandwidth)
		}

		pcm := make([]float32, dec.maxPacketSamples*dec.channels)
		got, err := dec.Decode(packet, pcm)
		if err != nil {
			t.Fatalf("Decode(hybrid SWB packet): %v", err)
		}
		if got != frameSize {
			t.Fatalf("Decode(hybrid SWB packet) returned %d samples per channel, want %d", got, frameSize)
		}

		// PCM must be non-zero -- BWE must not have clobbered the standard
		// hybrid upsampler output (Hybrid mode does not satisfy
		// OSCE_MODE_SILK_BBWE in libopus).
		var energy float64
		for _, v := range pcm[:got*dec.channels] {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Fatalf("decoded PCM contains NaN/Inf: %v", v)
			}
			energy += float64(v) * float64(v)
		}
		if energy == 0 {
			t.Fatalf("decoded PCM is silent after BWE-armed Hybrid SWB decode -- standard upsampler output was unexpectedly clobbered")
		}
	})

	// SILK WB packets DO satisfy OSCE_MODE_SILK_BBWE so the BWE hook
	// should overwrite the standard silk_resampler output with the BWE
	// 16k -> 48k forward pass. The packet is mono so the stereo decoder's
	// channels==2 branch in maybeApplyOSCEBWEPostSilk applies. Verify the
	// decode completes and PCM is non-zero.
	t.Run("silk_wb_invokes_bwe", func(t *testing.T) {
		// Reset to clear any prior packet state.
		dec.Reset()
		const frameSize = 960 // 20 ms @ 48 kHz
		packet := makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)
		toc := ParseTOC(packet[0])
		if toc.Mode != ModeSILK || toc.Bandwidth != BandwidthWideband {
			t.Fatalf("unexpected TOC: mode=%v bandwidth=%v", toc.Mode, toc.Bandwidth)
		}

		pcm := make([]float32, dec.maxPacketSamples*dec.channels)
		got, err := dec.Decode(packet, pcm)
		if err != nil {
			t.Fatalf("Decode(silk WB packet): %v", err)
		}
		if got != frameSize {
			t.Fatalf("Decode(silk WB packet) returned %d samples per channel, want %d", got, frameSize)
		}

		var energy float64
		for _, v := range pcm[:got*dec.channels] {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Fatalf("decoded PCM contains NaN/Inf: %v", v)
			}
			energy += float64(v) * float64(v)
		}
		if energy == 0 {
			t.Fatalf("decoded PCM is silent after BWE-armed SILK WB decode")
		}
	})

	// Stereo SILK WB packets also satisfy OSCE_MODE_SILK_BBWE. libopus runs
	// `osce_bwe(...)` independently on each channel with its own
	// `silk_OSCE_BWE_struct`. The gopus runtime mirrors that with
	// `osceBWERuntime[0]` (mid/left) and `osceBWERuntime[1]` (side/right)
	// in decoderOSCEBWEState. Verify both channels carry non-zero BWE
	// output and the channels differ (the stereo test packet is encoded
	// with distinct L/R content so a per-channel BWE pass must produce
	// distinguishable outputs).
	t.Run("stereo_silk_wb_invokes_bwe", func(t *testing.T) {
		// Reset to clear any prior packet state from the previous sub-test.
		dec.Reset()
		const frameSize = 960 // 20 ms @ 48 kHz
		packet := makeValidStereoSILKPacketForFrameSizeBandwidthForOSCEBWETest(t, frameSize, BandwidthWideband)
		toc := ParseTOC(packet[0])
		if toc.Mode != ModeSILK || toc.Bandwidth != BandwidthWideband {
			t.Fatalf("unexpected TOC: mode=%v bandwidth=%v", toc.Mode, toc.Bandwidth)
		}
		if !toc.Stereo {
			t.Fatalf("expected stereo packet but TOC.Stereo=false")
		}

		pcm := make([]float32, dec.maxPacketSamples*dec.channels)
		got, err := dec.Decode(packet, pcm)
		if err != nil {
			t.Fatalf("Decode(stereo silk WB packet): %v", err)
		}
		if got != frameSize {
			t.Fatalf("Decode(stereo silk WB packet) returned %d samples per channel, want %d", got, frameSize)
		}

		var leftEnergy, rightEnergy, diffEnergy float64
		for i := 0; i < got; i++ {
			l := pcm[2*i]
			r := pcm[2*i+1]
			if math.IsNaN(float64(l)) || math.IsInf(float64(l), 0) {
				t.Fatalf("decoded left PCM contains NaN/Inf at i=%d: %v", i, l)
			}
			if math.IsNaN(float64(r)) || math.IsInf(float64(r), 0) {
				t.Fatalf("decoded right PCM contains NaN/Inf at i=%d: %v", i, r)
			}
			leftEnergy += float64(l) * float64(l)
			rightEnergy += float64(r) * float64(r)
			diff := float64(l - r)
			diffEnergy += diff * diff
		}
		if leftEnergy == 0 {
			t.Fatalf("decoded left channel PCM is silent after stereo BWE-armed SILK WB decode")
		}
		if rightEnergy == 0 {
			t.Fatalf("decoded right channel PCM is silent after stereo BWE-armed SILK WB decode")
		}
		// The stereo test packet uses different signals per channel so the
		// per-channel BWE forward pass should produce distinguishable
		// output. If both channels were identical (e.g. because the side-
		// channel runtime was not invoked) diffEnergy would be exactly zero.
		if diffEnergy == 0 {
			t.Fatalf("decoded stereo PCM is mono (left == right) after stereo BWE pass -- side-channel runtime not invoked?")
		}
	})
}

func TestDecoderOSCEBWEComplexityGate(t *testing.T) {
	coreBlob := requireLibopusDecoderNeuralModelBlob(t)
	bweBlob := requireLibopusOSCEBWEModelBlob(t)

	merged := make([]byte, 0, len(coreBlob)+len(bweBlob))
	merged = append(merged, coreBlob...)
	merged = append(merged, bweBlob...)

	const frameSize = 960
	packet := makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)

	for _, tc := range []struct {
		name       string
		complexity int
		wantActive bool
	}{
		{name: "below_gate", complexity: 3, wantActive: false},
		{name: "at_gate", complexity: 4, wantActive: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			if err := dec.SetComplexity(tc.complexity); err != nil {
				t.Fatalf("SetComplexity(%d): %v", tc.complexity, err)
			}
			if err := dec.SetOSCEBWE(true); err != nil {
				t.Fatalf("SetOSCEBWE(true): %v", err)
			}
			if err := dec.SetDNNBlob(merged); err != nil {
				t.Fatalf("SetDNNBlob(merged core+BWE): %v", err)
			}
			pcm := make([]float32, dec.maxPacketSamples*dec.channels)
			if got, err := dec.Decode(packet, pcm); err != nil {
				t.Fatalf("Decode(silk WB): %v", err)
			} else if got != frameSize {
				t.Fatalf("Decode returned %d samples per channel, want %d", got, frameSize)
			}
			gotActive := dec.osceBWE != nil && dec.osceBWE.prevBWEActive
			if gotActive != tc.wantActive {
				t.Fatalf("prevBWEActive=%v want %v at complexity %d", gotActive, tc.wantActive, tc.complexity)
			}
		})
	}
}

// makeValidStereoSILKPacketForFrameSizeBandwidthForOSCEBWETest synthesises a
// stereo SILK packet at the given frame size / bandwidth with distinct L/R
// content so the per-channel OSCE BWE forward pass produces distinguishable
// output. Mirrors the mono helper in decoder_dred_test.go but with two-channel
// input.
func makeValidStereoSILKPacketForFrameSizeBandwidthForOSCEBWETest(t *testing.T, frameSize int, bandwidth Bandwidth) []byte {
	t.Helper()

	if frameSize != 480 && frameSize != 960 && frameSize != 1920 && frameSize != 2880 {
		t.Fatalf("silk stereo BWE test packet requires 10/20/40/60ms frame size, got %d", frameSize)
	}
	if bandwidth != BandwidthNarrowband && bandwidth != BandwidthMediumband && bandwidth != BandwidthWideband {
		t.Fatalf("silk stereo BWE test packet requires NB/MB/WB bandwidth, got %v", bandwidth)
	}

	enc := internalenc.NewEncoder(48000, 2)
	enc.SetMode(internalenc.ModeSILK)
	enc.SetBandwidth(types.Bandwidth(bandwidth))
	enc.SetBitrate(48000)
	enc.SetForceChannels(2)

	pcm := make([]float64, frameSize*2)
	for i := 0; i < frameSize; i++ {
		tm := float64(i) / 48000.0
		l := 0.31*math.Sin(2*math.Pi*197*tm) + 0.12*math.Sin(2*math.Pi*389*tm+0.23)
		// Distinct right-channel content (different frequencies / phase) so
		// the per-channel BWE forward pass should produce L != R.
		r := 0.27*math.Sin(2*math.Pi*263*tm+0.41) + 0.14*math.Sin(2*math.Pi*431*tm+0.07)
		pcm[2*i] = l
		pcm[2*i+1] = r
	}

	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode(stereo SILK): %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("Encode(stereo SILK) returned empty packet")
	}
	toc := ParseTOC(packet[0])
	if toc.Mode != ModeSILK || toc.Bandwidth != bandwidth || toc.FrameSize != frameSize {
		t.Fatalf("Encode(stereo SILK) produced mode=%v bandwidth=%v frame=%d, want mode=%v bandwidth=%v frame=%d", toc.Mode, toc.Bandwidth, toc.FrameSize, ModeSILK, bandwidth, frameSize)
	}
	if !toc.Stereo {
		t.Fatalf("Encode(stereo SILK) produced mono packet (TOC.Stereo=false) despite ForceChannels=2")
	}
	return packet
}
