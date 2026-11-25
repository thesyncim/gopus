//go:build gopus_fixed_point

package silk

import (
	"fmt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"math/rand"
	"testing"
)

const (
	libopusSILKFixedNSQDelDecInputMagic  = "GDDI"
	libopusSILKFixedNSQDelDecOutputMagic = "GDDO"

	libopusSILKFixedNSQDelDecScaleInputMagic  = "GDSI"
	libopusSILKFixedNSQDelDecScaleOutputMagic = "GDSO"
)

type silkFixedNSQDelDecCase struct {
	name            string
	length          int
	signalType      int
	predictLPCOrder int
	shapingLPCOrder int
	nStates         int
	lag             int
	harmShapeFIR    int32
	tilt            int32
	lfShp           int32
	gain            int32
	lambda          int32
	offset          int
	warping         int32
	subfr           int
	smplBufIdx      int
	decisionDelay   int
	sLTPShpBufIdx   int
	sLTPBufIdx      int
	aQ12            [16]int16
	bQ14            [5]int16
	arShpQ13        [24]int16
	xQ10            []int32
	sLTPShpQ14      [nsqSLTPShpLen]int32
	sLTPQ15         [nsqSLTPQ15Len]int32
	delayedGainQ10  [decisionDelay]int32
	states          []nsqDelDecStateFixed
}

type silkFixedNSQDelDecResult struct {
	pulses         []int8
	xq             []int16
	sLTPQ15        []int32
	sLTPShpQ14     []int32
	delayedGainQ10 []int32
	sLTPShpBufIdx  int32
	sLTPBufIdx     int32
	smplBufIdx     int32
	states         []nsqDelDecStateFixed
}

func payloadState(payload *libopustest.OraclePayload, d *nsqDelDecStateFixed) {
	for _, v := range d.sLPCQ14 {
		payload.I32(v)
	}
	for _, v := range d.randState {
		payload.I32(v)
	}
	for _, v := range d.qQ10 {
		payload.I32(v)
	}
	for _, v := range d.xqQ14 {
		payload.I32(v)
	}
	for _, v := range d.predQ15 {
		payload.I32(v)
	}
	for _, v := range d.shapeQ14 {
		payload.I32(v)
	}
	for _, v := range d.sAR2Q14 {
		payload.I32(v)
	}
	payload.I32(d.lfARQ14)
	payload.I32(d.diffQ14)
	payload.I32(d.seed)
	payload.I32(d.seedInit)
	payload.I32(d.rdQ10)
}

func readState(reader *libopustest.OracleReader, d *nsqDelDecStateFixed) {
	for j := range d.sLPCQ14 {
		d.sLPCQ14[j] = reader.I32()
	}
	for j := range d.randState {
		d.randState[j] = reader.I32()
	}
	for j := range d.qQ10 {
		d.qQ10[j] = reader.I32()
	}
	for j := range d.xqQ14 {
		d.xqQ14[j] = reader.I32()
	}
	for j := range d.predQ15 {
		d.predQ15[j] = reader.I32()
	}
	for j := range d.shapeQ14 {
		d.shapeQ14[j] = reader.I32()
	}
	for j := range d.sAR2Q14 {
		d.sAR2Q14[j] = reader.I32()
	}
	d.lfARQ14 = reader.I32()
	d.diffQ14 = reader.I32()
	d.seed = reader.I32()
	d.seedInit = reader.I32()
	d.rdQ10 = reader.I32()
}

