package silk

import (
	"fmt"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"math"
	"math/rand"
	"testing"
)

func decodeExcitationReference(exc []int32, pulses []int16, seed, offsetQ10 int32) {
	for i := range exc {
		seed = silkRand(seed)
		exc[i] = int32(pulses[i]) << 14
		if exc[i] > 0 {
			exc[i] -= quantLevelAdjustQ10 << 4
		} else if exc[i] < 0 {
			exc[i] += quantLevelAdjustQ10 << 4
		}
		exc[i] += offsetQ10 << 4
		if seed < 0 {
			exc[i] = -exc[i]
		}
		seed += int32(pulses[i])
	}
}

func TestDecodeExcitationMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0xe4c))
	for iter := 0; iter < 5000; iter++ {
		n := 1 + rng.Intn(maxFrameLength)
		pulses := make([]int16, n)
		for i := range pulses {
			switch rng.Intn(4) {
			case 0:
				pulses[i] = int16(rng.Uint32())
			case 1:
				pulses[i] = 0
			default:
				pulses[i] = int16(rng.Intn(31) - 15)
			}
		}
		seed := int32(rng.Uint32())
		offsetQ10 := int32(silk_Quantization_Offsets_Q10[rng.Intn(2)][rng.Intn(2)])
		got := make([]int32, n)
		want := make([]int32, n)
		silkDecodeExcitation(got, pulses, seed, offsetQ10<<4)
		decodeExcitationReference(want, pulses, seed, offsetQ10)
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("iter %d: exc[%d] = %d, want %d", iter, i, got[i], want[i])
			}
		}
	}
}

// ltpSynthesisReference is the voiced LTP loop of libopus
// silk/decode_core.c silk_decode_core written literally.
func ltpSynthesisReference(presQ14, exc, sLTPQ15 []int32, bufIdx, lag int, bQ14 []int16) {
	predLag := bufIdx - lag + ltpOrder/2
	for i := range presQ14 {
		ltpPredQ13 := int32(2)
		ltpPredQ13 = silkSMLAWB(ltpPredQ13, sLTPQ15[predLag+0], int32(bQ14[0]))
		ltpPredQ13 = silkSMLAWB(ltpPredQ13, sLTPQ15[predLag-1], int32(bQ14[1]))
		ltpPredQ13 = silkSMLAWB(ltpPredQ13, sLTPQ15[predLag-2], int32(bQ14[2]))
		ltpPredQ13 = silkSMLAWB(ltpPredQ13, sLTPQ15[predLag-3], int32(bQ14[3]))
		ltpPredQ13 = silkSMLAWB(ltpPredQ13, sLTPQ15[predLag-4], int32(bQ14[4]))
		predLag++
		presQ14[i] = silkADD_LSHIFT32(exc[i], ltpPredQ13, 1)
		sLTPQ15[bufIdx] = silkLSHIFT(presQ14[i], 1)
		bufIdx++
	}
}

func TestLTPSynthesisMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0x17b))
	for iter := 0; iter < 20000; iter++ {
		fsKHz := []int{8, 12, 16}[rng.Intn(3)]
		subfr := 5 * fsKHz
		ltpMem := 20 * fsKHz
		// Lags shorter than the subframe read outputs of the same call.
		lag := 2*fsKHz + rng.Intn(16*fsKHz+1)
		bufIdx := ltpMem + subfr*rng.Intn(4)
		wide := rng.Intn(3) == 0
		state := make([]int32, ltpMem+4*subfr)
		for i := range state {
			if wide {
				state[i] = int32(rng.Uint32())
			} else {
				state[i] = int32(rng.Intn(1<<22)) - 1<<21
			}
		}
		exc := make([]int32, subfr)
		for i := range exc {
			if wide {
				exc[i] = int32(rng.Uint32())
			} else {
				exc[i] = int32(rng.Intn(1<<20)) - 1<<19
			}
		}
		var b [ltpOrder]int16
		for i := range b {
			if wide {
				b[i] = int16(rng.Uint32())
			} else {
				b[i] = int16(rng.Intn(1<<14) - 1<<13)
			}
		}
		gotState := append([]int32(nil), state...)
		wantState := append([]int32(nil), state...)
		got := make([]int32, subfr)
		want := make([]int32, subfr)
		silkLTPSynthesis(got, exc, gotState, bufIdx, lag, &b)
		ltpSynthesisReference(want, exc, wantState, bufIdx, lag, b[:])
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("iter %d lag %d: presQ14[%d] = %d, want %d", iter, lag, i, got[i], want[i])
			}
		}
		for i := range wantState {
			if gotState[i] != wantState[i] {
				t.Fatalf("iter %d lag %d: sLTP_Q15[%d] = %d, want %d", iter, lag, i, gotState[i], wantState[i])
			}
		}
	}
}

func TestDecodeCoreKernelsDoNotAllocate(t *testing.T) {
	pulses := make([]int16, maxFrameLength)
	exc := make([]int32, maxFrameLength)
	state := make([]int32, 2*maxFrameLength)
	pres := make([]int32, maxSubFrameLength)
	var b [ltpOrder]int16
	if allocs := testing.AllocsPerRun(100, func() {
		silkDecodeExcitation(exc, pulses, 1, 3)
		silkLTPSynthesis(pres, exc, state, maxFrameLength, 100, &b)
	}); allocs != 0 {
		t.Fatalf("allocs/run = %v, want 0", allocs)
	}
}

// clz32Reference counts leading zeros by halving, as the portable
// silk_CLZ32 fallback does.
func clz32Reference(x int32) int {
	if x == 0 {
		return 32
	}
	if x < 0 {
		return 0
	}
	n := 0
	ux := uint32(x)
	for ux&0x80000000 == 0 {
		n++
		ux <<= 1
	}
	return n
}

func TestCLZ32MatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0xc12))
	check := func(x int32) {
		if got, want := silk_CLZ32(x), clz32Reference(x); got != want {
			t.Fatalf("silk_CLZ32(%d) = %d, want %d", x, got, want)
		}
	}
	for _, x := range []int32{0, 1, -1, 0x7fffffff, -0x80000000, 0x10000, 0xffff} {
		check(x)
	}
	for range 100000 {
		check(int32(rng.Uint32()) >> uint(rng.Intn(32)))
	}
}

