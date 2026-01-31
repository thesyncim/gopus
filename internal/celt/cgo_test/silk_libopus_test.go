// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestSilkVsLibopus compares SILK mode decoding against libopus
func TestSilkVsLibopus(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets := loadPackets(t, bitFile, 0)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	channels := 1 // testvector02 is mono SILK

	// Create persistent decoders
	goDec, err := gopus.NewDecoderDefault(48000, channels)
	if err != nil {
		t.Fatalf("Failed to create gopus decoder: %v", err)
	}

	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder")
	}
	defer libDec.Destroy()

	t.Logf("Loaded %d packets from testvector02", len(packets))

	var totalSig, totalNoise float64
	badPackets := 0

	// Decode and compare all packets
	for i, pkt := range packets {
		toc := gopus.ParseTOC(pkt[0])

		// Decode with gopus
		goPcm, decErr := goDec.DecodeFloat32(pkt)
		if decErr != nil {
			t.Logf("Packet %d: gopus error: %v", i, decErr)
			continue
		}

		// Decode with libopus
		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
		if libSamples < 0 {
			t.Logf("Packet %d: libopus error: %d", i, libSamples)
			continue
		}

		// Compare
		goSamples := len(goPcm) / channels
		if goSamples != libSamples {
			t.Logf("Packet %d: sample count mismatch gopus=%d libopus=%d", i, goSamples, libSamples)
			continue
		}

		// Calculate per-packet SNR
		var sigPow, noisePow float64
		for j := 0; j < goSamples*channels; j++ {
			sig := float64(libPcm[j])
			noise := float64(goPcm[j]) - sig
			sigPow += sig * sig
			noisePow += noise * noise
		}

		totalSig += sigPow
		totalNoise += noisePow

		snr := 10 * math.Log10(sigPow/noisePow)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999
		}

		// Log first 20 packets and any bad packets
		if i < 20 || snr < 40 {
			t.Logf("Packet %d: mode=%v, stereo=%v, frameSize=%d, SNR=%.1f dB",
				i, toc.Mode, toc.Stereo, toc.FrameSize, snr)
			if snr < 40 {
				badPackets++
			}
		}
	}

	overallSNR := 10 * math.Log10(totalSig/totalNoise)
	t.Logf("\nOverall SNR: %.2f dB", overallSNR)
	t.Logf("Bad packets (SNR < 40 dB): %d / %d", badPackets, len(packets))
}

// TestSilkFirstPacketDetail examines the first packet in detail
func TestSilkFirstPacketDetail(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets := loadPackets(t, bitFile, 5)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	channels := 1

	// Create decoders
	goDec, _ := gopus.NewDecoderDefault(48000, channels)
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder")
	}
	defer libDec.Destroy()

	pkt := packets[0]
	toc := gopus.ParseTOC(pkt[0])
	t.Logf("Packet 0: %d bytes, stereo=%v, frameSize=%d, mode=%v, bw=%d",
		len(pkt), toc.Stereo, toc.FrameSize, toc.Mode, toc.Bandwidth)

	// Show first few bytes
	t.Logf("First bytes: %02x %02x %02x %02x %02x",
		pkt[0], pkt[1],
		func() byte {
			if len(pkt) > 2 {
				return pkt[2]
			}
			return 0
		}(),
		func() byte {
			if len(pkt) > 3 {
				return pkt[3]
			}
			return 0
		}(),
		func() byte {
			if len(pkt) > 4 {
				return pkt[4]
			}
			return 0
		}())

	// Decode with gopus
	goPcm, decErr := goDec.DecodeFloat32(pkt)
	if decErr != nil {
		t.Fatalf("gopus decode failed: %v", decErr)
	}

	// Decode with libopus
	libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
	if libSamples < 0 {
		t.Fatalf("libopus decode failed: %d", libSamples)
	}

	goSamples := len(goPcm) / channels
	t.Logf("gopus samples: %d, libopus samples: %d", goSamples, libSamples)

	// Show first 50 samples
	t.Log("\nFirst 50 samples comparison:")
	t.Log("Index\tgopus\t\tlibopus\t\tdiff")
	for i := 0; i < minInt(50, goSamples); i++ {
		goVal := goPcm[i]
		libVal := libPcm[i]
		diff := goVal - libVal
		marker := ""
		if math.Abs(float64(diff)) > 0.001 {
			marker = " *"
		}
		t.Logf("%d\t%.6f\t%.6f\t%.6f%s", i, goVal, libVal, diff, marker)
	}

	// Calculate SNR
	var sigPow, noisePow float64
	for j := 0; j < goSamples*channels; j++ {
		sig := float64(libPcm[j])
		noise := float64(goPcm[j]) - sig
		sigPow += sig * sig
		noisePow += noise * noise
	}
	snr := 10 * math.Log10(sigPow/noisePow)
	t.Logf("\nPacket 0 SNR: %.1f dB", snr)
}

