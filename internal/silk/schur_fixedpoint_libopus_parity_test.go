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
	libopusSILKFixedSchurInputMagic  = "GSCI"
	libopusSILKFixedSchurOutputMagic = "GSCO"
)

var (
	libopusSILKFixedSchurOnce sync.Once
	libopusSILKFixedSchurBin  string
	libopusSILKFixedSchurErr  error
)

// buildLibopusSILKFixedSchurHelper ensures the FIXED_POINT libopus reference
// exists, then compiles tools/csrc/libopus_silk_fixed_schur_info.c against it.
func buildLibopusSILKFixedSchurHelper() (string, error) {
	libopusSILKFixedSchurOnce.Do(func() {
		_, file, _, _ := runtime.Caller(0)
		repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))

		refDir := fixedRefPath()
		staticLib := fixedRefPath(".libs", "libopus.a")
		if _, err := os.Stat(staticLib); err != nil {
			cmd := exec.Command("bash", filepath.Join("tools", "ensure_libopus.sh"))
			cmd.Dir = repoRoot
			cmd.Env = append(os.Environ(), "LIBOPUS_ENABLE_FIXED=1")
			if out, berr := cmd.CombinedOutput(); berr != nil {
				libopusSILKFixedSchurErr = fmt.Errorf("ensure fixed libopus: %w (%s)", berr, out)
				return
			}
		}
		if _, err := os.Stat(staticLib); err != nil {
			libopusSILKFixedSchurErr = fmt.Errorf("fixed libopus static lib missing: %w", err)
			return
		}

		cc, err := libopustooling.FindCCompiler()
		if err != nil {
			libopusSILKFixedSchurErr = err
			return
		}

		src := filepath.Join(repoRoot, "tools", "csrc", "libopus_silk_fixed_schur_info.c")
		outDir := filepath.Join(os.TempDir(), "gopus_libopus_test_helpers")
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			libopusSILKFixedSchurErr = err
			return
		}
		out := filepath.Join(outDir, fmt.Sprintf("gopus_silk_fixed_schur_%s_%s", runtime.GOOS, runtime.GOARCH))

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
			libopusSILKFixedSchurErr = fmt.Errorf("build silk fixed schur helper: %w (%s)", cerr, combined)
			return
		}
		libopusSILKFixedSchurBin = out
	})
	return libopusSILKFixedSchurBin, libopusSILKFixedSchurErr
}

type silkFixedSchurCase struct {
	name  string
	order int
	c     []int32
}

type silkFixedSchurResult struct {
	res   int32
	rcQ15 []int32
	aQ24  []int32
}

