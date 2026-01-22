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

	"gopus"
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

	t.Logf("Test vector %s: %d packets", name, len(packets))

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
	var allDecoded []int16
	for i, pkt := range packets {
		// Decode the packet
		pcm := make([]int16, frameSize*channels)
		n, err := dec.DecodeInt16(pkt.Data, pcm)
		if err != nil {
			t.Logf("  Packet %d decode error (non-fatal): %v", i, err)
			// Use zeros for failed packets (PLC would be better but this is for testing)
			allDecoded = append(allDecoded, pcm[:frameSize*channels]...)
			continue
		}

		allDecoded = append(allDecoded, pcm[:n*channels]...)
	}

	t.Logf("  Decoded: %d samples (%d per channel)", len(allDecoded), len(allDecoded)/channels)

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
