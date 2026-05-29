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
	libopusSILKFixedWarpedAutocorrInputMagic  = "GWAI"
	libopusSILKFixedWarpedAutocorrOutputMagic = "GWAO"
)

var (
	libopusSILKFixedWarpedOnce sync.Once
	libopusSILKFixedWarpedBin  string
	libopusSILKFixedWarpedErr  error
)

// buildLibopusSILKFixedWarpedHelper ensures the FIXED_POINT libopus reference
// exists, then compiles tools/csrc/libopus_silk_fixed_warped_autocorr_info.c
// against it.
func buildLibopusSILKFixedWarpedHelper() (string, error) {
	libopusSILKFixedWarpedOnce.Do(func() {
		_, file, _, _ := runtime.Caller(0)
		repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), ".."))

		refDir := fixedRefPath()
		staticLib := fixedRefPath(".libs", "libopus.a")
		if _, err := os.Stat(staticLib); err != nil {
			cmd := exec.Command("bash", filepath.Join("tools", "ensure_libopus.sh"))
			cmd.Dir = repoRoot
			cmd.Env = append(os.Environ(), "LIBOPUS_ENABLE_FIXED=1")
			if out, berr := cmd.CombinedOutput(); berr != nil {
				libopusSILKFixedWarpedErr = fmt.Errorf("ensure fixed libopus: %w (%s)", berr, out)
				return
			}
		}
		if _, err := os.Stat(staticLib); err != nil {
			libopusSILKFixedWarpedErr = fmt.Errorf("fixed libopus static lib missing: %w", err)
			return
		}

		cc, err := libopustooling.FindCCompiler()
		if err != nil {
			libopusSILKFixedWarpedErr = err
			return
		}

		src := filepath.Join(repoRoot, "tools", "csrc", "libopus_silk_fixed_warped_autocorr_info.c")
		outDir := filepath.Join(os.TempDir(), "gopus_libopus_test_helpers")
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			libopusSILKFixedWarpedErr = err
			return
		}
		out := filepath.Join(outDir, fmt.Sprintf("gopus_silk_fixed_warped_%s_%s", runtime.GOOS, runtime.GOARCH))

		args := []string{
			"-std=c99", "-O2", "-DHAVE_CONFIG_H",
			"-I", refDir,
			"-I", filepath.Join(refDir, "include"),
			"-I", filepath.Join(refDir, "celt"),
			"-I", filepath.Join(refDir, "silk"),
			src, staticLib, "-lm",
			"-o", out,
		}
		cmd := exec.Command(cc, args...)
		if combined, cerr := cmd.CombinedOutput(); cerr != nil {
			libopusSILKFixedWarpedErr = fmt.Errorf("build silk fixed warped helper: %w (%s)", cerr, combined)
			return
		}
		libopusSILKFixedWarpedBin = out
	})
	return libopusSILKFixedWarpedBin, libopusSILKFixedWarpedErr
}

type silkFixedWarpedCase struct {
	name       string
	warpingQ16 int32
	order      int
	in         []int16
}

type silkFixedWarpedResult struct {
	scale int
	corr  []int32
}

func probeLibopusSILKFixedWarped(cases []silkFixedWarpedCase) ([]silkFixedWarpedResult, error) {
	binPath, err := buildLibopusSILKFixedWarpedHelper()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedWarpedAutocorrInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.I32(tc.warpingQ16)
		payload.U32(uint32(len(tc.in)))
		payload.I32(int32(tc.order))
		for _, v := range tc.in {
			payload.I16(v)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed warped autocorr", libopusSILKFixedWarpedAutocorrOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]silkFixedWarpedResult, count)
	for i := range out {
		out[i].scale = int(reader.I32())
		out[i].corr = make([]int32, cases[i].order+1)
		for j := range out[i].corr {
			out[i].corr[j] = reader.I32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKWarpedAutocorrelationFixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0x7A12))

	randSignal := func(n int, amp int32) []int16 {
		x := make([]int16, n)
		for i := range x {
			x[i] = int16(rng.Int31n(2*amp+1) - amp)
		}
		return x
	}

	var cases []silkFixedWarpedCase

	// Sweep orders (even), lengths, and warping coefficients. Noise-shaping
	// warping_Q16 is small, but exercise a broad signed range.
	warps := []int32{0, 1311, 6554, -6554, 19661, -19661, 32767, -32768}
	for _, order := range []int{0, 2, 6, 12, 16, 24} {
		for _, length := range []int{1, order + 1, 40, 240, 1024} {
			if length < 1 {
				length = 1
			}
			for _, w := range warps {
				cases = append(cases, silkFixedWarpedCase{
					name:       "sweep",
					warpingQ16: w,
					order:      order,
					in:         randSignal(length, 12000),
				})
			}
		}
	}

	// Full-scale saturation stress at max order/warping.
	fullScale := make([]int16, 320)
	for i := range fullScale {
		if i%2 == 0 {
			fullScale[i] = 32767
		} else {
			fullScale[i] = -32768
		}
	}
	cases = append(cases, silkFixedWarpedCase{
		name:       "saturation",
		warpingQ16: 32767,
		order:      24,
		in:         fullScale,
	})
	cases = append(cases, silkFixedWarpedCase{
		name:       "saturation-neg-warp",
		warpingQ16: -32768,
		order:      24,
		in:         fullScale,
	})

	// All-zero input exercises the corr_QC[0]==0 CLZ64 path.
	cases = append(cases, silkFixedWarpedCase{
		name:       "zero-input",
		warpingQ16: 6554,
		order:      12,
		in:         make([]int16, 240),
	})

	// Broad random bulk.
	for i := 0; i < 96; i++ {
		order := 2 * rng.Intn(13) // 0..24 even
		length := 1 + rng.Intn(800)
		cases = append(cases, silkFixedWarpedCase{
			name:       "rand-bulk",
			warpingQ16: rng.Int31n(65536) - 32768,
			order:      order,
			in:         randSignal(length, int32(1+rng.Intn(32767))),
		})
	}

	want, err := probeLibopusSILKFixedWarped(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed warped autocorr", err)
		return
	}

	for i, tc := range cases {
		corr := make([]int32, tc.order+1)
		var scale int
		silkWarpedAutocorrelationFIX(corr, &scale, tc.in, tc.warpingQ16, len(tc.in), tc.order)
		if scale != want[i].scale {
			t.Fatalf("case %d (%s order=%d len=%d warp=%d): scale=%d want %d",
				i, tc.name, tc.order, len(tc.in), tc.warpingQ16, scale, want[i].scale)
		}
		for j := range corr {
			if corr[j] != want[i].corr[j] {
				t.Fatalf("case %d (%s order=%d len=%d warp=%d): corr[%d]=%d want %d",
					i, tc.name, tc.order, len(tc.in), tc.warpingQ16, j, corr[j], want[i].corr[j])
			}
		}
	}
}
