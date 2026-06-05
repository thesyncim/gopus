//go:build gopus_fixed_point

package silk

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

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
