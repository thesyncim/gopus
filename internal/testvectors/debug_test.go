package testvectors

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus"
)

func TestDebugDecode(t *testing.T) {
	vectorPath := "testdata/opus_testvectors"
	bitFile := filepath.Join(vectorPath, "testvector07.bit")
	decFile := filepath.Join(vectorPath, "testvector07.dec")

	// Read packets using the proper parser
	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Fatalf("Error reading bit file: %v", err)
	}
	t.Logf("Read %d packets", len(packets))

	// Read reference
	reference, err := readDebugPCMFile(decFile)
	if err != nil {
		t.Fatalf("Error reading dec file: %v", err)
	}
	t.Logf("Reference has %d samples (stereo format)", len(reference))

	// Decode first few packets
	if len(packets) == 0 {
		t.Fatal("No packets")
	}

	toc := packets[0].Data[0]
	stereo := (toc & 0x04) != 0
	channels := 1
	if stereo {
		channels = 2
	}
	t.Logf("TOC=0x%02X, Channels=%d", toc, channels)

	dec, err := gopus.NewDecoder(48000, channels)
	if err != nil {
		t.Fatalf("Error creating decoder: %v", err)
	}

	// Decode first 3 packets
	var allDecoded []int16
	for i := 0; i < 3 && i < len(packets); i++ {
		pcm, err := dec.DecodeInt16Slice(packets[i].Data)
		if err != nil {
			t.Logf("Packet %d decode error: %v", i, err)
			// Fill with zeros
			pcm = make([]int16, 960*channels)
		}
		allDecoded = append(allDecoded, pcm...)
		t.Logf("Packet %d: %d samples decoded", i, len(pcm))
	}

	// Convert mono to stereo for comparison
	if channels == 1 {
		stereoData := make([]int16, len(allDecoded)*2)
		for i, s := range allDecoded {
			stereoData[i*2] = s
			stereoData[i*2+1] = s
		}
		allDecoded = stereoData
	}

	t.Logf("Decoded %d samples (stereo)", len(allDecoded))

	// Find first non-zero samples in reference
	firstNonZeroRef := -1
	for i, s := range reference {
		if s != 0 {
			firstNonZeroRef = i
			break
		}
	}
	t.Logf("First non-zero sample in reference at index: %d", firstNonZeroRef)

	// Compare samples around first non-zero in reference
	startIdx := 0
	if firstNonZeroRef > 20 {
		startIdx = firstNonZeroRef - 20
	}
	t.Logf("Samples around first non-zero (starting at %d):", startIdx)
	minLen := len(allDecoded)
	if len(reference) < minLen {
		minLen = len(reference)
	}
	endIdx := startIdx + 60
	if endIdx > minLen {
		endIdx = minLen
	}

	for i := startIdx; i < endIdx; i++ {
		d := int16(0)
		if i < len(allDecoded) {
			d = allDecoded[i]
		}
		ref := reference[i]
		diff := int(d) - int(ref)
		t.Logf("[%5d] dec=%7d, ref=%7d, diff=%7d", i, d, ref, diff)
	}

	// Also check if decoded ever produces non-zero
	firstNonZeroDec := -1
	for i, s := range allDecoded {
		if s != 0 {
			firstNonZeroDec = i
			break
		}
	}
	t.Logf("First non-zero sample in decoded at index: %d", firstNonZeroDec)

	// Compare more samples for energy calculation
	t.Log("Energy analysis (first 1000 samples):")
	var sumDecSq, sumRefSq, sumDiffSq float64
	n := 1000
	if n > len(allDecoded) {
		n = len(allDecoded)
	}
	if n > len(reference) {
		n = len(reference)
	}

	maxDec := int16(0)
	maxRef := int16(0)
	for i := 0; i < n; i++ {
		d := allDecoded[i]
		ref := reference[i]
		if abs16Debug(d) > abs16Debug(maxDec) {
			maxDec = d
		}
		if abs16Debug(ref) > abs16Debug(maxRef) {
			maxRef = ref
		}
		sumDecSq += float64(d) * float64(d)
		sumRefSq += float64(ref) * float64(ref)
		diff := float64(d) - float64(ref)
		sumDiffSq += diff * diff
	}

	t.Logf("  Decoded energy: %.2e (max sample: %d)", sumDecSq, maxDec)
	t.Logf("  Reference energy: %.2e (max sample: %d)", sumRefSq, maxRef)
	t.Logf("  Noise energy: %.2e", sumDiffSq)

	if sumRefSq > 0 && sumDiffSq > 0 {
		snr := 10 * math.Log10(sumRefSq/sumDiffSq)
		t.Logf("  SNR: %.2f dB", snr)
	}
}

