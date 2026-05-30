//go:build gopus_fixedpoint

package silk

import (
	"fmt"
	"math"
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
	libopusSILKFixedPitchCorrInputMagic  = "GFCI"
	libopusSILKFixedPitchCorrOutputMagic = "GFCO"
)

var (
	libopusSILKFixedPitchCorrOnce sync.Once
	libopusSILKFixedPitchCorrBin  string
	libopusSILKFixedPitchCorrErr  error
)

// buildLibopusSILKFixedPitchCorrHelper ensures the FIXED_POINT libopus
// reference exists, then compiles
// tools/csrc/libopus_silk_fixed_pitch_corr_st3_info.c against it.
func buildLibopusSILKFixedPitchCorrHelper() (string, error) {
	libopusSILKFixedPitchCorrOnce.Do(func() {
		_, file, _, _ := runtime.Caller(0)
		repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), ".."))

		refDir := fixedRefPath()
		staticLib := fixedRefPath(".libs", "libopus.a")
		if _, err := os.Stat(staticLib); err != nil {
			cmd := exec.Command("bash", filepath.Join("tools", "ensure_libopus.sh"))
			cmd.Dir = repoRoot
			cmd.Env = append(os.Environ(), "LIBOPUS_ENABLE_FIXED=1")
			if out, berr := cmd.CombinedOutput(); berr != nil {
				libopusSILKFixedPitchCorrErr = fmt.Errorf("ensure fixed libopus: %w (%s)", berr, out)
				return
			}
		}
		if _, err := os.Stat(staticLib); err != nil {
			libopusSILKFixedPitchCorrErr = fmt.Errorf("fixed libopus static lib missing: %w", err)
			return
		}

		cc, err := libopustooling.FindCCompiler()
		if err != nil {
			libopusSILKFixedPitchCorrErr = err
			return
		}

		src := filepath.Join(repoRoot, "tools", "csrc", "libopus_silk_fixed_pitch_corr_st3_info.c")
		outDir := filepath.Join(os.TempDir(), "gopus_libopus_test_helpers")
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			libopusSILKFixedPitchCorrErr = err
			return
		}
		out := filepath.Join(outDir, fmt.Sprintf("gopus_silk_fixed_pitch_corr_%s_%s", runtime.GOOS, runtime.GOARCH))

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
			libopusSILKFixedPitchCorrErr = fmt.Errorf("build silk fixed pitch corr helper: %w (%s)", cerr, combined)
			return
		}
		libopusSILKFixedPitchCorrBin = out
	})
	return libopusSILKFixedPitchCorrBin, libopusSILKFixedPitchCorrErr
}

type silkFixedPitchCorrCase struct {
	name       string
	fsKHz      int
	nbSubfr    int
	complexity int
	startLag   int
	frame      []int16
}

func (tc silkFixedPitchCorrCase) sfLength() int { return peSubfrLengthMS * tc.fsKHz }

func (tc silkFixedPitchCorrCase) nbCbkSearch() int {
	if tc.nbSubfr == peMaxNbSubfr {
		return pitchNbCbkSearchsStage3[tc.complexity]
	}
	return peNbCbksStage310ms
}

