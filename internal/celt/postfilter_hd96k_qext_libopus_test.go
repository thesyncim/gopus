//go:build gopus_qext

package celt

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

var libopusHD96kCombHelper libopustest.HelperCache

func buildLibopusHD96kCombHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "HD96k QEXT comb filter",
		OutputBase:  "gopus_libopus_hd96k_qext_comb",
		SourceFile:  "libopus_celt_filter_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-DENABLE_QEXT", "-DGOPUS_FILTER_SELECT_ARCH", "-DGOPUS_FILTER_COMB_ONLY", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"src", "celt", "silk", "silk/float"},
		QEXTRef:     true,
		Libs:        []string{libopustest.QEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func probeLibopusHD96kComb(t *testing.T, separateInput bool, start, n, t0, t1, tap0, tap1 int, g0, g1 float32, window, input []float32) []float32 {
	t.Helper()
	mode := libopusCELTFilterModeCombFilter
	if separateInput {
		mode = libopusCELTFilterModeCombFilterInput
	}
	payload := libopustest.NewOraclePayload("GCFI", mode)
	for _, v := range [...]int{start, n, t0, t1, tap0, tap1, len(window)} {
		payload.U32(uint32(v))
	}
	payload.Float32(g0)
	payload.Float32(g1)
	payload.Float32s(window...)
	payload.Float32s(input...)
	binPath, err := libopusHD96kCombHelper.Path(buildLibopusHD96kCombHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "HD96k QEXT comb filter", err)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "HD96k QEXT comb filter", "GCFO")
	if err != nil {
		t.Fatalf("run HD96k QEXT comb filter: %v", err)
	}
	if got := reader.U32(); got != mode {
		t.Fatalf("comb mode: got %d want %d", got, mode)
	}
	if got := reader.U32(); int(got) != n {
		t.Fatalf("comb output length: got %d want %d", got, n)
	}
	want := make([]float32, n)
	for i := range want {
		want[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return want
}

func TestHD96kQEXTCombMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	window := GetWindowBufferF32(240)
	for _, tc := range []struct {
		name                  string
		n, t0, t1, tap0, tap1 int
		g0, g1                float32
	}{
		{"ramp_short", 320, 39, 43, 0, 1, 0.28125, 0.65625},
		{"ramp_long", 1920, 75, 73, 2, 0, 0.4375, 0.75},
		{"steady_tail", 1920, 75, 75, 1, 1, 0.65625, 0.65625},
		{"gain_to_zero", 480, 71, 73, 0, 2, 0.5, 0},
	} {
		for _, separateInput := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/separate=%t", tc.name, separateInput), func(t *testing.T) {
				const start = hd96kCombHistory
				input := make([]float32, start+tc.n+2)
				for i := range input {
					input[i] = float32(math.Sin(float64(i+11)*0.031)*2300 + math.Cos(float64(i+7)*0.017)*170)
				}
				want := probeLibopusHD96kComb(t, separateInput, start, tc.n, tc.t0, tc.t1,
					tc.tap0, tc.tap1, tc.g0, tc.g1, window, input)
				got := make([]float32, tc.n)
				if separateInput {
					src := make([]celtSig, len(input))
					dst := make([]celtSig, len(input))
					for i, v := range input {
						src[i], dst[i] = celtSig(v), celtSig(v)
					}
					combFilterWithInputSigQEXT(dst, src, start, tc.t0, tc.t1, tc.n,
						tc.g0, tc.g1, tc.tap0, tc.tap1, window, 240)
					for i := range got {
						got[i] = float32(dst[start+i])
					}
				} else {
					copyInput := append([]float32(nil), input...)
					var scratch hd96kCombPhase
					combFilterQEXTFloat32(copyInput, start, tc.t0, tc.t1, tc.n,
						tc.g0, tc.g1, tc.tap0, tc.tap1, window, 240, &scratch)
					copy(got, copyInput[start:start+tc.n])
				}
				for i := range want {
					if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
						t.Fatalf("sample[%d]: got %08x want %08x", i,
							math.Float32bits(got[i]), math.Float32bits(want[i]))
					}
				}
			})
		}
	}
}
