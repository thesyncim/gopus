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

	// Decode entire stream once with libopus to preserve decoder state
	libSamples, err := decodeBitstreamWithLibopus(opusDemoPath, bitFile, 2)
	if err != nil {
		t.Fatalf("libopus decode failed: %v", err)
	}

	// Create Go decoder
	goDec, err := gopus.NewDecoderDefault(48000, 2)
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

	offset := 0
	for i := 0; i < maxFrames; i++ {
		pkt := packets[i]

		// Decode with Go
		goSamples, err := decodeFloat32(goDec, pkt.Data)
		if err != nil {
			t.Logf("Frame %d: Go decode error: %v", i, err)
			continue
		}

		// Compare samples against libopus stream output (16-bit PCM)
		if offset+len(goSamples) > len(libSamples) {
			t.Logf("Frame %d: libopus PCM too short (need %d, have %d)", i, offset+len(goSamples), len(libSamples))
			break
		}
		libFrame := libSamples[offset : offset+len(goSamples)]

		// Compute max abs diff and SNR
		frameDiff := 0.0
		signalPower := 0.0
		errorPower := 0.0
		for j := 0; j < len(goSamples); j++ {
			goQ := quantizeTo16(goSamples[j])
			diff := math.Abs(float64(goQ) - float64(libFrame[j]))
			if diff > frameDiff {
				frameDiff = diff
			}
			signalPower += float64(libFrame[j]) * float64(libFrame[j])
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
		if frameDiff <= 2.0/32768.0 && snr >= 60 {
			passedFrames++
		} else if i < 10 {
			// Log details for first few failing frames
			t.Logf("Frame %d: maxDiff=%.2e, SNR=%.1f dB", i, frameDiff, snr)
			// Show first few samples
			showLen := 16
			if len(goSamples) < showLen {
				showLen = len(goSamples)
			}
			goPreview := make([]float32, showLen)
			for k := 0; k < showLen; k++ {
				goPreview[k] = quantizeTo16(goSamples[k])
			}
			t.Logf("  Go[0:%d]:     %v", showLen, goPreview)
			t.Logf("  Libopus[0:%d]: %v", showLen, libFrame[:showLen])
		}

		offset += len(goSamples)
	}

	avgSNR := 0.0
	if totalFrames > 0 {
		avgSNR = sumSNR / float64(totalFrames)
	}

	t.Logf("Results: %d/%d frames passed (%.1f%%)", passedFrames, totalFrames, 100.0*float64(passedFrames)/float64(totalFrames))
	t.Logf("Max abs diff: %.2e (threshold: %.2e)", maxDiff, 2.0/32768.0)
	t.Logf("Average SNR: %.1f dB (threshold: 60 dB)", avgSNR)

	// Overall pass/fail
	if passedFrames < totalFrames {
		t.Errorf("FAIL: Only %d/%d frames meet acceptance criteria", passedFrames, totalFrames)
	}
}

// decodeBitstreamWithLibopus decodes an entire .bit file using opus_demo.
func decodeBitstreamWithLibopus(opusDemoPath string, bitFile string, channels int) ([]float32, error) {
	pcmFile := filepath.Join(os.TempDir(), "stereo_test.pcm")

	defer os.Remove(pcmFile)

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

	return samples, nil
}

// quantizeTo16 matches opus_demo 16-bit PCM output for comparison.
func quantizeTo16(x float32) float32 {
	if x > 1.0 {
		x = 1.0
	} else if x < -1.0 {
		x = -1.0
	}
	q := int32(math.Round(float64(x * 32768.0)))
	if q > 32767 {
		q = 32767
	} else if q < -32768 {
		q = -32768
	}
	return float32(int16(q)) / 32768.0
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
	monoDec, err := gopus.NewDecoderDefault(48000, 1)
	if err != nil {
		t.Fatalf("Failed to create mono decoder: %v", err)
	}
	stereoDec, err := gopus.NewDecoderDefault(48000, 2)
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
			pcm, err = decodeInt16(stereoDec, pkt.Data)
		} else {
			monoCount++
			monoSamples, decErr := decodeInt16(monoDec, pkt.Data)
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
