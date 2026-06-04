//go:build gopus_fixedpoint

package silk

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

const (
	libopusSILKFixedFindLPCInputMagic  = "GFLI"
	libopusSILKFixedFindLPCOutputMagic = "GFLO"
)

var (
	libopusSILKFixedFindLPCOnce sync.Once
	libopusSILKFixedFindLPCBin  string
	libopusSILKFixedFindLPCErr  error
)

// buildLibopusSILKFixedFindLPCHelper ensures the FIXED_POINT libopus reference
// exists, then compiles tools/csrc/libopus_silk_fixed_find_lpc_info.c against it.
func buildLibopusSILKFixedFindLPCHelper() (string, error) {
	libopusSILKFixedFindLPCOnce.Do(func() {
		_, file, _, _ := runtime.Caller(0)
		repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))

		refDir := fixedRefPath()
		staticLib := fixedRefPath(".libs", "libopus.a")
		if _, err := os.Stat(staticLib); err != nil {
			cmd := exec.Command("bash", filepath.Join("tools", "ensure_libopus.sh"))
			cmd.Dir = repoRoot
			cmd.Env = append(os.Environ(), "LIBOPUS_ENABLE_FIXED=1")
			if out, berr := cmd.CombinedOutput(); berr != nil {
				libopusSILKFixedFindLPCErr = fmt.Errorf("ensure fixed libopus: %w (%s)", berr, out)
				return
			}
		}
		if _, err := os.Stat(staticLib); err != nil {
			libopusSILKFixedFindLPCErr = fmt.Errorf("fixed libopus static lib missing: %w", err)
			return
		}

		cc, err := libopustooling.FindCCompiler()
		if err != nil {
			libopusSILKFixedFindLPCErr = err
			return
		}

		src := filepath.Join(repoRoot, "tools", "csrc", "libopus_silk_fixed_find_lpc_info.c")
		outDir := filepath.Join(os.TempDir(), "gopus_libopus_test_helpers")
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			libopusSILKFixedFindLPCErr = err
			return
		}
		out := filepath.Join(outDir, fmt.Sprintf("gopus_silk_fixed_find_lpc_%s_%s", runtime.GOOS, runtime.GOARCH))

		args := []string{
			"-std=c99", "-O2", "-DHAVE_CONFIG_H",
			"-I", refDir,
			"-I", filepath.Join(refDir, "include"),
			"-I", filepath.Join(refDir, "celt"),
			"-I", filepath.Join(refDir, "silk"),
			"-I", filepath.Join(refDir, "silk", "fixed"),
			src, staticLib, "-lm",
			"-o", out,
		}
		cmd := exec.Command(cc, args...)
		if combined, cerr := cmd.CombinedOutput(); cerr != nil {
			libopusSILKFixedFindLPCErr = fmt.Errorf("build silk fixed find_LPC helper: %w (%s)", cerr, combined)
			return
		}
		libopusSILKFixedFindLPCBin = out
	})
	return libopusSILKFixedFindLPCBin, libopusSILKFixedFindLPCErr
}

type silkFixedFindLPCResult struct {
	nlsfQ15          [maxLPCOrder]int16
	nlsfInterpCoefQ2 int32
}

