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
	libopusSILKFixedLimitWarpedInputMagic  = "LWCI"
	libopusSILKFixedLimitWarpedOutputMagic = "LWCO"
)

var (
	libopusSILKFixedLimitWarpedOnce sync.Once
	libopusSILKFixedLimitWarpedBin  string
	libopusSILKFixedLimitWarpedErr  error
)

// buildLibopusSILKFixedLimitWarpedHelper ensures the FIXED_POINT libopus
// reference exists, then compiles
// tools/csrc/libopus_silk_fixed_limit_warped_info.c against it.
func buildLibopusSILKFixedLimitWarpedHelper() (string, error) {
	libopusSILKFixedLimitWarpedOnce.Do(func() {
		_, file, _, _ := runtime.Caller(0)
		repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), ".."))

		refDir := fixedRefPath()
		staticLib := fixedRefPath(".libs", "libopus.a")
		if _, err := os.Stat(staticLib); err != nil {
			cmd := exec.Command("bash", filepath.Join("tools", "ensure_libopus.sh"))
			cmd.Dir = repoRoot
			cmd.Env = append(os.Environ(), "LIBOPUS_ENABLE_FIXED=1")
			if out, berr := cmd.CombinedOutput(); berr != nil {
				libopusSILKFixedLimitWarpedErr = fmt.Errorf("ensure fixed libopus: %w (%s)", berr, out)
				return
			}
		}
		if _, err := os.Stat(staticLib); err != nil {
			libopusSILKFixedLimitWarpedErr = fmt.Errorf("fixed libopus static lib missing: %w", err)
			return
		}

		cc, err := libopustooling.FindCCompiler()
		if err != nil {
			libopusSILKFixedLimitWarpedErr = err
			return
		}

		src := filepath.Join(repoRoot, "tools", "csrc", "libopus_silk_fixed_limit_warped_info.c")
		outDir := filepath.Join(os.TempDir(), "gopus_libopus_test_helpers")
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			libopusSILKFixedLimitWarpedErr = err
			return
		}
		out := filepath.Join(outDir, fmt.Sprintf("gopus_silk_fixed_limit_warped_%s_%s", runtime.GOOS, runtime.GOARCH))

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
			libopusSILKFixedLimitWarpedErr = fmt.Errorf("build silk fixed limit warped helper: %w (%s)", cerr, combined)
			return
		}
		libopusSILKFixedLimitWarpedBin = out
	})
	return libopusSILKFixedLimitWarpedBin, libopusSILKFixedLimitWarpedErr
}

type silkFixedLimitWarpedCase struct {
	name      string
	lambdaQ16 int32
	limitQ24  int32
	order     int
	coefsQ24  []int32
}

func probeLibopusSILKFixedLimitWarped(cases []silkFixedLimitWarpedCase) ([][]int32, error) {
	binPath, err := buildLibopusSILKFixedLimitWarpedHelper()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedLimitWarpedInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.I32(tc.lambdaQ16)
		payload.I32(tc.limitQ24)
		payload.I32(int32(tc.order))
		for i := 0; i < tc.order; i++ {
			payload.I32(tc.coefsQ24[i])
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed limit warped", libopusSILKFixedLimitWarpedOutputMagic)
	if err != nil {
		return nil, err
	}
	cnt := reader.Count(len(cases))
	out := make([][]int32, cnt)
	for i := range out {
		out[i] = make([]int32, cases[i].order)
		for k := range out[i] {
			out[i][k] = reader.I32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKLimitWarpedCoefsFixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0x11ed7a92))

	const limitQ24 = 67092938 // SILK_FIX_CONST(3.999, 24)

	var cases []silkFixedLimitWarpedCase

	// realisticCoefs builds coefficients with magnitudes around the typical
	// range produced by k2a_Q16 + bwexpander_32 (Q24, a few units), scaled by
	// mag to push above/below the limit.
	mkCoefs := func(order int, mag float64) []int32 {
		c := make([]int32, order)
		for i := range c {
			// Decaying magnitude with random sign, like real LPC coefs.
			v := (rng.Float64()*2 - 1) * mag / float64(i+1)
			c[i] = int32(v * (1 << 24))
		}
		return c
	}

	// fs_kHz * SILK_FIX_CONST(WARPING_MULTIPLIER=0.015, 16) = fs_kHz * 983,
	// plus a small coding-quality adjustment.
	warpingFor := func(fsKHz int) int32 {
		base := int32(fsKHz) * int32(silkFixConst(0.015, 16))
		adj := int32(rng.Intn(16385)) // coding_quality_Q14 in [0,16384]
		return silkSMLAWB(base, adj, int32(silkFixConst(0.01, 18)))
	}

	add := func(name string, lambda, limit int32, coefs []int32) {
		cases = append(cases, silkFixedLimitWarpedCase{
			name:      name,
			lambdaQ16: lambda,
			limitQ24:  limit,
			order:     len(coefs),
			coefsQ24:  coefs,
		})
	}

	// Realistic: typical orders (12..24 even) and warping factors, with
	// coefficients that may or may not exceed the limit.
	for _, fs := range []int{8, 12, 16} {
		for _, order := range []int{12, 16, 18, 20, 24} {
			for _, mag := range []float64{0.5, 2.0, 5.0, 12.0} {
				add(fmt.Sprintf("fs%d_o%d_m%.1f", fs, order, mag),
					warpingFor(fs), limitQ24, mkCoefs(order, mag))
			}
		}
	}

	// Edge: lambda = 0 (no warping path is still valid through this helper).
	for _, order := range []int{12, 16, 24} {
		add(fmt.Sprintf("lambda0_o%d", order), 0, limitQ24, mkCoefs(order, 6.0))
	}

	// Edge: tiny coefficients well within range (early return on iter 0).
	for _, order := range []int{12, 16, 24} {
		add(fmt.Sprintf("tiny_o%d", order), warpingFor(16), limitQ24, mkCoefs(order, 0.01))
	}

	// Edge: very large coefficients forcing many bandwidth-expansion iterations.
	for _, order := range []int{12, 16, 24} {
		add(fmt.Sprintf("huge_o%d", order), warpingFor(16), limitQ24, mkCoefs(order, 40.0))
	}

	// Edge: alternative limits.
	for _, lim := range []int32{int32(silkFixConst(1.0, 24)), int32(silkFixConst(8.0, 24))} {
		add(fmt.Sprintf("limit%d", lim), warpingFor(12), lim, mkCoefs(16, 6.0))
	}

	// Bulk random coverage with random warping in [-20000, 20000].
	for i := 0; i < 256; i++ {
		order := 12 + 2*rng.Intn(7) // 12..24 even
		lambda := int32(rng.Intn(40001) - 20000)
		mag := rng.Float64() * 30.0
		add("bulk", lambda, limitQ24, mkCoefs(order, mag))
	}

	want, err := probeLibopusSILKFixedLimitWarped(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed limit warped", err)
		return
	}

	for i, tc := range cases {
		got := make([]int32, tc.order)
		copy(got, tc.coefsQ24)
		silkLimitWarpedCoefsFixed(got, tc.lambdaQ16, tc.limitQ24, tc.order)

		for k := 0; k < tc.order; k++ {
			if got[k] != want[i][k] {
				t.Fatalf("case %d (%s lambda=%d limit=%d order=%d): coefs_Q24[%d]=%d want %d",
					i, tc.name, tc.lambdaQ16, tc.limitQ24, tc.order, k, got[k], want[i][k])
			}
		}
	}
}
