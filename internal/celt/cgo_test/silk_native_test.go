// Package cgo provides CGO comparison tests for SILK decoding at native rate.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestSilkNativeDecodeComparison compares SILK native decode (before resampling)
func TestSilkNativeDecodeComparison(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets := loadPackets(t, bitFile, 5)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	for i, pkt := range packets {
		toc := gopus.ParseTOC(pkt[0])
		t.Logf("Packet %d: %d bytes, mode=%v, frameSize=%d", i, len(pkt), toc.Mode, toc.FrameSize)

		// Determine internal rate from bandwidth
		internalRate := 8000
		switch toc.Bandwidth {
		case 1:
			internalRate = 12000
		case 2:
			internalRate = 16000
		}
		t.Logf("  Internal rate from TOC: %d Hz", internalRate)

		// Decode with gopus SILK at native rate
		silkDec := silk.NewDecoder()
		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			t.Logf("  Invalid bandwidth for SILK")
			continue
		}

		duration := silk.FrameDurationFromTOC(toc.FrameSize)

		// Initialize range decoder for SILK data (skip TOC byte)
		var rd rangecoding.Decoder
		if len(pkt) > 1 {
			rd.Init(pkt[1:])
		}

		// Decode at native rate
		nativeSamples, err := silkDec.DecodeFrame(&rd, silkBW, duration, true)
		if err != nil {
			t.Logf("  gopus native decode failed: %v", err)
			continue
		}

		expectedNativeSamples := toc.FrameSize * internalRate / 48000
		t.Logf("  gopus native samples: %d (expected %d at %dHz)",
			len(nativeSamples), expectedNativeSamples, internalRate)

		// Show first few native samples
		t.Log("  First 20 native samples:")
		for j := 0; j < minInt(20, len(nativeSamples)); j++ {
			t.Logf("    [%d] %.6f", j, nativeSamples[j])
		}
	}
}

// TestCompareWithFreshDecoders compares fresh decoder state
func TestCompareWithFreshDecoders(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets := loadPackets(t, bitFile, 1)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	pkt := packets[0]
	toc := gopus.ParseTOC(pkt[0])
	t.Logf("Packet 0: %d bytes, mode=%v, frameSize=%d, bw=%d",
		len(pkt), toc.Mode, toc.FrameSize, toc.Bandwidth)

	// Decode with libopus
	libDec, err := NewLibopusDecoder(48000, 1)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder")
	}
	defer libDec.Destroy()

	libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
	if libSamples < 0 {
		t.Fatalf("libopus decode failed: %d", libSamples)
	}

	t.Logf("libopus: %d samples at 48kHz", libSamples)

	// Decode with gopus
	goDec, _ := gopus.NewDecoderDefault(48000, 1)
	goPcm, decErr := decodeFloat32(goDec, pkt)
	if decErr != nil {
		t.Fatalf("gopus decode failed: %v", decErr)
	}

	t.Logf("gopus: %d samples", len(goPcm))

	// Compare sample counts
	if len(goPcm) != libSamples {
		t.Logf("Sample count mismatch: gopus=%d, libopus=%d", len(goPcm), libSamples)
	}

	// Show first 50 samples comparison
	t.Log("\nFirst 50 samples:")
	t.Log("Index\tgopus\t\tlibopus\t\tdiff")
	for i := 0; i < minInt(50, minInt(len(goPcm), libSamples)); i++ {
		diff := goPcm[i] - libPcm[i]
		marker := ""
		if math.Abs(float64(diff)) > 0.001 {
			marker = " *"
		}
		t.Logf("%d\t%.6f\t%.6f\t%.6f%s", i, goPcm[i], libPcm[i], diff, marker)
	}

	// Calculate SNR
	var sigPow, noisePow float64
	n := minInt(len(goPcm), libSamples)
	for i := 0; i < n; i++ {
		sig := float64(libPcm[i])
		noise := float64(goPcm[i]) - sig
		sigPow += sig * sig
		noisePow += noise * noise
	}
	snr := 10 * math.Log10(sigPow/noisePow)
	t.Logf("\nSNR: %.1f dB", snr)
}

// TestSilkDecodePipeline traces the SILK decode pipeline to find divergence
func TestSilkDecodePipeline(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets := loadPackets(t, bitFile, 1)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	pkt := packets[0]
	toc := gopus.ParseTOC(pkt[0])
	t.Logf("Packet 0: TOC=0x%02x, mode=%v, frameSize=%d, bw=%d",
		pkt[0], toc.Mode, toc.FrameSize, toc.Bandwidth)

	// Look at the raw packet data
	t.Logf("Packet bytes (first 32): ")
	for i := 0; i < minInt(32, len(pkt)); i++ {
		t.Logf("  [%2d] 0x%02x", i, pkt[i])
	}

	// Parse the TOC byte breakdown
	config := pkt[0] >> 3
	stereoFlag := (pkt[0] >> 2) & 1
	frameCode := pkt[0] & 3
	t.Logf("TOC breakdown: config=%d, stereo=%d, frameCode=%d", config, stereoFlag, frameCode)

	// For SILK: config determines frame size and bandwidth
	// 0-3: NB (8kHz), 4-7: MB (12kHz), 8-11: WB (16kHz)
	// Within each bandwidth: 0=10ms, 1=20ms, 2=40ms, 3=60ms
	bandwidth := config / 4
	frameMs := (config % 4)
	t.Logf("Bandwidth index: %d, Frame ms code: %d", bandwidth, frameMs)

	frameMsValues := []int{10, 20, 40, 60}
	sampleRates := []int{8000, 12000, 16000}
	if bandwidth < 3 && frameMs < 4 {
		actualFrameMs := frameMsValues[frameMs]
		actualSampleRate := sampleRates[bandwidth]
		nativeFrameSamples := actualFrameMs * actualSampleRate / 1000
		t.Logf("Expected native frame: %dms at %dHz = %d samples",
			actualFrameMs, actualSampleRate, nativeFrameSamples)
	}
}
