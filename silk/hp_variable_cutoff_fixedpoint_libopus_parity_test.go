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
	libopusSILKHPBiquadInputMagic  = "HPBI"
	libopusSILKHPBiquadOutputMagic = "HPBO"

	hpBiquadModeStride1 = uint32(0)
	hpBiquadModeStride2 = uint32(1)
	hpBiquadModeHPVar   = uint32(2)
)

var (
	libopusSILKHPBiquadOnce sync.Once
	libopusSILKHPBiquadBin  string
	libopusSILKHPBiquadErr  error
)

// buildLibopusSILKHPBiquadHelper ensures the FIXED_POINT libopus reference
// exists, then compiles tools/csrc/libopus_silk_fixed_hp_biquad_info.c against
// it.
func buildLibopusSILKHPBiquadHelper() (string, error) {
	libopusSILKHPBiquadOnce.Do(func() {
		_, file, _, _ := runtime.Caller(0)
		repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), ".."))

		refDir := fixedRefPath()
		staticLib := fixedRefPath(".libs", "libopus.a")
		if _, err := os.Stat(staticLib); err != nil {
			cmd := exec.Command("bash", filepath.Join("tools", "ensure_libopus.sh"))
			cmd.Dir = repoRoot
			cmd.Env = append(os.Environ(), "LIBOPUS_ENABLE_FIXED=1")
			if out, berr := cmd.CombinedOutput(); berr != nil {
				libopusSILKHPBiquadErr = fmt.Errorf("ensure fixed libopus: %w (%s)", berr, out)
				return
			}
		}
		if _, err := os.Stat(staticLib); err != nil {
			libopusSILKHPBiquadErr = fmt.Errorf("fixed libopus static lib missing: %w", err)
			return
		}

		cc, err := libopustooling.FindCCompiler()
		if err != nil {
			libopusSILKHPBiquadErr = err
			return
		}

		src := filepath.Join(repoRoot, "tools", "csrc", "libopus_silk_fixed_hp_biquad_info.c")
		outDir := filepath.Join(os.TempDir(), "gopus_libopus_test_helpers")
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			libopusSILKHPBiquadErr = err
			return
		}
		out := filepath.Join(outDir, fmt.Sprintf("gopus_silk_fixed_hp_biquad_%s_%s", runtime.GOOS, runtime.GOARCH))

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
			libopusSILKHPBiquadErr = fmt.Errorf("build silk fixed hp biquad helper: %w (%s)", cerr, combined)
			return
		}
		libopusSILKHPBiquadBin = out
	})
	return libopusSILKHPBiquadBin, libopusSILKHPBiquadErr
}

type hpBiquadStrideCase struct {
	name   string
	stride int // 1 or 2
	length int // sample pairs (stride2) or samples (stride1)
	bQ28   [transitionNB]int32
	aQ28   [transitionNA]int32
	state  [4]int32 // stride1 uses [0:2], stride2 uses [0:4]
	in     []int16
}

type hpVarCase struct {
	name             string
	fsKHz            int32
	prevLag          int32
	qualityQ15       int32
	speechActivityQ8 int32
	smth1Q15         int32
}

