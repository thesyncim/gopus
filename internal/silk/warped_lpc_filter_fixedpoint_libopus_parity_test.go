//go:build gopus_fixedpoint

package silk

import (
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusSILKFixedWarpedLPCInputMagic  = "GWLI"
	libopusSILKFixedWarpedLPCOutputMagic = "GWLO"
)

type silkFixedWarpedGainCase struct {
	name      string
	lambdaQ16 int32
	order     int
	coefsQ24  []int32
}

func probeLibopusSILKFixedWarpedGain(cases []silkFixedWarpedGainCase) ([]int32, error) {
	binPath, err := buildFixedSILKOracle("libopus_silk_fixed_warped_lpc_info.c", "warped_lpc")
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedWarpedLPCInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.I32(tc.lambdaQ16)
		payload.I32(int32(tc.order))
		for _, v := range tc.coefsQ24 {
			payload.I32(v)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed warped gain", libopusSILKFixedWarpedLPCOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]int32, count)
	for i := range out {
		out[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKWarpedGainFixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0x5A9F))

	randCoefs := func(order int, amp int32) []int32 {
		c := make([]int32, order)
		for i := range c {
			c[i] = rng.Int31n(2*amp+1) - amp
		}
		return c
	}

	var cases []silkFixedWarpedGainCase

	// Noise-shaping warping_Q16 is small and positive in practice, but exercise
	// a broad signed lambda range and the full set of shaping LPC orders.
	lambdas := []int32{0, 1311, 6554, -6554, 19661, -19661, 32767, -32768}
	for _, order := range []int{1, 2, 6, 12, 16, 24} {
		for _, lambda := range lambdas {
			// SILK_FIX_CONST(1.0, 24) == 1<<24; coefs are typically below ~1.0 in Q24.
			cases = append(cases, silkFixedWarpedGainCase{
				name:      "sweep",
				lambdaQ16: lambda,
				order:     order,
				coefsQ24:  randCoefs(order, 1<<23),
			})
		}
	}

	// Small-magnitude coefficients (near-flat filter -> gain near 1.0).
	cases = append(cases, silkFixedWarpedGainCase{
		name:      "small",
		lambdaQ16: 6554,
		order:     12,
		coefsQ24:  randCoefs(12, 4096),
	})

	// All-zero coefficients: gain_Q24 collapses to SILK_FIX_CONST(1.0,24).
	cases = append(cases, silkFixedWarpedGainCase{
		name:      "zero-coefs",
		lambdaQ16: 6554,
		order:     16,
		coefsQ24:  make([]int32, 16),
	})

	// Large coefficients near full Q24 scale, stressing the inverse.
	bigPos := make([]int32, 24)
	bigNeg := make([]int32, 24)
	for i := range bigPos {
		bigPos[i] = (1 << 24) - 1
		bigNeg[i] = -(1 << 24)
	}
	cases = append(cases,
		silkFixedWarpedGainCase{name: "big-pos", lambdaQ16: 32767, order: 24, coefsQ24: bigPos},
		silkFixedWarpedGainCase{name: "big-neg", lambdaQ16: -32768, order: 24, coefsQ24: bigNeg},
	)

	// Broad random bulk.
	for i := 0; i < 128; i++ {
		order := 1 + rng.Intn(24)
		cases = append(cases, silkFixedWarpedGainCase{
			name:      "rand-bulk",
			lambdaQ16: rng.Int31n(65536) - 32768,
			order:     order,
			coefsQ24:  randCoefs(order, int32(1+rng.Intn(1<<24))),
		})
	}

	want, err := probeLibopusSILKFixedWarpedGain(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed warped gain", err)
		return
	}

	for i, tc := range cases {
		got := silkWarpedGainFIX(tc.coefsQ24, tc.lambdaQ16, tc.order)
		if got != want[i] {
			t.Fatalf("case %d (%s order=%d lambda=%d): gain=%d want %d",
				i, tc.name, tc.order, tc.lambdaQ16, got, want[i])
		}
	}
}
