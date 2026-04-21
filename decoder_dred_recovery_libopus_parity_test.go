//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

import (
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
)

func TestDecoderCachedDREDRecoveryMatchesLibopusLifecycle(t *testing.T) {
	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		t.Skipf("libopus dred model helper unavailable: %v", err)
	}
	packetInfo, err := emitLibopusDREDPacket()
	if err != nil {
		t.Skipf("libopus dred packet helper unavailable: %v", err)
	}

	channels := 1
	if ParseTOC(packetInfo.packet[0]).Stereo {
		channels = 2
	}
	dec, err := NewDecoder(DefaultDecoderConfig(packetInfo.sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	blob, err := dnnblob.Clone(modelBlob)
	if err != nil {
		t.Fatalf("dnnblob.Clone(real model) error: %v", err)
	}
	if err := blob.ValidateDREDDecoderControl(); err != nil {
		t.Fatalf("ValidateDREDDecoderControl(real model) error: %v", err)
	}
	dec.setDREDDecoderBlob(blob)
	if !dec.dredModelLoaded {
		t.Fatal("standalone DRED blob did not arm decoder retention path")
	}

	pcm := make([]float32, dec.maxPacketSamples*channels)
	n, err := dec.Decode(packetInfo.packet, pcm)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if n <= 0 {
		t.Fatal("Decode returned no audio")
	}
	if dec.dredCache.Empty() {
		t.Fatal("Decode did not retain DRED payload")
	}
	if dec.dredDecoded.NbLatents <= 0 {
		t.Fatal("Decode did not retain processed DRED latents")
	}
	if got := dec.dredPLC.Blend(); got != 0 {
		t.Fatalf("Blend after good decode=%d want 0", got)
	}

	assertDecoderCachedDREDRecoveryMatchesLibopus(t, dec, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, false)

	if _, err := dec.Decode(nil, pcm); err != nil {
		t.Fatalf("Decode(nil) error: %v", err)
	}
	if !dec.dredCache.Empty() {
		t.Fatal("Decode(nil) retained cached DRED payload")
	}
	if got := dec.dredPLC.Blend(); got != 1 {
		t.Fatalf("Blend after PLC=%d want 1", got)
	}

	if _, err := dec.Decode(packetInfo.packet, pcm); err != nil {
		t.Fatalf("Decode after PLC error: %v", err)
	}
	if dec.dredCache.Empty() {
		t.Fatal("Decode after PLC did not re-retain DRED payload")
	}
	assertDecoderCachedDREDRecoveryMatchesLibopus(t, dec, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, true)
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
			if dec.dredPLC.FECFillPos() != want.recoverableFeatureFrames {
				t.Fatalf("FECFillPos()=%d want %d", dec.dredPLC.FECFillPos(), want.recoverableFeatureFrames)
			}
			if dec.dredPLC.FECSkip() != want.missingPositiveFrames {
				t.Fatalf("FECSkip()=%d want %d", dec.dredPLC.FECSkip(), want.missingPositiveFrames)
			}

			queuedFeature := 0
			var gotFeatures [lpcnetplc.NumFeatures]float32
			for _, featureOffset := range want.featureOffsets {
				if featureOffset < 0 || featureOffset > want.maxFeatureIndex {
					continue
				}
				if n := dec.dredPLC.FillQueuedFeatures(queuedFeature, gotFeatures[:]); n != lpcnetplc.NumFeatures {
					t.Fatalf("FillQueuedFeatures(%d) count=%d want %d", queuedFeature, n, lpcnetplc.NumFeatures)
				}
				start := featureOffset * lpcnetplc.NumFeatures
				assertFloat32BitsEqual(t, gotFeatures[:], dec.dredDecoded.Features[start:start+lpcnetplc.NumFeatures], "queued features")
				queuedFeature++
			}
		})
	}
}
