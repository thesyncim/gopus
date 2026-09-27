//go:build gopus_fixed_point

package gopus

import (
	"fmt"
	"testing"
)

// The recursive 5 ms PLC stage and the following received CELT/Hybrid frames
// reuse decoder-owned scratch once the caller has warmed both transitions.
func TestDecoderFixedPointHybridTransitionWarmZeroAlloc(t *testing.T) {
	for _, channels := range []int{1, 2} {
		celt := encodeFixedSingleModePacket(t, channels, 960, EncoderModeCELT, 0)
		hybrid := encodeFixedSingleModePacket(t, channels, 960, EncoderModeHybrid, 960)
		for _, rate := range []int{8000, 48000} {
			t.Run(fmt.Sprintf("ch%d_%dHz", channels, rate), func(t *testing.T) {
				dec, err := NewDecoder(DefaultDecoderConfig(rate, channels))
				if err != nil {
					t.Fatal(err)
				}
				frameSize := rate / 50
				out := make([]int16, frameSize*channels)
				for i := 0; i < 3; i++ {
					if _, err := dec.DecodeInt16(celt, out); err != nil {
						t.Fatal(err)
					}
					if _, err := dec.DecodeInt16(hybrid, out); err != nil {
						t.Fatal(err)
					}
				}
				var celtN, hybridN int
				allocs := testing.AllocsPerRun(100, func() {
					celtN, err = dec.DecodeInt16(celt, out)
					if err != nil {
						return
					}
					hybridN, err = dec.DecodeInt16(hybrid, out)
				})
				if err != nil || celtN != frameSize || hybridN != frameSize {
					t.Fatalf("transition samples CELT=%d Hybrid=%d want %d: %v", celtN, hybridN, frameSize, err)
				}
				if allocs != 0 {
					t.Fatalf("warm CELT/Hybrid transition allocs=%g want 0", allocs)
				}
				active := false
				for _, sample := range out {
					active = active || sample != 0
				}
				if !active {
					t.Fatal("warm Hybrid output is silent")
				}
			})
		}
	}
}
