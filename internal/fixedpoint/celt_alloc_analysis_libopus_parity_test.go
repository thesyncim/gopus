//go:build gopus_fixedpoint

package fixedpoint

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/rangecoding"
)

// eband5ms is the libopus 5ms band layout (celt/static_modes_fixed_arm.h /
// modes.c): nbEBands=21, boundaries in MDCT bins of the short window. tf_analysis
// and alloc_trim_analysis read m->eBands directly, so we drive the kernels with
// the real table.
var eband5ms = []int16{
	0, 1, 2, 3, 4, 5, 6, 7, 8, 10, 12, 14, 16, 20, 24, 28, 34, 40, 48, 60, 78, 100,
}

func allocAnalysisNorm(rng *rand.Rand) int32 {
	// celt_norm values are Q24-ish normalised bins; exercise a wide signed range
	// including the SHR32 corner cases.
	return rng.Int31n(1<<26) - (1 << 25)
}

func TestCELTTFAnalysisParity(t *testing.T) {
	rng := rand.New(rand.NewSource(0x7f10a))
	for _, lm := range []int{0, 1, 2, 3} {
		for _, isTransient := range []bool{false, true} {
			for _, tfChan := range []int{0, 1} {
				for trial := 0; trial < 8; trial++ {
					length := 21
					eBands := eband5ms
					n0 := (int(eBands[len(eBands)-1]) << lm) + 16
					x := make([]int32, 2*n0)
					for i := range x {
						x[i] = allocAnalysisNorm(rng)
					}
					importance := make([]int, length)
					for i := range importance {
						importance[i] = rng.Intn(64) + 1
					}
					lambda := rng.Intn(40)
					tfEstimate := int16(rng.Intn(1 << 14))

					tfResGo := make([]int, length)
					selGo := TFAnalysis(eBands, length, isTransient, tfResGo, lambda, x, n0, lm, tfEstimate, tfChan, importance)

					selC, tfResC, err := libopustest.ProbeCELTTFAnalysis(eBands, length, isTransient, lambda, x, n0, lm, tfEstimate, tfChan, importance)
					if err != nil {
						t.Fatalf("oracle: %v", err)
					}
					name := fmt.Sprintf("LM%d trans=%v chan=%d trial=%d", lm, isTransient, tfChan, trial)
					if selGo != selC {
						t.Fatalf("%s: tf_select Go=%d C=%d", name, selGo, selC)
					}
					for i := range tfResGo {
						if tfResGo[i] != tfResC[i] {
							t.Fatalf("%s: tf_res[%d] Go=%d C=%d (full Go=%v C=%v)", name, i, tfResGo[i], tfResC[i], tfResGo, tfResC)
						}
					}
				}
			}
		}
	}
}

func TestCELTTFEncodeParity(t *testing.T) {
	rng := rand.New(rand.NewSource(0x33b1))
	for _, lm := range []int{0, 1, 2, 3} {
		for _, isTransient := range []bool{false, true} {
			for _, tfSelect := range []int{0, 1} {
				for _, bufSize := range []int{2, 8, 64} {
					for trial := 0; trial < 6; trial++ {
						end := 21
						tfRes := make([]int, end)
						for i := range tfRes {
							tfRes[i] = rng.Intn(2)
						}
						preBits := rng.Intn(bufSize * 6)

						goTFRes := append([]int(nil), tfRes...)
						buf := make([]byte, bufSize)
						var enc rangecoding.Encoder
						enc.Init(buf)
						for i := 0; i < preBits; i++ {
							enc.EncodeBit(0, 1)
						}
						TFEncode(0, end, isTransient, goTFRes, lm, tfSelect, &enc)
						enc.Done()
						// Compare the full coder storage (C dumps the whole
						// zero-padded buffer that ec_enc_done() finalised into).
						goBuf := buf

						cBuf, cTFRes, err := libopustest.ProbeCELTTFEncode(0, end, isTransient, tfRes, lm, tfSelect, bufSize, preBits)
						if err != nil {
							t.Fatalf("oracle: %v", err)
						}
						name := fmt.Sprintf("LM%d trans=%v sel=%d buf=%d pre=%d trial=%d", lm, isTransient, tfSelect, bufSize, preBits, trial)
						if !bytes.Equal(goBuf, cBuf) {
							t.Fatalf("%s: coded bytes differ Go=%x C=%x", name, goBuf, cBuf)
						}
						for i := range goTFRes {
							if goTFRes[i] != cTFRes[i] {
								t.Fatalf("%s: tf_res[%d] Go=%d C=%d", name, i, goTFRes[i], cTFRes[i])
							}
						}
					}
				}
			}
		}
	}
}

func TestCELTAllocTrimAnalysisParity(t *testing.T) {
	rng := rand.New(rand.NewSource(0xa11c))
	for _, lm := range []int{0, 1, 2, 3} {
		for _, c := range []int{1, 2} {
			for _, valid := range []bool{false, true} {
				for trial := 0; trial < 12; trial++ {
					end := 21
					eBands := eband5ms
					nbEBands := len(eBands) - 1
					n0 := (int(eBands[len(eBands)-1]) << lm) + 16
					x := make([]int32, 2*n0)
					for i := range x {
						x[i] = allocAnalysisNorm(rng)
					}
					bandLogE := make([]int32, 2*nbEBands)
					for i := range bandLogE {
						// celt_glog Q24: a few octaves of dynamic range, signed.
						bandLogE[i] = int32(rng.Intn(1<<27)) - (1 << 26)
					}
					stereoSaving := int16(rng.Intn(2048) - 1024)
					tfEstimate := int16(rng.Intn(1 << 14))
					intensity := rng.Intn(nbEBands-8) + 8
					surroundTrim := int32(rng.Intn(1<<26) - (1 << 25))
					equivRates := []int32{20000, 50000, 64000, 72000, 80000, 128000}
					equivRate := equivRates[rng.Intn(len(equivRates))]
					slope := rng.Float32()*0.4 - 0.2

					goRes := AllocTrimAnalysis(eBands, x, bandLogE, end, lm, c, n0, nbEBands, stereoSaving, tfEstimate, intensity, surroundTrim, equivRate, valid, slope)

					cRes, err := libopustest.ProbeCELTAllocTrimAnalysis(eBands, x, bandLogE, end, lm, c, n0, stereoSaving, tfEstimate, intensity, surroundTrim, equivRate, valid, slope)
					if err != nil {
						t.Fatalf("oracle: %v", err)
					}
					name := fmt.Sprintf("LM%d C=%d valid=%v rate=%d trial=%d", lm, c, valid, equivRate, trial)
					if goRes.TrimIndex != cRes.TrimIndex {
						t.Fatalf("%s: trim_index Go=%d C=%d", name, goRes.TrimIndex, cRes.TrimIndex)
					}
					if goRes.StereoSaving != cRes.StereoSaving {
						t.Fatalf("%s: stereo_saving Go=%d C=%d", name, goRes.StereoSaving, cRes.StereoSaving)
					}
				}
			}
		}
	}
}