// stride1 result: 2 state words + length output samples.
// stride2 result: 4 state words + 2*length output samples.
func probeLibopusSILKHPBiquad(strideCases []hpBiquadStrideCase, hpCases []hpVarCase) (strideState [][]int32, strideOut [][]int16, hpOut []int32, err error) {
	binPath, berr := buildLibopusSILKHPBiquadHelper()
	if berr != nil {
		return nil, nil, nil, berr
	}

	payload := libopustest.NewOraclePayload(libopusSILKHPBiquadInputMagic, uint32(len(strideCases)+len(hpCases)))
	for _, tc := range strideCases {
		if tc.stride == 2 {
			payload.U32(hpBiquadModeStride2)
		} else {
			payload.U32(hpBiquadModeStride1)
		}
		payload.I32(int32(tc.length))
		for _, v := range tc.bQ28 {
			payload.I32(v)
		}
		for _, v := range tc.aQ28 {
			payload.I32(v)
		}
		nstate := 2
		if tc.stride == 2 {
			nstate = 4
		}
		for i := 0; i < nstate; i++ {
			payload.I32(tc.state[i])
		}
		nsamp := tc.length
		if tc.stride == 2 {
			nsamp = 2 * tc.length
		}
		for i := 0; i < nsamp; i++ {
			payload.I32(int32(tc.in[i]))
		}
	}
	for _, tc := range hpCases {
		payload.U32(hpBiquadModeHPVar)
		payload.I32(tc.fsKHz)
		payload.I32(tc.prevLag)
		payload.I32(tc.qualityQ15)
		payload.I32(tc.speechActivityQ8)
		payload.I32(tc.smth1Q15)
	}

	reader, rerr := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed hp biquad", libopusSILKHPBiquadOutputMagic)
	if rerr != nil {
		return nil, nil, nil, rerr
	}
	reader.Count(len(strideCases) + len(hpCases))

	strideState = make([][]int32, len(strideCases))
	strideOut = make([][]int16, len(strideCases))
	for i, tc := range strideCases {
		nstate := 2
		nsamp := tc.length
		if tc.stride == 2 {
			nstate = 4
			nsamp = 2 * tc.length
		}
		st := make([]int32, nstate)
		for k := range st {
			st[k] = reader.I32()
		}
		strideState[i] = st
		o := make([]int16, nsamp)
		for k := range o {
			o[k] = int16(reader.I32())
		}
		strideOut[i] = o
	}
	hpOut = make([]int32, len(hpCases))
	for i := range hpOut {
		hpOut[i] = reader.I32()
	}
	if cerr := reader.ExpectConsumed(); cerr != nil {
		return nil, nil, nil, cerr
	}
	return strideState, strideOut, hpOut, nil
}

func TestSILKBiquadAltStride1And2FixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0x5e1f0b1a))

	// Coefficient sets: the LP transition tables, the adaptive HP cutoff
	// coefficients, plus random Q28 coefficients.
	type coefSet struct {
		name string
		b    [transitionNB]int32
		a    [transitionNA]int32
	}
	var coefSets []coefSet
	for i := 0; i < transitionIntNum; i++ {
		coefSets = append(coefSets, coefSet{
			name: fmt.Sprintf("lp%d", i),
			b:    silkTransitionLPBQ28[i],
			a:    silkTransitionLPAQ28[i],
		})
	}
	for _, fs := range []int32{8000, 12000, 16000, 24000, 48000} {
		for _, cut := range []int32{60, 80, 100} {
			b, a := HPCutoffCoefsQ28(cut, fs)
			coefSets = append(coefSets, coefSet{
				name: fmt.Sprintf("hp_fs%d_c%d", fs, cut),
				b:    b,
				a:    a,
			})
		}
	}
	for i := 0; i < 8; i++ {
		coefSets = append(coefSets, coefSet{
			name: fmt.Sprintf("rand%d", i),
			b:    [transitionNB]int32{rng.Int31() % (1 << 29), rng.Int31()%(1<<29) - (1 << 28), rng.Int31() % (1 << 29)},
			a:    [transitionNA]int32{rng.Int31() % (1 << 29), rng.Int31() % (1 << 29)},
		})
	}

	mkInput := func(n int, kind int) []int16 {
		out := make([]int16, n)
		for i := range out {
			switch kind {
			case 0: // random full range
				out[i] = int16(rng.Intn(65536) - 32768)
			case 1: // saturating extremes
				if rng.Intn(2) == 0 {
					out[i] = 32767
				} else {
					out[i] = -32768
				}
			case 2: // small values
				out[i] = int16(rng.Intn(65) - 32)
			default: // ramp / impulse
				if i == 0 {
					out[i] = 32767
				} else {
					out[i] = 0
				}
			}
		}
		return out
	}

	var strideCases []hpBiquadStrideCase
	add := func(name string, stride, length int, cs coefSet, state [4]int32, in []int16) {
		strideCases = append(strideCases, hpBiquadStrideCase{
			name:   name,
			stride: stride,
			length: length,
			bQ28:   cs.b,
			aQ28:   cs.a,
			state:  state,
			in:     in,
		})
	}

	lengths := []int{2, 4, 16, 80, 320, 960}
	for _, cs := range coefSets {
		for _, length := range lengths {
			for kind := 0; kind < 4; kind++ {
				// stride1
				var st1 [4]int32
				st1[0] = int32(rng.Intn(1<<20) - (1 << 19))
				st1[1] = int32(rng.Intn(1<<20) - (1 << 19))
				add(fmt.Sprintf("s1_%s_l%d_k%d", cs.name, length, kind), 1, length, cs, st1, mkInput(length, kind))

				// stride2 (interleaved, length sample pairs)
				var st2 [4]int32
				for j := range st2 {
					st2[j] = int32(rng.Intn(1<<20) - (1 << 19))
				}
				add(fmt.Sprintf("s2_%s_l%d_k%d", cs.name, length, kind), 2, length, cs, st2, mkInput(2*length, kind))
			}
		}
	}

	gotState, gotOut, _, err := probeLibopusSILKHPBiquad(strideCases, nil)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed hp biquad", err)
		return
	}

	for i, tc := range strideCases {
		if tc.stride == 1 {
			st := [2]int32{tc.state[0], tc.state[1]}
			out := make([]int16, tc.length)
			in := make([]int16, tc.length)
			copy(in, tc.in)
			silkBiquadAltStride1(in, tc.bQ28, tc.aQ28, &st, out, tc.length)

			wantState := gotState[i]
			if st[0] != wantState[0] || st[1] != wantState[1] {
				t.Fatalf("case %d (%s): stride1 state=%v want %v", i, tc.name, st, wantState)
			}
			for k := range out {
				if out[k] != gotOut[i][k] {
					t.Fatalf("case %d (%s): stride1 out[%d]=%d want %d", i, tc.name, k, out[k], gotOut[i][k])
				}
			}
		} else {
			st := tc.state
			out := make([]int16, 2*tc.length)
			in := make([]int16, 2*tc.length)
			copy(in, tc.in)
			silkBiquadAltStride2(in, tc.bQ28, tc.aQ28, &st, out, tc.length)

			wantState := gotState[i]
			for k := 0; k < 4; k++ {
				if st[k] != wantState[k] {
					t.Fatalf("case %d (%s): stride2 state[%d]=%d want %d", i, tc.name, k, st[k], wantState[k])
				}
			}
			for k := range out {
				if out[k] != gotOut[i][k] {
					t.Fatalf("case %d (%s): stride2 out[%d]=%d want %d", i, tc.name, k, out[k], gotOut[i][k])
				}
			}
		}
	}
}

func TestSILKHPVariableCutoffFixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0x9a3c771d))

	var cases []hpVarCase
	add := func(name string, fsKHz, prevLag, quality, activity, smth1 int32) {
		cases = append(cases, hpVarCase{
			name:             name,
			fsKHz:            fsKHz,
			prevLag:          prevLag,
			qualityQ15:       quality,
			speechActivityQ8: activity,
			smth1Q15:         smth1,
		})
	}

	// Init seed for variable_HP_smth1_Q15 from silk/init_encoder.c.
	initSmth1 := initVariableHPSmth1Q15()

	// Sweep cutoff/pitch-freq inputs across realistic ranges.
	for _, fsKHz := range []int32{8, 12, 16} {
		// prevLag spans the SILK pitch-lag search range (in samples at fs_kHz).
		for _, prevLag := range []int32{1, 16, 32, 64, 128, 256, 288, 512, 1000} {
			for _, quality := range []int32{0, 4096, 16384, 24576, 32767} {
				for _, activity := range []int32{0, 64, 128, 200, 256} {
					for _, smth1 := range []int32{0, initSmth1, initSmth1 + 5000, 1 << 20, 1 << 23} {
						add(fmt.Sprintf("fs%d_lag%d_q%d_a%d_s%d", fsKHz, prevLag, quality, activity, smth1),
							fsKHz, prevLag, quality, activity, smth1)
					}
				}
			}
		}
	}

	// Cross-frame state: chain the smoother through repeated updates so the
	// updated smth1 from one call feeds the next (mirrors per-packet use).
	for i := 0; i < 256; i++ {
		fsKHz := []int32{8, 12, 16}[rng.Intn(3)]
		prevLag := int32(rng.Intn(512) + 1)
		quality := int32(rng.Intn(32768))
		activity := int32(rng.Intn(257))
		smth1 := initSmth1 + int32(rng.Intn(1<<22)) - (1 << 21)
		add(fmt.Sprintf("rand%d", i), fsKHz, prevLag, quality, activity, smth1)
	}

	_, _, want, err := probeLibopusSILKHPBiquad(nil, cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed hp biquad", err)
		return
	}

	for i, tc := range cases {
		got := updateVariableHPSmth1Q15(tc.fsKHz, tc.prevLag, tc.qualityQ15, tc.speechActivityQ8, tc.smth1Q15)
		if got != want[i] {
			t.Fatalf("case %d (%s): smth1=%d want %d", i, tc.name, got, want[i])
		}
	}
}