func probeLibopusSILKFixedNSQDelDec(cases []silkFixedNSQDelDecCase) ([]silkFixedNSQDelDecResult, error) {
	binPath, err := buildFixedSILKOracle("libopus_silk_fixed_nsq_del_dec_info.c", "nsq_del_dec")
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedNSQDelDecInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.length))
		payload.U32(uint32(tc.signalType))
		payload.U32(uint32(tc.predictLPCOrder))
		payload.U32(uint32(tc.shapingLPCOrder))
		payload.U32(uint32(tc.nStates))
		payload.I32(int32(tc.lag))
		payload.I32(tc.harmShapeFIR)
		payload.I32(tc.tilt)
		payload.I32(tc.lfShp)
		payload.I32(tc.gain)
		payload.I32(tc.lambda)
		payload.I32(int32(tc.offset))
		payload.I32(tc.warping)
		payload.I32(int32(tc.subfr))
		payload.I32(int32(tc.smplBufIdx))
		payload.I32(int32(tc.decisionDelay))
		payload.I32(int32(tc.sLTPShpBufIdx))
		payload.I32(int32(tc.sLTPBufIdx))
		for _, v := range tc.aQ12 {
			payload.I16(v)
		}
		for _, v := range tc.bQ14 {
			payload.I16(v)
		}
		for _, v := range tc.arShpQ13 {
			payload.I16(v)
		}
		for i := 0; i < tc.length; i++ {
			payload.I32(tc.xQ10[i])
		}
		for _, v := range tc.sLTPShpQ14 {
			payload.I32(v)
		}
		for _, v := range tc.sLTPQ15 {
			payload.I32(v)
		}
		for _, v := range tc.delayedGainQ10 {
			payload.I32(v)
		}
		for k := 0; k < tc.nStates; k++ {
			payloadState(payload, &tc.states[k])
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed nsq del dec", libopusSILKFixedNSQDelDecOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]silkFixedNSQDelDecResult, count)
	for i := range out {
		n := decisionDelay + cases[i].length
		r := silkFixedNSQDelDecResult{
			pulses:         make([]int8, n),
			xq:             make([]int16, n),
			sLTPQ15:        make([]int32, nsqSLTPQ15Len),
			sLTPShpQ14:     make([]int32, nsqSLTPShpLen),
			delayedGainQ10: make([]int32, decisionDelay),
			states:         make([]nsqDelDecStateFixed, cases[i].nStates),
		}
		for j := 0; j < n; j++ {
			r.pulses[j] = int8(reader.I16())
		}
		for j := 0; j < n; j++ {
			r.xq[j] = reader.I16()
		}
		for j := range r.sLTPQ15 {
			r.sLTPQ15[j] = reader.I32()
		}
		for j := range r.sLTPShpQ14 {
			r.sLTPShpQ14[j] = reader.I32()
		}
		for j := range r.delayedGainQ10 {
			r.delayedGainQ10[j] = reader.I32()
		}
		r.sLTPShpBufIdx = reader.I32()
		r.sLTPBufIdx = reader.I32()
		r.smplBufIdx = reader.I32()
		for k := 0; k < cases[i].nStates; k++ {
			readState(reader, &r.states[k])
		}
		out[i] = r
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKNoiseShapeQuantizerDelDecFixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0x4444)) // "DD"

	r16 := func(amp int32) int16 { return int16(rng.Int31n(2*amp+1) - amp) }
	r32 := func(amp int32) int32 { return rng.Int31n(2*amp+1) - amp }

	makeCase := func(name string, voiced bool, predOrder, shapeOrder, length, nStates, subfr int) silkFixedNSQDelDecCase {
		var tc silkFixedNSQDelDecCase
		tc.name = name
		tc.length = length
		tc.predictLPCOrder = predOrder
		tc.shapingLPCOrder = shapeOrder
		tc.nStates = nStates
		tc.subfr = subfr
		tc.offset = []int{offsetVLQ10, offsetVHQ10, offsetUVLQ10, offsetUVHQ10}[rng.Intn(4)]
		tc.harmShapeFIR = r32(1 << 14)
		tc.tilt = r32(1 << 14)
		tc.lfShp = r32(1 << 26)
		tc.gain = 1<<16 + rng.Int31n(1<<22)
		tc.lambda = rng.Int31n(8192)
		tc.warping = r32(1 << 16)

		// Decision delay must be in [1, length] and < DECISION_DELAY so the ring
		// buffer indexing and the smpl_buf_idx + decisionDelay wrap stay valid.
		maxDD := decisionDelay - 1
		if maxDD > length {
			maxDD = length
		}
		if maxDD < 1 {
			maxDD = 1
		}
		tc.decisionDelay = 1 + rng.Intn(maxDD)
		tc.smplBufIdx = rng.Intn(decisionDelay)

		if voiced {
			tc.signalType = typeVoiced
			tc.lag = 32 + rng.Intn(160)
		} else {
			tc.signalType = 0
			tc.lag = 0
		}
		// Place buffer indices so lag-relative reads stay in range and the
		// length writes do not overrun the buffers.
		tc.sLTPShpBufIdx = ltpMemLength
		tc.sLTPBufIdx = ltpMemLength

		for i := range tc.aQ12 {
			tc.aQ12[i] = r16(4096)
		}
		for i := range tc.bQ14 {
			tc.bQ14[i] = r16(8192)
		}
		for i := range tc.arShpQ13 {
			tc.arShpQ13[i] = r16(8192)
		}
		tc.xQ10 = make([]int32, length)
		for i := range tc.xQ10 {
			tc.xQ10[i] = r32(1 << 19)
		}
		for i := range tc.sLTPShpQ14 {
			tc.sLTPShpQ14[i] = r32(1 << 22)
		}
		for i := range tc.sLTPQ15 {
			tc.sLTPQ15[i] = r32(1 << 23)
		}
		for i := range tc.delayedGainQ10 {
			tc.delayedGainQ10[i] = r32(1 << 20)
		}

		tc.states = make([]nsqDelDecStateFixed, nStates)
		for k := 0; k < nStates; k++ {
			d := &tc.states[k]
			for i := range d.sLPCQ14 {
				d.sLPCQ14[i] = r32(1 << 22)
			}
			for i := range d.randState {
				// Mostly distinct random states so the expired-state penalty and
				// survivor pruning paths are exercised; occasionally collide.
				if rng.Intn(4) == 0 {
					d.randState[i] = 0
				} else {
					d.randState[i] = int32(rng.Uint32())
				}
			}
			for i := range d.qQ10 {
				d.qQ10[i] = r32(1 << 15)
			}
			for i := range d.xqQ14 {
				d.xqQ14[i] = r32(1 << 22)
			}
			for i := range d.predQ15 {
				d.predQ15[i] = r32(1 << 23)
			}
			for i := range d.shapeQ14 {
				d.shapeQ14[i] = r32(1 << 22)
			}
			for i := range d.sAR2Q14 {
				d.sAR2Q14[i] = r32(1 << 22)
			}
			d.lfARQ14 = r32(1 << 20)
			d.diffQ14 = r32(1 << 20)
			d.seed = int32(rng.Uint32())
			d.seedInit = d.seed
			d.rdQ10 = rng.Int31n(1 << 20)
		}
		return tc
	}

	var cases []silkFixedNSQDelDecCase
	for _, voiced := range []bool{false, true} {
		for _, predOrder := range []int{10, 16} {
			for _, nStates := range []int{1, 2, 4} {
				for _, length := range []int{2, 16, 40, 80} {
					for _, subfr := range []int{0, 1} {
						name := fmt.Sprintf("v=%t/pred=%d/states=%d/len=%d/sf=%d", voiced, predOrder, nStates, length, subfr)
						cases = append(cases, makeCase(name, voiced, predOrder, 16, length, nStates, subfr))
					}
				}
			}
		}
	}
	for i := 0; i < 128; i++ {
		voiced := rng.Intn(2) == 1
		predOrder := []int{10, 16}[rng.Intn(2)]
		shapeOrder := 2 * (1 + rng.Intn(12)) // 2..24, even
		nStates := []int{1, 2, 3, 4}[rng.Intn(4)]
		length := 2 + rng.Intn(78)
		subfr := rng.Intn(2)
		tc := makeCase(fmt.Sprintf("bulk-%d", i), voiced, predOrder, shapeOrder, length, nStates, subfr)
		if rng.Intn(2) == 0 {
			tc.lambda = 2049 + rng.Int31n(60000) // force RDO branch
		}
		cases = append(cases, tc)
	}

	want, err := probeLibopusSILKFixedNSQDelDec(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed nsq del dec", err)
		return
	}

	for i, tc := range cases {
		nsq := &NSQState{
			sLTPShpQ14:    tc.sLTPShpQ14,
			sLTPBufIdx:    tc.sLTPBufIdx,
			sLTPShpBufIdx: tc.sLTPShpBufIdx,
		}
		// Output buffers carry a decisionDelay prefix; pulseOffset=decisionDelay
		// shifts the kernel's [i-decisionDelay] writes to non-negative offsets.
		pulsesBuf := make([]int8, decisionDelay+tc.length)
		xqBuf := make([]int16, decisionDelay+tc.length)

		sLTPQ15 := make([]int32, nsqSLTPQ15Len)
		copy(sLTPQ15, tc.sLTPQ15[:])
		delayedGainQ10 := tc.delayedGainQ10
		states := make([]nsqDelDecStateFixed, tc.nStates)
		copy(states, tc.states)

		smplBufIdx := tc.smplBufIdx
		silkNoiseShapeQuantizerDelDecFixed(&silkFixedEncodeScratch{}, nsq, states, tc.signalType, tc.xQ10, pulsesBuf, xqBuf, sLTPQ15,
			delayedGainQ10[:], tc.aQ12[:], tc.bQ14[:], tc.arShpQ13[:], tc.lag, tc.harmShapeFIR,
			tc.tilt, tc.lfShp, tc.gain, tc.lambda, tc.offset, tc.length, tc.subfr,
			tc.shapingLPCOrder, tc.predictLPCOrder, tc.warping, tc.nStates, &smplBufIdx, tc.decisionDelay, decisionDelay)

		w := want[i]
		for j := range pulsesBuf {
			if pulsesBuf[j] != w.pulses[j] {
				t.Fatalf("case %d (%s): pulses[%d]=%d want %d", i, tc.name, j, pulsesBuf[j], w.pulses[j])
			}
			if xqBuf[j] != w.xq[j] {
				t.Fatalf("case %d (%s): xq[%d]=%d want %d", i, tc.name, j, xqBuf[j], w.xq[j])
			}
		}
		for j := range sLTPQ15 {
			if sLTPQ15[j] != w.sLTPQ15[j] {
				t.Fatalf("case %d (%s): sLTPQ15[%d]=%d want %d", i, tc.name, j, sLTPQ15[j], w.sLTPQ15[j])
			}
		}
		for j := range nsq.sLTPShpQ14 {
			if nsq.sLTPShpQ14[j] != w.sLTPShpQ14[j] {
				t.Fatalf("case %d (%s): sLTPShpQ14[%d]=%d want %d", i, tc.name, j, nsq.sLTPShpQ14[j], w.sLTPShpQ14[j])
			}
		}
		for j := range delayedGainQ10 {
			if delayedGainQ10[j] != w.delayedGainQ10[j] {
				t.Fatalf("case %d (%s): delayedGainQ10[%d]=%d want %d", i, tc.name, j, delayedGainQ10[j], w.delayedGainQ10[j])
			}
		}
		if int32(nsq.sLTPShpBufIdx) != w.sLTPShpBufIdx {
			t.Fatalf("case %d (%s): sLTPShpBufIdx=%d want %d", i, tc.name, nsq.sLTPShpBufIdx, w.sLTPShpBufIdx)
		}
		if int32(nsq.sLTPBufIdx) != w.sLTPBufIdx {
			t.Fatalf("case %d (%s): sLTPBufIdx=%d want %d", i, tc.name, nsq.sLTPBufIdx, w.sLTPBufIdx)
		}
		if int32(smplBufIdx) != w.smplBufIdx {
			t.Fatalf("case %d (%s): smplBufIdx=%d want %d", i, tc.name, smplBufIdx, w.smplBufIdx)
		}
		for k := 0; k < tc.nStates; k++ {
			gd := &states[k]
			cd := &w.states[k]
			for j := range gd.sLPCQ14 {
				if gd.sLPCQ14[j] != cd.sLPCQ14[j] {
					t.Fatalf("case %d (%s): state %d sLPCQ14[%d]=%d want %d", i, tc.name, k, j, gd.sLPCQ14[j], cd.sLPCQ14[j])
				}
			}
			for j := range gd.randState {
				if gd.randState[j] != cd.randState[j] {
					t.Fatalf("case %d (%s): state %d randState[%d]=%d want %d", i, tc.name, k, j, gd.randState[j], cd.randState[j])
				}
			}
			for j := range gd.qQ10 {
				if gd.qQ10[j] != cd.qQ10[j] {
					t.Fatalf("case %d (%s): state %d qQ10[%d]=%d want %d", i, tc.name, k, j, gd.qQ10[j], cd.qQ10[j])
				}
			}
			for j := range gd.xqQ14 {
				if gd.xqQ14[j] != cd.xqQ14[j] {
					t.Fatalf("case %d (%s): state %d xqQ14[%d]=%d want %d", i, tc.name, k, j, gd.xqQ14[j], cd.xqQ14[j])
				}
			}
			for j := range gd.predQ15 {
				if gd.predQ15[j] != cd.predQ15[j] {
					t.Fatalf("case %d (%s): state %d predQ15[%d]=%d want %d", i, tc.name, k, j, gd.predQ15[j], cd.predQ15[j])
				}
			}
			for j := range gd.shapeQ14 {
				if gd.shapeQ14[j] != cd.shapeQ14[j] {
					t.Fatalf("case %d (%s): state %d shapeQ14[%d]=%d want %d", i, tc.name, k, j, gd.shapeQ14[j], cd.shapeQ14[j])
				}
			}
			for j := range gd.sAR2Q14 {
				if gd.sAR2Q14[j] != cd.sAR2Q14[j] {
					t.Fatalf("case %d (%s): state %d sAR2Q14[%d]=%d want %d", i, tc.name, k, j, gd.sAR2Q14[j], cd.sAR2Q14[j])
				}
			}
			if gd.lfARQ14 != cd.lfARQ14 {
				t.Fatalf("case %d (%s): state %d lfARQ14=%d want %d", i, tc.name, k, gd.lfARQ14, cd.lfARQ14)
			}
			if gd.diffQ14 != cd.diffQ14 {
				t.Fatalf("case %d (%s): state %d diffQ14=%d want %d", i, tc.name, k, gd.diffQ14, cd.diffQ14)
			}
			if gd.seed != cd.seed {
				t.Fatalf("case %d (%s): state %d seed=%d want %d", i, tc.name, k, gd.seed, cd.seed)
			}
			if gd.rdQ10 != cd.rdQ10 {
				t.Fatalf("case %d (%s): state %d rdQ10=%d want %d", i, tc.name, k, gd.rdQ10, cd.rdQ10)
			}
		}
	}
}

