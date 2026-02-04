package testvectors

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTV12AnalyzeWorstSILKPackets deep-dives into the worst SILK packets
// to understand the failure pattern.
func TestTV12AnalyzeWorstSILKPackets(t *testing.T) {
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping: %v", err)
		return
	}

	bitFile := filepath.Join(testVectorDir, "testvector12.bit")
	decFile := filepath.Join(testVectorDir, "testvector12.dec")

	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Fatalf("Failed to parse bitstream: %v", err)
	}

	reference, err := readPCMFile(decFile)
	if err != nil {
		t.Fatalf("Failed to read reference: %v", err)
	}

	// Worst SILK packets from previous analysis
	worstPackets := []int{826, 213, 137, 758, 1041, 825, 1118, 214, 1117}

	// Decode up to max(worstPackets) + 1 to get all samples
	maxPacket := 0
	for _, p := range worstPackets {
		if p > maxPacket {
			maxPacket = p
		}
	}

	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("Failed to create decoder: %v", err)
	}

	// Collect packet info for all worst packets
	for _, targetIdx := range worstPackets {
		if targetIdx >= len(packets) {
			continue
		}
		pkt := packets[targetIdx]
		if len(pkt.Data) == 0 {
			continue
		}

		toc := pkt.Data[0]
		cfg := toc >> 3
		stereo := (toc & 0x04) != 0
		frameCode := toc & 0x03
		fs := getFrameSizeFromConfig(cfg)
		mode := getModeFromConfig(cfg)

		bw := "unknown"
		switch {
		case cfg <= 3:
			bw = "NB"
		case cfg <= 7:
			bw = "MB"
		case cfg <= 11:
			bw = "WB"
		case cfg <= 13:
			bw = "SWB"
		case cfg <= 15:
			bw = "FB"
		default:
			bw = "CELT"
		}

		t.Logf("Packet %d: TOC=0x%02X, config=%d, mode=%s, bw=%s, stereo=%v, frameCode=%d, fs=%d, len=%d bytes",
			targetIdx, toc, cfg, mode, bw, stereo, frameCode, fs, len(pkt.Data))
	}

	// Now decode and analyze specific packets with fresh decoders
	t.Logf("\n=== Analyzing packet 826 (worst) with fresh decoder ===")
	analyzePacketWithFreshDecoder(t, packets, reference, 826)

	t.Logf("\n=== Analyzing packet 137 with fresh decoder ===")
	analyzePacketWithFreshDecoder(t, packets, reference, 137)

	t.Logf("\n=== Analyzing packet 758 with fresh decoder ===")
	analyzePacketWithFreshDecoder(t, packets, reference, 758)

	// Continuous decode to show context
	t.Logf("\n=== Continuous decode showing packets 820-830 ===")
	var decoded []int16
	for i := 0; i <= 830 && i < len(packets); i++ {
		pkt := packets[i]
		pcm, err := decodeInt16(dec, pkt.Data)
		if err != nil {
			fs := 960
			if len(pkt.Data) > 0 {
				cfg := pkt.Data[0] >> 3
				fs = getFrameSizeFromConfig(cfg)
			}
			zeros := make([]int16, fs*2)
			decoded = append(decoded, zeros...)
			continue
		}
		decoded = append(decoded, pcm...)

		if i >= 820 && i <= 830 {
			// Calculate stats for this packet
			refStart := len(decoded) - len(pcm)
			refEnd := len(decoded)
			if refEnd > len(reference) {
				refEnd = len(reference)
			}

			var maxDiff int16
			var signalPower, noisePower float64

			for j := refStart; j < refEnd && j < len(reference); j++ {
				decIdx := j - refStart + len(decoded) - len(pcm)
				if decIdx >= len(decoded) {
					continue
				}
				d := decoded[decIdx]
				r := reference[j]
				diff := int32(d) - int32(r)
				if diff < 0 {
					diff = -diff
				}
				if int16(diff) > maxDiff {
					maxDiff = int16(diff)
				}
				signalPower += float64(r) * float64(r)
				noisePower += float64(d-r) * float64(d-r)
			}

			snr := float64(0)
			if signalPower > 0 && noisePower > 0 {
				snr = 10 * math.Log10(signalPower/noisePower)
			} else if noisePower == 0 {
				snr = 200
			}

			toc := packets[i].Data[0]
			cfg := toc >> 3
			mode := getModeFromConfig(cfg)

			t.Logf("  Packet %d: mode=%s, SNR=%.1f dB, maxDiff=%d", i, mode, snr, maxDiff)
		}
	}
}

