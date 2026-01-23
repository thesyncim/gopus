package testvectors

import (
	"archive/tar"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesyncim/gopus"
)

// Test vector URLs and paths
const (
	// testVectorURL is the official RFC 8251 test vector archive
	testVectorURL = "https://opus-codec.org/docs/opus_testvectors-rfc8251.tar.gz"

	// testVectorDir is where test vectors are extracted
	testVectorDir = "testdata/opus_testvectors"
)

// testVectorNames lists all 12 official test vectors
var testVectorNames = []string{
	"testvector01", "testvector02", "testvector03", "testvector04",
	"testvector05", "testvector06", "testvector07", "testvector08",
	"testvector09", "testvector10", "testvector11", "testvector12",
}

// TestDecoderCompliance runs all RFC 8251 test vectors against the gopus decoder.
// This is the main compliance test that validates decoder correctness.
//
// Test methodology:
// 1. Download and extract official test vectors (cached in testdata/)
// 2. For each test vector:
//   - Parse the .bit file (opus_demo format)
//   - Decode all packets through gopus decoder
//   - Read reference .dec file (and alternative m.dec)
//   - Compute quality metric against both references
//   - Pass if either Q >= 0 (RFC 8251 allows either reference)
func TestDecoderCompliance(t *testing.T) {
	// Ensure test vectors are available
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping compliance test: %v", err)
		return
	}

	for _, name := range testVectorNames {
		t.Run(name, func(t *testing.T) {
			runTestVector(t, name)
		})
	}
}

