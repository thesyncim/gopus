package celt_test

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sync"
	"testing"

	"github.com/thesyncim/gopus"
)

const antiCollapseFixturePath = "testdata/anticollapse_libopus_fixture.json"

type antiCollapseFixtureFile struct {
	Version int                       `json:"version"`
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
		t.Fatalf("anti-collapse fixture unavailable: %v", err)
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
	var (
		opusPackets    [][]byte
		libopusDecoded []float32
		preSkip        int
		err            error
	)

	fixture, ferr := getAntiCollapseFixtureCase(name)
	if ferr != nil {
		t.Fatalf("load anti-collapse fixture case %s: %v", name, ferr)
	}
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

	expectedSamples := fixture.NumFrames * frameSize
	if expectedSamples > 0 && len(libopusDecoded) != expectedSamples {
		t.Fatalf("fixture decoded sample length mismatch for %s: got %d want %d", name, len(libopusDecoded), expectedSamples)
	}
	if signalGen != nil {
		signal := signalGen(expectedSamples)
		if len(signal) != expectedSamples {
			t.Fatalf("signal generator length mismatch for %s: got %d want %d", name, len(signal), expectedSamples)
		}
	}

	for _, pkt := range opusPackets {
		if len(pkt) == 0 {
			t.Fatalf("empty packet found in fixture for %s", name)
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

func isCELTPacket(pkt []byte) bool {
	if len(pkt) == 0 {
		return false
	}
	config := pkt[0] >> 3
	return config >= 16
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