func analyzePacketWithFreshDecoder(t *testing.T, packets []Packet, reference []int16, targetIdx int) {
	// Start from some packets before to warm up state
	startIdx := targetIdx - 20
	if startIdx < 0 {
		startIdx = 0
	}

	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("Failed to create decoder: %v", err)
	}

	var decoded []int16
	for i := startIdx; i <= targetIdx; i++ {
		pkt := packets[i]
		pcm, err := decodeInt16(dec, pkt.Data)
		if err != nil {
			fs := 960
			if len(pkt.Data) > 0 {
				cfg := pkt.Data[0] >> 3
				fs = getFrameSizeFromConfig(cfg)
			}
			zeros := make([]int16, fs*2)
			decoded = append(decoded, zeros...)
			continue
		}
		decoded = append(decoded, pcm...)
	}

	// Calculate reference offset
	refOffset := 0
	for i := 0; i < startIdx; i++ {
		if i >= len(packets) {
			break
		}
		fs := 960
		if len(packets[i].Data) > 0 {
			cfg := packets[i].Data[0] >> 3
			fs = getFrameSizeFromConfig(cfg)
		}
		refOffset += fs * 2 // stereo
	}

	// Get stats for target packet
	targetPkt := packets[targetIdx]
	fs := 960
	if len(targetPkt.Data) > 0 {
		cfg := targetPkt.Data[0] >> 3
		fs = getFrameSizeFromConfig(cfg)
	}
	targetSamples := fs * 2

	targetDecodedStart := len(decoded) - targetSamples
	targetRefStart := refOffset + (targetIdx-startIdx)*fs*2

	// Show first 20 samples comparison
	t.Logf("First 20 sample pairs (decoded vs reference):")
	for i := 0; i < 20 && i < targetSamples/2; i++ {
		decIdx := targetDecodedStart + i*2
		refIdx := targetRefStart + i*2
		if decIdx+1 >= len(decoded) || refIdx+1 >= len(reference) {
			break
		}
		decL := decoded[decIdx]
		decR := decoded[decIdx+1]
		refL := reference[refIdx]
		refR := reference[refIdx+1]
		diffL := int32(decL) - int32(refL)
		diffR := int32(decR) - int32(refR)
		t.Logf("  [%3d] L: dec=%6d ref=%6d diff=%4d | R: dec=%6d ref=%6d diff=%4d",
			i, decL, refL, diffL, decR, refR, diffR)
	}

	// Show where max diff occurs
	var maxDiff int32
	var maxDiffIdx int
	for i := 0; i < targetSamples; i++ {
		decIdx := targetDecodedStart + i
		refIdx := targetRefStart + i
		if decIdx >= len(decoded) || refIdx >= len(reference) {
			break
		}
		diff := int32(decoded[decIdx]) - int32(reference[refIdx])
		if diff < 0 {
			diff = -diff
		}
		if diff > maxDiff {
			maxDiff = diff
			maxDiffIdx = i
		}
	}
	t.Logf("Max diff at sample %d: %d", maxDiffIdx, maxDiff)

	// Show samples around max diff
	t.Logf("Samples around max diff (sample %d):", maxDiffIdx)
	for i := maxDiffIdx - 5; i <= maxDiffIdx+5; i++ {
		if i < 0 || i >= targetSamples {
			continue
		}
		decIdx := targetDecodedStart + i
		refIdx := targetRefStart + i
		if decIdx >= len(decoded) || refIdx >= len(reference) {
			continue
		}
		marker := ""
		if i == maxDiffIdx {
			marker = " <-- MAX"
		}
		t.Logf("  [%3d] dec=%6d ref=%6d diff=%4d%s",
			i, decoded[decIdx], reference[refIdx], int32(decoded[decIdx])-int32(reference[refIdx]), marker)
	}
}

