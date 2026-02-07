package testvectors

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var ffmpegParityMinQByCase = map[string]float64{
	"celt-fb-10ms-mono-64k":     60.0,
	"celt-fb-20ms-mono-64k":     60.0,
	"celt-fb-20ms-stereo-128k":  50.0,
	"hybrid-fb-10ms-mono-24k":   -105.0,
	"hybrid-fb-10ms-stereo-24k": -105.0,
	"hybrid-swb-10ms-mono-24k":  -105.0,
	"silk-nb-10ms-mono-16k":     -105.0,
	"silk-nb-20ms-mono-16k":     -105.0,
	"silk-wb-20ms-mono-32k":     -105.0,
	"silk-wb-20ms-stereo-48k":   -105.0,
}

func checkFFmpegAvailable() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
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
			minQ, ok := ffmpegParityMinQByCase[c.Name]
			if !ok {
				t.Fatalf("missing ffmpeg parity threshold for case %q", c.Name)
			}
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
