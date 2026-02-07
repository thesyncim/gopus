//go:build cgo_libopus

package silk

import (
	"math"
	"testing"
)

func TestDownsamplingResamplerMatchesLibopus(t *testing.T) {
	const (
		fsIn   = 48000
		frames = 3
	)
	for _, fsOut := range []int{8000, 12000, 16000} {
		t.Run("48k_to_"+itoa(fsOut), func(t *testing.T) {
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
			t.Logf("resampler diff (fsOut=%d): max=%.8f rms=%.8f", fsOut, maxDiff, rms)

			// Allow 1 LSB of float32 output error.
			const tol = 1.0 / 32768.0
			if maxDiff > tol {
				t.Fatalf("resampler mismatch: max diff %.8f > %.8f", maxDiff, tol)
			}
		})
	}
}

func itoa(v int) string {
	switch v {
	case 8000:
		return "8k"
	case 12000:
		return "12k"
	case 16000:
		return "16k"
	default:
		return "x"
	}
}

func TestDownsamplingResamplerChunkedMatchesOneShot(t *testing.T) {
	const (
		fsIn   = 48000
		fsOut  = 16000
		frames = 4
	)

	frameLen := fsIn / 50 // 20ms
	inLen := frameLen * frames
	in := make([]float32, inLen)
	for i := range in {
		tm := float64(i) / float64(fsIn)
		in[i] = float32(
			0.5*math.Sin(2*math.Pi*173*tm) +
				0.4*math.Sin(2*math.Pi*911*tm) +
				0.1*math.Sin(2*math.Pi*3011*tm),
		)
	}

	oneShot := NewDownsamplingResampler(fsIn, fsOut)
	outAll := make([]float32, inLen*fsOut/fsIn)
	nAll := oneShot.ProcessInto(in, outAll)
	outAll = outAll[:nAll]

	chunked := NewDownsamplingResampler(fsIn, fsOut)
	outChunk := make([]float32, 0, len(outAll))
	for f := 0; f < frames; f++ {
		chunk := in[f*frameLen : (f+1)*frameLen]
		tmp := make([]float32, frameLen*fsOut/fsIn)
		n := chunked.ProcessInto(chunk, tmp)
		outChunk = append(outChunk, tmp[:n]...)
	}

	if len(outChunk) != len(outAll) {
		t.Fatalf("output length mismatch: chunked=%d oneshot=%d", len(outChunk), len(outAll))
	}

	maxDiff := float32(0)
	maxIdx := -1
	for i := range outAll {
		diff := outChunk[i] - outAll[i]
		if diff < 0 {
			diff = -diff
		}
		if diff > maxDiff {
			maxDiff = diff
			maxIdx = i
		}
	}
	if maxDiff > 0 {
		t.Fatalf("chunked vs oneshot mismatch maxDiff=%g at %d: chunked=%g oneshot=%g", maxDiff, maxIdx, outChunk[maxIdx], outAll[maxIdx])
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
