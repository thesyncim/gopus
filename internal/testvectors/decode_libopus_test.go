package testvectors

import (
	"bytes"
	"math"
	"os"
	"os/exec"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

func TestDecodeLibopusPacket(t *testing.T) {
	// Generate test signal
	sampleRate := 48000
	frameSize := 960
	freq := 440.0

	pcm := make([]float64, frameSize)
	for i := 0; i < frameSize; i++ {
		ti := float64(i) / float64(sampleRate)
		pcm[i] = 0.5 * math.Sin(2*math.Pi*freq*ti)
	}

	// Save as WAV (convert to float32 for WAV)
	pcm32 := make([]float32, len(pcm))
	for i, v := range pcm {
		pcm32[i] = float32(v)
	}
	saveTestWAV("/tmp/decode_test.wav", pcm32, sampleRate, 1)

	// Encode with libopus
	cmd := exec.Command("opusenc", "--hard-cbr", "--bitrate", "64", "--framesize", "20",
		"/tmp/decode_test.wav", "/tmp/decode_test_lib.opus")
	if err := cmd.Run(); err != nil {
		t.Fatalf("opusenc failed: %v", err)
	}

	// Extract the raw opus packet from the ogg file
	packet := extractFirstAudioPacket("/tmp/decode_test_lib.opus")
	if packet == nil {
		t.Fatal("Failed to extract audio packet")
	}
	t.Logf("Libopus packet: %d bytes", len(packet))
	t.Logf("First 20 bytes: % x", packet[:minInt(20, len(packet))])

	// Decode with our decoder (skip TOC byte)
	dec := celt.NewDecoder(1)
	decoded, err := dec.DecodeFrame(packet[1:], frameSize)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	t.Logf("\nFirst 10 decoded samples:")
	for i := 0; i < 10 && i < len(decoded); i++ {
		t.Logf("  decoded[%d] = %.4f (orig=%.4f)", i, decoded[i], pcm[i])
	}

	maxOrig := 0.0
	maxDec := 0.0
	for _, v := range pcm {
		if math.Abs(v) > maxOrig {
			maxOrig = math.Abs(v)
		}
	}
	for _, v := range decoded {
		if math.Abs(v) > maxDec {
			maxDec = math.Abs(v)
		}
	}
	t.Logf("\nMax amplitudes: orig=%.4f, decoded=%.4f, ratio=%.2f", maxOrig, maxDec, maxDec/maxOrig)
}

func extractFirstAudioPacket(filename string) []byte {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil
	}

	// Find Ogg pages
	pages := findOggPages(data)
	if len(pages) < 3 {
		return nil
	}

	// Third page (index 2) should be first audio packet
	pageStart := pages[2]
	nSegments := int(data[pageStart+26])
	segTable := data[pageStart+27 : pageStart+27+nSegments]

	audioStart := pageStart + 27 + nSegments
	audioLen := 0
	for _, s := range segTable {
		audioLen += int(s)
	}

	return data[audioStart : audioStart+audioLen]
}

func findOggPages(data []byte) []int {
	var pages []int
	for i := 0; i < len(data)-4; i++ {
		if bytes.Equal(data[i:i+4], []byte("OggS")) {
			pages = append(pages, i)
		}
	}
	return pages
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
