package celt

import (
	"fmt"
	"math"
	"testing"
)

func imdctOverlapWithPrevScratchF32LegacyBufferCopy(out []float64, spectrum []float64, prevOverlap []float64, overlap int, scratch *imdctScratchF32) {
	n2 := len(spectrum)
	if n2 == 0 {
		return
	}
	if overlap < 0 {
		overlap = 0
	}

	n := n2 * 2
	n4 := n2 / 2
	needed := n2 + overlap
	start := overlap / 2
	if len(out) < needed {
		return
	}

	trig := getMDCTTrigF32(n)

	var fftIn []complex64
	var fftTmp []kissCpx
	var buf []float32
	var outF32 []float32
	if scratch == nil {
		fftIn = make([]complex64, n4)
		fftTmp = make([]kissCpx, n4)
		buf = make([]float32, n2)
		outF32 = make([]float32, needed)
	} else {
		fftIn = ensureComplex64Slice(&scratch.fftIn, n4)
		fftTmp = ensureKissCpxSlice(&scratch.fftTmp, n4)
		buf = ensureFloat32Slice(&scratch.buf, n2)
		outF32 = ensureFloat32Slice(&scratch.out, needed)
	}

	if start+n2 < needed {
		clear(outF32[start+n2:needed])
	}

	if overlap > 0 && len(prevOverlap) > 0 {
		copyLen := min(len(prevOverlap), overlap)
		for i := 0; i < copyLen; i++ {
			outF32[i] = float32(prevOverlap[i])
		}
		if copyLen < overlap {
			clear(outF32[copyLen:overlap])
		}
	} else if overlap > 0 {
		clear(outF32[:overlap])
	}

	imdctPreRotateF32(fftIn, spectrum, trig, n2, n4)
	kissFFT32ToInterleaved(buf, fftIn, fftTmp)
	imdctPostRotateF32(buf, trig, n2, n4)
	copy(outF32[start:start+n2], buf)

	if overlap > 0 {
		windowF32 := GetWindowBufferF32(overlap)
		xp1 := overlap - 1
		yp1 := 0
		wp1 := 0
		wp2 := overlap - 1
		limit := overlap / 2
		i := 0
		for ; i+1 < limit; i += 2 {
			x1 := outF32[xp1]
			x2 := outF32[yp1]
			outF32[yp1] = x2*windowF32[wp2] - x1*windowF32[wp1]
			outF32[xp1] = x2*windowF32[wp1] + x1*windowF32[wp2]
			yp1++
			xp1--
			wp1++
			wp2--

			x1 = outF32[xp1]
			x2 = outF32[yp1]
			outF32[yp1] = x2*windowF32[wp2] - x1*windowF32[wp1]
			outF32[xp1] = x2*windowF32[wp1] + x1*windowF32[wp2]
			yp1++
			xp1--
			wp1++
			wp2--
		}
		for ; i < limit; i++ {
			x1 := outF32[xp1]
			x2 := outF32[yp1]
			outF32[yp1] = x2*windowF32[wp2] - x1*windowF32[wp1]
			outF32[xp1] = x2*windowF32[wp1] + x1*windowF32[wp2]
			yp1++
			xp1--
			wp1++
			wp2--
		}
	}

	if needed > 0 {
		out = out[:needed:needed]
		outF32 = outF32[:needed:needed]
	}
	for i := 0; i < needed; i++ {
		out[i] = float64(outF32[i])
	}
}

func TestIMDCTOverlapWithPrevScratchF32MatchesLegacyBufferCopy(t *testing.T) {
	testCases := []struct {
		frameSize int
		overlap   int
	}{
		{frameSize: 120, overlap: 120},
		{frameSize: 240, overlap: 120},
		{frameSize: 480, overlap: 120},
		{frameSize: 960, overlap: 120},
	}

	for _, tc := range testCases {
		for seed := 1; seed <= 4; seed++ {
			t.Run(fmt.Sprintf("frame=%d/seed=%d", tc.frameSize, seed), func(t *testing.T) {
				spectrum := make([]float64, tc.frameSize)
				prevOverlap := make([]float64, tc.overlap)
				for i := range spectrum {
					sine := math.Sin(float64(i+seed*11) * 0.063)
					cosine := math.Cos(float64((i+1)*(seed+5)) * 0.017)
					step := float64((i*13+seed*29)%23-11) / 28.0
					spectrum[i] = 0.6*sine + 0.25*cosine + step
				}
				for i := range prevOverlap {
					sine := math.Sin(float64(i+seed*3) * 0.041)
					step := float64((i*7+seed*19)%17-8) / 20.0
					prevOverlap[i] = 0.7*sine + step
				}

				got := make([]float64, tc.frameSize+tc.overlap)
				want := make([]float64, tc.frameSize+tc.overlap)
				imdctOverlapWithPrevScratchF32(got, spectrum, prevOverlap, tc.overlap, &imdctScratchF32{})
				imdctOverlapWithPrevScratchF32LegacyBufferCopy(want, spectrum, prevOverlap, tc.overlap, &imdctScratchF32{})

				for i := range want {
					if math.Float64bits(got[i]) != math.Float64bits(want[i]) {
						t.Fatalf("sample %d mismatch: got %.9g want %.9g", i, got[i], want[i])
					}
				}
			})
		}
	}
}
