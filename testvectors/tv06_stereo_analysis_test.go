package testvectors

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTV06StereoChannelAnalysis analyzes L/R channel errors separately.
// The compliance findings document mentions diff_M = -diff_S pattern for stereo issues.
func TestTV06StereoChannelAnalysis(t *testing.T) {
	bitFile := filepath.Join(testVectorDir, "testvector06.bit")
	decFile := filepath.Join(testVectorDir, "testvector06.dec")

	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Skipf("Could not read test vector: %v", err)
	}

	reference, err := readPCMFile(decFile)
	if err != nil {
		t.Skipf("Could not read reference: %v", err)
	}

	dec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))

	// Track per-packet stats for both channels
	refOffset := 0
	for i, pkt := range packets {
		if len(pkt.Data) == 0 {
			continue
		}

		pcm, err := decodeInt16(dec, pkt.Data)
		if err != nil {
			continue
		}

		if refOffset+len(pcm) > len(reference) {
			break
		}

		refSlice := reference[refOffset : refOffset+len(pcm)]

		// Calculate per-channel stats (interleaved: L, R, L, R, ...)
		var sigPowL, noisePowL float64
		var sigPowR, noisePowR float64
		var maxDiffL, maxDiffR float64
		for j := 0; j < len(pcm)/2; j++ {
			// Left channel
			sigL := float64(refSlice[j*2])
			noiseL := float64(pcm[j*2]) - sigL
			sigPowL += sigL * sigL
			noisePowL += noiseL * noiseL
			if math.Abs(noiseL) > maxDiffL {
				maxDiffL = math.Abs(noiseL)
			}

			// Right channel
			sigR := float64(refSlice[j*2+1])
			noiseR := float64(pcm[j*2+1]) - sigR
			sigPowR += sigR * sigR
			noisePowR += noiseR * noiseR
			if math.Abs(noiseR) > maxDiffR {
				maxDiffR = math.Abs(noiseR)
			}
		}

		snrL := 10 * math.Log10(sigPowL/noisePowL)
		snrR := 10 * math.Log10(sigPowR/noisePowR)
		qL := (snrL - 48.0) * (100.0 / 48.0)
		qR := (snrR - 48.0) * (100.0 / 48.0)

		// Log packets around the problem area (1494-1505)
		if i >= 1494 && i <= 1505 {
			toc := pkt.Data[0]
			stereo := (toc & 0x04) != 0
			t.Logf("Packet %4d: L: Q=%8.2f maxDiff=%6.0f | R: Q=%8.2f maxDiff=%6.0f | stereo=%v",
				i, qL, maxDiffL, qR, maxDiffR, stereo)
		}

		refOffset += len(pcm)
	}
}

// TestTV06MonoVsNonMonoPackets compares quality of mono vs stereo packets.
func TestTV06MonoVsNonMonoPackets(t *testing.T) {
	bitFile := filepath.Join(testVectorDir, "testvector06.bit")
	decFile := filepath.Join(testVectorDir, "testvector06.dec")

	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Skipf("Could not read test vector: %v", err)
	}

	reference, err := readPCMFile(decFile)
	if err != nil {
		t.Skipf("Could not read reference: %v", err)
	}

	dec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))

	var monoSumQ, stereoSumQ float64
	var monoCount, stereoCount int

	refOffset := 0
	for _, pkt := range packets {
		if len(pkt.Data) == 0 {
			continue
		}

		toc := pkt.Data[0]
		stereo := (toc & 0x04) != 0

		pcm, err := decodeInt16(dec, pkt.Data)
		if err != nil {
			continue
		}

		if refOffset+len(pcm) > len(reference) {
			break
		}

		refSlice := reference[refOffset : refOffset+len(pcm)]

		var sigPow, noisePow float64
		for j := 0; j < len(pcm); j++ {
			sig := float64(refSlice[j])
			noise := float64(pcm[j]) - sig
			sigPow += sig * sig
			noisePow += noise * noise
		}

		snr := 10 * math.Log10(sigPow/noisePow)
		q := (snr - 48.0) * (100.0 / 48.0)

		if stereo {
			stereoSumQ += q
			stereoCount++
		} else {
			monoSumQ += q
			monoCount++
		}

		refOffset += len(pcm)
	}

	if monoCount > 0 {
		avgMonoQ := monoSumQ / float64(monoCount)
		t.Logf("Mono packets: %d, avg Q=%.2f", monoCount, avgMonoQ)
	}
	if stereoCount > 0 {
		avgStereoQ := stereoSumQ / float64(stereoCount)
		t.Logf("Stereo packets: %d, avg Q=%.2f", stereoCount, avgStereoQ)
	}
}

