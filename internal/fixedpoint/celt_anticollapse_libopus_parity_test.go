//go:build gopus_fixedpoint

package fixedpoint

import (
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// renormRanges span the celt_norm magnitudes renormalise_vector sees: the
// normalised residuals it rescales live in the NORM_SHIFT (Q24) domain, but the
// internal norm_scaledown drops them toward the int16 working range, so we
// exercise both the already-small and the needs-scaledown regimes.
var renormRanges = []struct {
	name string
	span int32
}{
	{"q14", 1 << 14},
	{"q20", 1 << 20},
	{"norm", 1 << 24},
}

func randNorm(rng *rand.Rand, n int, span int32) []int32 {
	out := make([]int32, n)
	for i := range out {
		out[i] = rng.Int31n(2*span+1) - span
	}
	return out
}

func TestRenormaliseVectorMatchesLibopusFixed(t *testing.T) {
	libopustest.RequireOracle(t)
	rng := rand.New(rand.NewSource(0x4E0721))

	lengths := []int{2, 3, 4, 8, 16, 44, 120, 176}
	// Q31ONE plus a few scaled-down gains.
	gains := []int32{2147483647, 1 << 30, 1 << 28, 1234567890}

	for _, n := range lengths {
		for _, r := range renormRanges {
			for _, gain := range gains {
				for trial := 0; trial < 6; trial++ {
					x := randNorm(rng, n, r.span)
					xC := append([]int32(nil), x...)
					xGo := append([]int32(nil), x...)

					want, err := libopustest.ProbeCELTRenormaliseVector(xC, gain)
					if err != nil {
						libopustest.HelperUnavailable(t, "CELT fixed anti-collapse", err)
					}

					RenormaliseVector(xGo, n, gain)

					for i := 0; i < n; i++ {
						if xGo[i] != want[i] {
							t.Fatalf("RenormaliseVector[%d] N=%d range=%s gain=%d trial=%d got=%d want=%d",
								i, n, r.name, gain, trial, xGo[i], want[i])
						}
					}
				}
			}
		}
	}
}

// celtBandsMono builds a monotonically increasing eBands layout of nbEBands+1
// entries with per-band widths drawn from the CELT-typical range.
func celtBandsMono(rng *rand.Rand, nbEBands int) []int16 {
	eb := make([]int16, nbEBands+1)
	acc := int16(0)
	for i := 0; i <= nbEBands; i++ {
		eb[i] = acc
		acc += int16(1 + rng.Intn(8))
	}
	return eb
}

func TestAntiCollapseMatchesLibopusFixed(t *testing.T) {
	libopustest.RequireOracle(t)
	rng := rand.New(rand.NewSource(0xA17C011A))

	type tc struct {
		lm, c, nbEBands, start, end int
	}
	cases := []tc{
		{0, 1, 21, 0, 21},
		{1, 1, 21, 0, 21},
		{2, 2, 21, 0, 21},
		{3, 2, 21, 0, 21},
		{3, 1, 21, 0, 21},
		{2, 1, 25, 3, 20},
		{1, 2, 17, 0, 17},
		{0, 2, 21, 0, 21},
	}

	for ci, c := range cases {
		for trial := 0; trial < 6; trial++ {
			eBands := celtBandsMono(rng, c.nbEBands)
			// size must cover the largest band offset shifted by LM.
			size := (int(eBands[c.nbEBands]) << uint(c.lm)) + 16

			total := c.c * size
			x := randNorm(rng, total, 1<<24)

			masks := make([]byte, c.nbEBands*c.c)
			for i := range masks {
				// Clear some collapse bits to trigger the noise-fill path.
				masks[i] = byte(rng.Intn(256))
			}

			// libopus always allocates the energy logs for both channels
			// (2*nbEBands); the !encode && C==1 path reads the 2nd-channel slot.
			logEN := 2 * c.nbEBands
			logE := make([]int32, logEN)
			prev1 := make([]int32, logEN)
			prev2 := make([]int32, logEN)
			for i := range logE {
				// celt_glog is Q24; keep energies in a realistic +-20 dB band.
				logE[i] = int32(rng.Intn(40<<24)) - (20 << 24)
				prev1[i] = int32(rng.Intn(40<<24)) - (20 << 24)
				prev2[i] = int32(rng.Intn(40<<24)) - (20 << 24)
			}

			pulses := make([]int, c.nbEBands)
			for i := range pulses {
				pulses[i] = rng.Intn(400)
			}

			seed := rng.Uint32()
			encode := trial%2 == 0

			in := libopustest.CELTAntiCollapseInput{
				X:             append([]int32(nil), x...),
				CollapseMasks: append([]byte(nil), masks...),
				LM:            c.lm,
				C:             c.c,
				Size:          size,
				Start:         c.start,
				End:           c.end,
				LogE:          logE,
				Prev1LogE:     prev1,
				Prev2LogE:     prev2,
				Pulses:        pulses,
				EBands:        eBands,
				NbEBands:      c.nbEBands,
				Seed:          seed,
				Encode:        encode,
			}

			want, err := libopustest.ProbeCELTAntiCollapse(in)
			if err != nil {
				libopustest.HelperUnavailable(t, "CELT fixed anti-collapse", err)
			}

			xGo := append([]int32(nil), x...)
			AntiCollapse(xGo, append([]byte(nil), masks...), c.lm, c.c, size,
				c.start, c.end, logE, prev1, prev2, pulses, eBands, c.nbEBands,
				seed, encode)

			for i := 0; i < total; i++ {
				if xGo[i] != want[i] {
					t.Fatalf("AntiCollapse[%d] case=%d trial=%d LM=%d C=%d got=%d want=%d",
						i, ci, trial, c.lm, c.c, xGo[i], want[i])
				}
			}
		}
	}
}
