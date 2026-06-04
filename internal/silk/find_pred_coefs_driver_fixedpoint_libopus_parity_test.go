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
	libopusSILKFixedFindPredInputMagic  = "GFPI"
	libopusSILKFixedFindPredOutputMagic = "GFPO"
)

var (
	libopusSILKFixedFindPredOnce sync.Once
	libopusSILKFixedFindPredBin  string
	libopusSILKFixedFindPredErr  error
)

// buildLibopusSILKFixedFindPredHelper ensures the FIXED_POINT libopus reference
// exists, then compiles tools/csrc/libopus_silk_fixed_find_pred_coefs_info.c
// against it.
func buildLibopusSILKFixedFindPredHelper() (string, error) {
	libopusSILKFixedFindPredOnce.Do(func() {
		_, file, _, _ := runtime.Caller(0)
		repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))

		refDir := fixedRefPath()
		staticLib := fixedRefPath(".libs", "libopus.a")
		if _, err := os.Stat(staticLib); err != nil {
			cmd := exec.Command("bash", filepath.Join("tools", "ensure_libopus.sh"))
			cmd.Dir = repoRoot
			cmd.Env = append(os.Environ(), "LIBOPUS_ENABLE_FIXED=1")
			if out, berr := cmd.CombinedOutput(); berr != nil {
				libopusSILKFixedFindPredErr = fmt.Errorf("ensure fixed libopus: %w (%s)", berr, out)
				return
			}
		}
		if _, err := os.Stat(staticLib); err != nil {
			libopusSILKFixedFindPredErr = fmt.Errorf("fixed libopus static lib missing: %w", err)
			return
		}

		cc, err := libopustooling.FindCCompiler()
		if err != nil {
			libopusSILKFixedFindPredErr = err
			return
		}

		src := filepath.Join(repoRoot, "tools", "csrc", "libopus_silk_fixed_find_pred_coefs_info.c")
		outDir := filepath.Join(os.TempDir(), "gopus_libopus_test_helpers")
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			libopusSILKFixedFindPredErr = err
			return
		}
		out := filepath.Join(outDir, fmt.Sprintf("gopus_silk_fixed_find_pred_%s_%s", runtime.GOOS, runtime.GOARCH))

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
			libopusSILKFixedFindPredErr = fmt.Errorf("build silk fixed find_pred_coefs helper: %w (%s)", cerr, combined)
			return
		}
		libopusSILKFixedFindPredBin = out
	})
	return libopusSILKFixedFindPredBin, libopusSILKFixedFindPredErr
}

type silkFixedFindPredResult struct {
	predCoef0       [maxLPCOrder]int32
	predCoef1       [maxLPCOrder]int32
	ltpCoefQ14      [ltpOrder * maxNbSubfr]int32
	ltpScaleQ14     int32
	nlsfInterpCoef  int32
	perIndex        int32
	ltpIndex        [maxNbSubfr]int32
	nlsfIndices     [maxLPCOrder + 1]int32
	resNrg          [maxNbSubfr]int32
	resNrgQ         [maxNbSubfr]int32
	ltpredCodGainQ7 int32
	sumLogGainQ7    int32
	prevNLSFqQ15    [maxLPCOrder]int32
}

