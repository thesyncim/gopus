//go:build gopus_custom_modes

package celt

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

var customPreemphasisHelper libopustest.HelperCache

func TestCustomPreemphasisMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	helper, err := customPreemphasisHelper.Path(func() (string, error) {
		return buildCustomFloatKernelHelper(libopustest.CHelperConfig{
			Label: "custom preemphasis", OutputBase: "gopus_custom_preemphasis", SourceFile: "libopus_celt_preemphasis_info.c",
			CFlags: []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"}, RefIncludes: []string{"celt", "silk", "src"},
			Libs: []string{"-lm"}, DeadStrip: true,
		})
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "custom preemphasis", err)
		return
	}
	// celt/modes.c tables for 16 kHz and 96 kHz exercise both signs of coef1.
	for _, coef := range [][4]float32{{0.6000061035, -0.1799926758, 0.4424998650, 2.2598876953}, {0.9230041504, 0.2200012207, 1.5128347184, 0.6610107422}} {
		for _, channels := range []int{1, 2} {
			t.Run(fmt.Sprintf("coef%g/ch%d", coef[0], channels), func(t *testing.T) {
				enc := NewEncoder(channels)
				enc.hd96kPreemph = coef
				mem := make([]float32, channels)
				for c := range mem {
					mem[c] = float32(c+1) * 0.0123456
				}
				copy(enc.preemphState, mem)
				for frame := range 3 {
					const n = 61
					pcm := preemphOraclePCM(channels, n, float64(frame)*0.23)
					if frame == 1 {
						for i := range pcm {
							pcm[i] *= 1e-30
						}
					}
					if frame == 2 {
						clear(pcm)
					}
					req := libopustest.NewOraclePayloadVersion("GCPI", 2, 1, uint32(channels), n)
					req.Float32s(coef[:]...)
					req.Float32s(mem...)
					req.Float32s(pcm...)
					rd, err := libopustest.RunOracleVersion(helper, req.Bytes(), "custom preemphasis", "GCPO", 2)
					if err != nil {
						t.Fatal(err)
					}
					if count := rd.U32(); count != 1 {
						t.Fatalf("C case count=%d", count)
					}
					want := make([]float32, len(pcm))
					for i := range want {
						want[i] = rd.Float32()
					}
					for c := range mem {
						mem[c] = rd.Float32()
					}
					if err := rd.ExpectConsumed(); err != nil {
						t.Fatal(err)
					}
					out := make([]float32, len(pcm))
					run := func() { enc.applyPreemphasis2TapAndSilenceCore(pcm, out, len(pcm), len(pcm)-20*channels, channels) }
					run()
					assertCELTFilterFloat32Bits(t, fmt.Sprintf("frame%d output", frame), out, want)
					assertCELTFilterFloat32Bits(t, fmt.Sprintf("frame%d memory", frame), enc.preemphState, mem)
					if allocs := testing.AllocsPerRun(20, run); allocs != 0 {
						t.Fatalf("warm allocations=%g", allocs)
					}
					copy(enc.preemphState, mem)
				}
			})
		}
	}
}
