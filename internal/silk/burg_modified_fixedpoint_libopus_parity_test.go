//go:build gopus_fixed_point

package silk

import (
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusSILKFixedBurgInputMagic  = "GBMI"
	libopusSILKFixedBurgOutputMagic = "GBMO"
)

type silkFixedBurgCase struct {
	name          string
	minInvGainQ30 int32
	subfrLength   int
	nbSubfr       int
	order         int
	x             []int16
}

type silkFixedBurgResult struct {
	resNrg  int32
	resNrgQ int32
	aQ16    []int32
}

func probeLibopusSILKFixedBurg(cases []silkFixedBurgCase) ([]silkFixedBurgResult, error) {
	binPath, err := buildFixedSILKOracle("libopus_silk_fixed_burg_info.c", "burg")
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedBurgInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.I32(tc.minInvGainQ30)
		payload.I32(int32(tc.subfrLength))
		payload.I32(int32(tc.nbSubfr))
		payload.I32(int32(tc.order))
		for _, v := range tc.x {
			payload.I16(v)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed burg", libopusSILKFixedBurgOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]silkFixedBurgResult, count)
	for i := range out {
		out[i].resNrg = reader.I32()
		out[i].resNrgQ = reader.I32()
		out[i].aQ16 = make([]int32, cases[i].order)
		for j := range out[i].aQ16 {
			out[i].aQ16[j] = reader.I32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKBurgModifiedFixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0xB126))

	const maxFrame = 384

	randSignal := func(n int, amp int32) []int16 {
		x := make([]int16, n)
		for i := range x {
			if amp <= 0 {
				continue
			}
			x[i] = int16(rng.Int31n(2*amp+1) - amp)
		}
		return x
	}

	tonal := func(n int, amp float64, periodSamples float64) []int16 {
		x := make([]int16, n)
		for i := range x {
			// Deterministic integer-rounded sinusoid; the oracle consumes the
			// resulting int16 samples verbatim.
			v := amp * math.Sin(2*math.Pi*float64(i)/periodSamples)
			x[i] = int16(int32(v))
		}
		return x
	}

	var cases []silkFixedBurgCase

	orders := []int{10, 16}
	// (subfrLength, nbSubfr) layouts honoring subfr*nb <= 384.
	layouts := [][2]int{{96, 4}, {80, 4}, {72, 1}, {48, 4}, {40, 2}, {24, 1}, {20, 4}, {32, 1}}

	// Permissive inverse gain (no early-out) and a tight one to force the
	// min-invgain max-prediction-gain path.
	gains := []int32{
		1 << 24, // loose (~ default SILK find_LPC value range)
		1 << 28, // tighter
		1 << 30, // unity inverse gain -> forces the early-out almost immediately
	}

	for _, order := range orders {
		for _, lay := range layouts {
			sub, nb := lay[0], lay[1]
			if sub <= order || sub*nb > maxFrame {
				continue
			}
			total := sub * nb
			for _, g := range gains {
				// White noise.
				cases = append(cases, silkFixedBurgCase{
					name: "white", minInvGainQ30: g, subfrLength: sub, nbSubfr: nb,
					order: order, x: randSignal(total, 8000),
				})
				// Low-amplitude white noise.
				cases = append(cases, silkFixedBurgCase{
					name: "white-low", minInvGainQ30: g, subfrLength: sub, nbSubfr: nb,
					order: order, x: randSignal(total, 50),
				})
				// Tonal (highly predictable -> drives the early-out for tight gains).
				cases = append(cases, silkFixedBurgCase{
					name: "tonal", minInvGainQ30: g, subfrLength: sub, nbSubfr: nb,
					order: order, x: tonal(total, 20000, 11.3),
				})
				// Silence (C0 == 0 -> CLZ64 boundary, MIN_RSHIFTS path).
				cases = append(cases, silkFixedBurgCase{
					name: "silence", minInvGainQ30: g, subfrLength: sub, nbSubfr: nb,
					order: order, x: make([]int16, total),
				})
				// Full-scale saturation alternating signal (large rshifts path).
				fs := make([]int16, total)
				for i := range fs {
					if i%2 == 0 {
						fs[i] = 32767
					} else {
						fs[i] = -32768
					}
				}
				cases = append(cases, silkFixedBurgCase{
					name: "fullscale", minInvGainQ30: g, subfrLength: sub, nbSubfr: nb,
					order: order, x: fs,
				})
			}
		}
	}

	// Broad random bulk over valid layouts.
	for i := 0; i < 120; i++ {
		order := orders[rng.Intn(len(orders))]
		nb := 1 + rng.Intn(4)
		maxSub := maxFrame / nb
		if maxSub <= order+1 {
			continue
		}
		sub := order + 2 + rng.Intn(maxSub-order-1)
		total := sub * nb
		amp := int32(1 + rng.Intn(32767))
		cases = append(cases, silkFixedBurgCase{
			name:          "rand-bulk",
			minInvGainQ30: int32(1) << uint(20+rng.Intn(11)),
			subfrLength:   sub,
			nbSubfr:       nb,
			order:         order,
			x:             randSignal(total, amp),
		})
	}

	want, err := probeLibopusSILKFixedBurg(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed burg", err)
		return
	}

	for i, tc := range cases {
		aQ16 := make([]int32, tc.order)
		resNrg, resNrgQ := silkBurgModifiedFixed(aQ16, tc.x, tc.minInvGainQ30,
			tc.subfrLength, tc.nbSubfr, tc.order)
		if resNrg != want[i].resNrg {
			t.Fatalf("case %d (%s order=%d sub=%d nb=%d gain=%d): res_nrg=%d want %d",
				i, tc.name, tc.order, tc.subfrLength, tc.nbSubfr, tc.minInvGainQ30,
				resNrg, want[i].resNrg)
		}
		if int32(resNrgQ) != want[i].resNrgQ {
			t.Fatalf("case %d (%s): res_nrg_Q=%d want %d", i, tc.name, resNrgQ, want[i].resNrgQ)
		}
		for j := range aQ16 {
			if aQ16[j] != want[i].aQ16[j] {
				t.Fatalf("case %d (%s order=%d sub=%d nb=%d gain=%d): A_Q16[%d]=%d want %d",
					i, tc.name, tc.order, tc.subfrLength, tc.nbSubfr, tc.minInvGainQ30,
					j, aQ16[j], want[i].aQ16[j])
			}
		}
	}
}