func abs16Debug(x int16) int16 {
	if x < 0 {
		return -x
	}
	return x
}

func readDebugPCMFile(filename string) ([]int16, error) {
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

func TestDebugSinglePacketDecode(t *testing.T) {
	vectorPath := "testdata/opus_testvectors"
	bitFile := filepath.Join(vectorPath, "testvector07.bit")
	decFile := filepath.Join(vectorPath, "testvector07.dec")

	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Fatalf("Error reading bit file: %v", err)
	}
	if len(packets) == 0 {
		t.Fatal("No packets")
	}

	reference, err := readDebugPCMFile(decFile)
	if err != nil {
		t.Fatalf("Error reading dec file: %v", err)
	}

	// Find a packet with significant audio
	// Let's try decoding packet 50-60 where there should be audio
	toc := packets[0].Data[0]
	stereo := (toc & 0x04) != 0
	channels := 1
	if stereo {
		channels = 2
	}

	dec, err := gopus.NewDecoder(48000, channels)
	if err != nil {
		t.Fatalf("Error creating decoder: %v", err)
	}

	// Decode packets 0-60
	var allDecoded []int16
	for i := 0; i < 60 && i < len(packets); i++ {
		pcm, err := dec.DecodeInt16Slice(packets[i].Data)
		if err != nil {
			pcm = make([]int16, 960*channels)
		}
		allDecoded = append(allDecoded, pcm...)
	}

	// Convert mono to stereo for comparison
	if channels == 1 {
		stereoData := make([]int16, len(allDecoded)*2)
		for i, s := range allDecoded {
			stereoData[i*2] = s
			stereoData[i*2+1] = s
		}
		allDecoded = stereoData
	}

	t.Logf("Decoded %d samples (60 packets)", len(allDecoded))

	// Check the ratio between decoded and reference
	var totalDecAbs, totalRefAbs float64
	n := len(allDecoded)
	if len(reference) < n {
		n = len(reference)
	}
	for i := 0; i < n; i++ {
		d := allDecoded[i]
		r := reference[i]
		totalDecAbs += math.Abs(float64(d))
		totalRefAbs += math.Abs(float64(r))
	}
	if totalDecAbs > 0 {
		t.Logf("Scale ratio (ref/dec): %.2f", totalRefAbs/totalDecAbs)
	} else {
		t.Logf("Decoded is all zeros!")
	}

	// Check various positions
	positions := []int{1000, 5000, 10000, 50000, 100000}
	for _, pos := range positions {
		if pos >= len(allDecoded) || pos >= len(reference) {
			continue
		}
		decNonZero := 0
		refNonZero := 0
		for i := pos; i < pos+100 && i < len(allDecoded); i++ {
			if allDecoded[i] != 0 {
				decNonZero++
			}
			if i < len(reference) && reference[i] != 0 {
				refNonZero++
			}
		}
		t.Logf("Position %d: decoded non-zero=%d, ref non-zero=%d", pos, decNonZero, refNonZero)

		// Print a few samples with ratio
		for i := pos; i < pos+5 && i < len(allDecoded); i++ {
			d := allDecoded[i]
			r := int16(0)
			if i < len(reference) {
				r = reference[i]
			}
			ratio := 0.0
			if d != 0 {
				ratio = float64(r) / float64(d)
			}
			t.Logf("  [%d] dec=%d, ref=%d, ratio=%.1f", i, d, r, ratio)
		}
	}
}

