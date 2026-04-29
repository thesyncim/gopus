//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
)

func TestDecoderCachedDREDRecoveryMatchesLibopusLifecycle(t *testing.T) {
	packetInfo, err := emitLibopusDREDPacketWithFrameSize(480)
	if err != nil {
		t.Skipf("libopus dred packet helper unavailable: %v", err)
	}
	assertDecoderCachedDREDRecoveryMatchesLibopusLifecycle(t, "16k_celt_10ms", packetInfo, 16000)
}

func TestDecoderCachedDREDRecoveryMatchesLibopusLifecycle48kCELT(t *testing.T) {
	for _, frameSize := range []int{480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthFullband,
			})
			if err != nil {
				t.Skipf("libopus dred packet helper unavailable: %v", err)
			}
			assertDecoderCachedDREDRecoveryMatchesLibopusLifecycle(t, "48k_celt", packetInfo, packetInfo.sampleRate)
		})
	}
}

func TestDecoderCachedDREDRecoveryMatchesLibopusLifecycle48kHybrid(t *testing.T) {
	for _, frameSize := range []int{480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: BandwidthFullband,
			})
			if err != nil {
				t.Skipf("libopus dred packet helper unavailable: %v", err)
			}
			assertDecoderCachedDREDRecoveryMatchesLibopusLifecycle(t, "48k_hybrid", packetInfo, packetInfo.sampleRate)
		})
	}
}

func assertDecoderCachedDREDRecoveryMatchesLibopusLifecycle(t *testing.T, label string, packetInfo libopusDREDPacket, decoderSampleRate int) {
	t.Helper()

	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		t.Skipf("libopus dred model helper unavailable: %v", err)
	}
	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)

	channels := 1
	if ParseTOC(packetInfo.packet[0]).Stereo {
		channels = 2
	}
	dec, err := NewDecoder(DefaultDecoderConfig(decoderSampleRate, channels))
	if err != nil {
		t.Fatalf("%s NewDecoder error: %v", label, err)
	}
	if err := dec.SetDNNBlob(decoderBlob); err != nil {
		t.Fatalf("%s SetDNNBlob error: %v", label, err)
	}
	blob, err := dnnblob.Clone(modelBlob)
	if err != nil {
		t.Fatalf("%s dnnblob.Clone(real model) error: %v", label, err)
	}
	if err := blob.ValidateDREDDecoderControl(); err != nil {
		t.Fatalf("%s ValidateDREDDecoderControl(real model) error: %v", label, err)
	}
	dec.setDREDDecoderBlob(blob)
	if !requireDecoderDREDState(t, dec).dredModelLoaded {
		t.Fatalf("%s standalone DRED blob did not arm decoder retention path", label)
	}

	pcm := make([]float32, dec.maxPacketSamples*channels)
	n, err := dec.Decode(packetInfo.packet, pcm)
	if err != nil {
		t.Fatalf("%s Decode error: %v", label, err)
	}
	if n <= 0 {
		t.Fatalf("%s Decode returned no audio", label)
	}
	if requireDecoderDREDState(t, dec).dredCache.Empty() {
		t.Fatalf("%s Decode did not retain DRED payload", label)
	}
	if requireDecoderDREDState(t, dec).dredDecoded.NbLatents <= 0 {
		t.Fatalf("%s Decode did not retain processed DRED latents", label)
	}
	if got := requireDecoderDREDState(t, dec).dredPLC.Blend(); got != 0 {
		t.Fatalf("%s Blend after good decode=%d want 0", label, got)
	}

	assertDecoderCachedDREDRecoveryMatchesLibopus(t, dec, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, false)

	if _, err := dec.Decode(nil, pcm); err != nil {
		t.Fatalf("%s Decode(nil) error: %v", label, err)
	}
	if requireDecoderDREDState(t, dec).dredCache.Empty() {
		t.Fatalf("%s Decode(nil) dropped cached DRED payload before recovery scheduling", label)
	}
	if got := requireDecoderDREDState(t, dec).dredPLC.Blend(); got != 1 {
		t.Fatalf("%s Blend after PLC=%d want 1", label, got)
	}

	if _, err := dec.Decode(packetInfo.packet, pcm); err != nil {
		t.Fatalf("%s Decode after PLC error: %v", label, err)
	}
	if requireDecoderDREDState(t, dec).dredCache.Empty() {
		t.Fatalf("%s Decode after PLC did not re-retain DRED payload", label)
	}
	assertDecoderCachedDREDRecoveryMatchesLibopus(t, dec, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, true)
}

