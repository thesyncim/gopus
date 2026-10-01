//go:build amd64 && goexperiment.simd && !nosimd && !purego

package silk

import (
	"math/rand"
	"reflect"
	"testing"
)

type nsqDelDecAVX2Case struct {
	params *NSQParams
	input  []int16
}

// randomNSQDelDecAVX2Frame builds a frame whose parameters, state and input
// swing between typical encoder values and extremes that saturate the
// shaping sums, the residual limit and the xq output.
func randomNSQDelDecAVX2Frame(rng *rand.Rand, nsq *NSQState, nStates int) nsqDelDecAVX2Case {
	r32 := func(amp int32) int32 { return int32(rng.Int63n(2*int64(amp)+1) - int64(amp)) }
	r16 := func(amp int32) int16 { return int16(rng.Int31n(2*amp+1) - amp) }
	extreme := rng.Intn(4) == 0

	fsKHz := [...]int{8, 12, 16}[rng.Intn(3)]
	nbSubfr := [...]int{2, 4}[rng.Intn(2)]
	subfrLength := 5 * fsKHz
	frameLength := nbSubfr * subfrLength
	ltpMemLength := 20 * fsKHz
	predOrder := [...]int{10, 16}[rng.Intn(2)]
	if fsKHz == 16 && rng.Intn(4) != 0 {
		predOrder = 16
	}
	shapeOrder := 2 * (1 + rng.Intn(maxShapeLpcOrder/2))
	if rng.Intn(2) == 0 {
		shapeOrder = maxShapeLpcOrder
	}

	p := &NSQParams{
		SignalType:             rng.Intn(3),
		QuantOffsetType:        rng.Intn(2),
		PredCoefQ12:            make([]int16, 2*maxLPCOrder),
		NLSFInterpCoefQ2:       [...]int{4, rng.Intn(4)}[rng.Intn(2)],
		LTPCoefQ14:             make([]int16, maxNbSubfr*ltpOrderConst),
		ARShpQ13:               make([]int16, maxNbSubfr*maxShapeLpcOrder),
		HarmShapeGainQ14:       make([]int32, maxNbSubfr),
		TiltQ14:                make([]int32, maxNbSubfr),
		LFShpQ14:               make([]int32, maxNbSubfr),
		GainsQ16:               make([]int32, maxNbSubfr),
		PitchL:                 make([]int32, maxNbSubfr),
		LambdaQ10:              rng.Int31n(4096),
		LTPScaleQ14:            rng.Int31n(1 << 14),
		FrameLength:            frameLength,
		SubfrLength:            subfrLength,
		NbSubfr:                nbSubfr,
		LTPMemLength:           ltpMemLength,
		PredLPCOrder:           predOrder,
		ShapeLPCOrder:          shapeOrder,
		WarpingQ16:             int(rng.Int31n(1 << 14)),
		NStatesDelayedDecision: nStates,
		Seed:                   rng.Intn(4),
	}
	if rng.Intn(3) == 0 {
		p.LambdaQ10 = 2048 + rng.Int31n(1<<15)
	}
	for i := range p.PredCoefQ12 {
		p.PredCoefQ12[i] = r16(4096)
	}
	for i := range p.LTPCoefQ14 {
		p.LTPCoefQ14[i] = r16(1 << 14)
	}
	for i := range p.ARShpQ13 {
		p.ARShpQ13[i] = r16(1 << 13)
	}
	minLag, maxLag := 2*fsKHz, 18*fsKHz
	for k := range maxNbSubfr {
		p.HarmShapeGainQ14[k] = rng.Int31n(1 << 14)
		p.TiltQ14[k] = r32(1 << 14)
		p.LFShpQ14[k] = int32(uint32(r16(1<<14))<<16 | uint32(uint16(r16(1<<14))))
		p.GainsQ16[k] = 1<<14 + rng.Int31n(1<<22)
		if extreme && rng.Intn(2) == 0 {
			p.GainsQ16[k] = 1 + rng.Int31n(1<<10)
		}
		p.PitchL[k] = int32(minLag + rng.Intn(maxLag-minLag+1))
	}
	if rng.Intn(3) == 0 {
		for k := range maxNbSubfr {
			p.GainsQ16[k] = p.GainsQ16[0]
		}
	}

	input := make([]int16, frameLength)
	amp := int32(1 << (6 + rng.Intn(10)))
	for i := range input {
		input[i] = r16(min(amp, 32767))
		if extreme && rng.Intn(8) == 0 {
			input[i] = [...]int16{32767, -32768}[rng.Intn(2)]
		}
	}

	if extreme {
		big := int32(1 << 30)
		for i := range nsq.sLTPShpQ14 {
			nsq.sLTPShpQ14[i] = r32(big)
		}
		for i := range nsq.sAR2Q14 {
			nsq.sAR2Q14[i] = r32(big)
		}
		for i := range nsq.sLPCQ14 {
			nsq.sLPCQ14[i] = r32(big)
		}
		nsq.sLFARShpQ14 = r32(big)
		nsq.sDiffShpQ14 = r32(big)
		for i := range nsq.xq {
			nsq.xq[i] = r16(32767)
		}
	}
	if rng.Intn(4) == 0 {
		nsq.lagPrev = int32(minLag + rng.Intn(maxLag-minLag+1))
	}
	if rng.Intn(4) == 0 {
		nsq.prevGainQ16 = 1 + rng.Int31n(1<<24)
	}
	return nsqDelDecAVX2Case{params: p, input: input}
}

