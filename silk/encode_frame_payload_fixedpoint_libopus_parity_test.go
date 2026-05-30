//go:build gopus_fixedpoint

package silk

import (
	"bytes"
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
	"github.com/thesyncim/gopus/rangecoding"
)

const (
	libopusSILKFixedEncodeFramePayloadInputMagic  = "GEPI"
	libopusSILKFixedEncodeFramePayloadOutputMagic = "GEPO"
)

var (
	libopusSILKFixedEncodeFramePayloadOnce sync.Once
	libopusSILKFixedEncodeFramePayloadBin  string
	libopusSILKFixedEncodeFramePayloadErr  error
)

func buildLibopusSILKFixedEncodeFramePayloadHelper() (string, error) {
	libopusSILKFixedEncodeFramePayloadOnce.Do(func() {
		_, file, _, _ := runtime.Caller(0)
		repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), ".."))

		refDir := fixedRefPath()
		staticLib := fixedRefPath(".libs", "libopus.a")
		if _, err := os.Stat(staticLib); err != nil {
			cmd := exec.Command("bash", filepath.Join("tools", "ensure_libopus.sh"))
			cmd.Dir = repoRoot
			cmd.Env = append(os.Environ(), "LIBOPUS_ENABLE_FIXED=1")
			if out, berr := cmd.CombinedOutput(); berr != nil {
				libopusSILKFixedEncodeFramePayloadErr = fmt.Errorf("ensure fixed libopus: %w (%s)", berr, out)
				return
			}
		}
		if _, err := os.Stat(staticLib); err != nil {
			libopusSILKFixedEncodeFramePayloadErr = fmt.Errorf("fixed libopus static lib missing: %w", err)
			return
		}

		cc, err := libopustooling.FindCCompiler()
		if err != nil {
			libopusSILKFixedEncodeFramePayloadErr = err
			return
		}

		src := filepath.Join(repoRoot, "tools", "csrc", "libopus_silk_fixed_encode_frame_payload_info.c")
		outDir := filepath.Join(os.TempDir(), "gopus_libopus_test_helpers")
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			libopusSILKFixedEncodeFramePayloadErr = err
			return
		}
		out := filepath.Join(outDir, fmt.Sprintf("gopus_silk_fixed_encode_frame_payload_%s_%s", runtime.GOOS, runtime.GOARCH))

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
			libopusSILKFixedEncodeFramePayloadErr = fmt.Errorf("build silk fixed encode frame payload helper: %w (%s)", cerr, combined)
			return
		}
		libopusSILKFixedEncodeFramePayloadBin = out
	})
	return libopusSILKFixedEncodeFramePayloadBin, libopusSILKFixedEncodeFramePayloadErr
}

// silkFixedEncodeFramePayloadCase extends the analysis case with the
// rate-control / entropy / LBRR inputs.
type silkFixedEncodeFramePayloadCase struct {
	silkFixedEncodeFrameCase

	maxBits              int32
	ecPrevLagIndex       int32
	ecPrevSignalType     int32
	lbrrEnabled          int32
	lbrrGainIncreases    int32
	nFramesEncoded       int32
	lbrrPrevFrameHadLBRR int32
	bandwidth            Bandwidth
}

type silkFixedEncodeFramePayloadResult struct {
	nBytesOut        int32
	finalRange       uint32
	vadFlag          int32
	signalType       int32
	lbrrFlag         int32
	ecPrevLagIndex   int32
	ecPrevSignalType int32
	payload          []byte
	lbrrGainsIndices []int32
	lbrrSignalType   int32
	lbrrQuantOffset  int32
	lbrrPulses       []int32
}

