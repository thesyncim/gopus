package testvectors

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func checkFFmpegAvailable() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

func ffmpegParityMinQ(c libopusDecoderMatrixCaseFile) float64 {
	if strings.HasPrefix(c.Name, "hybrid-") || c.ModeHistogram["hybrid"] > 0 {
		return -110.0
	}
	if decoderDominantMode(c.ModeHistogram) == "celt" {
		return 45.0
	}
	// ffmpeg may use a different libopus build/config, so non-CELT paths are
	// checked as broad compatibility rather than tight waveform parity.
	return -110.0
}

func decodeWithFFmpeg(oggData []byte, channels int) ([]float32, error) {
	tmpDir, err := os.MkdirTemp("", "gopus-ffmpeg-decode-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	opusPath := filepath.Join(tmpDir, "in.opus")
	pcmPath := filepath.Join(tmpDir, "out.f32")
	if err := os.WriteFile(opusPath, oggData, 0o644); err != nil {
		return nil, fmt.Errorf("write opus input: %w", err)
	}

	args := []string{
		"-v", "error",
		"-nostdin",
		"-y",
		"-i", opusPath,
		"-f", "f32le",
		"-acodec", "pcm_f32le",
		"-ar", "48000",
		"-ac", fmt.Sprintf("%d", channels),
		pcmPath,
	}
	cmd := exec.Command("ffmpeg", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg decode failed: %v (%s)", err, out)
	}
	raw, err := os.ReadFile(pcmPath)
	if err != nil {
		return nil, fmt.Errorf("read ffmpeg output: %w", err)
	}
	if len(raw)%4 != 0 {
		return nil, fmt.Errorf("ffmpeg output length must be multiple of 4, got %d", len(raw))
	}
	out := make([]float32, len(raw)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4 : i*4+4]))
	}
	return out, nil
}

func TestDecoderParityMatrixWithFFmpeg(t *testing.T) {
	requireTestTier(t, testTierParity)

	if !checkFFmpegAvailable() {
		t.Skip("ffmpeg not available")
	}
	fixture, err := loadLibopusDecoderMatrixFixture()
	if err != nil {
		t.Fatalf("load decoder matrix fixture: %v", err)
	}
	for _, c := range fixture.Cases {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			minQ := ffmpegParityMinQ(c)
			packets, err := decodeLibopusDecoderMatrixPackets(c)
			if err != nil {
				t.Fatalf("decode fixture packets: %v", err)
			}
			var ogg bytes.Buffer
			if err := writeOggOpusEncoder(&ogg, packets, c.Channels, 48000, c.FrameSize); err != nil {
				t.Fatalf("write ogg: %v", err)
			}
			ffmpegDecoded, err := decodeWithFFmpeg(ogg.Bytes(), c.Channels)
			if err != nil {
				t.Fatalf("decode with ffmpeg: %v", err)
			}
			refDecoded, err := decodeLibopusDecoderMatrixSamples(c)
			if err != nil {
				t.Fatalf("decode fixture f32: %v", err)
			}

			compareLen := len(refDecoded)
			if len(ffmpegDecoded) < compareLen {
				compareLen = len(ffmpegDecoded)
			}
			maxDelay := 4 * c.FrameSize
			if maxDelay < 960 {
				maxDelay = 960
			}
			q, delay := ComputeQualityFloat32WithDelay(refDecoded[:compareLen], ffmpegDecoded[:compareLen], 48000, maxDelay)
			t.Logf("ffmpeg parity: Q=%.2f SNR=%.2f delay=%d", q, SNRFromQuality(q), delay)
			if q < minQ {
				t.Fatalf("ffmpeg parity regression: Q=%.2f < %.2f", q, minQ)
			}
		})
	}
}
