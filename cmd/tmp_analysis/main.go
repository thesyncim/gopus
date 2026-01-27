package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/thesyncim/gopus"
)

func main() {
	vectorPath := "internal/testvectors/testdata/opus_testvectors"
	bitFile := filepath.Join(vectorPath, "testvector07.bit")
	decFile := filepath.Join(vectorPath, "testvector07.dec")

	packets, err := readBitstreamFile(bitFile)
	if err != nil {
		fmt.Printf("Error reading bit file: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Read %d packets\n", len(packets))

	reference, err := readPCMFile(decFile)
	if err != nil {
		fmt.Printf("Error reading dec file: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Reference has %d samples (stereo format)\n", len(reference))

	// Create both mono and stereo decoders
	monoDec, err := gopus.NewDecoder(48000, 1)
	if err != nil {
		fmt.Printf("Error creating mono decoder: %v\n", err)
		os.Exit(1)
	}
	stereoDec, err := gopus.NewDecoder(48000, 2)
	if err != nil {
		fmt.Printf("Error creating stereo decoder: %v\n", err)
		os.Exit(1)
	}

	// Track per-packet quality
	var allDecoded []int16
	offset := 0 // stereo sample offset

	fmt.Println("\n=== Per-Packet Quality Analysis ===")
	fmt.Printf("%-6s %-8s %-8s %-6s %-6s %-8s %-10s %-10s %-10s\n",
		"Pkt#", "Config", "Mode", "Stereo", "Size", "Samples", "SignalE", "NoiseE", "SNR(dB)")

	type badPacket struct {
		idx     int
		offset  int
		config  int
		stereo  bool
		size    int
		samples int
		snr     float64
		signalE float64
		noiseE  float64
	}
	var badPackets []badPacket

	for i, pkt := range packets {
		if len(pkt) == 0 {
			continue
		}

		pktTOC := gopus.ParseTOC(pkt[0])
		config := pkt[0] >> 3
		stereo := pktTOC.Stereo
		var pcm []int16

		if stereo {
			pcm, err = stereoDec.DecodeInt16Slice(pkt)
			if err != nil {
				fs := getFrameSize(config)
				pcm = make([]int16, fs*2)
			}
		} else {
			monoSamples, decErr := monoDec.DecodeInt16Slice(pkt)
			if decErr != nil {
				fs := getFrameSize(config)
				pcm = make([]int16, fs*2)
			} else {
				// Duplicate mono to stereo
				pcm = make([]int16, len(monoSamples)*2)
				for j, s := range monoSamples {
					pcm[2*j] = s
					pcm[2*j+1] = s
				}
			}
		}

		// Compute per-packet quality
		n := len(pcm)
		if offset+n > len(reference) {
			n = len(reference) - offset
		}
		if n <= 0 {
			allDecoded = append(allDecoded, pcm...)
			offset += len(pcm)
			continue
		}

		var signalE, noiseE float64
		for j := 0; j < n; j++ {
			ref := float64(reference[offset+j])
			dec := float64(pcm[j])
			signalE += ref * ref
			noise := dec - ref
			noiseE += noise * noise
		}

		snr := -999.0
		if signalE > 0 && noiseE > 0 {
			snr = 10.0 * math.Log10(signalE/noiseE)
		}

		// Track bad packets (low SNR with significant signal)
		if signalE > 1e6 && snr < 20 {
			badPackets = append(badPackets, badPacket{
				idx:     i,
				offset:  offset,
				config:  int(config),
				stereo:  stereo,
				size:    len(pkt),
				samples: len(pcm),
				snr:     snr,
				signalE: signalE,
				noiseE:  noiseE,
			})
		}

		// Print interesting packets
		if i < 10 || (signalE > 1e6 && snr < 20) || (i >= len(packets)-10) {
			mode := "CELT"
			if config < 12 {
				mode = "SILK"
			} else if config < 16 {
				mode = "Hybrid"
			}
			stereoStr := "mono"
			if stereo {
				stereoStr = "stereo"
			}
			fmt.Printf("%-6d %-8d %-8s %-6s %-6d %-8d %-10.2e %-10.2e %-10.2f\n",
				i, config, mode, stereoStr, len(pkt), len(pcm), signalE, noiseE, snr)
		}

		allDecoded = append(allDecoded, pcm...)
		offset += len(pcm)
	}

	fmt.Printf("\n=== BAD PACKETS (SNR < 20 dB with signal > 1e6) ===\n")
	fmt.Printf("Found %d bad packets\n\n", len(badPackets))

	for _, bp := range badPackets[:minInt(20, len(badPackets))] {
		mode := "CELT"
		if bp.config < 12 {
			mode = "SILK"
		} else if bp.config < 16 {
			mode = "Hybrid"
		}
		stereoStr := "mono"
		if bp.stereo {
			stereoStr = "stereo"
		}
		fmt.Printf("Pkt %d: offset=%d, config=%d (%s), %s, size=%d, samples=%d, SNR=%.2f dB\n",
			bp.idx, bp.offset, bp.config, mode, stereoStr, bp.size, bp.samples, bp.snr)

		// Show sample divergence
		pkt := packets[bp.idx]
		var pcm []int16
		if bp.stereo {
			pcm, _ = stereoDec.DecodeInt16Slice(pkt)
		} else {
			monoSamples, _ := monoDec.DecodeInt16Slice(pkt)
			if monoSamples != nil {
				pcm = make([]int16, len(monoSamples)*2)
				for j, s := range monoSamples {
					pcm[2*j] = s
					pcm[2*j+1] = s
				}
			}
		}
		if pcm != nil && bp.offset+10 < len(reference) {
			fmt.Printf("  First 5 sample pairs at offset %d:\n", bp.offset)
			for k := 0; k < 10 && k < len(pcm); k++ {
				refIdx := bp.offset + k
				if refIdx < len(reference) {
					fmt.Printf("    [%d] dec=%6d, ref=%6d, diff=%6d\n", k, pcm[k], reference[refIdx], int(pcm[k])-int(reference[refIdx]))
				}
			}
		}
	}

	// Analyze transition points (mono to stereo)
	fmt.Println("\n=== MONO/STEREO TRANSITIONS ===")
	prevStereo := false
	for i, pkt := range packets {
		if len(pkt) == 0 {
			continue
		}
		pktTOC := gopus.ParseTOC(pkt[0])
		if pktTOC.Stereo != prevStereo {
			config := pkt[0] >> 3
			mode := "CELT"
			if config < 12 {
				mode = "SILK"
			} else if config < 16 {
				mode = "Hybrid"
			}
			fmt.Printf("Packet %d: %s -> %s (config=%d, %s)\n",
				i, boolStr(prevStereo), boolStr(pktTOC.Stereo), config, mode)
			prevStereo = pktTOC.Stereo
		}
	}

	// Analyze frame size distribution
	fmt.Println("\n=== FRAME SIZE DISTRIBUTION ===")
	frameSizeCounts := make(map[int]int)
	for _, pkt := range packets {
		if len(pkt) > 0 {
			config := pkt[0] >> 3
			fs := getFrameSize(config)
			frameSizeCounts[fs]++
		}
	}
	for fs, count := range frameSizeCounts {
		fmt.Printf("  %d samples (%.1fms): %d packets\n", fs, float64(fs)/48.0, count)
	}

	// Focus on the stereo transition area (packet 2128 and around)
	fmt.Println("\n=== STEREO TRANSITION ANALYSIS (packets 2125-2135) ===")

	fmt.Println("\n--- Using SINGLE STEREO DECODER for all packets ---")
	// Use a single stereo decoder for ALL packets (both mono and stereo)
	singleDec, _ := gopus.NewDecoder(48000, 2)

	// Decode ALL packets and compute overall quality
	allDecoded = nil // Reset for single-decoder test
	for _, pkt := range packets {
		if len(pkt) == 0 {
			continue
		}
		pcm, err := singleDec.DecodeInt16Slice(pkt)
		if err != nil {
			config := pkt[0] >> 3
			fs := getFrameSize(config)
			pcm = make([]int16, fs*2)
		}
		allDecoded = append(allDecoded, pcm...)
	}

	fmt.Printf("Total decoded: %d samples, Reference: %d samples\n", len(allDecoded), len(reference))

	// Compute overall quality
	n := len(allDecoded)
	if len(reference) < n {
		n = len(reference)
	}
	var signalE, noiseE float64
	for i := 0; i < n; i++ {
		ref := float64(reference[i])
		dec := float64(allDecoded[i])
		signalE += ref * ref
		noise := dec - ref
		noiseE += noise * noise
	}
	if signalE > 0 && noiseE > 0 {
		snr := 10.0 * math.Log10(signalE/noiseE)
		q := (snr - 48.0)
		fmt.Printf("Overall SNR=%.2f dB, Q=%.2f (threshold: Q >= 0)\n", snr, q)
		if q >= 0 {
			fmt.Println("PASS: Quality meets RFC 8251 threshold!")
		} else {
			fmt.Println("FAIL: Quality below threshold")
		}
	}

	// Find first 10 samples with significant difference
	fmt.Println("\nFirst 10 samples with |diff| > 100:")
	count := 0
	for i := 0; i < n && count < 10; i++ {
		diff := int(allDecoded[i]) - int(reference[i])
		if diff > 100 || diff < -100 {
			fmt.Printf("  [%d] dec=%6d, ref=%6d, diff=%6d\n", i, allDecoded[i], reference[i], diff)
			count++
		}
	}
}

func boolStr(b bool) string {
	if b {
		return "stereo"
	}
	return "mono"
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func getFrameSize(config byte) int {
	frameSizes := []int{
		480, 960, 1920, 2880,
		480, 960, 1920, 2880,
		480, 960, 1920, 2880,
		480, 960,
		480, 960,
		120, 240, 480, 960,
		120, 240, 480, 960,
		120, 240, 480, 960,
		120, 240, 480, 960,
	}
	if int(config) < len(frameSizes) {
		return frameSizes[config]
	}
	return 960
}

func readPCMFile(filename string) ([]int16, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	samples := make([]int16, len(data)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(data[i*2:]))
	}
	return samples, nil
}

// Simple bitstream parser (opus_demo format uses big-endian)
func readBitstreamFile(filename string) ([][]byte, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	var packets [][]byte
	offset := 0
	for offset < len(data) {
		if offset+8 > len(data) {
			break
		}
		// Read packet length (4 bytes big endian)
		pktLen := int(binary.BigEndian.Uint32(data[offset:]))
		offset += 4

		// Skip enc_final_range (4 bytes)
		offset += 4

		// Read packet data
		if offset+pktLen > len(data) {
			break
		}
		pkt := make([]byte, pktLen)
		copy(pkt, data[offset:offset+pktLen])
		packets = append(packets, pkt)
		offset += pktLen
	}

	return packets, nil
}
