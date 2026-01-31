package testvectors

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestCELTFirstPacketAnalysis decodes ONLY the first frame of testvector01 (CELT stereo)
// with full tracing to capture ALL intermediate values for comparison with libopus.
//
// This test outputs structured comparison data for:
// - Header flags (silence, transient, intra, LM)
// - Range decoder state after key decode operations
// - Coarse energy for each band (21 bands x 2 channels)
// - Fine energy adjustments
// - Bit allocation per band
// - PVQ decode: band, n, k, index, pulses
// - Denormalized coefficients (first 8 per band)
// - Synthesis output (first 32 samples)
func TestCELTFirstPacketAnalysis(t *testing.T) {
	// Ensure test vectors are available
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping: %v", err)
		return
	}

	bitFile := filepath.Join(testVectorDir, "testvector01.bit")

	// 1. Parse testvector01.bit, extract first packet
	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Fatalf("Failed to parse %s: %v", bitFile, err)
	}

	if len(packets) == 0 {
		t.Fatal("No packets in testvector01.bit")
	}

	firstPacket := packets[0]
	t.Logf("=== First Packet Analysis ===")
	t.Logf("Packet size: %d bytes", len(firstPacket.Data))

	if len(firstPacket.Data) == 0 {
		t.Fatal("First packet is empty")
	}

	// Parse TOC byte
	toc := firstPacket.Data[0]
	config := toc >> 3
	stereo := (toc & 0x04) != 0
	frameCode := toc & 0x03
	mode := getModeFromConfig(config)
	frameSize := getFrameSizeFromConfig(config)

	t.Logf("TOC: 0x%02X", toc)
	t.Logf("  Config: %d (%s)", config, mode)
	t.Logf("  Stereo: %v", stereo)
	t.Logf("  Frame code: %d", frameCode)
	t.Logf("  Frame size: %d samples (%.1fms at 48kHz)", frameSize, float64(frameSize)/48.0)

	// Verify this is a CELT packet
	if mode != "CELT" {
		t.Skipf("First packet is %s mode, not CELT. Skipping CELT analysis.", mode)
		return
	}

	// 2. Set up comprehensive tracer to capture ALL intermediate values
	var traceBuf bytes.Buffer
	tracer := &celt.LogTracer{W: &traceBuf}
	celt.SetTracer(tracer)
	defer celt.SetTracer(&celt.NoopTracer{}) // Reset after test

	// Create CELT decoder
	channels := 1
	if stereo {
		channels = 2
	}
	decoder := celt.NewDecoder(channels)
	setDecoderBandwidth(decoder, toc)

	// Extract frame data (skip TOC byte for frame code 0)
	// For code 0: single frame, data starts at byte 1
	var frameData []byte
	switch frameCode {
	case 0:
		// Code 0: single frame
		frameData = firstPacket.Data[1:]
	case 1, 2, 3:
		// Multi-frame packet - extract first frame only
		frameData = extractFirstFrame(firstPacket.Data)
	}

	t.Logf("Frame data size: %d bytes", len(frameData))

	// 3. Decode with comprehensive tracing
	t.Logf("")
	t.Logf("=== Range Decoder State ===")

	// Initialize range decoder for manual inspection
	rd := &rangecoding.Decoder{}
	rd.Init(frameData)

	t.Logf("Initial: tell=%d, val=0x%08X, rng=0x%08X", rd.Tell(), rd.Val(), rd.Range())

	// Decode through celt decoder which will trigger tracer calls
	samples, err := decoder.DecodeFrame(frameData, frameSize)
	if err != nil {
		t.Logf("Decode error (continuing for analysis): %v", err)
	}

	// 4. Output structured comparison data
	t.Logf("")
	t.Logf("=== Trace Output ===")
	t.Log(traceBuf.String())

	// Output synthesis results
	t.Logf("")
	t.Logf("=== Synthesis Output ===")
	if len(samples) > 0 {
		t.Logf("Total samples: %d (expected: %d)", len(samples), frameSize*channels)

		// First 32 samples
		nSamples := 32
		if len(samples) < nSamples {
			nSamples = len(samples)
		}

		t.Logf("First %d samples:", nSamples)
		for i := 0; i < nSamples; i++ {
			if channels == 2 {
				ch := "L"
				if i%2 == 1 {
					ch = "R"
				}
				t.Logf("  [%d] %s: %.6f", i/2, ch, samples[i])
			} else {
				t.Logf("  [%d]: %.6f", i, samples[i])
			}
		}

		// Compute basic statistics
		var sum, sumSq, maxAbs float64
		for _, s := range samples {
			sum += s
			sumSq += s * s
			if math.Abs(s) > maxAbs {
				maxAbs = math.Abs(s)
			}
		}
		mean := sum / float64(len(samples))
		rms := math.Sqrt(sumSq / float64(len(samples)))

		t.Logf("")
		t.Logf("Sample statistics:")
		t.Logf("  Mean: %.6f", mean)
		t.Logf("  RMS: %.6f", rms)
		t.Logf("  Max abs: %.6f", maxAbs)

		// Energy in dB (relative to full scale assuming 16-bit output)
		// Full scale would be 32767^2 per sample
		energyDB := 10 * math.Log10(sumSq/float64(len(samples))+1e-20)
		t.Logf("  Energy: %.2f dB (relative)", energyDB)
	} else {
		t.Logf("No samples output")
	}

	t.Logf("")
	t.Logf("=== Analysis Complete ===")
}