func TestInterleaveStereoFloat32(t *testing.T) {
	left := []float32{1, 2, 3}
	right := []float32{-1, -2, -3, -4}
	dst := make([]float32, 6)
	interleaveStereoFloat32(dst, left, right)
	want := []float32{1, -1, 2, -2, 3, -3}
	for i := range want {
		if dst[i] != want[i] {
			t.Fatalf("dst[%d] = %v, want %v", i, dst[i], want[i])
		}
	}
}

func lpcSynthesisReference(sLPC []int32, aQ12 []int16, presQ14 []int32, pxq []int16, gainQ10 int32, subfrLength, order int) {
	for i := range subfrLength {
		lpcPredQ10 := int32(order >> 1)
		for j := range order {
			lpcPredQ10 = silkSMLAWB(lpcPredQ10, sLPC[maxLPCOrder+i-j-1], int32(aQ12[j]))
		}
		sLPC[maxLPCOrder+i] = silkAddSat32(presQ14[i], silkLShiftSAT32(lpcPredQ10, 4))
		pxq[i] = silkSAT16(silkRSHIFT_ROUND(silkSMULWW(sLPC[maxLPCOrder+i], gainQ10), 8))
	}
}

func randomLPCSynthesisCase(rng *rand.Rand) (sLPC [maxLPCOrder + maxSubFrameLength]int32, a [maxLPCOrder]int16, pres [maxSubFrameLength]int32, gainQ10 int32) {
	// Mix realistic (small, stable) cases with full-range ones that saturate.
	wide := rng.Intn(3) == 0
	for i := range sLPC {
		if wide {
			sLPC[i] = int32(rng.Uint32())
		} else {
			sLPC[i] = int32(rng.Intn(1<<20)) - 1<<19
		}
	}
	for i := range a {
		if wide {
			a[i] = int16(rng.Uint32())
		} else {
			a[i] = int16(rng.Intn(8192) - 4096)
		}
	}
	for i := range pres {
		if wide {
			pres[i] = int32(rng.Uint32())
		} else {
			pres[i] = int32(rng.Intn(1<<18)) - 1<<17
		}
	}
	gainQ10 = int32(rng.Intn(1 << 22))
	if wide {
		gainQ10 = int32(rng.Uint32())
	}
	return
}

func TestLPCSynthesisMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0x1c5))
	for iter := 0; iter < 20000; iter++ {
		sLPC, a, pres, gainQ10 := randomLPCSynthesisCase(rng)
		n := 1 + rng.Intn(maxSubFrameLength)
		for _, order := range []int{minLPCOrder, maxLPCOrder} {
			got, want := sLPC, sLPC
			var gotOut, wantOut [maxSubFrameLength]int16
			if order == maxLPCOrder {
				synthesizeLPCOrder16(got[:], a[:], pres[:], gotOut[:], gainQ10, n)
			} else {
				synthesizeLPCOrder10(got[:], a[:], pres[:], gotOut[:], gainQ10, n)
			}
			lpcSynthesisReference(want[:], a[:], pres[:], wantOut[:], gainQ10, n, order)
			if got != want || gotOut != wantOut {
				for k := range got {
					if got[k] != want[k] {
						t.Errorf("sLPC[%d] got %d want %d", k, got[k], want[k])
						break
					}
				}
				for k := range gotOut {
					if gotOut[k] != wantOut[k] {
						t.Errorf("pxq[%d] got %d want %d", k, gotOut[k], wantOut[k])
						break
					}
				}
				t.Fatalf("iter %d order %d n %d: synthesis mismatch", iter, order, n)
			}
		}
	}
}

func TestLShiftSAT32By4MatchesLShiftSAT32(t *testing.T) {
	rng := rand.New(rand.NewSource(0x5a7))
	edges := []int32{0, 1, -1, 0x07ffffff, 0x08000000, -0x08000000, -0x08000001, 1<<31 - 1, -1 << 31}
	for i := 0; i < 200000; i++ {
		x := int32(rng.Uint32())
		if i < len(edges) {
			x = edges[i]
		}
		if got, want := lShiftSAT32By4(x), silkLShiftSAT32(x, 4); got != want {
			t.Fatalf("lShiftSAT32By4(%d) = %d, want %d", x, got, want)
		}
	}
}

// lpcAnalysisFilterReference is libopus silk/LPC_analysis_filter.c
// silk_LPC_analysis_filter written literally.
func lpcAnalysisFilterReference(out, in, B []int16, length, order int) {
	for ix := order; ix < length; ix++ {
		outQ12 := silkSMULBB(int32(in[ix-1]), int32(B[0]))
		for j := 1; j < order; j++ {
			outQ12 = silkSMLABB(outQ12, int32(in[ix-1-j]), int32(B[j]))
		}
		out[ix] = silkSAT16(silkRSHIFT_ROUND(silkLSHIFT(int32(in[ix]), 12)-outQ12, 12))
	}
	for i := range order {
		out[i] = 0
	}
}

func TestLPCAnalysisFilterMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0xa7a))
	for iter := 0; iter < 20000; iter++ {
		order := []int{minLPCOrder, maxLPCOrder, 12, 6}[rng.Intn(4)]
		length := order + rng.Intn(330)
		in := make([]int16, length+rng.Intn(4))
		wide := rng.Intn(3) == 0
		for i := range in {
			if wide {
				in[i] = int16(rng.Uint32())
			} else {
				in[i] = int16(rng.Intn(4000) - 2000)
			}
		}
		B := make([]int16, order)
		for i := range B {
			if wide {
				B[i] = int16(rng.Uint32())
			} else {
				B[i] = int16(rng.Intn(8192) - 4096)
			}
		}
		got := make([]int16, length)
		want := make([]int16, length)
		for i := range got {
			got[i] = int16(rng.Uint32())
			want[i] = got[i]
		}
		silkLPCAnalysisFilter(got, in, B, length, order)
		lpcAnalysisFilterReference(want, in, B, length, order)
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("iter %d order %d length %d: out[%d] = %d, want %d", iter, order, length, i, got[i], want[i])
			}
		}
	}
}