type silkFixedNSQDelDecScaleCase struct {
	name          string
	subfrLength   int
	signalType    int
	nStates       int
	rewhite       int
	subfr         int
	ltpScaleQ14   int32
	decisionDelay int
	ltpMemLen     int
	prevGainQ16   int32
	sLTPShpBufIdx int
	sLTPBufIdx    int
	gains         [maxNbSubfr]int32
	pitchL        [maxNbSubfr]int32
	x16           []int16
	sLTP          [nsqSLTPQ15Len]int16
	sLTPShpQ14    [nsqSLTPShpLen]int32
	sLTPQ15       [nsqSLTPQ15Len]int32
	states        []nsqDelDecStateFixed
}

type silkFixedNSQDelDecScaleResult struct {
	xScQ10      []int32
	sLTPShpQ14  []int32
	sLTPQ15     []int32
	prevGainQ16 int32
	states      []nsqDelDecStateFixed
}

func probeLibopusSILKFixedNSQDelDecScale(cases []silkFixedNSQDelDecScaleCase) ([]silkFixedNSQDelDecScaleResult, error) {
	binPath, err := buildFixedSILKOracle("libopus_silk_fixed_nsq_del_dec_scale_info.c", "nsq_del_dec_scale")
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedNSQDelDecScaleInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.subfrLength))
		payload.U32(uint32(tc.signalType))
		payload.U32(uint32(tc.nStates))
		payload.U32(uint32(tc.rewhite))
		payload.I32(int32(tc.subfr))
		payload.I32(tc.ltpScaleQ14)
		payload.I32(int32(tc.decisionDelay))
		payload.I32(int32(tc.ltpMemLen))
		payload.I32(tc.prevGainQ16)
		payload.I32(int32(tc.sLTPShpBufIdx))
		payload.I32(int32(tc.sLTPBufIdx))
		for _, v := range tc.gains {
			payload.I32(v)
		}
		for _, v := range tc.pitchL {
			payload.I32(v)
		}
		for i := 0; i < tc.subfrLength; i++ {
			payload.I16(tc.x16[i])
		}
		for _, v := range tc.sLTP {
			payload.I16(v)
		}
		for _, v := range tc.sLTPShpQ14 {
			payload.I32(v)
		}
		for _, v := range tc.sLTPQ15 {
			payload.I32(v)
		}
		for k := 0; k < tc.nStates; k++ {
			payloadState(payload, &tc.states[k])
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed nsq del dec scale", libopusSILKFixedNSQDelDecScaleOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]silkFixedNSQDelDecScaleResult, count)
	for i := range out {
		r := silkFixedNSQDelDecScaleResult{
			xScQ10:     make([]int32, cases[i].subfrLength),
			sLTPShpQ14: make([]int32, nsqSLTPShpLen),
			sLTPQ15:    make([]int32, nsqSLTPQ15Len),
			states:     make([]nsqDelDecStateFixed, cases[i].nStates),
		}
		for j := range r.xScQ10 {
			r.xScQ10[j] = reader.I32()
		}
		for j := range r.sLTPShpQ14 {
			r.sLTPShpQ14[j] = reader.I32()
		}
		for j := range r.sLTPQ15 {
			r.sLTPQ15[j] = reader.I32()
		}
		r.prevGainQ16 = reader.I32()
		for k := 0; k < cases[i].nStates; k++ {
			readState(reader, &r.states[k])
		}
		out[i] = r
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKNSQDelDecScaleStatesFixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0x44535453)) // "DSTS"
	r16 := func(amp int32) int16 { return int16(rng.Int31n(2*amp+1) - amp) }
	r32 := func(amp int32) int32 { return rng.Int31n(2*amp+1) - amp }

	makeCase := func(name string, voiced bool, subfr, nStates int, rewhite bool, gainChange bool) silkFixedNSQDelDecScaleCase {
		var tc silkFixedNSQDelDecScaleCase
		tc.name = name
		tc.subfrLength = []int{40, 80}[rng.Intn(2)]
		tc.nStates = nStates
		tc.subfr = subfr
		tc.ltpScaleQ14 = int32(rng.Int31n(1 << 14))
		tc.decisionDelay = 1 + rng.Intn(decisionDelay-1)
		tc.ltpMemLen = ltpMemLength
		tc.sLTPShpBufIdx = ltpMemLength
		tc.sLTPBufIdx = ltpMemLength
		if rewhite {
			tc.rewhite = 1
		}
		if voiced {
			tc.signalType = typeVoiced
		}
		for i := range tc.gains {
			tc.gains[i] = 1<<16 + rng.Int31n(1<<22)
		}
		for i := range tc.pitchL {
			tc.pitchL[i] = int32(32 + rng.Intn(160))
		}
		if gainChange {
			tc.prevGainQ16 = 1<<16 + rng.Int31n(1<<22)
		} else {
			tc.prevGainQ16 = tc.gains[subfr]
		}
		tc.x16 = make([]int16, tc.subfrLength)
		for i := range tc.x16 {
			tc.x16[i] = r16(1 << 14)
		}
		for i := range tc.sLTP {
			tc.sLTP[i] = r16(1 << 14)
		}
		for i := range tc.sLTPShpQ14 {
			tc.sLTPShpQ14[i] = r32(1 << 22)
		}
		for i := range tc.sLTPQ15 {
			tc.sLTPQ15[i] = r32(1 << 23)
		}
		tc.states = make([]nsqDelDecStateFixed, nStates)
		for k := 0; k < nStates; k++ {
			d := &tc.states[k]
			for i := range d.sLPCQ14 {
				d.sLPCQ14[i] = r32(1 << 22)
			}
			for i := range d.predQ15 {
				d.predQ15[i] = r32(1 << 23)
			}
			for i := range d.shapeQ14 {
				d.shapeQ14[i] = r32(1 << 22)
			}
			for i := range d.sAR2Q14 {
				d.sAR2Q14[i] = r32(1 << 22)
			}
			d.lfARQ14 = r32(1 << 20)
			d.diffQ14 = r32(1 << 20)
		}
		return tc
	}

	var cases []silkFixedNSQDelDecScaleCase
	idx := 0
	for _, voiced := range []bool{false, true} {
		for _, nStates := range []int{1, 2, 4} {
			for _, rewhite := range []bool{false, true} {
				for _, gc := range []bool{false, true} {
					for _, subfr := range []int{0, 1, 3} {
						name := fmt.Sprintf("v=%t/states=%d/rw=%t/gc=%t/sf=%d", voiced, nStates, rewhite, gc, subfr)
						cases = append(cases, makeCase(name, voiced, subfr, nStates, rewhite, gc))
						idx++
					}
				}
			}
		}
	}

	want, err := probeLibopusSILKFixedNSQDelDecScale(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed nsq del dec scale", err)
		return
	}

	for i, tc := range cases {
		nsq := &NSQState{
			sLTPShpQ14:    tc.sLTPShpQ14,
			sLTPBufIdx:    tc.sLTPBufIdx,
			sLTPShpBufIdx: tc.sLTPShpBufIdx,
			prevGainQ16:   tc.prevGainQ16,
			rewhiteFlag:   tc.rewhite,
		}
		xScQ10 := make([]int32, tc.subfrLength)
		sLTPQ15 := make([]int32, nsqSLTPQ15Len)
		copy(sLTPQ15, tc.sLTPQ15[:])
		states := make([]nsqDelDecStateFixed, tc.nStates)
		copy(states, tc.states)

		silkNSQDelDecScaleStatesFixed(nsq, states, tc.x16, xScQ10, tc.sLTP[:], sLTPQ15,
			tc.subfr, tc.nStates, tc.ltpScaleQ14, tc.gains[:], tc.pitchL[:], tc.signalType,
			tc.decisionDelay, tc.subfrLength, tc.ltpMemLen)

		w := want[i]
		for j := range xScQ10 {
			if xScQ10[j] != w.xScQ10[j] {
				t.Fatalf("case %d (%s): xScQ10[%d]=%d want %d", i, tc.name, j, xScQ10[j], w.xScQ10[j])
			}
		}
		for j := range sLTPQ15 {
			if sLTPQ15[j] != w.sLTPQ15[j] {
				t.Fatalf("case %d (%s): sLTPQ15[%d]=%d want %d", i, tc.name, j, sLTPQ15[j], w.sLTPQ15[j])
			}
		}
		for j := range nsq.sLTPShpQ14 {
			if nsq.sLTPShpQ14[j] != w.sLTPShpQ14[j] {
				t.Fatalf("case %d (%s): sLTPShpQ14[%d]=%d want %d", i, tc.name, j, nsq.sLTPShpQ14[j], w.sLTPShpQ14[j])
			}
		}
		if nsq.prevGainQ16 != w.prevGainQ16 {
			t.Fatalf("case %d (%s): prevGainQ16=%d want %d", i, tc.name, nsq.prevGainQ16, w.prevGainQ16)
		}
		for k := 0; k < tc.nStates; k++ {
			gd := &states[k]
			cd := &w.states[k]
			for j := 0; j < nsqLpcBufLength; j++ {
				if gd.sLPCQ14[j] != cd.sLPCQ14[j] {
					t.Fatalf("case %d (%s): state %d sLPCQ14[%d]=%d want %d", i, tc.name, k, j, gd.sLPCQ14[j], cd.sLPCQ14[j])
				}
			}
			for j := range gd.sAR2Q14 {
				if gd.sAR2Q14[j] != cd.sAR2Q14[j] {
					t.Fatalf("case %d (%s): state %d sAR2Q14[%d]=%d want %d", i, tc.name, k, j, gd.sAR2Q14[j], cd.sAR2Q14[j])
				}
			}
			for j := range gd.predQ15 {
				if gd.predQ15[j] != cd.predQ15[j] {
					t.Fatalf("case %d (%s): state %d predQ15[%d]=%d want %d", i, tc.name, k, j, gd.predQ15[j], cd.predQ15[j])
				}
			}
			for j := range gd.shapeQ14 {
				if gd.shapeQ14[j] != cd.shapeQ14[j] {
					t.Fatalf("case %d (%s): state %d shapeQ14[%d]=%d want %d", i, tc.name, k, j, gd.shapeQ14[j], cd.shapeQ14[j])
				}
			}
			if gd.lfARQ14 != cd.lfARQ14 {
				t.Fatalf("case %d (%s): state %d lfARQ14=%d want %d", i, tc.name, k, gd.lfARQ14, cd.lfARQ14)
			}
			if gd.diffQ14 != cd.diffQ14 {
				t.Fatalf("case %d (%s): state %d diffQ14=%d want %d", i, tc.name, k, gd.diffQ14, cd.diffQ14)
			}
		}
	}
}

