package testvectors

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTV06DetailedAround1252 investigates packets around the frame size transition.
func TestTV06DetailedAround1252(t *testing.T) {
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

	dec, _ := gopus.NewDecoderDefault(48000, 2)

	refOffset := 0
	var cumulativeSigPow, cumulativeNoisePow float64

	for i, pkt := range packets {
		if len(pkt.Data) == 0 {
			continue
		}

		pcm, err := dec.DecodeInt16Slice(pkt.Data)
		if err != nil {
			continue
		}

		if refOffset+len(pcm) > len(reference) {
			break
		}

		refSlice := reference[refOffset : refOffset+len(pcm)]

		var sigPow, noisePow float64
		var maxDiff float64
		maxDiffIdx := 0
		for j := 0; j < len(pcm); j++ {
			sig := float64(refSlice[j])
			noise := float64(pcm[j]) - sig
			sigPow += sig * sig
			noisePow += noise * noise
			cumulativeSigPow += sig * sig
			cumulativeNoisePow += noise * noise
			if math.Abs(noise) > maxDiff {
				maxDiff = math.Abs(noise)
				maxDiffIdx = j
			}
		}

		snr := 10 * math.Log10(sigPow/noisePow)
		q := (snr - 48.0) * (100.0 / 48.0)

		// Cumulative quality
		cumSNR := 10 * math.Log10(cumulativeSigPow/cumulativeNoisePow)
		cumQ := (cumSNR - 48.0) * (100.0 / 48.0)

		// Show packets 1245-1270
		if i >= 1245 && i <= 1270 {
			toc := pkt.Data[0]
			config := toc >> 3
			stereo := (toc & 0x04) != 0
			frameSize := getFrameSizeFromConfig(config)

			marker := ""
			if i == 1252 {
				marker = " <-- FRAME SIZE CHANGE 20ms->10ms"
			}
			t.Logf("Packet %4d: Q=%8.2f (cumQ=%.2f), maxDiff=%6.0f at %4d, fs=%3d, st=%v%s",
				i, q, cumQ, maxDiff, maxDiffIdx, frameSize/48, stereo, marker)
		}

		refOffset += len(pcm)
	}
}

// TestTV06WorsePeriod shows packets in the 1300-1500 period.
func TestTV06WorsePeriod(t *testing.T) {
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

	dec, _ := gopus.NewDecoderDefault(48000, 2)

	refOffset := 0
	worstQ := 100.0
	worstPacket := 0
	worstDiff := 0.0

	for i, pkt := range packets {
		if len(pkt.Data) == 0 {
			continue
		}

		pcm, err := dec.DecodeInt16Slice(pkt.Data)
		if err != nil {
			continue
		}

		if refOffset+len(pcm) > len(reference) {
			break
		}

		refSlice := reference[refOffset : refOffset+len(pcm)]

		var sigPow, noisePow float64
		var maxDiff float64
		for j := 0; j < len(pcm); j++ {
			sig := float64(refSlice[j])
			noise := float64(pcm[j]) - sig
			sigPow += sig * sig
			noisePow += noise * noise
			if math.Abs(noise) > maxDiff {
				maxDiff = math.Abs(noise)
			}
		}

		snr := 10 * math.Log10(sigPow/noisePow)
		q := (snr - 48.0) * (100.0 / 48.0)

		// Track worst packet in 1300-1700 range
		if i >= 1300 && i <= 1700 {
			if q < worstQ {
				worstQ = q
				worstPacket = i
				worstDiff = maxDiff
			}
		}

		refOffset += len(pcm)
	}

	t.Logf("Worst packet in 1300-1700 range:")
	t.Logf("  Packet %d: Q=%.2f, maxDiff=%.0f", worstPacket, worstQ, worstDiff)

	// Now show detail around that packet
	dec2, _ := gopus.NewDecoderDefault(48000, 2)
	refOffset = 0
	for i, pkt := range packets {
		if len(pkt.Data) == 0 {
			continue
		}

		pcm, err := dec2.DecodeInt16Slice(pkt.Data)
		if err != nil {
			continue
		}

		if refOffset+len(pcm) > len(reference) {
			break
		}

		refSlice := reference[refOffset : refOffset+len(pcm)]

		if i >= worstPacket-5 && i <= worstPacket+5 {
			var sigPow, noisePow float64
			var maxDiff float64
			maxDiffIdx := 0
			for j := 0; j < len(pcm); j++ {
				sig := float64(refSlice[j])
				noise := float64(pcm[j]) - sig
				sigPow += sig * sig
				noisePow += noise * noise
				if math.Abs(noise) > maxDiff {
					maxDiff = math.Abs(noise)
					maxDiffIdx = j
				}
			}

			snr := 10 * math.Log10(sigPow/noisePow)
			q := (snr - 48.0) * (100.0 / 48.0)

			toc := pkt.Data[0]
			config := toc >> 3
			stereo := (toc & 0x04) != 0
			frameSize := getFrameSizeFromConfig(config)

			marker := ""
			if i == worstPacket {
				marker = " <-- WORST"
			}
			t.Logf("Packet %4d: Q=%8.2f, maxDiff=%6.0f at %4d, fs=%3d, st=%v%s",
				i, q, maxDiff, maxDiffIdx, frameSize/48, stereo, marker)
		}

		refOffset += len(pcm)
	}
}

// TestTV06FreshDecoderAtBadPackets tries decoding with fresh decoder at problem packets.
func TestTV06FreshDecoderAtBadPackets(t *testing.T) {
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

	// Test at various starting points
	startPoints := []int{1495, 1496, 1497, 1498}

	for _, startPkt := range startPoints {
		dec, _ := gopus.NewDecoderDefault(48000, 2)

		// Calculate reference offset
		refOffset := 0
		for i := 0; i < startPkt && i < len(packets); i++ {
			pkt := packets[i]
			if len(pkt.Data) == 0 {
				continue
			}
			toc := pkt.Data[0]
			config := toc >> 3
			frameSize := getFrameSizeFromConfig(config)
			frameCode := toc & 0x03
			frameCount := 1
			switch frameCode {
			case 0:
				frameCount = 1
			case 1, 2:
				frameCount = 2
			case 3:
				if len(pkt.Data) > 1 {
					frameCount = int(pkt.Data[1] & 0x3F)
					if frameCount == 0 {
						frameCount = 1
					}
				}
			}
			refOffset += frameSize * frameCount * 2 // stereo
		}

		// Decode 10 packets from start point
		var totalSigPow, totalNoisePow float64
		var maxDiffTotal float64
		for i := startPkt; i < startPkt+10 && i < len(packets); i++ {
			pkt := packets[i]
			if len(pkt.Data) == 0 {
				continue
			}
			pcm, err := dec.DecodeInt16Slice(pkt.Data)
			if err != nil {
				continue
			}

			if refOffset+len(pcm) > len(reference) {
				break
			}

			refSlice := reference[refOffset : refOffset+len(pcm)]
			for j := 0; j < len(pcm); j++ {
				sig := float64(refSlice[j])
				noise := float64(pcm[j]) - sig
				totalSigPow += sig * sig
				totalNoisePow += noise * noise
				if math.Abs(noise) > maxDiffTotal {
					maxDiffTotal = math.Abs(noise)
				}
			}

			refOffset += len(pcm)
		}

		snr := 10 * math.Log10(totalSigPow/totalNoisePow)
		q := (snr - 48.0) * (100.0 / 48.0)
		t.Logf("Fresh decoder starting at packet %d: Q=%.2f, maxDiff=%.0f", startPkt, q, maxDiffTotal)
	}
}