func probeLibopusSILKFixedSchur(cases []silkFixedSchurCase) ([]silkFixedSchurResult, error) {
	binPath, err := buildLibopusSILKFixedSchurHelper()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedSchurInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.I32(int32(tc.order))
		for _, v := range tc.c {
			payload.I32(v)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed schur", libopusSILKFixedSchurOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]silkFixedSchurResult, count)
	for i := range out {
		out[i].res = reader.I32()
		out[i].rcQ15 = make([]int32, cases[i].order)
		for j := range out[i].rcQ15 {
			out[i].rcQ15[j] = reader.I32()
		}
		out[i].aQ24 = make([]int32, cases[i].order)
		for j := range out[i].aQ24 {
			out[i].aQ24[j] = reader.I32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

// makeAutocorr builds a valid autocorrelation vector c[0..order] from a random
// signal so silk_schur sees a positive-definite, properly conditioned input.
func makeAutocorr(rng *rand.Rand, order int, length int, amp int32) []int32 {
	x := make([]int64, length)
	for i := range x {
		x[i] = int64(rng.Int31n(2*amp+1) - amp)
	}
	c := make([]int32, order+1)
	for lag := 0; lag <= order; lag++ {
		var acc int64
		for i := lag; i < length; i++ {
			acc += x[i] * x[i-lag]
		}
		// Scale into a range comparable to libopus autocorr outputs and keep
		// within int32. c[0] must stay positive for a valid energy.
		for acc > (1<<30) || acc < -(1<<30) {
			acc >>= 1
		}
		if lag == 0 && acc <= 0 {
			acc = 1
		}
		c[lag] = int32(acc)
	}
	return c
}

func TestSILKSchurFixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0x5C40))

	var cases []silkFixedSchurCase

	// White-noise-like autocorrelations across the order sweep.
	for _, order := range []int{0, 1, 2, 4, 8, 10, 12, 16, 24} {
		cases = append(cases, silkFixedSchurCase{
			name:  "white",
			order: order,
			c:     makeAutocorr(rng, order, 400, 12000),
		})
	}

	// Tonal: strong correlation at successive lags via a low-frequency signal.
	tonal := func(order, length int) []int32 {
		x := make([]int64, length)
		for i := range x {
			// Slowly varying sinusoid-like ramp produces high lag correlation.
			v := int64((i % 17) - 8)
			x[i] = v * 1500
		}
		c := make([]int32, order+1)
		for lag := 0; lag <= order; lag++ {
			var acc int64
			for i := lag; i < length; i++ {
				acc += x[i] * x[i-lag]
			}
			for acc > (1<<30) || acc < -(1<<30) {
				acc >>= 1
			}
			if lag == 0 && acc <= 0 {
				acc = 1
			}
			c[lag] = int32(acc)
		}
		return c
	}
	for _, order := range []int{2, 6, 12, 16, 24} {
		cases = append(cases, silkFixedSchurCase{
			name:  "tonal",
			order: order,
			c:     tonal(order, 320),
		})
	}

	// Silence / near-silence exercises the leading-zero left-shift path and the
	// max(.,1) divisor guard.
	for _, order := range []int{2, 8, 16, 24} {
		c := make([]int32, order+1)
		c[0] = 1
		cases = append(cases, silkFixedSchurCase{
			name:  "silence",
			order: order,
			c:     c,
		})
	}
	for _, order := range []int{4, 16, 24} {
		c := make([]int32, order+1)
		c[0] = 64
		for i := 1; i <= order; i++ {
			c[i] = int32(rng.Int31n(33) - 16)
		}
		cases = append(cases, silkFixedSchurCase{
			name:  "near-silence",
			order: order,
			c:     c,
		})
	}

	// Saturation / unstable-rc: off-diagonal magnitude >= c[0] forces the 0.99
	// clamp and early break.
	for _, order := range []int{2, 8, 16, 24} {
		c := make([]int32, order+1)
		c[0] = 1 << 20
		for i := 1; i <= order; i++ {
			if i%2 == 0 {
				c[i] = 1 << 21 // exceeds c[0]
			} else {
				c[i] = -(1 << 21)
			}
		}
		cases = append(cases, silkFixedSchurCase{
			name:  "unstable-rc",
			order: order,
			c:     c,
		})
	}

	// Large-magnitude energy hitting the right-shift (lz<2) path.
	for _, order := range []int{4, 12, 24} {
		c := makeAutocorr(rng, order, 600, 30000)
		c[0] = 0x7FFFFFFF // lz==1 → shift-right branch
		cases = append(cases, silkFixedSchurCase{
			name:  "highenergy",
			order: order,
			c:     c,
		})
	}

	// Broad random bulk over valid autocorrelations.
	for i := 0; i < 128; i++ {
		order := rng.Intn(25) // 0..24
		cases = append(cases, silkFixedSchurCase{
			name:  "rand-bulk",
			order: order,
			c:     makeAutocorr(rng, order, 1+rng.Intn(800), int32(1+rng.Intn(32767))),
		})
	}

	want, err := probeLibopusSILKFixedSchur(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed schur", err)
		return
	}

	for i, tc := range cases {
		rc := make([]int16, tc.order)
		res := silkSchur(rc, tc.c, int32(tc.order))
		if res != want[i].res {
			t.Fatalf("case %d (%s order=%d): residual=%d want %d",
				i, tc.name, tc.order, res, want[i].res)
		}
		for j := range rc {
			if int32(rc[j]) != want[i].rcQ15[j] {
				t.Fatalf("case %d (%s order=%d): rc_Q15[%d]=%d want %d",
					i, tc.name, tc.order, j, rc[j], want[i].rcQ15[j])
			}
		}

		a := make([]int32, tc.order)
		silkK2a(a, rc, int32(tc.order))
		for j := range a {
			if a[j] != want[i].aQ24[j] {
				t.Fatalf("case %d (%s order=%d): A_Q24[%d]=%d want %d",
					i, tc.name, tc.order, j, a[j], want[i].aQ24[j])
			}
		}
	}
}
