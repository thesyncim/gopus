package celt_test

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/thesyncim/gopus"
)

const antiCollapseFixturePath = "testdata/anticollapse_libopus_fixture.json"

type antiCollapseFixtureFile struct {
	Version int                      `json:"version"`
	Cases   []antiCollapseFixtureCase `json:"cases"`
}

type antiCollapseFixtureCase struct {
	Name             string   `json:"name"`
	FrameSize        int      `json:"frame_size"`
	Bitrate          int      `json:"bitrate"`
	NumFrames        int      `json:"num_frames"`
	PreSkip          int      `json:"pre_skip"`
	PacketsBase64    []string `json:"packets_base64"`
	DecodedF32Base64 string   `json:"decoded_f32le_base64"`
}

var (
	antiCollapseFixtureOnce sync.Once
	antiCollapseFixtureData antiCollapseFixtureFile
	antiCollapseFixtureErr  error
)

// TestAntiCollapseVsLibopus compares anti-collapse behavior against libopus.
// This test generates audio, encodes with libopus (which may trigger anti-collapse),
// and decodes with both libopus and gopus to compare outputs.
func TestAntiCollapseVsLibopus(t *testing.T) {
	if _, err := loadAntiCollapseFixture(); err != nil {
		if !checkOpusdecAvailableAnticollapse() || !checkOpusencAvailableAnticollapse() {
			t.Fatalf("anti-collapse fixture unavailable and opusenc/opusdec missing: %v", err)
		}
		t.Logf("fixture unavailable (%v), using live opusenc/opusdec generation", err)
	}

	// Create a test signal that's likely to trigger anti-collapse:
	// - Transient signal (sudden onset)
	// - Low bitrate to force collapsing
	// - Frame sizes that enable anti-collapse (LM >= 2)

	tests := []struct {
		name      string
		signal    func(int) []float32 // generates signal of given length
		frameSize int
		bitrate   int
	}{
		{
			name:      "transient_20ms_32k",
			signal:    generateTransientSignalAnticollapse,
			frameSize: 960,
			bitrate:   32000,
		},
		{
			name:      "transient_10ms_24k",
			signal:    generateTransientSignalAnticollapse,
			frameSize: 480,
			bitrate:   24000,
		},
		{
			name:      "impulse_20ms_32k",
			signal:    generateImpulseSignal,
			frameSize: 960,
			bitrate:   32000,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			compareAntiCollapseOutput(t, tc.name, tc.signal, tc.frameSize, tc.bitrate)
		})
	}
}

func generateTransientSignalAnticollapse(length int) []float32 {
	pcm := make([]float32, length)
	// Start with silence, then sudden burst of energy
	silentPart := length / 4
	for i := silentPart; i < length; i++ {
		// High-frequency burst
		t := float64(i-silentPart) / 48000.0
		pcm[i] = float32(0.5 * math.Sin(2*math.Pi*4000*t) * math.Exp(-3*t))
	}
	return pcm
}

func generateImpulseSignal(length int) []float32 {
	pcm := make([]float32, length)
	// Single impulse followed by decay
	impulsePos := length / 3
	pcm[impulsePos] = 0.9
	// Exponential decay with some noise
	for i := impulsePos + 1; i < length && i < impulsePos+200; i++ {
		decay := math.Exp(-float64(i-impulsePos) / 20.0)
		pcm[i] = float32(0.5 * decay)
	}
	return pcm
}