// stereoMSToLRReference is libopus silk/stereo_MS_to_LR.c silk_stereo_MS_to_LR
// written literally.
func stereoMSToLRReference(state *stereoDecState, x1, x2 []int16, predQ13 []int32, fsKHz, frameLength int) {
	copy(x1, state.sMid[:])
	copy(x2, state.sSide[:])
	copy(state.sMid[:], x1[frameLength:frameLength+2])
	copy(state.sSide[:], x2[frameLength:frameLength+2])
	pred0 := int32(state.predPrevQ13[0])
	pred1 := int32(state.predPrevQ13[1])
	denomQ16 := int32((1 << 16) / (stereoInterpLenMs * fsKHz))
	delta0 := silkRSHIFT_ROUND(silkSMULBB(predQ13[0]-int32(state.predPrevQ13[0]), denomQ16), 16)
	delta1 := silkRSHIFT_ROUND(silkSMULBB(predQ13[1]-int32(state.predPrevQ13[1]), denomQ16), 16)
	step := func(n int) {
		sum := silkLSHIFT(silkADD_LSHIFT32(int32(x1[n])+int32(x1[n+2]), int32(x1[n+1]), 1), 9)
		sum = silkSMLAWB(silkLSHIFT(int32(x2[n+1]), 8), sum, pred0)
		sum = silkSMLAWB(sum, silkLSHIFT(int32(x1[n+1]), 11), pred1)
		x2[n+1] = silkSAT16(silkRSHIFT_ROUND(sum, 8))
	}
	for n := 0; n < stereoInterpLenMs*fsKHz; n++ {
		pred0 += delta0
		pred1 += delta1
		step(n)
	}
	pred0 = predQ13[0]
	pred1 = predQ13[1]
	for n := stereoInterpLenMs * fsKHz; n < frameLength; n++ {
		step(n)
	}
	state.predPrevQ13[0] = int16(predQ13[0])
	state.predPrevQ13[1] = int16(predQ13[1])
	for n := range frameLength {
		sum := int32(x1[n+1]) + int32(x2[n+1])
		diff := int32(x1[n+1]) - int32(x2[n+1])
		x1[n+1] = silkSAT16(sum)
		x2[n+1] = silkSAT16(diff)
	}
}

func TestStereoMSToLRMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0x57e))
	for iter := 0; iter < 20000; iter++ {
		fsKHz := []int{8, 12, 16}[rng.Intn(3)]
		frameLength := fsKHz * []int{10, 20}[rng.Intn(2)]
		wide := rng.Intn(3) == 0
		sample := func() int16 {
			if wide {
				return int16(rng.Uint32())
			}
			return int16(rng.Intn(8000) - 4000)
		}
		mid := make([]int16, frameLength+2)
		side := make([]int16, frameLength+2)
		for i := range mid {
			mid[i] = sample()
			side[i] = sample()
		}
		var state stereoDecState
		for i := range 2 {
			state.sMid[i] = sample()
			state.sSide[i] = sample()
			state.predPrevQ13[i] = int16(rng.Intn(1<<15) - 1<<14)
		}
		predQ13 := []int32{int32(rng.Intn(1<<15) - 1<<14), int32(rng.Intn(1<<15) - 1<<14)}
		if wide {
			predQ13[0] = int32(int16(rng.Uint32()))
			predQ13[1] = int32(int16(rng.Uint32()))
			state.predPrevQ13[0] = int16(rng.Uint32())
		}
		wantState := state
		wantMid := append([]int16(nil), mid...)
		wantSide := append([]int16(nil), side...)
		silkStereoMSToLR(&state, mid, side, predQ13, fsKHz, frameLength)
		stereoMSToLRReference(&wantState, wantMid, wantSide, predQ13, fsKHz, frameLength)
		if state != wantState {
			t.Fatalf("iter %d: state %+v want %+v", iter, state, wantState)
		}
		for i := range mid {
			if mid[i] != wantMid[i] || side[i] != wantSide[i] {
				t.Fatalf("iter %d fs=%d len=%d: sample %d got (%d,%d) want (%d,%d)", iter, fsKHz, frameLength, i, mid[i], side[i], wantMid[i], wantSide[i])
			}
		}
	}
}

func TestUp2HQCoreMatchesScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(0x2b9))
	for iter := 0; iter < 5000; iter++ {
		n := rng.Intn(500)
		in := make([]int16, n)
		for i := range in {
			in[i] = int16(rng.Uint32())
		}
		var state [6]int32
		for i := range state {
			state[i] = int32(rng.Uint32())
			if rng.Intn(2) == 0 {
				state[i] >>= 6
			}
		}
		wantState := state
		got := make([]int16, 2*n)
		want := make([]int16, 2*n)
		up2HQCore(got, in, &state)
		up2HQCoreGo(want, in, &wantState)
		if state != wantState {
			t.Fatalf("iter %d n %d: state %v want %v", iter, n, state, wantState)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("iter %d n %d: out[%d] = %d, want %d", iter, n, i, got[i], want[i])
			}
		}
	}
}

// firInterpolLibopusRef is silk_resampler_private_IIR_FIR_INTERPOL as written
// in libopus: the phase's four taps from frac_FIR_12 and the mirrored phase's
// taps in reverse, accumulated in order with int32 wraparound.
func firInterpolLibopusRef(out []int16, buf []int16, incr int32) {
	indexQ16 := int32(0)
	for n := range out {
		tableIndex := int32((int64(indexQ16&0xFFFF) * 12) >> 16)
		b := buf[indexQ16>>16:]
		f := silkResamplerFracFIR12Flat[tableIndex*4:]
		m := silkResamplerFracFIR12Flat[(11-tableIndex)*4:]
		res := int32(b[0]) * int32(f[0])
		res += int32(b[1]) * int32(f[1])
		res += int32(b[2]) * int32(f[2])
		res += int32(b[3]) * int32(f[3])
		res += int32(b[4]) * int32(m[3])
		res += int32(b[5]) * int32(m[2])
		res += int32(b[6]) * int32(m[1])
		res += int32(b[7]) * int32(m[0])
		res = ((res >> 14) + 1) >> 1
		out[n] = int16(max(-32768, min(32767, res)))
		indexQ16 += incr
	}
}

func TestFIRInterpolMatchesGeneric(t *testing.T) {
	rng := rand.New(rand.NewSource(0xf12))
	incrs := []int32{21846, 32768, 43691, 65536, 87381, 87382, 131072, 98304, 49152}
	for iter := 0; iter < 5000; iter++ {
		incr := incrs[rng.Intn(len(incrs))]
		if rng.Intn(4) == 0 {
			incr = int32(8192 + rng.Intn(3<<16))
		}
		nIn := 1 + rng.Intn(480)
		maxIndexQ16 := int32(nIn) << 17
		buf := make([]int16, 2*nIn+resamplerOrderFIR12)
		for i := range buf {
			buf[i] = int16(rng.Uint32())
		}
		nOut := int((maxIndexQ16 + incr - 1) / incr)
		r := &LibopusResampler{invRatioQ16: incr}
		got := make([]int16, nOut)
		want := make([]int16, nOut)
		if n := r.firInterpol(got, 0, buf, maxIndexQ16); n != nOut {
			t.Fatalf("iter %d: firInterpol wrote %d, want %d", iter, n, nOut)
		}
		firInterpolGeneric(want, buf, 0, incr)
		ref := make([]int16, nOut)
		firInterpolLibopusRef(ref, buf, incr)
		for i := range want {
			if want[i] != ref[i] {
				t.Fatalf("iter %d incr %d nIn %d: generic out[%d] = %d, libopus loop %d", iter, incr, nIn, i, want[i], ref[i])
			}
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("iter %d incr %d nIn %d: out[%d] = %d, want %d", iter, incr, nIn, i, got[i], want[i])
			}
		}
	}
}