func TestDecoderCachedDREDRecoveryCursorAdvancesAcrossLosses(t *testing.T) {
	packetInfo, err := emitLibopusDREDPacket()
	if err != nil {
		t.Skipf("libopus dred packet helper unavailable: %v", err)
	}
	assertDecoderCachedDREDRecoveryCursorAcrossLosses(t, "16k_celt", packetInfo, 16000, true)
}

func TestDecoderCachedDREDRecoveryCursorAdvancesAcrossLosses48kCELT(t *testing.T) {
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeCELT,
		Bandwidth: BandwidthFullband,
	})
	if err != nil {
		t.Skipf("libopus dred packet helper unavailable: %v", err)
	}
	assertDecoderCachedDREDRecoveryCursorAcrossLosses(t, "48k_celt", packetInfo, packetInfo.sampleRate, true)
}

func TestDecoderCachedDREDRecoveryCursorStaysIdleAcrossLosses48kHybrid(t *testing.T) {
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeHybrid,
		Bandwidth: BandwidthFullband,
	})
	if err != nil {
		t.Skipf("libopus dred packet helper unavailable: %v", err)
	}
	// Ordinary cached Hybrid Decode(nil) follows libopus live loss semantics:
	// the lowband hook may run, but cached DRED recovery is not queued.
	assertDecoderCachedDREDRecoveryCursorAcrossLosses(t, "48k_hybrid", packetInfo, packetInfo.sampleRate, false)
}

func assertDecoderCachedDREDRecoveryCursorAcrossLosses(t *testing.T, label string, packetInfo libopusDREDPacket, decoderSampleRate int, wantAdvance bool) {
	t.Helper()

	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		t.Skipf("libopus dred model helper unavailable: %v", err)
	}
	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)
	channels := 1
	if ParseTOC(packetInfo.packet[0]).Stereo {
		channels = 2
	}
	if channels != 1 {
		t.Skipf("%s cursor test requires mono packet, got sampleRate=%d channels=%d", label, packetInfo.sampleRate, channels)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(decoderSampleRate, channels))
	if err != nil {
		t.Fatalf("%s NewDecoder error: %v", label, err)
	}
	if err := dec.SetDNNBlob(decoderBlob); err != nil {
		t.Fatalf("%s SetDNNBlob error: %v", label, err)
	}
	blob, err := dnnblob.Clone(modelBlob)
	if err != nil {
		t.Fatalf("%s dnnblob.Clone(real model) error: %v", label, err)
	}
	if err := blob.ValidateDREDDecoderControl(); err != nil {
		t.Fatalf("%s ValidateDREDDecoderControl(real model) error: %v", label, err)
	}
	dec.setDREDDecoderBlob(blob)

	pcm := make([]float32, dec.maxPacketSamples*channels)
	if _, err := dec.Decode(packetInfo.packet, pcm); err != nil {
		t.Fatalf("%s Decode error: %v", label, err)
	}
	if requireDecoderDREDState(t, dec).dredRecovery != 0 {
		t.Fatalf("%s dredRecovery after good decode=%d want 0", label, requireDecoderDREDState(t, dec).dredRecovery)
	}

	n1, err := dec.Decode(nil, pcm)
	if err != nil {
		t.Fatalf("%s Decode(nil, first) error: %v", label, err)
	}
	wantRecovery := 0
	if wantAdvance {
		wantRecovery = n1
	}
	if requireDecoderDREDState(t, dec).dredRecovery != wantRecovery {
		t.Fatalf("%s dredRecovery after first loss=%d want %d", label, requireDecoderDREDState(t, dec).dredRecovery, wantRecovery)
	}

	n2, err := dec.Decode(nil, pcm)
	if err != nil {
		t.Fatalf("%s Decode(nil, second) error: %v", label, err)
	}
	if wantAdvance {
		wantRecovery += n2
	}
	if requireDecoderDREDState(t, dec).dredRecovery != wantRecovery {
		t.Fatalf("%s dredRecovery after second loss=%d want %d", label, requireDecoderDREDState(t, dec).dredRecovery, wantRecovery)
	}

	if _, err := dec.Decode(packetInfo.packet, pcm); err != nil {
		t.Fatalf("%s Decode(after losses) error: %v", label, err)
	}
	if requireDecoderDREDState(t, dec).dredRecovery != 0 {
		t.Fatalf("%s dredRecovery after re-decode=%d want 0", label, requireDecoderDREDState(t, dec).dredRecovery)
	}
}

