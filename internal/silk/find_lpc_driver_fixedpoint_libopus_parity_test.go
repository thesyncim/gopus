//go:build gopus_fixed_point

package silk

import (
	"fmt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"math/rand"
	"testing"
)

const (
	libopusSILKFixedFindLPCInputMagic  = "GFLI"
	libopusSILKFixedFindLPCOutputMagic = "GFLO"
)

type silkFixedFindLPCResult struct {
	nlsfQ15          [maxLPCOrder]int16
	nlsfInterpCoefQ2 int32
}

func probeLibopusSILKFixedFindLPC(cases []silkFindLPCInput) ([]silkFixedFindLPCResult, error) {
	binPath, err := buildFixedSILKOracle("libopus_silk_fixed_find_lpc_info.c", "find_lpc")
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedFindLPCInputMagic, uint32(len(cases)))
	for i := range cases {
		tc := &cases[i]
		payload.I32(int32(tc.predictLPCOrder))
		payload.I32(int32(tc.subfrLength))
		payload.I32(int32(tc.nbSubfr))
		if tc.useInterpolatedNLSFs {
			payload.I32(1)
		} else {
			payload.I32(0)
		}
		if tc.firstFrameAfterReset {
			payload.I32(1)
		} else {
			payload.I32(0)
		}
		payload.I32(tc.minInvGainQ30)
		for j := 0; j < maxLPCOrder; j++ {
			payload.I16(tc.prevNLSFqQ15[j])
		}
		payload.U32(uint32(len(tc.x)))
		for _, v := range tc.x {
			payload.I16(v)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed find_LPC", libopusSILKFixedFindLPCOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]silkFixedFindLPCResult, count)
	for i := range out {
		for j := 0; j < maxLPCOrder; j++ {
			out[i].nlsfQ15[j] = int16(reader.I32())
		}
		out[i].nlsfInterpCoefQ2 = reader.I32()
	}
	if err := reader.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKFindLPCFixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0x10C))

	// Build a monotonically increasing prev NLSF vector (valid LSF ordering).
	makePrevNLSF := func(order int) [maxLPCOrder]int16 {
		var p [maxLPCOrder]int16
		spacing := int16((1 << 15) / int32(order+1))
		v := spacing
		for i := 0; i < order; i++ {
			p[i] = v
			v += spacing
		}
		return p
	}

	// randSignal generates a band-limited-ish int16 signal (low-frequency
	// correlated) so that Burg analysis produces meaningful LPC structure.
	randSignal := func(n int, amp int32) []int16 {
		x := make([]int16, n)
		var acc int32
		for i := range x {
			acc += rng.Int31n(2*amp+1) - amp
			if acc > 32767 {
				acc = 32767
			} else if acc < -32768 {
				acc = -32768
			}
			x[i] = int16(acc >> 4)
		}
		return x
	}

	makeCase := func(order, fsKHz, nbSubfr int, useInterp, firstFrame bool, minInvGainQ30, amp int32) silkFindLPCInput {
		subfrLength := 5 * fsKHz
		blockLen := subfrLength + order
		xLen := nbSubfr * blockLen
		return silkFindLPCInput{
			predictLPCOrder:      order,
			subfrLength:          subfrLength,
			nbSubfr:              nbSubfr,
			useInterpolatedNLSFs: useInterp,
			firstFrameAfterReset: firstFrame,
			prevNLSFqQ15:         makePrevNLSF(order),
			minInvGainQ30:        minInvGainQ30,
			x:                    randSignal(xLen, amp),
		}
	}

	// SILK_FIX_CONST(1e-4, 30) ~= 107374; a realistic minInvGain_Q30.
	const minInvGain = int32(107374)

	var cases []silkFindLPCInput
	for _, order := range []int{10, 16} {
		for _, fs := range []int{8, 12, 16} {
			for _, nb := range []int{2, 4} {
				for _, useInterp := range []bool{false, true} {
					for _, first := range []bool{false, true} {
						for _, amp := range []int32{200, 4000, 30000} {
							cases = append(cases, makeCase(order, fs, nb, useInterp, first, minInvGain, amp))
						}
					}
				}
			}
		}
	}
	// A couple of extreme minInvGain values to exercise the max-gain early-out.
	cases = append(cases, makeCase(16, 16, 4, true, false, int32(1)<<30, 8000))
	cases = append(cases, makeCase(16, 16, 4, true, false, 1, 8000))

	want, err := probeLibopusSILKFixedFindLPC(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed find_LPC", err)
		return
	}

	sc := &silkFixedEncodeScratch{}
	for i := range cases {
		in := cases[i]
		got := silkFindLPCFIX(sc, &in)
		w := want[i]

		fail := func(field string, g, e interface{}) {
			t.Fatalf("case %d (order=%d nb=%d len=%d interp=%v first=%v): %s got %v want %v",
				i, cases[i].predictLPCOrder, cases[i].nbSubfr, len(cases[i].x),
				cases[i].useInterpolatedNLSFs, cases[i].firstFrameAfterReset, field, g, e)
		}

		if int32(got.nlsfInterpCoefQ2) != w.nlsfInterpCoefQ2 {
			fail("NLSFInterpCoef_Q2", got.nlsfInterpCoefQ2, w.nlsfInterpCoefQ2)
		}
		for j := 0; j < cases[i].predictLPCOrder; j++ {
			if got.nlsfQ15[j] != w.nlsfQ15[j] {
				fail(fmt.Sprintf("NLSF_Q15[%d]", j), got.nlsfQ15[j], w.nlsfQ15[j])
			}
		}
	}
}

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

