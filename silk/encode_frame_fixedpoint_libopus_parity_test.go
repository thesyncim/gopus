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
	libopusSILKFixedEncodeFrameInputMagic  = "GEFI"
	libopusSILKFixedEncodeFrameOutputMagic = "GEFO"
)

var (
	libopusSILKFixedEncodeFrameOnce sync.Once
	libopusSILKFixedEncodeFrameBin  string
	libopusSILKFixedEncodeFrameErr  error
)

// buildLibopusSILKFixedEncodeFrameHelper ensures the FIXED_POINT libopus
// reference exists, then compiles
// tools/csrc/libopus_silk_fixed_encode_frame_info.c against it.
func buildLibopusSILKFixedEncodeFrameHelper() (string, error) {
	libopusSILKFixedEncodeFrameOnce.Do(func() {
		_, file, _, _ := runtime.Caller(0)
		repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), ".."))

		refDir := fixedRefPath()
		staticLib := fixedRefPath(".libs", "libopus.a")
		if _, err := os.Stat(staticLib); err != nil {
			cmd := exec.Command("bash", filepath.Join("tools", "ensure_libopus.sh"))
			cmd.Dir = repoRoot
			cmd.Env = append(os.Environ(), "LIBOPUS_ENABLE_FIXED=1")
			if out, berr := cmd.CombinedOutput(); berr != nil {
				libopusSILKFixedEncodeFrameErr = fmt.Errorf("ensure fixed libopus: %w (%s)", berr, out)
				return
			}
		}
		if _, err := os.Stat(staticLib); err != nil {
			libopusSILKFixedEncodeFrameErr = fmt.Errorf("fixed libopus static lib missing: %w", err)
			return
		}

		cc, err := libopustooling.FindCCompiler()
		if err != nil {
			libopusSILKFixedEncodeFrameErr = err
			return
		}

		src := filepath.Join(repoRoot, "tools", "csrc", "libopus_silk_fixed_encode_frame_info.c")
		outDir := filepath.Join(os.TempDir(), "gopus_libopus_test_helpers")
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			libopusSILKFixedEncodeFrameErr = err
			return
		}
		out := filepath.Join(outDir, fmt.Sprintf("gopus_silk_fixed_encode_frame_%s_%s", runtime.GOOS, runtime.GOARCH))

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
			libopusSILKFixedEncodeFrameErr = fmt.Errorf("build silk fixed encode frame helper: %w (%s)", cerr, combined)
			return
		}
		libopusSILKFixedEncodeFrameBin = out
	})
	return libopusSILKFixedEncodeFrameBin, libopusSILKFixedEncodeFrameErr
}

// silkFixedEncodeFrameCase carries the full flattened encoder-state inputs for
// one frame.
type silkFixedEncodeFrameCase struct {
	name string

	fsKHz                   int
	frameLength             int
	subfrLength             int
	nbSubfr                 int
	ltpMemLength            int
	laPitch                 int
	laShape                 int
	pitchLPCWinLength       int
	pitchEstimationLPCOrder int
	predictLPCOrder         int
	shapingLPCOrder         int
	shapeWinLength          int
	complexity              int
	nStatesDelayedDecision  int
	warpingQ16              int32
	useCBR                  int
	nlsfMSVQSurvivors       int
	pitchEstThresQ16        int32
	snrDBQ7                 int32
	packetLossPerc          int32
	nFramesPerPacket        int32
	lbrrFlag                int32
	condCoding              int32
	opusVADActivity         int
	frameCounter            int32
	prevSignalType          int32
	prevLag                 int32
	firstFrameAfterReset    int32

	sumLogGainQ7         int32
	harmShapeGainSmthQ16 int32
	tiltSmthQ16          int32
	lastGainIndex        int32
	ltpCorrQ15           int32
	prevNLSFqQ15         [maxLPCOrder]int16

	vadInput []int16 // frameLength samples (inputBuf+1)
	xBuf     []int16 // ltpMemLength + laShape + frameLength samples
}

