package libopustest

import (
	"fmt"
	"testing"
)

func TestProbeGainFadeRejectsFramesShorterThanOverlap(t *testing.T) {
	for _, tc := range []struct {
		name           string
		sampleRate     int
		frameSize      int
		minimumSamples int
	}{
		{name: "48k", sampleRate: 48000, frameSize: 119, minimumSamples: 120},
		{name: "24k", sampleRate: 24000, frameSize: 59, minimumSamples: 60},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ProbeGainFade([]GainFadeParams{{
				SampleRate: tc.sampleRate,
				Channels:   1,
				G1:         0.8,
				G2:         0.9,
				Samples:    make([]float32, tc.frameSize),
			}})
			want := fmt.Sprintf("gain fade case 0 frame size %d below minimum %d", tc.frameSize, tc.minimumSamples)
			if err == nil || err.Error() != want {
				t.Fatalf("ProbeGainFade error = %v, want %q", err, want)
			}
		})
	}
}
