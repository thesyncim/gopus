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
	libopusSILKFixedCorrMatrixInputMagic  = "GCMI"
	libopusSILKFixedCorrMatrixOutputMagic = "GCMO"
)

var (
	libopusSILKFixedCorrMatrixOnce sync.Once
	libopusSILKFixedCorrMatrixBin  string
	libopusSILKFixedCorrMatrixErr  error
)

// buildLibopusSILKFixedCorrMatrixHelper ensures the FIXED_POINT libopus
// reference exists, then compiles
// tools/csrc/libopus_silk_fixed_corr_matrix_info.c against it.
func buildLibopusSILKFixedCorrMatrixHelper() (string, error) {
	libopusSILKFixedCorrMatrixOnce.Do(func() {
		_, file, _, _ := runtime.Caller(0)
		repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), ".."))

		refDir := fixedRefPath()
		staticLib := fixedRefPath(".libs", "libopus.a")
		if _, err := os.Stat(staticLib); err != nil {
			cmd := exec.Command("bash", filepath.Join("tools", "ensure_libopus.sh"))
			cmd.Dir = repoRoot
			cmd.Env = append(os.Environ(), "LIBOPUS_ENABLE_FIXED=1")
			if out, berr := cmd.CombinedOutput(); berr != nil {
				libopusSILKFixedCorrMatrixErr = fmt.Errorf("ensure fixed libopus: %w (%s)", berr, out)
				return
			}
		}
		if _, err := os.Stat(staticLib); err != nil {
			libopusSILKFixedCorrMatrixErr = fmt.Errorf("fixed libopus static lib missing: %w", err)
			return
		}

		cc, err := libopustooling.FindCCompiler()
		if err != nil {
			libopusSILKFixedCorrMatrixErr = err
			return
		}

		src := filepath.Join(repoRoot, "tools", "csrc", "libopus_silk_fixed_corr_matrix_info.c")
		outDir := filepath.Join(os.TempDir(), "gopus_libopus_test_helpers")
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			libopusSILKFixedCorrMatrixErr = err
			return
		}
		out := filepath.Join(outDir, fmt.Sprintf("gopus_silk_fixed_corrmatrix_%s_%s", runtime.GOOS, runtime.GOARCH))

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
			libopusSILKFixedCorrMatrixErr = fmt.Errorf("build silk fixed corr matrix helper: %w (%s)", cerr, combined)
			return
		}
		libopusSILKFixedCorrMatrixBin = out
	})
	return libopusSILKFixedCorrMatrixBin, libopusSILKFixedCorrMatrixErr
}

type silkFixedCorrMatrixCase struct {
	name  string
	L     int
	order int
	x     []int16 // [L+order-1]
	t     []int16 // [L]
}

type silkFixedCorrMatrixResult struct {
	XX      []int32
	Xt      []int32
	nrg     int32
	rshifts int32
}

func probeLibopusSILKFixedCorrMatrix(cases []silkFixedCorrMatrixCase) ([]silkFixedCorrMatrixResult, error) {
	binPath, err := buildLibopusSILKFixedCorrMatrixHelper()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedCorrMatrixInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.L))
		payload.U32(uint32(tc.order))
		for _, v := range tc.x {
			payload.I16(v)
		}
		for _, v := range tc.t {
			payload.I16(v)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed corr matrix", libopusSILKFixedCorrMatrixOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]silkFixedCorrMatrixResult, count)
	for i := range out {
		order := cases[i].order
		out[i].XX = make([]int32, order*order)
		for j := range out[i].XX {
			out[i].XX[j] = reader.I32()
		}
		out[i].Xt = make([]int32, order)
		for j := range out[i].Xt {
			out[i].Xt[j] = reader.I32()
		}
		out[i].nrg = reader.I32()
		out[i].rshifts = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKCorrMatrixFixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0xc077))

	randSignal := func(n int, amp int32) []int16 {
		x := make([]int16, n)
		for i := range x {
			x[i] = int16(rng.Int31n(2*amp+1) - amp)
		}
		return x
	}

	newCase := func(name string, L, order int, amp int32) silkFixedCorrMatrixCase {
		return silkFixedCorrMatrixCase{
			name:  name,
			L:     L,
			order: order,
			x:     randSignal(L+order-1, amp),
			t:     randSignal(L, amp),
		}
	}

	var cases []silkFixedCorrMatrixCase

	// Standard SILK LPC-analysis configurations: short subframe lengths and
	// LPC orders 10/16, low amplitude so rshifts==0 (silk_inner_prod path).
	for _, L := range []int{40, 60, 80, 120, 160} {
		for _, order := range []int{10, 16} {
			cases = append(cases, newCase("std", L, order, 2000))
		}
	}

	// High amplitude / long vectors to force rshifts > 0 (shifted path).
	for _, L := range []int{160, 320, 640} {
		for _, order := range []int{10, 16, 24} {
			cases = append(cases, newCase("bigenergy", L, order, 32767))
		}
	}

	// Small edge cases.
	cases = append(cases, newCase("order1", 64, 1, 12000))
	cases = append(cases, newCase("order2", 64, 2, 12000))
	cases = append(cases, newCase("shortL", 4, 4, 12000))

	// Bulk random coverage spanning amplitude and shape.
	for i := 0; i < 64; i++ {
		L := 8 + rng.Intn(600)
		order := 1 + rng.Intn(24)
		amp := int32(1 + rng.Intn(32767))
		cases = append(cases, newCase("bulk", L, order, amp))
	}

	want, err := probeLibopusSILKFixedCorrMatrix(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed corr matrix", err)
		return
	}

	for i, tc := range cases {
		XX := make([]int32, tc.order*tc.order)
		nrg, rshifts := silkCorrMatrixFixed(tc.x, tc.L, tc.order, XX)
		Xt := make([]int32, tc.order)
		silkCorrVectorFixed(tc.x, tc.t, tc.L, tc.order, Xt, rshifts)

		if nrg != want[i].nrg {
			t.Fatalf("case %d (%s L=%d order=%d): nrg=%d want %d",
				i, tc.name, tc.L, tc.order, nrg, want[i].nrg)
		}
		if int32(rshifts) != want[i].rshifts {
			t.Fatalf("case %d (%s L=%d order=%d): rshifts=%d want %d",
				i, tc.name, tc.L, tc.order, rshifts, want[i].rshifts)
		}
		for j := range XX {
			if XX[j] != want[i].XX[j] {
				t.Fatalf("case %d (%s L=%d order=%d): XX[%d]=%d want %d",
					i, tc.name, tc.L, tc.order, j, XX[j], want[i].XX[j])
			}
		}
		for j := range Xt {
			if Xt[j] != want[i].Xt[j] {
				t.Fatalf("case %d (%s L=%d order=%d): Xt[%d]=%d want %d",
					i, tc.name, tc.L, tc.order, j, Xt[j], want[i].Xt[j])
			}
		}
	}
}