// TestTV06StereoPacketDistribution checks when stereo packets appear.
func TestTV06StereoPacketDistribution(t *testing.T) {
	bitFile := filepath.Join(testVectorDir, "testvector06.bit")

	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Skipf("Could not read test vector: %v", err)
	}

	var monoPackets, stereoPackets []int
	prevStereo := false

	for i, pkt := range packets {
		if len(pkt.Data) == 0 {
			continue
		}

		toc := pkt.Data[0]
		stereo := (toc & 0x04) != 0

		if stereo {
			stereoPackets = append(stereoPackets, i)
		} else {
			monoPackets = append(monoPackets, i)
		}

		if stereo != prevStereo {
			t.Logf("Stereo change at packet %d: %v -> %v", i, prevStereo, stereo)
		}
		prevStereo = stereo
	}

	t.Logf("Total mono packets: %d", len(monoPackets))
	t.Logf("Total stereo packets: %d", len(stereoPackets))

	// Show first few of each
	if len(monoPackets) > 0 && len(monoPackets) <= 20 {
		t.Logf("Mono packet indices: %v", monoPackets)
	}
	if len(stereoPackets) > 0 && len(stereoPackets) <= 20 {
		t.Logf("Stereo packet indices (first 20): %v", stereoPackets[:min(20, len(stereoPackets))])
	}
}

// TestTV06CumulativeQualityByRegion shows Q by packet region.
func TestTV06CumulativeQualityByRegion(t *testing.T) {
	bitFile := filepath.Join(testVectorDir, "testvector06.bit")
	decFile := filepath.Join(testVectorDir, "testvector06.dec")

	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Skipf("Could not read test vector: %v", err)
	}

	reference, err := readPCMFile(decFile)
	if err != nil {
		t.Skipf("Could not read reference: %v", err)
	}

	dec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))

	// Regions to analyze
	regions := []struct {
		start int
		end   int
		name  string
	}{
		{0, 500, "0-500"},
		{500, 1000, "500-1000"},
		{1000, 1250, "1000-1250"},
		{1250, 1500, "1250-1500"},
		{1500, 1700, "1500-1700"},
		{1700, 1876, "1700-end"},
	}

	refOffset := 0
	currentRegion := 0
	var regionSigPow, regionNoisePow float64

	for i, pkt := range packets {
		if len(pkt.Data) == 0 {
			continue
		}

		pcm, err := decodeInt16(dec, pkt.Data)
		if err != nil {
			continue
		}

		if refOffset+len(pcm) > len(reference) {
			break
		}

		refSlice := reference[refOffset : refOffset+len(pcm)]

		// Accumulate for current region
		for j := 0; j < len(pcm); j++ {
			sig := float64(refSlice[j])
			noise := float64(pcm[j]) - sig
			regionSigPow += sig * sig
			regionNoisePow += noise * noise
		}

		// Check if we've completed a region
		if currentRegion < len(regions) && i >= regions[currentRegion].end-1 {
			snr := 10 * math.Log10(regionSigPow/regionNoisePow)
			q := (snr - 48.0) * (100.0 / 48.0)
			t.Logf("Region %s: Q=%.2f (SNR=%.2f dB)", regions[currentRegion].name, q, snr)

			// Reset for next region
			regionSigPow = 0
			regionNoisePow = 0
			currentRegion++
		}

		refOffset += len(pcm)
	}
}
