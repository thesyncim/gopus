package testvectors

import (
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestDecoderAlignmentVectors checks if decoder output is time-shifted vs RFC 8251 reference.
// A consistent non-zero bestShift suggests delay compensation mismatch (not an audio quality bug).
func TestDecoderAlignmentVectors(t *testing.T) {
	requireTestTier(t, testTierParity)

	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping: %v", err)
		return
	}

	const maxShift = 200 // samples (stereo interleaved)
	vectors := []string{
		"testvector02",
		"testvector03",
		"testvector04",
		"testvector05",
		"testvector06",
		"testvector12",
	}

	for _, vector := range vectors {
		t.Run(vector, func(t *testing.T) {
			bitFile := filepath.Join(testVectorDir, vector+".bit")
			decFile := filepath.Join(testVectorDir, vector+".dec")

			packets, err := ReadBitstreamFile(bitFile)
			if err != nil {
				t.Fatalf("read bitstream: %v", err)
			}
			ref, err := readPCMFile(decFile)
			if err != nil {
				t.Fatalf("read reference: %v", err)
			}

			dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))
			if err != nil {
				t.Fatalf("new decoder: %v", err)
			}

			var decoded []int16
			for _, pkt := range packets {
				pcm, err := decodeInt16(dec, pkt.Data)
				if err != nil {
					t.Fatalf("decode failed: %v", err)
				}
				decoded = append(decoded, pcm...)
			}

			decodedF32 := pcm16ToFloat32(decoded)
			refF32 := pcm16ToFloat32(ref)
			baseStats := computeWaveformStats(decodedF32, refF32)
			scale := bestScale(decodedF32, refF32)
			offset := bestOffset(decodedF32, refF32, scale)
			scaledStats := computeWaveformStats(applyScaleOffset(decodedF32, scale, 0), refF32)
			affineStats := computeWaveformStats(applyScaleOffset(decodedF32, scale, offset), refF32)
			bestShift, bestStats := bestWaveformDelayByCorrelation(decodedF32, refF32, maxShift)

			t.Logf("base: corr=%.6f rmsRatio=%.3f meanAbs=%.1f maxAbs=%.1f", baseStats.Correlation, baseStats.RMSRatio, baseStats.MeanAbsErr*32768.0, baseStats.MaxAbsErr*32768.0)
			t.Logf("scaled: scale=%.6f corr=%.6f meanAbs=%.1f maxAbs=%.1f", scale, scaledStats.Correlation, scaledStats.MeanAbsErr*32768.0, scaledStats.MaxAbsErr*32768.0)
			t.Logf("affine: scale=%.6f offset=%.6f corr=%.6f meanAbs=%.1f maxAbs=%.1f", scale, offset, affineStats.Correlation, affineStats.MeanAbsErr*32768.0, affineStats.MaxAbsErr*32768.0)
			t.Logf("bestShift=%d samples (interleaved), corr=%.6f meanAbs=%.1f maxAbs=%.1f", bestShift, bestStats.Correlation, bestStats.MeanAbsErr*32768.0, bestStats.MaxAbsErr*32768.0)
		})
	}
}

func applyScaleOffset(samples []float32, scale, offset float64) []float32 {
	out := make([]float32, len(samples))
	for i, sample := range samples {
		out[i] = float32(float64(sample)*scale + offset)
	}
	return out
}

// bestScale computes least-squares scale factor to align decoded to reference.
func bestScale(decoded, reference []float32) float64 {
	n := len(decoded)
	if len(reference) < n {
		n = len(reference)
	}
	if n == 0 {
		return 1.0
	}
	var num, den float64
	for i := 0; i < n; i++ {
		d := float64(decoded[i])
		r := float64(reference[i])
		num += r * d
		den += d * d
	}
	if den == 0 {
		return 1.0
	}
	return num / den
}

// bestOffset computes mean error after applying scale.
func bestOffset(decoded, reference []float32, scale float64) float64 {
	n := len(decoded)
	if len(reference) < n {
		n = len(reference)
	}
	if n == 0 {
		return 0
	}
	var sum float64
	for i := 0; i < n; i++ {
		sum += float64(reference[i]) - float64(decoded[i])*scale
	}
	return sum / float64(n)
}
