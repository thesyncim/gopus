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
	libopusSILKFixedNSQDelDecOuterInputMagic  = "GDXI"
	libopusSILKFixedNSQDelDecOuterOutputMagic = "GDXO"
)

var (
	libopusSILKFixedNSQDelDecOuterOnce sync.Once
	libopusSILKFixedNSQDelDecOuterBin  string
	libopusSILKFixedNSQDelDecOuterErr  error
)

// buildLibopusSILKFixedNSQDelDecOuterHelper ensures the FIXED_POINT libopus
// reference exists, then compiles
// tools/csrc/libopus_silk_fixed_nsq_del_dec_outer_info.c against it. The oracle
// reproduces silk_NSQ_del_dec_c and its file-static helpers verbatim, routing
// the inner short-prediction through the scalar _c reference.
func buildLibopusSILKFixedNSQDelDecOuterHelper() (string, error) {
	libopusSILKFixedNSQDelDecOuterOnce.Do(func() {
		_, file, _, _ := runtime.Caller(0)
		repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), ".."))

		refDir := fixedRefPath()
		staticLib := fixedRefPath(".libs", "libopus.a")
		if _, err := os.Stat(staticLib); err != nil {
			cmd := exec.Command("bash", filepath.Join("tools", "ensure_libopus.sh"))
			cmd.Dir = repoRoot
			cmd.Env = append(os.Environ(), "LIBOPUS_ENABLE_FIXED=1")
			if out, berr := cmd.CombinedOutput(); berr != nil {
				libopusSILKFixedNSQDelDecOuterErr = fmt.Errorf("ensure fixed libopus: %w (%s)", berr, out)
				return
			}
		}
		if _, err := os.Stat(staticLib); err != nil {
			libopusSILKFixedNSQDelDecOuterErr = fmt.Errorf("fixed libopus static lib missing: %w", err)
			return
		}

		cc, err := libopustooling.FindCCompiler()
		if err != nil {
			libopusSILKFixedNSQDelDecOuterErr = err
			return
		}

		src := filepath.Join(repoRoot, "tools", "csrc", "libopus_silk_fixed_nsq_del_dec_outer_info.c")
		outDir := filepath.Join(os.TempDir(), "gopus_libopus_test_helpers")
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			libopusSILKFixedNSQDelDecOuterErr = err
			return
		}
		out := filepath.Join(outDir, fmt.Sprintf("gopus_silk_fixed_nsq_del_dec_outer_%s_%s", runtime.GOOS, runtime.GOARCH))

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
			libopusSILKFixedNSQDelDecOuterErr = fmt.Errorf("build silk fixed nsq del dec outer helper: %w (%s)", cerr, combined)
			return
		}
		libopusSILKFixedNSQDelDecOuterBin = out
	})
	return libopusSILKFixedNSQDelDecOuterBin, libopusSILKFixedNSQDelDecOuterErr
}

type silkFixedNSQDelDecOuterCase struct {
	name            string
	nbSubfr         int
	frameLength     int
	subfrLength     int
	ltpMemLength    int
	predictLPCOrder int
	shapingLPCOrder int
	nStates         int
	warpingQ16      int32

	signalType       int
	quantOffsetType  int
	nlsfInterpCoefQ2 int
	seed             int
	lambdaQ10        int32
	ltpScaleQ14      int32

	lagPrev       int
	sLTPBufIdx    int
	sLTPShpBufIdx int
	prevGainQ16   int32
	rewhiteFlag   int
	sLFARShp      int32
	sDiffShp      int32

	predCoefQ12      [2 * maxLPCOrder]int16
	ltpCoefQ14       [ltpOrderConst * maxNbSubfr]int16
	arQ13            [maxNbSubfr * maxShapeLpcOrder]int16
	harmShapeGainQ14 [maxNbSubfr]int32
	tiltQ14          [maxNbSubfr]int32
	lfShpQ14         [maxNbSubfr]int32
	gainsQ16         [maxNbSubfr]int32
	pitchL           [maxNbSubfr]int32

	x16        []int16
	xq         [nsqOuterXQLen]int16
	sLTPShpQ14 [nsqOuterSLTPShpLen]int32
	sLPCQ14    [nsqOuterSLPCLen]int32
	sAR2Q14    [maxShapeLpcOrder]int32
}

