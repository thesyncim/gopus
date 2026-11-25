//go:build gopus_fixed_point

package silk

import (
	"bytes"
	"fmt"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/testsignal"
	"math"
	"math/rand"
	"testing"
)

var fixedSILKAPIHelper libopustest.HelperCache

// assertFixedSILKAPI compares the complete packets and coder state produced by
// PacketEncoder and the linked silk_Encode API. Both consume the same signed16
// input lattice, controls, packet history and reset points.
func assertFixedSILKAPI(t *testing.T, p *testPacketEncoder, frames [][]float32, resetAt int) {
	t.Helper()
	helper, err := fixedSILKAPIHelper.CHelperPath(libopustest.CHelperConfig{
		Label: "fixed SILK API", OutputBase: "gopus_silk_fixed_api", SourceFile: "libopus_silk_fixed_api_info.c",
		FixedRef: true, CFlags: []string{"-DHAVE_CONFIG_H", "-O3"}, RefIncludes: []string{"celt", "silk", "silk/fixed"},
		Libs: []string{libopustest.FixedRefPath(".libs", "libopus.a"), "-lm"},
	})
	if err != nil {
		t.Fatal(err)
	}
	c := p.ctl
	original := newTestPacketEncoderAt(int(c.APISampleRate), p.enc.state[0].bandwidth, int(c.NChannelsAPI))
	original.ctl = c
	channels := int(c.NChannelsAPI)
	frameSamples := len(frames[0]) / channels
	ms := 1000 * frameSamples / int(c.APISampleRate)
	payload := libopustest.NewOraclePayload("GSAI", uint32(c.APISampleRate), uint32(channels), uint32(ms), uint32(c.BitRate), uint32(boolToI32(c.UseCBR)), uint32(c.MaxBits), uint32(c.Complexity), uint32(len(frames)))
	inputs := make([][]float32, len(frames))
	for f, frame := range frames {
		if len(frame) != frameSamples*channels {
			t.Fatal("inconsistent frame length")
		}
		payload.U32(uint32(boolToI32(f == resetAt)))
		inputs[f] = make([]float32, len(frame))
		for i, sample := range frame {
			q := opusmath.Float32ToInt16(sample)
			payload.I16(q)
			inputs[f][i] = float32(q) * (1.0 / 32768.0)
		}
	}
	reader, err := libopustest.RunOracle(helper, payload.Bytes(), "fixed SILK API", "GSAO")
	if err != nil {
		t.Fatal(err)
	}
	if n := reader.Count(len(frames)); n != len(frames) {
		t.Fatalf("C packet count=%d want%d", n, len(frames))
	}
	t.Logf("selected C arch=%d", reader.U32())
	for f, frame := range inputs {
		n := reader.U32()
		rng, tell := reader.U32(), reader.U32()
		if n == 0 || n > maxSilkPacketBytes {
			t.Fatalf("C packet%d size=%d", f, n)
		}
		want := reader.Bytes(int(n))
		if f == resetAt {
			p.enc.Init()
			original.enc.Init()
		}
		got := p.encodeInto(t, frame, 1)
		unquantized := original.encodeInto(t, frames[f], 1)
		if len(got) == 0 || !bytes.Equal(got, unquantized) || p.re.Range() != original.re.Range() {
			t.Fatalf("packet%d signed16 input transport changes the original Go encoding", f)
		}
		if !bytes.Equal(got, want) || p.re.Range() != rng || uint32(p.re.Tell()) != tell {
			t.Fatalf("packet%d bytes=%d/%d range=%08x/%08x tell=%d/%d\nGo %x\nC  %x", f, len(got), len(want), p.re.Range(), rng, p.re.Tell(), tell, got, want)
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
}

func TestPublicSILKFixedStatefulComplexityParity(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, complexity := range []int32{0, 1, 2, 3, 5, 10} {
		for _, channels := range []int{1, 2} {
			t.Run(fmt.Sprintf("cx%d_ch%d", complexity, channels), func(t *testing.T) {
				p := newTestPacketEncoder(BandwidthWideband, channels)
				p.ctl.Complexity = complexity
				p.ctl.BitRate = int32(18000 * channels)
				p.ctl.MaxBits = p.ctl.BitRate / 50
				pcm := speechLikeSignal(16000, 320*6, channels)
				frames := make([][]float32, 6)
				for f := range frames {
					frames[f] = pcm[f*320*channels : (f+1)*320*channels]
				}
				assertFixedSILKAPI(t, p, frames, 3)
			})
		}
	}
}

const (
	libopusSILKFixedEncodeFrameInputMagic  = "GEFI"
	libopusSILKFixedEncodeFrameOutputMagic = "GEFO"
)

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
	binPath, err := buildFixedSILKOracle("libopus_silk_fixed_encode_frame_info.c", "encode_frame")
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
	frameLength := nbSubfr * subfrLength // 320
	ltpMemLength := 20 * fsKHz           // 320
	laPitch := laPitchMs * fsKHz         // 32
	laShape := laShapeMs * fsKHz         // 80
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

func TestPublicSILKEncodeFrameFixedByteExact(t *testing.T) {
	libopustest.RequireOracle(t)

	type kase struct {
		name      string
		bandwidth Bandwidth
		fsKHz     int
		frameMs   int
		cbr       bool
		bitrate   int
		gen       int
	}
	var cases []kase
	bws := []struct {
		bw    Bandwidth
		fsKHz int
	}{
		{BandwidthNarrowband, 8},
		{BandwidthMediumband, 12},
		{BandwidthWideband, 16},
	}
	for _, b := range bws {
		for _, ms := range []int{20, 10} {
			for _, cbr := range []bool{false, true} {
				for _, g := range []int{0, 2} {
					mode := "vbr"
					if cbr {
						mode = "cbr"
					}
					cases = append(cases, kase{
						name:      fmt.Sprintf("%s_%dms_%s_g%d", bwName(b.bw), ms, mode, g),
						bandwidth: b.bw,
						fsKHz:     b.fsKHz,
						frameMs:   ms,
						cbr:       cbr,
						bitrate:   18000,
						gen:       g,
					})
				}
			}
		}
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			frameSamples := c.frameMs * c.fsKHz

			pcm := make([]float32, frameSamples)
			for i := range pcm {
				tt := float64(i) / float64(c.fsKHz*1000)
				var v float64
				switch c.gen {
				case 0:
					v = 0.5*math.Sin(2*math.Pi*150*tt) + 0.2*math.Sin(2*math.Pi*450*tt)
				default:
					v = 0.4 * math.Sin(2*math.Pi*2500*tt) * (0.5 + 0.5*math.Sin(2*math.Pi*40*tt))
				}
				pcm[i] = float32(v * 0.35)
			}

			p := newTestPacketEncoder(c.bandwidth, 1)
			p.ctl.Complexity = 2
			p.ctl.BitRate = int32(c.bitrate)
			p.ctl.UseCBR = c.cbr
			p.ctl.MaxBits = int32(c.bitrate * c.frameMs / 1000)
			assertFixedSILKAPI(t, p, [][]float32{pcm}, -1)
			if !p.enc.state[0].vadFlags[0] {
				t.Fatal("test signal is not VAD active")
			}
		})
	}
}