func TestWriteInt16AsFloat32MatchesScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(0x1f3))
	for iter := 0; iter < 2000; iter++ {
		n := 1 + rng.Intn(1000)
		src := make([]int16, n)
		for i := range src {
			src[i] = int16(rng.Uint32())
		}
		src[0] = -32768
		got := make([]float32, n)
		writeInt16AsFloat32Core(got, src, n)
		for i, s := range src {
			if want := float32(s) / 32768; math.Float32bits(got[i]) != math.Float32bits(want) {
				t.Fatalf("n %d: dst[%d] = %v, want %v", n, i, got[i], want)
			}
		}
	}
}

func TestBandwidthConfig(t *testing.T) {
	tests := []struct {
		bw       Bandwidth
		wantLPC  int
		wantRate int
		wantMin  int
		wantMax  int
	}{
		{BandwidthNarrowband, 10, 8000, 16, 144},
		{BandwidthMediumband, 10, 12000, 24, 216},
		{BandwidthWideband, 16, 16000, 32, 288},
	}
	for _, tt := range tests {
		cfg := GetBandwidthConfig(tt.bw)
		if cfg.LPCOrder != tt.wantLPC {
			t.Errorf("LPCOrder for %v: got %d, want %d", tt.bw, cfg.LPCOrder, tt.wantLPC)
		}
		if cfg.SampleRate != tt.wantRate {
			t.Errorf("SampleRate for %v: got %d, want %d", tt.bw, cfg.SampleRate, tt.wantRate)
		}
		if cfg.PitchLagMin != tt.wantMin {
			t.Errorf("PitchLagMin for %v: got %d, want %d", tt.bw, cfg.PitchLagMin, tt.wantMin)
		}
		if cfg.PitchLagMax != tt.wantMax {
			t.Errorf("PitchLagMax for %v: got %d, want %d", tt.bw, cfg.PitchLagMax, tt.wantMax)
		}
	}
}

func TestFrameTypeInactive(t *testing.T) {
	// Encode an inactive frame type (typeOffset=0) and decode it.
	var enc rangecoding.Encoder
	buf := make([]byte, 32)
	enc.Init(buf)
	enc.EncodeICDF16(0, ICDFFrameTypeVADInactive, 8)
	raw := enc.Done()

	d := NewDecoder()
	rd := &rangecoding.Decoder{}
	rd.Init(raw)
	d.SetRangeDecoder(rd)
	sig, quant := d.DecodeFrameType(false)
	if sig != 0 || quant != 0 {
		t.Errorf("Inactive frame: got (%d, %d), want (0, 0)", sig, quant)
	}
}

func TestFrameTypeInactiveHighOffsetRoundtrip(t *testing.T) {
	e := newTestEncoder(BandwidthWideband)
	var enc rangecoding.Encoder
	buf := make([]byte, 32)
	enc.Init(buf)
	e.rangeEncoder = &enc

	// VAD inactive with typeOffset=1 (signal=0, quantOffset=1).
	e.encodeFrameType(false, typeNoVoiceActivity, 1)
	raw := enc.Done()

	d := NewDecoder()
	rd := &rangecoding.Decoder{}
	rd.Init(raw)
	d.SetRangeDecoder(rd)
	sig, quant := d.DecodeFrameType(false)
	if sig != typeNoVoiceActivity || quant != 1 {
		t.Errorf("Inactive frame typeOffset=1: got (%d, %d), want (%d, %d)", sig, quant, typeNoVoiceActivity, 1)
	}
}

func TestFrameTypeActiveVoicedHighOffsetRoundtrip(t *testing.T) {
	e := newTestEncoder(BandwidthWideband)
	var enc rangecoding.Encoder
	buf := make([]byte, 32)
	enc.Init(buf)
	e.rangeEncoder = &enc

	// VAD active with typeOffset=5 (signal=2, quantOffset=1).
	e.encodeFrameType(true, typeVoiced, 1)
	raw := enc.Done()

	d := NewDecoder()
	rd := &rangecoding.Decoder{}
	rd.Init(raw)
	d.SetRangeDecoder(rd)
	sig, quant := d.DecodeFrameType(true)
	if sig != typeVoiced || quant != 1 {
		t.Errorf("Active frame typeOffset=5: got (%d, %d), want (%d, %d)", sig, quant, typeVoiced, 1)
	}
}

func TestFrameParamsStruct(t *testing.T) {
	// Verify FrameParams can be instantiated with expected fields
	params := FrameParams{
		SignalType:     2, // Voiced
		QuantOffset:    1, // High
		NumSubframes:   4,
		Gains:          make([]int32, 4),
		LPCOrder:       10,
		LPCCoeffs:      make([]int16, 10),
		PitchLags:      make([]int32, 4),
		LTPCoeffs:      make([][]int8, 4),
		LTPPeriodicity: 1,
	}

	if params.SignalType != 2 {
		t.Errorf("SignalType: got %d, want 2", params.SignalType)
	}
	if params.NumSubframes != 4 {
		t.Errorf("NumSubframes: got %d, want 4", params.NumSubframes)
	}
	if params.LPCOrder != 10 {
		t.Errorf("LPCOrder: got %d, want 10", params.LPCOrder)
	}
}

func TestGainDequantTableRange(t *testing.T) {
	// Verify GainDequantTable has 64 entries and values are in increasing order
	if len(GainDequantTable) != 64 {
		t.Errorf("GainDequantTable length: got %d, want 64", len(GainDequantTable))
	}

	// Values should be monotonically increasing
	for i := 1; i < len(GainDequantTable); i++ {
		if GainDequantTable[i] <= GainDequantTable[i-1] {
			t.Errorf("GainDequantTable not increasing at index %d: %d <= %d",
				i, GainDequantTable[i], GainDequantTable[i-1])
		}
	}

	// First value should be around 81920 (Q16 gain ~1.25), last around 1.68 billion (Q16 gain ~25700)
	// These are Q16 values, so 65536 = gain of 1.0
	if GainDequantTable[0] < 75000 || GainDequantTable[0] > 90000 {
		t.Errorf("GainDequantTable[0] unexpected: %d (expected ~81920)", GainDequantTable[0])
	}
	if GainDequantTable[63] < 1500000000 {
		t.Errorf("GainDequantTable[63] too small: %d (expected ~1686110208)", GainDequantTable[63])
	}
}