type silkFixedNSQDelDecOuterResult struct {
	pulses        []int8
	xq            []int16
	sLTPShpQ14    []int32
	sLPCQ14       []int32
	sAR2Q14       []int32
	sLFARShp      int32
	sDiffShp      int32
	lagPrev       int32
	sLTPBufIdx    int32
	sLTPShpBufIdx int32
	prevGainQ16   int32
	rewhiteFlag   int32
	seed          int32
}

func probeLibopusSILKFixedNSQDelDecOuter(cases []silkFixedNSQDelDecOuterCase) ([]silkFixedNSQDelDecOuterResult, error) {
	binPath, err := buildLibopusSILKFixedNSQDelDecOuterHelper()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedNSQDelDecOuterInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.nbSubfr))
		payload.U32(uint32(tc.frameLength))
		payload.U32(uint32(tc.subfrLength))
		payload.U32(uint32(tc.ltpMemLength))
		payload.U32(uint32(tc.predictLPCOrder))
		payload.U32(uint32(tc.shapingLPCOrder))
		payload.U32(uint32(tc.nStates))
		payload.I32(tc.warpingQ16)
		payload.I32(int32(tc.signalType))
		payload.I32(int32(tc.quantOffsetType))
		payload.I32(int32(tc.nlsfInterpCoefQ2))
		payload.I32(int32(tc.seed))
		payload.I32(tc.lambdaQ10)
		payload.I32(tc.ltpScaleQ14)
		payload.I32(int32(tc.lagPrev))
		payload.I32(int32(tc.sLTPBufIdx))
		payload.I32(int32(tc.sLTPShpBufIdx))
		payload.I32(tc.prevGainQ16)
		payload.I32(int32(tc.rewhiteFlag))
		payload.I32(tc.sLFARShp)
		payload.I32(tc.sDiffShp)
		for _, v := range tc.predCoefQ12 {
			payload.I16(v)
		}
		for _, v := range tc.ltpCoefQ14 {
			payload.I16(v)
		}
		for _, v := range tc.arQ13 {
			payload.I16(v)
		}
		for _, v := range tc.harmShapeGainQ14 {
			payload.I32(v)
		}
		for _, v := range tc.tiltQ14 {
			payload.I32(v)
		}
		for _, v := range tc.lfShpQ14 {
			payload.I32(v)
		}
		for _, v := range tc.gainsQ16 {
			payload.I32(v)
		}
		for _, v := range tc.pitchL {
			payload.I32(v)
		}
		for i := 0; i < tc.frameLength; i++ {
			payload.I16(tc.x16[i])
		}
		for _, v := range tc.xq {
			payload.I16(v)
		}
		for _, v := range tc.sLTPShpQ14 {
			payload.I32(v)
		}
		for _, v := range tc.sLPCQ14 {
			payload.I32(v)
		}
		for _, v := range tc.sAR2Q14 {
			payload.I32(v)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed nsq del dec outer", libopusSILKFixedNSQDelDecOuterOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]silkFixedNSQDelDecOuterResult, count)
	for i := range out {
		n := cases[i].frameLength
		r := silkFixedNSQDelDecOuterResult{
			pulses:     make([]int8, n),
			xq:         make([]int16, nsqOuterXQLen),
			sLTPShpQ14: make([]int32, nsqOuterSLTPShpLen),
			sLPCQ14:    make([]int32, nsqOuterSLPCLen),
			sAR2Q14:    make([]int32, maxShapeLpcOrder),
		}
		for j := 0; j < n; j++ {
			r.pulses[j] = int8(reader.I16())
		}
		for j := range r.xq {
			r.xq[j] = reader.I16()
		}
		for j := range r.sLTPShpQ14 {
			r.sLTPShpQ14[j] = reader.I32()
		}
		for j := range r.sLPCQ14 {
			r.sLPCQ14[j] = reader.I32()
		}
		for j := range r.sAR2Q14 {
			r.sAR2Q14[j] = reader.I32()
		}
		r.sLFARShp = reader.I32()
		r.sDiffShp = reader.I32()
		r.lagPrev = reader.I32()
		r.sLTPBufIdx = reader.I32()
		r.sLTPShpBufIdx = reader.I32()
		r.prevGainQ16 = reader.I32()
		r.rewhiteFlag = reader.I32()
		r.seed = reader.I32()
		out[i] = r
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKNSQDelDecFixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0x4e534444)) // "NSDD"

	r16 := func(amp int32) int16 { return int16(rng.Int31n(2*amp+1) - amp) }
	r32 := func(amp int32) int32 { return rng.Int31n(2*amp+1) - amp }

	makeCase := func(name string, voiced bool, nbSubfr, subfrLength, predOrder, shapeOrder, nlsfInterp, nStates int, gainChange, warped bool) silkFixedNSQDelDecOuterCase {
		var tc silkFixedNSQDelDecOuterCase
		tc.name = name
		tc.nbSubfr = nbSubfr
		tc.subfrLength = subfrLength
		tc.frameLength = nbSubfr * subfrLength
		tc.ltpMemLength = ltpMemLength // 320
		tc.predictLPCOrder = predOrder
		tc.shapingLPCOrder = shapeOrder
		tc.nStates = nStates
		tc.nlsfInterpCoefQ2 = nlsfInterp
		tc.quantOffsetType = rng.Intn(2)
		tc.seed = rng.Intn(4)
		tc.lambdaQ10 = rng.Int31n(8192)
		tc.ltpScaleQ14 = []int32{15565, 12288, 8192}[rng.Intn(3)]
		tc.prevGainQ16 = 1<<16 + rng.Int31n(1<<22)
		tc.sLFARShp = r32(1 << 20)
		tc.sDiffShp = r32(1 << 20)
		tc.lagPrev = 32 + rng.Intn(160)
		tc.rewhiteFlag = 0
		if warped {
			tc.warpingQ16 = r32(1 << 16)
		}

		if voiced {
			tc.signalType = typeVoiced
		} else {
			tc.signalType = rng.Intn(2) // 0=inactive, 1=unvoiced
		}

		// Constant pitch lag across the frame keeps every LTP-history read inside
		// the rewhitened window (see the non-del-dec NSQ parity rationale). The
		// decision delay is min(DECISION_DELAY, subfr_length, lag - LTP_ORDER/2 -
		// 1); pick a lag large enough that decisionDelay stays >= 1 and
		// start_idx = ltp_mem - lag - predOrder - LTP_ORDER/2 > 0.
		lag := int32(20 + rng.Intn(tc.ltpMemLength-predOrder-ltpOrderConst/2-21))
		for k := 0; k < maxNbSubfr; k++ {
			tc.harmShapeGainQ14[k] = rng.Int31n(1 << 14)
			tc.tiltQ14[k] = r32(1 << 14)
			tc.lfShpQ14[k] = r32(1 << 26)
			if gainChange {
				tc.gainsQ16[k] = 1<<16 + rng.Int31n(1<<22)
			} else {
				tc.gainsQ16[k] = tc.prevGainQ16
			}
			tc.pitchL[k] = lag
		}

		for i := range tc.predCoefQ12 {
			tc.predCoefQ12[i] = r16(4096)
		}
		for i := range tc.ltpCoefQ14 {
			tc.ltpCoefQ14[i] = r16(8192)
		}
		for i := range tc.arQ13 {
			tc.arQ13[i] = r16(8192)
		}

		tc.x16 = make([]int16, tc.frameLength)
		for i := range tc.x16 {
			tc.x16[i] = r16(1 << 14)
		}
		for i := range tc.xq {
			tc.xq[i] = r16(1 << 13)
		}
		for i := range tc.sLTPShpQ14 {
			tc.sLTPShpQ14[i] = r32(1 << 22)
		}
		for i := range tc.sLPCQ14 {
			tc.sLPCQ14[i] = r32(1 << 22)
		}
		for i := range tc.sAR2Q14 {
			tc.sAR2Q14[i] = r32(1 << 22)
		}
		return tc
	}

	var cases []silkFixedNSQDelDecOuterCase
	// Structured coverage: voiced/unvoiced, both prediction orders, both NLSF
	// interpolation flags, nStatesDelayedDecision in {1,2,3,4}, 2 and 4
	// subframes (4 subframes exercises the k==2 mid-frame reset), gain changes,
	// and the warped shaping path.
	for _, voiced := range []bool{false, true} {
		for _, predOrder := range []int{10, 16} {
			for _, nlsfInterp := range []int{2, 4} {
				for _, nStates := range []int{1, 2, 4} {
					for _, cfg := range []struct {
						nbSubfr, subfrLength int
					}{{2, 40}, {4, 40}, {4, 80}} {
						for _, gainChange := range []bool{false, true} {
							name := fmt.Sprintf("v=%t/pred=%d/interp=%d/states=%d/nb=%d/sub=%d/gc=%t",
								voiced, predOrder, nlsfInterp, nStates, cfg.nbSubfr, cfg.subfrLength, gainChange)
							cases = append(cases, makeCase(name, voiced, cfg.nbSubfr, cfg.subfrLength,
								predOrder, 16, nlsfInterp, nStates, gainChange, false))
						}
					}
				}
			}
		}
	}
	// Randomized bulk coverage including odd-but-even shaping orders, the warped
	// shaping path, and the aggressive RDO Lambda branch.
	for i := 0; i < 128; i++ {
		voiced := rng.Intn(2) == 1
		predOrder := []int{10, 16}[rng.Intn(2)]
		shapeOrder := 2 * (3 + rng.Intn(10)) // 6..24, even, >=6
		nlsfInterp := []int{0, 1, 2, 3, 4}[rng.Intn(5)]
		nStates := []int{1, 2, 3, 4}[rng.Intn(4)]
		nbSubfr := []int{2, 4}[rng.Intn(2)]
		subfrLength := []int{40, 80}[rng.Intn(2)]
		gainChange := rng.Intn(2) == 0
		warped := rng.Intn(2) == 0
		tc := makeCase(fmt.Sprintf("bulk-%d", i), voiced, nbSubfr, subfrLength,
			predOrder, shapeOrder, nlsfInterp, nStates, gainChange, warped)
		if rng.Intn(2) == 0 {
			tc.lambdaQ10 = 2049 + rng.Int31n(60000) // force RDO branch
		}
		cases = append(cases, tc)
	}

	want, err := probeLibopusSILKFixedNSQDelDecOuter(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed nsq del dec outer", err)
		return
	}

	for i, tc := range cases {
		nsq := &NSQState{
			xq:            tc.xq,
			sLTPShpQ14:    tc.sLTPShpQ14,
			sLPCQ14:       tc.sLPCQ14,
			sAR2Q14:       tc.sAR2Q14,
			sLFARShpQ14:   tc.sLFARShp,
			sDiffShpQ14:   tc.sDiffShp,
			lagPrev:       int32(tc.lagPrev),
			sLTPBufIdx:    tc.sLTPBufIdx,
			sLTPShpBufIdx: tc.sLTPShpBufIdx,
			prevGainQ16:   tc.prevGainQ16,
			rewhiteFlag:   tc.rewhiteFlag,
		}
		pulses := make([]int8, tc.frameLength)

		seedOut := silkNSQDelDecFixed(
			&silkFixedEncodeScratch{},
			nsq,
			tc.seed,
			tc.signalType,
			tc.quantOffsetType,
			tc.nlsfInterpCoefQ2,
			tc.x16,
			pulses,
			tc.predCoefQ12[:],
			tc.ltpCoefQ14[:],
			tc.arQ13[:],
			tc.harmShapeGainQ14[:],
			tc.tiltQ14[:],
			tc.lfShpQ14[:],
			tc.gainsQ16[:],
			tc.pitchL[:],
			tc.lambdaQ10,
			tc.ltpScaleQ14,
			tc.ltpMemLength,
			tc.frameLength,
			tc.subfrLength,
			tc.nbSubfr,
			tc.predictLPCOrder,
			tc.shapingLPCOrder,
			tc.warpingQ16,
			tc.nStates,
		)

		w := want[i]
		for j := 0; j < tc.frameLength; j++ {
			if pulses[j] != w.pulses[j] {
				t.Fatalf("case %d (%s): pulses[%d]=%d want %d", i, tc.name, j, pulses[j], w.pulses[j])
			}
		}
		for j := range nsq.xq {
			if nsq.xq[j] != w.xq[j] {
				t.Fatalf("case %d (%s): xq[%d]=%d want %d", i, tc.name, j, nsq.xq[j], w.xq[j])
			}
		}
		for j := range nsq.sLTPShpQ14 {
			if nsq.sLTPShpQ14[j] != w.sLTPShpQ14[j] {
				t.Fatalf("case %d (%s): sLTPShpQ14[%d]=%d want %d", i, tc.name, j, nsq.sLTPShpQ14[j], w.sLTPShpQ14[j])
			}
		}
		for j := range nsq.sLPCQ14 {
			if nsq.sLPCQ14[j] != w.sLPCQ14[j] {
				t.Fatalf("case %d (%s): sLPCQ14[%d]=%d want %d", i, tc.name, j, nsq.sLPCQ14[j], w.sLPCQ14[j])
			}
		}
		for j := range nsq.sAR2Q14 {
			if nsq.sAR2Q14[j] != w.sAR2Q14[j] {
				t.Fatalf("case %d (%s): sAR2Q14[%d]=%d want %d", i, tc.name, j, nsq.sAR2Q14[j], w.sAR2Q14[j])
			}
		}
		if nsq.sLFARShpQ14 != w.sLFARShp {
			t.Fatalf("case %d (%s): sLFARShpQ14=%d want %d", i, tc.name, nsq.sLFARShpQ14, w.sLFARShp)
		}
		if nsq.sDiffShpQ14 != w.sDiffShp {
			t.Fatalf("case %d (%s): sDiffShpQ14=%d want %d", i, tc.name, nsq.sDiffShpQ14, w.sDiffShp)
		}
		if nsq.lagPrev != w.lagPrev {
			t.Fatalf("case %d (%s): lagPrev=%d want %d", i, tc.name, nsq.lagPrev, w.lagPrev)
		}
		if int32(nsq.sLTPBufIdx) != w.sLTPBufIdx {
			t.Fatalf("case %d (%s): sLTPBufIdx=%d want %d", i, tc.name, nsq.sLTPBufIdx, w.sLTPBufIdx)
		}
		if int32(nsq.sLTPShpBufIdx) != w.sLTPShpBufIdx {
			t.Fatalf("case %d (%s): sLTPShpBufIdx=%d want %d", i, tc.name, nsq.sLTPShpBufIdx, w.sLTPShpBufIdx)
		}
		if nsq.prevGainQ16 != w.prevGainQ16 {
			t.Fatalf("case %d (%s): prevGainQ16=%d want %d", i, tc.name, nsq.prevGainQ16, w.prevGainQ16)
		}
		if int32(nsq.rewhiteFlag) != w.rewhiteFlag {
			t.Fatalf("case %d (%s): rewhiteFlag=%d want %d", i, tc.name, nsq.rewhiteFlag, w.rewhiteFlag)
		}
		if int32(seedOut) != w.seed {
			t.Fatalf("case %d (%s): seed=%d want %d", i, tc.name, seedOut, w.seed)
		}
	}
}