const (
	libopusSILKFixedFindPitchLagsInputMagic  = "GFPI"
	libopusSILKFixedFindPitchLagsOutputMagic = "GFPO"
)

func probeLibopusSILKFixedFindPitchLags(cases []silkFindPitchLagsInput) ([]silkFindPitchLagsResult, error) {
	binPath, err := buildFixedSILKOracle("libopus_silk_fixed_find_pitch_lags_info.c", "find_pitch_lags")
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedFindPitchLagsInputMagic, uint32(len(cases)))
	for i := range cases {
		tc := &cases[i]
		payload.I32(int32(tc.laPitch))
		payload.I32(int32(tc.frameLength))
		payload.I32(int32(tc.ltpMemLength))
		payload.I32(int32(tc.pitchLPCWinLength))
		payload.I32(int32(tc.pitchEstimationLPCOrder))
		payload.U32(uint32(len(tc.x)))
		for _, v := range tc.x {
			payload.I16(v)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed find_pitch_lags", libopusSILKFixedFindPitchLagsOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]silkFindPitchLagsResult, count)
	for i := range out {
		n := int(reader.U32())
		res := make([]int16, n)
		for j := 0; j < n; j++ {
			res[j] = int16(reader.I32())
		}
		out[i].res = res
		out[i].predGainQ16 = reader.I32()
	}
	if err := reader.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKFindPitchLagsFixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0xF17))

	// randSignal generates a low-frequency-correlated int16 signal so the
	// whitening analysis produces meaningful LPC structure.
	randSignal := func(n int, amp int32) []int16 {
		x := make([]int16, n)
		var acc int32
		for i := range x {
			acc += rng.Int31n(2*amp+1) - amp
			if acc > 32767 {
				acc = 32767
			} else if acc < -32768 {
				acc = -32768
			}
			x[i] = int16(acc >> 4)
		}
		return x
	}

	// makeCase mirrors the SILK encoder buffer-length setup for a given Fs and
	// subframe count. Values follow silk_init_encoder / silk_control_encoder:
	//   la_pitch          = LA_PITCH_MS * fs_kHz (LA_PITCH_MS = 2 -> 2*fs_kHz)
	//   frame_length      = nb_subfr * 5 * fs_kHz
	//   ltp_mem_length    = LTP_MEM_LENGTH_MS * fs_kHz (20 ms -> 20*fs_kHz)
	//   pitch_LPC_win_length = (FIND_PITCH_LPC_WIN_MS) * fs_kHz, clamped so
	//   buf_len >= pitch_LPC_win_length (we use a value that satisfies the
	//   safety celt_assert).
	makeCase := func(fsKHz, nbSubfr, order int, winMs int, amp int32) silkFindPitchLagsInput {
		laPitch := 2 * fsKHz
		frameLength := nbSubfr * 5 * fsKHz
		ltpMemLength := 20 * fsKHz
		bufLen := laPitch + frameLength + ltpMemLength
		pitchLPCWin := winMs * fsKHz
		if pitchLPCWin > bufLen {
			pitchLPCWin = bufLen
		}
		// pitch_LPC_win_length must be a multiple of 4 and leave room for the
		// sine window (>= 2*la_pitch); round down to a multiple of 4.
		pitchLPCWin -= pitchLPCWin % 4
		return silkFindPitchLagsInput{
			laPitch:                 laPitch,
			frameLength:             frameLength,
			ltpMemLength:            ltpMemLength,
			pitchLPCWinLength:       pitchLPCWin,
			pitchEstimationLPCOrder: order,
			x:                       randSignal(bufLen, amp),
		}
	}

	var cases []silkFindPitchLagsInput
	for _, fs := range []int{8, 12, 16} {
		for _, nb := range []int{2, 4} {
			for _, order := range []int{8, 12, 16} {
				// FIND_PITCH_LPC_WIN_MS variants: 20 ms and 24 ms style windows.
				for _, winMs := range []int{20, 24} {
					for _, amp := range []int32{200, 4000, 30000} {
						cases = append(cases, makeCase(fs, nb, order, winMs, amp))
					}
				}
			}
		}
	}

	want, err := probeLibopusSILKFixedFindPitchLags(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed find_pitch_lags", err)
		return
	}

	sc := &silkFixedEncodeScratch{}
	for i := range cases {
		in := cases[i]
		got := silkFindPitchLagsFIXFrontEnd(sc, &in)
		w := want[i]

		fail := func(field string, g, e interface{}) {
			t.Fatalf("case %d (fs unknown, nb len=%d order=%d win=%d): %s got %v want %v",
				i, len(cases[i].x), cases[i].pitchEstimationLPCOrder, cases[i].pitchLPCWinLength, field, g, e)
		}

		if got.predGainQ16 != w.predGainQ16 {
			fail("predGain_Q16", got.predGainQ16, w.predGainQ16)
		}
		if len(got.res) != len(w.res) {
			fail("len(res)", len(got.res), len(w.res))
		}
		for j := range w.res {
			if got.res[j] != w.res[j] {
				fail(fmt.Sprintf("res[%d]", j), got.res[j], w.res[j])
				break
			}
		}
	}
}