// extractFirstFrame extracts the first frame from a multi-frame Opus packet.
// For frame code 1: 2 equal-size frames, each (len-1)/2 bytes
// For frame code 2: 2 frames with explicit first-frame size
// For frame code 3: M frames (VBR or CBR)
func extractFirstFrame(data []byte) []byte {
	if len(data) < 1 {
		return data
	}

	info, err := gopus.ParsePacket(data)
	if err != nil {
		return nil
	}

	frames := extractFrames(data, info)
	if len(frames) == 0 {
		return nil
	}
	return frames[0]
}

func extractFrames(data []byte, info gopus.PacketInfo) [][]byte {
	frames := make([][]byte, info.FrameCount)
	totalFrameBytes := 0
	for _, size := range info.FrameSizes {
		totalFrameBytes += size
	}
	frameDataStart := len(data) - info.Padding - totalFrameBytes
	if frameDataStart < 1 {
		frameDataStart = 1
	}
	dataEnd := len(data) - info.Padding
	if dataEnd < frameDataStart {
		dataEnd = frameDataStart
	}
	offset := frameDataStart
	for i := 0; i < info.FrameCount; i++ {
		frameLen := info.FrameSizes[i]
		endOffset := offset + frameLen
		if endOffset > dataEnd {
			endOffset = dataEnd
		}
		if offset >= dataEnd {
			frames[i] = nil
		} else {
			frames[i] = data[offset:endOffset]
		}
		offset = endOffset
	}
	return frames
}

func setDecoderBandwidth(dec *celt.Decoder, toc byte) {
	tocInfo := gopus.ParseTOC(toc)
	dec.SetBandwidth(celt.BandwidthFromOpusConfig(int(tocInfo.Bandwidth)))
}

