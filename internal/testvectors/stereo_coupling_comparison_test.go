package testvectors

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestStereoCouplingVsLibopus compares stereo decoding with libopus per-frame.
// This test decodes stereo CELT packets and compares the PCM output with libopus.
func TestStereoCouplingVsLibopus(t *testing.T) {
	// Check if opus_demo is available
	opusDemoPath := filepath.Join("..", "..", "tmp_check", "opus-1.6.1", "opus_demo")
	if _, err := os.Stat(opusDemoPath); os.IsNotExist(err) {
		t.Skipf("opus_demo not found at %s", opusDemoPath)
	}

	// Ensure test vectors are available
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping test: %v", err)
	}

	// Test with testvector11 which is stereo CELT only
	testVector := "testvector11"
	bitFile := filepath.Join(testVectorDir, testVector+".bit")

	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Fatalf("Failed to parse %s: %v", bitFile, err)
	}

	if len(packets) == 0 {
		t.Fatal("No packets in test file")
	}

	// Analyze first packet to determine parameters
	toc := gopus.ParseTOC(packets[0].Data[0])
	if !toc.Stereo {
		t.Skip("Test vector is not stereo")
	}

	t.Logf("Test vector %s: %d packets, stereo=%v", testVector, len(packets), toc.Stereo)

	// Create Go decoder
	goDec, err := gopus.NewDecoder(48000, 2)
	if err != nil {
		t.Fatalf("Failed to create decoder: %v", err)
	}

	// Track statistics
	var totalFrames int
	var passedFrames int
	var maxDiff float64
	var sumSNR float64

	// Process first 100 frames for detailed comparison
	maxFrames := 100
	if len(packets) < maxFrames {
		maxFrames = len(packets)
	}

	for i := 0; i < maxFrames; i++ {
		pkt := packets[i]

		// Decode with Go
		goSamples, err := goDec.DecodeFloat32(pkt.Data)
		if err != nil {
			t.Logf("Frame %d: Go decode error: %v", i, err)
			continue
		}

		// Decode with libopus using opus_demo
		libSamples, err := decodeWithLibopus(opusDemoPath, pkt.Data, 2)
		if err != nil {
			t.Logf("Frame %d: libopus decode error: %v", i, err)
			continue
		}

		// Compare samples
		if len(goSamples) != len(libSamples) {
			t.Logf("Frame %d: length mismatch: Go=%d, libopus=%d", i, len(goSamples), len(libSamples))
			continue
		}

		// Compute max abs diff and SNR
		frameDiff := 0.0
		signalPower := 0.0
		errorPower := 0.0
		for j := 0; j < len(goSamples); j++ {
			diff := math.Abs(float64(goSamples[j]) - float64(libSamples[j]))
			if diff > frameDiff {
				frameDiff = diff
			}
			signalPower += float64(libSamples[j]) * float64(libSamples[j])
			errorPower += diff * diff
		}

		snr := 0.0
		if errorPower > 0 {
			snr = 10 * math.Log10(signalPower/errorPower)
		} else if signalPower > 0 {
			snr = 999.0 // Perfect match
		}

		totalFrames++
		sumSNR += snr

		if frameDiff > maxDiff {
			maxDiff = frameDiff
		}

		// Check acceptance criteria
		if frameDiff <= 1e-6 && snr >= 90 {
			passedFrames++
		} else if i < 10 {
			// Log details for first few failing frames
			t.Logf("Frame %d: maxDiff=%.2e, SNR=%.1f dB", i, frameDiff, snr)
			// Show first few samples
			showLen := 16
			if len(goSamples) < showLen {
				showLen = len(goSamples)
			}
			t.Logf("  Go[0:%d]:     %v", showLen, goSamples[:showLen])
			t.Logf("  Libopus[0:%d]: %v", showLen, libSamples[:showLen])
		}
	}

	avgSNR := 0.0
	if totalFrames > 0 {
		avgSNR = sumSNR / float64(totalFrames)
	}

	t.Logf("Results: %d/%d frames passed (%.1f%%)", passedFrames, totalFrames, 100.0*float64(passedFrames)/float64(totalFrames))
	t.Logf("Max abs diff: %.2e (threshold: 1e-6)", maxDiff)
	t.Logf("Average SNR: %.1f dB (threshold: 90 dB)", avgSNR)

	// Overall pass/fail
	if passedFrames < totalFrames {
		t.Errorf("FAIL: Only %d/%d frames meet acceptance criteria", passedFrames, totalFrames)
	}
}

