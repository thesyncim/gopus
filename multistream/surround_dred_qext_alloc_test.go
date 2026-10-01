//go:build gopus_dred && gopus_qext && !gopus_osce

package multistream

import (
	"testing"
)

func TestDREDQEXTSurroundAndProjectionWarmedCycleZeroAllocs(t *testing.T) {
	cases := []struct {
		name       string
		channels   int
		projection bool
		bitrate    int
		duration   int
	}{
		{name: "surround_5_1", channels: 6, bitrate: dredQEXTBitrate, duration: dredQEXTDuration},
		{name: "projection_foa", channels: 4, projection: true, bitrate: dredQEXTBitrate, duration: dredQEXTDuration},
		{name: "surround_5_1_high_rate", channels: 6, bitrate: dredQEXTHighBitrate, duration: dredQEXTHighRateDuration},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pcm := dredQEXTPCM(tc.channels, tc.projection)
			enc := newDREDQEXTMultistreamEncoder(t, tc.channels, tc.projection, tc.bitrate, tc.duration)
			packet := make([]byte, dredQEXTPacketCapacity)
			encodeCycle := func() {
				for frame := range dredQEXTFrameCount {
					if frame == dredQEXTResetFrame {
						enc.Reset()
						if err := enc.SetDREDDuration(tc.duration); err != nil {
							t.Fatalf("frame %d reapply DRED duration after reset: %v", frame, err)
						}
					}
					start := frame * dredQEXTFrameSize * tc.channels
					n, err := enc.EncodeInt16(pcm[start:start+dredQEXTFrameSize*tc.channels], dredQEXTFrameSize, packet)
					if err != nil || n <= 0 {
						t.Fatalf("frame %d encode: bytes=%d err=%v", frame, n, err)
					}
				}
			}
			encodeCycle()
			encodeCycle()
			allocs := testing.AllocsPerRun(2, encodeCycle)
			if allocs != 0 {
				t.Fatalf("warmed 96-frame combined DRED-QEXT cycle allocated %.0f objects", allocs)
			}
			t.Logf("warmed 96-frame encode/reset/rearm cycle: %.0f allocations", allocs)
		})
	}
}
