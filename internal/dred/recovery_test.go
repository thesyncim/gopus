package dred

import (
	"testing"

	"github.com/thesyncim/gopus/internal/lpcnetplc"
)

func TestQueueProcessedFeaturesDoesNotAllocate(t *testing.T) {
	var plc lpcnetplc.State
	var decoded Decoded
	decoded.NbLatents = 2
	for i := 0; i < decoded.NbLatents*4*NumFeatures; i++ {
		decoded.Features[i] = float32(i + 1)
	}
	result := Result{
		Request: Request{
			MaxDREDSamples: 960,
			SampleRate:     48000,
		},
		Availability: Availability{
			MaxLatents:    decoded.NbLatents,
			OffsetSamples: -480,
		},
	}

	allocs := testing.AllocsPerRun(1000, func() {
		plc.MarkUpdated()
		QueueProcessedFeatures(&plc, result, &decoded, 0, 960)
	})
	if allocs != 0 {
		t.Fatalf("AllocsPerRun=%v want 0", allocs)
	}
}

func TestProcessedFeatureWindowUsesDecodedLatents(t *testing.T) {
	result := Result{
		Request: Request{
			MaxDREDSamples: 960,
			SampleRate:     48000,
		},
		Availability: Availability{
			MaxLatents:    1,
			OffsetSamples: 480,
		},
	}
	decoded := &Decoded{NbLatents: 2}

	got := ProcessedFeatureWindow(result, decoded, 2880, 960, 2)
	if got.MaxFeatureIndex != 7 {
		t.Fatalf("MaxFeatureIndex=%d want 7", got.MaxFeatureIndex)
	}
	if got.RecoverableFeatureFrames != 4 || got.MissingPositiveFrames != 0 {
		t.Fatalf("window=(recoverable=%d, missing=%d) want (4,0)", got.RecoverableFeatureFrames, got.MissingPositiveFrames)
	}
}
