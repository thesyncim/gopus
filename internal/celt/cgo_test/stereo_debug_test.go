// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
)

// TestStereoSampleCount debugs the stereo sample count mismatch.
func TestStereoSampleCount(t *testing.T) {
	testVectors := []string{
		"testvector01", "testvector02", "testvector08", "testvector11",
	}

	for _, name := range testVectors {
		t.Run(name, func(t *testing.T) {
			bitFile := "../../../internal/testvectors/testdata/opus_testvectors/" + name + ".bit"

			packets, err := loadPacketsSimple(bitFile, 5) // Just first 5 packets
			if err != nil {
				t.Skipf("Could not load packets: %v", err)
				return
			}

			if len(packets) == 0 {
				t.Skip("No packets")
				return
			}

			// Check first packet's TOC
			toc := gopus.ParseTOC(packets[0][0])
			t.Logf("First packet TOC: stereo=%v, mode=%d, bandwidth=%d, frameSize=%d",
				toc.Stereo, toc.Mode, toc.Bandwidth, toc.FrameSize)

			channels := 1
			if toc.Stereo {
				channels = 2
			}

			// Create decoders
			goDec, err := gopus.NewDecoder(48000, channels)
			if err != nil {
				t.Fatal(err)
			}

			libDec, _ := NewLibopusDecoder(48000, channels)
			if libDec == nil {
				t.Skip("Could not create libopus decoder")
			}
			defer libDec.Destroy()

			t.Logf("Created decoders with %d channels", channels)

			// Decode first few packets
			for i := 0; i < len(packets) && i < 3; i++ {
				pkt := packets[i]
				pktTOC := gopus.ParseTOC(pkt[0])

				// Decode with Go
				goOut, err := goDec.DecodeFloat32(pkt)
				if err != nil {
					t.Logf("Packet %d: Go decode error: %v", i, err)
					continue
				}

				// Decode with libopus
				maxSamples := pktTOC.FrameSize * channels * 2
				libOut, libN := libDec.DecodeFloat(pkt, maxSamples)

				t.Logf("Packet %d: stereo=%v frameSize=%d, go=%d lib=%d samples",
					i, pktTOC.Stereo, pktTOC.FrameSize, len(goOut), libN)

				if len(goOut) != libN {
					t.Logf("  MISMATCH: expected %d, go produced %d", libN, len(goOut))
					// Show expected samples per channel
					goPerChannel := len(goOut) / channels
					libPerChannel := libN / channels
					t.Logf("  Go per channel: %d, Lib per channel: %d", goPerChannel, libPerChannel)
				}

				// Show first few samples
				if libN > 0 && len(goOut) > 0 {
					t.Logf("  First 5 go samples: %v", goOut[:min(5, len(goOut))])
					t.Logf("  First 5 lib samples: %v", libOut[:min(5, libN)])
				}
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