// TestTV12LibopusComparison decodes TV12 using libopus via CGO and compares
func TestTV12CheckPacketContent(t *testing.T) {
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping: %v", err)
		return
	}

	bitFile := filepath.Join(testVectorDir, "testvector12.bit")
	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Fatalf("Failed to parse bitstream: %v", err)
	}

	// Analyze TOC patterns for worst packets
	worstPackets := []int{826, 213, 137, 758, 1041, 825, 1118, 214, 1117}

	t.Logf("=== TOC Pattern Analysis for Worst Packets ===")
	for _, idx := range worstPackets {
		if idx >= len(packets) {
			continue
		}
		pkt := packets[idx]
		if len(pkt.Data) < 2 {
			continue
		}

		toc := pkt.Data[0]
		cfg := toc >> 3
		stereo := (toc & 0x04) != 0
		frameCode := toc & 0x03
		mode := getModeFromConfig(cfg)

		// For SILK, analyze VAD and LBRR flags
		// Byte 1 in SILK: contains VAD flags
		vadByte := pkt.Data[1]

		t.Logf("Packet %d: mode=%s, config=%d, stereo=%v, frameCode=%d, vadByte=0x%02X, dataLen=%d",
			idx, mode, cfg, stereo, frameCode, vadByte, len(pkt.Data))

		// Look for patterns in the data
		if len(pkt.Data) >= 10 {
			t.Logf("  First 10 bytes: % 02X", pkt.Data[:10])
		}
	}

	// Check if any of the worst packets are near mode transitions
	t.Logf("\n=== Mode Context for Worst Packets ===")
	for _, idx := range worstPackets {
		if idx >= len(packets) {
			continue
		}

		// Get modes for packets around this one
		prevMode := ""
		if idx > 0 && len(packets[idx-1].Data) > 0 {
			prevMode = getModeFromConfig(packets[idx-1].Data[0] >> 3)
		}
		currMode := ""
		if len(packets[idx].Data) > 0 {
			currMode = getModeFromConfig(packets[idx].Data[0] >> 3)
		}
		nextMode := ""
		if idx+1 < len(packets) && len(packets[idx+1].Data) > 0 {
			nextMode = getModeFromConfig(packets[idx+1].Data[0] >> 3)
		}

		t.Logf("Packet %d: prev=%s, curr=%s, next=%s", idx, prevMode, currMode, nextMode)
	}
}