func compareAntiCollapseOutput(t *testing.T, name string, signalGen func(int) []float32, frameSize, bitrate int) {
	numFrames := 10
	var (
		opusPackets    [][]byte
		libopusDecoded []float32
		preSkip        int
		err            error
	)

	if fixture, ferr := getAntiCollapseFixtureCase(name); ferr == nil {
		if fixture.FrameSize != frameSize || fixture.Bitrate != bitrate {
			t.Fatalf("fixture metadata mismatch for %s", name)
		}
		opusPackets, err = decodeFixturePacketsBase64(fixture.PacketsBase64)
		if err != nil {
			t.Fatalf("decode fixture packets: %v", err)
		}
		libopusDecoded, err = decodeFixtureFloat32Base64(fixture.DecodedF32Base64)
		if err != nil {
			t.Fatalf("decode fixture pcm: %v", err)
		}
		preSkip = fixture.PreSkip
	} else {
		// Fallback: live libopus generation if fixture missing.
		if !checkOpusdecAvailableAnticollapse() || !checkOpusencAvailableAnticollapse() {
			t.Fatalf("fixture missing and opusenc/opusdec unavailable: %v", ferr)
		}
		opusPackets, libopusDecoded, preSkip, err = generateAntiCollapseReference(name, signalGen, frameSize, bitrate, numFrames)
		if err != nil {
			t.Fatalf("generate live reference: %v", err)
		}
	}

	for _, pkt := range opusPackets {
		if !isCELTPacket(pkt) {
			t.Fatalf("non-CELT packet detected in fixture/reference for %s (toc=0x%02x)", name, pkt[0])
		}
	}

	// Decode with gopus
	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("Failed to create gopus decoder: %v", err)
	}
	var gopusDecoded []float32
	pcmBuf := make([]float32, 5760) // 60ms @ 48kHz, mono

	for _, pkt := range opusPackets {
		if len(pkt) == 0 {
			continue
		}
		n, err := dec.Decode(pkt, pcmBuf)
		if err != nil {
			t.Logf("Warning: decode error on packet: %v", err)
			continue
		}
		gopusDecoded = append(gopusDecoded, pcmBuf[:n]...)
	}

	// Apply Opus pre-skip (opusdec already does this).
	if preSkip > 0 {
		skip := preSkip
		if skip < len(gopusDecoded) {
			gopusDecoded = gopusDecoded[skip:]
		} else {
			gopusDecoded = nil
		}
	}

	// Compare outputs
	minLen := len(libopusDecoded)
	if len(gopusDecoded) < minLen {
		minLen = len(gopusDecoded)
	}

	if minLen == 0 {
		t.Fatal("No samples to compare")
	}

	// Compute metrics
	maxDiff := float64(0)
	sumSquaredErr := float64(0)
	sumSignal := float64(0)

	for i := 0; i < minLen; i++ {
		diff := math.Abs(float64(gopusDecoded[i] - libopusDecoded[i]))
		if diff > maxDiff {
			maxDiff = diff
		}
		sumSquaredErr += diff * diff
		sumSignal += float64(libopusDecoded[i]) * float64(libopusDecoded[i])
	}

	mse := sumSquaredErr / float64(minLen)
	snr := 10 * math.Log10(sumSignal/sumSquaredErr)

	t.Logf("Comparison results:")
	t.Logf("  Samples compared: %d", minLen)
	t.Logf("  Max abs diff: %.6f", maxDiff)
	t.Logf("  MSE: %.9f", mse)
	t.Logf("  SNR: %.2f dB", snr)

	// Acceptance criteria from LIBOPUS_VALIDATION_PLAN.md
	if maxDiff > 1e-5 {
		t.Errorf("Max abs diff %.6f exceeds 1e-5 threshold", maxDiff)
	}
	if snr < 90 {
		t.Errorf("SNR %.2f dB is below 90 dB threshold", snr)
	}
}

func generateAntiCollapseReference(name string, signalGen func(int) []float32, frameSize, bitrate, numFrames int) ([][]byte, []float32, int, error) {
	totalSamples := numFrames * frameSize
	pcmF32 := signalGen(totalSamples)
	pcmS16 := float32ToInt16Samples(pcmF32)

	tmpDir, err := os.MkdirTemp("", "anticollapse_test")
	if err != nil {
		return nil, nil, 0, err
	}
	defer os.RemoveAll(tmpDir)

	rawPath := filepath.Join(tmpDir, "input.raw")
	opusPath := filepath.Join(tmpDir, "encoded.opus")
	decPath := filepath.Join(tmpDir, "decoded.raw")

	if err := writeRawPCM(rawPath, pcmS16); err != nil {
		return nil, nil, 0, err
	}

	frameSizeMsStr := "20"
	switch frameSize {
	case 120:
		frameSizeMsStr = "2.5"
	case 240:
		frameSizeMsStr = "5"
	case 480:
		frameSizeMsStr = "10"
	case 960:
		frameSizeMsStr = "20"
	case 1920:
		frameSizeMsStr = "40"
	case 2880:
		frameSizeMsStr = "60"
	}

	cmd := exec.Command("opusenc",
		"--raw", "--raw-rate", "48000", "--raw-chan", "1",
		"--music",
		"--hard-cbr", "--bitrate", fmt.Sprintf("%d", bitrate/1000),
		"--framesize", frameSizeMsStr,
		rawPath, opusPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, nil, 0, fmt.Errorf("opusenc failed for %s: %v\n%s", name, err, out)
	}

	cmd = exec.Command("opusdec", "--float", "--force-wav", opusPath, decPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, nil, 0, fmt.Errorf("opusdec failed for %s: %v\n%s", name, err, out)
	}

	libopusDecoded, err := readWavFloat32(decPath)
	if err != nil {
		return nil, nil, 0, err
	}
	opusPackets, preSkip, err := extractOpusPackets(opusPath)
	if err != nil {
		return nil, nil, 0, err
	}
	return opusPackets, libopusDecoded, preSkip, nil
}

func isCELTPacket(pkt []byte) bool {
	if len(pkt) == 0 {
		return false
	}
	config := pkt[0] >> 3
	return config >= 16
}

func checkOpusdecAvailableAnticollapse() bool {
	_, err := exec.LookPath("opusdec")
	return err == nil
}

func checkOpusencAvailableAnticollapse() bool {
	_, err := exec.LookPath("opusenc")
	return err == nil
}