// TestSilkPacket1Detail examines packet 1 (first non-silent packet) in detail
func TestSilkPacket1Detail(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets := loadPackets(t, bitFile, 5)
	if len(packets) < 2 {
		t.Skip("Could not load enough test packets")
	}

	channels := 1

	// Create decoders
	goDec, _ := gopus.NewDecoderDefault(48000, channels)
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder")
	}
	defer libDec.Destroy()

	// Decode packet 0 first (to initialize state)
	goDec.DecodeFloat32(packets[0])
	libDec.DecodeFloat(packets[0], 5760)

	// Now examine packet 1
	pkt := packets[1]
	toc := gopus.ParseTOC(pkt[0])
	t.Logf("Packet 1: %d bytes, stereo=%v, frameSize=%d, mode=%v, bw=%d",
		len(pkt), toc.Stereo, toc.FrameSize, toc.Mode, toc.Bandwidth)

	// Decode with gopus
	goPcm, decErr := goDec.DecodeFloat32(pkt)
	if decErr != nil {
		t.Fatalf("gopus decode failed: %v", decErr)
	}

	// Decode with libopus
	libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
	if libSamples < 0 {
		t.Fatalf("libopus decode failed: %d", libSamples)
	}

	goSamples := len(goPcm) / channels
	t.Logf("gopus samples: %d, libopus samples: %d", goSamples, libSamples)

	// Show first 100 samples
	t.Log("\nFirst 100 samples comparison:")
	t.Log("Index\tgopus\t\tlibopus\t\tdiff\t\tratio")
	for i := 0; i < minInt(100, goSamples); i++ {
		goVal := goPcm[i]
		libVal := libPcm[i]
		diff := goVal - libVal
		ratio := float32(0)
		if libVal != 0 {
			ratio = goVal / libVal
		}
		marker := ""
		if math.Abs(float64(diff)) > 0.001 {
			marker = " *"
		}
		t.Logf("%d\t%.6f\t%.6f\t%.6f\t%.4f%s", i, goVal, libVal, diff, ratio, marker)
	}

	// Find where significant differences start
	firstDiff := -1
	for i := 0; i < minInt(goSamples, libSamples); i++ {
		if math.Abs(float64(goPcm[i]-libPcm[i])) > 0.0001 {
			firstDiff = i
			break
		}
	}
	t.Logf("\nFirst significant difference at sample: %d", firstDiff)

	// Calculate SNR in windows
	windowSize := 480 // 10ms at 48kHz
	t.Log("\nSNR per 10ms window:")
	for start := 0; start+windowSize <= goSamples; start += windowSize {
		var sigPow, noisePow float64
		for j := start; j < start+windowSize; j++ {
			sig := float64(libPcm[j])
			noise := float64(goPcm[j]) - sig
			sigPow += sig * sig
			noisePow += noise * noise
		}
		snr := 10 * math.Log10(sigPow/noisePow)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999
		}
		t.Logf("  [%d-%d]: SNR=%.1f dB", start, start+windowSize-1, snr)
	}

	// Overall SNR
	var sigPow, noisePow float64
	for j := 0; j < minInt(goSamples, libSamples); j++ {
		sig := float64(libPcm[j])
		noise := float64(goPcm[j]) - sig
		sigPow += sig * sig
		noisePow += noise * noise
	}
	snr := 10 * math.Log10(sigPow/noisePow)
	t.Logf("\nPacket 1 Overall SNR: %.1f dB", snr)

	// Show samples around where the difference starts
	if firstDiff > 0 {
		t.Logf("\nSamples around first divergence (sample %d):", firstDiff)
		showStart := maxInt2(0, firstDiff-10)
		showEnd := minInt(goSamples, firstDiff+30)
		for i := showStart; i < showEnd; i++ {
			goVal := goPcm[i]
			libVal := libPcm[i]
			diff := goVal - libVal
			marker := ""
			if math.Abs(float64(diff)) > 0.0001 {
				marker = " <-- DIFF"
			}
			t.Logf("  [%d] go=%.6f lib=%.6f diff=%.6f%s", i, goVal, libVal, diff, marker)
		}
	}
}

func maxInt2(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func loadSilkPackets(t *testing.T, bitFile string, maxPackets int) [][]byte {
	t.Helper()

	data, err := os.ReadFile(bitFile)
	if err != nil {
		t.Logf("Cannot read %s: %v", bitFile, err)
		return nil
	}

	var packets [][]byte
	offset := 0
	for offset < len(data)-8 {
		if maxPackets > 0 && len(packets) >= maxPackets {
			break
		}
		pktLen := binary.BigEndian.Uint32(data[offset:])
		offset += 4
		_ = binary.BigEndian.Uint32(data[offset:])
		offset += 4

		if int(pktLen) <= 0 || offset+int(pktLen) > len(data) {
			break
		}

		packets = append(packets, data[offset:offset+int(pktLen)])
		offset += int(pktLen)
	}

	return packets
}