// runTestVector runs a single test vector through the decoder and validates output.
func runTestVector(t *testing.T, name string) {
	bitFile := filepath.Join(testVectorDir, name+".bit")
	decFile := filepath.Join(testVectorDir, name+".dec")
	mdecFile := filepath.Join(testVectorDir, name+"m.dec")

	// 1. Parse .bit file
	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Fatalf("Failed to parse %s: %v", bitFile, err)
	}

	if len(packets) == 0 {
		t.Fatalf("No packets in %s", bitFile)
	}

	// Track frame sizes and modes encountered
	type frameSizeMode struct {
		frameSize int
		mode      string // "SILK", "CELT", or "Hybrid"
	}
	frameSizeModes := make(map[frameSizeMode]int)

	t.Logf("Test vector %s: %d packets", name, len(packets))

	// Parse each packet's TOC to extract mode and frame size for tracking
	for i, pkt := range packets {
		if len(pkt.Data) > 0 {
			tocByte := pkt.Data[0]
			cfg := tocByte >> 3
			fs := getFrameSizeFromConfig(cfg)
			mode := getModeFromConfig(cfg)

			key := frameSizeMode{frameSize: fs, mode: mode}
			frameSizeModes[key]++

			if i == 0 {
				stereo := (tocByte & 0x04) != 0
				t.Logf("  First packet: Config=%d, Mode=%s, Stereo=%v, FrameSize=%d (%.1fms)",
					cfg, mode, stereo, fs, float64(fs)/48.0)
			}
		}
	}

	// Report frame size and mode distribution
	t.Logf("  Frame sizes by mode:")
	for fsm, count := range frameSizeModes {
		t.Logf("    %s %d samples (%.1fms): %d packets",
			fsm.mode, fsm.frameSize, float64(fsm.frameSize)/48.0, count)

		// Flag if extended frame size appears in Hybrid mode (unexpected per RFC)
		isExtended := fsm.frameSize == 120 || fsm.frameSize == 240 || // 2.5/5ms
			fsm.frameSize == 1920 || fsm.frameSize == 2880 // 40/60ms
		if isExtended && fsm.mode == "Hybrid" {
			t.Logf("    WARNING: Extended frame size %d in Hybrid mode (unexpected per RFC 6716)",
				fsm.frameSize)
		}
	}

	// 2. Determine decoder parameters from first packet TOC
	toc := packets[0].Data[0]
	config := toc >> 3
	stereo := (toc & 0x04) != 0
	channels := 1
	if stereo {
		channels = 2
	}
	frameSize := getFrameSizeFromConfig(config)

	t.Logf("  Config: %d, Stereo: %v, FrameSize: %d samples", config, stereo, frameSize)

	// 3. Create decoder (always use 48kHz for native output)
	dec, err := gopus.NewDecoder(48000, channels)
	if err != nil {
		t.Fatalf("Failed to create decoder: %v", err)
	}

	// 4. Decode all packets
	// Note: Opus packets can contain multiple frames (code 1/2/3 in TOC byte).
	// The decoder returns all frames combined, so we use DecodeInt16Slice
	// which allocates the correct buffer size based on packet structure.
	var allDecoded []int16
	decodeErrors := make(map[string]int) // Track error types
	for i, pkt := range packets {
		// Decode the packet using auto-allocating method for multi-frame support
		pcm, err := dec.DecodeInt16Slice(pkt.Data)
		if err != nil {
			// Log more detail about the failure
			errKey := err.Error()
			decodeErrors[errKey]++
			if decodeErrors[errKey] <= 3 { // Only log first 3 occurrences of each error type
				tocByte := pkt.Data[0]
				cfg := tocByte >> 3
				fs := getFrameSizeFromConfig(cfg)
				mode := getModeFromConfig(cfg)
				t.Logf("  Packet %d decode error: %v (config=%d, mode=%s, frameSize=%d)",
					i, err, cfg, mode, fs)
			}
			// Use zeros for failed packets (based on single frame size)
			pktFrameSize := frameSize
			if len(pkt.Data) > 0 {
				pktCfg := pkt.Data[0] >> 3
				pktFrameSize = getFrameSizeFromConfig(pktCfg)
			}
			zeros := make([]int16, pktFrameSize*channels)
			allDecoded = append(allDecoded, zeros...)
			continue
		}

		allDecoded = append(allDecoded, pcm...)
	}

	// Report decode error summary
	if len(decodeErrors) > 0 {
		t.Logf("  Decode error summary:")
		for errType, count := range decodeErrors {
			t.Logf("    %q: %d packets", errType, count)
		}
	}

	t.Logf("  Decoded: %d samples (%d per channel)", len(allDecoded), len(allDecoded)/channels)

	// 4a. Convert mono output to stereo format if needed
	// opus_demo always outputs stereo (2 channels), even for mono sources.
	// For mono packets, L and R are identical. We must match this format.
	if channels == 1 {
		t.Logf("  Converting mono output to stereo (duplicating samples)")
		allDecoded = duplicateMonoToStereo(allDecoded)
		t.Logf("  After conversion: %d samples (stereo)", len(allDecoded))
	}

	// 5. Read reference files
	reference, err := readPCMFile(decFile)
	if err != nil {
		t.Fatalf("Failed to read reference %s: %v", decFile, err)
	}

	referenceAlt, err := readPCMFile(mdecFile)
	if err != nil {
		// Alternative reference may not exist for all vectors
		t.Logf("  No alternative reference (m.dec): %v", err)
		referenceAlt = nil
	}

	t.Logf("  Reference: %d samples", len(reference))

	// 6. Compute quality metrics
	q1 := ComputeQuality(allDecoded, reference, 48000)
	t.Logf("  Quality vs .dec: Q=%.2f (threshold: Q >= 0)", q1)

	var q2 float64
	if referenceAlt != nil {
		q2 = ComputeQuality(allDecoded, referenceAlt, 48000)
		t.Logf("  Quality vs m.dec: Q=%.2f", q2)
	}

	// 7. Pass if either Q >= 0
	passes := QualityPasses(q1) || (referenceAlt != nil && QualityPasses(q2))
	if !passes {
		t.Errorf("FAILED: Quality below threshold. Q1=%.2f, Q2=%.2f", q1, q2)
	} else {
		t.Logf("  PASS: Quality meets threshold")
	}
}