func float32ToInt16Samples(f32 []float32) []int16 {
	s16 := make([]int16, len(f32))
	for i, v := range f32 {
		// Clamp and convert
		sample := v * 32767
		if sample > 32767 {
			sample = 32767
		}
		if sample < -32768 {
			sample = -32768
		}
		s16[i] = int16(sample)
	}
	return s16
}

func writeRawPCM(path string, samples []int16) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return binary.Write(f, binary.LittleEndian, samples)
}

func readWavFloat32(path string) ([]float32, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Find "data" chunk
	dataIdx := bytes.Index(data, []byte("data"))
	if dataIdx == -1 {
		return nil, fmt.Errorf("no data chunk found")
	}

	// Read chunk size
	chunkSize := binary.LittleEndian.Uint32(data[dataIdx+4:])
	audioData := data[dataIdx+8 : dataIdx+8+int(chunkSize)]

	// Parse as float32
	numSamples := len(audioData) / 4
	samples := make([]float32, numSamples)
	for i := 0; i < numSamples; i++ {
		bits := binary.LittleEndian.Uint32(audioData[i*4:])
		samples[i] = math.Float32frombits(bits)
	}

	return samples, nil
}

func extractOpusPackets(opusPath string) ([][]byte, int, error) {
	data, err := os.ReadFile(opusPath)
	if err != nil {
		return nil, 0, err
	}

	var packets [][]byte
	preSkip := 0

	// Parse OGG pages to extract Opus packets
	offset := 0
	pktNum := 0
	var currentPacket []byte
	for offset < len(data)-27 {
		// Check for OggS magic
		if !bytes.Equal(data[offset:offset+4], []byte("OggS")) {
			offset++
			continue
		}

		// Parse OGG page header
		numSegments := int(data[offset+26])
		if offset+27+numSegments > len(data) {
			break
		}

		segmentTable := data[offset+27 : offset+27+numSegments]
		pageDataStart := offset + 27 + numSegments

		// Calculate total page data size
		totalSize := 0
		for _, s := range segmentTable {
			totalSize += int(s)
		}

		if pageDataStart+totalSize > len(data) {
			break
		}

		// Extract packet(s) from this page
		pageData := data[pageDataStart : pageDataStart+totalSize]

		// Handle packet segmentation with continuation across pages.
		packetStart := 0
		for _, segSize := range segmentTable {
			if packetStart+int(segSize) > len(pageData) {
				break
			}
			if segSize > 0 {
				currentPacket = append(currentPacket, pageData[packetStart:packetStart+int(segSize)]...)
			}
			packetStart += int(segSize)
			// segSize < 255 indicates end of packet
			if segSize < 255 {
				// Skip OpusHead and OpusTags packets
				if pktNum == 0 {
					// Parse pre-skip from OpusHead
					if len(currentPacket) >= 12 && bytes.Equal(currentPacket[0:8], []byte("OpusHead")) {
						preSkip = int(binary.LittleEndian.Uint16(currentPacket[10:12]))
					}
				}
				if pktNum >= 2 && len(currentPacket) > 0 {
					packets = append(packets, append([]byte(nil), currentPacket...))
				}
				pktNum++
				currentPacket = currentPacket[:0]
			}
		}

		offset = pageDataStart + totalSize
	}

	return packets, preSkip, nil
}

func loadAntiCollapseFixture() (antiCollapseFixtureFile, error) {
	antiCollapseFixtureOnce.Do(func() {
		data, err := os.ReadFile(antiCollapseFixturePath)
		if err != nil {
			antiCollapseFixtureErr = err
			return
		}
		if err := json.Unmarshal(data, &antiCollapseFixtureData); err != nil {
			antiCollapseFixtureErr = err
			return
		}
		if antiCollapseFixtureData.Version != 1 {
			antiCollapseFixtureErr = fmt.Errorf("unsupported anti-collapse fixture version %d", antiCollapseFixtureData.Version)
			return
		}
	})
	return antiCollapseFixtureData, antiCollapseFixtureErr
}

func getAntiCollapseFixtureCase(name string) (antiCollapseFixtureCase, error) {
	fixture, err := loadAntiCollapseFixture()
	if err != nil {
		return antiCollapseFixtureCase{}, err
	}
	for _, c := range fixture.Cases {
		if c.Name == name {
			return c, nil
		}
	}
	return antiCollapseFixtureCase{}, fmt.Errorf("case %q not found in fixture", name)
}

func decodeFixturePacketsBase64(src []string) ([][]byte, error) {
	out := make([][]byte, len(src))
	for i, s := range src {
		pkt, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return nil, err
		}
		out[i] = pkt
	}
	return out, nil
}

func decodeFixtureFloat32Base64(s string) ([]float32, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	if len(raw)%4 != 0 {
		return nil, fmt.Errorf("invalid float32 fixture byte length %d", len(raw))
	}
	out := make([]float32, len(raw)/4)
	for i := 0; i < len(out); i++ {
		bits := binary.LittleEndian.Uint32(raw[i*4:])
		out[i] = math.Float32frombits(bits)
	}
	return out, nil
}
