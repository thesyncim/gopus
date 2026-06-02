//go:build gopus_fixedpoint

package silk

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

const (
	libopusSILKProcessNLSFsInputMagic  = "PNLI"
	libopusSILKProcessNLSFsOutputMagic = "PNLO"
)

var (
	libopusSILKProcessNLSFsOnce sync.Once
	libopusSILKProcessNLSFsBin  string
	libopusSILKProcessNLSFsErr  error
)

// buildLibopusSILKProcessNLSFsHelper ensures the FIXED_POINT libopus reference
// exists, then compiles tools/csrc/libopus_silk_process_nlsfs_info.c against it.
func buildLibopusSILKProcessNLSFsHelper() (string, error) {
	libopusSILKProcessNLSFsOnce.Do(func() {
		_, file, _, _ := runtime.Caller(0)
		repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), ".."))

		refDir := fixedRefPath()
		staticLib := fixedRefPath(".libs", "libopus.a")
		if _, err := os.Stat(staticLib); err != nil {
			cmd := exec.Command("bash", filepath.Join("tools", "ensure_libopus.sh"))
			cmd.Dir = repoRoot
			cmd.Env = append(os.Environ(), "LIBOPUS_ENABLE_FIXED=1")
			if out, berr := cmd.CombinedOutput(); berr != nil {
				libopusSILKProcessNLSFsErr = fmt.Errorf("ensure fixed libopus: %w (%s)", berr, out)
				return
			}
		}
		if _, err := os.Stat(staticLib); err != nil {
			libopusSILKProcessNLSFsErr = fmt.Errorf("fixed libopus static lib missing: %w", err)
			return
		}

		cc, err := libopustooling.FindCCompiler()
		if err != nil {
			libopusSILKProcessNLSFsErr = err
			return
		}

		src := filepath.Join(repoRoot, "tools", "csrc", "libopus_silk_process_nlsfs_info.c")
		outDir := filepath.Join(os.TempDir(), "gopus_libopus_test_helpers")
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			libopusSILKProcessNLSFsErr = err
			return
		}
		out := filepath.Join(outDir, fmt.Sprintf("gopus_silk_process_nlsfs_%s_%s", runtime.GOOS, runtime.GOARCH))

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
			libopusSILKProcessNLSFsErr = fmt.Errorf("build silk process nlsfs helper: %w (%s)", cerr, combined)
			return
		}
		libopusSILKProcessNLSFsBin = out
	})
	return libopusSILKProcessNLSFsBin, libopusSILKProcessNLSFsErr
}

type silkProcessNLSFsCase struct {
	name              string
	order             int
	nbSubfr           int
	signalType        int32
	useInterp         int32
	nlsfMSVQSurvivors int
	speechActivityQ8  int32
	nlsfInterpCoefQ2  int32
	nlsfQ15           []int16
	prevNLSFQ15       []int16
}

type silkProcessNLSFsResultWire struct {
	predCoef0   []int32
	predCoef1   []int32
	nlsfIndices []int32
	nlsfQ15     []int32
}