// readPCMFile reads raw signed 16-bit little-endian PCM samples from a file.
// This is the format used by opus_demo for .dec reference files.
func readPCMFile(filename string) ([]int16, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	// Each sample is 2 bytes (int16 little-endian)
	if len(data)%2 != 0 {
		return nil, fmt.Errorf("odd number of bytes in PCM file")
	}

	samples := make([]int16, len(data)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(data[i*2:]))
	}

	return samples, nil
}

// duplicateMonoToStereo converts mono PCM samples to stereo by duplicating each sample.
// This matches opus_demo behavior which always outputs stereo (L=R for mono sources).
// Input:  [S0, S1, S2, ...]
// Output: [S0, S0, S1, S1, S2, S2, ...]
func duplicateMonoToStereo(mono []int16) []int16 {
	stereo := make([]int16, len(mono)*2)
	for i, s := range mono {
		stereo[i*2] = s   // Left
		stereo[i*2+1] = s // Right (duplicate)
	}
	return stereo
}

// ensureTestVectors downloads and extracts test vectors if needed.
func ensureTestVectors(t *testing.T) error {
	// Check if test vectors already exist
	if _, err := os.Stat(filepath.Join(testVectorDir, "testvector01.bit")); err == nil {
		t.Log("Test vectors already downloaded")
		return nil
	}

	t.Log("Downloading RFC 8251 test vectors...")

	// Create testdata directory
	if err := os.MkdirAll("testdata", 0755); err != nil {
		return fmt.Errorf("failed to create testdata dir: %w", err)
	}

	// Download the archive
	resp, err := http.Get(testVectorURL)
	if err != nil {
		return fmt.Errorf("failed to download test vectors (network unavailable?): %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download test vectors: HTTP %d", resp.StatusCode)
	}

	// Extract the archive
	if err := extractTarGz(resp.Body); err != nil {
		return fmt.Errorf("failed to extract test vectors: %w", err)
	}

	t.Log("Test vectors downloaded and extracted successfully")
	return nil
}

// extractTarGz extracts a .tar.gz archive to testdata/
func extractTarGz(r io.Reader) error {
	gzr, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		// Determine output path
		// The archive contains a directory like "opus_testvectors-rfc8251/"
		// We want to extract to testdata/opus_testvectors/
		name := header.Name

		// Skip the top-level directory entry
		if header.Typeflag == tar.TypeDir {
			continue
		}

		// Extract filename from path (handle various archive structures)
		base := filepath.Base(name)
		if base == "" || base == "." {
			continue
		}

		// Only extract .bit, .dec, and m.dec files
		if !strings.HasSuffix(base, ".bit") &&
		   !strings.HasSuffix(base, ".dec") {
			continue
		}

		outPath := filepath.Join(testVectorDir, base)

		// Ensure output directory exists
		if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
			return err
		}

		// Create output file
		outFile, err := os.Create(outPath)
		if err != nil {
			return err
		}

		// Copy content
		if _, err := io.Copy(outFile, tr); err != nil {
			outFile.Close()
			return err
		}
		outFile.Close()
	}

	return nil
}

// TestSingleVector allows running a single test vector for debugging.
// Usage: go test -v -run TestSingleVector/testvector01
func TestSingleVector(t *testing.T) {
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping: %v", err)
		return
	}

	for _, name := range testVectorNames {
		t.Run(name, func(t *testing.T) {
			runTestVector(t, name)
		})
	}
}

// TestParseTestVectorBitstreams validates that all test vector .bit files can be parsed.
func TestParseTestVectorBitstreams(t *testing.T) {
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping: %v", err)
		return
	}

	for _, name := range testVectorNames {
		t.Run(name, func(t *testing.T) {
			bitFile := filepath.Join(testVectorDir, name+".bit")
			packets, err := ReadBitstreamFile(bitFile)
			if err != nil {
				t.Fatalf("Failed to parse: %v", err)
			}

			info := GetBitstreamInfo(packets)
			t.Logf("Packets: %d, Bytes: %d, Duration: %d samples (%.2fs at 48kHz)",
				info.PacketCount, info.TotalBytes, info.Duration,
				float64(info.Duration)/48000)

			// Check basic validity
			if info.PacketCount == 0 {
				t.Error("No packets parsed")
			}
		})
	}
}

