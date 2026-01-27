// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
)

// TestStereoPacketByPacket compares stereo packet-by-packet.
func TestStereoPacketByPacket(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector01.bit"

	packets, err := loadPacketsSimple(bitFile, 20) // First 20 packets
	if err != nil {
		t.Skip("Could not load packets:", err)
	}

	// Check first packet for stereo
	toc := gopus.ParseTOC(packets[0][0])
	t.Logf("First packet: stereo=%v, mode=%d, bandwidth=%d, frameSize=%d",
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

	var goTotal, libTotal int

	for i, pkt := range packets {
		pktTOC := gopus.ParseTOC(pkt[0])

		// Decode with Go
		goOut, err := goDec.DecodeFloat32(pkt)
		goLen := 0
		if err == nil {
			goLen = len(goOut)
		}

		// Decode with libopus
		_, libN := libDec.DecodeFloat(pkt, pktTOC.FrameSize*4)
		libLen := 0
		if libN > 0 {
			libLen = libN * channels
		}

		goTotal += goLen
		libTotal += libLen

		// Determine mode
		modeStr := "SILK"
		if pktTOC.Mode == 1 {
			modeStr = "Hybrid"
		} else if pktTOC.Mode == 2 {
			modeStr = "CELT"
		}

		// Log any mismatches
		if goLen != libLen {
			t.Logf("Packet %2d [%s fs=%4d]: go=%5d lib=%5d MISMATCH",
				i, modeStr, pktTOC.FrameSize, goLen, libLen)
			if libN > 0 {
				t.Logf("  libN (per channel) = %d, expected total = %d", libN, libN*channels)
			}
		} else {
			t.Logf("Packet %2d [%s fs=%4d]: go=%5d lib=%5d OK",
				i, modeStr, pktTOC.FrameSize, goLen, libLen)
		}
	}

	t.Logf("\nTotal: go=%d lib=%d", goTotal, libTotal)
}

// TestCELTPacketByPacket tests CELT packets specifically (testvector07).
func TestCELTPacketByPacket(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector07.bit"

	packets, err := loadPacketsSimple(bitFile, 20)
	if err != nil {
		t.Skip("Could not load packets:", err)
	}

	toc := gopus.ParseTOC(packets[0][0])
	t.Logf("First packet: stereo=%v, mode=%d, bandwidth=%d, frameSize=%d",
		toc.Stereo, toc.Mode, toc.Bandwidth, toc.FrameSize)

	channels := 1
	if toc.Stereo {
		channels = 2
	}

	goDec, _ := gopus.NewDecoder(48000, channels)
	libDec, _ := NewLibopusDecoder(48000, channels)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	for i, pkt := range packets {
		pktTOC := gopus.ParseTOC(pkt[0])

		goOut, _ := goDec.DecodeFloat32(pkt)
		libOut, libN := libDec.DecodeFloat(pkt, pktTOC.FrameSize*4)

		libTotal := 0
		if libN > 0 {
			libTotal = libN * channels
		}

		modeStr := "SILK"
		if pktTOC.Mode == 1 {
			modeStr = "Hybrid"
		} else if pktTOC.Mode == 2 {
			modeStr = "CELT"
		}

		t.Logf("Packet %2d [%s fs=%4d]: go=%5d lib=%5d (%d*%d)",
			i, modeStr, pktTOC.FrameSize, len(goOut), libTotal, libN, channels)

		// If lengths match, compare values
		if len(goOut) == libTotal && libTotal > 0 {
			matches := 0
			for j := 0; j < libTotal; j++ {
				diff := goOut[j] - libOut[j]
				if diff > -0.0001 && diff < 0.0001 {
					matches++
				}
			}
			pct := 100.0 * float64(matches) / float64(libTotal)
			if pct < 99 {
				t.Logf("  Near-exact: %.1f%%", pct)
			}
		}
	}
}