// TestCELTFirstPacketComparison compares gopus output with reference for first packet.
// This test identifies the exact divergence point between gopus and libopus output.
func TestCELTFirstPacketComparison(t *testing.T) {
	// Ensure test vectors are available
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping: %v", err)
		return
	}

	bitFile := filepath.Join(testVectorDir, "testvector01.bit")
	decFile := filepath.Join(testVectorDir, "testvector01.dec")

	// 1. Parse .bit file and get first packet
	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Fatalf("Failed to parse %s: %v", bitFile, err)
	}

	if len(packets) == 0 {
		t.Fatal("No packets in testvector01.bit")
	}

	firstPacket := packets[0]

	// Parse TOC
	toc := firstPacket.Data[0]
	config := toc >> 3
	stereo := (toc & 0x04) != 0
	mode := getModeFromConfig(config)
	frameSize := getFrameSizeFromConfig(config)

	t.Logf("=== First Packet Comparison ===")
	t.Logf("Packet: %d bytes, TOC: 0x%02X (config=%d, %s, stereo=%v)",
		len(firstPacket.Data), toc, config, mode, stereo)

	if mode != "CELT" {
		t.Skipf("First packet is %s mode, not CELT", mode)
		return
	}

	channels := 1
	if stereo {
		channels = 2
	}

	// Decode first packet using CELT decoder
	decoder := celt.NewDecoder(channels)
	setDecoderBandwidth(decoder, toc)
	frameData := extractFirstFrame(firstPacket.Data)

	samples, err := decoder.DecodeFrame(frameData, frameSize)
	if err != nil {
		t.Logf("Decode error: %v", err)
	}

	// Convert to int16 (match gopus DecodeInt16 scaling)
	decoded := make([]int16, len(samples))
	for i, s := range samples {
		scaled := s * 32768.0
		if scaled > 32767.0 {
			decoded[i] = 32767
		} else if scaled < -32768.0 {
			decoded[i] = -32768
		} else {
			decoded[i] = int16(math.RoundToEven(scaled))
		}
	}

	t.Logf("Decoded: %d samples", len(decoded))

	// 2. Read reference samples (first frameSize*channels samples)
	refData, err := os.ReadFile(decFile)
	if err != nil {
		t.Fatalf("Failed to read reference: %v", err)
	}

	expectedSamples := frameSize * channels
	if len(refData) < expectedSamples*2 {
		t.Fatalf("Reference file too short: %d bytes, need %d", len(refData), expectedSamples*2)
	}

	reference := make([]int16, expectedSamples)
	for i := 0; i < expectedSamples; i++ {
		reference[i] = int16(binary.LittleEndian.Uint16(refData[i*2:]))
	}

	t.Logf("Reference: %d samples", len(reference))

	// 3. Compare sample-by-sample
	t.Logf("")
	t.Logf("=== Sample-by-Sample Comparison ===")

	n := len(decoded)
	if len(reference) < n {
		n = len(reference)
	}

	var maxDiff int
	var maxDiffIdx int
	var firstDivergenceIdx int = -1
	var sumDiff, sumAbsDiff float64
	const divergenceThreshold = 1000

	// Output first 10 samples
	t.Logf("First 10 samples:")
	for i := 0; i < 10 && i < n; i++ {
		diff := int(decoded[i]) - int(reference[i])
		marker := ""
		if abs(diff) > divergenceThreshold && firstDivergenceIdx < 0 {
			firstDivergenceIdx = i
			marker = " <-- FIRST DIVERGENCE"
		}
		if channels == 2 {
			ch := "L"
			if i%2 == 1 {
				ch = "R"
			}
			t.Logf("  [%d] %s: decoded=%6d, ref=%6d, diff=%6d%s",
				i/2, ch, decoded[i], reference[i], diff, marker)
		} else {
			t.Logf("  [%d]: decoded=%6d, ref=%6d, diff=%6d%s",
				i, decoded[i], reference[i], diff, marker)
		}
	}

	// Full comparison
	for i := 0; i < n; i++ {
		diff := int(decoded[i]) - int(reference[i])
		absDiff := abs(diff)

		sumDiff += float64(diff)
		sumAbsDiff += float64(absDiff)

		if absDiff > maxDiff {
			maxDiff = absDiff
			maxDiffIdx = i
		}

		if absDiff > divergenceThreshold && firstDivergenceIdx < 0 {
			firstDivergenceIdx = i
		}
	}

	meanDiff := sumDiff / float64(n)
	meanAbsDiff := sumAbsDiff / float64(n)

	t.Logf("")
	t.Logf("=== Error Metrics ===")
	t.Logf("Max diff: %d (at sample %d)", maxDiff, maxDiffIdx)
	t.Logf("Mean diff: %.2f", meanDiff)
	t.Logf("Mean abs diff: %.2f", meanAbsDiff)

	if firstDivergenceIdx >= 0 {
		timeMs := float64(firstDivergenceIdx/channels) / 48.0
		t.Logf("First divergence (|diff| > %d): sample %d (%.2fms into frame)",
			divergenceThreshold, firstDivergenceIdx, timeMs)
	} else {
		t.Logf("No divergence above threshold %d found", divergenceThreshold)
	}

	// 4. Pattern analysis
	t.Logf("")
	t.Logf("=== Pattern Analysis ===")

	// Compute energy of decoded and reference
	var decodedEnergy, refEnergy float64
	for i := 0; i < n; i++ {
		decodedEnergy += float64(decoded[i]) * float64(decoded[i])
		refEnergy += float64(reference[i]) * float64(reference[i])
	}

	t.Logf("Decoded energy: %.2e", decodedEnergy)
	t.Logf("Reference energy: %.2e", refEnergy)

	if refEnergy > 0 {
		energyRatio := decodedEnergy / refEnergy
		t.Logf("Energy ratio (decoded/ref): %.6f (%.2f%%)", energyRatio, energyRatio*100)

		// Diagnose based on ratio
		if energyRatio < 0.01 {
			t.Logf("DIAGNOSIS: Output energy is <1%% of reference - likely energy/gain calculation bug")
		} else if energyRatio < 0.1 {
			t.Logf("DIAGNOSIS: Output energy is <10%% of reference - possible denormalization issue")
		} else if energyRatio > 10 {
			t.Logf("DIAGNOSIS: Output energy is >10x reference - possible overflow or incorrect scaling")
		} else if meanAbsDiff > 10000 {
			t.Logf("DIAGNOSIS: Large absolute error but similar energy - possible sign or phase issue")
		}
	}

	// Check for correlation (sign of values matching)
	var signMatches int
	for i := 0; i < n; i++ {
		if (decoded[i] >= 0) == (reference[i] >= 0) {
			signMatches++
		}
	}
	signMatchRatio := float64(signMatches) / float64(n)
	t.Logf("Sign match ratio: %.2f%% (50%% = random, 100%% = perfect)", signMatchRatio*100)

	if signMatchRatio < 0.55 {
		t.Logf("DIAGNOSIS: Sign match near 50%% suggests bitstream desync or random output")
	}
}

