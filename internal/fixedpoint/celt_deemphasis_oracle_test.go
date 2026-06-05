//go:build gopus_fixed_point

package fixedpoint

import (
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestDeemphasisOracle checks Deemphasis against the real libopus FIXED_POINT
// celt/celt_decoder.c deemphasis bit-for-bit: the opus_res output PCM buffer,
// the updated per-channel filter state mem, and the derived RES2INT16 int16
// PCM. It covers mono and stereo, the stereo fast path, accumulation, and
// downsample factors 1/2, with input scales that reach SIG_SAT saturation.
func TestDeemphasisOracle(t *testing.T) {
	libopustest.RequireOracle(t)

	// Standard 48 kHz preemph coef0 = QCONST16(0.9230041504, 15) plus a couple
	// of other quantized magnitudes (incl. a near-unity value).
	coefs := []int16{30243, 32767, 16384, -30243, 0}

	type sigGen func(rng *rand.Rand, n int) []int32
	sigGens := []struct {
		name string
		fn   sigGen
	}{
		{"zeros", func(_ *rand.Rand, n int) []int32 { return make([]int32, n) }},
		{"small", func(rng *rand.Rand, n int) []int32 {
			x := make([]int32, n)
			for i := range x {
				x[i] = int32(rng.Intn(2001) - 1000)
			}
			return x
		}},
		{"midscale", func(rng *rand.Rand, n int) []int32 {
			x := make([]int32, n)
			for i := range x {
				x[i] = int32(rng.Intn(1<<21) - (1 << 20))
			}
			return x
		}},
		{"saturating", func(rng *rand.Rand, n int) []int32 {
			// Values around and beyond SIG_SAT so SATURATE and the int32 IIR
			// accumulation wrap/clamp identically.
			x := make([]int32, n)
			for i := range x {
				base := int32(rng.Intn(2*sigSat) - sigSat)
				if rng.Intn(4) == 0 {
					base = sigSat + int32(rng.Intn(1<<20))
				} else if rng.Intn(4) == 0 {
					base = -sigSat - int32(rng.Intn(1<<20))
				}
				x[i] = base
			}
			return x
		}},
	}

	memGens := []struct {
		name string
		fn   func(rng *rand.Rand, c int) []int32
	}{
		{"zeromem", func(_ *rand.Rand, c int) []int32 { return make([]int32, c) }},
		{"randmem", func(rng *rand.Rand, c int) []int32 {
			m := make([]int32, c)
			for i := range m {
				m[i] = int32(rng.Intn(2*sigSat) - sigSat)
			}
			return m
		}},
	}

	Ns := []int{1, 8, 120, 480, 960}
	channelsList := []int{1, 2}
	downsamples := []int{1, 2}
	accums := []bool{false, true}

	seed := int64(1)
	for _, C := range channelsList {
		for _, N := range Ns {
			for _, downsample := range downsamples {
				if N%downsample != 0 {
					continue
				}
				Nd := N / downsample
				for _, accum := range accums {
					for _, coef := range coefs {
						for _, sg := range sigGens {
							for _, mg := range memGens {
								seed++
								rng := rand.New(rand.NewSource(seed))

								in := make([][]int32, C)
								for c := 0; c < C; c++ {
									in[c] = sg.fn(rng, N)
								}
								mem := mg.fn(rng, C)
								// Seed an existing pcm buffer so accum has
								// something to add into.
								pcmIn := make([]int32, Nd*C)
								if accum {
									for i := range pcmIn {
										pcmIn[i] = int32(rng.Intn(1<<18) - (1 << 17))
									}
								}

								// Reference.
								refMem := append([]int32(nil), mem...)
								refPCM := append([]int32(nil), pcmIn...)
								wantIn := make([][]int32, C)
								for c := 0; c < C; c++ {
									wantIn[c] = append([]int32(nil), in[c]...)
								}
								got, err := libopustest.ProbeCELTFixedDeemphasis(wantIn, refPCM, []int16{coef, 0, 0, 0}, refMem, N, downsample, accum)
								if err != nil {
									t.Fatalf("oracle C=%d N=%d ds=%d accum=%v coef=%d sig=%s mem=%s: %v",
										C, N, downsample, accum, coef, sg.name, mg.name, err)
								}

								// Port.
								portMem := append([]int32(nil), mem...)
								portPCM := append([]int32(nil), pcmIn...)
								Deemphasis(in, portPCM, coef, portMem, N, downsample, accum)

								for i := range got.PCM {
									if portPCM[i] != got.PCM[i] {
										t.Fatalf("pcm mismatch C=%d N=%d ds=%d accum=%v coef=%d sig=%s mem=%s i=%d: port=%d ref=%d",
											C, N, downsample, accum, coef, sg.name, mg.name, i, portPCM[i], got.PCM[i])
									}
									if Res2Int16(portPCM[i]) != Res2Int16(got.PCM[i]) {
										t.Fatalf("int16 pcm mismatch C=%d N=%d ds=%d accum=%v coef=%d sig=%s mem=%s i=%d",
											C, N, downsample, accum, coef, sg.name, mg.name, i)
									}
								}
								for c := 0; c < C; c++ {
									if portMem[c] != got.Mem[c] {
										t.Fatalf("mem mismatch C=%d N=%d ds=%d accum=%v coef=%d sig=%s mem=%s c=%d: port=%d ref=%d",
											C, N, downsample, accum, coef, sg.name, mg.name, c, portMem[c], got.Mem[c])
									}
								}
							}
						}
					}
				}
			}
		}
	}
}
