//go:build gopus_qext

package gopus

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus/internal/benchutil"
	"github.com/thesyncim/gopus/internal/libopustest"
)

// allOpusDemoPackets parses every packet from an opus_demo bitstream file.
// Each record is: u32 big-endian length, u32 big-endian final range, payload.
func allOpusDemoPackets(path string) ([][]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var packets [][]byte
	off := 0
	for off+8 <= len(data) {
		n := int(binary.BigEndian.Uint32(data[off : off+4]))
		off += 8 // skip length + final range
		if n < 0 || off+n > len(data) {
			return nil, os.ErrInvalid
		}
		packets = append(packets, append([]byte(nil), data[off:off+n]...))
		off += n
	}
	if len(packets) == 0 {
		return nil, os.ErrInvalid
	}
	return packets, nil
}

// encodeNative96kQEXTPackets encodes a native 96 kHz QEXT bitstream via the
// QEXT-enabled opus_demo (sampling rate 96000) and returns all packets.
func encodeNative96kQEXTPackets(t *testing.T, opusDemo string, channels int, pcm96 []float32, bitrate int) [][]byte {
	t.Helper()
	tmpDir := t.TempDir()
	inputPath := filepath.Join(tmpDir, "in96.f32")
	bitPath := filepath.Join(tmpDir, "out96.bit")
	if err := benchutil.WriteRepeatedRawFloat32(inputPath, pcm96, 1); err != nil {
		t.Fatalf("WriteRepeatedRawFloat32: %v", err)
	}
	args := []string{
		"-e", "restricted-celt", "96000", fmt.Sprint(channels), fmt.Sprint(bitrate),
		"-f32", "-complexity", "10", "-bandwidth", "FB", "-framesize", "20",
		"-qext", "-cbr", inputPath, bitPath,
	}
	cmd := exec.Command(opusDemo, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("opus_demo 96k encode failed: %v (%s)", err, out)
	}
	packets, err := allOpusDemoPackets(bitPath)
	if err != nil {
		t.Fatalf("allOpusDemoPackets: %v", err)
	}
	return packets
}

// native96kSine returns frames worth of a tone above 24 kHz (a frequency that
// only the native 96 kHz path can represent) plus a low tone, so a correct
// native decode and a 2:1-resample-of-48k decode are distinguishable.
func native96kSine(channels, frames int) []float32 {
	n := 1920 * frames
	pcm := make([]float32, n*channels)
	for i := 0; i < n; i++ {
		v := 0.30*math.Sin(2*math.Pi*6000*float64(i)/96000.0) +
			0.25*math.Sin(2*math.Pi*30000*float64(i)/96000.0)
		pcm[i*channels] = float32(v)
		if channels == 2 {
			pcm[i*channels+1] = float32(0.9 * v)
		}
	}
	return pcm
}

// TestQEXTDecode96kOracleProducesNative96k validates the new native 96 kHz QEXT
// full-packet decode oracle end-to-end: a real native 96 kHz QEXT bitstream
// (produced by the QEXT opus_demo at Fs=96000, 1920-sample frames) is decoded
// through the QEXT-enabled libopus reference at Fs=96000 and the oracle returns
// native 96 kHz PCM (1920 samples/frame) carrying real >24 kHz energy.
func TestQEXTDecode96kOracleProducesNative96k(t *testing.T) {
	libopustest.RequireOracle(t)
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	const channels = 1
	const frames = 4
	pcm96 := native96kSine(channels, frames)
	packets := encodeNative96kQEXTPackets(t, opusDemo, channels, pcm96, 320000)

	res, err := libopustest.ProbeQEXTDecode96k(libopustest.QEXTDecode96kParams{
		SampleFormat: libopustest.QEXTDecode96kFormatFloat32,
		Channels:     channels,
		MaxFrameSize: 1920,
		Packets:      packets,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "qext decode96k", err)
	}

	if len(res.PCM) == 0 {
		t.Fatal("oracle returned no PCM")
	}
	if len(res.FinalRanges) != len(packets) {
		t.Fatalf("final ranges: got %d want %d", len(res.FinalRanges), len(packets))
	}

	// Native 96 kHz decode must yield 1920 samples/frame/channel.
	samplesPerCh := len(res.PCM) / channels
	if samplesPerCh%1920 != 0 {
		t.Errorf("decoded %d samples/ch, not a multiple of native 1920", samplesPerCh)
	}

	// Verify the decode carries real high-frequency (>24 kHz) energy: a 2:1
	// resample of a 48 kHz decode could not. Use a coarse Goertzel at 30 kHz.
	mono := make([]float64, samplesPerCh)
	for i := 0; i < samplesPerCh; i++ {
		mono[i] = float64(res.PCM[i*channels])
	}
	mag := goertzelMag(mono, 30000.0, 96000.0)
	total := 0.0
	for _, v := range mono {
		total += v * v
	}
	if total == 0 {
		t.Fatal("decoded audio is all zero")
	}
	if mag <= 0 {
		t.Errorf("no measurable 30 kHz energy in native 96 kHz decode (mag=%g)", mag)
	}
	t.Logf("native 96k decode: %d frames, %d samples/ch, 30kHz mag=%.4g, finalRanges=%v",
		samplesPerCh/1920, samplesPerCh, mag, res.FinalRanges)
}

func goertzelMag(x []float64, freq, fs float64) float64 {
	if len(x) == 0 {
		return 0
	}
	w := 2 * math.Pi * freq / fs
	c := 2 * math.Cos(w)
	var s0, s1, s2 float64
	for _, v := range x {
		s0 = v + c*s1 - s2
		s2 = s1
		s1 = s0
	}
	return math.Sqrt(s1*s1 + s2*s2 - c*s1*s2)
}