func TestLSFMinSpacingLength(t *testing.T) {
	// Verify minimum spacing tables have correct length
	// NB/MB: 10 coefficients + 1 = 11
	if len(LSFMinSpacingNBMB) != 11 {
		t.Errorf("LSFMinSpacingNBMB length: got %d, want 11", len(LSFMinSpacingNBMB))
	}

	// WB: 16 coefficients + 1 = 17
	if len(LSFMinSpacingWB) != 17 {
		t.Errorf("LSFMinSpacingWB length: got %d, want 17", len(LSFMinSpacingWB))
	}
}

func TestCosineTableRange(t *testing.T) {
	// CosineTable should have 129 entries for [0, pi]
	if len(CosineTable) != 129 {
		t.Errorf("CosineTable length: got %d, want 129", len(CosineTable))
	}

	// cos(0) = 1.0 = 4096 in Q12
	if CosineTable[0] != 4096 {
		t.Errorf("CosineTable[0]: got %d, want 4096", CosineTable[0])
	}

	// cos(pi) = -1.0 = -4096 in Q12
	if CosineTable[128] != -4096 {
		t.Errorf("CosineTable[128]: got %d, want -4096", CosineTable[128])
	}

	// cos(pi/2) = 0
	if CosineTable[64] != 0 {
		t.Errorf("CosineTable[64]: got %d, want 0", CosineTable[64])
	}
}

func TestLTPFilterCodebookSizes(t *testing.T) {
	// Verify LTP filter codebook sizes
	if len(LTPFilterLow) != 8 {
		t.Errorf("LTPFilterLow length: got %d, want 8", len(LTPFilterLow))
	}
	if len(LTPFilterMid) != 16 {
		t.Errorf("LTPFilterMid length: got %d, want 16", len(LTPFilterMid))
	}
	if len(LTPFilterHigh) != 32 {
		t.Errorf("LTPFilterHigh length: got %d, want 32", len(LTPFilterHigh))
	}

	// Each entry should have 5 taps
	for i, taps := range LTPFilterLow {
		if len(taps) != 5 {
			t.Errorf("LTPFilterLow[%d] taps: got %d, want 5", i, len(taps))
		}
	}
}

func TestPitchContourSizes(t *testing.T) {
	// Verify pitch contour table sizes
	if len(PitchContourNB10ms) != 16 {
		t.Errorf("PitchContourNB10ms length: got %d, want 16", len(PitchContourNB10ms))
	}
	if len(PitchContourNB20ms) != 16 {
		t.Errorf("PitchContourNB20ms length: got %d, want 16", len(PitchContourNB20ms))
	}

	// 10ms has 2 subframes, 20ms has 4 subframes
	if len(PitchContourNB10ms[0]) != 2 {
		t.Errorf("PitchContourNB10ms[0] subframes: got %d, want 2", len(PitchContourNB10ms[0]))
	}
	if len(PitchContourNB20ms[0]) != 4 {
		t.Errorf("PitchContourNB20ms[0] subframes: got %d, want 4", len(PitchContourNB20ms[0]))
	}
}

func TestStabilizeLSF(t *testing.T) {
	// Test that stabilizeLSF enforces minimum spacing
	lsf := []int16{100, 100, 100, 100, 100, 100, 100, 100, 100, 100}

	// NB/MB: 10 coefficients
	stabilizeLSF(lsf, false)

	// After stabilization, values should be strictly increasing
	for i := 1; i < len(lsf); i++ {
		if lsf[i] <= lsf[i-1] {
			t.Errorf("LSF not increasing after stabilize at index %d: %d <= %d",
				i, lsf[i], lsf[i-1])
		}
	}

	// Values should remain non-negative Q15 values.
	for i, v := range lsf {
		if v < 0 {
			t.Errorf("LSF[%d] is negative after stabilize: %d", i, v)
		}
	}
}

func TestDecoderState(t *testing.T) {
	d := NewDecoder()

	// Initial state should be clean
	if d.HaveDecoded() {
		t.Error("New decoder should not have decoded")
	}
	if d.PreviousLogGain() != 0 {
		t.Errorf("Initial previousLogGain: got %d, want 0", d.PreviousLogGain())
	}

	// Test state updates
	d.MarkDecoded()
	if !d.HaveDecoded() {
		t.Error("Decoder should be marked as decoded")
	}

	d.SetPreviousLogGain(42)
	if d.PreviousLogGain() != 42 {
		t.Errorf("previousLogGain: got %d, want 42", d.PreviousLogGain())
	}

	// Test reset
	d.Reset()
	if d.HaveDecoded() {
		t.Error("Reset decoder should not have decoded")
	}
	if d.PreviousLogGain() != 0 {
		t.Errorf("Reset previousLogGain: got %d, want 0", d.PreviousLogGain())
	}
}

var silkDecodeFuzzBandwidths = []Bandwidth{
	BandwidthNarrowband,
	BandwidthMediumband,
	BandwidthWideband,
	Bandwidth(3), // SWB: rejected by Decode/DecodeStereo as non-SILK.
	Bandwidth(255),
}

// silkDecodeFuzzFrameSizes covers every legal 48 kHz SILK frame size plus a
// few values that do not map to a SILK duration (0, tiny, huge, odd).
var silkDecodeFuzzFrameSizes = []int{
	0,
	1,
	480,  // 10ms @48k
	960,  // 20ms @48k
	1920, // 40ms @48k
	2880, // 60ms @48k
	123,  // unmapped
	100000,
}

func requireFiniteSILKPCM(t *testing.T, samples []float32) {
	t.Helper()
	for i, s := range samples {
		if math.IsNaN(float64(s)) || math.IsInf(float64(s), 0) {
			t.Fatalf("sample[%d] is not finite: %v", i, s)
		}
	}
}

