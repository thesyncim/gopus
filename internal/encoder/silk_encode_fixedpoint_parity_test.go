//go:build gopus_fixed_point

package encoder

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/types"
)

// TestPublicEncoderSILKFixedPathEngaged drives the PUBLIC encoder.Encoder in
// SILK-only and Hybrid modes under the gopus_fixed_point build and confirms each
// SILK frame is routed through the integer FIXED_POINT SILK encode driver. The
// per-frame SILK payload byte-exactness against libopus silk_encode_frame_FIX is
// proven at the silk.Encoder layer (silk.TestPublicSILKEncodeFrameFixedByteExact);
// encoder.Encoder delegates to that exact driver frame by frame, so this test
// guards the wiring: the integer path engages and produces a non-empty packet
// across mono SILK NB/MB/WB and Hybrid SWB/FB.
func TestPublicEncoderSILKFixedPathEngaged(t *testing.T) {
	type kase struct {
		name string
		mode Mode
		bw   types.Bandwidth
	}
	cases := []kase{
		{"silk_nb", ModeSILK, types.BandwidthNarrowband},
		{"silk_mb", ModeSILK, types.BandwidthMediumband},
		{"silk_wb", ModeSILK, types.BandwidthWideband},
		{"hybrid_swb", ModeHybrid, types.BandwidthSuperwideband},
		{"hybrid_fb", ModeHybrid, types.BandwidthFullband},
	}

	const fs = 48000
	const frameSize = 960 // 20 ms

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			enc := NewEncoder(fs, 1)
			enc.SetMode(c.mode)
			enc.SetBandwidth(c.bw)
			enc.SetBitrate(24000)
			enc.SetBitrateMode(ModeVBR)
			enc.SetComplexity(5)

			engaged := false
			for f := 0; f < 6; f++ {
				pcm := make([]float32, frameSize)
				for i := range pcm {
					ti := float64(f*frameSize+i) / fs
					pcm[i] = float32(0.3 * math.Sin(2*math.Pi*300*ti))
				}
				pkt, err := enc.Encode(pcm, frameSize)
				if err != nil {
					t.Fatalf("frame %d: Encode: %v", f, err)
				}
				if len(pkt) == 0 {
					t.Fatalf("frame %d: empty packet", f)
				}
				if enc.silkEncoder != nil && len(enc.silkEncoder.FixedXBufForTest()) > 0 {
					engaged = true
				}
			}
			if !engaged {
				t.Fatalf("SILK frames were not routed through the integer FIXED_POINT encode path")
			}
		})
	}
}
