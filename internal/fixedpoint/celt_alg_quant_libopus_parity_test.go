//go:build gopus_fixedpoint

package fixedpoint

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/rangecoding"
)

// algQuantRanges spans the celt_norm magnitudes alg_quant sees: post-MDCT
// normalised residuals around Q14 unit norm, plus larger Q18/Q24 regimes that
// drive exp_rotation's norm_scaledown and op_pvq_search's internal scaling.
var algQuantRanges = []struct {
	name string
	span int32
}{
	{"q14", 1 << 14},
	{"q18", 1 << 18},
	{"norm", 1 << 24},
}

func randAlgQuantInput(rng *rand.Rand, n int, span int32) []int32 {
	out := make([]int32, n)
	for i := range out {
		out[i] = rng.Int31n(2*span+1) - span
	}
	return out
}

// TestAlgQuantMatchesLibopusFixed drives the Go AlgQuant through a real range
// coder and checks the encoded bytes, collapse mask, and resynthesised X are
// bit-exact to libopus alg_quant (FIXED_POINT). It then checks AlgUnquant
// recovers the same X and mask as libopus alg_unquant from the C-produced
// bytes, and that Go-encoded bytes round-trip through libopus alg_unquant.
func TestAlgQuantMatchesLibopusFixed(t *testing.T) {
	libopustest.RequireOracle(t)
	rng := rand.New(rand.NewSource(0x4A17C0DE))

	// (N, K, B) covering: minimum N==2, K small vs N (greedy loop), K > N/2
	// (pre-search projection), multi-block B (collapse mask, exp_rotation
	// stride2), and larger CELT bands. spread is swept separately.
	//
	// K is kept within the (N,K) domain the CWRS coder (icwrs/cwrsi) handles
	// bit-exactly, which is the same domain CELT's band-splitting guarantees:
	// each coded sub-band has a codeword count and index that fit ec_enc_uint
	// and round-trip through the combinatorial coder.
	type tc struct{ n, k, b int }
	cases := []tc{
		{2, 1, 1}, {2, 3, 1}, {2, 8, 1},
		{3, 1, 1}, {3, 5, 1}, {3, 16, 1},
		{4, 2, 1}, {4, 7, 2}, {4, 20, 1},
		{8, 1, 1}, {8, 4, 2}, {8, 12, 1}, {8, 24, 1},
		{16, 3, 1}, {16, 8, 2}, {16, 11, 4},
		{32, 5, 2}, {32, 7, 4},
		{44, 4, 1}, {44, 6, 1},
		{64, 4, 1},
		{100, 3, 1},
		{120, 2, 1}, {120, 3, 1}, {120, 4, 4},
	}
	// SPREAD_NONE=0 (rotation skipped) .. SPREAD_AGGRESSIVE=3.
	spreads := []int{0, 1, 2, 3}
	const gain = 1 << 28 // Q31-ish resynthesis gain; libopus passes Q31 gains.

	for _, c := range cases {
		for _, spread := range spreads {
			for trial := 0; trial < 4; trial++ {
				r := algQuantRanges[trial%len(algQuantRanges)]
				x := randAlgQuantInput(rng, c.n, r.span)
				bufBytes := c.n*2 + 16

				// libopus alg_quant (resynth on) over a real ec_enc.
				cMask, cBytes, cResynth, err := libopustest.ProbeCELTAlgQuant(
					append([]int32(nil), x...), c.k, spread, c.b, gain, bufBytes)
				if err != nil {
					t.Fatalf("ProbeCELTAlgQuant N=%d K=%d B=%d spread=%d: %v", c.n, c.k, c.b, spread, err)
				}

				// Go AlgQuant over a real range encoder.
				var enc rangecoding.Encoder
				enc.Init(make([]byte, bufBytes))
				goX := append([]int32(nil), x...)
				goMask := AlgQuant(goX, c.n, c.k, spread, c.b, &enc, gain, true, nil)
				// Done() returns the compacted packet (range bytes at the front,
				// raw/end bytes appended) matching the C oracle's compact_packet.
				goBytes := enc.Done()

				label := func() string {
					return labelf(c.n, c.k, c.b, spread, r.name)
				}

				if goMask != cMask {
					t.Fatalf("%s: collapse mask Go=%#x C=%#x", label(), goMask, cMask)
				}
				if !bytes.Equal(goBytes, cBytes) {
					t.Fatalf("%s: encoded bytes mismatch\n Go=%x\n  C=%x", label(), goBytes, cBytes)
				}
				for i := range cResynth {
					if goX[i] != cResynth[i] {
						t.Fatalf("%s: resynth X[%d] Go=%d C=%d", label(), i, goX[i], cResynth[i])
					}
				}

				// Decode side: Go AlgUnquant over the C-produced bytes vs
				// libopus alg_unquant over the same bytes.
				dMaskC, dXC, err := libopustest.ProbeCELTAlgUnquant(c.n, c.k, spread, c.b, gain, cBytes)
				if err != nil {
					t.Fatalf("ProbeCELTAlgUnquant %s: %v", label(), err)
				}
				var dec rangecoding.Decoder
				dec.Init(append([]byte(nil), cBytes...))
				dX := make([]int32, c.n)
				dMaskGo := AlgUnquant(dX, c.n, c.k, spread, c.b, &dec, gain)
				if dMaskGo != dMaskC {
					t.Fatalf("%s: decode collapse mask Go=%#x C=%#x", label(), dMaskGo, dMaskC)
				}
				for i := range dXC {
					if dX[i] != dXC[i] {
						t.Fatalf("%s: decode X[%d] Go=%d C=%d", label(), i, dX[i], dXC[i])
					}
				}
			}
		}
	}
}

func labelf(n, k, b, spread int, rangeName string) string {
	return fmt.Sprintf("N=%d K=%d B=%d spread=%d range=%s", n, k, b, spread, rangeName)
}