func TestDebugFullDecode(t *testing.T) {
	vectorPath := "testdata/opus_testvectors"
	bitFile := filepath.Join(vectorPath, "testvector07.bit")
	decFile := filepath.Join(vectorPath, "testvector07.dec")

	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Fatalf("Error reading bit file: %v", err)
	}
	t.Logf("Read %d packets", len(packets))

	reference, err := readDebugPCMFile(decFile)
	if err != nil {
		t.Fatalf("Error reading dec file: %v", err)
	}
	t.Logf("Reference has %d samples", len(reference))

	// Create both mono and stereo decoders to handle mixed streams
	t.Log("Creating mono and stereo decoders...")
	monoDec, err := gopus.NewDecoder(48000, 1)
	if err != nil {
		t.Fatalf("Error creating mono decoder: %v", err)
	}
	stereoDec, err := gopus.NewDecoder(48000, 2)
	if err != nil {
		t.Fatalf("Error creating stereo decoder: %v", err)
	}

	// Decode ALL packets, tracking sample offset per packet
	var allDecoded []int16
	errCounts := make(map[string]int)
	frameSizeCounts := make(map[int]int)
	monoCount, stereoCount := 0, 0
	packetOffsets := make([]int, len(packets)) // stereo sample offset for each packet
	for i, pkt := range packets {
		packetOffsets[i] = len(allDecoded) // stereo sample offset

		// Check TOC stereo bit
		pktTOC := gopus.ParseTOC(pkt.Data[0])
		var pcm []int16

		if pktTOC.Stereo {
			stereoCount++
			pcm, err = stereoDec.DecodeInt16Slice(pkt.Data)
			if err != nil {
				errKey := err.Error()
				errCounts[errKey]++
				cfg := pkt.Data[0] >> 3
				fs := getFrameSizeFromConfigDebug(cfg)
				pcm = make([]int16, fs*2)
			}
		} else {
			monoCount++
			monoSamples, decErr := monoDec.DecodeInt16Slice(pkt.Data)
			if decErr != nil {
				errKey := decErr.Error()
				errCounts[errKey]++
				cfg := pkt.Data[0] >> 3
				fs := getFrameSizeFromConfigDebug(cfg)
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

		frameSizeCounts[len(pcm)/2]++
		allDecoded = append(allDecoded, pcm...)
		if i < 5 || i%1000 == 0 {
			t.Logf("Packet %d: decoded %d samples, total now %d", i, len(pcm), len(allDecoded))
		}
	}
	t.Logf("Mono packets: %d, Stereo packets: %d", monoCount, stereoCount)

	// Find which packet contains a problematic offset (around 1150000 where we saw huge errors)
	badOffsets := []int{1090000, 1150000, 1180000}
	for _, badOffset := range badOffsets {
		for i := range packets {
			if i < len(packetOffsets)-1 && packetOffsets[i] <= badOffset && packetOffsets[i+1] > badOffset {
				t.Logf("Offset %d is in packet %d (stereo offset %d-%d)",
					badOffset, i, packetOffsets[i], packetOffsets[i+1]-1)
				cfg := packets[i].Data[0] >> 3
				fs := getFrameSizeFromConfigDebug(cfg)
				t.Logf("  Packet %d config=%d, frameSize=%d, size=%d bytes", i, cfg, fs, len(packets[i].Data))
				// Show samples at this offset
				for j := badOffset; j < badOffset+10 && j < len(allDecoded); j++ {
					if j < len(reference) {
						t.Logf("    [%d] dec=%d, ref=%d, diff=%d", j, allDecoded[j], reference[j], int(allDecoded[j])-int(reference[j]))
					}
				}
				break
			}
		}
	}

	// Report errors
	if len(errCounts) > 0 {
		t.Log("Decode errors:")
		for errMsg, cnt := range errCounts {
			t.Logf("  %s: %d", errMsg, cnt)
		}
	}
	t.Log("Frame sizes decoded:")
	for fs, cnt := range frameSizeCounts {
		t.Logf("  %d samples: %d packets", fs, cnt)
	}

	// Output is already stereo (dual decoders produce stereo output)
	t.Logf("Total decoded: %d samples (stereo)", len(allDecoded))

	// Check if reference is truly stereo-duplicate (L=R) or actual stereo (L≠R)
	t.Log("Checking reference stereo format...")
	diffCount := 0
	for i := 0; i < len(reference)-1 && i < 10000; i += 2 {
		if reference[i] != reference[i+1] {
			diffCount++
		}
	}
	t.Logf("  In first 5000 stereo samples, %d have L≠R", diffCount)

	// Also check at the bad offset (1130000)
	t.Log("Reference samples around offset 1130000:")
	for i := 1130000; i < 1130010 && i < len(reference); i += 2 {
		t.Logf("  [%d] L=%d, R=%d, L==R: %v", i, reference[i], reference[i+1], reference[i] == reference[i+1])
	}

	// Find packet around offset 1130000 and check TOC
	for i := range packets {
		if i < len(packetOffsets)-1 && packetOffsets[i] <= 1130000 && packetOffsets[i+1] > 1130000 {
			toc := packets[i].Data[0]
			stereo := (toc & 0x04) != 0
			cfg := toc >> 3
			t.Logf("Packet %d contains offset 1130000: TOC=0x%02X, stereo=%v, config=%d", i, toc, stereo, cfg)
			// Also check nearby packets
			for j := i - 5; j <= i+5 && j < len(packets); j++ {
				if j >= 0 {
					toc2 := packets[j].Data[0]
					stereo2 := (toc2 & 0x04) != 0
					cfg2 := toc2 >> 3
					t.Logf("  Packet %d: TOC=0x%02X, stereo=%v, config=%d, offset=%d", j, toc2, stereo2, cfg2, packetOffsets[j])
				}
			}
			break
		}
	}

	// Find first packet where reference L≠R
	t.Log("Finding first reference offset where L≠R...")
	firstDiff := -1
	for i := 0; i < len(reference)-1; i += 2 {
		if reference[i] != reference[i+1] {
			firstDiff = i
			break
		}
	}
	t.Logf("First L≠R in reference at offset: %d", firstDiff)
	if firstDiff >= 0 {
		// Find which packet
		for i := range packets {
			if i < len(packetOffsets)-1 && packetOffsets[i] <= firstDiff && packetOffsets[i+1] > firstDiff {
				toc := packets[i].Data[0]
				stereo := (toc & 0x04) != 0
				cfg := toc >> 3
				t.Logf("  In packet %d: TOC=0x%02X, stereo=%v, config=%d", i, toc, stereo, cfg)
				break
			}
		}
	}

	// First compute overall quality like the compliance test
	var totalSignalPower, totalNoisePower float64
	n := len(allDecoded)
	if len(reference) < n {
		n = len(reference)
	}
	for i := 0; i < n; i++ {
		ref := float64(reference[i])
		dec := float64(allDecoded[i])
		totalSignalPower += ref * ref
		noise := dec - ref
		totalNoisePower += noise * noise
	}
	if totalSignalPower > 0 && totalNoisePower > 0 {
		snr := 10.0 * math.Log10(totalSignalPower/totalNoisePower)
		q := (snr - 48.0) * 1.0 // Q = (SNR - TargetSNR) * QualityScale
		t.Logf("Overall: SNR=%.2f dB, Q=%.2f", snr, q)
	}

	// Find sections with bad SNR
	t.Log("Scanning for high-error sections...")
	sectionSize := 10000
	var badSections []int
	for offset := 0; offset < n-sectionSize; offset += sectionSize {
		var signalP, noiseP float64
		for i := offset; i < offset+sectionSize; i++ {
			ref := float64(reference[i])
			dec := float64(allDecoded[i])
			signalP += ref * ref
			noise := dec - ref
			noiseP += noise * noise
		}
		if signalP > 0 && noiseP > signalP {
			snr := 10.0 * math.Log10(signalP/noiseP)
			if snr < 0 {
				badSections = append(badSections, offset)
				if len(badSections) <= 10 {
					t.Logf("  Bad section at %d: SNR=%.2f dB", offset, snr)
					// Show first 5 samples
					for i := offset; i < offset+5; i++ {
						t.Logf("    [%d] dec=%d, ref=%d, diff=%d", i, allDecoded[i], reference[i], int(allDecoded[i])-int(reference[i]))
					}
				}
			}
		}
	}
	t.Logf("Found %d bad sections (out of %d)", len(badSections), n/sectionSize)

	// Compute quality at different offsets
	offsets := []int{0, 10000, 50000, 100000, 500000, 1000000, 1093000, 1100000, 1900000, 1950000, 2000000}
	for _, offset := range offsets {
		if offset >= len(allDecoded) || offset >= len(reference) {
			continue
		}
		end := offset + 100000
		if end > len(allDecoded) {
			end = len(allDecoded)
		}
		if end > len(reference) {
			end = len(reference)
		}

		var signalPower, noisePower float64
		for i := offset; i < end; i++ {
			ref := float64(reference[i])
			dec := float64(allDecoded[i])
			signalPower += ref * ref
			noise := dec - ref
			noisePower += noise * noise
		}

		if signalPower > 0 && noisePower > 0 {
			snr := 10.0 * math.Log10(signalPower/noisePower)
			t.Logf("Offset %d: SNR=%.2f dB", offset, snr)
		} else {
			t.Logf("Offset %d: signalPower=%.2e noisePower=%.2e", offset, signalPower, noisePower)
		}

		// Show first 5 samples at this offset
		for i := offset; i < offset+5 && i < len(allDecoded); i++ {
			d := allDecoded[i]
			r := int16(0)
			if i < len(reference) {
				r = reference[i]
			}
			t.Logf("  [%d] dec=%d, ref=%d, diff=%d", i, d, r, int(d)-int(r))
		}
	}
}

func getFrameSizeFromConfigDebug(config byte) int {
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
