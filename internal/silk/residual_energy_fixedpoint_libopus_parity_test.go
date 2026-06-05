//go:build gopus_fixed_point

package silk

import (
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusSILKFixedResEnergyInputMagic  = "GSRI"
	libopusSILKFixedResEnergyOutputMagic = "GSRO"
)

type silkFixedResEnergyCase struct {
	name        string
	subfrLength int
	nbSubfr     int
	lpcOrder    int
	aQ12        [][]int16 // [2][lpcOrder]
	gains       []int32   // [nbSubfr]
	x           []int16   // [(nbSubfr/2)*(maxNbSubfr/2)*(lpcOrder+subfrLength)]
}

type silkFixedResEnergyResult struct {
	nrgs  []int32
	nrgsQ []int32
}

func probeLibopusSILKFixedResEnergy(cases []silkFixedResEnergyCase) ([]silkFixedResEnergyResult, error) {
	binPath, err := buildFixedSILKOracle("libopus_silk_fixed_residual_energy_info.c", "resenergy")
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedResEnergyInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.subfrLength))
		payload.U32(uint32(tc.nbSubfr))
		payload.U32(uint32(tc.lpcOrder))
		for h := 0; h < 2; h++ {
			for _, v := range tc.aQ12[h] {
				payload.I16(v)
			}
		}
		for _, v := range tc.gains {
			payload.I32(v)
		}
		for _, v := range tc.x {
			payload.I16(v)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed residual energy", libopusSILKFixedResEnergyOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]silkFixedResEnergyResult, count)
	for i := range out {
		n := cases[i].nbSubfr
		out[i].nrgs = make([]int32, n)
		out[i].nrgsQ = make([]int32, n)
		for j := 0; j < n; j++ {
			out[i].nrgs[j] = reader.I32()
			out[i].nrgsQ[j] = reader.I32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKResidualEnergyFixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0x5e7a))

	randCoefs := func(d int, scale int32) []int16 {
		b := make([]int16, d)
		for i := range b {
			b[i] = int16(rng.Int31n(2*scale+1) - scale)
		}
		return b
	}
	randSignal := func(n int, amp int32) []int16 {
		x := make([]int16, n)
		for i := range x {
			x[i] = int16(rng.Int31n(2*amp+1) - amp)
		}
		return x
	}

	const halfNbSubfr = maxNbSubfr >> 1

	newCase := func(name string, subfrLength, nbSubfr, lpcOrder int, coefScale, sigAmp int32, gains []int32) silkFixedResEnergyCase {
		a := make([][]int16, 2)
		a[0] = randCoefs(lpcOrder, coefScale)
		a[1] = randCoefs(lpcOrder, coefScale)
		offset := lpcOrder + subfrLength
		xLen := (nbSubfr >> 1) * halfNbSubfr * offset
		return silkFixedResEnergyCase{
			name:        name,
			subfrLength: subfrLength,
			nbSubfr:     nbSubfr,
			lpcOrder:    lpcOrder,
			aQ12:        a,
			gains:       gains,
			x:           randSignal(xLen, sigAmp),
		}
	}

	gains4 := func(vals ...int32) []int32 { return vals }

	var cases []silkFixedResEnergyCase

	// Standard SILK configurations: 4 subframes, various subframe lengths and
	// LPC orders. Gains span a wide Q16-ish range.
	for _, subfrLen := range []int{40, 60, 80, 120} {
		for _, order := range []int{10, 16} {
			cases = append(cases, newCase("std", subfrLen, 4, order, 600, 12000,
				gains4(65536, 131072, 32768, 262144)))
		}
	}

	// 2-subframe (10 ms) configuration.
	cases = append(cases, newCase("nbsubfr2", 80, 2, 16, 600, 12000,
		gains4(100000, 50000)))

	// Saturation / edge stress: full-scale signal, large coefficients.
	{
		c := newCase("saturation", 80, 4, 16, 4000, 32767,
			gains4(1<<30, 1, 1<<20, 65536))
		cases = append(cases, c)
	}

	// Tiny gains and tiny signal (small-energy / shift edge cases).
	cases = append(cases, newCase("tiny", 40, 4, 10, 50, 4,
		gains4(1, 2, 3, 4)))

	// Bulk random coverage.
	for i := 0; i < 48; i++ {
		nbSubfr := 4
		if i%5 == 0 {
			nbSubfr = 2
		}
		order := 10
		if rng.Intn(2) == 0 {
			order = 16
		}
		subfrLen := 20 + rng.Intn(120)
		g := make([]int32, nbSubfr)
		for k := range g {
			g[k] = 1 + rng.Int31n(1<<30)
		}
		cases = append(cases, newCase("bulk", subfrLen, nbSubfr, order,
			int32(1+rng.Intn(2000)), int32(1+rng.Intn(32767)), g))
	}

	want, err := probeLibopusSILKFixedResEnergy(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed residual energy", err)
		return
	}

	for i, tc := range cases {
		nrgs := make([]int32, tc.nbSubfr)
		nrgsQ := make([]int, tc.nbSubfr)
		silkResidualEnergyFixed(&silkFixedEncodeScratch{}, nrgs, nrgsQ, tc.x, tc.aQ12, tc.gains,
			tc.subfrLength, tc.nbSubfr, tc.lpcOrder)
		for j := 0; j < tc.nbSubfr; j++ {
			if nrgs[j] != want[i].nrgs[j] {
				t.Fatalf("case %d (%s subfr=%d nb=%d order=%d): nrgs[%d]=%d want %d",
					i, tc.name, tc.subfrLength, tc.nbSubfr, tc.lpcOrder, j, nrgs[j], want[i].nrgs[j])
			}
			if int32(nrgsQ[j]) != want[i].nrgsQ[j] {
				t.Fatalf("case %d (%s subfr=%d nb=%d order=%d): nrgsQ[%d]=%d want %d",
					i, tc.name, tc.subfrLength, tc.nbSubfr, tc.lpcOrder, j, nrgsQ[j], want[i].nrgsQ[j])
			}
		}
	}
}