func probeLibopusSILKFixedEncodeFramePayload(cases []silkFixedEncodeFramePayloadCase) ([]silkFixedEncodeFramePayloadResult, error) {
	binPath, err := buildLibopusSILKFixedEncodeFramePayloadHelper()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedEncodeFramePayloadInputMagic, uint32(len(cases)))
	for _, c := range cases {
		tc := c.silkFixedEncodeFrameCase
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

		payload.I32(c.maxBits)
		payload.I32(c.ecPrevLagIndex)
		payload.I32(c.ecPrevSignalType)
		payload.I32(c.lbrrEnabled)
		payload.I32(c.lbrrGainIncreases)
		payload.I32(c.nFramesEncoded)
		payload.I32(c.lbrrPrevFrameHadLBRR)

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

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed encode frame payload", libopusSILKFixedEncodeFramePayloadOutputMagic)
	if err != nil {
		return nil, err
	}
	cnt := reader.Count(len(cases))
	out := make([]silkFixedEncodeFramePayloadResult, cnt)
	for i := range out {
		r := &out[i]
		r.nBytesOut = reader.I32()
		r.finalRange = reader.U32()
		r.vadFlag = reader.I32()
		r.signalType = reader.I32()
		r.lbrrFlag = reader.I32()
		r.ecPrevLagIndex = reader.I32()
		r.ecPrevSignalType = reader.I32()
		n := reader.I32()
		r.payload = reader.Bytes(int(n))
		nb := cases[i].nbSubfr
		r.lbrrGainsIndices = make([]int32, nb)
		for k := range r.lbrrGainsIndices {
			r.lbrrGainsIndices[k] = reader.I32()
		}
		r.lbrrSignalType = reader.I32()
		r.lbrrQuantOffset = reader.I32()
		r.lbrrPulses = make([]int32, cases[i].frameLength)
		for k := range r.lbrrPulses {
			r.lbrrPulses[k] = reader.I32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

// genericFrameCase builds a frame case for an arbitrary bandwidth / frame
// duration / NSQ mode, synthesizing a coherent signal across the x_buf history.
func genericFrameCase(name string, rng *rand.Rand, genIdx, fsKHz, nbSubfr, predictOrder int, frameMs int, nStatesDelayedDecision int, warpingQ16 int32) silkFixedEncodeFrameCase {
	const (
		laShapeMs = 5
		laPitchMs = 2
	)
	subfrLength := 5 * fsKHz
	frameLength := nbSubfr * subfrLength
	ltpMemLength := 20 * fsKHz
	laPitch := laPitchMs * fsKHz
	laShape := laShapeMs * fsKHz
	pitchLPCWinLength := (20 + (laPitchMs << 1)) * fsKHz
	shapeWinLength := 5*fsKHz + 2*laShape
	_ = frameMs

	xBufLen := ltpMemLength + laShape + frameLength

	survivors := 16
	if predictOrder == 10 {
		survivors = 10
	}

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
		pitchEstimationLPCOrder: predictOrder,
		predictLPCOrder:         predictOrder,
		shapingLPCOrder:         predictOrder,
		shapeWinLength:          shapeWinLength,
		complexity:              2,
		nStatesDelayedDecision:  nStatesDelayedDecision,
		warpingQ16:              warpingQ16,
		useCBR:                  0,
		nlsfMSVQSurvivors:       survivors,
		pitchEstThresQ16:        19661,
		snrDBQ7:                 int32(28 * 128),
		packetLossPerc:          0,
		nFramesPerPacket:        1,
		lbrrFlag:                0,
		condCoding:              codeIndependently,
		opusVADActivity:         1,
		frameCounter:            int32(rng.Intn(4)),
		lastGainIndex:           10,
	}
	tc.vadInput = make([]int16, frameLength)
	tc.xBuf = make([]int16, xBufLen)

	gen := func(n int) float64 {
		t := float64(n) / float64(fsKHz*1000)
		switch genIdx {
		case 0:
			return 0.5*math.Sin(2*math.Pi*140*t) + 0.25*math.Sin(2*math.Pi*280*t) + 0.12*math.Sin(2*math.Pi*420*t)
		case 1:
			return 0.55*math.Sin(2*math.Pi*110*t) + 0.2*math.Sin(2*math.Pi*330*t)
		case 2:
			return 0.4 * math.Sin(2*math.Pi*3000*t) * (0.5 + 0.5*math.Sin(2*math.Pi*50*t))
		default:
			return 0.45*math.Sin(2*math.Pi*200*t) + 0.15*(rng.Float64()*2-1)
		}
	}
	for i := 0; i < xBufLen; i++ {
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

func TestSILKEncodeFramePayloadFIXLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0x9a7104))

	var cases []silkFixedEncodeFramePayloadCase

	mk := func(name string, genIdx int, useCBR int, maxBits int32, condCoding int32, frameCounter int32) silkFixedEncodeFramePayloadCase {
		base := wbFrameCase(name, rng, genIdx)
		base.useCBR = useCBR
		base.condCoding = condCoding
		base.frameCounter = frameCounter
		return silkFixedEncodeFramePayloadCase{
			silkFixedEncodeFrameCase: base,
			maxBits:                  maxBits,
			bandwidth:                BandwidthWideband,
		}
	}

	// VBR / CBR, voiced + unvoiced, varied budgets and conditional coding.
	for g := 0; g < 4; g++ {
		cases = append(cases, mk(fmt.Sprintf("vbr_gen%d_loose", g), g, 0, 8000, codeIndependently, int32(rng.Intn(4))))
		cases = append(cases, mk(fmt.Sprintf("vbr_gen%d_tight", g), g, 0, 600, codeIndependently, int32(rng.Intn(4))))
		cases = append(cases, mk(fmt.Sprintf("cbr_gen%d", g), g, 1, 1400, codeIndependently, int32(rng.Intn(4))))
	}
	// Conditional coding (mid-packet) with prior ec state.
	{
		c := mk("cond_voiced", 0, 0, 2000, codeConditionally, 1)
		c.prevSignalType = 2
		c.prevLag = 120
		c.ecPrevSignalType = typeVoiced
		c.ecPrevLagIndex = 60
		cases = append(cases, c)
	}
	// CBR very tight to exercise gain-lock / damage control.
	cases = append(cases, mk("cbr_starved", 0, 1, 200, codeIndependently, 0))

	// mkg wraps a generic case (NB/MB/10ms/del-dec) with rate-control inputs.
	mkg := func(name string, base silkFixedEncodeFrameCase, useCBR int, maxBits int32, bw Bandwidth) silkFixedEncodeFramePayloadCase {
		base.name = name
		base.useCBR = useCBR
		return silkFixedEncodeFramePayloadCase{
			silkFixedEncodeFrameCase: base,
			maxBits:                  maxBits,
			bandwidth:                bw,
		}
	}

	// NB (8 kHz, order 10), 20 ms, 4 subframes.
	for g := 0; g < 3; g++ {
		cases = append(cases, mkg(fmt.Sprintf("nb_gen%d", g),
			genericFrameCase("", rng, g, 8, 4, 10, 20, 1, 0), 0, 4000, BandwidthNarrowband))
	}
	// MB (12 kHz, order 10), 20 ms.
	for g := 0; g < 2; g++ {
		cases = append(cases, mkg(fmt.Sprintf("mb_gen%d", g),
			genericFrameCase("", rng, g, 12, 4, 10, 20, 1, 0), 0, 5000, BandwidthMediumband))
	}
	// WB 10 ms (2 subframes).
	for g := 0; g < 2; g++ {
		cases = append(cases, mkg(fmt.Sprintf("wb10ms_gen%d", g),
			genericFrameCase("", rng, g, 16, 2, 16, 10, 1, 0), 0, 4000, BandwidthWideband))
	}
	// NB 10 ms.
	cases = append(cases, mkg("nb10ms_gen0",
		genericFrameCase("", rng, 0, 8, 2, 10, 10, 1, 0), 0, 2500, BandwidthNarrowband))
	// Delayed-decision NSQ path (nStatesDelayedDecision > 1) and warping.
	for g := 0; g < 2; g++ {
		cases = append(cases, mkg(fmt.Sprintf("wb_deldec_gen%d", g),
			genericFrameCase("", rng, g, 16, 4, 16, 20, 4, 0), 0, 8000, BandwidthWideband))
	}
	cases = append(cases, mkg("wb_warp_gen0",
		genericFrameCase("", rng, 0, 16, 4, 16, 20, 4, 655), 0, 8000, BandwidthWideband))
	// LBRR-enabled frames (high activity synthesized signal exceeds threshold).
	{
		c := mkg("wb_lbrr_indep",
			genericFrameCase("", rng, 0, 16, 4, 16, 20, 1, 0), 0, 8000, BandwidthWideband)
		c.lbrrEnabled = 1
		c.lbrrGainIncreases = 2
		cases = append(cases, c)
	}
	{
		c := mkg("wb_lbrr_cond",
			genericFrameCase("", rng, 1, 16, 4, 16, 20, 1, 0), 0, 8000, BandwidthWideband)
		c.silkFixedEncodeFrameCase.condCoding = codeConditionally
		c.silkFixedEncodeFrameCase.prevSignalType = 2
		c.silkFixedEncodeFrameCase.prevLag = 100
		c.ecPrevSignalType = typeVoiced
		c.ecPrevLagIndex = 55
		c.lbrrEnabled = 1
		c.lbrrGainIncreases = 1
		c.nFramesEncoded = 1
		cases = append(cases, c)
	}

	want, err := probeLibopusSILKFixedEncodeFramePayload(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed encode frame payload", err)
		return
	}

	voicedSeen := false
	for _, w := range want {
		if w.signalType == typeVoiced {
			voicedSeen = true
		}
	}
	if !voicedSeen {
		t.Fatalf("no voiced frame in corpus")
	}

	e := &Encoder{}
	for i, c := range cases {
		tc := c.silkFixedEncodeFrameCase
		t.Run(tc.name, func(t *testing.T) {
			ps := &silkEncodeFramePayloadFIXState{
				silkEncodeFrameFIXState: silkEncodeFrameFIXState{
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
				},
				ecPrevLagIndex:       int16(c.ecPrevLagIndex),
				ecPrevSignalType:     c.ecPrevSignalType,
				lbrrEnabled:          c.lbrrEnabled != 0,
				lbrrGainIncreases:    c.lbrrGainIncreases,
				nFramesEncoded:       int(c.nFramesEncoded),
				lbrrPrevFrameHadLBRR: c.lbrrPrevFrameHadLBRR != 0,
				maxBits:              int(c.maxBits),
				useCBR:               tc.useCBR != 0,
				bandwidth:            c.bandwidth,
			}
			silkVADInit(&ps.vad)
			ps.nsq.prevGainQ16 = 1 << 16
			ps.nsq.lagPrev = 100

			buf := make([]byte, 1275)
			re := &rangecoding.Encoder{}
			re.Init(buf)
			ps.rangeEncoder = re

			res := e.silkEncodeFramePayloadFIX(ps)
			w := want[i]

			if int32(res.nBytesOut) != w.nBytesOut {
				t.Errorf("nBytesOut: got %d want %d", res.nBytesOut, w.nBytesOut)
			}
			gotRange := re.Range()
			if gotRange != w.finalRange {
				t.Errorf("finalRange: got %d want %d", gotRange, w.finalRange)
			}

			re.Done()
			// libopus returns nBytesOut = (ec_tell+7)>>3 and the oracle reads
			// that many bytes from the (zero-initialized) ec buffer, so a final
			// rounding byte that Done() leaves as zero is still included. The
			// Done() output writes into the same backing buffer, so take
			// nBytesOut bytes from it (zero-padded) to mirror the oracle.
			n := res.nBytesOut
			if n > len(buf) {
				n = len(buf)
			}
			gotPayload := buf[:n]
			if !bytes.Equal(gotPayload, w.payload) {
				diffAt := -1
				for k := 0; k < len(gotPayload) && k < len(w.payload); k++ {
					if gotPayload[k] != w.payload[k] {
						diffAt = k
						break
					}
				}
				t.Errorf("payload mismatch (signalType=%d nBytes got=%d want=%d): first diff at byte %d\n got=%x\nwant=%x",
					w.signalType, len(gotPayload), len(w.payload), diffAt, gotPayload, w.payload)
			}
			if int32(ps.ecPrevSignalType) != w.ecPrevSignalType {
				t.Errorf("ecPrevSignalType: got %d want %d", ps.ecPrevSignalType, w.ecPrevSignalType)
			}
			if int32(ps.ecPrevLagIndex) != w.ecPrevLagIndex {
				t.Errorf("ecPrevLagIndex: got %d want %d", ps.ecPrevLagIndex, w.ecPrevLagIndex)
			}
			if int32(res.lbrrFlag) != w.lbrrFlag {
				t.Errorf("lbrrFlag: got %d want %d", res.lbrrFlag, w.lbrrFlag)
			}
			if res.lbrrFlag != 0 {
				if int32(res.lbrrIndices.signalType) != w.lbrrSignalType {
					t.Errorf("lbrr signalType: got %d want %d", res.lbrrIndices.signalType, w.lbrrSignalType)
				}
				if int32(res.lbrrIndices.quantOffsetType) != w.lbrrQuantOffset {
					t.Errorf("lbrr quantOffsetType: got %d want %d", res.lbrrIndices.quantOffsetType, w.lbrrQuantOffset)
				}
				for k := 0; k < tc.nbSubfr; k++ {
					if int32(res.lbrrIndices.GainsIndices[k]) != w.lbrrGainsIndices[k] {
						t.Errorf("lbrr GainsIndices[%d]: got %d want %d", k, res.lbrrIndices.GainsIndices[k], w.lbrrGainsIndices[k])
					}
				}
				for k := 0; k < tc.frameLength; k++ {
					if int32(res.lbrrPulses[k]) != w.lbrrPulses[k] {
						t.Errorf("lbrr pulses[%d]: got %d want %d", k, res.lbrrPulses[k], w.lbrrPulses[k])
						break
					}
				}
			}
		})
	}

	// Confirm the corpus actually exercised LBRR encoding.
	lbrrSeen := false
	for _, w := range want {
		if w.lbrrFlag != 0 {
			lbrrSeen = true
			break
		}
	}
	if !lbrrSeen {
		t.Errorf("no LBRR frame in corpus; LBRR path not exercised")
	}
}