// abs returns absolute value of an int
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// TestCELTNonSilentFrameComparison compares a frame with actual audio content.
// The first frames may be silence or low-energy; this test finds frames with
// substantial energy for meaningful comparison.
func TestCELTNonSilentFrameComparison(t *testing.T) {
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping: %v", err)
		return
	}

	bitFile := filepath.Join(testVectorDir, "testvector01.bit")
	decFile := filepath.Join(testVectorDir, "testvector01.dec")

	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Fatalf("Failed to parse %s: %v", bitFile, err)
	}

	// Read entire reference file
	refData, err := os.ReadFile(decFile)
	if err != nil {
		t.Fatalf("Failed to read reference: %v", err)
	}

	t.Logf("Total packets: %d", len(packets))
	t.Logf("Reference file: %d bytes (%d samples)", len(refData), len(refData)/2)

	// Find first packet with significant reference energy
	// We need to compute cumulative sample offset as we go
	sampleOffset := 0

	var targetPacketIdx int = -1
	var targetEnergy float64

	for pktIdx, pkt := range packets {
		if len(pkt.Data) == 0 {
			continue
		}

		toc := pkt.Data[0]
		config := toc >> 3
		mode := getModeFromConfig(config)
		frameSize := getFrameSizeFromConfig(config)

		// Only check CELT packets
		if mode != "CELT" {
			// Account for frames even for non-CELT packets
			stereo := (toc & 0x04) != 0
			channels := 1
			if stereo {
				channels = 2
			}
			frameCode := toc & 0x03
			frameCount := 1
			if frameCode == 1 || frameCode == 2 {
				frameCount = 2
			} else if frameCode == 3 && len(pkt.Data) > 1 {
				frameCount = int(pkt.Data[1] & 0x3F)
			}
			sampleOffset += frameSize * channels * frameCount
			continue
		}

		stereo := (toc & 0x04) != 0
		channels := 1
		if stereo {
			channels = 2
		}
		frameCode := toc & 0x03
		frameCount := 1
		if frameCode == 1 || frameCode == 2 {
			frameCount = 2
		} else if frameCode == 3 && len(pkt.Data) > 1 {
			frameCount = int(pkt.Data[1] & 0x3F)
		}

		samplesPerPacket := frameSize * channels * frameCount

		// Check reference energy for this packet's samples
		refStart := sampleOffset * 2
		refEnd := (sampleOffset + samplesPerPacket) * 2
		if refEnd > len(refData) {
			break
		}

		var energy float64
		for i := refStart; i < refEnd; i += 2 {
			sample := int16(binary.LittleEndian.Uint16(refData[i:]))
			energy += float64(sample) * float64(sample)
		}
		energyPerSample := energy / float64(samplesPerPacket)

		// Look for packet with significant energy (not silence)
		if energyPerSample > 1e6 { // ~1000 RMS
			targetPacketIdx = pktIdx
			targetEnergy = energyPerSample
			t.Logf("Found non-silent frame: packet %d, ref energy/sample: %.2e", pktIdx, energyPerSample)
			break
		}

		sampleOffset += samplesPerPacket
	}

	if targetPacketIdx < 0 {
		t.Log("No high-energy CELT frames found, using first substantial frame")
		// Fall back to first CELT frame
		for i, pkt := range packets {
			if len(pkt.Data) > 0 {
				toc := pkt.Data[0]
				config := toc >> 3
				if getModeFromConfig(config) == "CELT" {
					targetPacketIdx = i
					break
				}
			}
		}
	}

	if targetPacketIdx < 0 {
		t.Skip("No CELT packets found")
		return
	}

	// Recalculate sample offset up to target packet
	sampleOffset = 0
	for i := 0; i < targetPacketIdx; i++ {
		pkt := packets[i]
		if len(pkt.Data) == 0 {
			continue
		}
		toc := pkt.Data[0]
		config := toc >> 3
		frameSize := getFrameSizeFromConfig(config)
		stereo := (toc & 0x04) != 0
		channels := 1
		if stereo {
			channels = 2
		}
		frameCode := toc & 0x03
		frameCount := 1
		if frameCode == 1 || frameCode == 2 {
			frameCount = 2
		} else if frameCode == 3 && len(pkt.Data) > 1 {
			frameCount = int(pkt.Data[1] & 0x3F)
		}
		sampleOffset += frameSize * channels * frameCount
	}

	targetPacket := packets[targetPacketIdx]
	toc := targetPacket.Data[0]
	config := toc >> 3
	stereo := (toc & 0x04) != 0
	frameSize := getFrameSizeFromConfig(config)
	channels := 1
	if stereo {
		channels = 2
	}

	t.Logf("")
	t.Logf("=== Non-Silent Frame Comparison (Packet %d) ===", targetPacketIdx)
	t.Logf("Sample offset: %d, frame size: %d, channels: %d", sampleOffset, frameSize, channels)
	t.Logf("Reference energy/sample: %.2e", targetEnergy)

	// Decode first frame of this packet
	decoder := celt.NewDecoder(channels)

	// We need to warm up the decoder by decoding previous frames to get correct state
	t.Logf("Warming up decoder with %d previous packets...", targetPacketIdx)
	for i := 0; i < targetPacketIdx; i++ {
		pkt := packets[i]
		if len(pkt.Data) == 0 {
			continue
		}
		info, err := gopus.ParsePacket(pkt.Data)
		if err != nil {
			continue
		}
		if info.TOC.Mode != gopus.ModeCELT {
			continue
		}
		pframeSize := info.TOC.FrameSize
		frames := extractFrames(pkt.Data, info)
		for _, frame := range frames {
			if len(frame) == 0 {
				continue
			}
			setDecoderBandwidth(decoder, pkt.Data[0])
			_, _ = decoder.DecodeFrame(frame, pframeSize)
		}
	}

	// Now decode target frame with tracing
	var traceBuf bytes.Buffer
	tracer := &celt.LogTracer{W: &traceBuf}
	celt.SetTracer(tracer)
	defer celt.SetTracer(&celt.NoopTracer{})

	frameData := extractFirstFrame(targetPacket.Data)
	setDecoderBandwidth(decoder, toc)

	// Debug: check frame data
	t.Logf("")
	t.Logf("=== Frame Data Debug ===")
	t.Logf("Frame data length: %d bytes", len(frameData))
	if len(frameData) >= 4 {
		t.Logf("First 4 bytes: 0x%02X 0x%02X 0x%02X 0x%02X", frameData[0], frameData[1], frameData[2], frameData[3])
	}

	// Initialize a range decoder to check silence flag
	testRD := &rangecoding.Decoder{}
	testRD.Init(frameData)
	t.Logf("Range decoder after init: tell=%d, val=0x%08X, rng=0x%08X", testRD.Tell(), testRD.Val(), testRD.Range())

	// Check silence bit - silence is indicated by top bit being 1 with high probability (logp=15)
	// DecodeBit(15) means P(1) = 1/32768
	silenceBit := testRD.DecodeBit(15)
	t.Logf("Silence bit (logp=15): %d", silenceBit)

	samples, err := decoder.DecodeFrame(frameData, frameSize)
	if err != nil {
		t.Logf("Decode error: %v", err)
	}

	// Output trace
	t.Logf("")
	t.Logf("=== Trace Output ===")
	traceStr := traceBuf.String()
	if len(traceStr) > 2000 {
		t.Log(traceStr[:2000] + "...")
	} else if len(traceStr) > 0 {
		t.Log(traceStr)
	} else {
		t.Log("(no trace output)")
	}

	// Convert to int16 (match gopus DecodeInt16 scaling)
	decoded := make([]int16, len(samples))
	for i, s := range samples {
		scaled := s * 32768.0
		if scaled > 32767.0 {
			decoded[i] = 32767
		} else if scaled < -32768.0 {
			decoded[i] = -32768
		} else {
			decoded[i] = int16(math.RoundToEven(scaled))
		}
	}

	// Get reference samples
	refStart := sampleOffset
	refEnd := sampleOffset + frameSize*channels
	if refEnd*2 > len(refData) {
		t.Fatalf("Reference file too short for packet %d", targetPacketIdx)
	}

	reference := make([]int16, frameSize*channels)
	for i := 0; i < len(reference); i++ {
		reference[i] = int16(binary.LittleEndian.Uint16(refData[(refStart+i)*2:]))
	}

	// Compare
	n := len(decoded)
	if len(reference) < n {
		n = len(reference)
	}

	var maxDiff int
	var sumDiff, sumAbsDiff float64
	var decodedEnergy, refEnergy float64

	for i := 0; i < n; i++ {
		diff := int(decoded[i]) - int(reference[i])
		absDiff := abs(diff)
		sumDiff += float64(diff)
		sumAbsDiff += float64(absDiff)
		if absDiff > maxDiff {
			maxDiff = absDiff
		}
		decodedEnergy += float64(decoded[i]) * float64(decoded[i])
		refEnergy += float64(reference[i]) * float64(reference[i])
	}

	t.Logf("")
	t.Logf("=== Comparison Results ===")
	t.Logf("Decoded samples: %d, Reference samples: %d", len(decoded), len(reference))
	t.Logf("Max diff: %d", maxDiff)
	t.Logf("Mean abs diff: %.2f", sumAbsDiff/float64(n))
	t.Logf("Decoded energy: %.2e", decodedEnergy)
	t.Logf("Reference energy: %.2e", refEnergy)

	if refEnergy > 0 {
		ratio := decodedEnergy / refEnergy
		t.Logf("Energy ratio: %.6f (%.2f%%)", ratio, ratio*100)
	}

	// Show first 10 samples
	t.Logf("")
	t.Logf("First 10 samples:")
	for i := 0; i < 10 && i < n; i++ {
		diff := int(decoded[i]) - int(reference[i])
		t.Logf("  [%d]: decoded=%6d, ref=%6d, diff=%6d", i, decoded[i], reference[i], diff)
	}
}
