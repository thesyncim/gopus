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
	libopusSILKFixedPitchSearchInputMagic  = "GPSI"
	libopusSILKFixedPitchSearchOutputMagic = "GPSO"
)

type silkFixedPitchSearchCase struct {
	name       string
	fsKHz      int
	complexity int
	nbSubfr    int
	prevLag    int
	thres1Q16  int32
	thres2Q13  int
	ltpInQ15   int32
	frame      []int16
}

type silkFixedPitchSearchResult struct {
	voicing      int32
	lagIndex     int32
	contourIndex int32
	ltpOutQ15    int32
	pitchOut     []int32
}

func probeLibopusSILKFixedPitchSearch(cases []silkFixedPitchSearchCase) ([]silkFixedPitchSearchResult, error) {
	binPath, err := buildFixedSILKOracle("libopus_silk_fixed_pitch_search_info.c", "pitch_search")
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedPitchSearchInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.fsKHz))
		payload.U32(uint32(tc.complexity))
		payload.U32(uint32(tc.nbSubfr))
		payload.U32(uint32(tc.prevLag))
		payload.U32(uint32(tc.thres1Q16))
		payload.U32(uint32(tc.thres2Q13))
		payload.U32(uint32(tc.ltpInQ15))
		payload.U32(uint32(len(tc.frame)))
		for _, v := range tc.frame {
			payload.I16(v)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed pitch search", libopusSILKFixedPitchSearchOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]silkFixedPitchSearchResult, count)
	for i := range out {
		out[i].voicing = reader.I32()
		out[i].lagIndex = reader.I32()
		out[i].contourIndex = reader.I32()
		out[i].ltpOutQ15 = reader.I32()
		out[i].pitchOut = make([]int32, cases[i].nbSubfr)
		for k := range out[i].pitchOut {
			out[i].pitchOut[k] = reader.I32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKPitchAnalysisSearchFixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0x9173ad))

	frameLen := func(fsKHz, nbSubfr int) int {
		return (peLTPMemLengthMS + nbSubfr*peSubfrLengthMS) * fsKHz
	}

	// Periodic content with a clear pitch period drives a voiced result and
	// exercises the full stage-1/2/3 pipeline.
	periodFrame := func(fsKHz, nbSubfr, periodSamples int, amp float64) []int16 {
		n := frameLen(fsKHz, nbSubfr)
		f := make([]int16, n)
		for i := range f {
			ph := 2 * math.Pi * float64(i) / float64(periodSamples)
			v := amp * (math.Sin(ph) + 0.35*math.Sin(2*ph+0.4) + 0.15*math.Sin(3*ph+1.1))
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

	var cases []silkFixedPitchSearchCase

	// Deterministic sweep over fs / nb_subfr / complexity / prevLag with periodic
	// frames at a few representative pitch periods (in source-rate samples).
	for _, fsKHz := range []int{8, 12, 16} {
		for _, nbSubfr := range []int{peMaxNbSubfr, peMaxNbSubfr >> 1} {
			for cx := 0; cx <= SILK_PE_MAX_COMPLEX; cx++ {
				for _, periodMS := range []int{3, 6, 10, 14} {
					for _, prevLag := range []int{0, periodMS * fsKHz} {
						for _, ltpIn := range []int32{0, 16000, 30000} {
							cases = append(cases, silkFixedPitchSearchCase{
								name:       fmt.Sprintf("period_fs%d_sf%d_cx%d_p%d_pl%d_ltp%d", fsKHz, nbSubfr, cx, periodMS, prevLag, ltpIn),
								fsKHz:      fsKHz,
								complexity: cx,
								nbSubfr:    nbSubfr,
								prevLag:    prevLag,
								thres1Q16:  26214, // 0.4 in Q16
								thres2Q13:  3276,  // 0.4 in Q13
								ltpInQ15:   ltpIn,
								frame:      periodFrame(fsKHz, nbSubfr, periodMS*fsKHz, 8000),
							})
						}
					}
				}
				// Low-amplitude / noise cases stress the unvoiced escape paths.
				cases = append(cases, silkFixedPitchSearchCase{
					name:       fmt.Sprintf("rand_fs%d_sf%d_cx%d", fsKHz, nbSubfr, cx),
					fsKHz:      fsKHz,
					complexity: cx,
					nbSubfr:    nbSubfr,
					prevLag:    0,
					thres1Q16:  39322, // 0.6 in Q16
					thres2Q13:  4096,  // 0.5 in Q13
					ltpInQ15:   0,
					frame:      randFrame(fsKHz, nbSubfr, 4000),
				})
			}
		}
	}

	// Bulk randomized coverage over the full parameter space.
	for i := 0; i < 128; i++ {
		fsKHz := []int{8, 12, 16}[rng.Intn(3)]
		nbSubfr := peMaxNbSubfr
		if rng.Intn(2) == 0 {
			nbSubfr = peMaxNbSubfr >> 1
		}
		cx := rng.Intn(SILK_PE_MAX_COMPLEX + 1)
		var frame []int16
		if rng.Intn(2) == 0 {
			periodMS := 2 + rng.Intn(15)
			frame = periodFrame(fsKHz, nbSubfr, periodMS*fsKHz, float64(2000+rng.Intn(12000)))
		} else {
			frame = randFrame(fsKHz, nbSubfr, int32(1+rng.Intn(20000)))
		}
		minLag := peMinLagMS * fsKHz
		maxLag := peMaxLagMS*fsKHz - 1
		prevLag := 0
		if rng.Intn(2) == 0 {
			prevLag = minLag + rng.Intn(maxLag-minLag)
		}
		cases = append(cases, silkFixedPitchSearchCase{
			name:       fmt.Sprintf("bulk_%d", i),
			fsKHz:      fsKHz,
			complexity: cx,
			nbSubfr:    nbSubfr,
			prevLag:    prevLag,
			thres1Q16:  int32(rng.Intn(65537)),
			thres2Q13:  rng.Intn(8193),
			ltpInQ15:   int32(rng.Intn(32768)),
			frame:      frame,
		})
	}

	want, err := probeLibopusSILKFixedPitchSearch(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed pitch search", err)
		return
	}

	sc := &silkFixedEncodeScratch{}
	for i, tc := range cases {
		pitchOut := make([]int, tc.nbSubfr)
		ltp := tc.ltpInQ15
		lagIdx, contourIdx, voicing := silkPitchAnalysisCoreFixed(
			sc, tc.frame, pitchOut, &ltp, tc.prevLag, tc.thres1Q16, tc.thres2Q13,
			tc.fsKHz, tc.complexity, tc.nbSubfr)

		w := want[i]
		if int32(voicing) != w.voicing {
			t.Fatalf("case %d (%s): voicing=%d want %d", i, tc.name, voicing, w.voicing)
		}
		if int32(lagIdx) != w.lagIndex {
			t.Fatalf("case %d (%s): lagIndex=%d want %d", i, tc.name, lagIdx, w.lagIndex)
		}
		if int32(contourIdx) != w.contourIndex {
			t.Fatalf("case %d (%s): contourIndex=%d want %d", i, tc.name, contourIdx, w.contourIndex)
		}
		if ltp != w.ltpOutQ15 {
			t.Fatalf("case %d (%s): LTPCorr_Q15=%d want %d", i, tc.name, ltp, w.ltpOutQ15)
		}
		for k := range pitchOut {
			if int32(pitchOut[k]) != w.pitchOut[k] {
				t.Fatalf("case %d (%s fs=%d sf=%d cx=%d): pitch_out[%d]=%d want %d",
					i, tc.name, tc.fsKHz, tc.nbSubfr, tc.complexity, k, pitchOut[k], w.pitchOut[k])
			}
		}
	}
}