// TestReadReferenceFiles validates that all .dec reference files can be read.
func TestReadReferenceFiles(t *testing.T) {
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping: %v", err)
		return
	}

	for _, name := range testVectorNames {
		t.Run(name, func(t *testing.T) {
			decFile := filepath.Join(testVectorDir, name+".dec")
			samples, err := readPCMFile(decFile)
			if err != nil {
				t.Fatalf("Failed to read .dec: %v", err)
			}

			t.Logf("Reference samples: %d (%.2fs at 48kHz stereo)",
				len(samples), float64(len(samples))/(48000*2))

			if len(samples) == 0 {
				t.Error("Empty reference file")
			}
		})
	}
}

// vectorResult holds structured results from running a single test vector.
type vectorResult struct {
	name              string
	packets           int
	frameSizes        []int    // unique frame sizes in samples
	modes             []string // unique modes
	hasExtendedHybrid bool     // true if extended frame size appears in Hybrid mode
	q1                float64  // quality vs .dec
	q2                float64  // quality vs m.dec
	passed            bool
	decodeErrors      int // total decode errors
	err               error
}

// TestComplianceSummary runs all vectors and prints a summary table.
// This provides an overview of compliance status and verifies the hybrid mode assumption.
func TestComplianceSummary(t *testing.T) {
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping: %v", err)
		return
	}

	var results []vectorResult

	for _, name := range testVectorNames {
		r := runVectorSilent(t, name)
		results = append(results, r)
	}

	// Print summary table
	t.Log("")
	t.Log("=== RFC 8251 Compliance Summary ===")
	t.Log("")
	t.Logf("%-14s | %7s | %-11s | %-18s | %7s | %8s | %s",
		"Vector", "Packets", "Modes", "Frame Sizes", "Q(.dec)", "Q(m.dec)", "Status")
	t.Log("---------------|---------|-------------|--------------------|---------|---------|---------")

	passed := 0
	hybridExtendedCount := 0
	for _, r := range results {
		status := "FAIL"
		if r.passed {
			status = "PASS"
			passed++
		}
		fsStr := formatFrameSizes(r.frameSizes)
		modesStr := strings.Join(r.modes, ",")
		t.Logf("%-14s | %7d | %-11s | %-18s | %7.2f | %8.2f | %s",
			r.name, r.packets, modesStr, fsStr, r.q1, r.q2, status)
		if r.hasExtendedHybrid {
			hybridExtendedCount++
		}
	}

	t.Log("")
	t.Logf("Overall: %d/%d passed", passed, len(results))

	// Report on hybrid mode verification
	t.Log("")
	if hybridExtendedCount == 0 {
		t.Log("Hybrid mode verification: CONFIRMED - no extended frame sizes in Hybrid mode")
		t.Log("  Extended sizes (2.5/5/40/60ms) appear only in SILK or CELT modes as expected per RFC 6716")
	} else {
		t.Logf("Hybrid mode verification: UNEXPECTED - %d vectors have extended frame sizes in Hybrid mode", hybridExtendedCount)
	}

	if passed < len(results) {
		t.Log("")
		t.Log("Note: Q >= 0 required for compliance per RFC 8251")
	}
}

// formatFrameSizes converts sample counts to millisecond string.
func formatFrameSizes(sizes []int) string {
	if len(sizes) == 0 {
		return "-"
	}
	// Convert to ms and format
	var ms []string
	for _, s := range sizes {
		ms = append(ms, fmt.Sprintf("%.1f", float64(s)/48.0))
	}
	return strings.Join(ms, ",") + "ms"
}

