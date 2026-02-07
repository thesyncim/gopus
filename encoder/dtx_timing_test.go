package encoder

import "testing"

func TestShouldUseDTXFrameDurationUsesConfiguredSampleRate(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetDTX(true)

	if _, _ = enc.shouldUseDTX(make([]float64, 960)); enc.dtx.frameDurationMs != 20 {
		t.Fatalf("frameDurationMs(960@48k) = %d, want 20", enc.dtx.frameDurationMs)
	}

	if _, _ = enc.shouldUseDTX(make([]float64, 1920)); enc.dtx.frameDurationMs != 40 {
		t.Fatalf("frameDurationMs(1920@48k) = %d, want 40", enc.dtx.frameDurationMs)
	}

	if _, _ = enc.shouldUseDTX(make([]float64, 2880)); enc.dtx.frameDurationMs != 60 {
		t.Fatalf("frameDurationMs(2880@48k) = %d, want 60", enc.dtx.frameDurationMs)
	}
}

func TestShouldUseDTXDoesNotSuppressBeforeThresholdAt48k20ms(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetDTX(true)
	silence := make([]float64, 960) // 20ms @ 48kHz

	// DTX must not suppress before the configured 200ms threshold.
	for i := 0; i < DTXFrameThreshold; i++ {
		suppress, _ := enc.shouldUseDTX(silence)
		if suppress {
			t.Fatalf("suppressed too early at frame %d (threshold=%d)", i, DTXFrameThreshold)
		}
	}
}
