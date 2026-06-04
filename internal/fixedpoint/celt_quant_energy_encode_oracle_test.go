//go:build gopus_fixedpoint

package fixedpoint

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestQuantEnergyEncodeOracle checks the FIXED_POINT energy quantizers
// (QuantCoarseEnergy + QuantFineEnergy + QuantEnergyFinalise) against the real
// libopus quant_bands.c quant_* functions bit-for-bit. Identical target band
// energies, predictor, intra/budget configuration and fine-quant patterns must
// yield byte-identical range-coder output as well as identical reconstructed
// oldEBands, error and delayedIntra.
func TestQuantEnergyEncodeOracle(t *testing.T) {
	libopustest.RequireOracle(t)

	const nbEBands = 21

	// Deterministic pseudo-random Q24 log-energy generator. Values are kept in a
	// plausible coarse-energy range (roughly [-16,16] in the log2 domain).
	genEnergies := func(seed uint32, n int) []int32 {
		state := seed
		out := make([]int32, n)
		for i := range out {
			state ^= state << 13
			state ^= state >> 17
			state ^= state << 5
			// Map to roughly [-12,12] << dbShift with sub-integer jitter.
			v := int32(state%(24<<dbShift)) - (12 << dbShift)
			out[i] = v
		}
		return out
	}

	fineQuantPatterns := func(kind, n int) []int32 {
		fq := make([]int32, n)
		for i := range fq {
			switch kind {
			case 0:
				fq[i] = 0
			case 1:
				fq[i] = int32(i % 4)
			case 2:
				fq[i] = int32(i % (maxFineBits + 1))
			case 3:
				fq[i] = maxFineBits
			}
		}
		return fq
	}
	extraQuantPattern := func(n int) []int32 {
		eq := make([]int32, n)
		for i := range eq {
			eq[i] = int32(i % 4)
		}
		return eq
	}
	finePriorityPattern := func(n int) []int32 {
		fp := make([]int32, n)
		for i := range fp {
			fp[i] = int32(i & 1)
		}
		return fp
	}

	type boundCase struct{ start, end int }
	bounds := []boundCase{
		{0, nbEBands},
		{0, 17},
		{17, nbEBands},
		{0, 10},
	}

	budgets := []int{8 * 16, 8 * 48, 8 * 128, 8 * 400}
	seed := uint32(0x2468ace)

	for _, C := range []int{1, 2} {
		for _, LM := range []int{0, 1, 2, 3} {
			for _, forceIntra := range []bool{false, true} {
				for _, b := range bounds {
					for _, fqKind := range []int{0, 1, 2, 3} {
						for _, budget := range budgets {
							seed += 0x9e3779b1

							bufSize := budget / 8
							eBands := genEnergies(seed, C*nbEBands)
							oldEBands := genEnergies(seed^0x55555555, C*nbEBands)
							fineQuant := fineQuantPatterns(fqKind, nbEBands)
							extraQuant := extraQuantPattern(nbEBands)
							finePriority := finePriorityPattern(nbEBands)

							nbAvailableBytes := bufSize
							delayedIntra := int32(3 + (seed % 50))
							finaliseBits := 7
							twoPass := !forceIntra

							params := libopustest.CELTEnergyEncodeParams{
								NbEBands:         nbEBands,
								Start:            b.start,
								End:              b.end,
								EffEnd:           b.end,
								C:                C,
								LM:               LM,
								Budget:           budget,
								NbAvailableBytes: nbAvailableBytes,
								ForceIntra:       forceIntra,
								TwoPass:          twoPass,
								LossRate:         0,
								Lfe:              false,
								DelayedIntra:     delayedIntra,
								BufSize:          bufSize,
								FinaliseBits:     finaliseBits,
								EBands:           append([]int32(nil), eBands...),
								OldEBands:        append([]int32(nil), oldEBands...),
								FineQuant:        fineQuant,
								ExtraQuant:       extraQuant,
								FinePriority:     finePriority,
							}
							want, err := libopustest.ProbeCELTQuantEnergyEncode(params)
							if err != nil {
								libopustest.HelperUnavailable(t, "celt quant energy encode", err)
								return
							}

							// gopus side: drive the same three quantizers.
							buf := make([]byte, bufSize)
							enc := &rangecoding.Encoder{}
							enc.Init(buf)

							gotOld := append([]int32(nil), oldEBands...)
							gotErr := make([]int32, C*nbEBands)
							gotDelayed := delayedIntra

							QuantCoarseEnergy(enc, eBands, gotOld, gotErr,
								b.start, b.end, b.end, nbEBands, C, LM, budget,
								nbAvailableBytes, forceIntra, twoPass, 0, false, &gotDelayed, nil)
							QuantFineEnergy(enc, gotOld, gotErr,
								b.start, b.end, nbEBands, C, nil, extraQuant)
							QuantEnergyFinalise(enc, gotOld, gotErr,
								b.start, b.end, nbEBands, C, fineQuant, finePriority, finaliseBits)

							enc.Shrink(uint32(bufSize))
							gotBytes := enc.Done()

							ctx := func() string {
								return tctx(C, LM, forceIntra, b.start, b.end, fqKind, budget)
							}

							if !bytes.Equal(gotBytes, want.Bytes) {
								t.Fatalf("%s: coded bytes differ\n got=%x\nwant=%x", ctx(), gotBytes, want.Bytes)
							}
							assertI32Eq(t, ctx, "oldEBands", gotOld, want.OldEBands)
							// libopus leaves error[] outside [start,end) uninitialized
							// (the intra two-pass copies the whole array including
							// never-written bands), so only the coded bands are
							// meaningful for parity.
							for c := 0; c < C; c++ {
								for i := b.start; i < b.end; i++ {
									idx := i + c*nbEBands
									if gotErr[idx] != want.Error[idx] {
										t.Fatalf("%s: error[%d]=%d libopus=%d", ctx(), idx, gotErr[idx], want.Error[idx])
									}
								}
							}
							if gotDelayed != want.DelayedIntra {
								t.Fatalf("%s: delayedIntra=%d libopus=%d", ctx(), gotDelayed, want.DelayedIntra)
							}
						}
					}
				}
			}
		}
	}
}

func assertI32Eq(t *testing.T, ctx func() string, name string, got, want []int32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %s len got=%d want=%d", ctx(), name, len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s: %s[%d]=%d libopus=%d", ctx(), name, i, got[i], want[i])
		}
	}
}

func tctx(C, LM int, forceIntra bool, start, end, fqKind, budget int) string {
	return fmt.Sprintf("C=%d LM=%d forceIntra=%v [%d,%d) fq=%d budget=%d",
		C, LM, forceIntra, start, end, fqKind, budget)
}
