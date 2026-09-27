//go:build gopus_fixed_point && gopus_qext

package gopus

import (
	"fmt"
	"testing"
)

func TestPublicFixedQEXTWarmEncodeAllocations(t *testing.T) {
	for _, channels := range []int{1, 2} {
		t.Run(fmt.Sprintf("channels_%d", channels), func(t *testing.T) {
			const frameSize = 960
			enc, err := NewEncoder(EncoderConfig{
				SampleRate:  48000,
				Channels:    channels,
				Application: ApplicationAudio,
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, set := range []func() error{
				func() error { return enc.SetMode(EncoderModeCELT) },
				func() error { return enc.SetBandwidth(BandwidthFullband) },
				func() error { return enc.SetMaxBandwidth(BandwidthFullband) },
				func() error { return enc.SetFrameSize(frameSize) },
				func() error { return enc.SetBitrate(256000) },
				func() error { return enc.SetComplexity(10) },
				func() error { return enc.SetBitrateMode(BitrateModeCBR) },
				func() error { return enc.SetForceChannels(channels) },
				func() error { return enc.SetLSBDepth(24) },
				func() error { return enc.SetQEXT(true) },
			} {
				if err := set(); err != nil {
					t.Fatal(err)
				}
			}

			pcm := make([]int16, frameSize*channels)
			for i := range pcm {
				pcm[i] = int16((i*7919+17)%65536 - 32768)
			}
			packet := make([]byte, 4000)
			lastN := 0
			encode := func() {
				n, err := enc.EncodeInt16(pcm, packet)
				if err != nil {
					panic(err)
				}
				if n == 0 {
					panic("fixed QEXT encoder returned an empty packet")
				}
				lastN = n
			}
			for range 4 {
				encode()
			}
			if got := len(enc.enc.LastFixedCELTInputQ8()); got != frameSize*channels {
				t.Fatalf("QEXT frame bypassed the fixed CELT bridge: Q8 input length=%d want=%d", got, frameSize*channels)
			}
			_, qext := fixedQEXTPacketParts(t, packet[:lastN])
			if len(qext) == 0 {
				t.Fatal("fixed CELT frame did not produce a QEXT extension")
			}
			if allocs := testing.AllocsPerRun(20, encode); allocs != 0 {
				t.Fatalf("warmed public fixed-QEXT encode allocated %g objects", allocs)
			}
		})
	}
}
