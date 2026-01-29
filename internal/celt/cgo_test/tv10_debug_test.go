// tv10_debug_test.go - Debug testvector10 SNR=0.0 issue
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTV10FirstPacket examines why first packet has SNR=0.0
func TestTV10FirstPacket(t *testing.T) {
	packets := loadVectorPackets(t, "testvector10")
	if len(packets) == 0 {
		t.Skip("No packets")
		return
	}

	channels := 2

	// Decode first packet with fresh decoders
	goDec, _ := gopus.NewDecoder(48000, channels)
	libDec, _ := NewLibopusDecoder(48000, channels)
	defer libDec.Destroy()

	pkt := packets[0]
	toc := gopus.ParseTOC(pkt[0])
	t.Logf("Packet 0: %d bytes, mode=%v, frame=%d, stereo=%v",
		len(pkt), toc.Mode, toc.FrameSize, toc.Stereo)

	goPcm, goErr := goDec.DecodeFloat32(pkt)
	if goErr != nil {
		t.Fatalf("gopus error: %v", goErr)
	}

	libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
	if libSamples < 0 {
		t.Fatalf("libopus error: %d", libSamples)
	}

	t.Logf("gopus samples: %d, libopus samples: %d", len(goPcm)/channels, libSamples)

	// Check if samples are all zeros
	goZeroCount := 0
	libZeroCount := 0
	for i := 0; i < len(goPcm); i++ {
		if goPcm[i] == 0 {
			goZeroCount++
		}
	}
	for i := 0; i < libSamples*channels; i++ {
		if libPcm[i] == 0 {
			libZeroCount++
		}
	}

	t.Logf("gopus zeros: %d/%d (%.1f%%)", goZeroCount, len(goPcm), 100*float64(goZeroCount)/float64(len(goPcm)))
	t.Logf("libopus zeros: %d/%d (%.1f%%)", libZeroCount, libSamples*channels, 100*float64(libZeroCount)/float64(libSamples*channels))

	// Show sample comparison
	t.Log("\nFirst 40 samples (interleaved L/R):")
	maxShow := 40
	if len(goPcm) < maxShow {
		maxShow = len(goPcm)
	}
	for i := 0; i < maxShow; i++ {
		ch := "L"
		if i%2 == 1 {
			ch = "R"
		}
		t.Logf("[%d %s] go=%.8f lib=%.8f", i/2, ch, goPcm[i], libPcm[i])
	}

	// Calculate proper SNR
	var sig, noise float64
	n := minInt(len(goPcm), libSamples*channels)
	for i := 0; i < n; i++ {
		s := float64(libPcm[i])
		d := float64(goPcm[i]) - s
		sig += s * s
		noise += d * d
	}

	snr := 10 * math.Log10(sig/noise)
	t.Logf("\nActual SNR: %.2f dB (sig=%.10f, noise=%.10f)", snr, sig, noise)
}

// TestTV10CompareFirst20Packets compares the first 20 packets
func TestTV10CompareFirst20Packets(t *testing.T) {
	packets := loadVectorPackets(t, "testvector10")
	if len(packets) < 20 {
		t.Skip("Need at least 20 packets")
		return
	}

	channels := 2

	goDec, _ := gopus.NewDecoder(48000, channels)
	libDec, _ := NewLibopusDecoder(48000, channels)
	defer libDec.Destroy()

	for i := 0; i < 20; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		goPcm, _ := goDec.DecodeFloat32(pkt)
		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)

		if libSamples < 0 {
			t.Logf("Pkt %d: libopus error %d", i, libSamples)
			continue
		}

		var sig, noise float64
		var maxDiff float64
		n := minInt(len(goPcm), libSamples*channels)
		for j := 0; j < n; j++ {
			s := float64(libPcm[j])
			d := float64(goPcm[j]) - s
			sig += s * s
			noise += d * d
			if math.Abs(d) > maxDiff {
				maxDiff = math.Abs(d)
			}
		}

		snr := 10 * math.Log10(sig/noise)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999
		}

		status := "OK"
		if snr < 40 {
			status = "BAD"
		}

		t.Logf("Pkt %2d: mode=%v frame=%4d stereo=%v len=%3d SNR=%7.1f maxDiff=%.6f %s",
			i, toc.Mode, toc.FrameSize, toc.Stereo, len(pkt), snr, maxDiff, status)
	}
}

// TestTV10FirstModeCheck verifies what modes testvector10 has
func TestTV10FirstModeCheck(t *testing.T) {
	packets := loadVectorPackets(t, "testvector10")
	if len(packets) == 0 {
		t.Skip("No packets")
		return
	}

	modeCount := make(map[gopus.Mode]int)
	frameCount := make(map[int]int)
	stereoCount := make(map[bool]int)

	for i := 0; i < minInt(100, len(packets)); i++ {
		toc := gopus.ParseTOC(packets[i][0])
		modeCount[toc.Mode]++
		frameCount[toc.FrameSize]++
		stereoCount[toc.Stereo]++
	}

	t.Logf("First 100 packets modes: %v", modeCount)
	t.Logf("First 100 packets frame sizes: %v", frameCount)
	t.Logf("First 100 packets stereo: %v", stereoCount)
}
