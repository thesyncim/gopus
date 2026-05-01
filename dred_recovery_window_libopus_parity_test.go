//go:build gopus_dred || gopus_unsupported_controls
// +build gopus_dred gopus_unsupported_controls

package gopus

import "testing"

func TestStandaloneDREDRecoveryWindowMatchesLibopus(t *testing.T) {
	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		t.Skipf("libopus dred model helper unavailable: %v", err)
	}
	packetInfo, err := emitLibopusDREDPacket()
	if err != nil {
		t.Skipf("libopus dred packet helper unavailable: %v", err)
	}

	dec := NewDREDDecoder()
	if err := dec.SetDNNBlob(modelBlob); err != nil {
		t.Fatalf("SetDNNBlob(real model) error: %v", err)
	}

	dred := NewDRED()
	available, dredEnd, err := dec.Parse(dred, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, true)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if err := dec.Process(dred, dred); err != nil {
		t.Fatalf("Process error: %v", err)
	}

	tests := []struct {
		name                string
		decodeOffsetSamples int
		frameSizeSamples    int
		blend               bool
	}{
		{name: "blend_off_negative_offset", decodeOffsetSamples: -480, frameSizeSamples: 960, blend: false},
		{name: "blend_off_current_frame", decodeOffsetSamples: 0, frameSizeSamples: 960, blend: false},
		{name: "blend_on_current_frame", decodeOffsetSamples: 0, frameSizeSamples: 960, blend: true},
		{name: "blend_off_late_recovery", decodeOffsetSamples: 3840, frameSizeSamples: 960, blend: false},
		{name: "blend_on_half_frame", decodeOffsetSamples: 960, frameSizeSamples: 480, blend: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want, err := probeLibopusDREDRecoveryWindow(packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, tc.frameSizeSamples, tc.decodeOffsetSamples, tc.blend)
			if err != nil {
				t.Skipf("libopus dred recovery helper unavailable: %v", err)
			}
			if want.availableSamples != available {
				t.Fatalf("available=%d want %d", available, want.availableSamples)
			}
			if want.dredEndSamples != dredEnd {
				t.Fatalf("dredEnd=%d want %d", dredEnd, want.dredEndSamples)
			}
			if want.processRet != 0 {
				t.Fatalf("libopus processRet=%d want 0", want.processRet)
			}
			if want.processStage != 2 {
				t.Fatalf("libopus processStage=%d want 2", want.processStage)
			}

			initFrames := 2
			if tc.blend {
				initFrames = 0
			}
			got := dred.FeatureWindow(packetInfo.maxDREDSamples, packetInfo.sampleRate, tc.decodeOffsetSamples, tc.frameSizeSamples, initFrames)
			if got.FeaturesPerFrame != want.featuresPerFrame ||
				got.NeededFeatureFrames != want.neededFeatureFrames ||
				got.FeatureOffsetBase != want.featureOffsetBase ||
				got.MaxFeatureIndex != want.maxFeatureIndex ||
				got.RecoverableFeatureFrames != want.recoverableFeatureFrames ||
				got.MissingPositiveFrames != want.missingPositiveFrames {
				t.Fatalf("FeatureWindow=%+v want %+v", got, want)
			}

			offsets := make([]int, got.NeededFeatureFrames)
			if n := got.FillFeatureOffsets(offsets); n != len(want.featureOffsets) {
				t.Fatalf("FillFeatureOffsets count=%d want %d", n, len(want.featureOffsets))
			}
			for i, wantOffset := range want.featureOffsets {
				if offsets[i] != wantOffset {
					t.Fatalf("featureOffsets[%d]=%d want %d", i, offsets[i], wantOffset)
				}
			}
		})
	}
}