func addSILKDecodeSeeds(f *testing.F) {
	seeds := [][]byte{
		nil,
		{},
		{0x00},
		{0xFF},
		{0x00, 0x00, 0x00, 0x00},
		{0xFF, 0xFF, 0xFF, 0xFF},
		{0x80, 0x01, 0x02, 0x03, 0x04, 0x05},
		make([]byte, 64),  // all-zero medium
		make([]byte, 320), // all-zero large
	}
	// A pseudo-entropy-coded buffer like createMockRangeDecoder uses.
	mock := make([]byte, 128)
	for i := range mock {
		mock[i] = byte(i * 7)
	}
	seeds = append(seeds, mock)

	for _, s := range seeds {
		f.Add(s, uint8(2), uint8(3), true) // WB / 20ms-ish / vad
		f.Add(s, uint8(0), uint8(1), false)
	}

	// Regression: a 1-byte malformed packet decoded as WB primes the decoder
	// state, then the follow-up nil-data PLC at a 60 ms (2880-sample) frame size
	// requested an oversized single-call concealment that overflowed the
	// fixed-size CNG synthesis buffer (cng.go silk_CNG). bwSel 2 -> WB,
	// fsSel 5 -> 2880.
	f.Add([]byte{0x00}, uint8(2), uint8(5), true)
	// Regression: stereo PLC with frameSizeSamples 0 mapped to zero native
	// samples and reached silkStereoMSToLR with frameLength 0, reading past the
	// mid/side history. fsSel 0 -> frame size 0.
	f.Add([]byte{0x00}, uint8(2), uint8(0), true)
}

// FuzzSILKDecodeMonoNeverPanics drives Decoder.Decode (the primary public mono
// SILK entry point) with arbitrary bytes and parameters. It also issues a
// follow-up nil-data PLC decode to exercise the loss path on whatever state the
// malformed packet left behind.
//
// Mirrors the public SILK decode entry of libopus silk/dec_API.c silk_Decode.
func FuzzSILKDecodeMonoNeverPanics(f *testing.F) {
	addSILKDecodeSeeds(f)

	f.Fuzz(func(t *testing.T, data []byte, bwSel, fsSel uint8, vad bool) {
		if len(data) > 8192 {
			data = data[:8192]
		}
		bw := silkDecodeFuzzBandwidths[int(bwSel)%len(silkDecodeFuzzBandwidths)]
		fs := silkDecodeFuzzFrameSizes[int(fsSel)%len(silkDecodeFuzzFrameSizes)]

		d := NewDecoder()
		out, err := d.Decode(data, bw, fs, vad)
		if err == nil {
			requireFiniteSILKPCM(t, out)
		}

		// Follow-up loss concealment on the same decoder state.
		plcOut, plcErr := d.Decode(nil, bw, fs, vad)
		if plcErr == nil {
			requireFiniteSILKPCM(t, plcOut)
		}
	})
}

// FuzzSILKDecodeStereoNeverPanics drives the stereo public entry points
// (DecodeStereo, DecodeStereoToMono, DecodeMonoToStereo) with malformed bytes.
//
// Mirrors the stereo branch of libopus silk/dec_API.c silk_Decode (mid/side
// channel decode + MS->LR unmix in silk/stereo_MS_to_LR.c).
func FuzzSILKDecodeStereoNeverPanics(f *testing.F) {
	addSILKDecodeSeeds(f)

	f.Fuzz(func(t *testing.T, data []byte, bwSel, fsSel uint8, vad bool) {
		if len(data) > 8192 {
			data = data[:8192]
		}
		bw := silkDecodeFuzzBandwidths[int(bwSel)%len(silkDecodeFuzzBandwidths)]
		fs := silkDecodeFuzzFrameSizes[int(fsSel)%len(silkDecodeFuzzFrameSizes)]

		d := NewDecoder()
		if out, err := d.DecodeStereo(data, bw, fs, vad); err == nil {
			requireFiniteSILKPCM(t, out)
		}

		d2 := NewDecoder()
		if out, err := d2.DecodeStereoToMono(data, bw, fs, vad); err == nil {
			requireFiniteSILKPCM(t, out)
		}

		d3 := NewDecoder()
		if out, err := d3.DecodeMonoToStereo(data, bw, fs, vad, false); err == nil {
			requireFiniteSILKPCM(t, out)
		}
	})
}

// FuzzSILKDecodeFECNeverPanics drives the LBRR/FEC recovery path
// (Decoder.DecodeFEC and HasLBRR) with malformed bytes. FEC decoding reads the
// LBRR flag header then either decodes a redundant frame or runs concealment,
// so out-of-range gains / pitch / NLSF indices in the LBRR payload must stay
// bounded.
//
// Mirrors libopus silk/dec_API.c silk_Decode with lostFlag=FLAG_DECODE_LBRR
// (LBRR frame decode in silk/decode_frame.c).
func FuzzSILKDecodeFECNeverPanics(f *testing.F) {
	addSILKDecodeSeeds(f)

	f.Fuzz(func(t *testing.T, data []byte, bwSel, fsSel uint8, stereo bool) {
		if len(data) > 8192 {
			data = data[:8192]
		}
		bw := silkDecodeFuzzBandwidths[int(bwSel)%len(silkDecodeFuzzBandwidths)]
		fs := silkDecodeFuzzFrameSizes[int(fsSel)%len(silkDecodeFuzzFrameSizes)]
		channels := 1
		if stereo {
			channels = 2
		}

		d := NewDecoder()
		// HasLBRR parses the same header and must never panic.
		_ = d.HasLBRR(data, bw, fs)

		if out, err := d.DecodeFEC(data, bw, fs, stereo, false, channels); err == nil {
			requireFiniteSILKPCM(t, out)
		}
	})
}

// FuzzSILKDecodeStereoEncodedNeverPanics drives the package-level
// DecodeStereoEncoded helper, which is the convenience wrapper used to decode a
// standalone SILK stereo bitstream.
func FuzzSILKDecodeStereoEncodedNeverPanics(f *testing.F) {
	addSILKDecodeSeeds(f)

	f.Fuzz(func(t *testing.T, data []byte, bwSel, fsSel uint8, _ bool) {
		if len(data) > 8192 {
			data = data[:8192]
		}
		bw := silkDecodeFuzzBandwidths[int(bwSel)%len(silkDecodeFuzzBandwidths)]

		left, right, err := DecodeStereoEncoded(data, bw)
		if err == nil {
			requireFiniteSILKPCM(t, left)
			requireFiniteSILKPCM(t, right)
		}
	})
}

