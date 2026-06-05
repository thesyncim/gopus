//go:build gopus_fixed_point

package silk

import (
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusSILKFixedLTPAnalysisInputMagic  = "GLAI"
	libopusSILKFixedLTPAnalysisOutputMagic = "GLAO"
)

type silkFixedLTPAnalysisCase struct {
	name         string
	x            []int16
	xStart       int
	ltpCoefQ14   []int16
	pitchL       []int32
	invGainsQ16  []int32
	subfrLength  int
	nbSubfr      int
	preLength    int
	scaleIn      []int16
	scaleGainQ16 int32
}

type silkFixedLTPAnalysisResult struct {
	ltpRes   []int16
	scaleOut []int16
}

func probeLibopusSILKFixedLTPAnalysis(cases []silkFixedLTPAnalysisCase) ([]silkFixedLTPAnalysisResult, error) {
	binPath, err := buildFixedSILKOracle("libopus_silk_fixed_ltp_analysis_filter_info.c", "ltp_analysis")
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedLTPAnalysisInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(len(tc.x)))
		payload.U32(uint32(tc.xStart))
		payload.U32(uint32(tc.subfrLength))
		payload.U32(uint32(tc.nbSubfr))
		payload.U32(uint32(tc.preLength))
		for _, v := range tc.x {
			payload.I16(v)
		}
		for _, v := range tc.ltpCoefQ14 {
			payload.I16(v)
		}
		for _, v := range tc.pitchL {
			payload.U32(uint32(v))
		}
		for _, v := range tc.invGainsQ16 {
			payload.I32(v)
		}
		payload.U32(uint32(len(tc.scaleIn)))
		payload.I32(tc.scaleGainQ16)
		for _, v := range tc.scaleIn {
			payload.I16(v)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed ltp analysis", libopusSILKFixedLTPAnalysisOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]silkFixedLTPAnalysisResult, count)
	for i := range out {
		resLen := cases[i].nbSubfr * (cases[i].preLength + cases[i].subfrLength)
		out[i].ltpRes = make([]int16, resLen)
		for j := range out[i].ltpRes {
			out[i].ltpRes[j] = reader.I16()
		}
		out[i].scaleOut = make([]int16, len(cases[i].scaleIn))
		for j := range out[i].scaleOut {
			out[i].scaleOut[j] = reader.I16()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKLTPAnalysisFilterFixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0x17a1))

	randSignal := func(n int, amp int32) []int16 {
		x := make([]int16, n)
		for i := range x {
			x[i] = int16(rng.Int31n(2*amp+1) - amp)
		}
		return x
	}

	// newCase builds an input buffer with enough leading headroom so that
	// x_lag_ptr[-2] = x_start + k*subfr_length - pitchL[k] - 2 stays in bounds
	// for every subframe, plus enough trailing samples for the FIR window of the
	// last subframe (x_ptr reads up to x_start + (nb-1)*subfr_length +
	// subfr_length + pre_length - 1, x_lag_ptr up to x_start + nb*subfr_length -
	// minLag + 2).
	newCase := func(name string, subfrLength, nbSubfr, preLength int, lags []int, coefAmp int32, invGains []int32, xAmp int32, scaleSize int, scaleGain int32) silkFixedLTPAnalysisCase {
		maxLag := 0
		for _, l := range lags {
			if l > maxLag {
				maxLag = l
			}
		}
		// x_lag_ptr starts at x_start - pitchL[k]; worst case at k=0 reads
		// x_lag_ptr[-2] = x_start - maxLag - 2 >= 0.
		xStart := maxLag + 2
		// Trailing: x_ptr for last subframe reads x[xStart + (nb-1)*subfr +
		// subfr + pre - 1]; x_lag for last subframe reads up to xStart +
		// (nb-1)*subfr + (subfr+pre-1) - minLag + 2. The x_ptr read is the
		// largest. Provide a generous margin.
		xLen := xStart + nbSubfr*subfrLength + preLength + 4

		coefs := make([]int16, nbSubfr*ltpOrder)
		for i := range coefs {
			coefs[i] = int16(rng.Int31n(2*coefAmp+1) - coefAmp)
		}

		pitchL := make([]int32, len(lags))
		for i, l := range lags {
			pitchL[i] = int32(l)
		}

		return silkFixedLTPAnalysisCase{
			name:         name,
			x:            randSignal(xLen, xAmp),
			xStart:       xStart,
			ltpCoefQ14:   coefs,
			pitchL:       pitchL,
			invGainsQ16:  invGains,
			subfrLength:  subfrLength,
			nbSubfr:      nbSubfr,
			preLength:    preLength,
			scaleIn:      randSignal(scaleSize, xAmp),
			scaleGainQ16: scaleGain,
		}
	}

	gains := func(vals ...int32) []int32 { return vals }

	var cases []silkFixedLTPAnalysisCase

	// Standard SILK configurations: NB/MB/WB subframe lengths, 2 or 4 subframes,
	// typical pre_length (LTP_ORDER) and gains.
	cases = append(cases, newCase("nb_2sf", 40, 2, ltpOrder, []int{20, 24}, 8000, gains(65536, 32768), 4000, 41, 65536))
	cases = append(cases, newCase("nb_4sf", 40, 4, ltpOrder, []int{20, 24, 18, 28}, 8000, gains(65536, 40000, 80000, 12000), 4000, 17, 100000))
	cases = append(cases, newCase("mb_4sf", 60, 4, ltpOrder, []int{40, 44, 38, 50}, 12000, gains(50000, 60000, 70000, 80000), 6000, 60, 32768))
	cases = append(cases, newCase("wb_4sf", 80, 4, ltpOrder, []int{80, 90, 70, 100}, 12000, gains(65536, 65536, 65536, 65536), 8000, 80, 65536))

	// Saturation stress: large amplitudes, large coefficients and gains to drive
	// SAT16 and SMULWB toward their limits, and overflow in SMLABB_ovflw.
	cases = append(cases, newCase("sat_wb_4sf", 80, 4, ltpOrder, []int{120, 200, 160, 288}, 32767, gains(1<<30, 1<<29, 1<<28, 2000000000), 32767, 128, 1<<30))
	cases = append(cases, newCase("sat_long_4sf", 160, 4, ltpOrder, []int{200, 250, 180, 288}, 32767, gains(2000000000, 2000000000, 2000000000, 2000000000), 32767, 256, 2000000000))

	// Negative gains and coefficients.
	cases = append(cases, newCase("neg_gains", 60, 4, ltpOrder, []int{40, 44, 38, 50}, 32767, gains(-65536, -100000, -2000000000, -1), 32767, 33, -65536))

	// Edge: single subframe, minimal lag, zero pre_length.
	cases = append(cases, newCase("single_zero_pre", 80, 1, 0, []int{4}, 16384, gains(65536), 12000, 1, 65536))
	cases = append(cases, newCase("single_minlag", 40, 1, 2, []int{2}, 32767, gains(2000000000), 32767, 5, 1234567))

	// Bulk random coverage spanning amplitude, subframe length, lag, count,
	// pre_length, coefficients and gains.
	for i := 0; i < 64; i++ {
		nbSubfr := 1 + rng.Intn(maxNbSubfr)
		subfrLength := 8 + rng.Intn(160)
		preLength := rng.Intn(ltpOrder + 4)
		xAmp := int32(1 + rng.Intn(32767))
		coefAmp := int32(1 + rng.Intn(32767))
		lags := make([]int, nbSubfr)
		invGains := make([]int32, nbSubfr)
		for k := range lags {
			lags[k] = 2 + rng.Intn(288) // PE_MAX_LAG range.
			invGains[k] = rng.Int31() - (1 << 30)
		}
		scaleSize := rng.Intn(200)
		scaleGain := rng.Int31() - (1 << 30)
		cases = append(cases, newCase("bulk", subfrLength, nbSubfr, preLength, lags, coefAmp, invGains, xAmp, scaleSize, scaleGain))
	}

	want, err := probeLibopusSILKFixedLTPAnalysis(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed ltp analysis", err)
		return
	}

	for i, tc := range cases {
		resLen := tc.nbSubfr * (tc.preLength + tc.subfrLength)
		ltpRes := make([]int16, resLen)
		silkLTPAnalysisFilterFixed(ltpRes, tc.x, tc.xStart, tc.ltpCoefQ14, tc.pitchL, tc.invGainsQ16, tc.subfrLength, tc.nbSubfr, tc.preLength)

		for j := range ltpRes {
			if ltpRes[j] != want[i].ltpRes[j] {
				t.Fatalf("case %d (%s sf=%d nb=%d pre=%d): LTP_res[%d]=%d want %d",
					i, tc.name, tc.subfrLength, tc.nbSubfr, tc.preLength, j, ltpRes[j], want[i].ltpRes[j])
			}
		}

		scaleOut := make([]int16, len(tc.scaleIn))
		silkScaleCopyVector16(scaleOut, tc.scaleIn, tc.scaleGainQ16, len(tc.scaleIn))
		for j := range scaleOut {
			if scaleOut[j] != want[i].scaleOut[j] {
				t.Fatalf("case %d (%s): scale_out[%d]=%d want %d",
					i, tc.name, j, scaleOut[j], want[i].scaleOut[j])
			}
		}
	}
}
