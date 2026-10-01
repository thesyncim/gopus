//go:build gopus_custom_modes

package celt

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

var customDeemphasisHelper libopustest.HelperCache

func probeCustomDeemphasis(t *testing.T, planes [][]float32, mem []float32, coef [4]float32, downsample int, seed []float32) libopusDeemphasisResult {
	t.Helper()
	helper, err := customDeemphasisHelper.Path(func() (string, error) {
		return buildCustomFloatKernelHelper(libopustest.CHelperConfig{
			Label: "custom deemphasis", OutputBase: "gopus_custom_deemphasis", SourceFile: "libopus_celt_filter_info.c",
			CFlags:      []string{"-DHAVE_CONFIG_H", "-DRESYNTH", "-O3", "-DNDEBUG"},
			RefIncludes: []string{"src", "celt", "silk", "silk/float"},
			RefSources:  []string{"celt/celt_decoder.c", "celt/celt.c", "celt/x86/pitch_sse.c", "celt/x86/x86_celt_map.c"},
			Libs:        []string{"-lm"}, DeadStrip: true,
		})
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "custom deemphasis", err)
		return libopusDeemphasisResult{}
	}
	channels, n := len(planes), len(planes[0])
	payload := libopustest.NewOraclePayload("GCFI", libopusCELTFilterModeDeemphasis, uint32(channels), uint32(n), uint32(downsample))
	accum := uint32(0)
	if seed != nil {
		accum = 1
	}
	payload.U32(accum)
	payload.Float32s(coef[:]...)
	payload.Float32s(mem...)
	for _, plane := range planes {
		payload.Float32s(plane...)
	}
	payload.Float32s(seed...)
	reader, err := libopustest.RunOracle(helper, payload.Bytes(), "custom deemphasis", "GCFO")
	if err != nil {
		t.Fatal(err)
	}
	if mode := reader.U32(); mode != libopusCELTFilterModeDeemphasis {
		t.Fatalf("C mode=%d", mode)
	}
	count := channels * (n / downsample)
	if got := reader.U32(); got != uint32(count) {
		t.Fatalf("C samples=%d want=%d", got, count)
	}
	out := libopusDeemphasisResult{mem: make([]float32, channels), pcm: make([]float32, count)}
	for i := range out.mem {
		out.mem[i] = reader.Float32()
	}
	for i := range out.pcm {
		out.pcm[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return out
}

// The two-tap branch is compiled by CUSTOM_MODES. Compare its memory and
// samples with the actual celt_decoder.c function in the paired C build.
func TestCustomDeemphasisMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	// celt/modes.c:opus_custom_mode_create coefficients below 12 kHz and at 96 kHz.
	for _, coef := range [][4]float32{{0.3500061035, -0.1799926758, 0.2719968125, 3.6765136719}, {0.9230041504, 0.2200012207, 1.5128347184, 0.6610107422}} {
		for _, channels := range []int{1, 2} {
			for _, downsample := range []int{1, 2, 3} {
				for _, accum := range []bool{false, true} {
					t.Run(fmt.Sprintf("coef%g/ch%d/ds%d/accum%t", coef[0], channels, downsample, accum), func(t *testing.T) {
						const n = 121
						dec := NewDecoder(channels)
						dec.deemphCoef, dec.deemphCoef1, dec.deemphCoef3 = coef[0], coef[1], coef[3]
						oracleMem := make([]float32, channels)
						for c := range oracleMem {
							oracleMem[c] = float32(c+1) * -0.03125
						}
						copy(dec.preemphState, oracleMem)
						for frame := range 3 {
							planes := make([][]float32, channels)
							interleaved := make([]float32, n*channels)
							for c := range channels {
								planes[c] = make([]float32, n)
								for i := range n {
									x := float32((i*37+c*17)%101-50) * 0.1234567
									if frame == 1 {
										x *= 1e-30
									}
									if frame == 2 {
										x = 0
									}
									planes[c][i], interleaved[i*channels+c] = x, x
								}
							}
							count := channels * (n / downsample)
							var seed []float32
							if accum {
								seed = make([]float32, count)
								for i := range seed {
									seed[i] = float32(i%7-3) * 0.017321
								}
							}
							want := probeCustomDeemphasis(t, planes, oracleMem, coef, downsample, seed)
							for _, layout := range []string{"planar", "interleaved", "in-place"} {
								if layout == "in-place" && (accum || downsample != 1) {
									continue
								}
								copy(dec.preemphState, oracleMem)
								out := make([]float32, count)
								copy(out, seed)
								x0, x1, stride := planes[0], planes[0], 1
								if channels == 2 {
									x1 = planes[1]
								}
								if layout != "planar" {
									x0, stride = interleaved, channels
									if layout == "in-place" {
										copy(out, interleaved)
										x0 = out
									}
									x1 = x0
									if channels == 2 {
										x1 = x0[1:]
									}
								}
								run := func() { dec.deemphasis(out, x0, x1, stride, n, downsample, accum) }
								run()
								assertCELTFilterFloat32Bits(t, fmt.Sprintf("frame%d/%s", frame, layout), out, want.pcm)
								assertCELTFilterMemBits(t, dec, want.mem)
								if allocs := testing.AllocsPerRun(20, run); allocs != 0 {
									t.Fatalf("%s warm allocations=%g", layout, allocs)
								}
							}
							copy(oracleMem, want.mem)
						}
					})
				}
			}
		}
	}
}
