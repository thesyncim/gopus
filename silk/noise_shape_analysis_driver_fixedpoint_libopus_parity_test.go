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
	libopusSILKFixedNSAInputMagic  = "GNSI"
	libopusSILKFixedNSAOutputMagic = "GNSO"
)

var (
	libopusSILKFixedNSAOnce sync.Once
	libopusSILKFixedNSABin  string
	libopusSILKFixedNSAErr  error
)

// buildLibopusSILKFixedNSAHelper ensures the FIXED_POINT libopus reference
// exists, then compiles
// tools/csrc/libopus_silk_fixed_noise_shape_analysis_info.c against it.
func buildLibopusSILKFixedNSAHelper() (string, error) {
	libopusSILKFixedNSAOnce.Do(func() {
		_, file, _, _ := runtime.Caller(0)
		repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), ".."))

		refDir := fixedRefPath()
		staticLib := fixedRefPath(".libs", "libopus.a")
		if _, err := os.Stat(staticLib); err != nil {
			cmd := exec.Command("bash", filepath.Join("tools", "ensure_libopus.sh"))
			cmd.Dir = repoRoot
			cmd.Env = append(os.Environ(), "LIBOPUS_ENABLE_FIXED=1")
			if out, berr := cmd.CombinedOutput(); berr != nil {
				libopusSILKFixedNSAErr = fmt.Errorf("ensure fixed libopus: %w (%s)", berr, out)
				return
			}
		}
		if _, err := os.Stat(staticLib); err != nil {
			libopusSILKFixedNSAErr = fmt.Errorf("fixed libopus static lib missing: %w", err)
			return
		}

		cc, err := libopustooling.FindCCompiler()
		if err != nil {
			libopusSILKFixedNSAErr = err
			return
		}

		src := filepath.Join(repoRoot, "tools", "csrc", "libopus_silk_fixed_noise_shape_analysis_info.c")
		outDir := filepath.Join(os.TempDir(), "gopus_libopus_test_helpers")
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			libopusSILKFixedNSAErr = err
			return
		}
		out := filepath.Join(outDir, fmt.Sprintf("gopus_silk_fixed_nsa_%s_%s", runtime.GOOS, runtime.GOARCH))

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
			libopusSILKFixedNSAErr = fmt.Errorf("build silk fixed nsa helper: %w (%s)", cerr, combined)
			return
		}
		libopusSILKFixedNSABin = out
	})
	return libopusSILKFixedNSABin, libopusSILKFixedNSAErr
}

type silkFixedNSAResult struct {
	inputQualityQ14      int32
	codingQualityQ14     int32
	quantOffsetType      int32
	gainsQ16             [maxNbSubfr]int32
	arQ13                [maxNbSubfr * maxShapeLpcOrder]int32
	lfShpQ14             [maxNbSubfr]int32
	tiltQ14              [maxNbSubfr]int32
	harmShapeGainQ14     [maxNbSubfr]int32
	harmShapeGainSmthQ16 int32
	tiltSmthQ16          int32
}