const (
	libopusSILKFixedNSQDelDecOuterInputMagic  = "GDXI"
	libopusSILKFixedNSQDelDecOuterOutputMagic = "GDXO"
)

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
	binPath, err := buildFixedSILKOracle("libopus_silk_fixed_nsq_del_dec_outer_info.c", "nsq_del_dec_outer")
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

const (
	libopusSILKFixedNSQInputMagic  = "GNQI"
	libopusSILKFixedNSQOutputMagic = "GNQO"

	// Mirror the C oracle buffer sizes (silk/structs.h + define.h, MAX_FS_KHZ=16).
	nsqSLTPShpLen = 2 * maxFrameLengthNSQ               // sLTP_shp_Q14
	nsqSLPCLen    = maxSubFrameLength + nsqLpcBufLength // sLPC_Q14
	nsqSLTPQ15Len = 2 * maxFrameLengthNSQ               // sLTP_Q15
)

type silkFixedNSQCase struct {
	name            string
	length          int
	signalType      int
	predictLPCOrder int
	shapingLPCOrder int
	lag             int
	harmShapeFIR    int32
	tilt            int32
	lfShp           int32
	gain            int32
	lambda          int32
	offset          int
	sLTPShpBufIdx   int
	sLTPBufIdx      int
	randSeed        int32
	sLFARShp        int32
	sDiffShp        int32
	aQ12            [16]int16
	bQ14            [5]int16
	arShpQ13        [24]int16
	xScQ10          []int32
	sLPCQ14         [nsqSLPCLen]int32
	sAR2Q14         [24]int32
	sLTPShpQ14      [nsqSLTPShpLen]int32
	sLTPQ15         [nsqSLTPQ15Len]int32
}

