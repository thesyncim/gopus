//go:build gopus_fixedpoint

package silk

import (
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusSILKFixedDecodeCoreInputMagic  = "GSDI"
	libopusSILKFixedDecodeCoreOutputMagic = "GSDO"
)

type silkFixedDecodeCoreCase struct {
	name        string
	voiced      bool
	order       int
	subfrLength int
	lag         int
	gainQ10     int32
	aQ12        []int16 // length order
	bQ14        []int16 // length ltpOrderConst
	sLPCQ14     []int32 // length maxLPCOrder
	excQ14      []int32 // length subfrLength
	sLTPHist    []int32 // length lag+ltpOrderConst (voiced only), newest first
}

type silkFixedDecodeCoreResult struct {
	xq     []int16
	resQ14 []int32
}

func probeLibopusSILKFixedDecodeCore(cases []silkFixedDecodeCoreCase) ([]silkFixedDecodeCoreResult, error) {
	binPath, err := buildFixedSILKOracle("libopus_silk_fixed_decode_core_info.c", "decode_core")
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedDecodeCoreInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		voiced := uint32(0)
		if tc.voiced {
			voiced = 1
		}
		payload.U32(voiced)
		payload.U32(uint32(tc.order))
		payload.U32(uint32(tc.subfrLength))
		payload.U32(uint32(tc.lag))
		payload.I32(tc.gainQ10)
		for _, v := range tc.aQ12 {
			payload.I16(v)
		}
		for _, v := range tc.bQ14 {
			payload.I16(v)
		}
		for _, v := range tc.sLPCQ14 {
			payload.I32(v)
		}
		for _, v := range tc.excQ14 {
			payload.I32(v)
		}
		if tc.voiced {
			for _, v := range tc.sLTPHist {
				payload.I32(v)
			}
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed decode_core", libopusSILKFixedDecodeCoreOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]silkFixedDecodeCoreResult, count)
	for i := range out {
		n := cases[i].subfrLength
		out[i].xq = make([]int16, n)
		for j := 0; j < n; j++ {
			out[i].xq[j] = reader.I16()
		}
		out[i].resQ14 = make([]int32, n)
		for j := 0; j < n; j++ {
			out[i].resQ14[j] = reader.I32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

// runFixedDecodeCoreCase reproduces the host-side synthesis using the gated
// fixed-point ports, mirroring the oracle's buffer layout.
func runFixedDecodeCoreCase(tc silkFixedDecodeCoreCase) silkFixedDecodeCoreResult {
	const sltpSize = 2048
	sLPC := make([]int32, maxLPCOrder+tc.subfrLength)
	copy(sLPC, tc.sLPCQ14)

	resQ14 := make([]int32, tc.subfrLength)
	xq := make([]int16, tc.subfrLength)

	var pres []int32
	if tc.voiced {
		sLTPQ15 := make([]int32, sltpSize)
		bufIdx := tc.lag + ltpOrderConst + 8
		for i := 0; i < tc.lag+ltpOrderConst; i++ {
			sLTPQ15[bufIdx-1-i] = tc.sLTPHist[i]
		}
		predLag := bufIdx - tc.lag + ltpOrderConst/2
		silkDecodeCoreLTPSynthesisFixed(sLTPQ15, bufIdx, predLag, tc.bQ14, tc.excQ14, resQ14, tc.subfrLength)
		pres = resQ14
	} else {
		pres = tc.excQ14
		copy(resQ14, tc.excQ14)
	}

	silkDecodeCoreShortTermFixed(sLPC, tc.aQ12, pres, xq, tc.gainQ10, tc.subfrLength, tc.order)
	return silkFixedDecodeCoreResult{xq: xq, resQ14: resQ14}
}

func TestSILKDecodeCoreSynthesisFixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0xDEC0))

	randI16 := func(scale int32) int16 { return int16(rng.Int31n(2*scale+1) - scale) }
	randI32 := func(scale int32) int32 { return rng.Int31n(2*scale+1) - scale }
	randCoefsA := func(order int) []int16 {
		a := make([]int16, order)
		for i := range a {
			a[i] = randI16(2048)
		}
		return a
	}
	randCoefsB := func() []int16 {
		b := make([]int16, ltpOrderConst)
		for i := range b {
			b[i] = randI16(8192)
		}
		return b
	}

	var cases []silkFixedDecodeCoreCase
	addCase := func(name string, voiced bool, order, subfrLength, lag int, excScale, sLPCScale int32) {
		exc := make([]int32, subfrLength)
		for i := range exc {
			exc[i] = randI32(excScale)
		}
		sLPC := make([]int32, maxLPCOrder)
		for i := range sLPC {
			sLPC[i] = randI32(sLPCScale)
		}
		tc := silkFixedDecodeCoreCase{
			name:        name,
			voiced:      voiced,
			order:       order,
			subfrLength: subfrLength,
			lag:         lag,
			gainQ10:     1 + rng.Int31n(1<<20),
			aQ12:        randCoefsA(order),
			bQ14:        randCoefsB(),
			sLPCQ14:     sLPC,
			excQ14:      exc,
		}
		if voiced {
			hist := make([]int32, lag+ltpOrderConst)
			for i := range hist {
				h := randI32(1 << 20)
				hist[i] = h
			}
			tc.sLTPHist = hist
		}
		cases = append(cases, tc)
	}

	for _, order := range []int{minLPCOrder, maxLPCOrder, 12} {
		for _, subfr := range []int{maxSubFrameLength, 40, 1} {
			for _, voiced := range []bool{false, true} {
				lag := 0
				if voiced {
					lag = 18 + rng.Intn(280)
				}
				addCase("rand", voiced, order, subfr, lag, 1<<24, 1<<24)
			}
		}
	}

	// Saturation stress: large excitation and state push the LSHIFT_SAT32 and
	// ADD_SAT32 boundaries; large gain stresses SMULWW/RSHIFT_ROUND/SAT16.
	for i := 0; i < 32; i++ {
		order := []int{minLPCOrder, maxLPCOrder}[rng.Intn(2)]
		subfr := maxSubFrameLength
		voiced := i%2 == 0
		lag := 0
		if voiced {
			lag = 18 + rng.Intn(280)
		}
		exc := make([]int32, subfr)
		for j := range exc {
			exc[j] = []int32{fixedTestMinInt32, fixedTestMaxInt32, randI32(1 << 28)}[rng.Intn(3)]
		}
		sLPC := make([]int32, maxLPCOrder)
		for j := range sLPC {
			sLPC[j] = []int32{fixedTestMinInt32, fixedTestMaxInt32, randI32(1 << 28)}[rng.Intn(3)]
		}
		a := make([]int16, order)
		for j := range a {
			a[j] = []int16{-32768, 32767, randI16(4096)}[rng.Intn(3)]
		}
		b := make([]int16, ltpOrderConst)
		for j := range b {
			b[j] = []int16{-32768, 32767, randI16(8192)}[rng.Intn(3)]
		}
		tc := silkFixedDecodeCoreCase{
			name:        "saturation",
			voiced:      voiced,
			order:       order,
			subfrLength: subfr,
			lag:         lag,
			gainQ10:     1 + rng.Int31n(1<<24),
			aQ12:        a,
			bQ14:        b,
			sLPCQ14:     sLPC,
			excQ14:      exc,
		}
		if voiced {
			hist := make([]int32, lag+ltpOrderConst)
			for j := range hist {
				hist[j] = []int32{fixedTestMinInt32, fixedTestMaxInt32, randI32(1 << 28)}[rng.Intn(3)]
			}
			tc.sLTPHist = hist
		}
		cases = append(cases, tc)
	}

	want, err := probeLibopusSILKFixedDecodeCore(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed decode_core", err)
		return
	}

	for i, tc := range cases {
		got := runFixedDecodeCoreCase(tc)
		for j := range got.xq {
			if got.xq[j] != want[i].xq[j] {
				t.Fatalf("case %d (%s voiced=%v order=%d subfr=%d lag=%d): xq[%d]=%d want %d",
					i, tc.name, tc.voiced, tc.order, tc.subfrLength, tc.lag, j, got.xq[j], want[i].xq[j])
			}
		}
		for j := range got.resQ14 {
			if got.resQ14[j] != want[i].resQ14[j] {
				t.Fatalf("case %d (%s voiced=%v order=%d subfr=%d lag=%d): res_Q14[%d]=%d want %d",
					i, tc.name, tc.voiced, tc.order, tc.subfrLength, tc.lag, j, got.resQ14[j], want[i].resQ14[j])
			}
		}
	}
}