func probeLibopusSILKProcessNLSFs(cases []silkProcessNLSFsCase) ([]silkProcessNLSFsResultWire, error) {
	binPath, err := buildLibopusSILKProcessNLSFsHelper()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKProcessNLSFsInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.order))
		payload.U32(uint32(tc.nbSubfr))
		payload.U32(uint32(tc.signalType))
		payload.U32(uint32(tc.useInterp))
		payload.U32(uint32(tc.nlsfMSVQSurvivors))
		payload.I32(tc.speechActivityQ8)
		payload.I32(tc.nlsfInterpCoefQ2)
		for i := 0; i < tc.order; i++ {
			payload.I32(int32(tc.nlsfQ15[i]))
		}
		for i := 0; i < tc.order; i++ {
			payload.I32(int32(tc.prevNLSFQ15[i]))
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk process nlsfs", libopusSILKProcessNLSFsOutputMagic)
	if err != nil {
		return nil, err
	}
	cnt := reader.Count(len(cases))
	out := make([]silkProcessNLSFsResultWire, cnt)
	for i := range out {
		order := cases[i].order
		out[i].predCoef0 = make([]int32, order)
		for k := range out[i].predCoef0 {
			out[i].predCoef0[k] = reader.I32()
		}
		out[i].predCoef1 = make([]int32, order)
		for k := range out[i].predCoef1 {
			out[i].predCoef1[k] = reader.I32()
		}
		out[i].nlsfIndices = make([]int32, order+1)
		for k := range out[i].nlsfIndices {
			out[i].nlsfIndices[k] = reader.I32()
		}
		out[i].nlsfQ15 = make([]int32, order)
		for k := range out[i].nlsfQ15 {
			out[i].nlsfQ15[k] = reader.I32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

// makeStableNLSF produces a monotonically increasing NLSF_Q15 vector with a
// generous minimum spacing so the encoder's stabilizer does not have to clamp.
func makeStableNLSF(rng *rand.Rand, order int) []int16 {
	out := make([]int16, order)
	// Distribute roughly evenly across [0, 32768) with jitter.
	step := 32768 / (order + 1)
	pos := 0
	for i := 0; i < order; i++ {
		pos += step
		jitter := rng.Intn(step/2+1) - step/4
		v := pos + jitter
		if v < 1 {
			v = 1
		}
		if v > 32767 {
			v = 32767
		}
		out[i] = int16(v)
	}
	sortInt16(out)
	return out
}

// makeMarginalNLSF produces a vector with tight / out-of-order spacing to
// exercise the stabilizer path inside silk_NLSF_encode.
func makeMarginalNLSF(rng *rand.Rand, order int) []int16 {
	out := make([]int16, order)
	for i := 0; i < order; i++ {
		out[i] = int16(rng.Intn(32768))
	}
	// Cluster a couple of entries to force tiny deltas.
	if order >= 4 {
		out[1] = out[0]
		out[3] = out[2] + int16(rng.Intn(3))
	}
	return out
}

func sortInt16(a []int16) {
	sort.Slice(a, func(i, j int) bool { return a[i] < a[j] })
}

func TestSILKProcessNLSFsFixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0x5c1a7))

	var cases []silkProcessNLSFsCase

	survivorsFor := func(order int) int {
		if order == 16 {
			return 16
		}
		return 8
	}

	addCase := func(name string, order int, nbSubfr int, signalType, useInterp, interpCoef int32, marginal bool) {
		var nlsf []int16
		if marginal {
			nlsf = makeMarginalNLSF(rng, order)
		} else {
			nlsf = makeStableNLSF(rng, order)
		}
		prev := makeStableNLSF(rng, order)
		cases = append(cases, silkProcessNLSFsCase{
			name:              name,
			order:             order,
			nbSubfr:           nbSubfr,
			signalType:        signalType,
			useInterp:         useInterp,
			nlsfMSVQSurvivors: survivorsFor(order),
			speechActivityQ8:  int32(rng.Intn(257)),
			nlsfInterpCoefQ2:  interpCoef,
			nlsfQ15:           nlsf,
			prevNLSFQ15:       prev,
		})
	}

	signalTypes := []int32{typeNoVoiceActivity, typeUnvoiced, typeVoiced}
	orders := []int{10, 16}

	// Interpolation off (useInterp=0, coef forced to 4 == no interpolation) and
	// on (useInterp=1, coef 0..3), across orders, subframe counts, signal types,
	// and both stable and marginal NLSF sets.
	for _, order := range orders {
		for _, st := range signalTypes {
			for _, nb := range []int{2, 4} {
				// No interpolation: coef must be 4 when nb_subfr==4 / useInterp==0.
				addCase("nointerp", order, nb, st, 0, 4, false)
				addCase("nointerp-marg", order, nb, st, 0, 4, true)
				if nb == 4 {
					for _, coef := range []int32{0, 1, 2, 3} {
						addCase("interp", order, nb, st, 1, coef, false)
						addCase("interp-marg", order, nb, st, 1, coef, true)
					}
				}
			}
		}
	}

	// Bulk random coverage.
	for i := 0; i < 200; i++ {
		order := orders[rng.Intn(len(orders))]
		st := signalTypes[rng.Intn(len(signalTypes))]
		nb := 2 + 2*rng.Intn(2)
		useInterp := int32(0)
		coef := int32(4)
		if nb == 4 && rng.Intn(2) == 0 {
			useInterp = 1
			coef = int32(rng.Intn(4))
		}
		addCase("bulk", order, nb, st, useInterp, coef, rng.Intn(3) == 0)
	}

	want, err := probeLibopusSILKProcessNLSFs(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk process nlsfs", err)
		return
	}

	for i, tc := range cases {
		nlsf := make([]int16, tc.order)
		copy(nlsf, tc.nlsfQ15)
		prev := make([]int16, tc.order)
		copy(prev, tc.prevNLSFQ15)

		var cb *nlsfCB
		if tc.order == 16 {
			cb = &silk_NLSF_CB_WB
		} else {
			cb = &silk_NLSF_CB_NB_MB
		}

		var enc Encoder
		p := silkProcessNLSFsParams{
			predictLPCOrder:      tc.order,
			nbSubfr:              tc.nbSubfr,
			speechActivityQ8:     tc.speechActivityQ8,
			signalType:           tc.signalType,
			useInterpolatedNLSFs: tc.useInterp,
			nlsfInterpCoefQ2:     tc.nlsfInterpCoefQ2,
			nlsfMSVQSurvivors:    tc.nlsfMSVQSurvivors,
			cb:                   cb,
			nlsfQ15:              nlsf,
			prevNLSFQ15:          prev,
		}
		res := enc.silkProcessNLSFsFixed(&silkFixedEncodeScratch{}, &p)

		fail := func(field string, got, exp interface{}) {
			t.Fatalf("case %d (%s order=%d nb=%d st=%d useInterp=%d coef=%d): %s=%v want %v",
				i, tc.name, tc.order, tc.nbSubfr, tc.signalType, tc.useInterp, tc.nlsfInterpCoefQ2, field, got, exp)
		}

		for k := 0; k < tc.order; k++ {
			if int32(res.predCoefQ12[0][k]) != want[i].predCoef0[k] {
				fail(fmt.Sprintf("PredCoef_Q12[0][%d]", k), res.predCoefQ12[0][k], want[i].predCoef0[k])
			}
			if int32(res.predCoefQ12[1][k]) != want[i].predCoef1[k] {
				fail(fmt.Sprintf("PredCoef_Q12[1][%d]", k), res.predCoefQ12[1][k], want[i].predCoef1[k])
			}
			if int32(nlsf[k]) != want[i].nlsfQ15[k] {
				fail(fmt.Sprintf("pNLSF_Q15[%d]", k), nlsf[k], want[i].nlsfQ15[k])
			}
		}
		for k := 0; k < tc.order+1; k++ {
			if int32(res.nlsfIndices[k]) != want[i].nlsfIndices[k] {
				fail(fmt.Sprintf("NLSFIndices[%d]", k), res.nlsfIndices[k], want[i].nlsfIndices[k])
			}
		}
	}
}