func probeLibopusSILKFixedNSA(cases []silkNoiseShapeAnalysisInput) ([]silkFixedNSAResult, error) {
	binPath, err := buildLibopusSILKFixedNSAHelper()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedNSAInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.I32(int32(tc.laShape))
		payload.I32(tc.snrDBQ7)
		payload.I32(tc.inputQualityBandsQ15[0])
		payload.I32(tc.inputQualityBandsQ15[1])
		payload.I32(int32(tc.useCBR))
		payload.I32(tc.speechActivityQ8)
		payload.I32(int32(tc.signalType))
		payload.I32(int32(tc.fsKHz))
		payload.I32(int32(tc.nbSubfr))
		payload.I32(int32(tc.subfrLength))
		payload.I32(tc.warpingQ16)
		payload.I32(int32(tc.shapeWinLength))
		payload.I32(int32(tc.shapingLPCOrder))
		payload.I32(tc.ltpCorrQ15)
		payload.I32(tc.predGainQ16)
		for i := 0; i < maxNbSubfr; i++ {
			payload.I32(int32(tc.pitchL[i]))
		}
		payload.I32(tc.harmShapeGainSmthQ16)
		payload.I32(tc.tiltSmthQ16)
		payload.U32(uint32(len(tc.pitchRes)))
		payload.U32(uint32(len(tc.x)))
		for _, v := range tc.pitchRes {
			payload.I16(v)
		}
		for _, v := range tc.x {
			payload.I16(v)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed noise shape analysis", libopusSILKFixedNSAOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]silkFixedNSAResult, count)
	for i := range out {
		out[i].inputQualityQ14 = reader.I32()
		out[i].codingQualityQ14 = reader.I32()
		out[i].quantOffsetType = reader.I32()
		for j := range out[i].gainsQ16 {
			out[i].gainsQ16[j] = reader.I32()
		}
		for j := range out[i].arQ13 {
			out[i].arQ13[j] = reader.I32()
		}
		for j := range out[i].lfShpQ14 {
			out[i].lfShpQ14[j] = reader.I32()
		}
		for j := range out[i].tiltQ14 {
			out[i].tiltQ14[j] = reader.I32()
		}
		for j := range out[i].harmShapeGainQ14 {
			out[i].harmShapeGainQ14[j] = reader.I32()
		}
		out[i].harmShapeGainSmthQ16 = reader.I32()
		out[i].tiltSmthQ16 = reader.I32()
	}
	if err := reader.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKNoiseShapeAnalysisFixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0x4E5A))

	randSignal := func(n int, amp int32) []int16 {
		x := make([]int16, n)
		for i := range x {
			x[i] = int16(rng.Int31n(2*amp+1) - amp)
		}
		return x
	}

	// makeCase builds a self-consistent driver input for the given config.
	makeCase := func(fsKHz, nbSubfr, shapingLPCOrder, laShapeMul, signalType int, warped, cbr bool, amp int32) silkNoiseShapeAnalysisInput {
		subfrLength := 5 * fsKHz
		laShape := laShapeMul * fsKHz
		shapeWinLength := subfrLength + 2*laShape
		var warpingQ16 int32
		if warped {
			warpingQ16 = int32(fsKHz) * int32(silkFixConst(0.015, 16)) // WARPING_MULTIPLIER
		}
		// x buffer (starting at x - la_shape): last block needs shapeWinLength.
		xLen := (nbSubfr-1)*subfrLength + shapeWinLength
		// pitch_res for the unvoiced sparseness scan.
		nSamples := 2 * fsKHz
		nSegs := (5 * nbSubfr) / 2
		resLen := nSegs * nSamples
		if resLen < 1 {
			resLen = 1
		}
		useCBR := 0
		if cbr {
			useCBR = 1
		}
		in := silkNoiseShapeAnalysisInput{
			laShape:              laShape,
			snrDBQ7:              int32(20+rng.Intn(40)) << 7,
			inputQualityBandsQ15: [2]int32{int32(rng.Intn(32768)), int32(rng.Intn(32768))},
			useCBR:               useCBR,
			speechActivityQ8:     int32(rng.Intn(257)),
			signalType:           signalType,
			fsKHz:                fsKHz,
			nbSubfr:              nbSubfr,
			subfrLength:          subfrLength,
			warpingQ16:           warpingQ16,
			shapeWinLength:       shapeWinLength,
			shapingLPCOrder:      shapingLPCOrder,
			ltpCorrQ15:           int32(rng.Intn(32768)),
			predGainQ16:          int32(rng.Intn(1 << 22)),
			harmShapeGainSmthQ16: int32(rng.Intn(1 << 16)),
			tiltSmthQ16:          -int32(rng.Intn(1 << 16)),
			pitchRes:             randSignal(resLen, amp),
			x:                    randSignal(xLen, amp),
		}
		for k := 0; k < nbSubfr; k++ {
			in.pitchL[k] = int32(40 + rng.Intn(280))
		}
		return in
	}

	var cases []silkNoiseShapeAnalysisInput

	fsOpts := []int{8, 12, 16}
	for _, fs := range fsOpts {
		for _, nb := range []int{2, 4} {
			for _, order := range []int{12, 16, 24} {
				for _, sig := range []int{typeVoiced, 1 /*unvoiced*/} {
					for _, warped := range []bool{true, false} {
						for _, cbr := range []bool{false, true} {
							cases = append(cases, makeCase(fs, nb, order, 5, sig, warped, cbr, 8000))
						}
					}
				}
			}
		}
	}

	// 3*fs_kHz look-ahead variants and quiet/loud signals.
	cases = append(cases, makeCase(16, 4, 16, 3, typeVoiced, true, false, 32767))
	cases = append(cases, makeCase(16, 4, 16, 3, 1, false, false, 50))
	cases = append(cases, makeCase(8, 4, 24, 3, typeVoiced, true, true, 1))

	want, err := probeLibopusSILKFixedNSA(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed noise shape analysis", err)
		return
	}

	for i := range cases {
		in := cases[i] // copy: driver mutates smoothing accumulators
		got := silkNoiseShapeAnalysisFIX(&in)
		w := want[i]

		fail := func(field string, g, e interface{}) {
			t.Fatalf("case %d (fs=%d nb=%d order=%d sig=%d warp=%d cbr=%d): %s got %v want %v",
				i, cases[i].fsKHz, cases[i].nbSubfr, cases[i].shapingLPCOrder, cases[i].signalType,
				cases[i].warpingQ16, cases[i].useCBR, field, g, e)
		}

		if got.inputQualityQ14 != w.inputQualityQ14 {
			fail("inputQualityQ14", got.inputQualityQ14, w.inputQualityQ14)
		}
		if got.codingQualityQ14 != w.codingQualityQ14 {
			fail("codingQualityQ14", got.codingQualityQ14, w.codingQualityQ14)
		}
		if int32(got.quantOffsetType) != w.quantOffsetType {
			fail("quantOffsetType", got.quantOffsetType, w.quantOffsetType)
		}
		for k := 0; k < cases[i].nbSubfr; k++ {
			if got.gainsQ16[k] != w.gainsQ16[k] {
				fail(fmt.Sprintf("Gains_Q16[%d]", k), got.gainsQ16[k], w.gainsQ16[k])
			}
		}
		for k := 0; k < cases[i].nbSubfr; k++ {
			for j := 0; j < cases[i].shapingLPCOrder; j++ {
				idx := k*maxShapeLpcOrder + j
				if int32(got.arQ13[idx]) != w.arQ13[idx] {
					fail(fmt.Sprintf("AR_Q13[%d][%d]", k, j), got.arQ13[idx], w.arQ13[idx])
				}
			}
		}
		for k := 0; k < cases[i].nbSubfr; k++ {
			if got.lfShpQ14[k] != w.lfShpQ14[k] {
				fail(fmt.Sprintf("LF_shp_Q14[%d]", k), got.lfShpQ14[k], w.lfShpQ14[k])
			}
		}
		for k := 0; k < maxNbSubfr; k++ {
			if got.tiltQ14[k] != w.tiltQ14[k] {
				fail(fmt.Sprintf("Tilt_Q14[%d]", k), got.tiltQ14[k], w.tiltQ14[k])
			}
			if got.harmShapeGainQ14[k] != w.harmShapeGainQ14[k] {
				fail(fmt.Sprintf("HarmShapeGain_Q14[%d]", k), got.harmShapeGainQ14[k], w.harmShapeGainQ14[k])
			}
		}
		if got.harmShapeGainSmthQ16 != w.harmShapeGainSmthQ16 {
			fail("HarmShapeGain_smth_Q16", got.harmShapeGainSmthQ16, w.harmShapeGainSmthQ16)
		}
		if got.tiltSmthQ16 != w.tiltSmthQ16 {
			fail("Tilt_smth_Q16", got.tiltSmthQ16, w.tiltSmthQ16)
		}
	}
}
