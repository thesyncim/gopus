//go:build cgo_libopus

package silk

import (
	"math"
	"testing"
)

func TestDownsamplingResamplerMatchesLibopus(t *testing.T) {
	const (
		fsIn   = 48000
		fsOut  = 16000
		frames = 3
	)

	frameLen := fsIn / 50 // 20ms
	inLen := frameLen * frames
	if inLen < fsIn/1000 {
		t.Fatalf("input too short: %d", inLen)
	}

	in := make([]float32, inLen)
	for i := range in {
		tm := float64(i) / float64(fsIn)
		in[i] = float32(
			0.6*math.Sin(2*math.Pi*220*tm) +
				0.3*math.Sin(2*math.Pi*440*tm) +
				0.1*math.Sin(2*math.Pi*880*tm),
		)
	}

	// Go resampler (encoder path)
	resampler := NewDownsamplingResampler(fsIn, fsOut)
	outGo := make([]float32, inLen*fsOut/fsIn)
	nGo := resampler.ProcessInto(in, outGo)
	outGo = outGo[:nGo]

	// Libopus resampler
	outLibInt16, err := libopusResampleOnce(in, fsIn, fsOut, true)
	if err != nil {
		t.Fatalf("libopus resampler failed: %v", err)
	}
	outLib := make([]float32, len(outLibInt16))
	for i, v := range outLibInt16 {
		outLib[i] = float32(v) / 32768.0
	}

	if len(outGo) != len(outLib) {
		t.Fatalf("output length mismatch: go=%d lib=%d", len(outGo), len(outLib))
	}

	var maxDiff float64
	var sumSq float64
	for i := range outGo {
		diff := float64(outGo[i] - outLib[i])
		if diff < 0 {
			diff = -diff
		}
		if diff > maxDiff {
			maxDiff = diff
		}
		sumSq += diff * diff
	}
	rms := math.Sqrt(sumSq / float64(len(outGo)))
	t.Logf("resampler diff: max=%.8f rms=%.8f", maxDiff, rms)

	// Allow 1 LSB of float32 output error.
	const tol = 1.0 / 32768.0
	if maxDiff > tol {
		t.Fatalf("resampler mismatch: max diff %.8f > %.8f", maxDiff, tol)
	}
}

func TestDecoderResamplerMatchesLibopus(t *testing.T) {
	testCases := []struct {
		fsIn   int
		fsOut  int
		frames int
	}{
		{8000, 48000, 3},
		{12000, 48000, 3},
		{16000, 48000, 3},
	}

	for _, tc := range testCases {
		t.Run("upsample", func(t *testing.T) {
			frameLen := tc.fsIn / 50 // 20ms
			inLen := frameLen * tc.frames
			if inLen < tc.fsIn/1000 {
				t.Fatalf("input too short: %d", inLen)
			}

			in := make([]float32, inLen)
			for i := range in {
				tm := float64(i) / float64(tc.fsIn)
				in[i] = float32(
					0.6*math.Sin(2*math.Pi*220*tm) +
						0.3*math.Sin(2*math.Pi*440*tm) +
						0.1*math.Sin(2*math.Pi*880*tm),
				)
			}

			// Go resampler (decoder path)
			resampler := NewLibopusResampler(tc.fsIn, tc.fsOut)
			outGo := resampler.Process(in)

			// Libopus resampler
			outLibInt16, err := libopusResampleOnce(in, tc.fsIn, tc.fsOut, false)
			if err != nil {
				t.Fatalf("libopus resampler failed: %v", err)
			}
			outLib := make([]float32, len(outLibInt16))
			for i, v := range outLibInt16 {
				outLib[i] = float32(v) / 32768.0
			}

			if len(outGo) != len(outLib) {
				t.Fatalf("output length mismatch: go=%d lib=%d", len(outGo), len(outLib))
			}

			var maxDiff float64
			var sumSq float64
			for i := range outGo {
				diff := float64(outGo[i] - outLib[i])
				if diff < 0 {
					diff = -diff
				}
				if diff > maxDiff {
					maxDiff = diff
				}
				sumSq += diff * diff
			}
			rms := math.Sqrt(sumSq / float64(len(outGo)))
			t.Logf("resampler diff (fsIn=%d): max=%.8f rms=%.8f", tc.fsIn, maxDiff, rms)

			// Allow 1 LSB of float32 output error.
			const tol = 1.0 / 32768.0
			if maxDiff > tol {
				t.Fatalf("resampler mismatch: max diff %.8f > %.8f", maxDiff, tol)
			}
		})
	}
}
