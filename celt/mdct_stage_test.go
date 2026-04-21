package celt

import (
	"fmt"
	"math"
	"testing"
)

func mdctForwardOverlapLegacyStagedReference(samples []float64, overlap int) []float64 {
	if len(samples) == 0 {
		return nil
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap > len(samples) {
		overlap = len(samples)
	}

	frameSize := len(samples) - overlap
	if frameSize <= 0 {
		return nil
	}

	n2 := frameSize
	n := n2 * 2
	n4 := n2 / 2
	if n4 <= 0 {
		return nil
	}

	trig := getMDCTTrigF32(n)
	var window []float32
	if overlap > 0 {
		window = GetWindowBufferF32(overlap)
	}

	f := make([]float32, n2)
	fftTmp := make([]kissCpx, n4)
	coeffs := make([]float64, n2)

	st := getKissFFTState(n4)
	useDirectKissCpx := st != nil && len(st.bitrev) >= n4
	var fftStage []kissCpx
	var fftIn []complex64
	var fftOut []complex64
	if useDirectKissCpx {
		fftStage = fftTmp[:n4]
	} else {
		fftIn = make([]complex64, n4)
		fftOut = make([]complex64, n4)
	}

	xp1 := overlap / 2
	xp2 := n2 - 1 + overlap/2
	wp1 := overlap / 2
	wp2 := overlap/2 - 1
	i := 0
	limit1 := (overlap + 3) >> 2

	for ; i < limit1; i++ {
		f[2*i] = mdctMulAddMix(float32(samples[xp1+n2]), float32(samples[xp2]), window[wp2], window[wp1])
		f[2*i+1] = mdctMulSubMix(float32(samples[xp1]), float32(samples[xp2-n2]), window[wp1], window[wp2])
		xp1 += 2
		xp2 -= 2
		wp1 += 2
		wp2 -= 2
	}

	wp1 = 0
	wp2 = overlap - 1
	for ; i < n4-limit1; i++ {
		f[2*i] = float32(samples[xp2])
		f[2*i+1] = float32(samples[xp1])
		xp1 += 2
		xp2 -= 2
	}

	for ; i < n4; i++ {
		f[2*i] = mdctMulSubMixAlt(float32(samples[xp2]), float32(samples[xp1-n2]), window[wp2], window[wp1])
		f[2*i+1] = mdctMulAddMix(float32(samples[xp1]), float32(samples[xp2+n2]), window[wp2], window[wp1])
		xp1 += 2
		xp2 -= 2
		wp1 += 2
		wp2 -= 2
	}

	scale := float32(1.0) / float32(n4)
	if useDirectKissCpx {
		bitrev := st.bitrev
		if mdctUseFMALikeMixEnabled {
			for i = 0; i < n4; i++ {
				re := f[2*i]
				im := f[2*i+1]
				t0 := trig[i]
				t1 := trig[n4+i]
				mdctStoreDirectStageFMALike(fftStage, bitrev[i], scale, re, im, t0, t1)
			}
		} else {
			for i = 0; i < n4; i++ {
				re := f[2*i]
				im := f[2*i+1]
				t0 := trig[i]
				t1 := trig[n4+i]
				mdctStoreDirectStage(fftStage, bitrev[i], scale, re, im, t0, t1)
			}
		}
		st.fftImpl(fftStage)
	} else {
		if mdctUseFMALikeMixEnabled {
			for i = 0; i < n4; i++ {
				re := f[2*i]
				im := f[2*i+1]
				t0 := trig[i]
				t1 := trig[n4+i]
				yr := float32(float64(re)*float64(t0) - float64(mdctMul(im, t1)))
				yi := float32(float64(im)*float64(t0) + float64(mdctMul(re, t1)))
				fftIn[i] = complex(yr*scale, yi*scale)
			}
		} else {
			for i = 0; i < n4; i++ {
				re := f[2*i]
				im := f[2*i+1]
				t0 := trig[i]
				t1 := trig[n4+i]
				yr := mdctMul(re, t0) - mdctMul(im, t1)
				yi := mdctMul(im, t0) + mdctMul(re, t1)
				fftIn[i] = complex(yr*scale, yi*scale)
			}
		}
		kissFFT32To(fftOut, fftIn[:n4], fftTmp)
	}

	if useDirectKissCpx {
		if mdctUseFMALikeMixEnabled {
			for i = 0; i < n4; i++ {
				re := fftStage[i].r
				im := fftStage[i].i
				t0 := trig[i]
				t1 := trig[n4+i]
				yr := float32(float64(im)*float64(t1) - float64(mdctMul(re, t0)))
				yi := float32(float64(re)*float64(t1) + float64(mdctMul(im, t0)))
				coeffs[2*i] = float64(yr)
				coeffs[n2-1-2*i] = float64(yi)
			}
		} else {
			for i = 0; i < n4; i++ {
				re := fftStage[i].r
				im := fftStage[i].i
				t0 := trig[i]
				t1 := trig[n4+i]
				yr := mdctMul(im, t1) - mdctMul(re, t0)
				yi := mdctMul(re, t1) + mdctMul(im, t0)
				coeffs[2*i] = float64(yr)
				coeffs[n2-1-2*i] = float64(yi)
			}
		}
	} else {
		if mdctUseFMALikeMixEnabled {
			for i = 0; i < n4; i++ {
				re := real(fftOut[i])
				im := imag(fftOut[i])
				t0 := trig[i]
				t1 := trig[n4+i]
				yr := float32(float64(im)*float64(t1) - float64(mdctMul(re, t0)))
				yi := float32(float64(re)*float64(t1) + float64(mdctMul(im, t0)))
				coeffs[2*i] = float64(yr)
				coeffs[n2-1-2*i] = float64(yi)
			}
		} else {
			for i = 0; i < n4; i++ {
				re := real(fftOut[i])
				im := imag(fftOut[i])
				t0 := trig[i]
				t1 := trig[n4+i]
				yr := mdctMul(im, t1) - mdctMul(re, t0)
				yi := mdctMul(re, t1) + mdctMul(im, t0)
				coeffs[2*i] = float64(yr)
				coeffs[n2-1-2*i] = float64(yi)
			}
		}
	}

	return coeffs[:n2]
}

func TestMDCTForwardOverlapDirectStageMatchesLegacyStagedPath(t *testing.T) {
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
			name := fmt.Sprintf("frame=%d/seed=%d", tc.frameSize, seed)
			t.Run(name, func(t *testing.T) {
				input := make([]float64, tc.frameSize+tc.overlap)
				for i := range input {
					sine := math.Sin(float64(i+seed*7) * 0.073)
					cosine := math.Cos(float64((i+1)*(seed+3)) * 0.021)
					step := float64((i*17+seed*31)%29-14) / 32.0
					input[i] = 0.55*sine + 0.35*cosine + step
				}

				got := mdctForwardOverlapF32(input, tc.overlap)
				want := mdctForwardOverlapLegacyStagedReference(input, tc.overlap)
				if len(got) != len(want) {
					t.Fatalf("length mismatch: got %d want %d", len(got), len(want))
				}

				tol := 0.0
				if tc.frameSize == 240 && (mdctUseNativeMulShort240Enabled || mdctUseFMALikeMixShort240Enabled) {
					tol = 1e-7
				}
				for i := range want {
					if tol == 0 {
						if math.Float64bits(got[i]) == math.Float64bits(want[i]) {
							continue
						}
					} else if math.Abs(got[i]-want[i]) <= tol {
						continue
					}
					if tol == 0 {
						t.Fatalf("coefficient %d mismatch: got %.9g want %.9g", i, got[i], want[i])
					}
					t.Fatalf("coefficient %d mismatch: got %.9g want %.9g (tol=%g)", i, got[i], want[i], tol)
				}
			})
		}
	}
}