func probeLibopusSILKFixedPitchCorr(cases []silkFixedPitchCorrCase) ([][]int32, error) {
	binPath, err := buildLibopusSILKFixedPitchCorrHelper()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedPitchCorrInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.startLag))
		payload.U32(uint32(tc.sfLength()))
		payload.U32(uint32(tc.nbSubfr))
		payload.U32(uint32(tc.complexity))
		payload.U32(uint32(len(tc.frame)))
		for _, v := range tc.frame {
			payload.I16(v)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed pitch corr", libopusSILKFixedPitchCorrOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([][]int32, count)
	for i := range out {
		total := cases[i].nbSubfr * cases[i].nbCbkSearch() * peNbStage3Lags
		out[i] = make([]int32, total)
		for j := range out[i] {
			out[i][j] = reader.I32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKPitchAnalysisCorrSt3FixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0x5c0331))

	frameLen := func(fsKHz, nbSubfr int) int {
		return (peLTPMemLengthMS + nbSubfr*peSubfrLengthMS) * fsKHz
	}

	// Smooth periodic content keeps correlations well-behaved while exercising
	// the celt_pitch_xcorr lag sweep across all subframes.
	periodFrame := func(fsKHz, nbSubfr, period int, amp float64) []int16 {
		n := frameLen(fsKHz, nbSubfr)
		f := make([]int16, n)
		for i := range f {
			v := amp * (math.Sin(2*math.Pi*float64(i%period)/float64(period)) +
				0.2*math.Sin(4*math.Pi*float64(i%period)/float64(period)+0.3))
			f[i] = int16(math.Round(v))
		}
		return f
	}

	randFrame := func(fsKHz, nbSubfr int, amp int32) []int16 {
		n := frameLen(fsKHz, nbSubfr)
		f := make([]int16, n)
		for i := range f {
			f[i] = int16(rng.Int31n(2*amp+1) - amp)
		}
		return f
	}

	var cases []silkFixedPitchCorrCase
	for _, fsKHz := range []int{8, 12, 16} {
		minLag := peMinLagMS * fsKHz
		maxLag := peMaxLagMS*fsKHz - 1
		for _, nbSubfr := range []int{peMaxNbSubfr, peMaxNbSubfr >> 1} {
			maxCx := SILK_PE_MAX_COMPLEX
			if nbSubfr != peMaxNbSubfr {
				maxCx = 0 // complexity unused for 10ms tables; one pass is enough
			}
			for cx := 0; cx <= maxCx; cx++ {
				for _, startLag := range []int{minLag, (minLag + maxLag) / 2, maxLag - 2} {
					cases = append(cases, silkFixedPitchCorrCase{
						name:       fmt.Sprintf("period_fs%d_sf%d_cx%d_lag%d", fsKHz, nbSubfr, cx, startLag),
						fsKHz:      fsKHz,
						nbSubfr:    nbSubfr,
						complexity: cx,
						startLag:   startLag,
						frame:      periodFrame(fsKHz, nbSubfr, 3*fsKHz, 9000),
					})
					cases = append(cases, silkFixedPitchCorrCase{
						name:       fmt.Sprintf("rand_fs%d_sf%d_cx%d_lag%d", fsKHz, nbSubfr, cx, startLag),
						fsKHz:      fsKHz,
						nbSubfr:    nbSubfr,
						complexity: cx,
						startLag:   startLag,
						frame:      randFrame(fsKHz, nbSubfr, 30000),
					})
				}
			}
		}
	}

	// Bulk randomized coverage spanning the full lag range.
	for i := 0; i < 64; i++ {
		fsKHz := []int{8, 12, 16}[rng.Intn(3)]
		nbSubfr := peMaxNbSubfr
		cx := rng.Intn(SILK_PE_MAX_COMPLEX + 1)
		if rng.Intn(2) == 0 {
			nbSubfr = peMaxNbSubfr >> 1
			cx = 0
		}
		minLag := peMinLagMS * fsKHz
		maxLag := peMaxLagMS*fsKHz - 1
		startLag := minLag + rng.Intn(maxLag-minLag-1)
		cases = append(cases, silkFixedPitchCorrCase{
			name:       fmt.Sprintf("bulk_%d", i),
			fsKHz:      fsKHz,
			nbSubfr:    nbSubfr,
			complexity: cx,
			startLag:   startLag,
			frame:      randFrame(fsKHz, nbSubfr, int32(1+rng.Intn(32767))),
		})
	}

	want, err := probeLibopusSILKFixedPitchCorr(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed pitch corr", err)
		return
	}

	for i, tc := range cases {
		nbCbk := tc.nbCbkSearch()
		got := make([][peNbStage3Lags]int32, tc.nbSubfr*nbCbk)
		silkPAnaCalcCorrSt3Fixed(got, tc.frame, tc.startLag, tc.sfLength(), tc.nbSubfr, tc.complexity)

		flat := make([]int32, 0, len(got)*peNbStage3Lags)
		for _, row := range got {
			flat = append(flat, row[:]...)
		}
		if len(flat) != len(want[i]) {
			t.Fatalf("case %d (%s): got %d values want %d", i, tc.name, len(flat), len(want[i]))
		}
		for j := range flat {
			if flat[j] != want[i][j] {
				t.Fatalf("case %d (%s fs=%d nbSubfr=%d cx=%d startLag=%d): corr[%d]=%d want %d",
					i, tc.name, tc.fsKHz, tc.nbSubfr, tc.complexity, tc.startLag, j, flat[j], want[i][j])
			}
		}
	}
}