// nsqStateWithoutDelDecScratch returns a copy of nsq without the per-call
// delayed-decision scratch, which the two representations lay out
// differently and every call reinitializes.
func nsqStateWithoutDelDecScratch(nsq *NSQState) NSQState {
	c := *nsq
	c.delDecStates = [maxDelDecStates]nsqDelDecState{}
	c.delDecAVX2 = nsqDelDecAVX2State{}
	return c
}

func TestNoiseShapeQuantizeDelDecAVX2MatchesStates(t *testing.T) {
	if !silkNSQDelDecUsesAVX2 {
		t.Skip("host lacks AVX2+FMA")
	}
	rng := rand.New(rand.NewSource(0x5ca1ab1e))
	for trial := range 400 {
		nStates := 3 + trial%2
		ref := NewNSQState()
		got := NewNSQState()
		for frame := range 6 {
			tc := randomNSQDelDecAVX2Frame(rng, ref, nStates)
			got.RestoreFrom(ref)
			got.scratchSLTPQ15 = append(got.scratchSLTPQ15[:0], ref.scratchSLTPQ15...)
			got.scratchSLTP = append(got.scratchSLTP[:0], ref.scratchSLTP...)
			if !nsqDelDecAVX2Supports(tc.params) {
				t.Fatalf("trial %d frame %d: unsupported params", trial, frame)
			}
			wantPulses, wantXq, wantSeed := noiseShapeQuantizeDelDec(ref, tc.input, tc.params, true, false)
			gotPulses, gotXq, gotSeed := noiseShapeQuantizeDelDec(got, tc.input, tc.params, true, true)
			if gotSeed != wantSeed {
				t.Fatalf("trial %d frame %d: seed %d, want %d", trial, frame, gotSeed, wantSeed)
			}
			if !reflect.DeepEqual(gotPulses, wantPulses) {
				t.Fatalf("trial %d frame %d: pulses differ\ngot  %v\nwant %v", trial, frame, gotPulses, wantPulses)
			}
			if !reflect.DeepEqual(gotXq, wantXq) {
				t.Fatalf("trial %d frame %d: xq differs\ngot  %v\nwant %v", trial, frame, gotXq, wantXq)
			}
			if g, w := nsqStateWithoutDelDecScratch(got), nsqStateWithoutDelDecScratch(ref); !reflect.DeepEqual(g, w) {
				t.Fatalf("trial %d frame %d: NSQ state differs\ngot  %+v\nwant %+v", trial, frame, g, w)
			}
		}
	}
}

func TestNoiseShapeQuantizeDelDecAVX2ZeroAlloc(t *testing.T) {
	if !silkNSQDelDecUsesAVX2 {
		t.Skip("host lacks AVX2+FMA")
	}
	rng := rand.New(rand.NewSource(7))
	nsq := NewNSQState()
	tc := randomNSQDelDecAVX2Frame(rng, nsq, maxDelDecStates)
	tc.params.SignalType = typeVoiced
	noiseShapeQuantizeDelDec(nsq, tc.input, tc.params, true, true)
	if allocs := testing.AllocsPerRun(50, func() {
		noiseShapeQuantizeDelDec(nsq, tc.input, tc.params, true, true)
	}); allocs != 0 {
		t.Fatalf("structure-of-arrays delayed-decision NSQ allocated %v times", allocs)
	}
}