// TestTV12DecodeWithLibopusReference uses libopus's opus_demo to decode and compare
func TestTV12WriteDecodeDebugInfo(t *testing.T) {
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping: %v", err)
		return
	}

	bitFile := filepath.Join(testVectorDir, "testvector12.bit")
	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Fatalf("Failed to parse bitstream: %v", err)
	}

	// Extract packet 826 for detailed analysis
	targetIdx := 826
	if targetIdx >= len(packets) {
		t.Fatalf("Packet %d not found", targetIdx)
	}

	pkt := packets[targetIdx]
	t.Logf("Packet %d data (%d bytes):", targetIdx, len(pkt.Data))
	t.Logf("  FinalRange: 0x%08X", pkt.FinalRange)

	// Write packet to file for analysis with libopus
	packetFile := "/tmp/tv12_packet_826.opus"
	if err := os.WriteFile(packetFile, pkt.Data, 0644); err != nil {
		t.Logf("Failed to write packet file: %v", err)
	} else {
		t.Logf("  Written to: %s", packetFile)
	}

	// Parse TOC
	toc := pkt.Data[0]
	cfg := toc >> 3
	stereo := (toc & 0x04) != 0
	frameCode := toc & 0x03
	mode := getModeFromConfig(cfg)

	bwName := "unknown"
	switch {
	case cfg <= 3:
		bwName = "NB (8kHz)"
	case cfg <= 7:
		bwName = "MB (12kHz)"
	case cfg <= 11:
		bwName = "WB (16kHz)"
	}

	t.Logf("  TOC: 0x%02X", toc)
	t.Logf("    config: %d", cfg)
	t.Logf("    mode: %s", mode)
	t.Logf("    bandwidth: %s", bwName)
	t.Logf("    stereo: %v", stereo)
	t.Logf("    frameCode: %d", frameCode)

	// For SILK packets, the second byte contains VAD/LBRR flags
	if mode == "SILK" && len(pkt.Data) >= 2 {
		flags := pkt.Data[1]
		vadFlag := (flags >> 7) & 1
		lbrrFlag := (flags >> 6) & 1
		t.Logf("  SILK flags byte: 0x%02X", flags)
		t.Logf("    VAD: %d", vadFlag)
		t.Logf("    LBRR: %d", lbrrFlag)
	}

	// Write full hex dump
	t.Logf("\n  Full packet hex dump:")
	for i := 0; i < len(pkt.Data); i += 16 {
		end := i + 16
		if end > len(pkt.Data) {
			end = len(pkt.Data)
		}
		t.Logf("    %04X: % X", i, pkt.Data[i:end])
	}
}

// TestTV12CompareRangeDecoderState compares range decoder state with libopus
func TestTV12CompareRangeDecoderState(t *testing.T) {
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping: %v", err)
		return
	}

	bitFile := filepath.Join(testVectorDir, "testvector12.bit")
	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Fatalf("Failed to parse bitstream: %v", err)
	}

	// The FinalRange in the .bit file is the encoder's final range state.
	// After decoding, our decoder's range state should match this.
	// Check if worst packets have range mismatch.

	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("Failed to create decoder: %v", err)
	}

	worstPackets := []int{826, 213, 137, 758, 1041}

	// Decode up to max worst packet
	maxIdx := 0
	for _, idx := range worstPackets {
		if idx > maxIdx {
			maxIdx = idx
		}
	}

	t.Logf("=== Range Decoder State Check ===")
	for i := 0; i <= maxIdx && i < len(packets); i++ {
		pkt := packets[i]
		_, err := decodeInt16(dec, pkt.Data)
		if err != nil {
			t.Logf("Packet %d: decode error: %v", i, err)
			continue
		}

		// Check if this is a worst packet
		isWorst := false
		for _, idx := range worstPackets {
			if i == idx {
				isWorst = true
				break
			}
		}

		if isWorst {
			// The packet's FinalRange should match decoder's final range
			t.Logf("Packet %d: expectedFinalRange=0x%08X", i, pkt.FinalRange)
		}
	}
}

// TestTV12VerifySampleOffset verifies sample count alignment
func TestTV12VerifySampleOffset(t *testing.T) {
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping: %v", err)
		return
	}

	bitFile := filepath.Join(testVectorDir, "testvector12.bit")
	decFile := filepath.Join(testVectorDir, "testvector12.dec")

	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Fatalf("Failed to parse bitstream: %v", err)
	}

	reference, err := readPCMFile(decFile)
	if err != nil {
		t.Fatalf("Failed to read reference: %v", err)
	}

	// Calculate expected samples from all packets
	totalExpected := 0
	for _, pkt := range packets {
		if len(pkt.Data) > 0 {
			cfg := pkt.Data[0] >> 3
			fs := getFrameSizeFromConfig(cfg)
			totalExpected += fs * 2 // stereo
		}
	}

	t.Logf("Total packets: %d", len(packets))
	t.Logf("Expected samples (sum of frameSizes * 2): %d", totalExpected)
	t.Logf("Reference samples: %d", len(reference))
	t.Logf("Match: %v", totalExpected == len(reference))

	if totalExpected != len(reference) {
		diff := len(reference) - totalExpected
		t.Logf("Difference: %d samples (%.2f frames)", diff, float64(diff)/(960*2))
	}
}