func probeLibopusSILKFixedFindPred(cases []silkFindPredCoefsInput) ([]silkFixedFindPredResult, error) {
	binPath, err := buildLibopusSILKFixedFindPredHelper()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedFindPredInputMagic, uint32(len(cases)))
	for i := range cases {
		tc := &cases[i]
		payload.I32(int32(tc.predictLPCOrder))
		payload.I32(int32(tc.subfrLength))
		payload.I32(int32(tc.nbSubfr))
		payload.I32(int32(tc.frameLength))
		payload.I32(tc.signalType)
		payload.I32(tc.useInterpolatedNLSFs)
		if tc.firstFrameAfterReset {
			payload.I32(1)
		} else {
			payload.I32(0)
		}
		payload.I32(tc.speechActivityQ8)
		payload.I32(int32(tc.nlsfMSVQSurvivors))
		payload.I32(tc.condCoding)
		payload.I32(tc.packetLossPerc)
		payload.I32(tc.nFramesPerPacket)
		payload.I32(tc.lbrrFlag)
		payload.I32(tc.snrDBQ7)
		payload.I32(tc.codingQualityQ14)
		payload.I32(tc.sumLogGainQ7)
		for j := 0; j < maxLPCOrder; j++ {
			payload.I16(tc.prevNLSFqQ15[j])
		}
		for j := 0; j < maxNbSubfr; j++ {
			payload.I32(tc.gainsQ16[j])
		}
		for j := 0; j < maxNbSubfr; j++ {
			payload.I32(int32(tc.pitchL[j]))
		}
		payload.U32(uint32(len(tc.resPitch)))
		payload.U32(uint32(tc.resPitchStart))
		for _, v := range tc.resPitch {
			payload.I16(v)
		}
		payload.U32(uint32(len(tc.x)))
		payload.U32(uint32(tc.xStart))
		for _, v := range tc.x {
			payload.I16(v)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed find_pred_coefs", libopusSILKFixedFindPredOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]silkFixedFindPredResult, count)
	for i := range out {
		r := &out[i]
		for j := 0; j < maxLPCOrder; j++ {
			r.predCoef0[j] = reader.I32()
		}
		for j := 0; j < maxLPCOrder; j++ {
			r.predCoef1[j] = reader.I32()
		}
		for j := 0; j < ltpOrder*maxNbSubfr; j++ {
			r.ltpCoefQ14[j] = reader.I32()
		}
		r.ltpScaleQ14 = reader.I32()
		r.nlsfInterpCoef = reader.I32()
		r.perIndex = reader.I32()
		for j := 0; j < maxNbSubfr; j++ {
			r.ltpIndex[j] = reader.I32()
		}
		for j := 0; j < maxLPCOrder+1; j++ {
			r.nlsfIndices[j] = reader.I32()
		}
		for j := 0; j < maxNbSubfr; j++ {
			r.resNrg[j] = reader.I32()
		}
		for j := 0; j < maxNbSubfr; j++ {
			r.resNrgQ[j] = reader.I32()
		}
		r.ltpredCodGainQ7 = reader.I32()
		r.sumLogGainQ7 = reader.I32()
		for j := 0; j < maxLPCOrder; j++ {
			r.prevNLSFqQ15[j] = reader.I32()
		}
	}
	if err := reader.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKFindPredCoefsFixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0xF9ED))

	// makePrevNLSF builds a monotonically increasing prev NLSF vector.
	makePrevNLSF := func(order int) [maxLPCOrder]int16 {
		var p [maxLPCOrder]int16
		spacing := int16((1 << 15) / int32(order+1))
		v := spacing
		for i := 0; i < order; i++ {
			p[i] = v
			v += spacing
		}
		return p
	}

	// randSignal builds a low-frequency-correlated int16 signal.
	randSignal := func(n int, amp int32) []int16 {
		x := make([]int16, n)
		var acc int32
		for i := range x {
			acc += rng.Int31n(2*amp+1) - amp
			if acc > 32767 {
				acc = 32767
			} else if acc < -32768 {
				acc = -32768
			}
			x[i] = int16(acc >> 4)
		}
		return x
	}

	survivorsFor := func(order int) int {
		if order == 16 {
			return 16
		}
		return 8
	}

	makeCase := func(order, fsKHz, nbSubfr int, signalType int32, useInterp, firstFrame bool, condCoding int32, amp int32) silkFindPredCoefsInput {
		subfrLength := 5 * fsKHz
		frameLength := nbSubfr * subfrLength

		in := silkFindPredCoefsInput{
			predictLPCOrder:      order,
			subfrLength:          subfrLength,
			nbSubfr:              nbSubfr,
			frameLength:          frameLength,
			signalType:           signalType,
			speechActivityQ8:     int32(rng.Intn(257)),
			nlsfMSVQSurvivors:    survivorsFor(order),
			condCoding:           condCoding,
			packetLossPerc:       int32(rng.Intn(20)),
			nFramesPerPacket:     1 + int32(rng.Intn(3)),
			lbrrFlag:             int32(rng.Intn(2)),
			snrDBQ7:              int32(18+rng.Intn(20)) << 7,
			codingQualityQ14:     int32(rng.Intn(1 << 14)),
			sumLogGainQ7:         int32(rng.Intn(4096)),
			prevNLSFqQ15:         makePrevNLSF(order),
			firstFrameAfterReset: firstFrame,
		}
		if useInterp {
			in.useInterpolatedNLSFs = 1
		}

		// Per-subframe gains in Q16, all positive.
		for k := 0; k < nbSubfr; k++ {
			in.gainsQ16[k] = int32(65536) + rng.Int31n(1<<22)
		}

		// pitch lags within PE range; ltp_mem_length - predictLPCOrder must be
		// >= pitchL[0] + LTP_ORDER/2 (asserted only with hardening, but keep it
		// realistic). ltp_mem_length = 20 ms * fsKHz.
		ltpMem := 20 * fsKHz
		maxLag := ltpMem - order - ltpOrder/2 - 1
		if maxLag > 18*fsKHz {
			maxLag = 18 * fsKHz
		}
		minLag := 2 * fsKHz
		for k := 0; k < nbSubfr; k++ {
			in.pitchL[k] = int32(minLag + rng.Intn(maxLag-minLag+1))
		}

		// res_pitch buffer: r_ptr starts at res_start; lag_ptr reaches back
		// res_start - (maxPitch + LTP_ORDER/2); last subframe reads up to
		// res_start + (nbSubfr-1)*subfrLength + subfrLength + LTP_ORDER.
		maxPitch := 0
		for k := 0; k < nbSubfr; k++ {
			if int(in.pitchL[k]) > maxPitch {
				maxPitch = int(in.pitchL[k])
			}
		}
		resStart := maxPitch + ltpOrder/2
		resLen := resStart + nbSubfr*subfrLength + ltpOrder
		in.resPitch = randSignal(resLen, amp)
		in.resPitchStart = resStart

		// x buffer: analysis reads x - predictLPCOrder and, for voiced, the LTP
		// FIR reads x - predictLPCOrder - pitchL[k] - 2 (x_lag_ptr[-2]). Provide
		// maxPitch + predictLPCOrder + 2 history before xStart, then frame_length.
		xHist := maxPitch + order + 2
		xLen := xHist + frameLength
		in.x = randSignal(xLen, amp)
		in.xStart = xHist

		return in
	}

	var cases []silkFindPredCoefsInput
	signalTypes := []int32{typeNoVoiceActivity, typeUnvoiced, typeVoiced}
	condCodings := []int32{codeIndependently, codeConditionally}

	for _, order := range []int{10, 16} {
		for _, fs := range []int{8, 12, 16} {
			for _, nb := range []int{2, 4} {
				for _, st := range signalTypes {
					for _, useInterp := range []bool{false, true} {
						for _, first := range []bool{false, true} {
							for _, cc := range condCodings {
								// NLSF interpolation requires nb_subfr==4.
								ui := useInterp && nb == 4
								cases = append(cases, makeCase(order, fs, nb, st, ui, first, cc, 6000))
							}
						}
					}
				}
			}
		}
	}

	// Bulk random coverage.
	for i := 0; i < 120; i++ {
		order := []int{10, 16}[rng.Intn(2)]
		fs := []int{8, 12, 16}[rng.Intn(3)]
		nb := []int{2, 4}[rng.Intn(2)]
		st := signalTypes[rng.Intn(len(signalTypes))]
		ui := nb == 4 && rng.Intn(2) == 0
		first := rng.Intn(2) == 0
		cc := condCodings[rng.Intn(len(condCodings))]
		amp := []int32{200, 4000, 30000}[rng.Intn(3)]
		cases = append(cases, makeCase(order, fs, nb, st, ui, first, cc, amp))
	}

	want, err := probeLibopusSILKFixedFindPred(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed find_pred_coefs", err)
		return
	}

	for i := range cases {
		in := cases[i]
		var enc Encoder
		var cb *nlsfCB
		if in.predictLPCOrder == 16 {
			cb = &silk_NLSF_CB_WB
		} else {
			cb = &silk_NLSF_CB_NB_MB
		}
		in.cb = cb

		got := enc.silkFindPredCoefsFIX(&in)
		w := want[i]

		fail := func(field string, g, e interface{}) {
			t.Fatalf("case %d (order=%d fs nb=%d st=%d interp=%d first=%v cc=%d): %s got %v want %v",
				i, cases[i].predictLPCOrder, cases[i].nbSubfr, cases[i].signalType,
				cases[i].useInterpolatedNLSFs, cases[i].firstFrameAfterReset, cases[i].condCoding,
				field, g, e)
		}

		order := in.predictLPCOrder
		nb := in.nbSubfr

		for j := 0; j < order; j++ {
			if int32(got.predCoefQ12[0][j]) != w.predCoef0[j] {
				fail(fmt.Sprintf("PredCoef_Q12[0][%d]", j), got.predCoefQ12[0][j], w.predCoef0[j])
			}
			if int32(got.predCoefQ12[1][j]) != w.predCoef1[j] {
				fail(fmt.Sprintf("PredCoef_Q12[1][%d]", j), got.predCoefQ12[1][j], w.predCoef1[j])
			}
		}
		for j := 0; j < nb*ltpOrder; j++ {
			if int32(got.ltpCoefQ14[j]) != w.ltpCoefQ14[j] {
				fail(fmt.Sprintf("LTPCoef_Q14[%d]", j), got.ltpCoefQ14[j], w.ltpCoefQ14[j])
			}
		}
		if got.ltpScaleQ14 != w.ltpScaleQ14 {
			fail("LTP_scale_Q14", got.ltpScaleQ14, w.ltpScaleQ14)
		}
		if int32(got.nlsfInterpCoefQ2) != w.nlsfInterpCoef {
			fail("NLSFInterpCoef_Q2", got.nlsfInterpCoefQ2, w.nlsfInterpCoef)
		}
		if int32(got.perIndex) != w.perIndex {
			fail("PERIndex", got.perIndex, w.perIndex)
		}
		for j := 0; j < nb; j++ {
			if int32(got.ltpIndex[j]) != w.ltpIndex[j] {
				fail(fmt.Sprintf("LTPIndex[%d]", j), got.ltpIndex[j], w.ltpIndex[j])
			}
		}
		for j := 0; j < order+1; j++ {
			if int32(got.nlsfIndices[j]) != w.nlsfIndices[j] {
				fail(fmt.Sprintf("NLSFIndices[%d]", j), got.nlsfIndices[j], w.nlsfIndices[j])
			}
		}
		for j := 0; j < nb; j++ {
			if got.resNrg[j] != w.resNrg[j] {
				fail(fmt.Sprintf("ResNrg[%d]", j), got.resNrg[j], w.resNrg[j])
			}
			if int32(got.resNrgQ[j]) != w.resNrgQ[j] {
				fail(fmt.Sprintf("ResNrgQ[%d]", j), got.resNrgQ[j], w.resNrgQ[j])
			}
		}
		if got.ltpredCodGainQ7 != w.ltpredCodGainQ7 {
			fail("LTPredCodGain_Q7", got.ltpredCodGainQ7, w.ltpredCodGainQ7)
		}
		if got.sumLogGainQ7 != w.sumLogGainQ7 {
			fail("sum_log_gain_Q7", got.sumLogGainQ7, w.sumLogGainQ7)
		}
		for j := 0; j < maxLPCOrder; j++ {
			if int32(got.prevNLSFqQ15[j]) != w.prevNLSFqQ15[j] {
				fail(fmt.Sprintf("prev_NLSFq_Q15[%d]", j), got.prevNLSFqQ15[j], w.prevNLSFqQ15[j])
			}
		}
	}
}