// FuzzSILKDecodeFrameRawNeverPanics drives the lower-level frame entry points
// that operate directly on a caller-provided range decoder over the full
// duration matrix. These bypass the API-rate frame-size mapping, so they
// exercise the native-rate decode core (silk_decode_core, NLSF2A, LTP/LPC
// synthesis) against malformed range-coded bits more directly.
//
// Mirrors libopus silk/decode_frame.c silk_decode_frame for every supported
// (bandwidth, nb_subfr) combination.
func FuzzSILKDecodeFrameRawNeverPanics(f *testing.F) {
	addSILKDecodeSeeds(f)

	durations := []FrameDuration{Frame10ms, Frame20ms, Frame40ms, Frame60ms, FrameDuration(99)}

	f.Fuzz(func(t *testing.T, data []byte, bwSel, durSel uint8, vad bool) {
		if len(data) == 0 {
			data = []byte{0}
		}
		if len(data) > 8192 {
			data = data[:8192]
		}
		bw := silkDecodeFuzzBandwidths[int(bwSel)%len(silkDecodeFuzzBandwidths)]
		dur := durations[int(durSel)%len(durations)]

		// Mono frame, with-delay variant.
		func() {
			d := NewDecoder()
			var rd rangecoding.Decoder
			rd.Init(data)
			if out, err := d.DecodeFrame(&rd, bw, dur, vad); err == nil {
				requireFiniteSILKPCM(t, out)
			}
		}()

		// Mono frame, raw (no-delay) variant.
		func() {
			d := NewDecoder()
			var rd rangecoding.Decoder
			rd.Init(data)
			if out, err := d.DecodeFrameRaw(&rd, bw, dur, vad); err == nil {
				requireFiniteSILKPCM(t, out)
			}
		}()

		// Stereo frame.
		func() {
			d := NewDecoder()
			var rd rangecoding.Decoder
			rd.Init(data)
			if l, r, err := d.DecodeStereoFrame(&rd, bw, dur, vad); err == nil {
				requireFiniteSILKPCM(t, l)
				requireFiniteSILKPCM(t, r)
			}
		}()
	})
}

// FuzzSILKDecodeStatefulSequence drives a sequence of decode calls on a single
// persistent decoder, interleaving good-frame decode attempts, loss
// concealment, FEC recovery and bandwidth switches. Cross-frame state
// (prevNLSF, gains, pitch, stereo predictors, PLC history) is the area most
// likely to hide an out-of-bounds on malformed input, so this exercises it.
func FuzzSILKDecodeStatefulSequence(f *testing.F) {
	f.Add([]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}, uint8(0xb1))
	f.Add(make([]byte, 200), uint8(0x00))
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, uint8(0xff))

	f.Fuzz(func(t *testing.T, data []byte, ops uint8) {
		if len(data) > 4096 {
			data = data[:4096]
		}
		d := NewDecoder()

		// Derive up to 8 operations from the bits of ops; each step picks a
		// bandwidth and an action so the fuzzer can build hostile sequences.
		chunk := len(data) / 8
		if chunk == 0 {
			chunk = len(data)
		}
		for step := range 8 {
			bw := silkDecodeFuzzBandwidths[step%3] // NB/MB/WB only (valid set)
			var frame []byte
			if chunk > 0 {
				start := (step * chunk) % (len(data) + 1)
				end := min(start+chunk, len(data))
				if start <= end {
					frame = data[start:end]
				}
			}
			action := (ops >> uint(step)) & 1
			switch action {
			case 0:
				if out, err := d.Decode(frame, bw, 960, true); err == nil {
					requireFiniteSILKPCM(t, out)
				}
			default:
				if out, err := d.Decode(nil, bw, 960, true); err == nil {
					requireFiniteSILKPCM(t, out)
				}
			}
		}
	})
}

func TestDecodeFrame_LongFrameSubBlocks(t *testing.T) {
	// Verify sub-block count calculations
	if got := getSubBlockCount(Frame40ms); got != 2 {
		t.Errorf("40ms: got %d sub-blocks, want 2", got)
	}
	if got := getSubBlockCount(Frame60ms); got != 3 {
		t.Errorf("60ms: got %d sub-blocks, want 3", got)
	}

	// Verify subframe counts
	if got := getSubframeCount(Frame40ms); got != 8 {
		t.Errorf("40ms: got %d subframes, want 8", got)
	}
	if got := getSubframeCount(Frame60ms); got != 12 {
		t.Errorf("60ms: got %d subframes, want 12", got)
	}

	// Verify is40or60ms helper
	if !is40or60ms(Frame40ms) {
		t.Error("is40or60ms(Frame40ms) should be true")
	}
	if !is40or60ms(Frame60ms) {
		t.Error("is40or60ms(Frame60ms) should be true")
	}
	if is40or60ms(Frame10ms) {
		t.Error("is40or60ms(Frame10ms) should be false")
	}
	if is40or60ms(Frame20ms) {
		t.Error("is40or60ms(Frame20ms) should be false")
	}
}

// TestDecodeFrame_OutputSizes verifies output sample counts for all
// bandwidth/duration combinations.
func TestDecodeFrame_OutputSizes(t *testing.T) {
	testCases := []struct {
		bandwidth   Bandwidth
		duration    FrameDuration
		wantSamples int
	}{
		// 10ms frames
		{BandwidthNarrowband, Frame10ms, 80},  // 10ms * 8000 / 1000
		{BandwidthMediumband, Frame10ms, 120}, // 10ms * 12000 / 1000
		{BandwidthWideband, Frame10ms, 160},   // 10ms * 16000 / 1000

		// 20ms frames
		{BandwidthNarrowband, Frame20ms, 160}, // 20ms * 8000 / 1000
		{BandwidthMediumband, Frame20ms, 240}, // 20ms * 12000 / 1000
		{BandwidthWideband, Frame20ms, 320},   // 20ms * 16000 / 1000

		// 40ms frames (2 x 20ms sub-blocks)
		{BandwidthNarrowband, Frame40ms, 320}, // 40ms * 8000 / 1000
		{BandwidthMediumband, Frame40ms, 480}, // 40ms * 12000 / 1000
		{BandwidthWideband, Frame40ms, 640},   // 40ms * 16000 / 1000

		// 60ms frames (3 x 20ms sub-blocks)
		{BandwidthNarrowband, Frame60ms, 480}, // 60ms * 8000 / 1000
		{BandwidthMediumband, Frame60ms, 720}, // 60ms * 12000 / 1000
		{BandwidthWideband, Frame60ms, 960},   // 60ms * 16000 / 1000
	}

	for _, tc := range testCases {
		// Verify getFrameSamples helper
		got := getFrameSamples(tc.duration, tc.bandwidth)
		if got != tc.wantSamples {
			t.Errorf("getFrameSamples(%dms, %v) = %d, want %d",
				tc.duration, tc.bandwidth, got, tc.wantSamples)
		}

		// Verify via subframe calculation
		config := GetBandwidthConfig(tc.bandwidth)
		numSubframes := getSubframeCount(tc.duration)
		calculated := numSubframes * config.SubframeSamples
		if calculated != tc.wantSamples {
			t.Errorf("subframe calc(%dms, %v) = %d, want %d",
				tc.duration, tc.bandwidth, calculated, tc.wantSamples)
		}
	}
}

