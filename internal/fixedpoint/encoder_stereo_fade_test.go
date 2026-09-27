//go:build gopus_fixed_point

package fixedpoint

import (
	"fmt"
	"testing"
)

func TestGainFadeRes24Allocs(t *testing.T) {
	for _, sampleRate := range [...]int{8000, 12000, 16000, 24000, 48000} {
		for _, channels := range [...]int{1, 2} {
			t.Run(fmt.Sprintf("%dHz/%dch", sampleRate, channels), func(t *testing.T) {
				samples := make([]int32, sampleRate/50*channels)
				for i := range samples {
					samples[i] = int32((i*7919)%(1<<20) - (1 << 19))
				}
				for range 8 {
					GainFadeRes24(samples, channels, 32767, 24575, sampleRate)
				}
				if allocs := testing.AllocsPerRun(100, func() {
					GainFadeRes24(samples, channels, 32767, 24575, sampleRate)
				}); allocs != 0 {
					t.Fatalf("GainFadeRes24 allocs/op = %.2f, want 0", allocs)
				}
			})
		}
	}
}