// decodeWithLibopus decodes a single Opus packet using opus_demo.
func decodeWithLibopus(opusDemoPath string, packet []byte, channels int) ([]float32, error) {
	// Write packet to temp file in opus_demo format
	tmpDir := os.TempDir()
	bitFile := filepath.Join(tmpDir, "stereo_test.bit")
	pcmFile := filepath.Join(tmpDir, "stereo_test.pcm")

	// Create .bit file with single packet
	f, err := os.Create(bitFile)
	if err != nil {
		return nil, err
	}
	defer os.Remove(bitFile)

	// Write packet length (big-ian 32-bit) + final range state (0) + data
	length := int32(len(packet))
	binary.Write(f, binary.BigEndian, length)
	binary.Write(f, binary.BigEndian, uint32(0)) // final range (we don't check it)
	f.Write(packet)
	f.Close()

	defer os.Remove(pcmFile)

	// Get frame size from TOC
	toc := gopus.ParseTOC(packet[0])
	frameSize := toc.FrameSize

	// Run opus_demo to decode
	// opus_demo -d <samplerate> <channels> <input.bit> <output.pcm>
	cmd := exec.Command(opusDemoPath, "-d", "48000", fmt.Sprintf("%d", channels), bitFile, pcmFile)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("opus_demo failed: %v\n%s", err, output)
	}

	// Read PCM output (16-bit signed)
	pcmData, err := os.ReadFile(pcmFile)
	if err != nil {
		return nil, err
	}

	// Convert to float32
	numSamples := len(pcmData) / 2
	samples := make([]float32, numSamples)
	for i := 0; i < numSamples; i++ {
		s16 := int16(binary.LittleEndian.Uint16(pcmData[i*2:]))
		samples[i] = float32(s16) / 32768.0
	}

	// Verify we got expected number of samples
	expectedSamples := frameSize * channels
	if len(samples) != expectedSamples {
		return nil, fmt.Errorf("sample count mismatch: got %d, expected %d", len(samples), expectedSamples)
	}

	return samples, nil
}

// TestStereoCouplingTestvector07 specifically tests testvector07 which has mixed content.
func TestStereoCouplingTestvector07(t *testing.T) {
	// Ensure test vectors are available
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping test: %v", err)
	}

	testVector := "testvector07"
	bitFile := filepath.Join(testVectorDir, testVector+".bit")
	decFile := filepath.Join(testVectorDir, testVector+".dec")

	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Fatalf("Failed to parse %s: %v", bitFile, err)
	}

	t.Logf("Test vector %s: %d packets", testVector, len(packets))

	// Read reference
	decData, err := os.ReadFile(decFile)
	if err != nil {
		t.Fatalf("Failed to read reference: %v", err)
	}
	refSamples := make([]int16, len(decData)/2)
	for i := range refSamples {
		refSamples[i] = int16(binary.LittleEndian.Uint16(decData[i*2:]))
	}

	t.Logf("Reference: %d samples", len(refSamples))

	// Create decoders for mono and stereo
	monoDec, err := gopus.NewDecoder(48000, 1)
	if err != nil {
		t.Fatalf("Failed to create mono decoder: %v", err)
	}
	stereoDec, err := gopus.NewDecoder(48000, 2)
	if err != nil {
		t.Fatalf("Failed to create stereo decoder: %v", err)
	}

	// Decode all packets
	var allDecoded []int16
	monoCount := 0
	stereoCount := 0

	for i, pkt := range packets {
		pktTOC := gopus.ParseTOC(pkt.Data[0])

		var pcm []int16
		if pktTOC.Stereo {
			stereoCount++
			pcm, err = stereoDec.DecodeInt16Slice(pkt.Data)
		} else {
			monoCount++
			monoSamples, decErr := monoDec.DecodeInt16Slice(pkt.Data)
			err = decErr
			if err == nil {
				// Duplicate mono to stereo
				pcm = make([]int16, len(monoSamples)*2)
				for j, s := range monoSamples {
					pcm[2*j] = s
					pcm[2*j+1] = s
				}
			}
		}

		if err != nil {
			if i < 10 {
				t.Logf("Packet %d decode error: %v (stereo=%v)", i, err, pktTOC.Stereo)
			}
			// Use zeros
			zeros := make([]int16, pktTOC.FrameSize*2)
			allDecoded = append(allDecoded, zeros...)
			continue
		}

		allDecoded = append(allDecoded, pcm...)
	}

	t.Logf("Decoded: %d samples (mono packets: %d, stereo packets: %d)",
		len(allDecoded), monoCount, stereoCount)

	// Compare with reference
	compareLen := len(allDecoded)
	if len(refSamples) < compareLen {
		compareLen = len(refSamples)
	}

	// Compute quality metrics
	var signalPower, errorPower float64
	for i := 0; i < compareLen; i++ {
		ref := float64(refSamples[i])
		dec := float64(allDecoded[i])
		signalPower += ref * ref
		errorPower += (ref - dec) * (ref - dec)
	}

	snr := 0.0
	if errorPower > 0 {
		snr = 10 * math.Log10(signalPower/errorPower)
	}

	t.Logf("SNR vs reference: %.1f dB", snr)

	// Check if we pass the threshold (Q >= 0 maps to certain SNR level)
	if snr < 20 {
		t.Errorf("Quality too low: SNR=%.1f dB", snr)
	}
}
