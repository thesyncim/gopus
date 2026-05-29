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
	libopusSILKFixedSolveLSInputMagic  = "GSLI"
	libopusSILKFixedSolveLSOutputMagic = "GSLO"
)

var (
	libopusSILKFixedSolveLSOnce sync.Once
	libopusSILKFixedSolveLSBin  string
	libopusSILKFixedSolveLSErr  error
)

// buildLibopusSILKFixedSolveLSHelper ensures the FIXED_POINT libopus reference
// exists, then compiles tools/csrc/libopus_silk_fixed_solve_ls_info.c against
// it.
func buildLibopusSILKFixedSolveLSHelper() (string, error) {
	libopusSILKFixedSolveLSOnce.Do(func() {
		_, file, _, _ := runtime.Caller(0)
		repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), ".."))

		refDir := fixedRefPath()
		staticLib := fixedRefPath(".libs", "libopus.a")
		if _, err := os.Stat(staticLib); err != nil {
			cmd := exec.Command("bash", filepath.Join("tools", "ensure_libopus.sh"))
			cmd.Dir = repoRoot
			cmd.Env = append(os.Environ(), "LIBOPUS_ENABLE_FIXED=1")
			if out, berr := cmd.CombinedOutput(); berr != nil {
				libopusSILKFixedSolveLSErr = fmt.Errorf("ensure fixed libopus: %w (%s)", berr, out)
				return
			}
		}
		if _, err := os.Stat(staticLib); err != nil {
			libopusSILKFixedSolveLSErr = fmt.Errorf("fixed libopus static lib missing: %w", err)
			return
		}

		cc, err := libopustooling.FindCCompiler()
		if err != nil {
			libopusSILKFixedSolveLSErr = err
			return
		}

		src := filepath.Join(repoRoot, "tools", "csrc", "libopus_silk_fixed_solve_ls_info.c")
		outDir := filepath.Join(os.TempDir(), "gopus_libopus_test_helpers")
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			libopusSILKFixedSolveLSErr = err
			return
		}
		out := filepath.Join(outDir, fmt.Sprintf("gopus_silk_fixed_solvels_%s_%s", runtime.GOOS, runtime.GOARCH))

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
			libopusSILKFixedSolveLSErr = fmt.Errorf("build silk fixed solve ls helper: %w (%s)", cerr, combined)
			return
		}
		libopusSILKFixedSolveLSBin = out
	})
	return libopusSILKFixedSolveLSBin, libopusSILKFixedSolveLSErr
}

type silkFixedSolveLSCase struct {
	name  string
	D     int
	noise int32
	XX    []int32 // [D*D]
	xx    []int32 // [D]
}

type silkFixedSolveLSResult struct {
	XX []int32
	xx []int32
}

func probeLibopusSILKFixedSolveLS(cases []silkFixedSolveLSCase) ([]silkFixedSolveLSResult, error) {
	binPath, err := buildLibopusSILKFixedSolveLSHelper()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedSolveLSInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.D))
		payload.I32(tc.noise)
		for _, v := range tc.XX {
			payload.I32(v)
		}
		for _, v := range tc.xx {
			payload.I32(v)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed solve ls", libopusSILKFixedSolveLSOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]silkFixedSolveLSResult, count)
	for i := range out {
		D := cases[i].D
		out[i].XX = make([]int32, D*D)
		for j := range out[i].XX {
			out[i].XX[j] = reader.I32()
		}
		out[i].xx = make([]int32, D)
		for j := range out[i].xx {
			out[i].xx[j] = reader.I32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKSolveLSRegularizeFixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0x501e15))

	randMatrix := func(D int, amp int32) []int32 {
		m := make([]int32, D*D)
		for i := range m {
			m[i] = rng.Int31n(2*amp+1) - amp
		}
		return m
	}
	randVector := func(D int, amp int32) []int32 {
		v := make([]int32, D)
		for i := range v {
			v[i] = rng.Int31n(2*amp+1) - amp
		}
		return v
	}

	newCase := func(name string, D int, noise, amp int32) silkFixedSolveLSCase {
		return silkFixedSolveLSCase{
			name:  name,
			D:     D,
			noise: noise,
			XX:    randMatrix(D, amp),
			xx:    randVector(D, amp),
		}
	}

	var cases []silkFixedSolveLSCase

	// Standard SILK LTP/LPC normal-equation matrix orders.
	for _, D := range []int{1, 2, 5, 10, 16, 24} {
		cases = append(cases, newCase("std", D, 1<<10, 1<<20))
	}

	// Well-conditioned: large diagonal, small noise.
	cases = append(cases, newCase("wellcond", 16, 1, 1<<25))
	// Ill-conditioned: tiny diagonal, large noise floor dominates.
	cases = append(cases, newCase("illcond", 16, 1<<28, 4))

	// Saturation paths around silk_ADD32 wrap on the diagonal / xx[0].
	cases = append(cases, silkFixedSolveLSCase{
		name: "satpos", D: 4, noise: 1 << 30,
		XX: []int32{
			1 << 30, 0, 0, 0,
			0, 1 << 30, 0, 0,
			0, 0, 1 << 30, 0,
			0, 0, 0, 1 << 30,
		},
		xx: []int32{1 << 30, 0, 0, 0},
	})
	cases = append(cases, silkFixedSolveLSCase{
		name: "satneg", D: 3, noise: -(1 << 30),
		XX: []int32{
			-(1 << 30), 5, 5,
			5, -(1 << 30), 5,
			5, 5, -(1 << 30),
		},
		xx: []int32{-(1 << 30), 7, 9},
	})

	// Bulk random coverage spanning orders, noise and amplitude.
	for i := 0; i < 96; i++ {
		D := 1 + rng.Intn(24)
		amp := int32(1 + rng.Intn(1<<24))
		noise := rng.Int31n(1<<28) - (1 << 27)
		cases = append(cases, newCase("bulk", D, noise, amp))
	}

	want, err := probeLibopusSILKFixedSolveLS(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed solve ls", err)
		return
	}

	for i, tc := range cases {
		XX := make([]int32, len(tc.XX))
		copy(XX, tc.XX)
		xx := make([]int32, len(tc.xx))
		copy(xx, tc.xx)

		silkRegularizeCorrelationsFixed(XX, xx, tc.noise, tc.D)

		for j := range XX {
			if XX[j] != want[i].XX[j] {
				t.Fatalf("case %d (%s D=%d noise=%d): XX[%d]=%d want %d",
					i, tc.name, tc.D, tc.noise, j, XX[j], want[i].XX[j])
			}
		}
		for j := range xx {
			if xx[j] != want[i].xx[j] {
				t.Fatalf("case %d (%s D=%d noise=%d): xx[%d]=%d want %d",
					i, tc.name, tc.D, tc.noise, j, xx[j], want[i].xx[j])
			}
		}
	}
}
