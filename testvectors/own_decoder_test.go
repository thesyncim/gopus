package testvectors

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
)

func TestOwnEncoderDecoder(t *testing.T) {
	// Generate a short multi-frame sine wave so CELT can warm up overlap/history.
	sampleRate := 48000
	frameSize := 960
	numFrames := 12
	freq := 440.0

	pcm := make([]float64, frameSize*numFrames)
	for i := range pcm {
		ti := float64(i) / float64(sampleRate)
		pcm[i] = 0.5 * math.Sin(2*math.Pi*freq*ti)
	}

	// Create CELT encoder
	enc := celt.NewEncoder(1)
	enc.SetBitrate(64000)

	// Create CELT decoder
	dec := celt.NewDecoder(1)

	var decoded []float32
	for f := 0; f < numFrames; f++ {
		start := f * frameSize
		end := start + frameSize

		packet, err := enc.EncodeFrame(float32Slice(pcm[start:end]), frameSize)
		if err != nil {
			t.Fatalf("Encode error at frame %d: %v", f, err)
		}
		if f == 0 {
			t.Logf("Encoded packet: %d bytes", len(packet))
			if len(packet) >= 10 {
				t.Logf("First 10 bytes: %x", packet[:10])
			}
		}

		frameOut, err := dec.DecodeFrame(packet, frameSize)
		if err != nil {
			t.Fatalf("Decode error at frame %d: %v", f, err)
		}
		decoded = append(decoded, frameOut...)
	}
	t.Logf("Decoded samples: %d", len(decoded))

	// Compare first 20 samples
	t.Log("\nFirst 20 samples:")
	t.Log("  i      original     decoded")
	for i := 0; i < 20 && i < len(pcm) && i < len(decoded); i++ {
		t.Logf("%3d  %10.5f  %10.5f", i, pcm[i], decoded[i])
	}

	// Compute metrics
	maxOrig, maxDec := 0.0, 0.0
	for _, v := range pcm {
		if math.Abs(v) > maxOrig {
			maxOrig = math.Abs(v)
		}
	}
	for _, v := range decoded {
		abs := math.Abs(float64(v))
		if abs > maxDec {
			maxDec = abs
		}
	}
	t.Logf("\nMax amplitudes: orig=%.4f, decoded=%.4f", maxOrig, maxDec)

	origF32 := make([]float32, len(pcm))
	decF32 := make([]float32, len(decoded))
	for i := range pcm {
		origF32[i] = float32(pcm[i])
	}
	for i := range decoded {
		decF32[i] = float32(decoded[i])
	}

	compareLen := len(origF32)
	if len(decF32) < compareLen {
		compareLen = len(decF32)
	}
	if compareLen == 0 {
		t.Fatal("no samples to compare")
	}

	cmp, err := CompareDecodedFloat32(decF32[:compareLen], origF32[:compareLen], sampleRate, 1, 2000)
	if err != nil {
		t.Fatalf("compute opus_compare quality: %v", err)
	}

	// Outcome preserved: the original gate was opus_compare Q >= 0 only (no
	// secondary corr/RMS checks), i.e. the RFC-8251 conformance floor.
	bar := QualityBar{MinQ: 0.0, Desc: "RFC 8251 conformance floor (Q>=0)"}
	AssertQuality(t, cmp, bar, "own celt encode/decode quality")
}
