//go:build gopus_fixed_point

package gopus

import (
	"fmt"
	"testing"
)

// Received packets prime CELT pitch PLC; twelve consecutive losses cross into
// noise synthesis. Every warm cycle includes recovery and non-silent input.
func TestDecoderFixedPointActiveLossCycleZeroAlloc(t *testing.T) {
	for _, mode := range []EncoderMode{EncoderModeCELT, EncoderModeHybrid} {
		for _, channels := range []int{1, 2} {
			packet := encodeFixedSingleModePacket(t, channels, 960, mode, 0)
			for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
				for _, format := range []string{"float32", "int16", "int24"} {
					t.Run(fmt.Sprintf("mode%d_ch%d_%dHz_%s", mode, channels, rate, format), func(t *testing.T) {
						d, err := NewDecoder(DefaultDecoderConfig(rate, channels))
						if err != nil {
							t.Fatal(err)
						}
						size := rate / 50
						floats := make([]float32, size*channels)
						ints16 := make([]int16, size*channels)
						ints24 := make([]int32, size*channels)
						active := false
						activeLoss := false
						cycle := func() {
							active, activeLoss = false, false
							for step := 0; step < 17; step++ {
								data := packet
								if step >= 3 && step < 15 {
									data = nil
								}
								var n int
								switch format {
								case "float32":
									n, err = d.Decode(data, floats)
								case "int16":
									n, err = d.DecodeInt16(data, ints16)
								default:
									n, err = d.DecodeInt24(data, ints24)
								}
								if err != nil || n != size {
									t.Fatalf("step%d n=%d err=%v", step, n, err)
								}
								if step == 2 {
									for i := 0; i < size*channels; i++ {
										active = active || floats[i] != 0 || ints16[i] != 0 || ints24[i] != 0
									}
								}
								if step == 3 {
									for i := 0; i < size*channels; i++ {
										activeLoss = activeLoss || floats[i] != 0 || ints16[i] != 0 || ints24[i] != 0
									}
								}
							}
						}
						for range 3 {
							cycle()
						}
						if !active {
							t.Fatal("warm received signal is silent")
						}
						if allocs := testing.AllocsPerRun(10, cycle); allocs != 0 {
							t.Fatalf("active received/loss/recovery cycle allocations=%g want0", allocs)
						}
						if !active || !activeLoss {
							t.Fatal("measured cycle does not contain active received audio and concealment")
						}
					})
				}
			}
		}
	}
}
