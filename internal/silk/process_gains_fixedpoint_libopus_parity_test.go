//go:build gopus_fixed_point

package silk

import (
	"fmt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"math/rand"
	"sort"
	"testing"
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

const (
	libopusSILKProcessNLSFsInputMagic  = "PNLI"
	libopusSILKProcessNLSFsOutputMagic = "PNLO"
)

type silkProcessNLSFsCase struct {
	name              string
	order             int
	nbSubfr           int
	signalType        int32
	useInterp         int32
	nlsfMSVQSurvivors int
	speechActivityQ8  int32
	nlsfInterpCoefQ2  int32
	nlsfQ15           []int16
	prevNLSFQ15       []int16
}

type silkProcessNLSFsResultWire struct {
	predCoef0   []int32
	predCoef1   []int32
	nlsfIndices []int32
	nlsfQ15     []int32
}

func probeLibopusSILKProcessNLSFs(cases []silkProcessNLSFsCase) ([]silkProcessNLSFsResultWire, error) {
	binPath, err := buildFixedSILKOracle("libopus_silk_process_nlsfs_info.c", "process_nlsfs")
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKProcessNLSFsInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.order))
		payload.U32(uint32(tc.nbSubfr))
		payload.U32(uint32(tc.signalType))
		payload.U32(uint32(tc.useInterp))
		payload.U32(uint32(tc.nlsfMSVQSurvivors))
		payload.I32(tc.speechActivityQ8)
		payload.I32(tc.nlsfInterpCoefQ2)
		for i := 0; i < tc.order; i++ {
			payload.I32(int32(tc.nlsfQ15[i]))
		}
		for i := 0; i < tc.order; i++ {
			payload.I32(int32(tc.prevNLSFQ15[i]))
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk process nlsfs", libopusSILKProcessNLSFsOutputMagic)
	if err != nil {
		return nil, err
	}
	cnt := reader.Count(len(cases))
	out := make([]silkProcessNLSFsResultWire, cnt)
	for i := range out {
		order := cases[i].order
		out[i].predCoef0 = make([]int32, order)
		for k := range out[i].predCoef0 {
			out[i].predCoef0[k] = reader.I32()
		}
		out[i].predCoef1 = make([]int32, order)
		for k := range out[i].predCoef1 {
			out[i].predCoef1[k] = reader.I32()
		}
		out[i].nlsfIndices = make([]int32, order+1)
		for k := range out[i].nlsfIndices {
			out[i].nlsfIndices[k] = reader.I32()
		}
		out[i].nlsfQ15 = make([]int32, order)
		for k := range out[i].nlsfQ15 {
			out[i].nlsfQ15[k] = reader.I32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

// makeStableNLSF produces a monotonically increasing NLSF_Q15 vector with a
// generous minimum spacing so the encoder's stabilizer does not have to clamp.
func makeStableNLSF(rng *rand.Rand, order int) []int16 {
	out := make([]int16, order)
	// Distribute roughly evenly across [0, 32768) with jitter.
	step := 32768 / (order + 1)
	pos := 0
	for i := 0; i < order; i++ {
		pos += step
		jitter := rng.Intn(step/2+1) - step/4
		v := pos + jitter
		if v < 1 {
			v = 1
		}
		if v > 32767 {
			v = 32767
		}
		out[i] = int16(v)
	}
	sortInt16(out)
	return out
}

// makeMarginalNLSF produces a vector with tight / out-of-order spacing to
// exercise the stabilizer path inside silk_NLSF_encode.
func makeMarginalNLSF(rng *rand.Rand, order int) []int16 {
	out := make([]int16, order)
	for i := 0; i < order; i++ {
		out[i] = int16(rng.Intn(32768))
	}
	// Cluster a couple of entries to force tiny deltas.
	if order >= 4 {
		out[1] = out[0]
		out[3] = out[2] + int16(rng.Intn(3))
	}
	return out
}

func sortInt16(a []int16) {
	sort.Slice(a, func(i, j int) bool { return a[i] < a[j] })
}

func TestSILKProcessNLSFsFixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0x5c1a7))

	var cases []silkProcessNLSFsCase

	survivorsFor := func(order int) int {
		if order == 16 {
			return 16
		}
		return 8
	}

	addCase := func(name string, order int, nbSubfr int, signalType, useInterp, interpCoef int32, marginal bool) {
		var nlsf []int16
		if marginal {
			nlsf = makeMarginalNLSF(rng, order)
		} else {
			nlsf = makeStableNLSF(rng, order)
		}
		prev := makeStableNLSF(rng, order)
		cases = append(cases, silkProcessNLSFsCase{
			name:              name,
			order:             order,
			nbSubfr:           nbSubfr,
			signalType:        signalType,
			useInterp:         useInterp,
			nlsfMSVQSurvivors: survivorsFor(order),
			speechActivityQ8:  int32(rng.Intn(257)),
			nlsfInterpCoefQ2:  interpCoef,
			nlsfQ15:           nlsf,
			prevNLSFQ15:       prev,
		})
	}

	signalTypes := []int32{typeNoVoiceActivity, typeUnvoiced, typeVoiced}
	orders := []int{10, 16}

	// Interpolation off (useInterp=0, coef forced to 4 == no interpolation) and
	// on (useInterp=1, coef 0..3), across orders, subframe counts, signal types,
	// and both stable and marginal NLSF sets.
	for _, order := range orders {
		for _, st := range signalTypes {
			for _, nb := range []int{2, 4} {
				// No interpolation: coef must be 4 when nb_subfr==4 / useInterp==0.
				addCase("nointerp", order, nb, st, 0, 4, false)
				addCase("nointerp-marg", order, nb, st, 0, 4, true)
				if nb == 4 {
					for _, coef := range []int32{0, 1, 2, 3} {
						addCase("interp", order, nb, st, 1, coef, false)
						addCase("interp-marg", order, nb, st, 1, coef, true)
					}
				}
			}
		}
	}

	// Bulk random coverage.
	for i := 0; i < 200; i++ {
		order := orders[rng.Intn(len(orders))]
		st := signalTypes[rng.Intn(len(signalTypes))]
		nb := 2 + 2*rng.Intn(2)
		useInterp := int32(0)
		coef := int32(4)
		if nb == 4 && rng.Intn(2) == 0 {
			useInterp = 1
			coef = int32(rng.Intn(4))
		}
		addCase("bulk", order, nb, st, useInterp, coef, rng.Intn(3) == 0)
	}

	want, err := probeLibopusSILKProcessNLSFs(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk process nlsfs", err)
		return
	}

	for i, tc := range cases {
		nlsf := make([]int16, tc.order)
		copy(nlsf, tc.nlsfQ15)
		prev := make([]int16, tc.order)
		copy(prev, tc.prevNLSFQ15)

		var cb *nlsfCB
		if tc.order == 16 {
			cb = &silk_NLSF_CB_WB
		} else {
			cb = &silk_NLSF_CB_NB_MB
		}

		var enc Encoder
		p := silkProcessNLSFsParams{
			predictLPCOrder:      tc.order,
			nbSubfr:              tc.nbSubfr,
			speechActivityQ8:     tc.speechActivityQ8,
			signalType:           tc.signalType,
			useInterpolatedNLSFs: tc.useInterp,
			nlsfInterpCoefQ2:     tc.nlsfInterpCoefQ2,
			nlsfMSVQSurvivors:    tc.nlsfMSVQSurvivors,
			cb:                   cb,
			nlsfQ15:              nlsf,
			prevNLSFQ15:          prev,
		}
		res := enc.silkProcessNLSFsFixed(&silkFixedEncodeScratch{}, &p)

		fail := func(field string, got, exp interface{}) {
			t.Fatalf("case %d (%s order=%d nb=%d st=%d useInterp=%d coef=%d): %s=%v want %v",
				i, tc.name, tc.order, tc.nbSubfr, tc.signalType, tc.useInterp, tc.nlsfInterpCoefQ2, field, got, exp)
		}

		for k := 0; k < tc.order; k++ {
			if int32(res.predCoefQ12[0][k]) != want[i].predCoef0[k] {
				fail(fmt.Sprintf("PredCoef_Q12[0][%d]", k), res.predCoefQ12[0][k], want[i].predCoef0[k])
			}
			if int32(res.predCoefQ12[1][k]) != want[i].predCoef1[k] {
				fail(fmt.Sprintf("PredCoef_Q12[1][%d]", k), res.predCoefQ12[1][k], want[i].predCoef1[k])
			}
			if int32(nlsf[k]) != want[i].nlsfQ15[k] {
				fail(fmt.Sprintf("pNLSF_Q15[%d]", k), nlsf[k], want[i].nlsfQ15[k])
			}
		}
		for k := 0; k < tc.order+1; k++ {
			if int32(res.nlsfIndices[k]) != want[i].nlsfIndices[k] {
				fail(fmt.Sprintf("NLSFIndices[%d]", k), res.nlsfIndices[k], want[i].nlsfIndices[k])
			}
		}
	}
}
