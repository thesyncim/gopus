package encoder

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/types"
)

// TestSILK10msTOCDump dumps the actual TOC byte and packet structure for 10ms vs 20ms
func TestSILK10msTOCDump(t *testing.T) {
	for _, tc := range []struct {
		name      string
		bw        types.Bandwidth
		frameSize int
	}{
		{"WB-10ms", types.BandwidthWideband, 480},
		{"WB-20ms", types.BandwidthWideband, 960},
		{"NB-10ms", types.BandwidthNarrowband, 480},
		{"NB-20ms", types.BandwidthNarrowband, 960},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(48000, 1)
			enc.SetMode(ModeSILK)
			enc.SetBandwidth(tc.bw)
			enc.SetBitrate(32000)

			for i := 0; i < 5; i++ {
				pcm := make([]float64, tc.frameSize)
				for j := range pcm {
					sampleIdx := i*tc.frameSize + j
					tm := float64(sampleIdx) / 48000.0
					pcm[j] = 0.5 * math.Sin(2*math.Pi*440*tm)
				}
				pkt, err := enc.Encode(pcm, tc.frameSize)
				if err != nil {
					t.Fatalf("frame %d: %v", i, err)
				}
				if pkt == nil {
					continue
				}

				toc := pkt[0]
				config := toc >> 3
				stereo := (toc >> 2) & 1
				frameCode := toc & 3

				// Decode config to verify expected frame duration
				// SILK NB: config 0-3 (10/20/40/60ms)
				// SILK MB: config 4-7
				// SILK WB: config 8-11
				var expectedConfig uint8
				if tc.bw == types.BandwidthNarrowband {
					if tc.frameSize == 480 {
						expectedConfig = 0 // NB 10ms
					} else {
						expectedConfig = 1 // NB 20ms
					}
				} else {
					if tc.frameSize == 480 {
						expectedConfig = 8 // WB 10ms
					} else {
						expectedConfig = 9 // WB 20ms
					}
				}

				configOK := "OK"
				if config != expectedConfig {
					configOK = fmt.Sprintf("MISMATCH (expected %d)", expectedConfig)
				}

				t.Logf("Frame %d: TOC=0x%02x config=%d(%s) stereo=%d code=%d pktLen=%d data=[%x ...]",
					i, toc, config, configOK, stereo, frameCode, len(pkt), pkt[:min(8, len(pkt))])
			}
		})
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