func probeLibopusSILKFixedFindLPC(cases []silkFindLPCInput) ([]silkFixedFindLPCResult, error) {
	binPath, err := buildLibopusSILKFixedFindLPCHelper()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedFindLPCInputMagic, uint32(len(cases)))
	for i := range cases {
		tc := &cases[i]
		payload.I32(int32(tc.predictLPCOrder))
		payload.I32(int32(tc.subfrLength))
		payload.I32(int32(tc.nbSubfr))
		if tc.useInterpolatedNLSFs {
			payload.I32(1)
		} else {
			payload.I32(0)
		}
		if tc.firstFrameAfterReset {
			payload.I32(1)
		} else {
			payload.I32(0)
		}
		payload.I32(tc.minInvGainQ30)
		for j := 0; j < maxLPCOrder; j++ {
			payload.I16(tc.prevNLSFqQ15[j])
		}
		payload.U32(uint32(len(tc.x)))
		for _, v := range tc.x {
			payload.I16(v)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed find_LPC", libopusSILKFixedFindLPCOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]silkFixedFindLPCResult, count)
	for i := range out {
		for j := 0; j < maxLPCOrder; j++ {
			out[i].nlsfQ15[j] = int16(reader.I32())
		}
		out[i].nlsfInterpCoefQ2 = reader.I32()
	}
	if err := reader.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKFindLPCFixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0x10C))

	// Build a monotonically increasing prev NLSF vector (valid LSF ordering).
	makePrevNLSF := func(order int) [maxLPCOrder]int16 {
		var p [maxLPCOrder]int16
		spacing := int16((1 << 15) / int32(order+1))
		v := spacing
		for i := 0; i < order; i++ {
			p[i] = v
			v += spacing
		}
		return p
	}

	// randSignal generates a band-limited-ish int16 signal (low-frequency
	// correlated) so that Burg analysis produces meaningful LPC structure.
	randSignal := func(n int, amp int32) []int16 {
		x := make([]int16, n)
		var acc int32
		for i := range x {
			acc += rng.Int31n(2*amp+1) - amp
			if acc > 32767 {
				acc = 32767
			} else if acc < -32768 {
				acc = -32768
			}
			x[i] = int16(acc >> 4)
		}
		return x
	}

	makeCase := func(order, fsKHz, nbSubfr int, useInterp, firstFrame bool, minInvGainQ30, amp int32) silkFindLPCInput {
		subfrLength := 5 * fsKHz
		blockLen := subfrLength + order
		xLen := nbSubfr * blockLen
		return silkFindLPCInput{
			predictLPCOrder:      order,
			subfrLength:          subfrLength,
			nbSubfr:              nbSubfr,
			useInterpolatedNLSFs: useInterp,
			firstFrameAfterReset: firstFrame,
			prevNLSFqQ15:         makePrevNLSF(order),
			minInvGainQ30:        minInvGainQ30,
			x:                    randSignal(xLen, amp),
		}
	}

	// SILK_FIX_CONST(1e-4, 30) ~= 107374; a realistic minInvGain_Q30.
	const minInvGain = int32(107374)

	var cases []silkFindLPCInput
	for _, order := range []int{10, 16} {
		for _, fs := range []int{8, 12, 16} {
			for _, nb := range []int{2, 4} {
				for _, useInterp := range []bool{false, true} {
					for _, first := range []bool{false, true} {
						for _, amp := range []int32{200, 4000, 30000} {
							cases = append(cases, makeCase(order, fs, nb, useInterp, first, minInvGain, amp))
						}
					}
				}
			}
		}
	}
	// A couple of extreme minInvGain values to exercise the max-gain early-out.
	cases = append(cases, makeCase(16, 16, 4, true, false, int32(1)<<30, 8000))
	cases = append(cases, makeCase(16, 16, 4, true, false, 1, 8000))

	want, err := probeLibopusSILKFixedFindLPC(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed find_LPC", err)
		return
	}

	sc := &silkFixedEncodeScratch{}
	for i := range cases {
		in := cases[i]
		got := silkFindLPCFIX(sc, &in)
		w := want[i]

		fail := func(field string, g, e interface{}) {
			t.Fatalf("case %d (order=%d nb=%d len=%d interp=%v first=%v): %s got %v want %v",
				i, cases[i].predictLPCOrder, cases[i].nbSubfr, len(cases[i].x),
				cases[i].useInterpolatedNLSFs, cases[i].firstFrameAfterReset, field, g, e)
		}

		if int32(got.nlsfInterpCoefQ2) != w.nlsfInterpCoefQ2 {
			fail("NLSFInterpCoef_Q2", got.nlsfInterpCoefQ2, w.nlsfInterpCoefQ2)
		}
		for j := 0; j < cases[i].predictLPCOrder; j++ {
			if got.nlsfQ15[j] != w.nlsfQ15[j] {
				fail(fmt.Sprintf("NLSF_Q15[%d]", j), got.nlsfQ15[j], w.nlsfQ15[j])
			}
		}
	}
}
