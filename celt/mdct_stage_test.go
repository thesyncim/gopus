package celt

import (
	"fmt"
	"math"
	"testing"
)

func TestMDCTFMALikeMixUsesFusedSourceShape(t *testing.T) {
	if !mdctUseFMALikeMixEnabled {
		t.Skip("FMA-like MDCT mix is arch-specific")
	}

	addA := float32(12575.8603515625)
	addB := float32(15380.7783203125)
	addC := float32(0.27607929706573486)
	addD := float32(0.8652157187461853)
	addWant := float32(math.FMA(float64(addA), float64(addC), float64(mdctMulWith(false, addB, addD))))
	addSplit := mdctMulWith(false, addA, addC) + mdctMulWith(false, addB, addD)
	if math.Float32bits(addWant) == math.Float32bits(addSplit) {
		t.Fatalf("add fixture does not expose fused MDCT rounding")
	}
	if got := mdctMulAddMixWith(false, addA, addB, addC, addD); math.Float32bits(got) != math.Float32bits(addWant) {
		t.Fatalf("mdctMulAddMixWith=%08x %.9g want fused %08x %.9g",
			math.Float32bits(got), got, math.Float32bits(addWant), addWant)
	}

	subA := float32(15156.271484375)
	subB := float32(7875.662109375)
	subC := float32(0.19688130915164948)
	subD := float32(0.9199866056442261)
	subWant := float32(math.FMA(float64(subA), float64(subC), -float64(mdctMulWith(false, subB, subD))))
	subSplit := mdctMulWith(false, subA, subC) - mdctMulWith(false, subB, subD)
	if math.Float32bits(subWant) == math.Float32bits(subSplit) {
		t.Fatalf("sub fixture does not expose fused MDCT rounding")
	}
	if got := mdctMulSubMixWith(false, subA, subB, subC, subD); math.Float32bits(got) != math.Float32bits(subWant) {
		t.Fatalf("mdctMulSubMixWith=%08x %.9g want fused %08x %.9g",
			math.Float32bits(got), got, math.Float32bits(subWant), subWant)
	}
}

func mdctForwardOverlapLegacyStagedReference(samples []float32, overlap int) []float32 {
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
	coeffs := make([]float32, n2)

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
		f[2*i] = mdctMulAddMix(samples[xp1+n2], samples[xp2], window[wp2], window[wp1])
		f[2*i+1] = mdctMulSubMix(samples[xp1], samples[xp2-n2], window[wp1], window[wp2])
		xp1 += 2
		xp2 -= 2
		wp1 += 2
		wp2 -= 2
	}

	wp1 = 0
	wp2 = overlap - 1
	for ; i < n4-limit1; i++ {
		f[2*i] = samples[xp2]
		f[2*i+1] = samples[xp1]
		xp1 += 2
		xp2 -= 2
	}

	for ; i < n4; i++ {
		f[2*i] = mdctMulSubMixAlt(samples[xp2], samples[xp1-n2], window[wp2], window[wp1])
		f[2*i+1] = mdctMulAddMix(samples[xp1], samples[xp2+n2], window[wp2], window[wp1])
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
				coeffs[2*i] = yr
				coeffs[n2-1-2*i] = yi
			}
		} else {
			for i = 0; i < n4; i++ {
				re := fftStage[i].r
				im := fftStage[i].i
				t0 := trig[i]
				t1 := trig[n4+i]
				yr := mdctMul(im, t1) - mdctMul(re, t0)
				yi := mdctMul(re, t1) + mdctMul(im, t0)
				coeffs[2*i] = yr
				coeffs[n2-1-2*i] = yi
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
				coeffs[2*i] = yr
				coeffs[n2-1-2*i] = yi
			}
		} else {
			for i = 0; i < n4; i++ {
				re := real(fftOut[i])
				im := imag(fftOut[i])
				t0 := trig[i]
				t1 := trig[n4+i]
				yr := mdctMul(im, t1) - mdctMul(re, t0)
				yi := mdctMul(re, t1) + mdctMul(im, t0)
				coeffs[2*i] = yr
				coeffs[n2-1-2*i] = yi
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
				input := make([]float32, tc.frameSize+tc.overlap)
				for i := range input {
					sine := math.Sin(float64(i+seed*7) * 0.073)
					cosine := math.Cos(float64((i+1)*(seed+3)) * 0.021)
					step := float64((i*17+seed*31)%29-14) / 32.0
					input[i] = float32(0.55*sine + 0.35*cosine + step)
				}

				got := mdctForwardOverlapF32(input, tc.overlap)
				want := mdctForwardOverlapLegacyStagedReference(input, tc.overlap)
				if len(got) != len(want) {
					t.Fatalf("length mismatch: got %d want %d", len(got), len(want))
				}

				tol := 2e-7
				for i := range want {
					if got[i] == want[i] || math.Abs(float64(got[i]-want[i])) <= tol {
						continue
					}
					t.Fatalf("coefficient %d mismatch: got %.9g want %.9g (tol=%g)", i, got[i], want[i], tol)
				}
			})
		}
	}
}
