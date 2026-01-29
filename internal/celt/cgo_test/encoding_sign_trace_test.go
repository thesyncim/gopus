package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

func TestEncodingSignTrace(t *testing.T) {
	frameSize := 960

	// Generate DC offset signal to clearly track sign
	pcm := make([]float64, frameSize)
	for i := range pcm {
		pcm[i] = 0.3 // Positive DC
	}

	t.Log("=== DC Signal Sign Trace ===")
	t.Logf("Input: constant +0.3")

	// Step 1: Pre-emphasis
	enc := celt.NewEncoder(1)
	enc.SetBitrate(64000)
	preemph := enc.ApplyPreemphasisWithScaling(pcm)

	t.Logf("\nPre-emphasis output [0:5]: %.4f, %.4f, %.4f, %.4f, %.4f",
		preemph[0], preemph[1], preemph[2], preemph[3], preemph[4])
	t.Logf("Pre-emphasis sign at [0]: %s", signStrF64(preemph[0]))

	// Step 2: MDCT
	mdct := celt.ComputeMDCTWithHistory(preemph, make([]float64, 120), 1)
	t.Logf("\nMDCT output [0:5]: %.4f, %.4f, %.4f, %.4f, %.4f",
		mdct[0], mdct[1], mdct[2], mdct[3], mdct[4])
	t.Logf("MDCT DC coefficient sign: %s (value: %.4f)", signStrF64(mdct[0]), mdct[0])

	// Step 3: Full encode
	packet, err := enc.EncodeFrame(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	t.Logf("\nEncoded packet: %d bytes", len(packet))

	// Decode with libopus
	libDec, _ := NewLibopusDecoder(48000, 1)
	defer libDec.Destroy()

	toc := byte(0xF8)
	packetWithTOC := append([]byte{toc}, packet...)
	decoded, _ := libDec.DecodeFloat(packetWithTOC, frameSize)

	t.Logf("\nDecoded output [0:5]: %.4f, %.4f, %.4f, %.4f, %.4f",
		decoded[0], decoded[1], decoded[2], decoded[3], decoded[4])
	t.Logf("Decoded middle [400:405]: %.4f, %.4f, %.4f, %.4f, %.4f",
		decoded[400], decoded[401], decoded[402], decoded[403], decoded[404])

	// Check sign of decoded DC component
	avgDecoded := 0.0
	for _, v := range decoded[:frameSize] {
		avgDecoded += float64(v)
	}
	avgDecoded /= float64(frameSize)
	t.Logf("\nDecoded average (should be ~+0.3): %.4f", avgDecoded)
	t.Logf("Sign of decoded average: %s", signStrF64(avgDecoded))

	// Now test with SINE wave
	t.Log("\n\n=== SINE Wave Sign Trace ===")

	pcmSine := make([]float64, frameSize)
	for i := range pcmSine {
		ti := float64(i) / 48000.0
		pcmSine[i] = 0.5 * math.Sin(2*math.Pi*440*ti)
	}

	// Fresh encoder
	enc2 := celt.NewEncoder(1)
	enc2.SetBitrate(64000)

	// Pre-emphasis
	preemphSine := enc2.ApplyPreemphasisWithScaling(pcmSine)
	t.Logf("Sine pre-emph at peak (~11): input=%.4f, preemph=%.4f",
		pcmSine[11], preemphSine[11])

	// MDCT
	mdctSine := celt.ComputeMDCTWithHistory(preemphSine, make([]float64, 120), 1)
	t.Logf("Sine MDCT [17] (440Hz bin): %.4f, sign: %s", mdctSine[17], signStrF64(mdctSine[17]))

	// Full encode
	packetSine, _ := enc2.EncodeFrame(pcmSine, frameSize)

	// Decode
	libDec2, _ := NewLibopusDecoder(48000, 1)
	defer libDec2.Destroy()

	packetSineWithTOC := append([]byte{toc}, packetSine...)
	decodedSine, _ := libDec2.DecodeFloat(packetSineWithTOC, frameSize)

	// Find peak in original and decoded
	origMaxIdx := 0
	origMaxVal := 0.0
	for i := 10; i < 50; i++ {
		if pcmSine[i] > origMaxVal {
			origMaxVal = pcmSine[i]
			origMaxIdx = i
		}
	}

	decMaxIdx := 0
	decMaxVal := float32(0)
	decMinIdx := 0
	decMinVal := float32(0)
	for i := 0; i < 100; i++ {
		if decodedSine[i] > decMaxVal {
			decMaxVal = decodedSine[i]
			decMaxIdx = i
		}
		if decodedSine[i] < decMinVal {
			decMinVal = decodedSine[i]
			decMinIdx = i
		}
	}

	t.Logf("\nOriginal peak: idx=%d, val=%.4f (sign: %s)", origMaxIdx, origMaxVal, signStrF64(origMaxVal))
	t.Logf("Decoded peak: idx=%d, val=%.4f (sign: %s)", decMaxIdx, float64(decMaxVal), signStrF32(decMaxVal))
	t.Logf("Decoded min: idx=%d, val=%.4f (sign: %s)", decMinIdx, float64(decMinVal), signStrF32(decMinVal))
}
