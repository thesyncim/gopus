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
	libopusSILKFixedApplySineWindowInputMagic  = "GASI"
	libopusSILKFixedApplySineWindowOutputMagic = "GASO"
)

var (
	libopusSILKFixedApplySineOnce sync.Once
	libopusSILKFixedApplySineBin  string
	libopusSILKFixedApplySineErr  error
)

// buildLibopusSILKFixedApplySineHelper ensures the FIXED_POINT libopus reference
// exists, then compiles
// tools/csrc/libopus_silk_fixed_apply_sine_window_info.c against it.
func buildLibopusSILKFixedApplySineHelper() (string, error) {
	libopusSILKFixedApplySineOnce.Do(func() {
		_, file, _, _ := runtime.Caller(0)
		repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))

		refDir := fixedRefPath()
		staticLib := fixedRefPath(".libs", "libopus.a")
		if _, err := os.Stat(staticLib); err != nil {
			cmd := exec.Command("bash", filepath.Join("tools", "ensure_libopus.sh"))
			cmd.Dir = repoRoot
			cmd.Env = append(os.Environ(), "LIBOPUS_ENABLE_FIXED=1")
			if out, berr := cmd.CombinedOutput(); berr != nil {
				libopusSILKFixedApplySineErr = fmt.Errorf("ensure fixed libopus: %w (%s)", berr, out)
				return
			}
		}
		if _, err := os.Stat(staticLib); err != nil {
			libopusSILKFixedApplySineErr = fmt.Errorf("fixed libopus static lib missing: %w", err)
			return
		}

		cc, err := libopustooling.FindCCompiler()
		if err != nil {
			libopusSILKFixedApplySineErr = err
			return
		}

		src := filepath.Join(repoRoot, "tools", "csrc", "libopus_silk_fixed_apply_sine_window_info.c")
		outDir := filepath.Join(os.TempDir(), "gopus_libopus_test_helpers")
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			libopusSILKFixedApplySineErr = err
			return
		}
		out := filepath.Join(outDir, fmt.Sprintf("gopus_silk_fixed_apply_sine_%s_%s", runtime.GOOS, runtime.GOARCH))

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
			libopusSILKFixedApplySineErr = fmt.Errorf("build silk fixed apply-sine helper: %w (%s)", cerr, combined)
			return
		}
		libopusSILKFixedApplySineBin = out
	})
	return libopusSILKFixedApplySineBin, libopusSILKFixedApplySineErr
}

type silkFixedApplySineCase struct {
	name    string
	winType int
	in      []int16
}

func probeLibopusSILKFixedApplySine(cases []silkFixedApplySineCase) ([][]int16, error) {
	binPath, err := buildLibopusSILKFixedApplySineHelper()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedApplySineWindowInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.I32(int32(tc.winType))
		payload.U32(uint32(len(tc.in)))
		for _, v := range tc.in {
			payload.I16(v)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed apply sine window", libopusSILKFixedApplySineWindowOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([][]int16, count)
	for i := range out {
		out[i] = make([]int16, len(cases[i].in))
		for j := range out[i] {
			out[i][j] = reader.I16()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKApplySineWindowFixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0x5A1E))

	randSignal := func(n int, amp int32) []int16 {
		x := make([]int16, n)
		for i := range x {
			x[i] = int16(rng.Int31n(2*amp+1) - amp)
		}
		return x
	}

	var cases []silkFixedApplySineCase

	// Sweep every legal length (multiple of 4 in [16, 120]) and both window
	// types, plus extreme amplitudes.
	for length := 16; length <= 120; length += 4 {
		for _, winType := range []int{1, 2} {
			cases = append(cases, silkFixedApplySineCase{
				name:    "sweep",
				winType: winType,
				in:      randSignal(length, 12000),
			})
			cases = append(cases, silkFixedApplySineCase{
				name:    "sweep-fullscale",
				winType: winType,
				in:      randSignal(length, 32767),
			})
		}
	}

	// Constant and alternating full-scale stress at boundary lengths.
	for _, length := range []int{16, 120} {
		full := make([]int16, length)
		for i := range full {
			if i%2 == 0 {
				full[i] = 32767
			} else {
				full[i] = -32768
			}
		}
		cases = append(cases,
			silkFixedApplySineCase{name: "alt-fullscale", winType: 1, in: full},
			silkFixedApplySineCase{name: "alt-fullscale", winType: 2, in: full},
		)
		zero := make([]int16, length)
		cases = append(cases,
			silkFixedApplySineCase{name: "zero", winType: 1, in: zero},
			silkFixedApplySineCase{name: "zero", winType: 2, in: zero},
		)
	}

	// Broad random bulk over valid lengths/types.
	for i := 0; i < 128; i++ {
		length := 16 + 4*rng.Intn((120-16)/4+1)
		winType := 1 + rng.Intn(2)
		cases = append(cases, silkFixedApplySineCase{
			name:    "rand-bulk",
			winType: winType,
			in:      randSignal(length, int32(1+rng.Intn(32767))),
		})
	}

	want, err := probeLibopusSILKFixedApplySine(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed apply sine window", err)
		return
	}

	for i, tc := range cases {
		got := make([]int16, len(tc.in))
		silkApplySineWindowFIX(got, tc.in, tc.winType, len(tc.in))
		for j := range got {
			if got[j] != want[i][j] {
				t.Fatalf("case %d (%s winType=%d len=%d): out[%d]=%d want %d",
					i, tc.name, tc.winType, len(tc.in), j, got[j], want[i][j])
			}
		}
	}
}