// TestMonoCELTReferenceFormat investigates the format of testvector07 reference file.
// This test diagnoses whether the 2x sample count difference is because libopus
// outputs stereo even for mono sources.
func TestMonoCELTReferenceFormat(t *testing.T) {
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping: %v", err)
		return
	}

	name := "testvector07"
	bitFile := filepath.Join(testVectorDir, name+".bit")
	decFile := filepath.Join(testVectorDir, name+".dec")

	// 1. Parse .bit file to get packet info
	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Fatalf("Failed to parse %s: %v", bitFile, err)
	}

	t.Logf("Total packets: %d", len(packets))

	// Check TOC byte of first packet for stereo flag
	if len(packets) > 0 && len(packets[0].Data) > 0 {
		tocByte := packets[0].Data[0]
		stereo := (tocByte & 0x04) != 0
		config := tocByte >> 3
		mode := getModeFromConfig(config)
		t.Logf("First packet TOC: config=%d, mode=%s, stereo=%v", config, mode, stereo)
		if stereo {
			t.Logf("  TOC indicates stereo source")
		} else {
			t.Logf("  TOC indicates MONO source (stereo flag = 0)")
		}
	}

	// 2. Calculate expected decoded samples (sum of frameSize * frameCount per packet)
	var expectedSamples int
	for _, pkt := range packets {
		if len(pkt.Data) == 0 {
			continue
		}
		tocByte := pkt.Data[0]
		config := tocByte >> 3
		frameCode := tocByte & 0x03
		frameSize := getFrameSizeFromConfig(config)

		// Calculate frame count from frame code
		frameCount := 1
		switch frameCode {
		case 0:
			frameCount = 1
		case 1, 2:
			frameCount = 2
		case 3:
			// Variable frame count - need to parse byte at offset 1
			if len(pkt.Data) > 1 {
				frameCount = int(pkt.Data[1] & 0x3F)
				if frameCount == 0 {
					frameCount = 1
				}
			}
		}

		expectedSamples += frameSize * frameCount
	}
	t.Logf("Expected decoded samples (sum of frameSize*frameCount): %d", expectedSamples)

	// 3. Read reference file and count samples
	reference, err := readPCMFile(decFile)
	if err != nil {
		t.Fatalf("Failed to read reference %s: %v", decFile, err)
	}
	t.Logf("Reference file samples: %d", len(reference))

	// 4. Analyze the ratio
	ratio := float64(len(reference)) / float64(expectedSamples)
	t.Logf("Reference / Expected ratio: %.4f", ratio)

	if ratio >= 1.95 && ratio <= 2.05 {
		t.Logf("DIAGNOSIS: Reference file contains STEREO output for MONO source")
		t.Logf("  This is expected libopus behavior: opus_demo outputs 2-channel stereo")
		t.Logf("  even when input is mono (L and R are identical).")
	} else if ratio >= 0.95 && ratio <= 1.05 {
		t.Logf("DIAGNOSIS: Reference has mono samples, decoder may have a bug")
	} else {
		t.Logf("DIAGNOSIS: Unexpected ratio - further investigation needed")
	}

	// 5. Check if first few reference samples appear to be stereo-from-mono (L==R)
	t.Logf("\nReference sample analysis (first 20 samples as L/R pairs):")
	identicalPairs := 0
	pairsToCheck := 10
	if len(reference) >= pairsToCheck*2 {
		for i := 0; i < pairsToCheck; i++ {
			L := reference[i*2]
			R := reference[i*2+1]
			if L == R {
				identicalPairs++
			}
			t.Logf("  Pair %d: L=%6d, R=%6d, identical=%v", i, L, R, L == R)
		}
		t.Logf("Identical L/R pairs: %d/%d", identicalPairs, pairsToCheck)
		if identicalPairs >= pairsToCheck-1 {
			t.Logf("CONCLUSION: Reference is stereo-from-mono (L==R for all pairs)")
			t.Logf("  To match reference, decoder must output mono duplicated to stereo")
		}
	}

	// Output diagnostic summary
	t.Logf("\n=== SUMMARY ===")
	t.Logf("Reference sample count: %d", len(reference))
	t.Logf("Expected decoded (mono): %d", expectedSamples)
	t.Logf("Ratio (reference/expected): %.4f", ratio)
	if ratio >= 1.95 && ratio <= 2.05 {
		t.Logf("Reference appears to be: STEREO-from-MONO (2 channels, L==R)")
	}
}