// silkFixedEncodeFrameResult mirrors the C oracle output ordering.
type silkFixedEncodeFrameResult struct {
	vadFlag          int32
	speechActivityQ8 int32
	inputTiltQ15     int32
	signalType       int32
	quantOffsetType  int32
	seed             int32
	nlsfInterpCoefQ2 int32
	perIndex         int32
	ltpScaleIndex    int32
	lagIndex         int32
	contourIndex     int32
	ltpredCodGainQ7  int32
	lambdaQ10        int32
	ltpScaleQ14      int32
	ltpCorrQ15       int32
	lastGainIndex    int32

	nlsfIndices  []int32
	gainsIndices []int32
	ltpIndex     []int32
	gainsQ16     []int32
	pitchL       []int32
	predCoefQ12  []int32 // 2*predictLPCOrder
	ltpCoefQ14   []int32 // nbSubfr*LTP_ORDER
	pulses       []int32 // frameLength
}

func probeLibopusSILKFixedEncodeFrame(cases []silkFixedEncodeFrameCase) ([]silkFixedEncodeFrameResult, error) {
	binPath, err := buildLibopusSILKFixedEncodeFrameHelper()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedEncodeFrameInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.I32(int32(tc.fsKHz))
		payload.I32(int32(tc.frameLength))
		payload.I32(int32(tc.subfrLength))
		payload.I32(int32(tc.nbSubfr))
		payload.I32(int32(tc.ltpMemLength))
		payload.I32(int32(tc.laPitch))
		payload.I32(int32(tc.laShape))
		payload.I32(int32(tc.pitchLPCWinLength))
		payload.I32(int32(tc.pitchEstimationLPCOrder))
		payload.I32(int32(tc.predictLPCOrder))
		payload.I32(int32(tc.shapingLPCOrder))
		payload.I32(int32(tc.shapeWinLength))
		payload.I32(int32(tc.complexity))
		payload.I32(int32(tc.nStatesDelayedDecision))
		payload.I32(tc.warpingQ16)
		payload.I32(int32(tc.useCBR))
		payload.I32(int32(tc.nlsfMSVQSurvivors))
		payload.I32(tc.pitchEstThresQ16)
		payload.I32(tc.snrDBQ7)
		payload.I32(tc.packetLossPerc)
		payload.I32(tc.nFramesPerPacket)
		payload.I32(tc.lbrrFlag)
		payload.I32(tc.condCoding)
		payload.I32(int32(tc.opusVADActivity))
		payload.I32(tc.frameCounter)
		payload.I32(tc.prevSignalType)
		payload.I32(tc.prevLag)
		payload.I32(tc.firstFrameAfterReset)

		payload.I32(tc.sumLogGainQ7)
		payload.I32(tc.harmShapeGainSmthQ16)
		payload.I32(tc.tiltSmthQ16)
		payload.I32(tc.lastGainIndex)
		payload.I32(tc.ltpCorrQ15)
		for i := 0; i < maxLPCOrder; i++ {
			payload.I16(tc.prevNLSFqQ15[i])
		}
		for i := 0; i < tc.frameLength; i++ {
			payload.I16(tc.vadInput[i])
		}
		for i := 0; i < len(tc.xBuf); i++ {
			payload.I16(tc.xBuf[i])
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed encode frame", libopusSILKFixedEncodeFrameOutputMagic)
	if err != nil {
		return nil, err
	}
	cnt := reader.Count(len(cases))
	out := make([]silkFixedEncodeFrameResult, cnt)
	for i := range out {
		r := &out[i]
		r.vadFlag = reader.I32()
		r.speechActivityQ8 = reader.I32()
		r.inputTiltQ15 = reader.I32()
		r.signalType = reader.I32()
		r.quantOffsetType = reader.I32()
		r.seed = reader.I32()
		r.nlsfInterpCoefQ2 = reader.I32()
		r.perIndex = reader.I32()
		r.ltpScaleIndex = reader.I32()
		r.lagIndex = reader.I32()
		r.contourIndex = reader.I32()
		r.ltpredCodGainQ7 = reader.I32()
		r.lambdaQ10 = reader.I32()
		r.ltpScaleQ14 = reader.I32()
		r.ltpCorrQ15 = reader.I32()
		r.lastGainIndex = reader.I32()

		po := cases[i].predictLPCOrder
		nb := cases[i].nbSubfr
		r.nlsfIndices = make([]int32, po+1)
		for k := range r.nlsfIndices {
			r.nlsfIndices[k] = reader.I32()
		}
		r.gainsIndices = make([]int32, nb)
		for k := range r.gainsIndices {
			r.gainsIndices[k] = reader.I32()
		}
		r.ltpIndex = make([]int32, nb)
		for k := range r.ltpIndex {
			r.ltpIndex[k] = reader.I32()
		}
		r.gainsQ16 = make([]int32, nb)
		for k := range r.gainsQ16 {
			r.gainsQ16[k] = reader.I32()
		}
		r.pitchL = make([]int32, nb)
		for k := range r.pitchL {
			r.pitchL[k] = reader.I32()
		}
		r.predCoefQ12 = make([]int32, 2*po)
		for k := range r.predCoefQ12 {
			r.predCoefQ12[k] = reader.I32()
		}
		r.ltpCoefQ14 = make([]int32, nb*ltpOrderConst)
		for k := range r.ltpCoefQ14 {
			r.ltpCoefQ14[k] = reader.I32()
		}
		r.pulses = make([]int32, cases[i].frameLength)
		for k := range r.pulses {
			r.pulses[k] = reader.I32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

// wbFrameCase builds a 20 ms wideband (16 kHz, 4 subframe) frame case driven by
// a synthesized signal. genIdx selects a deterministic signal generator.
func wbFrameCase(name string, rng *rand.Rand, genIdx int) silkFixedEncodeFrameCase {
	const (
		fsKHz       = 16
		nbSubfr     = 4
		subfrLength = 5 * fsKHz // 80
		laShapeMs   = 5
		laPitchMs   = 2
	)
	frameLength := nbSubfr * subfrLength      // 320
	ltpMemLength := 20 * fsKHz                // 320
	laPitch := laPitchMs * fsKHz              // 32
	laShape := laShapeMs * fsKHz              // 80
	pitchLPCWinLength := (20 + (laPitchMs << 1)) * fsKHz
	shapeWinLength := 5*fsKHz + 2*laShape // SUB_FRAME_LENGTH_MS*fs + 2*la_shape

	xBufLen := ltpMemLength + laShape + frameLength

	tc := silkFixedEncodeFrameCase{
		name:                    name,
		fsKHz:                   fsKHz,
		frameLength:             frameLength,
		subfrLength:             subfrLength,
		nbSubfr:                 nbSubfr,
		ltpMemLength:            ltpMemLength,
		laPitch:                 laPitch,
		laShape:                 laShape,
		pitchLPCWinLength:       pitchLPCWinLength,
		pitchEstimationLPCOrder: 16,
		predictLPCOrder:         16,
		shapingLPCOrder:         16,
		shapeWinLength:          shapeWinLength,
		complexity:              2,
		nStatesDelayedDecision:  1, // silk_NSQ (non del-dec) path
		warpingQ16:              0, // warping 0 -> silk_NSQ path
		useCBR:                  0,
		nlsfMSVQSurvivors:       16,
		pitchEstThresQ16:        19661, // SILK_FIX_CONST(0.3, 16)
		snrDBQ7:                 int32(28 * 128),
		packetLossPerc:          0,
		nFramesPerPacket:        1,
		lbrrFlag:                0,
		condCoding:              codeIndependently,
		opusVADActivity:         1,
		frameCounter:            int32(rng.Intn(4)),
		prevSignalType:          0,
		prevLag:                 0,
		firstFrameAfterReset:    0,
		sumLogGainQ7:            0,
		harmShapeGainSmthQ16:    0,
		tiltSmthQ16:             0,
		lastGainIndex:           10,
		ltpCorrQ15:              0,
	}

	tc.vadInput = make([]int16, frameLength)
	tc.xBuf = make([]int16, xBufLen)

	// Synthesize a continuous signal across [xBuf history | frame]. The frame
	// occupies xBuf[ltpMemLength+laShape:] and equals vadInput.
	gen := func(n int) float64 {
		t := float64(n) / float64(fsKHz*1000)
		switch genIdx {
		case 0: // voiced: ~140 Hz periodic glottal-ish tone with harmonics
			v := 0.5*math.Sin(2*math.Pi*140*t) +
				0.25*math.Sin(2*math.Pi*280*t) +
				0.12*math.Sin(2*math.Pi*420*t)
			return v
		case 1: // voiced: ~110 Hz
			return 0.55*math.Sin(2*math.Pi*110*t) + 0.2*math.Sin(2*math.Pi*330*t)
		case 2: // unvoiced-ish: high-frequency band noise
			return 0.4 * math.Sin(2*math.Pi*3000*t) * (0.5 + 0.5*math.Sin(2*math.Pi*50*t))
		default: // mixed
			return 0.45*math.Sin(2*math.Pi*200*t) + 0.15*(rng.Float64()*2-1)
		}
	}

	// Fill the whole xBuf history+frame with a coherent signal so pitch/LPC
	// analysis sees real periodicity.
	for i := 0; i < xBufLen; i++ {
		// Phase index counts from the start of xBuf so history is continuous.
		s := gen(i) * 12000.0
		if s > 32767 {
			s = 32767
		} else if s < -32768 {
			s = -32768
		}
		tc.xBuf[i] = int16(s)
	}
	frameStart := ltpMemLength + laShape
	copy(tc.vadInput, tc.xBuf[frameStart:frameStart+frameLength])
	return tc
}

func TestSILKEncodeFrameFIXLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0x5ec0de))

	var cases []silkFixedEncodeFrameCase
	for g := 0; g < 4; g++ {
		cases = append(cases, wbFrameCase(fmt.Sprintf("gen%d", g), rng, g))
	}
	// A few cases with varied frame counters / prev state.
	for i := 0; i < 4; i++ {
		tc := wbFrameCase(fmt.Sprintf("var%d", i), rng, i%4)
		tc.frameCounter = int32(rng.Intn(4))
		tc.prevSignalType = int32(2 * (i % 2)) // 0 or 2 (voiced)
		tc.prevLag = int32(80 + rng.Intn(120))
		tc.snrDBQ7 = int32((20 + rng.Intn(20)) * 128)
		cases = append(cases, tc)
	}
	// first_frame_after_reset path: pitch core is skipped, frame is unvoiced.
	{
		tc := wbFrameCase("firstframe", rng, 0)
		tc.firstFrameAfterReset = 1
		cases = append(cases, tc)
	}

	want, err := probeLibopusSILKFixedEncodeFrame(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed encode frame", err)
		return
	}

	// Confirm the corpus exercises the voiced (LTP / pitch) path so the parity
	// is not trivially driven by unvoiced-only frames.
	voicedSeen := false
	for _, w := range want {
		if w.signalType == typeVoiced {
			voicedSeen = true
			break
		}
	}
	if !voicedSeen {
		t.Fatalf("no voiced frame in corpus; pitch/LTP path not exercised")
	}

	e := &Encoder{}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := &silkEncodeFrameFIXState{
				fsKHz:                       tc.fsKHz,
				frameLength:                 tc.frameLength,
				subfrLength:                 tc.subfrLength,
				nbSubfr:                     tc.nbSubfr,
				ltpMemLength:                tc.ltpMemLength,
				laPitch:                     tc.laPitch,
				laShape:                     tc.laShape,
				pitchLPCWinLength:           tc.pitchLPCWinLength,
				pitchEstimationLPCOrder:     tc.pitchEstimationLPCOrder,
				predictLPCOrder:             tc.predictLPCOrder,
				shapingLPCOrder:             tc.shapingLPCOrder,
				shapeWinLength:              tc.shapeWinLength,
				complexity:                  tc.complexity,
				nStatesDelayedDecision:      tc.nStatesDelayedDecision,
				warpingQ16:                  tc.warpingQ16,
				useCBR:                      tc.useCBR,
				nlsfMSVQSurvivors:           tc.nlsfMSVQSurvivors,
				pitchEstimationThresholdQ16: tc.pitchEstThresQ16,
				snrDBQ7:                     tc.snrDBQ7,
				packetLossPerc:              tc.packetLossPerc,
				nFramesPerPacket:            tc.nFramesPerPacket,
				lbrrFlag:                    tc.lbrrFlag,
				condCoding:                  tc.condCoding,
				opusVADActivity:             tc.opusVADActivity,
				frameCounter:                tc.frameCounter,
				prevSignalType:              tc.prevSignalType,
				prevLag:                     tc.prevLag,
				firstFrameAfterReset:        tc.firstFrameAfterReset != 0,
				sumLogGainQ7:                tc.sumLogGainQ7,
				harmShapeGainSmthQ16:        tc.harmShapeGainSmthQ16,
				tiltSmthQ16:                 tc.tiltSmthQ16,
				lastGainIndex:               int8(tc.lastGainIndex),
				ltpCorrQ15:                  tc.ltpCorrQ15,
				prevNLSFqQ15:                tc.prevNLSFqQ15,
				vadInput:                    tc.vadInput,
				xBuf:                        tc.xBuf,
			}
			// Match the C oracle init: VAD initialized, NSQ prev_gain/lagPrev.
			silkVADInit(&st.vad)
			st.nsq.prevGainQ16 = 1 << 16
			st.nsq.lagPrev = 100

			res := e.silkEncodeFrameFIX(st)
			w := want[i]

			ck := func(field string, got, exp int32) {
				t.Helper()
				if got != exp {
					t.Errorf("%s: got %d want %d", field, got, exp)
				}
			}
			ck("vadFlag", int32(res.vadFlag), w.vadFlag)
			ck("speechActivityQ8", st.speechActivityQ8, w.speechActivityQ8)
			ck("inputTiltQ15", st.inputTiltQ15, w.inputTiltQ15)
			ck("signalType", int32(res.signalType), w.signalType)
			ck("quantOffsetType", int32(res.quantOffsetType), w.quantOffsetType)
			ck("seed", int32(res.seed), w.seed)
			ck("nlsfInterpCoefQ2", int32(res.nlsfInterpCoefQ2), w.nlsfInterpCoefQ2)
			ck("perIndex", int32(res.perIndex), w.perIndex)
			ck("ltpScaleIndex", int32(res.ltpScaleIndex), w.ltpScaleIndex)
			ck("lagIndex", int32(res.lagIndex), w.lagIndex)
			ck("contourIndex", int32(res.contourIndex), w.contourIndex)
			ck("ltpredCodGainQ7", res.ltpredCodGainQ7, w.ltpredCodGainQ7)
			ck("lambdaQ10", res.lambdaQ10, w.lambdaQ10)
			ck("ltpScaleQ14", res.ltpScaleQ14, w.ltpScaleQ14)
			ck("ltpCorrQ15", st.ltpCorrQ15, w.ltpCorrQ15)
			ck("lastGainIndex", int32(res.lastGainIndex), w.lastGainIndex)

			for k := 0; k < tc.predictLPCOrder+1 && k < len(res.nlsfIndices); k++ {
				ck(fmt.Sprintf("nlsfIndices[%d]", k), int32(res.nlsfIndices[k]), w.nlsfIndices[k])
			}
			for k := 0; k < tc.nbSubfr; k++ {
				ck(fmt.Sprintf("gainsIndices[%d]", k), int32(res.gainsIndices[k]), w.gainsIndices[k])
				ck(fmt.Sprintf("ltpIndex[%d]", k), int32(res.ltpIndex[k]), w.ltpIndex[k])
				ck(fmt.Sprintf("gainsQ16[%d]", k), res.gainsQ16[k], w.gainsQ16[k])
				ck(fmt.Sprintf("pitchL[%d]", k), res.pitchL[k], w.pitchL[k])
			}
			for b := 0; b < 2; b++ {
				for k := 0; k < tc.predictLPCOrder; k++ {
					ck(fmt.Sprintf("predCoefQ12[%d][%d]", b, k), int32(res.predCoefQ12[b][k]), w.predCoefQ12[b*tc.predictLPCOrder+k])
				}
			}
			for k := 0; k < tc.nbSubfr*ltpOrderConst; k++ {
				ck(fmt.Sprintf("ltpCoefQ14[%d]", k), int32(res.ltpCoefQ14[k]), w.ltpCoefQ14[k])
			}
			for k := 0; k < tc.frameLength; k++ {
				if int32(res.pulses[k]) != w.pulses[k] {
					t.Errorf("pulses[%d]: got %d want %d", k, res.pulses[k], w.pulses[k])
					break
				}
			}
		})
	}
}
