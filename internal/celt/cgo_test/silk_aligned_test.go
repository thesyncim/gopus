// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestSilkAlignedComparison compares gopus and libopus outputs with proper delay alignment.
// libopus has a 5-sample delay for 8kHz output (4 from resampler delay buffer + 1 from buffer offset).
func TestSilkAlignedComparison(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 5)
	if err != nil || len(packets) < 1 {
		t.Skip("Could not load packets")
	}

	pkt := packets[0]
	toc := gopus.ParseTOC(pkt[0])
	if toc.Mode != gopus.ModeSILK {
		t.Skip("Not SILK mode")
	}

	silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
	if !ok {
		t.Skip("Invalid SILK bandwidth")
	}
	duration := silk.FrameDurationFromTOC(toc.FrameSize)
	config := silk.GetBandwidthConfig(silkBW)

	t.Logf("Bandwidth: %d kHz, Duration: %d ms", config.SampleRate/1000, duration)

	// Determine delay based on sample rate
	var delay int
	switch config.SampleRate {
	case 8000:
		delay = 5 // 4 from resampler + 1 from buffer offset
	case 12000:
		delay = 10 // 9 from resampler + 1 from buffer offset
	case 16000:
		delay = 13 // 12 from resampler + 1 from buffer offset
	default:
		delay = 5
	}
	t.Logf("Using delay compensation: %d samples", delay)

	// gopus native decode
	var rd rangecoding.Decoder
	rd.Init(pkt[1:])
	goDec := silk.NewDecoder()
	goNative, err := goDec.DecodeFrame(&rd, silkBW, duration, true)
	if err != nil {
		t.Fatalf("gopus native decode failed: %v", err)
	}

	// libopus decode at native rate
	libDec, err := NewLibopusDecoder(config.SampleRate, 1)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder at %d Hz", config.SampleRate)
	}
	defer libDec.Destroy()

	libPcm, libSamples := libDec.DecodeFloat(pkt, 960)
	if libSamples < 0 {
		t.Fatalf("libopus decode failed: %d", libSamples)
	}

	t.Logf("gopus samples: %d, libopus samples: %d", len(goNative), libSamples)

	// Compare with delay alignment
	alignedLen := len(goNative)
	if libSamples-delay < alignedLen {
		alignedLen = libSamples - delay
	}
	if alignedLen <= 0 {
		t.Fatal("Not enough samples after delay alignment")
	}

	t.Log("\nFirst 20 samples with delay alignment:")
	var sumSqErr, sumSqSig float64
	exactMatches := 0
	closeMatches := 0 // within 1 LSB
	for i := 0; i < alignedLen; i++ {
		goVal := goNative[i]
		libVal := libPcm[i+delay]

		diff := goVal - libVal
		sumSqErr += float64(diff * diff)
		sumSqSig += float64(libVal * libVal)

		if diff == 0 {
			exactMatches++
		}
		if math.Abs(float64(diff)) <= 1.0/32768.0 {
			closeMatches++
		}

		if i < 20 {
			goInt16 := int16(goVal * 32768)
			libInt16 := int16(libVal * 32768)
			diffInt16 := goInt16 - libInt16
			marker := ""
			if diffInt16 == 0 {
				marker = " (exact)"
			} else if diffInt16 >= -1 && diffInt16 <= 1 {
				marker = " (1 LSB)"
			}
			t.Logf("  go[%3d] vs lib[%3d]: go=%7d lib=%7d diff=%4d%s",
				i, i+delay, goInt16, libInt16, diffInt16, marker)
		}
	}

	snr := 10 * math.Log10(sumSqSig/sumSqErr)
	if math.IsNaN(snr) || math.IsInf(snr, 1) {
		snr = 999.0
	}

	t.Logf("\nAligned comparison statistics:")
	t.Logf("  Compared samples: %d", alignedLen)
	t.Logf("  Exact matches: %d (%.1f%%)", exactMatches, 100.0*float64(exactMatches)/float64(alignedLen))
	t.Logf("  Close matches (<=1 LSB): %d (%.1f%%)", closeMatches, 100.0*float64(closeMatches)/float64(alignedLen))
	t.Logf("  SNR: %.1f dB", snr)

	// Also compare without alignment to show the difference
	t.Log("\nWithout alignment (first 20 samples):")
	var sumSqErr2, sumSqSig2 float64
	exactMatches2 := 0
	for i := 0; i < alignedLen && i < 20; i++ {
		goVal := goNative[i]
		libVal := libPcm[i]
		diff := goVal - libVal
		sumSqErr2 += float64(diff * diff)
		sumSqSig2 += float64(libVal * libVal)
		if diff == 0 {
			exactMatches2++
		}
		goInt16 := int16(goVal * 32768)
		libInt16 := int16(libVal * 32768)
		diffInt16 := goInt16 - libInt16
		t.Logf("  go[%3d] vs lib[%3d]: go=%7d lib=%7d diff=%4d",
			i, i, goInt16, libInt16, diffInt16)
	}
	for i := 20; i < alignedLen; i++ {
		goVal := goNative[i]
		libVal := libPcm[i]
		diff := goVal - libVal
		sumSqErr2 += float64(diff * diff)
		sumSqSig2 += float64(libVal * libVal)
		if diff == 0 {
			exactMatches2++
		}
	}
	snr2 := 10 * math.Log10(sumSqSig2/sumSqErr2)
	if math.IsNaN(snr2) || math.IsInf(snr2, 1) {
		snr2 = 999.0
	}
	t.Logf("\nWithout alignment:")
	t.Logf("  Exact matches: %d (%.1f%%)", exactMatches2, 100.0*float64(exactMatches2)/float64(alignedLen))
	t.Logf("  SNR: %.1f dB", snr2)
}