type silkFixedNSQResult struct {
	pulses        []int8
	xq            []int16
	sLTPQ15       []int32
	sLPCQ14       []int32
	sAR2Q14       []int32
	sLTPShpQ14    []int32
	sLTPShpBufIdx int32
	sLTPBufIdx    int32
	randSeed      int32
	sLFARShp      int32
	sDiffShp      int32
}

func probeLibopusSILKFixedNSQ(cases []silkFixedNSQCase) ([]silkFixedNSQResult, error) {
	binPath, err := buildFixedSILKOracle("libopus_silk_fixed_nsq_info.c", "nsq")
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedNSQInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.length))
		payload.U32(uint32(tc.signalType))
		payload.U32(uint32(tc.predictLPCOrder))
		payload.U32(uint32(tc.shapingLPCOrder))
		payload.I32(int32(tc.lag))
		payload.I32(tc.harmShapeFIR)
		payload.I32(tc.tilt)
		payload.I32(tc.lfShp)
		payload.I32(tc.gain)
		payload.I32(tc.lambda)
		payload.I32(int32(tc.offset))
		payload.I32(int32(tc.sLTPShpBufIdx))
		payload.I32(int32(tc.sLTPBufIdx))
		payload.I32(tc.randSeed)
		payload.I32(tc.sLFARShp)
		payload.I32(tc.sDiffShp)
		for _, v := range tc.aQ12 {
			payload.I16(v)
		}
		for _, v := range tc.bQ14 {
			payload.I16(v)
		}
		for _, v := range tc.arShpQ13 {
			payload.I16(v)
		}
		for i := 0; i < tc.length; i++ {
			payload.I32(tc.xScQ10[i])
		}
		for _, v := range tc.sLPCQ14 {
			payload.I32(v)
		}
		for _, v := range tc.sAR2Q14 {
			payload.I32(v)
		}
		for _, v := range tc.sLTPShpQ14 {
			payload.I32(v)
		}
		for _, v := range tc.sLTPQ15 {
			payload.I32(v)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed nsq", libopusSILKFixedNSQOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]silkFixedNSQResult, count)
	for i := range out {
		n := cases[i].length
		r := silkFixedNSQResult{
			pulses:     make([]int8, n),
			xq:         make([]int16, n),
			sLTPQ15:    make([]int32, nsqSLTPQ15Len),
			sLPCQ14:    make([]int32, nsqSLPCLen),
			sAR2Q14:    make([]int32, 24),
			sLTPShpQ14: make([]int32, nsqSLTPShpLen),
		}
		for j := 0; j < n; j++ {
			r.pulses[j] = int8(reader.I16())
		}
		for j := 0; j < n; j++ {
			r.xq[j] = reader.I16()
		}
		for j := range r.sLTPQ15 {
			r.sLTPQ15[j] = reader.I32()
		}
		for j := range r.sLPCQ14 {
			r.sLPCQ14[j] = reader.I32()
		}
		for j := range r.sAR2Q14 {
			r.sAR2Q14[j] = reader.I32()
		}
		for j := range r.sLTPShpQ14 {
			r.sLTPShpQ14[j] = reader.I32()
		}
		r.sLTPShpBufIdx = reader.I32()
		r.sLTPBufIdx = reader.I32()
		r.randSeed = reader.I32()
		r.sLFARShp = reader.I32()
		r.sDiffShp = reader.I32()
		out[i] = r
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKNoiseShapeQuantizerFixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0x4e5351)) // "NSQ"

	r16 := func(amp int32) int16 { return int16(rng.Int31n(2*amp+1) - amp) }
	r32 := func(amp int32) int32 { return rng.Int31n(2*amp+1) - amp }

	makeCase := func(name string, voiced bool, predOrder, shapeOrder, length int) silkFixedNSQCase {
		var tc silkFixedNSQCase
		tc.name = name
		tc.length = length
		tc.predictLPCOrder = predOrder
		tc.shapingLPCOrder = shapeOrder
		tc.offset = []int{offsetVLQ10, offsetVHQ10, offsetUVLQ10, offsetUVHQ10}[rng.Intn(4)]
		tc.harmShapeFIR = r32(1 << 14)
		tc.tilt = r32(1 << 14)
		tc.lfShp = r32(1 << 26)
		tc.gain = 1<<16 + rng.Int31n(1<<22) // positive, > 0
		tc.lambda = rng.Int31n(8192)        // exercise both Lambda<=2048 and RDO branch
		tc.randSeed = int32(rng.Uint32())
		tc.sLFARShp = r32(1 << 20)
		tc.sDiffShp = r32(1 << 20)

		// Place the buffer indices so the lag-relative reads stay in range and
		// the length writes do not overrun the buffers. For voiced frames lag
		// must be > 0 (celt_assert); pick a lag that leaves headroom for the
		// HARM_SHAPE_FIR_TAPS/2 and LTP_ORDER/2 offsets and the +1 advances.
		if voiced {
			tc.signalType = typeVoiced
			tc.lag = 32 + rng.Intn(160) // 32..191
		} else {
			tc.signalType = 0
			tc.lag = 0
		}
		// sLTPShpBufIdx-lag+1 >= 0 and (idx+length-1) < nsqSLTPShpLen.
		tc.sLTPShpBufIdx = ltpMemLength // 320, mirrors NSQ start of subframe
		tc.sLTPBufIdx = ltpMemLength

		for i := range tc.aQ12 {
			tc.aQ12[i] = r16(4096)
		}
		for i := range tc.bQ14 {
			tc.bQ14[i] = r16(8192)
		}
		for i := range tc.arShpQ13 {
			tc.arShpQ13[i] = r16(8192)
		}
		tc.xScQ10 = make([]int32, length)
		for i := range tc.xScQ10 {
			tc.xScQ10[i] = r32(1 << 19)
		}
		for i := range tc.sLPCQ14 {
			tc.sLPCQ14[i] = r32(1 << 22)
		}
		for i := range tc.sAR2Q14 {
			tc.sAR2Q14[i] = r32(1 << 22)
		}
		for i := range tc.sLTPShpQ14 {
			tc.sLTPShpQ14[i] = r32(1 << 22)
		}
		for i := range tc.sLTPQ15 {
			tc.sLTPQ15[i] = r32(1 << 23)
		}
		return tc
	}

	var cases []silkFixedNSQCase
	// Cover voiced/unvoiced, both LPC orders, and several subframe lengths.
	for _, voiced := range []bool{false, true} {
		for _, predOrder := range []int{10, 16} {
			for _, length := range []int{1, 16, 40, 80} {
				name := fmt.Sprintf("v=%t/pred=%d/len=%d", voiced, predOrder, length)
				cases = append(cases, makeCase(name, voiced, predOrder, 16, length))
			}
		}
	}
	// Bulk randomized coverage, including odd shaping orders (even only) and
	// the aggressive RDO Lambda branch.
	for i := 0; i < 96; i++ {
		voiced := rng.Intn(2) == 1
		predOrder := []int{10, 16}[rng.Intn(2)]
		shapeOrder := 2 * (1 + rng.Intn(12)) // 2..24, even
		length := 1 + rng.Intn(80)
		tc := makeCase(fmt.Sprintf("bulk-%d", i), voiced, predOrder, shapeOrder, length)
		if rng.Intn(2) == 0 {
			tc.lambda = 2049 + rng.Int31n(60000) // force RDO branch
		}
		cases = append(cases, tc)
	}

	want, err := probeLibopusSILKFixedNSQ(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed nsq", err)
		return
	}

	for i, tc := range cases {
		nsq := &NSQState{
			sLPCQ14:       tc.sLPCQ14,
			sAR2Q14:       tc.sAR2Q14,
			sLTPShpQ14:    tc.sLTPShpQ14,
			sLFARShpQ14:   tc.sLFARShp,
			sDiffShpQ14:   tc.sDiffShp,
			sLTPBufIdx:    tc.sLTPBufIdx,
			sLTPShpBufIdx: tc.sLTPShpBufIdx,
			randSeed:      tc.randSeed,
		}
		pulses := make([]int8, tc.length)
		xq := make([]int16, tc.length)
		sLTPQ15 := make([]int32, nsqSLTPQ15Len)
		copy(sLTPQ15, tc.sLTPQ15[:])
		aQ12 := tc.aQ12[:]
		bQ14 := tc.bQ14[:]
		arShpQ13 := tc.arShpQ13[:]

		silkNoiseShapeQuantizerFixed(nsq, tc.signalType, tc.xScQ10, pulses, xq, sLTPQ15,
			aQ12, bQ14, arShpQ13, tc.lag, tc.harmShapeFIR, tc.tilt, tc.lfShp, tc.gain,
			tc.lambda, tc.offset, tc.length, tc.shapingLPCOrder, tc.predictLPCOrder)

		w := want[i]
		for j := 0; j < tc.length; j++ {
			if pulses[j] != w.pulses[j] {
				t.Fatalf("case %d (%s): pulses[%d]=%d want %d", i, tc.name, j, pulses[j], w.pulses[j])
			}
			if xq[j] != w.xq[j] {
				t.Fatalf("case %d (%s): xq[%d]=%d want %d", i, tc.name, j, xq[j], w.xq[j])
			}
		}
		for j := range sLTPQ15 {
			if sLTPQ15[j] != w.sLTPQ15[j] {
				t.Fatalf("case %d (%s): sLTPQ15[%d]=%d want %d", i, tc.name, j, sLTPQ15[j], w.sLTPQ15[j])
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
		for j := range nsq.sLTPShpQ14 {
			if nsq.sLTPShpQ14[j] != w.sLTPShpQ14[j] {
				t.Fatalf("case %d (%s): sLTPShpQ14[%d]=%d want %d", i, tc.name, j, nsq.sLTPShpQ14[j], w.sLTPShpQ14[j])
			}
		}
		if int32(nsq.sLTPShpBufIdx) != w.sLTPShpBufIdx {
			t.Fatalf("case %d (%s): sLTPShpBufIdx=%d want %d", i, tc.name, nsq.sLTPShpBufIdx, w.sLTPShpBufIdx)
		}
		if int32(nsq.sLTPBufIdx) != w.sLTPBufIdx {
			t.Fatalf("case %d (%s): sLTPBufIdx=%d want %d", i, tc.name, nsq.sLTPBufIdx, w.sLTPBufIdx)
		}
		if nsq.randSeed != w.randSeed {
			t.Fatalf("case %d (%s): randSeed=%d want %d", i, tc.name, nsq.randSeed, w.randSeed)
		}
		if nsq.sLFARShpQ14 != w.sLFARShp {
			t.Fatalf("case %d (%s): sLFARShpQ14=%d want %d", i, tc.name, nsq.sLFARShpQ14, w.sLFARShp)
		}
		if nsq.sDiffShpQ14 != w.sDiffShp {
			t.Fatalf("case %d (%s): sDiffShpQ14=%d want %d", i, tc.name, nsq.sDiffShpQ14, w.sDiffShp)
		}
	}
}

const (
	libopusSILKFixedNSQOuterInputMagic  = "GNOI"
	libopusSILKFixedNSQOuterOutputMagic = "GNOO"

	// Mirror the C oracle buffer sizes (silk/structs.h + define.h, MAX_FS_KHZ=16).
	nsqOuterXQLen      = 2 * maxFrameLengthNSQ               // xq
	nsqOuterSLTPShpLen = 2 * maxFrameLengthNSQ               // sLTP_shp_Q14
	nsqOuterSLPCLen    = maxSubFrameLength + nsqLpcBufLength // sLPC_Q14
)

type silkFixedNSQOuterCase struct {
	name            string
	nbSubfr         int
	frameLength     int
	subfrLength     int
	ltpMemLength    int
	predictLPCOrder int
	shapingLPCOrder int

	signalType       int
	quantOffsetType  int
	nlsfInterpCoefQ2 int
	seed             int
	lambdaQ10        int32
	ltpScaleQ14      int32

	lagPrev       int
	sLTPBufIdx    int
	sLTPShpBufIdx int
	randSeed      int32
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

type silkFixedNSQOuterResult struct {
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
	randSeed      int32
	prevGainQ16   int32
	rewhiteFlag   int32
}

func probeLibopusSILKFixedNSQOuter(cases []silkFixedNSQOuterCase) ([]silkFixedNSQOuterResult, error) {
	binPath, err := buildFixedSILKOracle("libopus_silk_fixed_nsq_outer_info.c", "nsq_outer")
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedNSQOuterInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.nbSubfr))
		payload.U32(uint32(tc.frameLength))
		payload.U32(uint32(tc.subfrLength))
		payload.U32(uint32(tc.ltpMemLength))
		payload.U32(uint32(tc.predictLPCOrder))
		payload.U32(uint32(tc.shapingLPCOrder))
		payload.I32(int32(tc.signalType))
		payload.I32(int32(tc.quantOffsetType))
		payload.I32(int32(tc.nlsfInterpCoefQ2))
		payload.I32(int32(tc.seed))
		payload.I32(tc.lambdaQ10)
		payload.I32(tc.ltpScaleQ14)
		payload.I32(int32(tc.lagPrev))
		payload.I32(int32(tc.sLTPBufIdx))
		payload.I32(int32(tc.sLTPShpBufIdx))
		payload.I32(tc.randSeed)
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

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed nsq outer", libopusSILKFixedNSQOuterOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]silkFixedNSQOuterResult, count)
	for i := range out {
		n := cases[i].frameLength
		r := silkFixedNSQOuterResult{
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
		r.randSeed = reader.I32()
		r.prevGainQ16 = reader.I32()
		r.rewhiteFlag = reader.I32()
		out[i] = r
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKNSQFixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0x4e53514f)) // "NSQO"

	r16 := func(amp int32) int16 { return int16(rng.Int31n(2*amp+1) - amp) }
	r32 := func(amp int32) int32 { return rng.Int31n(2*amp+1) - amp }

	makeCase := func(name string, voiced bool, nbSubfr, subfrLength, predOrder, shapeOrder, nlsfInterp int, gainChange bool) silkFixedNSQOuterCase {
		var tc silkFixedNSQOuterCase
		tc.name = name
		tc.nbSubfr = nbSubfr
		tc.subfrLength = subfrLength
		tc.frameLength = nbSubfr * subfrLength
		tc.ltpMemLength = ltpMemLength // 320
		tc.predictLPCOrder = predOrder
		tc.shapingLPCOrder = shapeOrder
		tc.nlsfInterpCoefQ2 = nlsfInterp
		tc.quantOffsetType = rng.Intn(2)
		tc.seed = rng.Intn(4) // libopus Seed is 2-bit (int8, used as initial rand_seed)
		tc.lambdaQ10 = rng.Int31n(8192)
		tc.ltpScaleQ14 = []int32{15565, 12288, 8192}[rng.Intn(3)] // silk_LTPScales_table_Q14
		tc.prevGainQ16 = 1<<16 + rng.Int31n(1<<22)
		tc.sLFARShp = r32(1 << 20)
		tc.sDiffShp = r32(1 << 20)
		tc.lagPrev = 32 + rng.Intn(160)
		tc.rewhiteFlag = 0

		if voiced {
			tc.signalType = typeVoiced
		} else {
			tc.signalType = rng.Intn(2) // 0=inactive, 1=unvoiced
		}

		// Pitch lag held constant across the frame: subframe 0 rewhitening (and
		// the changing-gain rescale) only scale the LTP history window sized to
		// this lag, so a later voiced subframe's LTP prediction must read inside
		// that same window. A constant lag mirrors real pitch contours and keeps
		// every pred_lag read inside an initialized region; varying lag per
		// subframe would have a later read fall into uninitialized sLTP_Q15
		// (undefined stack in libopus), which is not a meaningful comparison.
		// start_idx = ltp_mem - lag - predOrder - LTP_ORDER/2 > 0 (celt_assert).
		lag := int32(20 + rng.Intn(tc.ltpMemLength-predOrder-ltpOrderConst/2-21))
		for k := 0; k < maxNbSubfr; k++ {
			// HarmShapeGain must be >= 0 (silk_assert in NSQ.c).
			tc.harmShapeGainQ14[k] = rng.Int31n(1 << 14)
			tc.tiltQ14[k] = r32(1 << 14)
			tc.lfShpQ14[k] = r32(1 << 26)
			if gainChange {
				// Vary gains per subframe to exercise the changing-gain rescale.
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
		// xq history feeds the voiced rewhitening LPC analysis filter.
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

	var cases []silkFixedNSQOuterCase
	// Structured coverage: voiced/unvoiced, both prediction orders, both NLSF
	// interpolation flags (4 disables interp), 2 and 4 subframes, gain changes.
	for _, voiced := range []bool{false, true} {
		for _, predOrder := range []int{10, 16} {
			for _, nlsfInterp := range []int{2, 4} {
				for _, cfg := range []struct {
					nbSubfr, subfrLength int
				}{{2, 40}, {4, 40}, {4, 80}} {
					for _, gainChange := range []bool{false, true} {
						name := fmt.Sprintf("v=%t/pred=%d/interp=%d/nb=%d/sub=%d/gc=%t",
							voiced, predOrder, nlsfInterp, cfg.nbSubfr, cfg.subfrLength, gainChange)
						cases = append(cases, makeCase(name, voiced, cfg.nbSubfr, cfg.subfrLength,
							predOrder, 16, nlsfInterp, gainChange))
					}
				}
			}
		}
	}
	// Randomized bulk coverage including odd-but-even shaping orders and the
	// aggressive RDO Lambda branch.
	for i := 0; i < 96; i++ {
		voiced := rng.Intn(2) == 1
		predOrder := []int{10, 16}[rng.Intn(2)]
		shapeOrder := 2 * (3 + rng.Intn(10)) // 6..24, even, >=6
		nlsfInterp := []int{0, 1, 2, 3, 4}[rng.Intn(5)]
		nbSubfr := []int{2, 4}[rng.Intn(2)]
		subfrLength := []int{40, 80}[rng.Intn(2)]
		gainChange := rng.Intn(2) == 0
		tc := makeCase(fmt.Sprintf("bulk-%d", i), voiced, nbSubfr, subfrLength,
			predOrder, shapeOrder, nlsfInterp, gainChange)
		if rng.Intn(2) == 0 {
			tc.lambdaQ10 = 2049 + rng.Int31n(60000) // force RDO branch
		}
		cases = append(cases, tc)
	}

	want, err := probeLibopusSILKFixedNSQOuter(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed nsq outer", err)
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
			randSeed:      tc.randSeed,
			prevGainQ16:   tc.prevGainQ16,
			rewhiteFlag:   tc.rewhiteFlag,
		}
		pulses := make([]int8, tc.frameLength)

		silkNSQFixed(
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
		if nsq.randSeed != w.randSeed {
			t.Fatalf("case %d (%s): randSeed=%d want %d", i, tc.name, nsq.randSeed, w.randSeed)
		}
		if nsq.prevGainQ16 != w.prevGainQ16 {
			t.Fatalf("case %d (%s): prevGainQ16=%d want %d", i, tc.name, nsq.prevGainQ16, w.prevGainQ16)
		}
		if int32(nsq.rewhiteFlag) != w.rewhiteFlag {
			t.Fatalf("case %d (%s): rewhiteFlag=%d want %d", i, tc.name, nsq.rewhiteFlag, w.rewhiteFlag)
		}
	}
}