func assertDecoderCachedDREDRecoveryMatchesLibopus(t *testing.T, dec *Decoder, packet []byte, maxDREDSamples, sampleRate int, blend bool) {
	t.Helper()

	tests := []struct {
		name                string
		decodeOffsetSamples int
		frameSizeSamples    int
	}{
		{name: "negative_offset", decodeOffsetSamples: -480, frameSizeSamples: 960},
		{name: "current_frame", decodeOffsetSamples: 0, frameSizeSamples: 960},
		{name: "late_recovery", decodeOffsetSamples: 3840, frameSizeSamples: 960},
		{name: "half_frame", decodeOffsetSamples: 960, frameSizeSamples: 480},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want, err := probeLibopusDREDRecoveryWindow(packet, maxDREDSamples, sampleRate, tc.frameSizeSamples, tc.decodeOffsetSamples, blend)
			if err != nil {
				t.Skipf("libopus dred recovery helper unavailable: %v", err)
			}

			got := dec.cachedDREDRecoveryWindow(maxDREDSamples, tc.decodeOffsetSamples, tc.frameSizeSamples)
			if got.FeaturesPerFrame != want.featuresPerFrame ||
				got.NeededFeatureFrames != want.neededFeatureFrames ||
				got.FeatureOffsetBase != want.featureOffsetBase ||
				got.MaxFeatureIndex != want.maxFeatureIndex ||
				got.RecoverableFeatureFrames != want.recoverableFeatureFrames ||
				got.MissingPositiveFrames != want.missingPositiveFrames {
				t.Fatalf("cachedDREDRecoveryWindow=%+v want %+v", got, want)
			}

			queued := dec.queueCachedDREDRecovery(maxDREDSamples, tc.decodeOffsetSamples, tc.frameSizeSamples)
			if queued != got {
				t.Fatalf("queueCachedDREDRecovery=%+v want %+v", queued, got)
			}
			if requireDecoderDREDState(t, dec).dredPLC.FECFillPos() != want.recoverableFeatureFrames {
				t.Fatalf("FECFillPos()=%d want %d", requireDecoderDREDState(t, dec).dredPLC.FECFillPos(), want.recoverableFeatureFrames)
			}
			if requireDecoderDREDState(t, dec).dredPLC.FECSkip() != want.missingPositiveFrames {
				t.Fatalf("FECSkip()=%d want %d", requireDecoderDREDState(t, dec).dredPLC.FECSkip(), want.missingPositiveFrames)
			}

			queuedFeature := 0
			var gotFeatures [lpcnetplc.NumFeatures]float32
			for _, featureOffset := range want.featureOffsets {
				if featureOffset < 0 || featureOffset > want.maxFeatureIndex {
					continue
				}
				if n := requireDecoderDREDState(t, dec).dredPLC.FillQueuedFeatures(queuedFeature, gotFeatures[:]); n != lpcnetplc.NumFeatures {
					t.Fatalf("FillQueuedFeatures(%d) count=%d want %d", queuedFeature, n, lpcnetplc.NumFeatures)
				}
				start := featureOffset * lpcnetplc.NumFeatures
				assertFloat32BitsEqual(t, gotFeatures[:], requireDecoderDREDState(t, dec).dredDecoded.Features[start:start+lpcnetplc.NumFeatures], "queued features")
				queuedFeature++
			}
		})
	}
}