// ============================================================================
// 40ms frame decode tests
// ============================================================================

// createMockRangeDecoder creates a range decoder with some entropy-coded data.
// The data may not produce valid SILK output but exercises the code path.
func createMockRangeDecoder() *rangecoding.Decoder {
	// Create decoder with some deterministic pattern
	data := make([]byte, 100)
	for i := range data {
		data[i] = byte(i * 7)
	}
	rd := &rangecoding.Decoder{}
	rd.Init(data)
	return rd
}

func TestDecodeFrame_40ms(t *testing.T) {
	testCases := []struct {
		bandwidth   Bandwidth
		wantSamples int // at native SILK rate
	}{
		{BandwidthNarrowband, 320}, // 40ms * 8000 / 1000
		{BandwidthMediumband, 480}, // 40ms * 12000 / 1000
		{BandwidthWideband, 640},   // 40ms * 16000 / 1000
	}

	for _, tc := range testCases {
		t.Run(tc.bandwidth.String(), func(t *testing.T) {
			d := NewDecoder()
			rd := createMockRangeDecoder()

			samples, err := d.DecodeFrame(rd, tc.bandwidth, Frame40ms, true)
			if err != nil {
				// Expected to fail due to range decoder exhaustion with mock data.
				// The important thing is the function accepts 40ms frames.
				t.Logf("Expected decode error with mock data: %v", err)
				return
			}

			if len(samples) != tc.wantSamples {
				t.Errorf("got %d samples, want %d", len(samples), tc.wantSamples)
			}
		})
	}
}

// ============================================================================
// 60ms frame decode tests
// ============================================================================

func TestDecodeFrame_60ms(t *testing.T) {
	testCases := []struct {
		bandwidth   Bandwidth
		wantSamples int
	}{
		{BandwidthNarrowband, 480}, // 60ms * 8000 / 1000
		{BandwidthMediumband, 720}, // 60ms * 12000 / 1000
		{BandwidthWideband, 960},   // 60ms * 16000 / 1000
	}

	for _, tc := range testCases {
		t.Run(tc.bandwidth.String(), func(t *testing.T) {
			d := NewDecoder()
			rd := createMockRangeDecoder()

			samples, err := d.DecodeFrame(rd, tc.bandwidth, Frame60ms, true)
			if err != nil {
				// Expected to fail due to range decoder exhaustion with mock data.
				t.Logf("Expected decode error with mock data: %v", err)
				return
			}

			if len(samples) != tc.wantSamples {
				t.Errorf("got %d samples, want %d", len(samples), tc.wantSamples)
			}
		})
	}
}

// ============================================================================
// Stereo long frame tests
// ============================================================================

func TestDecodeStereoFrame_LongFrames(t *testing.T) {
	testCases := []struct {
		duration    FrameDuration
		wantSamples int // per channel at WB
	}{
		{Frame40ms, 640},
		{Frame60ms, 960},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%dms", tc.duration), func(t *testing.T) {
			d := NewDecoder()
			rd := createMockRangeDecoder()

			left, right, err := d.DecodeStereoFrame(rd, BandwidthWideband, tc.duration, true)
			if err != nil {
				// Expected to fail due to range decoder exhaustion with mock data.
				t.Logf("Expected decode error with mock data: %v", err)
				return
			}

			if len(left) != tc.wantSamples {
				t.Errorf("left: got %d samples, want %d", len(left), tc.wantSamples)
			}
			if len(right) != tc.wantSamples {
				t.Errorf("right: got %d samples, want %d", len(right), tc.wantSamples)
			}
		})
	}
}

func TestResetSideChannel_MonoToStereoBitstreamResetMatchesLibopus(t *testing.T) {
	d := NewDecoder()

	d.state[1].lagPrev = 42
	d.state[1].lastGainIndex = 7
	d.state[1].prevSignalType = 3
	d.state[1].firstFrameAfterReset = false
	d.stereo.predPrevQ13 = [2]int16{123, -456}
	d.stereo.sMid = [2]int16{11, 22}
	d.stereo.sSide = [2]int16{33, 44}

	d.ResetSideChannel()

	if got, want := d.state[1].prevGainQ16, int32(1<<16); got != want {
		t.Fatalf("prevGainQ16 = %d, want %d", got, want)
	}
	if !d.state[1].firstFrameAfterReset {
		t.Fatal("side decoder should be marked firstFrameAfterReset")
	}
	if d.state[1].lagPrev != 0 {
		t.Fatalf("lagPrev = %d, want 0 after side decoder re-init", d.state[1].lagPrev)
	}
	if d.state[1].lastGainIndex != 0 {
		t.Fatalf("lastGainIndex = %d, want 0 after side decoder re-init", d.state[1].lastGainIndex)
	}
	if d.stereo.predPrevQ13 != [2]int16{} {
		t.Fatalf("predPrevQ13 = %v, want cleared", d.stereo.predPrevQ13)
	}
	if d.stereo.sSide != [2]int16{} {
		t.Fatalf("sSide = %v, want cleared", d.stereo.sSide)
	}
	if got, want := d.stereo.sMid, [2]int16{11, 22}; got != want {
		t.Fatalf("sMid = %v, want %v preserved", got, want)
	}
}

func TestShouldUseStereoToMonoHistory_MatchesLibopusGate(t *testing.T) {
	d := NewDecoder()

	if d.ShouldUseStereoToMonoHistory(BandwidthWideband, false) {
		t.Fatal("history gate should be false without a previous stereo packet")
	}

	d.state[0].fsKHz = 12
	if d.ShouldUseStereoToMonoHistory(BandwidthWideband, true) {
		t.Fatal("history gate should be false when internal sample rate changed")
	}

	d.state[0].fsKHz = 16
	if !d.ShouldUseStereoToMonoHistory(BandwidthWideband, true) {
		t.Fatal("history gate should be true when previous stereo history matches current internal rate")
	}
}
