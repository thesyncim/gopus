//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

import (
	"testing"

	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
)

func TestStandaloneDREDRecoveryQueueMatchesLibopus(t *testing.T) {
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
	result := dred.Result(packetInfo.maxDREDSamples, packetInfo.sampleRate)

	tests := []struct {
		name                string
		decodeOffsetSamples int
		frameSizeSamples    int
		blend               bool
	}{
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

			var plc lpcnetplc.State
			allocs := testing.AllocsPerRun(1000, func() {
				if tc.blend {
					plc.MarkConcealed()
				} else {
					plc.MarkUpdated()
				}
				internaldred.QueueProcessedFeatures(&plc, result, &dred.decoded, tc.decodeOffsetSamples, tc.frameSizeSamples)
			})
			if allocs != 0 {
				t.Fatalf("AllocsPerRun=%v want 0", allocs)
			}

			if tc.blend {
				plc.MarkConcealed()
			} else {
				plc.MarkUpdated()
			}
			got := internaldred.QueueProcessedFeatures(&plc, result, &dred.decoded, tc.decodeOffsetSamples, tc.frameSizeSamples)
			if got.FeaturesPerFrame != want.featuresPerFrame ||
				got.NeededFeatureFrames != want.neededFeatureFrames ||
				got.FeatureOffsetBase != want.featureOffsetBase ||
				got.MaxFeatureIndex != want.maxFeatureIndex ||
				got.RecoverableFeatureFrames != want.recoverableFeatureFrames ||
				got.MissingPositiveFrames != want.missingPositiveFrames {
				t.Fatalf("QueueProcessedFeatures window=%+v want %+v", got, want)
			}
			if plc.FECFillPos() != want.recoverableFeatureFrames {
				t.Fatalf("FECFillPos()=%d want %d", plc.FECFillPos(), want.recoverableFeatureFrames)
			}
			if plc.FECSkip() != want.missingPositiveFrames {
				t.Fatalf("FECSkip()=%d want %d", plc.FECSkip(), want.missingPositiveFrames)
			}

			queued := 0
			var gotFeatures [lpcnetplc.NumFeatures]float32
			for _, featureOffset := range want.featureOffsets {
				if featureOffset < 0 || featureOffset > want.maxFeatureIndex {
					continue
				}
				if n := plc.FillQueuedFeatures(queued, gotFeatures[:]); n != lpcnetplc.NumFeatures {
					t.Fatalf("FillQueuedFeatures(%d) count=%d want %d", queued, n, lpcnetplc.NumFeatures)
				}
				start := featureOffset * internaldred.NumFeatures
				assertFloat32BitsEqual(t, gotFeatures[:], dred.decoded.Features[start:start+internaldred.NumFeatures], "queued features")
				queued++
			}
			if queued != plc.FECFillPos() {
				t.Fatalf("queued=%d want fill=%d", queued, plc.FECFillPos())
			}
		})
	}
}
