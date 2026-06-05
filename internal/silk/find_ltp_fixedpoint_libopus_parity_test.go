//go:build gopus_fixed_point

package silk

import (
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusSILKFixedFindLTPInputMagic  = "GFLI"
	libopusSILKFixedFindLTPOutputMagic = "GFLO"
)

type silkFixedFindLTPCase struct {
	name        string
	residual    []int16
	resStart    int
	lag         []int32
	subfrLength int
	nbSubfr     int
}

type silkFixedFindLTPResult struct {
	XX []int32 // nbSubfr*ltpOrder*ltpOrder
	xX []int32 // nbSubfr*ltpOrder
}

func probeLibopusSILKFixedFindLTP(cases []silkFixedFindLTPCase) ([]silkFixedFindLTPResult, error) {
	binPath, err := buildFixedSILKOracle("libopus_silk_fixed_find_ltp_info.c", "findltp")
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedFindLTPInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(len(tc.residual)))
		payload.U32(uint32(tc.resStart))
		payload.U32(uint32(tc.subfrLength))
		payload.U32(uint32(tc.nbSubfr))
		for _, v := range tc.residual {
			payload.I16(v)
		}
		for _, v := range tc.lag {
			payload.U32(uint32(v))
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed find ltp", libopusSILKFixedFindLTPOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]silkFixedFindLTPResult, count)
	for i := range out {
		nbSubfr := cases[i].nbSubfr
		out[i].XX = make([]int32, nbSubfr*ltpOrder*ltpOrder)
		for j := range out[i].XX {
			out[i].XX[j] = reader.I32()
		}
		out[i].xX = make([]int32, nbSubfr*ltpOrder)
		for j := range out[i].xX {
			out[i].xX[j] = reader.I32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKFindLTPFixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0xf17d))

	randSignal := func(n int, amp int32) []int16 {
		x := make([]int16, n)
		for i := range x {
			x[i] = int16(rng.Int31n(2*amp+1) - amp)
		}
		return x
	}

	// newCase builds a residual buffer with enough leading headroom so that
	// lag_ptr = r_ptr - (lag + LTP_ORDER/2) stays in bounds for every subframe,
	// and enough trailing samples for the silk_sum_sqr_shift window of the last
	// subframe (subfr_length + LTP_ORDER).
	newCase := func(name string, subfrLength, nbSubfr int, lags []int, amp int32) silkFixedFindLTPCase {
		maxLag := 0
		for _, l := range lags {
			if l > maxLag {
				maxLag = l
			}
		}
		resStart := maxLag + ltpOrder/2
		// Last subframe r_ptr = resStart + (nbSubfr-1)*subfrLength; it reads up
		// to r_ptr + subfrLength + LTP_ORDER.
		resLen := resStart + nbSubfr*subfrLength + ltpOrder
		lag := make([]int32, len(lags))
		for i, l := range lags {
			lag[i] = int32(l)
		}
		return silkFixedFindLTPCase{
			name:        name,
			residual:    randSignal(resLen, amp),
			resStart:    resStart,
			lag:         lag,
			subfrLength: subfrLength,
			nbSubfr:     nbSubfr,
		}
	}

	var cases []silkFixedFindLTPCase

	// Standard SILK configurations: 2 or 4 subframes, NB/MB/WB subframe lengths,
	// low amplitude so rshifts==0 (silk_inner_prod path).
	cases = append(cases, newCase("nb_2sf", 40, 2, []int{20, 24}, 2000))
	cases = append(cases, newCase("nb_4sf", 40, 4, []int{20, 24, 18, 28}, 2000))
	cases = append(cases, newCase("mb_4sf", 60, 4, []int{40, 44, 38, 50}, 2000))
	cases = append(cases, newCase("wb_4sf", 80, 4, []int{80, 90, 70, 100}, 2000))

	// High amplitude / long subframes to force rshifts > 0 (shifted path) and
	// exercise the xx/XX shift reconciliation in both directions.
	cases = append(cases, newCase("big_wb_4sf", 80, 4, []int{120, 200, 160, 288}, 32767))
	cases = append(cases, newCase("big_long_4sf", 160, 4, []int{200, 250, 180, 288}, 32767))

	// Edge: single subframe, minimal lag.
	cases = append(cases, newCase("single", 80, 1, []int{32}, 12000))

	// Bulk random coverage spanning amplitude, subframe length, lag and count.
	for i := 0; i < 48; i++ {
		nbSubfr := 1 + rng.Intn(maxNbSubfr)
		subfrLength := 16 + rng.Intn(160)
		amp := int32(1 + rng.Intn(32767))
		lags := make([]int, nbSubfr)
		for k := range lags {
			lags[k] = 2 + rng.Intn(288) // PE_MAX_LAG range.
		}
		cases = append(cases, newCase("bulk", subfrLength, nbSubfr, lags, amp))
	}

	want, err := probeLibopusSILKFixedFindLTP(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed find ltp", err)
		return
	}

	for i, tc := range cases {
		XX := make([]int32, tc.nbSubfr*ltpOrder*ltpOrder)
		xX := make([]int32, tc.nbSubfr*ltpOrder)
		silkFindLTPFixed(XX, xX, tc.residual, tc.resStart, tc.lag, tc.subfrLength, tc.nbSubfr)

		for j := range XX {
			if XX[j] != want[i].XX[j] {
				t.Fatalf("case %d (%s sf=%d nb=%d): XX[%d]=%d want %d",
					i, tc.name, tc.subfrLength, tc.nbSubfr, j, XX[j], want[i].XX[j])
			}
		}
		for j := range xX {
			if xX[j] != want[i].xX[j] {
				t.Fatalf("case %d (%s sf=%d nb=%d): xX[%d]=%d want %d",
					i, tc.name, tc.subfrLength, tc.nbSubfr, j, xX[j], want[i].xX[j])
			}
		}
	}
}
