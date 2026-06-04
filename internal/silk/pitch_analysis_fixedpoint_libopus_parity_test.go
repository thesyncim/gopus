//go:build gopus_fixedpoint

package silk

import (
	"fmt"
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusSILKFixedPitchEnergyInputMagic  = "GFEI"
	libopusSILKFixedPitchEnergyOutputMagic = "GFEO"
)

type silkFixedPitchEnergyCase struct {
	name       string
	fsKHz      int
	nbSubfr    int
	complexity int
	startLag   int
	frame      []int16
}

func (tc silkFixedPitchEnergyCase) sfLength() int { return peSubfrLengthMS * tc.fsKHz }

func (tc silkFixedPitchEnergyCase) nbCbkSearch() int {
	if tc.nbSubfr == peMaxNbSubfr {
		return pitchNbCbkSearchsStage3[tc.complexity]
	}
	return peNbCbksStage310ms
}

func probeLibopusSILKFixedPitchEnergy(cases []silkFixedPitchEnergyCase) ([][]int32, error) {
	binPath, err := buildFixedSILKOracle("libopus_silk_fixed_pitch_analysis_info.c", "pitch_energy")
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedPitchEnergyInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.startLag))
		payload.U32(uint32(tc.sfLength()))
		payload.U32(uint32(tc.nbSubfr))
		payload.U32(uint32(tc.complexity))
		payload.U32(uint32(len(tc.frame)))
		for _, v := range tc.frame {
			payload.I16(v)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed pitch energy", libopusSILKFixedPitchEnergyOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([][]int32, count)
	for i := range out {
		total := cases[i].nbSubfr * cases[i].nbCbkSearch() * peNbStage3Lags
		out[i] = make([]int32, total)
		for j := range out[i] {
			out[i][j] = reader.I32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKPitchAnalysisEnergySt3FixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0x57a93))

	frameLen := func(fsKHz, nbSubfr int) int {
		return (peLTPMemLengthMS + nbSubfr*peSubfrLengthMS) * fsKHz
	}

	// Smooth periodic content keeps energies positive and exercises the
	// recursive add/remove update without saturating int32.
	periodFrame := func(fsKHz, nbSubfr, period int, amp float64) []int16 {
		n := frameLen(fsKHz, nbSubfr)
		f := make([]int16, n)
		for i := range f {
			v := amp * (math.Sin(2*math.Pi*float64(i%period)/float64(period)) +
				0.2*math.Sin(4*math.Pi*float64(i%period)/float64(period)+0.3))
			f[i] = int16(math.Round(v))
		}
		return f
	}

	randFrame := func(fsKHz, nbSubfr int, amp int32) []int16 {
		n := frameLen(fsKHz, nbSubfr)
		f := make([]int16, n)
		for i := range f {
			f[i] = int16(rng.Int31n(2*amp+1) - amp)
		}
		return f
	}

	var cases []silkFixedPitchEnergyCase
	for _, fsKHz := range []int{8, 12, 16} {
		minLag := peMinLagMS * fsKHz
		maxLag := peMaxLagMS*fsKHz - 1
		for _, nbSubfr := range []int{peMaxNbSubfr, peMaxNbSubfr >> 1} {
			maxCx := SILK_PE_MAX_COMPLEX
			if nbSubfr != peMaxNbSubfr {
				maxCx = 0 // complexity unused for 10ms tables; one pass is enough
			}
			for cx := 0; cx <= maxCx; cx++ {
				for _, startLag := range []int{minLag, (minLag + maxLag) / 2, maxLag - 2} {
					cases = append(cases, silkFixedPitchEnergyCase{
						name:       fmt.Sprintf("period_fs%d_sf%d_cx%d_lag%d", fsKHz, nbSubfr, cx, startLag),
						fsKHz:      fsKHz,
						nbSubfr:    nbSubfr,
						complexity: cx,
						startLag:   startLag,
						frame:      periodFrame(fsKHz, nbSubfr, 3*fsKHz, 9000),
					})
					cases = append(cases, silkFixedPitchEnergyCase{
						name:       fmt.Sprintf("rand_fs%d_sf%d_cx%d_lag%d", fsKHz, nbSubfr, cx, startLag),
						fsKHz:      fsKHz,
						nbSubfr:    nbSubfr,
						complexity: cx,
						startLag:   startLag,
						frame:      randFrame(fsKHz, nbSubfr, 30000),
					})
				}
			}
		}
	}

	// Bulk randomized coverage spanning the full lag range.
	for i := 0; i < 64; i++ {
		fsKHz := []int{8, 12, 16}[rng.Intn(3)]
		nbSubfr := peMaxNbSubfr
		cx := rng.Intn(SILK_PE_MAX_COMPLEX + 1)
		if rng.Intn(2) == 0 {
			nbSubfr = peMaxNbSubfr >> 1
			cx = 0
		}
		minLag := peMinLagMS * fsKHz
		maxLag := peMaxLagMS*fsKHz - 1
		startLag := minLag + rng.Intn(maxLag-minLag-1)
		cases = append(cases, silkFixedPitchEnergyCase{
			name:       fmt.Sprintf("bulk_%d", i),
			fsKHz:      fsKHz,
			nbSubfr:    nbSubfr,
			complexity: cx,
			startLag:   startLag,
			frame:      randFrame(fsKHz, nbSubfr, int32(1+rng.Intn(32767))),
		})
	}

	want, err := probeLibopusSILKFixedPitchEnergy(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed pitch energy", err)
		return
	}

	for i, tc := range cases {
		nbCbk := tc.nbCbkSearch()
		got := make([][peNbStage3Lags]int32, tc.nbSubfr*nbCbk)
		silkPAnaCalcEnergySt3Fixed(got, tc.frame, tc.startLag, tc.sfLength(), tc.nbSubfr, tc.complexity)

		flat := make([]int32, 0, len(got)*peNbStage3Lags)
		for _, row := range got {
			flat = append(flat, row[:]...)
		}
		if len(flat) != len(want[i]) {
			t.Fatalf("case %d (%s): got %d values want %d", i, tc.name, len(flat), len(want[i]))
		}
		for j := range flat {
			if flat[j] != want[i][j] {
				t.Fatalf("case %d (%s fs=%d nbSubfr=%d cx=%d startLag=%d): energy[%d]=%d want %d",
					i, tc.name, tc.fsKHz, tc.nbSubfr, tc.complexity, tc.startLag, j, flat[j], want[i][j])
			}
		}
	}
}