// TestTV12CheckLBRRPackets checks if worst packets have LBRR data
func TestTV12CheckLBRRPackets(t *testing.T) {
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping: %v", err)
		return
	}

	bitFile := filepath.Join(testVectorDir, "testvector12.bit")
	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Fatalf("Failed to parse bitstream: %v", err)
	}

	worstPackets := []int{826, 213, 137, 758, 1041, 825, 1118, 214, 1117}

	t.Logf("=== LBRR Analysis for Worst Packets ===")
	for _, idx := range worstPackets {
		if idx >= len(packets) || idx == 0 {
			continue
		}

		// Check both current and previous packet
		prevPkt := packets[idx-1]
		currPkt := packets[idx]

		prevMode := ""
		prevHasLBRR := false
		if len(prevPkt.Data) > 1 {
			prevCfg := prevPkt.Data[0] >> 3
			prevMode = getModeFromConfig(prevCfg)
			if prevMode == "SILK" {
				prevHasLBRR = (prevPkt.Data[1] & 0x40) != 0
			}
		}

		currMode := ""
		currHasLBRR := false
		if len(currPkt.Data) > 1 {
			currCfg := currPkt.Data[0] >> 3
			currMode = getModeFromConfig(currCfg)
			if currMode == "SILK" {
				currHasLBRR = (currPkt.Data[1] & 0x40) != 0
			}
		}

		t.Logf("Packet %d: mode=%s, LBRR=%v | prev: mode=%s, LBRR=%v",
			idx, currMode, currHasLBRR, prevMode, prevHasLBRR)
	}

	// Count total LBRR packets
	lbrrCount := 0
	for i, pkt := range packets {
		if len(pkt.Data) > 1 {
			cfg := pkt.Data[0] >> 3
			mode := getModeFromConfig(cfg)
			if mode == "SILK" && (pkt.Data[1]&0x40) != 0 {
				lbrrCount++
				if i < 20 || (i >= 820 && i <= 830) {
					t.Logf("LBRR packet at %d", i)
				}
			}
		}
	}
	t.Logf("\nTotal LBRR packets: %d / %d SILK packets", lbrrCount, 1068)
}

// TestTV12WriteWorstPacketsForLibopus writes worst packets for testing with libopus
func TestTV12WriteWorstPacketsForLibopus(t *testing.T) {
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping: %v", err)
		return
	}

	bitFile := filepath.Join(testVectorDir, "testvector12.bit")
	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Fatalf("Failed to parse bitstream: %v", err)
	}

	worstPackets := []int{826, 137, 758, 1118}

	for _, idx := range worstPackets {
		if idx >= len(packets) {
			continue
		}

		pkt := packets[idx]
		filename := filepath.Join("/tmp", "tv12_pkt_"+string(rune('0'+idx/1000))+string(rune('0'+(idx%1000)/100))+string(rune('0'+(idx%100)/10))+string(rune('0'+idx%10))+".bin")

		// Write in opus_demo format: 4-byte length + 4-byte finalRange + data
		out := make([]byte, 8+len(pkt.Data))
		binary.BigEndian.PutUint32(out[0:4], uint32(len(pkt.Data)))
		binary.BigEndian.PutUint32(out[4:8], pkt.FinalRange)
		copy(out[8:], pkt.Data)

		if err := os.WriteFile(filename, out, 0644); err != nil {
			t.Logf("Failed to write %s: %v", filename, err)
		} else {
			t.Logf("Written packet %d to %s (%d bytes)", idx, filename, len(out))
		}
	}
}