// TestSilkMultiPacketAligned tests multiple packets with delay alignment.
func TestSilkMultiPacketAligned(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 10)
	if err != nil || len(packets) < 5 {
		t.Skip("Could not load packets")
	}

	for pktIdx := 0; pktIdx < 5; pktIdx++ {
		pkt := packets[pktIdx]
		toc := gopus.ParseTOC(pkt[0])
		if toc.Mode != gopus.ModeSILK {
			t.Logf("Packet %d: Not SILK mode, skipping", pktIdx)
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}
		duration := silk.FrameDurationFromTOC(toc.FrameSize)
		config := silk.GetBandwidthConfig(silkBW)

		// Delay based on sample rate
		delay := 5
		if config.SampleRate == 16000 {
			delay = 13
		} else if config.SampleRate == 12000 {
			delay = 10
		}

		// gopus decode with fresh decoder for each packet
		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		goDec := silk.NewDecoder()
		goNative, err := goDec.DecodeFrame(&rd, silkBW, duration, true)
		if err != nil {
			t.Logf("Packet %d: gopus decode failed: %v", pktIdx, err)
			continue
		}

		// libopus decode with fresh decoder
		libDec, _ := NewLibopusDecoder(config.SampleRate, 1)
		if libDec == nil {
			continue
		}
		libPcm, libSamples := libDec.DecodeFloat(pkt, 960)
		libDec.Destroy()
		if libSamples < 0 {
			continue
		}

		// Compare with alignment
		alignedLen := len(goNative)
		if libSamples-delay < alignedLen {
			alignedLen = libSamples - delay
		}

		var sumSqErr, sumSqSig float64
		exactMatches := 0
		for i := 0; i < alignedLen; i++ {
			goVal := goNative[i]
			libVal := libPcm[i+delay]
			diff := goVal - libVal
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libVal * libVal)
			if diff == 0 {
				exactMatches++
			}
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		t.Logf("Packet %d: aligned SNR=%.1f dB, exact=%.1f%%",
			pktIdx, snr, 100.0*float64(exactMatches)/float64(alignedLen))
	}
}