// runVectorSilent runs a test vector and returns structured results without verbose logging.
func runVectorSilent(t *testing.T, name string) vectorResult {
	result := vectorResult{name: name}

	bitFile := filepath.Join(testVectorDir, name+".bit")
	decFile := filepath.Join(testVectorDir, name+".dec")
	mdecFile := filepath.Join(testVectorDir, name+"m.dec")

	// 1. Parse .bit file
	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		result.err = err
		return result
	}

	if len(packets) == 0 {
		result.err = fmt.Errorf("no packets in %s", bitFile)
		return result
	}

	result.packets = len(packets)

	// Track frame sizes and modes encountered
	frameSizeSet := make(map[int]bool)
	modeSet := make(map[string]bool)

	for _, pkt := range packets {
		if len(pkt.Data) > 0 {
			tocByte := pkt.Data[0]
			cfg := tocByte >> 3
			fs := getFrameSizeFromConfig(cfg)
			mode := getModeFromConfig(cfg)

			frameSizeSet[fs] = true
			modeSet[mode] = true

			// Check for extended frame size in Hybrid mode
			isExtended := fs == 120 || fs == 240 || fs == 1920 || fs == 2880
			if isExtended && mode == "Hybrid" {
				result.hasExtendedHybrid = true
			}
		}
	}

	// Convert sets to slices
	for fs := range frameSizeSet {
		result.frameSizes = append(result.frameSizes, fs)
	}
	for mode := range modeSet {
		result.modes = append(result.modes, mode)
	}

	// 2. Determine decoder parameters from first packet TOC
	toc := packets[0].Data[0]
	config := toc >> 3
	stereo := (toc & 0x04) != 0
	channels := 1
	if stereo {
		channels = 2
	}
	frameSize := getFrameSizeFromConfig(config)

	// 3. Create decoder
	dec, err := gopus.NewDecoder(48000, channels)
	if err != nil {
		result.err = err
		return result
	}

	// 4. Decode all packets (using auto-allocating method for multi-frame support)
	var allDecoded []int16
	for _, pkt := range packets {
		pcm, err := dec.DecodeInt16Slice(pkt.Data)
		if err != nil {
			result.decodeErrors++
			// Use zeros for failed packets
			pktFrameSize := frameSize
			if len(pkt.Data) > 0 {
				pktCfg := pkt.Data[0] >> 3
				pktFrameSize = getFrameSizeFromConfig(pktCfg)
			}
			zeros := make([]int16, pktFrameSize*channels)
			allDecoded = append(allDecoded, zeros...)
			continue
		}
		allDecoded = append(allDecoded, pcm...)
	}

	// 4a. Convert mono output to stereo format if needed
	// opus_demo always outputs stereo (2 channels), even for mono sources.
	if channels == 1 {
		allDecoded = duplicateMonoToStereo(allDecoded)
	}

	// 5. Read reference files
	reference, err := readPCMFile(decFile)
	if err != nil {
		result.err = err
		return result
	}

	referenceAlt, _ := readPCMFile(mdecFile)

	// 6. Compute quality metrics
	result.q1 = ComputeQuality(allDecoded, reference, 48000)
	if referenceAlt != nil {
		result.q2 = ComputeQuality(allDecoded, referenceAlt, 48000)
	}

	// 7. Pass if either Q >= 0
	result.passed = QualityPasses(result.q1) || (referenceAlt != nil && QualityPasses(result.q2))

	return result
}
