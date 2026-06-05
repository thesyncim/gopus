//go:build gopus_fixed_point

package silk

import (
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusSILKFixedSchur64InputMagic  = "GS6I"
	libopusSILKFixedSchur64OutputMagic = "GS6O"
)

type silkFixedSchur64Case struct {
	name  string
	order int
	c     []int32
}

type silkFixedSchur64Result struct {
	res   int32
	rcQ16 []int32
	aQ24  []int32
}

func probeLibopusSILKFixedSchur64(cases []silkFixedSchur64Case) ([]silkFixedSchur64Result, error) {
	binPath, err := buildFixedSILKOracle("libopus_silk_fixed_schur64_info.c", "schur64")
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedSchur64InputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.I32(int32(tc.order))
		for _, v := range tc.c {
			payload.I32(v)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed schur64", libopusSILKFixedSchur64OutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]silkFixedSchur64Result, count)
	for i := range out {
		out[i].res = reader.I32()
		out[i].rcQ16 = make([]int32, cases[i].order)
		for j := range out[i].rcQ16 {
			out[i].rcQ16[j] = reader.I32()
		}
		out[i].aQ24 = make([]int32, cases[i].order)
		for j := range out[i].aQ24 {
			out[i].aQ24[j] = reader.I32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKSchur64FixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0x5C64))

	var cases []silkFixedSchur64Case

	// White-noise-like autocorrelations across the order sweep.
	for _, order := range []int{0, 1, 2, 4, 8, 10, 12, 16, 24} {
		cases = append(cases, silkFixedSchur64Case{
			name:  "white",
			order: order,
			c:     makeAutocorr(rng, order, 400, 12000),
		})
	}

	// Tonal: strong correlation at successive lags via a low-frequency signal.
	tonal := func(order, length int) []int32 {
		x := make([]int64, length)
		for i := range x {
			v := int64((i % 17) - 8)
			x[i] = v * 1500
		}
		c := make([]int32, order+1)
		for lag := 0; lag <= order; lag++ {
			var acc int64
			for i := lag; i < length; i++ {
				acc += x[i] * x[i-lag]
			}
			for acc > (1<<30) || acc < -(1<<30) {
				acc >>= 1
			}
			if lag == 0 && acc <= 0 {
				acc = 1
			}
			c[lag] = int32(acc)
		}
		return c
	}
	for _, order := range []int{2, 6, 12, 16, 24} {
		cases = append(cases, silkFixedSchur64Case{
			name:  "tonal",
			order: order,
			c:     tonal(order, 320),
		})
	}

	// Invalid input: c[0] <= 0 takes the early zero-rc / zero-energy path.
	for _, order := range []int{0, 2, 8, 16, 24} {
		c := make([]int32, order+1)
		c[0] = 0
		for i := 1; i <= order; i++ {
			c[i] = int32(rng.Int31n(2001) - 1000)
		}
		cases = append(cases, silkFixedSchur64Case{
			name:  "invalid-c0-zero",
			order: order,
			c:     c,
		})
	}
	for _, order := range []int{2, 16, 24} {
		c := make([]int32, order+1)
		c[0] = -(1 << 20)
		for i := 1; i <= order; i++ {
			c[i] = int32(rng.Int31n(2001) - 1000)
		}
		cases = append(cases, silkFixedSchur64Case{
			name:  "invalid-c0-neg",
			order: order,
			c:     c,
		})
	}

	// Silence / near-silence exercises the max(.,1) divisor guard and the
	// high-precision division headroom normalization.
	for _, order := range []int{2, 8, 16, 24} {
		c := make([]int32, order+1)
		c[0] = 1
		cases = append(cases, silkFixedSchur64Case{
			name:  "silence",
			order: order,
			c:     c,
		})
	}
	for _, order := range []int{4, 16, 24} {
		c := make([]int32, order+1)
		c[0] = 64
		for i := 1; i <= order; i++ {
			c[i] = int32(rng.Int31n(33) - 16)
		}
		cases = append(cases, silkFixedSchur64Case{
			name:  "near-silence",
			order: order,
			c:     c,
		})
	}

	// Saturation / unstable-rc: off-diagonal magnitude >= c[0] forces the 0.99
	// clamp and early break.
	for _, order := range []int{2, 8, 16, 24} {
		c := make([]int32, order+1)
		c[0] = 1 << 20
		for i := 1; i <= order; i++ {
			if i%2 == 0 {
				c[i] = 1 << 21 // exceeds c[0]
			} else {
				c[i] = -(1 << 21)
			}
		}
		cases = append(cases, silkFixedSchur64Case{
			name:  "unstable-rc",
			order: order,
			c:     c,
		})
	}

	// Large-magnitude energy exercising the full int32 SMMUL update range.
	for _, order := range []int{4, 12, 24} {
		c := makeAutocorr(rng, order, 600, 30000)
		c[0] = 0x7FFFFFFF
		cases = append(cases, silkFixedSchur64Case{
			name:  "highenergy",
			order: order,
			c:     c,
		})
	}

	// Broad random bulk over valid autocorrelations.
	for i := 0; i < 128; i++ {
		order := rng.Intn(25) // 0..24
		cases = append(cases, silkFixedSchur64Case{
			name:  "rand-bulk",
			order: order,
			c:     makeAutocorr(rng, order, 1+rng.Intn(800), int32(1+rng.Intn(32767))),
		})
	}

	want, err := probeLibopusSILKFixedSchur64(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed schur64", err)
		return
	}

	for i, tc := range cases {
		rc := make([]int32, tc.order)
		res := silkSchur64(rc, tc.c, int32(tc.order))
		if res != want[i].res {
			t.Fatalf("case %d (%s order=%d): residual=%d want %d",
				i, tc.name, tc.order, res, want[i].res)
		}
		for j := range rc {
			if rc[j] != want[i].rcQ16[j] {
				t.Fatalf("case %d (%s order=%d): rc_Q16[%d]=%d want %d",
					i, tc.name, tc.order, j, rc[j], want[i].rcQ16[j])
			}
		}

		a := make([]int32, tc.order)
		silkK2aQ16(a, rc, int32(tc.order))
		for j := range a {
			if a[j] != want[i].aQ24[j] {
				t.Fatalf("case %d (%s order=%d): A_Q24[%d]=%d want %d",
					i, tc.name, tc.order, j, a[j], want[i].aQ24[j])
			}
		}
	}
}
