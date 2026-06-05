//go:build gopus_fixed_point

package silk

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
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
