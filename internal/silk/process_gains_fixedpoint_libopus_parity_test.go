//go:build gopus_fixed_point

package silk

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusSILKFixedProcessGainsInputMagic  = "GPGI"
	libopusSILKFixedProcessGainsOutputMagic = "GPGO"
)

type silkFixedProcessGainsCase struct {
	name                   string
	signalType             int32
	nbSubfr                int
	subfrLength            int32
	snrDBQ7                int32
	inputTiltQ15           int32
	nStatesDelayedDecision int32
	speechActivityQ8       int32
	quantOffsetType        int32
	condCoding             int32
	ltpredCodGainQ7        int32
	inputQualityQ14        int32
	codingQualityQ14       int32
	lastGainIndex          int8
	gainsQ16               []int32
	resNrg                 []int32
	resNrgQ                []int32
}

type silkFixedProcessGainsResult struct {
	gainsQ16          []int32
	gainsIndices      []int32
	gainsUnqQ16       []int32
	lastGainIndexPrev int32
	lastGainIndex     int32
	quantOffsetType   int32
	lambdaQ10         int32
}

func probeLibopusSILKFixedProcessGains(cases []silkFixedProcessGainsCase) ([]silkFixedProcessGainsResult, error) {
	binPath, err := buildFixedSILKOracle("libopus_silk_fixed_process_gains_info.c", "process_gains")
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedProcessGainsInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.signalType))
		payload.U32(uint32(tc.nbSubfr))
		payload.U32(uint32(tc.quantOffsetType))
		payload.U32(uint32(tc.condCoding))
		payload.I32(tc.subfrLength)
		payload.I32(tc.snrDBQ7)
		payload.I32(tc.inputTiltQ15)
		payload.I32(tc.nStatesDelayedDecision)
		payload.I32(tc.speechActivityQ8)
		payload.I32(tc.ltpredCodGainQ7)
		payload.I32(tc.inputQualityQ14)
		payload.I32(tc.codingQualityQ14)
		payload.I32(int32(tc.lastGainIndex))
		for k := 0; k < tc.nbSubfr; k++ {
			payload.I32(tc.gainsQ16[k])
			payload.I32(tc.resNrg[k])
			payload.I32(tc.resNrgQ[k])
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed process gains", libopusSILKFixedProcessGainsOutputMagic)
	if err != nil {
		return nil, err
	}
	cnt := reader.Count(len(cases))
	out := make([]silkFixedProcessGainsResult, cnt)
	for i := range out {
		nb := cases[i].nbSubfr
		out[i].gainsQ16 = make([]int32, nb)
		for k := range out[i].gainsQ16 {
			out[i].gainsQ16[k] = reader.I32()
		}
		out[i].gainsIndices = make([]int32, nb)
		for k := range out[i].gainsIndices {
			out[i].gainsIndices[k] = reader.I32()
		}
		out[i].gainsUnqQ16 = make([]int32, nb)
		for k := range out[i].gainsUnqQ16 {
			out[i].gainsUnqQ16[k] = reader.I32()
		}
		out[i].lastGainIndexPrev = reader.I32()
		out[i].lastGainIndex = reader.I32()
		out[i].quantOffsetType = reader.I32()
		out[i].lambdaQ10 = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKProcessGainsFixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0x9a1f5))

	var cases []silkFixedProcessGainsCase

	newCase := func(name string, signalType int32, nbSubfr int, cond int32) silkFixedProcessGainsCase {
		tc := silkFixedProcessGainsCase{
			name:                   name,
			signalType:             signalType,
			nbSubfr:                nbSubfr,
			subfrLength:            int32(40 + 40*rng.Intn(4)), // 40,80,120,160
			snrDBQ7:                int32(10*128 + rng.Intn(30*128)),
			inputTiltQ15:           int32(rng.Intn(32768) - 16384),
			nStatesDelayedDecision: int32(1 + rng.Intn(4)),
			speechActivityQ8:       int32(rng.Intn(257)),
			quantOffsetType:        int32(rng.Intn(2)),
			condCoding:             cond,
			ltpredCodGainQ7:        int32(rng.Intn(20*128) - 4*128),
			inputQualityQ14:        int32(rng.Intn(16385)),
			codingQualityQ14:       int32(rng.Intn(16385)),
			lastGainIndex:          int8(rng.Intn(64)),
		}
		tc.gainsQ16 = make([]int32, nbSubfr)
		tc.resNrg = make([]int32, nbSubfr)
		tc.resNrgQ = make([]int32, nbSubfr)
		for k := 0; k < nbSubfr; k++ {
			tc.gainsQ16[k] = int32(1 + rng.Intn(1<<24))
			tc.resNrg[k] = rng.Int31()
			tc.resNrgQ[k] = int32(rng.Intn(31) - 8)
		}
		return tc
	}

	signalTypes := []int32{typeNoVoiceActivity, typeUnvoiced, typeVoiced}
	condModes := []int32{codeIndependently, codeConditionally}

	for _, st := range signalTypes {
		for _, nb := range []int{2, 4} {
			for _, cond := range condModes {
				cases = append(cases, newCase("std", st, nb, cond))
			}
		}
	}

	// Edge: tiny gains forcing the high-precision sqrt branch.
	for i := 0; i < 8; i++ {
		tc := newCase("tinygain", typeVoiced, 4, codeIndependently)
		for k := range tc.gainsQ16 {
			tc.gainsQ16[k] = int32(1 + rng.Intn(64))
			tc.resNrg[k] = int32(rng.Intn(16))
			tc.resNrgQ[k] = int32(rng.Intn(8))
		}
		cases = append(cases, tc)
	}

	// Edge: large residual energy with positive/negative Q to stress the
	// shift/saturation branches.
	for i := 0; i < 8; i++ {
		tc := newCase("bigrnrg", typeUnvoiced, 4, codeConditionally)
		for k := range tc.gainsQ16 {
			tc.gainsQ16[k] = int32(1 + rng.Intn(1<<28))
			tc.resNrg[k] = silk_int32_MAX - int32(rng.Intn(1024))
			tc.resNrgQ[k] = int32(rng.Intn(20) - 18)
		}
		cases = append(cases, tc)
	}

	// Bulk random coverage.
	for i := 0; i < 128; i++ {
		st := signalTypes[rng.Intn(len(signalTypes))]
		nb := 2 + 2*rng.Intn(2)
		cond := condModes[rng.Intn(len(condModes))]
		cases = append(cases, newCase("bulk", st, nb, cond))
	}

	want, err := probeLibopusSILKFixedProcessGains(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed process gains", err)
		return
	}

	for i, tc := range cases {
		gains := make([]int32, tc.nbSubfr)
		copy(gains, tc.gainsQ16)
		resNrg := make([]int32, tc.nbSubfr)
		copy(resNrg, tc.resNrg)
		resNrgQ := make([]int32, tc.nbSubfr)
		copy(resNrgQ, tc.resNrgQ)

		p := silkProcessGainsParams{
			signalType:             tc.signalType,
			nbSubfr:                tc.nbSubfr,
			subfrLength:            tc.subfrLength,
			snrDBQ7:                tc.snrDBQ7,
			inputTiltQ15:           tc.inputTiltQ15,
			nStatesDelayedDecision: tc.nStatesDelayedDecision,
			speechActivityQ8:       tc.speechActivityQ8,
			quantOffsetType:        tc.quantOffsetType,
			ltpredCodGainQ7:        tc.ltpredCodGainQ7,
			inputQualityQ14:        tc.inputQualityQ14,
			codingQualityQ14:       tc.codingQualityQ14,
			gainsQ16:               gains,
			resNrg:                 resNrg,
			resNrgQ:                resNrgQ,
			lastGainIndex:          tc.lastGainIndex,
			condCoding:             tc.condCoding,
		}
		res := silkProcessGainsFixed(&silkFixedEncodeScratch{}, &p)

		fail := func(field string, got, exp interface{}) {
			t.Fatalf("case %d (%s st=%d nb=%d cond=%d): %s=%v want %v",
				i, tc.name, tc.signalType, tc.nbSubfr, tc.condCoding, field, got, exp)
		}

		for k := 0; k < tc.nbSubfr; k++ {
			if gains[k] != want[i].gainsQ16[k] {
				fail(fmt.Sprintf("Gains_Q16[%d]", k), gains[k], want[i].gainsQ16[k])
			}
			if int32(res.gainsIndices[k]) != want[i].gainsIndices[k] {
				fail(fmt.Sprintf("GainsIndices[%d]", k), res.gainsIndices[k], want[i].gainsIndices[k])
			}
			if res.gainsUnqQ16[k] != want[i].gainsUnqQ16[k] {
				fail(fmt.Sprintf("GainsUnq_Q16[%d]", k), res.gainsUnqQ16[k], want[i].gainsUnqQ16[k])
			}
		}
		if int32(res.lastGainIndexPrev) != want[i].lastGainIndexPrev {
			fail("lastGainIndexPrev", res.lastGainIndexPrev, want[i].lastGainIndexPrev)
		}
		if int32(res.lastGainIndex) != want[i].lastGainIndex {
			fail("lastGainIndex", res.lastGainIndex, want[i].lastGainIndex)
		}
		if res.quantOffsetType != want[i].quantOffsetType {
			fail("quantOffsetType", res.quantOffsetType, want[i].quantOffsetType)
		}
		if res.lambdaQ10 != want[i].lambdaQ10 {
			fail("Lambda_Q10", res.lambdaQ10, want[i].lambdaQ10)
		}
	}
}