func boolToI32(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

func bwName(b Bandwidth) string {
	switch b {
	case BandwidthNarrowband:
		return "nb"
	case BandwidthMediumband:
		return "mb"
	case BandwidthWideband:
		return "wb"
	}
	return "?"
}

const (
	libopusSILKFixedEncodeFramePayloadInputMagic  = "GEPI"
	libopusSILKFixedEncodeFramePayloadOutputMagic = "GEPO"
)

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
	binPath, err := buildFixedSILKOracle("libopus_silk_fixed_encode_frame_payload_info.c", "encode_frame_payload")
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
	pitchLPCWinLengthMs := 20 + (laPitchMs << 1)
	if nbSubfr != maxNbSubfr {
		pitchLPCWinLengthMs = 10 + (laPitchMs << 1)
	}
	pitchLPCWinLength := pitchLPCWinLengthMs * fsKHz
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
		c.condCoding = codeConditionally
		c.prevSignalType = 2
		c.prevLag = 100
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

var silkRes24API48kHelper libopustest.HelperCache

func buildSILKRes24API48kHelper() (string, error) {
	cfg := libopustest.CHelperConfig{
		Label:       "fixed SILK RES24 48 kHz API",
		OutputBase:  "gopus_silk_fixed_api_res24_48k",
		SourceFile:  "libopus_silk_fixed_api_res24_info.c",
		FixedRef:    true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3"},
		RefIncludes: []string{"celt", "silk", "silk/fixed"},
		Libs:        []string{libopustest.FixedRefPath(".libs", "libopus.a"), "-lm"},
	}
	if extsupport.QEXT {
		cfg.FixedRef = false
		cfg.FixedQEXTRef = true
		cfg.Libs = []string{libopustest.FixedQEXTRefPath(".libs", "libopus.a"), "-lm"}
	}
	return libopustest.BuildCHelper(cfg)
}

func TestSILKFixedRes24API48kCBRSequenceMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	helper, err := silkRes24API48kHelper.Path(buildSILKRes24API48kHelper)
	if err != nil {
		t.Fatal(err)
	}

	const (
		apiRate    = 48000
		frameSize  = 480
		frameMs    = 10
		frames     = 3
		complexity = 10
	)
	for _, tc := range []struct {
		name         string
		internalRate int
		bitrate      int
		maxBits      int
	}{
		{name: "narrowband", internalRate: 8000, bitrate: 15200, maxBits: 19 * 8},
		{name: "wideband", internalRate: 16000, bitrate: 31200, maxBits: 39 * 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pcm, err := testsignal.GenerateEncoderSignalVariant(testsignal.EncoderVariantAMMultisineV1,
				apiRate, frameSize*frames, 1)
			if err != nil {
				t.Fatal(err)
			}
			raw := make([]int32, len(pcm))
			for i, sample := range pcm {
				q := float32(math.Floor(0.5+float64(sample)*8388608.0) / 8388608.0)
				raw[i] = opusmath.Float32ToInt24(q)
			}
			filtered, _, err := libopustest.ProbeFixedDCReject(raw, [4]int32{}, apiRate, 1, 3)
			if err != nil {
				t.Fatal(err)
			}

			input := libopustest.NewOraclePayload("GSRQ",
				uint32(apiRate), uint32(tc.internalRate), uint32(1), uint32(frameMs),
				uint32(tc.bitrate), uint32(tc.maxBits), uint32(complexity), uint32(1), uint32(frames))
			input.I32s(filtered...)
			reader, err := libopustest.RunOracle(helper, input.Bytes(), "fixed SILK RES24 48 kHz API", "GSRP")
			if err != nil {
				t.Fatal(err)
			}
			if reader.Count(frames) != frames {
				t.Fatalf("unexpected C helper header")
			}
			selectedCArch := int32(reader.U32())
			t.Logf("selected C arch=%d", selectedCArch)

			enc := NewPacketEncoder(1)
			ctl := EncControl{
				NChannelsAPI: 1, NChannelsInternal: 1, APISampleRate: apiRate,
				MaxInternalSampleRate: 16000, MinInternalSampleRate: 8000,
				DesiredInternalSampleRate: int32(tc.internalRate), PayloadSizeMs: frameMs,
				BitRate: int32(tc.bitrate), Complexity: complexity, UseCBR: true,
				MaxBits: int32(tc.maxBits),
			}
			frame := make([]int32, frameSize)
			for f := range frames {
				copy(frame, filtered[f*frameSize:(f+1)*frameSize])
				cStatus := int32(reader.U32())
				cBytes := int(reader.U32())
				cRange, cTell := reader.U32(), reader.U32()
				cPacket := reader.Bytes(cBytes)
				cState := make([]int32, 17)
				for i := range cState {
					cState[i] = reader.I32()
				}
				cXBufCount := int(reader.U32())
				cXBuf := make([]int16, cXBufCount)
				for i := range cXBuf {
					cXBuf[i] = int16(reader.I32())
				}
				cPitchMeta := make([]int32, 6)
				for i := range cPitchMeta {
					cPitchMeta[i] = reader.I32()
				}
				if cStatus != 0 {
					t.Fatalf("frame %d selected C status=%d", f, cStatus)
				}

				storage := make([]byte, 1275)
				var re rangecoding.Encoder
				re.Init(storage)
				goBytes, err := enc.EncodeResQ8(&ctl, frame, frameSize, &re, 0, 1)
				if err != nil {
					t.Fatalf("frame %d EncodeResQ8: %v", f, err)
				}
				goRange, goTell := re.Range(), uint32(re.Tell())
				goPacket := re.Done()
				st := enc.state[0]
				fixed := st.fixed
				goState := []int32{
					fixed.frameCounter, fixed.prevSignalType, int32(st.lastQuantOffsetType), int32(st.lastSeed),
					st.speechActivityQ8, st.inputTiltQ15, fixed.sumLogGainQ7,
					fixed.prevSignalType, fixed.prevLag,
					fixed.ecPrevSignalType, int32(fixed.ecPrevLagIndex),
					int32(fixed.lastGainIndex), fixed.harmShapeGainSmthQ16, fixed.tiltSmthQ16,
					fixed.ltpCorrQ15, enc.nBitsExceeded, enc.nBitsUsedLBRR,
				}
				stateNames := [...]string{
					"frameCounter", "indices.signalType", "indices.quantOffsetType", "indices.Seed",
					"speech_activity_Q8", "input_tilt_Q15", "sum_log_gain_Q7", "prevSignalType",
					"prevLag", "ec_prevSignalType", "ec_prevLagIndex", "LastGainIndex",
					"HarmShapeGain_smth_Q16", "Tilt_smth_Q16", "LTPCorr_Q15", "nBitsExceeded",
					"nBitsUsedLBRR",
				}
				for i := range goState {
					if goState[i] != cState[i] {
						t.Fatalf("frame %d state %s Go=%d C=%d", f, stateNames[i], goState[i], cState[i])
					}
				}
				firstXBufDiff := -1
				for i := 0; i < min(len(fixed.xBuf), len(cXBuf)); i++ {
					if fixed.xBuf[i] != cXBuf[i] {
						firstXBufDiff = i
						break
					}
				}
				if len(fixed.xBuf) != len(cXBuf) || firstXBufDiff >= 0 {
					goValue, cValue := int16(0), int16(0)
					if firstXBufDiff >= 0 {
						goValue, cValue = fixed.xBuf[firstXBufDiff], cXBuf[firstXBufDiff]
					}
					t.Fatalf("frame %d x_buf differs Go=%d samples C=%d samples first=%d Go=%d C=%d",
						f, len(fixed.xBuf), len(cXBuf), firstXBufDiff, goValue, cValue)
				}
				pitchWin := int32((10 + 4) * tc.internalRate / 1000)
				goFirstReset := int32(0)
				if fixed.firstFrameAfterReset {
					goFirstReset = 1
				}
				goPitchMeta := []int32{
					st.pitchEstimationThresholdQ16, st.pitchEstimationComplexity, pitchWin,
					goFirstReset, 2,
				}
				pitchMetaNames := [...]string{
					"pitchEstimationThreshold_Q16", "pitchEstimationComplexity", "pitch_LPC_win_length",
					"first_frame_after_reset", "nb_subfr",
				}
				cPitchMetaIndices := [...]int{0, 1, 2, 3, 5}
				for i, cIndex := range cPitchMetaIndices {
					if cPitchMeta[cIndex] != goPitchMeta[i] {
						t.Fatalf("frame %d pitch metadata %s C=%d Go=%d", f, pitchMetaNames[i], cPitchMeta[cIndex], goPitchMeta[i])
					}
				}
				if cPitchMeta[4] != selectedCArch {
					t.Fatalf("frame %d selected C arch metadata=%d header=%d", f, cPitchMeta[4], selectedCArch)
				}
				if goBytes < 0 || int(goBytes) != cBytes || goRange != cRange || goTell != cTell ||
					!bytes.Equal(goPacket[:goBytes], cPacket) {
					first := 0
					for first < min(len(goPacket[:goBytes]), len(cPacket)) && goPacket[first] == cPacket[first] {
						first++
					}
					t.Fatalf("frame %d SILK API mismatch bytes=%d/%d range=%08x/%08x tell=%d/%d firstByte=%d Go=%x C=%x",
						f, goBytes, cBytes, goRange, cRange, goTell, cTell, first,
						goPacket[:goBytes], cPacket)
				}
				// opus_encode_frame_native carries switchReady into the next
				// silk_Encode call as opusCanSwitch.
				ctl.OpusCanSwitch = ctl.SwitchReady
			}
			if err := reader.ExpectConsumed(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPublicStereoSILKEncodeFixedByteExact(t *testing.T) {
	libopustest.RequireOracle(t)

	type kase struct {
		name      string
		bandwidth Bandwidth
		fsKHz     int
		frameMs   int
		nFrames   int
		cbr       bool
		bitrate   int
		gen       int // signal generator
	}
	var cases []kase
	bws := []struct {
		bw    Bandwidth
		fsKHz int
	}{
		{BandwidthNarrowband, 8},
		{BandwidthMediumband, 12},
		{BandwidthWideband, 16},
	}
	// SILK packets are built from 20 ms internal blocks (multi-frame = 40/60 ms)
	// or a single 10 ms block. 10 ms is therefore always single-frame; 20 ms
	// blocks exercise 1-, 2- and 3-frame (cross-frame stereo predictor) packets.
	type frameShape struct {
		ms      int
		nFrames int
	}
	shapes := []frameShape{{10, 1}, {20, 1}, {20, 2}, {20, 3}}
	for _, b := range bws {
		for _, sh := range shapes {
			for _, cbr := range []bool{false, true} {
				for _, g := range []int{0, 1, 2} {
					mode := "vbr"
					if cbr {
						mode = "cbr"
					}
					cases = append(cases, kase{
						name:      fmt.Sprintf("%s_%dms_%df_%s_g%d", bwName(b.bw), sh.ms, sh.nFrames, mode, g),
						bandwidth: b.bw,
						fsKHz:     b.fsKHz,
						frameMs:   sh.ms,
						nFrames:   sh.nFrames,
						cbr:       cbr,
						bitrate:   24000,
						gen:       g,
					})
				}
			}
			// Low-rate identical-channel case (gen 3) to drive the mid-only
			// (mono-collapse) decision and its cross-frame silentSideLen state.
			cases = append(cases, kase{
				name:      fmt.Sprintf("%s_%dms_%df_midonly", bwName(b.bw), sh.ms, sh.nFrames),
				bandwidth: b.bw,
				fsKHz:     b.fsKHz,
				frameMs:   sh.ms,
				nFrames:   sh.nFrames,
				cbr:       false,
				bitrate:   7000,
				gen:       3,
			})
		}
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			frameSamples := c.frameMs * c.fsKHz
			total := frameSamples * c.nFrames

			left := make([]float32, total)
			right := make([]float32, total)
			for i := 0; i < total; i++ {
				tt := float64(i) / float64(c.fsKHz*1000)
				var l, r float64
				switch c.gen {
				case 0:
					// Correlated tonal pair with a panning side component.
					common := 0.5 * math.Sin(2*math.Pi*180*tt)
					side := 0.25 * math.Sin(2*math.Pi*320*tt)
					l = common + side
					r = common - side
				case 1:
					// Near-mono (very high correlation) to drive mid-only.
					common := 0.55 * math.Sin(2*math.Pi*210*tt) * (0.5 + 0.5*math.Sin(2*math.Pi*7*tt))
					l = common
					r = common * 0.999
				case 2:
					// Decorrelated wideband-ish content (wide stereo image).
					l = 0.4 * math.Sin(2*math.Pi*1500*tt) * (0.5 + 0.5*math.Sin(2*math.Pi*33*tt))
					r = 0.4 * math.Sin(2*math.Pi*1900*tt+0.9) * (0.5 + 0.5*math.Sin(2*math.Pi*41*tt))
				default:
					// Identical channels (pure mono) at low rate -> mid-only collapse.
					common := 0.5 * math.Sin(2*math.Pi*200*tt) * (0.5 + 0.5*math.Sin(2*math.Pi*11*tt))
					l = common
					r = common
				}
				left[i] = float32(l * 0.35)
				right[i] = float32(r * 0.35)
			}

			p := newTestPacketEncoder(c.bandwidth, 2)
			p.ctl.Complexity = 2
			p.ctl.BitRate = int32(c.bitrate)
			p.ctl.UseCBR = c.cbr
			p.ctl.MaxBits = int32(c.bitrate * c.frameMs * c.nFrames / 1000)
			assertFixedSILKAPI(t, p, [][]float32{interleaveStereo(left, right)}, -1)
		})
	}
}