const (
	libopusSILKFixedFindPredInputMagic  = "GFPI"
	libopusSILKFixedFindPredOutputMagic = "GFPO"
)

type silkFixedFindPredResult struct {
	predCoef0       [maxLPCOrder]int32
	predCoef1       [maxLPCOrder]int32
	ltpCoefQ14      [ltpOrder * maxNbSubfr]int32
	ltpScaleQ14     int32
	nlsfInterpCoef  int32
	perIndex        int32
	ltpIndex        [maxNbSubfr]int32
	nlsfIndices     [maxLPCOrder + 1]int32
	resNrg          [maxNbSubfr]int32
	resNrgQ         [maxNbSubfr]int32
	ltpredCodGainQ7 int32
	sumLogGainQ7    int32
	prevNLSFqQ15    [maxLPCOrder]int32
}

func probeLibopusSILKFixedFindPred(cases []silkFindPredCoefsInput) ([]silkFixedFindPredResult, error) {
	binPath, err := buildFixedSILKOracle("libopus_silk_fixed_find_pred_coefs_info.c", "find_pred")
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKFixedFindPredInputMagic, uint32(len(cases)))
	for i := range cases {
		tc := &cases[i]
		payload.I32(int32(tc.predictLPCOrder))
		payload.I32(int32(tc.subfrLength))
		payload.I32(int32(tc.nbSubfr))
		payload.I32(int32(tc.frameLength))
		payload.I32(tc.signalType)
		payload.I32(tc.useInterpolatedNLSFs)
		if tc.firstFrameAfterReset {
			payload.I32(1)
		} else {
			payload.I32(0)
		}
		payload.I32(tc.speechActivityQ8)
		payload.I32(int32(tc.nlsfMSVQSurvivors))
		payload.I32(tc.condCoding)
		payload.I32(tc.packetLossPerc)
		payload.I32(tc.nFramesPerPacket)
		payload.I32(tc.lbrrFlag)
		payload.I32(tc.snrDBQ7)
		payload.I32(tc.codingQualityQ14)
		payload.I32(tc.sumLogGainQ7)
		for j := 0; j < maxLPCOrder; j++ {
			payload.I16(tc.prevNLSFqQ15[j])
		}
		for j := 0; j < maxNbSubfr; j++ {
			payload.I32(tc.gainsQ16[j])
		}
		for j := 0; j < maxNbSubfr; j++ {
			payload.I32(int32(tc.pitchL[j]))
		}
		payload.U32(uint32(len(tc.resPitch)))
		payload.U32(uint32(tc.resPitchStart))
		for _, v := range tc.resPitch {
			payload.I16(v)
		}
		payload.U32(uint32(len(tc.x)))
		payload.U32(uint32(tc.xStart))
		for _, v := range tc.x {
			payload.I16(v)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed find_pred_coefs", libopusSILKFixedFindPredOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]silkFixedFindPredResult, count)
	for i := range out {
		r := &out[i]
		for j := 0; j < maxLPCOrder; j++ {
			r.predCoef0[j] = reader.I32()
		}
		for j := 0; j < maxLPCOrder; j++ {
			r.predCoef1[j] = reader.I32()
		}
		for j := 0; j < ltpOrder*maxNbSubfr; j++ {
			r.ltpCoefQ14[j] = reader.I32()
		}
		r.ltpScaleQ14 = reader.I32()
		r.nlsfInterpCoef = reader.I32()
		r.perIndex = reader.I32()
		for j := 0; j < maxNbSubfr; j++ {
			r.ltpIndex[j] = reader.I32()
		}
		for j := 0; j < maxLPCOrder+1; j++ {
			r.nlsfIndices[j] = reader.I32()
		}
		for j := 0; j < maxNbSubfr; j++ {
			r.resNrg[j] = reader.I32()
		}
		for j := 0; j < maxNbSubfr; j++ {
			r.resNrgQ[j] = reader.I32()
		}
		r.ltpredCodGainQ7 = reader.I32()
		r.sumLogGainQ7 = reader.I32()
		for j := 0; j < maxLPCOrder; j++ {
			r.prevNLSFqQ15[j] = reader.I32()
		}
	}
	if err := reader.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKFindPredCoefsFixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0xF9ED))

	// makePrevNLSF builds a monotonically increasing prev NLSF vector.
	makePrevNLSF := func(order int) [maxLPCOrder]int16 {
		var p [maxLPCOrder]int16
		spacing := int16((1 << 15) / int32(order+1))
		v := spacing
		for i := 0; i < order; i++ {
			p[i] = v
			v += spacing
		}
		return p
	}

	// randSignal builds a low-frequency-correlated int16 signal.
	randSignal := func(n int, amp int32) []int16 {
		x := make([]int16, n)
		var acc int32
		for i := range x {
			acc += rng.Int31n(2*amp+1) - amp
			if acc > 32767 {
				acc = 32767
			} else if acc < -32768 {
				acc = -32768
			}
			x[i] = int16(acc >> 4)
		}
		return x
	}

	survivorsFor := func(order int) int {
		if order == 16 {
			return 16
		}
		return 8
	}

	makeCase := func(order, fsKHz, nbSubfr int, signalType int32, useInterp, firstFrame bool, condCoding int32, amp int32) silkFindPredCoefsInput {
		subfrLength := 5 * fsKHz
		frameLength := nbSubfr * subfrLength

		in := silkFindPredCoefsInput{
			predictLPCOrder:      order,
			subfrLength:          subfrLength,
			nbSubfr:              nbSubfr,
			frameLength:          frameLength,
			signalType:           signalType,
			speechActivityQ8:     int32(rng.Intn(257)),
			nlsfMSVQSurvivors:    survivorsFor(order),
			condCoding:           condCoding,
			packetLossPerc:       int32(rng.Intn(20)),
			nFramesPerPacket:     1 + int32(rng.Intn(3)),
			lbrrFlag:             int32(rng.Intn(2)),
			snrDBQ7:              int32(18+rng.Intn(20)) << 7,
			codingQualityQ14:     int32(rng.Intn(1 << 14)),
			sumLogGainQ7:         int32(rng.Intn(4096)),
			prevNLSFqQ15:         makePrevNLSF(order),
			firstFrameAfterReset: firstFrame,
		}
		if useInterp {
			in.useInterpolatedNLSFs = 1
		}

		// Per-subframe gains in Q16, all positive.
		for k := 0; k < nbSubfr; k++ {
			in.gainsQ16[k] = int32(65536) + rng.Int31n(1<<22)
		}

		// pitch lags within PE range; ltp_mem_length - predictLPCOrder must be
		// >= pitchL[0] + LTP_ORDER/2 (asserted only with hardening, but keep it
		// realistic). ltp_mem_length = 20 ms * fsKHz.
		ltpMem := 20 * fsKHz
		maxLag := ltpMem - order - ltpOrder/2 - 1
		if maxLag > 18*fsKHz {
			maxLag = 18 * fsKHz
		}
		minLag := 2 * fsKHz
		for k := 0; k < nbSubfr; k++ {
			in.pitchL[k] = int32(minLag + rng.Intn(maxLag-minLag+1))
		}

		// res_pitch buffer: r_ptr starts at res_start; lag_ptr reaches back
		// res_start - (maxPitch + LTP_ORDER/2); last subframe reads up to
		// res_start + (nbSubfr-1)*subfrLength + subfrLength + LTP_ORDER.
		maxPitch := 0
		for k := 0; k < nbSubfr; k++ {
			if int(in.pitchL[k]) > maxPitch {
				maxPitch = int(in.pitchL[k])
			}
		}
		resStart := maxPitch + ltpOrder/2
		resLen := resStart + nbSubfr*subfrLength + ltpOrder
		in.resPitch = randSignal(resLen, amp)
		in.resPitchStart = resStart

		// x buffer: analysis reads x - predictLPCOrder and, for voiced, the LTP
		// FIR reads x - predictLPCOrder - pitchL[k] - 2 (x_lag_ptr[-2]). Provide
		// maxPitch + predictLPCOrder + 2 history before xStart, then frame_length.
		xHist := maxPitch + order + 2
		xLen := xHist + frameLength
		in.x = randSignal(xLen, amp)
		in.xStart = xHist

		return in
	}

	var cases []silkFindPredCoefsInput
	signalTypes := []int32{typeNoVoiceActivity, typeUnvoiced, typeVoiced}
	condCodings := []int32{codeIndependently, codeConditionally}

	for _, order := range []int{10, 16} {
		for _, fs := range []int{8, 12, 16} {
			for _, nb := range []int{2, 4} {
				for _, st := range signalTypes {
					for _, useInterp := range []bool{false, true} {
						for _, first := range []bool{false, true} {
							for _, cc := range condCodings {
								// NLSF interpolation requires nb_subfr==4.
								ui := useInterp && nb == 4
								cases = append(cases, makeCase(order, fs, nb, st, ui, first, cc, 6000))
							}
						}
					}
				}
			}
		}
	}

	// Bulk random coverage.
	for i := 0; i < 120; i++ {
		order := []int{10, 16}[rng.Intn(2)]
		fs := []int{8, 12, 16}[rng.Intn(3)]
		nb := []int{2, 4}[rng.Intn(2)]
		st := signalTypes[rng.Intn(len(signalTypes))]
		ui := nb == 4 && rng.Intn(2) == 0
		first := rng.Intn(2) == 0
		cc := condCodings[rng.Intn(len(condCodings))]
		amp := []int32{200, 4000, 30000}[rng.Intn(3)]
		cases = append(cases, makeCase(order, fs, nb, st, ui, first, cc, amp))
	}

	want, err := probeLibopusSILKFixedFindPred(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed find_pred_coefs", err)
		return
	}

	for i := range cases {
		in := cases[i]
		var enc Encoder
		var cb *nlsfCB
		if in.predictLPCOrder == 16 {
			cb = &silk_NLSF_CB_WB
		} else {
			cb = &silk_NLSF_CB_NB_MB
		}
		in.cb = cb

		got := enc.silkFindPredCoefsFIX(&in)
		w := want[i]

		fail := func(field string, g, e interface{}) {
			t.Fatalf("case %d (order=%d fs nb=%d st=%d interp=%d first=%v cc=%d): %s got %v want %v",
				i, cases[i].predictLPCOrder, cases[i].nbSubfr, cases[i].signalType,
				cases[i].useInterpolatedNLSFs, cases[i].firstFrameAfterReset, cases[i].condCoding,
				field, g, e)
		}

		order := in.predictLPCOrder
		nb := in.nbSubfr

		for j := 0; j < order; j++ {
			if int32(got.predCoefQ12[0][j]) != w.predCoef0[j] {
				fail(fmt.Sprintf("PredCoef_Q12[0][%d]", j), got.predCoefQ12[0][j], w.predCoef0[j])
			}
			if int32(got.predCoefQ12[1][j]) != w.predCoef1[j] {
				fail(fmt.Sprintf("PredCoef_Q12[1][%d]", j), got.predCoefQ12[1][j], w.predCoef1[j])
			}
		}
		for j := 0; j < nb*ltpOrder; j++ {
			if int32(got.ltpCoefQ14[j]) != w.ltpCoefQ14[j] {
				fail(fmt.Sprintf("LTPCoef_Q14[%d]", j), got.ltpCoefQ14[j], w.ltpCoefQ14[j])
			}
		}
		if got.ltpScaleQ14 != w.ltpScaleQ14 {
			fail("LTP_scale_Q14", got.ltpScaleQ14, w.ltpScaleQ14)
		}
		if int32(got.nlsfInterpCoefQ2) != w.nlsfInterpCoef {
			fail("NLSFInterpCoef_Q2", got.nlsfInterpCoefQ2, w.nlsfInterpCoef)
		}
		if int32(got.perIndex) != w.perIndex {
			fail("PERIndex", got.perIndex, w.perIndex)
		}
		for j := 0; j < nb; j++ {
			if int32(got.ltpIndex[j]) != w.ltpIndex[j] {
				fail(fmt.Sprintf("LTPIndex[%d]", j), got.ltpIndex[j], w.ltpIndex[j])
			}
		}
		for j := 0; j < order+1; j++ {
			if int32(got.nlsfIndices[j]) != w.nlsfIndices[j] {
				fail(fmt.Sprintf("NLSFIndices[%d]", j), got.nlsfIndices[j], w.nlsfIndices[j])
			}
		}
		for j := 0; j < nb; j++ {
			if got.resNrg[j] != w.resNrg[j] {
				fail(fmt.Sprintf("ResNrg[%d]", j), got.resNrg[j], w.resNrg[j])
			}
			if int32(got.resNrgQ[j]) != w.resNrgQ[j] {
				fail(fmt.Sprintf("ResNrgQ[%d]", j), got.resNrgQ[j], w.resNrgQ[j])
			}
		}
		if got.ltpredCodGainQ7 != w.ltpredCodGainQ7 {
			fail("LTPredCodGain_Q7", got.ltpredCodGainQ7, w.ltpredCodGainQ7)
		}
		if got.sumLogGainQ7 != w.sumLogGainQ7 {
			fail("sum_log_gain_Q7", got.sumLogGainQ7, w.sumLogGainQ7)
		}
		for j := 0; j < maxLPCOrder; j++ {
			if int32(got.prevNLSFqQ15[j]) != w.prevNLSFqQ15[j] {
				fail(fmt.Sprintf("prev_NLSFq_Q15[%d]", j), got.prevNLSFqQ15[j], w.prevNLSFqQ15[j])
			}
		}
	}
}
