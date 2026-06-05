//go:build gopus_fixed_point

package fixedpoint

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestUnquantEnergyOracle checks the FIXED_POINT energy unquantizers
// (UnquantCoarseEnergy + UnquantFineEnergy + UnquantEnergyFinalise) against the
// real libopus quant_bands.c unquant_* functions bit-for-bit. The same coded
// byte buffer is fed to both decoders; because the unquantizers are a pure
// function of the range-coded input, identical input bytes must yield identical
// Q24 oldEBands.
func TestUnquantEnergyOracle(t *testing.T) {
	libopustest.RequireOracle(t)

	const nbEBands = 21

	// Deterministic pseudo-random coded byte buffer generator.
	codedBuf := func(seed uint32, n int) []byte {
		state := seed
		buf := make([]byte, n)
		for i := range buf {
			state ^= state << 13
			state ^= state >> 17
			state ^= state << 5
			buf[i] = byte(state >> 11)
		}
		return buf
	}

	// fine_quant / fine_priority patterns exercising the refinement and
	// finalise passes (including bands at and below MAX_FINE_BITS).
	fineQuantPatterns := func(kind int) []int32 {
		fq := make([]int32, nbEBands)
		for i := range fq {
			switch kind {
			case 0:
				fq[i] = 0
			case 1:
				fq[i] = int32(i % 4)
			case 2:
				fq[i] = int32((i % (maxFineBits + 1)))
			case 3:
				fq[i] = maxFineBits
			}
		}
		return fq
	}
	finePriorityPattern := func() []int32 {
		fp := make([]int32, nbEBands)
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

	lens := []int{8, 24, 64, 200}
	seed := uint32(0x1234567)

	for _, C := range []int{1, 2} {
		for _, LM := range []int{0, 1, 2, 3} {
			for _, intra := range []bool{false, true} {
				for _, b := range bounds {
					for _, fqKind := range []int{0, 1, 2, 3} {
						for _, n := range lens {
							seed += 0x9e3779b1
							coded := codedBuf(seed, n)
							fineQuant := fineQuantPatterns(fqKind)
							finePriority := finePriorityPattern()
							bitsLeft := int32(7)

							params := libopustest.CELTEnergyUnquantParams{
								NbEBands:     nbEBands,
								Start:        b.start,
								End:          b.end,
								EffEnd:       b.end,
								Channels:     C,
								LM:           LM,
								Intra:        intra,
								Coded:        coded,
								FineQuant:    fineQuant,
								FinePriority: finePriority,
								BitsLeft:     bitsLeft,
							}
							want, err := libopustest.ProbeCELTFixedEnergyUnquant(params)
							if err != nil {
								libopustest.HelperUnavailable(t, "celt fixed energy unquant", err)
								return
							}

							got := make([]int32, C*nbEBands)
							dec := &rangecoding.Decoder{}
							dec.Init(coded)
							UnquantCoarseEnergy(dec, got, b.start, b.end, nbEBands, C, LM, intra)
							UnquantFineEnergy(dec, got, b.start, b.end, nbEBands, C, nil, fineQuant)
							UnquantEnergyFinalise(dec, got, b.start, b.end, nbEBands, C, fineQuant, finePriority, int(bitsLeft))

							if len(want) != len(got) {
								t.Fatalf("C=%d LM=%d intra=%v [%d,%d) fq=%d n=%d: oracle len=%d want=%d",
									C, LM, intra, b.start, b.end, fqKind, n, len(want), len(got))
							}
							for i := range got {
								if got[i] != want[i] {
									t.Fatalf("C=%d LM=%d intra=%v [%d,%d) fq=%d n=%d: oldEBands[%d]=%d libopus=%d",
										C, LM, intra, b.start, b.end, fqKind, n, i, got[i], want[i])
								}
							}
						}
					}
				}
			}
		}
	}
}
